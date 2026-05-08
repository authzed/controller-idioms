//go:build dsk

package dsk

import (
	"context"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
)

// TB is the subset of testing.TB that DSK requires. *testing.T, *testing.B,
// and *rapid.T all satisfy it. The full testing.TB interface is intentionally
// not required so that property-testing libraries whose T types implement only
// a narrower set of methods (e.g. pgregory.net/rapid) work without adaptation.
type TB interface {
	Context() context.Context
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
}

// helperMarker is an optional extension of TB. *testing.T implements it;
// rapid.T does not. DSK calls Helper() when available so that test failures
// are attributed to the call site rather than inside DSK itself.
type helperMarker interface {
	Helper()
}

// PendingEvent pairs a watch.Event with its destination.
// The dest, gvr, and id fields are unexported; callers access the event via the embedded watch.Event.
// PendingEvent values from Buffer() are valid inputs to Flush().
type PendingEvent struct {
	watch.Event
	dest eventSink
	gvr  schema.GroupVersionResource
	id   uint64
}

// DSK is the simulation coordinator. Create one per test scenario with New().
type DSK struct {
	tb     TB
	engine *engine
	faults *faultSet
}

// helper marks the calling DSK method as a test helper when the underlying TB
// supports it (i.e. when it implements helperMarker). This makes test failure
// output point to the caller of the DSK method rather than inside DSK itself.
func (d *DSK) helper() {
	if h, ok := d.tb.(helperMarker); ok {
		h.Helper()
	}
}

// New creates a new DSK instance bound to the given test.
//
// tb is required: DSK uses tb.Context() for lifecycle management and tb.Fatal
// for error reporting, making the API error-free at the call site. This makes
// DSK intentionally test-only; there is no production-use path.
//
// tb must implement the DSK TB interface: Context(), Fatal(), and Fatalf().
// *testing.T, *testing.B, and *rapid.T (pgregory.net/rapid) all qualify.
//
// For controllers that cannot use WrapQueue (external or legacy code without
// access to the workqueue), cooperative tick completion is not currently
// supported. Use WrapQueue where possible; it is the only supported completion
// mechanism.
func New(tb TB, opts ...Option) *DSK {
	// Initialize tb.Context() and eagerly access its Done channel before any
	// testing/synctest bubble is entered. context.cancelCtx.Done() is lazily
	// initialized: the first call creates the underlying chan struct{}. If this
	// happens inside a synctest bubble, the channel becomes a "synctest channel"
	// and the runtime panics when it is later closed by the test cleanup from
	// outside the bubble. Calling Done() here ensures the channel is allocated
	// on the real heap before the bubble starts.
	_ = tb.Context().Done()
	eng := newEngine()
	eng.tb = tb
	d := &DSK{
		tb:     tb,
		engine: eng,
		faults: newFaultSet(),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Option configures a DSK instance.
type Option func(*DSK)

// WithFaults sets initial faults on the DSK instance.
func WithFaults(faults ...Fault) Option {
	return func(d *DSK) {
		for _, f := range faults {
			d.faults.add(f)
		}
	}
}

// Dynamic returns a client that buffers watch events until Flush is called.
// Supports fake clients (installs reactors). For real HTTP clients use DynamicFromConfig.
func (d *DSK) Dynamic(underlying dynamic.Interface) dynamic.Interface {
	if r, ok := underlying.(reactable); ok {
		installReactors(d.tb.Context(), r, d.engine, d.faults, d.tb)
		return wrapApply(underlying, d.engine, r.Tracker(), d.faults)
	}
	d.tb.Fatalf("dsk: unsupported dynamic.Interface; for real clients use d.DynamicFromConfig(cfg)")
	return nil
}

// DynamicFromConfig creates a dynamic client from cfg with DSK watch buffering
// and write RV tracking. Use this for real Kubernetes clusters (envtest, kind).
// For fake clients, use Dynamic(fake).
func (d *DSK) DynamicFromConfig(cfg *rest.Config) (dynamic.Interface, error) {
	cfgCopy := rest.CopyConfig(cfg)
	cfgCopy.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &dskTransport{underlying: rt, engine: d.engine, faults: d.faults, tb: d.tb}
	})
	return dynamic.NewForConfig(cfgCopy)
}

