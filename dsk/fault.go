//go:build dsk

package dsk

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// HoldFault returns a FaultBuilder that blocks matching calls until the
// returned release function is called. This is the deterministic alternative
// to DelayFault for DSK tests: because a fault executes on the controller's
// reconcile goroutine, a time.After delay would prevent the current tick from
// completing (the engine waits for all reconciles via doneCh). HoldFault lets
// the test explicitly control when the blocked call unblocks.
//
// Context cancellation unblocks the hold in the transport path (where the
// per-request context is used). In the reactor (fake client) path, client-go
// does not expose the per-request context through the reactor chain, so only
// DSK-level context cancellation (test end) will unblock a held reactor call.
//
//	hold, release := dsk.HoldFault()
//	d.InjectFault(hold.OnVerb("patch"))
//	// ... trigger the operation ...
//	release() // unblock the blocked API call
func HoldFault() (FaultBuilder, func()) {
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	f := &baseFault{
		effectFn: func(ctx context.Context) error {
			select {
			case <-ch:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	return f, release
}

// Fault is a single injectable error condition.
type Fault interface {
	matches(verb string, gvr schema.GroupVersionResource, namespace, name string) bool
	execute(ctx context.Context) error
}

// FaultHandle is returned by InjectFault; pass to RemoveFault to remove the fault.
type FaultHandle int

// faultEntry pairs a fault with its insertion handle for removal.
type faultEntry struct {
	h FaultHandle
	f Fault
}

// faultSet holds the active faults in insertion order. Methods are safe for
// concurrent use. apply() iterates in reverse (LIFO): the last-injected fault
// fires first when multiple faults match the same call.
type faultSet struct {
	mu     sync.RWMutex
	faults []faultEntry
	next   FaultHandle
}

func newFaultSet() *faultSet {
	return &faultSet{next: 1} // 0 is reserved; a zero FaultHandle is never valid
}

func (fs *faultSet) add(f Fault) FaultHandle {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	h := fs.next
	fs.next++
	fs.faults = append(fs.faults, faultEntry{h: h, f: f})
	return h
}

func (fs *faultSet) remove(h FaultHandle) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := fs.faults[:0:0] // defensive: fresh backing array prevents aliasing with old slice
	for _, e := range fs.faults {
		if e.h != h {
			out = append(out, e)
		}
	}
	fs.faults = out
}

func (fs *faultSet) clear() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.faults = nil
}

// apply iterates faults in reverse insertion order (LIFO) and returns the
// first matching fault's effect, or nil if none match.
func (fs *faultSet) apply(ctx context.Context, verb string, gvr schema.GroupVersionResource, namespace, name string) error {
	fs.mu.RLock()
	var matched Fault
	for i := len(fs.faults) - 1; i >= 0; i-- {
		if fs.faults[i].f.matches(verb, gvr, namespace, name) {
			matched = fs.faults[i].f
			break
		}
	}
	fs.mu.RUnlock()
	if matched != nil {
		return matched.execute(ctx)
	}
	return nil
}

// FaultBuilder constructs a Fault with optional selectors.
type FaultBuilder interface {
	Fault
	OnVerb(verb string) FaultBuilder
	OnResource(gvr schema.GroupVersionResource) FaultBuilder
	OnNamespace(ns string) FaultBuilder
	OnName(name string) FaultBuilder
}

type baseFault struct {
	verb      string // empty = match all
	gvr       schema.GroupVersionResource
	hasGVR    bool
	namespace string // empty = match all
	name      string // empty = match all
	effectFn  func(ctx context.Context) error
}

func (b *baseFault) matches(verb string, gvr schema.GroupVersionResource, namespace, name string) bool {
	if b.verb != "" && b.verb != verb {
		return false
	}
	if b.hasGVR && b.gvr != gvr {
		return false
	}
	if b.namespace != "" && b.namespace != namespace {
		return false
	}
	if b.name != "" && b.name != name {
		return false
	}
	return true
}

func (b *baseFault) execute(ctx context.Context) error {
	return b.effectFn(ctx)
}

func (b *baseFault) OnVerb(verb string) FaultBuilder {
	c := *b
	c.verb = verb
	return &c
}

func (b *baseFault) OnResource(gvr schema.GroupVersionResource) FaultBuilder {
	c := *b
	c.gvr = gvr
	c.hasGVR = true
	return &c
}

func (b *baseFault) OnNamespace(ns string) FaultBuilder {
	c := *b
	c.namespace = ns
	return &c
}

func (b *baseFault) OnName(name string) FaultBuilder {
	c := *b
	c.name = name
	return &c
}

// ErrorFault returns an error for all matching calls.
func ErrorFault(err error) FaultBuilder {
	return &baseFault{
		effectFn: func(_ context.Context) error { return err },
	}
}

// DelayFault adds latency to matching calls before forwarding them.
//
// In DSK tests, DelayFault blocks the current tick from completing because
// the engine waits for all reconcile goroutines to finish via the queue
// wrappers. For deterministic hold-and-release behaviour within a tick,
// use HoldFault instead.
func DelayFault(d time.Duration) FaultBuilder {
	return &baseFault{
		effectFn: func(ctx context.Context) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}
