//go:build dsk

package dsk

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"
)

// tickableQueue is the internal interface the engine uses to coordinate ticks
// across registered queue wrappers. Both wrappedQueue (untyped) and
// typedWrappedQueue[T] (generic) implement it.
type tickableQueue interface {
	beginTick()
	closeWindow()
	doneCh() <-chan struct{}
	isIdle() bool
	waitForIdle(ctx context.Context) error
}

// wrappedQueue wraps workqueue.RateLimitingInterface to cooperate with DSK's
// tick engine.
type wrappedQueue struct {
	workqueue.RateLimitingInterface

	mu         sync.Mutex
	idleCond   *sync.Cond
	inFlight   map[any]struct{} // keys between Get and Done dequeued during an open window
	active     bool             // true if Add() or Get() was called during this tick window
	windowOpen bool
	done       chan struct{} // closed when tick is complete; reset each tick
}

func newWrappedQueue(q workqueue.RateLimitingInterface) *wrappedQueue {
	w := &wrappedQueue{
		RateLimitingInterface: q,
		inFlight:              make(map[any]struct{}),
		done:                  make(chan struct{}),
	}
	w.idleCond = sync.NewCond(&w.mu)
	return w
}

// beginTick opens the Get() tracking window and resets tick state.
// Called at the start of each Flush.
func (w *wrappedQueue) beginTick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inFlight = make(map[any]struct{})
	w.active = false
	w.windowOpen = true
	w.done = make(chan struct{})
}

// closeWindow closes the tick window and checks for completion.
// Called after events are flushed and handlers have returned.
func (w *wrappedQueue) closeWindow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.windowOpen = false
	w.checkDoneLocked()
}

// doneCh returns the current tick's completion channel.
// Callers must not invoke beginTick concurrently while waiting on the returned channel.
func (w *wrappedQueue) doneCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

// isIdle returns true when there are no keys in flight and no keys pending
// in the underlying queue. This catches items that have been requeued via
// AddRateLimited/AddAfter and have cleared the rate limiter but not yet
// been dequeued by a worker goroutine.
//
// Note: items added via AddRateLimited or AddAfter that have not yet cleared
// the rate limiter (i.e., are still in the delay heap) are not visible to
// Len() and are not counted here. In practice, DSK's tick model means
// controllers call AddRateLimited with zero or near-zero delays, so this
// window is negligible. If a controller uses significant rate-limit delays,
// IsSteady() may return a transient false positive until the delay expires.
func (w *wrappedQueue) isIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.inFlight) == 0 && w.RateLimitingInterface.Len() == 0
}

// Add intercepts item enqueuing. Items added during an open window mark this
// tick as active so waitForIdle knows to wait for them to be processed.
func (w *wrappedQueue) Add(item any) {
	w.RateLimitingInterface.Add(item)
	w.mu.Lock()
	if w.windowOpen {
		w.active = true
	}
	w.idleCond.Broadcast()
	w.mu.Unlock()
}

// AddRateLimited is a controller-triggered requeue. Not counted as this tick's work.
func (w *wrappedQueue) AddRateLimited(item any) {
	w.RateLimitingInterface.AddRateLimited(item)
}

// AddAfter is a controller-triggered requeue. Not counted as this tick's work.
func (w *wrappedQueue) AddAfter(item any, duration time.Duration) {
	w.RateLimitingInterface.AddAfter(item, duration)
}

// Get intercepts key dequeuing. Keys dequeued during an open window are tracked as in-flight.
func (w *wrappedQueue) Get() (any, bool) {
	item, shutdown := w.RateLimitingInterface.Get()
	if !shutdown {
		w.mu.Lock()
		if w.windowOpen {
			w.inFlight[item] = struct{}{}
			w.active = true
		}
		// Broadcast so waitForIdle can re-check Len() now that an item was dequeued.
		w.idleCond.Broadcast()
		w.mu.Unlock()
	}
	return item, shutdown
}

// Done intercepts reconcile completion. Removes the key from in-flight tracking
// and checks whether the tick is now complete.
func (w *wrappedQueue) Done(item any) {
	w.RateLimitingInterface.Done(item)
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inFlight, item)
	w.checkDoneLocked()
	w.idleCond.Broadcast()
}