// Typed instruments a kubernetes.Interface for DSK watch buffering and write
// RV tracking.
//
// For fake clients (satisfying the reactable interface, e.g. *k8sfake.Clientset),
// a watch reactor and write observer are installed on the existing client.
// The original value is returned with reactors installed. No wrapper is needed
// because k8sfake.Clientset does not bypass reactors for Apply/ApplyStatus.
//
// For real kubernetes clients, use TypedFromConfig instead.
func (d *DSK) Typed(underlying kubernetes.Interface) kubernetes.Interface {
	if r, ok := underlying.(reactable); ok {
		installReactors(d.tb.Context(), r, d.engine, d.faults, d.tb)
		return underlying
	}
	d.tb.Fatalf("dsk: unsupported kubernetes.Interface; for real clients use d.TypedFromConfig(cfg)")
	return nil
}

// TypedFromConfig creates a typed Kubernetes client from cfg with DSK watch
// buffering and write RV tracking. Use this for real Kubernetes clusters
// (envtest, kind). For fake clients, use Typed(fake).
func (d *DSK) TypedFromConfig(cfg *rest.Config) (kubernetes.Interface, error) {
	cfgCopy := rest.CopyConfig(cfg)
	cfgCopy.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &dskTransport{underlying: rt, engine: d.engine, faults: d.faults, tb: d.tb}
	})
	return kubernetes.NewForConfig(cfgCopy)
}

// WrapQueue wraps a work queue for cooperative tick completion.
// The returned queue is a drop-in replacement for the original.
// Register all controller queues before calling Tick or Flush.
func (d *DSK) WrapQueue(q workqueue.RateLimitingInterface) workqueue.RateLimitingInterface {
	wq := newWrappedQueue(q)
	d.engine.registerQueue(wq)
	return wq
}

// WrapTypedQueue wraps a generic typed work queue for cooperative tick completion.
// Use this instead of WrapQueue when the controller uses
// workqueue.TypedRateLimitingInterface[T] directly (e.g. TypedRateLimitingInterface[string]).
// The returned queue is a drop-in replacement for the original.
//
// Note: WrapTypedQueue is a package-level function (not a method on *DSK) because
// Go does not currently support generic methods. This will be addressable once
// generic methods land in a future Go version; at that point WrapTypedQueue will
// become d.WrapTypedQueue(q).
func WrapTypedQueue[T comparable](d *DSK, q workqueue.TypedRateLimitingInterface[T]) workqueue.TypedRateLimitingInterface[T] {
	wq := newTypedWrappedQueue(q)
	d.engine.registerQueue(wq)
	return wq
}

// WaitForEvents blocks until at least n events are present in the buffer, or
// the test context is cancelled. Unlike Buffer, it does not require a preceding
// write operation to have set up pendingRVs tracking.
//
// WaitForEvents uses sync.Cond.Wait internally, which is durably blocking for
// testing/synctest. This makes it safe to call from within a synctest bubble
// when waiting for events driven by external I/O (e.g. HTTP watch responses)
// that would otherwise prevent synctest.Wait from returning.
func (d *DSK) WaitForEvents(n int) {
	d.helper()
	if err := d.engine.waitForBufferCount(d.tb.Context(), n); err != nil {
		d.tb.Fatal(err)
	}
}

// Buffer waits for any write-triggered watch events to arrive, then returns the
// full set of buffered events pending delivery. Returns plain []PendingEvent for
// easy filtering and use with property testing libraries such as pgregory.net/rapid.
func (d *DSK) Buffer() []PendingEvent {
	d.helper()
	if err := d.engine.waitForPendingRVs(d.tb.Context()); err != nil {
		d.tb.Fatal(err)
	}
	raw := d.engine.snapshot()
	out := make([]PendingEvent, len(raw))
	for i, pe := range raw {
		out[i] = PendingEvent{Event: pe.event, dest: pe.dest, gvr: pe.gvr, id: pe.id}
	}
	return out
}

// Flush delivers the given events to their watch streams and waits for all
// triggered reconciles to complete before returning.
//
// events must be values returned by a prior call to Buffer(). Do not modify
// the Event.Object field of returned PendingEvent values — removal matching
// relies on object pointer identity, and re-constructing events bypasses that.
//
// Use Flush to control which events are delivered and in what order.
// For simple cases, use Tick which flushes all buffered events.
func (d *DSK) Flush(events []PendingEvent) {
	d.helper()
	if err := d.flush(d.tb.Context(), events); err != nil {
		d.tb.Fatal(err)
	}
}

