//go:build dsk

package dsk

// Lock ordering (all code in this package must obey this to avoid deadlocks):
//
//	engine.mu → wrappedQueue.mu → workqueue.cond.L

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// eventSink receives a watch event during Flush. Implemented by ControlledWatch
// (fake/channel path) and rawFrameSink (transport/HTTP path).
type eventSink interface {
	deliver(watch.Event)
}

// pendingEvent is an internal type pairing a watch event with its destination.
// The dest field carries routing info for Flush; it is not exposed to callers.
type pendingEvent struct {
	event watch.Event
	dest  eventSink
	gvr   schema.GroupVersionResource
	id    uint64 // unique ID assigned in addToBuffer; used for identity in removeFromBuffer
}

// gvrNSKey identifies a watch or handler registration by GVR and namespace.
// ns == "" means the watch covers all namespaces (cluster-scoped or all-namespace).
type gvrNSKey struct {
	gvr schema.GroupVersionResource
	ns  string
}

// watchEntry records one active watch drain goroutine along with its label selector.
// sel is the label selector the API server uses to filter events for this watch.
// A nil selector means match-all, which is correct for the fake client path where
// the tracker sends all events to all watchers regardless of label selectors.
type watchEntry struct {
	gvrNSKey
	sel labels.Selector
}

// handlerEntry records one AddEventHandler registration on an informer.
// sel is the informer's label selector; nil means match-all.
// For fake clients, sel is always nil because the fake tracker sends all events
// to all watchers regardless of label selectors, so all handlers are always called.
type handlerEntry struct {
	gvrNSKey
	sel labels.Selector
}

// engine is the shared tick coordination state between client wrappers and queue wrappers.
// All fields are guarded by mu. pendingCond is broadcast whenever pendingRVs or
// pendingAnyEvent transitions to empty so waitForPendingRVs wakes promptly.
type engine struct {
	tb               TB // optional; used for diagnostic logging only
	mu               sync.Mutex
	pendingCond      *sync.Cond
	buffer           []pendingEvent
	queues           []tickableQueue
	watchEntries     []watchEntry                        // active drain goroutines with their label selectors
	pendingRVs       map[string]int                      // RV → remaining event arrivals expected
	pendingAnyEvent  map[schema.GroupVersionResource]int // GVR → events expected (RV-agnostic)
	inFlightHandlers int64                               // accessed atomically; counts in-progress AddEventHandler callbacks
	handlerEntries   []handlerEntry                      // AddEventHandler registrations with their label selectors
	pendingCalls     int64                               // accessed atomically; credited per expected handler call, debited on completion
	nextEventID      uint64                              // accessed atomically; monotonically increasing IDs for buffered events
}

func newEngine() *engine {
	e := &engine{
		pendingRVs:      make(map[string]int),
		pendingAnyEvent: make(map[schema.GroupVersionResource]int),
	}
	e.pendingCond = sync.NewCond(&e.mu)
	return e
}

// selectorString returns a canonical string for a label selector for use in
// equality comparisons. Nil and labels.Everything() both return "" (match-all).
// labels.Nothing() returns "<nothing>" to avoid colliding with the match-all case.
// Requirements are sorted so that logically equivalent selectors with different
// requirement orderings produce the same string.
func selectorString(sel labels.Selector) string {
	if sel == nil {
		return ""
	}
	reqs, selectable := sel.Requirements()
	if !selectable {
		return "<nothing>"
	}
	strs := make([]string, len(reqs))
	for i := range reqs {
		strs[i] = reqs[i].String()
	}
	sort.Strings(strs)
	return strings.Join(strs, ",")
}

// handlerStart records that an AddEventHandler callback has begun executing.
// Must be paired with a handlerDone call.
func (e *engine) handlerStart() {
	atomic.AddInt64(&e.inFlightHandlers, 1)
}

// handlerDone records that an AddEventHandler callback has finished. When the
// count reaches zero it broadcasts on pendingCond so waitForHandlersIdle wakes.
// Acquires e.mu briefly to avoid a lost-wakeup race with waitForHandlersIdle.
func (e *engine) handlerDone() {
	if atomic.AddInt64(&e.inFlightHandlers, -1) == 0 {
		e.mu.Lock()
		e.pendingCond.Broadcast()
		e.mu.Unlock()
	}
}