// waitForIdle blocks until len(inFlight)==0 and Len()==0, or ctx is cancelled.
// It only waits if the tick was active: i.e., Add() or Get() was called during
// the tick window. Items that were already in the queue before the tick started
// and were never touched do not trigger waiting. This ensures TickUntilSteady
// correctly exhausts its tick budget for controllers that never drain their queue.
//
// Called by engine.flush after waitForHandlersIdle and before closeWindow so
// that items requeued inside a tick (via Add by handlers, or via AddAfter/Done
// dirty path) are drained within the same tick window rather than leaking into
// IsSteady checks after the tick returns.
func (w *wrappedQueue) waitForIdle(ctx context.Context) error {
	stopCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			w.idleCond.Broadcast()
		case <-stopCh:
		}
	}()
	defer close(stopCh)

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return ctx.Err()
	}
	for len(w.inFlight) > 0 || w.RateLimitingInterface.Len() > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.idleCond.Wait()
	}
	return ctx.Err()
}

// checkDoneLocked closes the done channel if the tick is complete:
// window closed and nothing in flight. The tick is considered complete once
// all items that were dequeued (Get) during the open window have been
// finished (Done). Items sitting in the queue but never dequeued do not
// block tick completion — use IsSteady() to detect a fully quiescent state.
//
// Lock ordering: w.mu is the only lock acquired here. engine.mu is
// released before any wrappedQueue method is called, so no inversion.
//
// Must be called with w.mu held.
func (w *wrappedQueue) checkDoneLocked() {
	if !w.windowOpen && len(w.inFlight) == 0 {
		select {
		case <-w.done:
			// already closed
		default:
			close(w.done)
		}
	}
}

// typedWrappedQueue is the generic counterpart of wrappedQueue for
// controllers that use workqueue.TypedRateLimitingInterface[T] directly.
// It implements both TypedRateLimitingInterface[T] and tickableQueue.
type typedWrappedQueue[T comparable] struct {
	workqueue.TypedRateLimitingInterface[T]

	mu         sync.Mutex
	idleCond   *sync.Cond
	inFlight   map[T]struct{}
	active     bool
	windowOpen bool
	done       chan struct{}
}

func newTypedWrappedQueue[T comparable](q workqueue.TypedRateLimitingInterface[T]) *typedWrappedQueue[T] {
	w := &typedWrappedQueue[T]{
		TypedRateLimitingInterface: q,
		inFlight:                   make(map[T]struct{}),
		done:                       make(chan struct{}),
	}
	w.idleCond = sync.NewCond(&w.mu)
	return w
}

func (w *typedWrappedQueue[T]) beginTick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inFlight = make(map[T]struct{})
	w.active = false
	w.windowOpen = true
	w.done = make(chan struct{})
}

func (w *typedWrappedQueue[T]) closeWindow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.windowOpen = false
	w.checkDoneLocked()
}

func (w *typedWrappedQueue[T]) doneCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

func (w *typedWrappedQueue[T]) isIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.inFlight) == 0 && w.TypedRateLimitingInterface.Len() == 0
}

func (w *typedWrappedQueue[T]) Add(item T) {
	w.TypedRateLimitingInterface.Add(item)
	w.mu.Lock()
	if w.windowOpen {
		w.active = true
	}
	w.idleCond.Broadcast()
	w.mu.Unlock()
}

func (w *typedWrappedQueue[T]) Get() (T, bool) {
	item, shutdown := w.TypedRateLimitingInterface.Get()
	if !shutdown {
		w.mu.Lock()
		if w.windowOpen {
			w.inFlight[item] = struct{}{}
			w.active = true
		}
		// Broadcast so waitForIdle can re-check Len() now that an item was dequeued.
		w.idleCond.Broadcast()
		w.mu.Unlock()
	}
	return item, shutdown
}

func (w *typedWrappedQueue[T]) Done(item T) {
	w.TypedRateLimitingInterface.Done(item)
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inFlight, item)
	w.checkDoneLocked()
	w.idleCond.Broadcast()
}

func (w *typedWrappedQueue[T]) checkDoneLocked() {
	if !w.windowOpen && len(w.inFlight) == 0 {
		select {
		case <-w.done:
		default:
			close(w.done)
		}
	}
}

func (w *typedWrappedQueue[T]) waitForIdle(ctx context.Context) error {
	stopCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			w.idleCond.Broadcast()
		case <-stopCh:
		}
	}()
	defer close(stopCh)

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return ctx.Err()
	}
	for len(w.inFlight) > 0 || w.TypedRateLimitingInterface.Len() > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.idleCond.Wait()
	}
	return ctx.Err()
}