func (d *DSK) flush(ctx context.Context, events []PendingEvent) error {
	if len(events) > 0 && !d.engine.hasQueues() {
		d.tb.Logf("dsk: Flush called with %d event(s) but no queues are registered via WrapQueue or WrapTypedQueue — controller reconciles will not be tracked and Flush will return immediately after delivering events", len(events))
	}
	raw := make([]pendingEvent, len(events))
	for i, pe := range events {
		if pe.dest == nil || pe.id == 0 {
			return fmt.Errorf("dsk: Flush received a PendingEvent not returned by Buffer()")
		}
		raw[i] = pendingEvent{event: pe.Event, dest: pe.dest, gvr: pe.gvr, id: pe.id}
	}
	d.engine.removeFromBuffer(raw)
	return d.engine.flush(ctx, raw)
}

// Tick flushes all currently buffered watch events and waits for all triggered
// reconciles to complete. Equivalent to Flush(d.Buffer()).
func (d *DSK) Tick() {
	d.helper()
	if err := d.tick(d.tb.Context()); err != nil {
		d.tb.Fatal(err)
	}
}

func (d *DSK) tick(ctx context.Context) error {
	if err := d.engine.waitForPendingRVs(ctx); err != nil {
		return err
	}
	raw := d.engine.snapshot()
	events := make([]PendingEvent, len(raw))
	for i, pe := range raw {
		events[i] = PendingEvent{Event: pe.event, dest: pe.dest, gvr: pe.gvr, id: pe.id}
	}
	return d.flush(ctx, events)
}

// Ticks calls Tick n times.
func (d *DSK) Ticks(n int) {
	d.helper()
	for range n {
		d.Tick()
	}
}

// TickUntilSteady ticks up to n times, returning after the first tick where
// IsSteady is true. Calls tb.Fatal if steady state is not reached within n ticks.
//
// After each tick, TickUntilSteady waits for all registered queues to fully
// drain before checking IsSteady. This handles items requeued within a tick
// (e.g. via Add in a handler, or via AddAfter/Done dirty path): the second
// processing round completes before the steady-state check, so a controller
// that requeues once converges in a single tick rather than requiring n>1.
// Queues that had no activity (no Add or Get calls) during the tick are not
// waited on, so TickUntilSteady still exhausts its tick budget when the queue
// has stuck items that are never processed.
func (d *DSK) TickUntilSteady(n int) {
	d.helper()
	if n <= 0 {
		d.tb.Fatalf("dsk: TickUntilSteady requires n > 0, got %d", n)
		return
	}
	for range n {
		d.Tick()
		if err := d.engine.waitForQueuesIdle(d.tb.Context()); err != nil {
			d.tb.Fatal(err)
			return
		}
		if d.IsSteady() {
			return
		}
	}
	d.tb.Fatalf("controller did not reach steady state after %d ticks", n)
}

// IsSteady returns true when the event buffer is empty and all registered
// queue wrappers have no pending or in-flight keys.
//
// Rate-limiter delay caveat: items requeued via AddRateLimited or AddAfter with
// a non-zero duration are held in the rate-limiter's delay heap and are not
// visible to queue.Len(). IsSteady may return a transient false positive until
// those items clear the delay and enter the queue. Running the test inside a
// testing/synctest bubble makes time virtual: calling synctest.Wait() advances
// time past all pending delays, so AddRateLimited/AddAfter items surface
// immediately and IsSteady reflects true quiescence.
func (d *DSK) IsSteady() bool {
	return d.engine.isSteady()
}

// InjectFault adds a fault to the DSK instance. Returns a handle for removal.
func (d *DSK) InjectFault(f Fault) FaultHandle {
	return d.faults.add(f)
}

// RemoveFault removes a previously injected fault.
func (d *DSK) RemoveFault(h FaultHandle) {
	d.faults.remove(h)
}

// ClearFaults removes all active faults.
func (d *DSK) ClearFaults() {
	d.faults.clear()
}