// handlerDoneCall decrements pendingCalls by one when an AddEventHandler
// callback completes. It is a saturating decrement: if pendingCalls is already
// zero (e.g. for initial-list callbacks that fired before any tick), it is a
// no-op. Broadcasts on pendingCond when the count reaches zero so that
// waitForHandlersIdle can detect completion.
func (e *engine) handlerDoneCall() {
	for {
		old := atomic.LoadInt64(&e.pendingCalls)
		if old <= 0 {
			return // no outstanding credits — initial-list or spurious call
		}
		if atomic.CompareAndSwapInt64(&e.pendingCalls, old, old-1) {
			if old == 1 {
				e.mu.Lock()
				e.pendingCond.Broadcast()
				e.mu.Unlock()
			}
			return
		}
	}
}

// registerHandler records that an AddEventHandler call has been made on an
// informer watching (gvr, ns) with the given label selector. sel nil means
// match-all (used for fake clients where the tracker does not filter events).
// This count is used by flush() to credit pendingCalls before delivering events,
// so waitForHandlersIdle can block until all handler invocations triggered by
// those events have completed.
func (e *engine) registerHandler(gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlerEntries = append(e.handlerEntries, handlerEntry{
		gvrNSKey: gvrNSKey{gvr: gvr, ns: ns},
		sel:      sel,
	})
}

// unregisterHandler removes the first handler entry matching (gvr, ns, sel).
// Called by dskSharedIndexInformer.RemoveEventHandler to keep handlerEntries
// in sync with the underlying informer's listener list. Callers must pass the
// same sel value that was passed to registerHandler.
func (e *engine) unregisterHandler(gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	selStr := selectorString(sel)
	for i, he := range e.handlerEntries {
		if he.gvr == gvr && he.ns == ns && selectorString(he.sel) == selStr {
			e.handlerEntries = append(e.handlerEntries[:i], e.handlerEntries[i+1:]...)
			return
		}
	}
	if e.tb != nil {
		e.tb.Logf("dsk: unregisterHandler: no matching entry for gvr=%v ns=%s sel=%s — selector mismatch between registerHandler and unregisterHandler can cause Tick/Flush to hang", gvr, ns, selectorString(sel))
	}
}

// wakeOnCancel starts a goroutine that broadcasts on pendingCond when ctx is
// cancelled, so that callers blocked in pendingCond.Wait() can re-check
// ctx.Err() and return. The returned stop function must be called (typically
// via defer) to clean up the goroutine when the wait completes normally.
//
// Broadcasting without holding e.mu is safe: the waiter re-checks ctx.Err()
// immediately after waking, so a missed broadcast only causes one extra loop
// iteration rather than a lost cancellation.
func (e *engine) wakeOnCancel(ctx context.Context) (stop func()) {
	if ctx.Err() != nil {
		e.pendingCond.Broadcast()
		return func() {}
	}
	ch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			e.pendingCond.Broadcast()
		case <-ch:
		}
	}()
	return func() { close(ch) }
}

// waitForHandlersIdle blocks until all in-flight AddEventHandler callbacks have
// returned AND all expected handler calls (credited via pendingCalls) have
// completed, or ctx is cancelled. Called by flush() after delivering events and
// before closing the tick window.
func (e *engine) waitForHandlersIdle(ctx context.Context) error {
	defer e.wakeOnCancel(ctx)()

	e.mu.Lock()
	defer e.mu.Unlock()
	for atomic.LoadInt64(&e.inFlightHandlers) > 0 || atomic.LoadInt64(&e.pendingCalls) > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.pendingCond.Wait()
	}
	return ctx.Err()
}

// registerWatch records that a drain goroutine has started for the given GVR,
// namespace, and label selector. ns == "" means the watch covers all namespaces.
// sel nil means match-all (correct for the fake client path; also used when no
// labelSelector query parameter was present on the watch request).
func (e *engine) registerWatch(gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.watchEntries = append(e.watchEntries, watchEntry{
		gvrNSKey: gvrNSKey{gvr: gvr, ns: ns},
		sel:      sel,
	})
}

// unregisterWatch records that a drain goroutine has exited for the given GVR,
// namespace, and label selector. Removes the first matching entry; the caller
// must pass the same sel value that was passed to registerWatch.
func (e *engine) unregisterWatch(gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	selStr := selectorString(sel)
	for i, we := range e.watchEntries {
		if we.gvr == gvr && we.ns == ns && selectorString(we.sel) == selStr {
			e.watchEntries = append(e.watchEntries[:i], e.watchEntries[i+1:]...)
			return
		}
	}
}

// preExpectWrite atomically increments pendingAnyEvent[gvr] by the number of
// active watches that will receive an event for an object write in objectNS,
// BEFORE the write operation, so that addToBuffer cannot decrement before
// expectRV/upgradeExpect runs. Returns the watch count N that was observed
// (0 if no watches or non-write verb). Callers must pass this N to
// upgradeExpect or cancelPreExpect so those functions remove exactly the count
// that was added, even if watchEntries changes in the interim.
//
// objectLabels is the label set of the object being written. It is used to
// filter watches whose label selector does not match the object, so that DSK
// does not wait for events that the API server will never send. A nil
// objectLabels disables label filtering (used for patch and delete, where the
// final labels are not known at pre-expect time — this may over-count for real
// clusters with selector-filtered watches, but is correct for the common cases).
//
// An all-namespace watcher (ns=="") receives events for every object namespace.
// A namespace-specific watcher (ns=="foo") receives events only for that namespace.
func (e *engine) preExpectWrite(verb string, gvr schema.GroupVersionResource, objectNS string, objectLabels map[string]string) int {
	switch verb {
	case "create", "update", "patch", "delete":
	default:
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	filterByLabels := objectLabels != nil
	var ls labels.Set
	if filterByLabels {
		ls = labels.Set(objectLabels)
	}

	n := 0
	for _, we := range e.watchEntries {
		if we.gvr != gvr {
			continue
		}
		// Namespace matching: all-namespace watcher ("") matches all objects;
		// namespace-specific watcher matches only that namespace.
		if we.ns != "" && we.ns != objectNS {
			continue
		}
		// Selector matching: nil selector (match-all) always counts.
		// Non-nil selector is only checked when objectLabels is known; when
		// unknown (patch/delete), we conservatively count the watch.
		if filterByLabels && we.sel != nil && !we.sel.Matches(ls) {
			continue
		}
		n++
	}
	if n > 0 {
		e.pendingAnyEvent[gvr] += n
	}
	return n
}

// upgradeExpect converts a pre-expected RV-agnostic count to RV-based tracking.
// Call after a successful write when the returned object has a non-empty RV.
// n must be the value returned by the corresponding preExpectWrite call so that
// exactly the count that was pre-incremented is removed, regardless of any
// concurrent watch goroutine exits that may have changed watchCounts[gvr].
func (e *engine) upgradeExpect(gvr schema.GroupVersionResource, rv string, n int) {
	if n <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Remove exactly n from pendingAnyEvent (the count that was pre-incremented).
	if cur := e.pendingAnyEvent[gvr]; cur <= n {
		delete(e.pendingAnyEvent, gvr)
	} else {
		e.pendingAnyEvent[gvr] = cur - n
	}
	// Add to RV-based tracking.
	e.pendingRVs[rv] = n
}

// cancelPreExpect removes a pre-increment added by preExpectWrite when the
// write failed or was not handled by the reactor. n must be the value returned
// by the corresponding preExpectWrite call so that exactly the count that was
// pre-incremented is removed, regardless of any concurrent watch goroutine
// exits that may have changed watchCounts[gvr].
func (e *engine) cancelPreExpect(gvr schema.GroupVersionResource, n int) {
	if n <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur := e.pendingAnyEvent[gvr]; cur <= n {
		delete(e.pendingAnyEvent, gvr)
	} else {
		e.pendingAnyEvent[gvr] = cur - n
	}
	// Broadcast in case we decremented to 0.
	if len(e.pendingRVs)+len(e.pendingAnyEvent) == 0 {
		e.pendingCond.Broadcast()
	}
}

func (e *engine) addToBuffer(pe pendingEvent) {
	pe.id = atomic.AddUint64(&e.nextEventID, 1)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buffer = append(e.buffer, pe)
	// Decrement the arrival count for this RV; remove when all copies have arrived.
	if obj, ok := pe.event.Object.(interface{ GetResourceVersion() string }); ok {
		rv := obj.GetResourceVersion()
		if n := e.pendingRVs[rv]; n <= 1 {
			delete(e.pendingRVs, rv)
		} else {
			e.pendingRVs[rv] = n - 1
		}
	}
	// Decrement the RV-agnostic counter (used when the underlying store does not
	// assign ResourceVersions, e.g. the plain fake object tracker).
	// Note: for writes where upgradeExpect already ran successfully, upgradeExpect
	// will have already zeroed pendingAnyEvent[gvr] and moved the count to
	// pendingRVs. In that case the n > 0 guard below is a no-op for this event;
	// only events from writes with an empty RV (or tracker-fallback writes) will
	// actually decrement pendingAnyEvent here.
	if pe.gvr != (schema.GroupVersionResource{}) {
		if n := e.pendingAnyEvent[pe.gvr]; n > 0 {
			if n <= 1 {
				delete(e.pendingAnyEvent, pe.gvr)
			} else {
				e.pendingAnyEvent[pe.gvr] = n - 1
			}
		}
	}
	// Notify waitForPendingRVs in case all pending counts are now zero.
	if len(e.pendingRVs)+len(e.pendingAnyEvent) == 0 {
		e.pendingCond.Broadcast()
	}
}

// waitForBufferCount blocks until at least n events are in the buffer, or ctx
// is cancelled. sync.Cond.Wait is durably blocking for testing/synctest, so
// this is safe to call from within a synctest bubble.
func (e *engine) waitForBufferCount(ctx context.Context, n int) error {
	defer e.wakeOnCancel(ctx)()
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.buffer) < n {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.pendingCond.Wait()
	}
	return ctx.Err()
}

// waitForPendingRVs blocks until all tracked RVs and RV-agnostic event counts
// have arrived in the buffer, or ctx is cancelled.
func (e *engine) waitForPendingRVs(ctx context.Context) error {
	defer e.wakeOnCancel(ctx)()
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.pendingRVs)+len(e.pendingAnyEvent) > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.pendingCond.Wait()
	}
	return ctx.Err()
}

// snapshot returns a copy of the current buffer contents.
func (e *engine) snapshot() []pendingEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]pendingEvent, len(e.buffer))
	copy(out, e.buffer)
	return out
}

// removeFromBuffer removes the given events from the buffer by identity.
// Identity is defined as (dest pointer, event.Type, event.Object pointer).
// Two pendingEvents are identical if and only if they originated from the same
// addToBuffer call — callers should pass values taken from snapshot() rather
// than constructing new pendingEvent values.
//
// Each entry in flushed consumes exactly one matching buffer entry. This
// prevents over-removal when two buffer entries are structurally equal (same
// dest and same event value) — for example, if the same watch connection
// receives the same event twice — and only one copy was flushed.
func (e *engine) removeFromBuffer(flushed []pendingEvent) {
	if len(flushed) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Build an id→count map so the outer loop is O(n) rather than O(n×m).
	// Counts handle the rare case where the same event ID appears multiple times.
	remove := make(map[uint64]int, len(flushed))
	for _, f := range flushed {
		remove[f.id]++
	}
	remaining := e.buffer[:0:0] // fresh backing array; prevents aliasing
	for _, be := range e.buffer {
		if remove[be.id] > 0 {
			remove[be.id]--
		} else {
			remaining = append(remaining, be)
		}
	}
	e.buffer = remaining
}

func (e *engine) registerQueue(q tickableQueue) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queues = append(e.queues, q)
}

// hasQueues reports whether any queue wrappers have been registered via WrapQueue
// or WrapTypedQueue. Called by DSK.flush to warn when events are flushed without
// any queue tracking in place.
func (e *engine) hasQueues() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queues) > 0
}

// flush delivers the given events to their watch channels, then waits for
// all registered queue wrappers to report tick completion.
//
// Before delivering, flush credits pendingCalls for each event based on the
// number of AddEventHandler registrations for that event's (GVR, namespace).
// This ensures waitForHandlersIdle blocks until all informer callbacks
// triggered by the delivered events have completed, even if the reflector
// goroutine hasn't started processing the event yet when waitForHandlersIdle
// is first called.
func (e *engine) flush(ctx context.Context, events []pendingEvent) error {
	e.mu.Lock()
	queues := make([]tickableQueue, len(e.queues))
	copy(queues, e.queues)

	// Credit pendingCalls before delivering so waitForHandlersIdle has a
	// non-zero count to block on even if the reflector hasn't consumed the
	// event from the watch channel yet.
	//
	// For each event, count only the handlers whose (GVR, namespace, selector)
	// matches the event's object. The event object always carries its current
	// labels, so we can filter precisely. This avoids over-crediting when
	// real-cluster informers have label selectors that exclude the event's object.
	type labelsGetter interface {
		GetNamespace() string
		GetLabels() map[string]string
	}
	for _, pe := range events {
		var objectNS string
		var ls labels.Set
		if meta, ok := pe.event.Object.(labelsGetter); ok {
			objectNS = meta.GetNamespace()
			ls = labels.Set(meta.GetLabels()) // nil map is valid as an empty Set
		}
		n := 0
		for _, he := range e.handlerEntries {
			if he.gvr != pe.gvr {
				continue
			}
			if he.ns != "" && he.ns != objectNS {
				continue
			}
			if he.sel != nil && !he.sel.Matches(ls) {
				continue
			}
			n++
		}
		if n > 0 {
			atomic.AddInt64(&e.pendingCalls, int64(n))
		}
	}
	e.mu.Unlock()

	for _, q := range queues {
		q.beginTick()
	}

	// Deliver events in a background goroutine so that Tick/Flush can wait for
	// handler completion without the caller needing to run a concurrent consumer.
	// The channel is unbuffered: deliver blocks until the consumer (informer
	// reflector) reads. Running delivery off-thread lets Tick return before the
	// consumer has drained all events — this matches DSK's expected usage pattern
	// where the test calls Tick and then reads from ResultChan.
	// Events within a single flush are delivered in order.
	if len(events) > 0 {
		go func() {
			for _, pe := range events {
				pe.dest.deliver(pe.event)
			}
		}()
	}

	// Wait for all AddEventHandler callbacks to return (both in-flight and
	// those yet to start, tracked via pendingCalls) before closing the tick
	// window. This ensures every queue.Add() triggered by delivered events has
	// been made before we check for completion.
	if err := e.waitForHandlersIdle(ctx); err != nil {
		return err
	}

	// Close the Add() window on all queue wrappers.
	for _, q := range queues {
		q.closeWindow()
	}

	// Wait for all queues to finish their tick.
	for _, q := range queues {
		select {
		case <-q.doneCh():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// waitForQueuesIdle waits until all registered queue wrappers report idle
// (Len()==0 and inFlight=={}), or ctx is cancelled. It skips queues that had
// no activity during the last tick (active==false) to avoid hanging when items
// pre-existed in the queue before the tick and were never Get'd.
//
// Called by TickUntilSteady after each Tick() and before IsSteady() so that
// items requeued within a tick (via Add by handlers or via AddAfter/Done dirty
// path) are drained before the steady-state check. Not called from flush()
// directly, which allows tests that drain the queue manually after Tick() to
// proceed without blocking.
func (e *engine) waitForQueuesIdle(ctx context.Context) error {
	e.mu.Lock()
	queues := make([]tickableQueue, len(e.queues))
	copy(queues, e.queues)
	e.mu.Unlock()
	for _, q := range queues {
		if err := q.waitForIdle(ctx); err != nil {
			return err
		}
	}
	return nil
}

// isSteady acquires engine.mu then calls q.isIdle() per the lock ordering above.
//
// Note: Tick/Flush only waits for in-flight items (Get→Done) to complete, not
// for Len() to reach zero. IsSteady() is the authoritative check for full
// quiescence — it verifies both the buffer is empty AND all queues are idle
// (no in-flight items AND Len() == 0).
func (e *engine) isSteady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.buffer) > 0 {
		return false
	}
	for _, q := range e.queues {
		if !q.isIdle() {
			return false
		}
	}
	return true
}
