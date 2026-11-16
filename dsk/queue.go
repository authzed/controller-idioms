//go:build dsk

package dsk

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"
)

// tickableQueue is the internal interface the engine uses to coordinate ticks
// across registered queue wrappers.
type tickableQueue interface {
	beginTick()
	closeWindow()
	doneCh() <-chan struct{}
	isIdle() bool
	waitForIdle(ctx context.Context) error
	pendingRequeues() requeueStatus
}

// requeueStatus summarises the delayed requeues a queue is holding.
type requeueStatus struct {
	pending bool      // any AddAfter (d > 0) or AddRateLimited requeue not yet dequeued
	hasNext bool      // next is set: at least one pending requeue has a known deadline
	next    time.Time // earliest known deadline
}

func (s requeueStatus) merge(o requeueStatus) requeueStatus {
	s.pending = s.pending || o.pending
	if o.hasNext && (!s.hasNext || o.next.Before(s.next)) {
		s.hasNext, s.next = true, o.next
	}
	return s
}

// requeueTracker records delayed requeues that sit in the delaying queue's
// heap, where Len() cannot see them. It is guarded by the owning wrapper's mu.
type requeueTracker[T comparable] struct {
	// after holds the AddAfter deadline per item. The delaying queue keeps only
	// the earliest deadline for an item that is already waiting, so this does too.
	after map[T]time.Time
	// rateLimited holds AddRateLimited items. Their delay comes from the
	// underlying rate limiter, which DSK cannot see, so they have no deadline.
	rateLimited map[T]struct{}
}

func newRequeueTracker[T comparable]() requeueTracker[T] {
	return requeueTracker[T]{after: make(map[T]time.Time), rateLimited: make(map[T]struct{})}
}

func (r *requeueTracker[T]) addAfter(item T, d time.Duration) {
	if d <= 0 {
		return // the delaying queue adds these immediately
	}
	now := time.Now()
	deadline := now.Add(d)
	// An expired deadline has already fired, so a new AddAfter is a new entry.
	if cur, ok := r.after[item]; ok && cur.After(now) && !deadline.Before(cur) {
		return
	}
	r.after[item] = deadline
}

func (r *requeueTracker[T]) addRateLimited(item T) {
	r.rateLimited[item] = struct{}{}
}

// got records that item was dequeued. A deadline-tracked requeue is resolved
// only once its deadline has passed, since an earlier Get came from another Add.
// A rate-limited requeue is resolved by the next Get of the item.
func (r *requeueTracker[T]) got(item T) {
	if deadline, ok := r.after[item]; ok && !time.Now().Before(deadline) {
		delete(r.after, item)
	}
	delete(r.rateLimited, item)
}

// due reports whether an AddAfter deadline has passed for an item that has not
// been dequeued since. The delaying queue moves such an item into the queue
// with its own Add, which the wrapper does not see, so the item is owed work
// from its deadline until a Get resolves it.
func (r *requeueTracker[T]) due() bool {
	now := time.Now()
	for _, deadline := range r.after {
		if !now.Before(deadline) {
			return true
		}
	}
	return false
}

func (r *requeueTracker[T]) clear() {
	clear(r.after)
	clear(r.rateLimited)
}

func (r *requeueTracker[T]) status() requeueStatus {
	s := requeueStatus{pending: len(r.after)+len(r.rateLimited) > 0}
	for _, deadline := range r.after {
		if !s.hasNext || deadline.Before(s.next) {
			s.hasNext, s.next = true, deadline
		}
	}
	return s
}

// wrappedQueue wraps a workqueue.TypedRateLimitingInterface[T] to cooperate
// with DSK's tick engine. It implements both TypedRateLimitingInterface[T] and
// tickableQueue. WrapQueue uses wrappedQueue[any]: client-go defines
// RateLimitingInterface as TypedRateLimitingInterface[any].
//
// The wrapper keeps a ledger of the work each item is owed, from Add to Done,
// that mirrors the workqueue's own dirty and processing sets:
//
//   - Add puts an item in queued, or in dirty if it is processing.
//   - Get moves it from queued to processing.
//   - Done removes it from processing, and moves it back to queued if it is
//     dirty: the workqueue hands a dirty item out again after Done.
//
// The ledger is updated before the underlying Add and Done and after the
// underlying Get, so it never shows an item as settled while the workqueue
// still owes it work.
//
// Lock ordering: w.mu → workqueue lock (via Len). The wrapper never holds
// w.mu while it calls Add, Get or Done on the underlying queue.
type wrappedQueue[T comparable] struct {
	workqueue.TypedRateLimitingInterface[T]

	mu       sync.Mutex
	idleCond *sync.Cond
	requeues requeueTracker[T]

	// The ledger.
	queued     map[T]struct{} // added and not yet dequeued
	processing map[T]struct{} // dequeued and not yet Done
	dirty      map[T]struct{} // added again while processing
	parked     int            // goroutines inside the underlying Get
	shutDown   bool           // the underlying queue drops Adds after ShutDown

	// Tick state, reset by beginTick.
	inFlight    map[T]struct{} // dequeued during this tick and not yet Done
	active      bool           // Add or Get was called while the window was open
	gotThisTick bool           // Get returned an item during this tick
	windowOpen  bool
	tickOver    bool          // done is closed
	done        chan struct{} // closed when the tick is complete
}

func newWrappedQueue[T comparable](q workqueue.TypedRateLimitingInterface[T]) *wrappedQueue[T] {
	w := &wrappedQueue[T]{
		TypedRateLimitingInterface: q,
		requeues:                   newRequeueTracker[T](),
		queued:                     make(map[T]struct{}),
		processing:                 make(map[T]struct{}),
		dirty:                      make(map[T]struct{}),
		inFlight:                   make(map[T]struct{}),
		done:                       make(chan struct{}),
	}
	w.idleCond = sync.NewCond(&w.mu)
	return w
}

// beginTick opens the tick window and resets tick state.
// Called at the start of each Flush.
func (w *wrappedQueue[T]) beginTick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inFlight = make(map[T]struct{})
	w.active = false
	w.gotThisTick = false
	w.windowOpen = true
	w.tickOver = false
	w.done = make(chan struct{})
}

// closeWindow closes the tick window and checks for completion.
// Called after events are flushed and handlers have returned.
func (w *wrappedQueue[T]) closeWindow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.windowOpen = false
	w.checkDoneLocked()
}

// doneCh returns the current tick's completion channel.
// Callers must not invoke beginTick concurrently while waiting on the returned channel.
func (w *wrappedQueue[T]) doneCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

// workerSeenLocked reports whether a goroutine is draining this queue: one is
// parked in Get, holds an item, or took one during this tick. A test that
// drains the queue by hand after Tick returns has no such goroutine.
func (w *wrappedQueue[T]) workerSeenLocked() bool {
	return w.parked > 0 || len(w.processing) > 0 || w.gotThisTick
}

// busyLocked reports whether the queue owes any work: an item is queued or
// processing in the ledger, an AddAfter deadline has passed for an item not
// yet dequeued, or the underlying queue holds an item. Len catches items the
// ledger cannot see: rate-limited requeues that have left the delay heap, and
// items added to the underlying queue before it was wrapped.
//
// After ShutDown the queue owes only the reconciles already processing:
// workers stop dequeuing, so items left in the queue would never drain.
//
// One case remains uncovered: a rate-limited requeue between the underlying
// Get returning it and the wrapper recording it. DSK cannot see its deadline.
func (w *wrappedQueue[T]) busyLocked() bool {
	if len(w.processing) > 0 {
		return true
	}
	if w.shutDown {
		return false
	}
	return len(w.queued) > 0 ||
		w.requeues.due() ||
		w.Len() > 0
}

// isIdle reports whether the queue owes no work. Items still in the delay heap
// (AddRateLimited, or AddAfter with d > 0 before its deadline) do not count;
// pendingRequeues reports them.
func (w *wrappedQueue[T]) isIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.busyLocked()
}

// addLocked records that item is owed work. Must be called before the
// underlying Add, so a Get that races with it finds the item in queued.
func (w *wrappedQueue[T]) addLocked(item T) {
	if w.shutDown {
		return
	}
	if _, ok := w.processing[item]; ok {
		w.dirty[item] = struct{}{}
	} else {
		w.queued[item] = struct{}{}
	}
	if w.windowOpen {
		w.active = true
	}
}

// Add records the item in the ledger, then enqueues it.
func (w *wrappedQueue[T]) Add(item T) {
	w.mu.Lock()
	w.addLocked(item)
	w.mu.Unlock()
	w.TypedRateLimitingInterface.Add(item)
}

// AddRateLimited is a controller-triggered requeue. Not counted as owed work,
// but recorded as a pending requeue until the item is next dequeued.
func (w *wrappedQueue[T]) AddRateLimited(item T) {
	w.mu.Lock()
	if !w.shutDown {
		w.requeues.addRateLimited(item)
	}
	w.mu.Unlock()
	w.TypedRateLimitingInterface.AddRateLimited(item)
}

// AddAfter is a controller-triggered requeue. A positive duration is recorded
// as a pending requeue with its deadline, and counts as owed work once the
// deadline passes. The delaying queue adds a non-positive duration at once,
// so that is recorded as an Add.
func (w *wrappedQueue[T]) AddAfter(item T, duration time.Duration) {
	w.mu.Lock()
	if duration <= 0 {
		w.addLocked(item)
	} else if !w.shutDown {
		w.requeues.addAfter(item, duration)
	}
	w.mu.Unlock()
	w.TypedRateLimitingInterface.AddAfter(item, duration)
}

// ShutDown shuts the underlying queue down and drops the work it will no longer
// hand out. Items already processing stay in the ledger until Done.
func (w *wrappedQueue[T]) ShutDown() {
	w.TypedRateLimitingInterface.ShutDown()
	w.shutDownLedger()
}

// ShutDownWithDrain is like ShutDown, but blocks until processing items are Done.
func (w *wrappedQueue[T]) ShutDownWithDrain() {
	w.TypedRateLimitingInterface.ShutDownWithDrain()
	w.shutDownLedger()
}

func (w *wrappedQueue[T]) shutDownLedger() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shutDown = true
	w.requeues.clear()
	clear(w.queued)
	clear(w.dirty)
	w.checkDoneLocked()
	w.idleCond.Broadcast()
}

func (w *wrappedQueue[T]) pendingRequeues() requeueStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.requeues.status()
}

// Get dequeues an item and records it as processing. While the caller is
// parked in the underlying Get, the tick does not complete with work queued.
func (w *wrappedQueue[T]) Get() (T, bool) {
	w.mu.Lock()
	w.parked++
	w.mu.Unlock()

	item, shutdown := w.TypedRateLimitingInterface.Get()

	w.mu.Lock()
	defer w.mu.Unlock()
	w.parked--
	if !shutdown {
		delete(w.queued, item)
		w.processing[item] = struct{}{}
		w.requeues.got(item)
		w.gotThisTick = true
		if w.windowOpen {
			w.active = true
		}
		if !w.tickOver {
			w.inFlight[item] = struct{}{}
		}
	}
	w.checkDoneLocked()
	w.idleCond.Broadcast()
	return item, shutdown
}

// Done records the end of a reconcile, then tells the underlying queue. A
// dirty item goes back to queued: the workqueue hands it out again.
func (w *wrappedQueue[T]) Done(item T) {
	w.mu.Lock()
	delete(w.processing, item)
	delete(w.inFlight, item)
	if _, ok := w.dirty[item]; ok {
		delete(w.dirty, item)
		w.queued[item] = struct{}{}
	}
	w.checkDoneLocked()
	w.idleCond.Broadcast()
	w.mu.Unlock()
	w.TypedRateLimitingInterface.Done(item)
}

// waitForIdle blocks until the queue owes no work (see busyLocked), or ctx is
// cancelled. It waits only if Add or Get was called while this tick's window
// was open, or a worker is draining the queue. A queue whose items are never
// dequeued does not block, so TickUntilSteady still exhausts its tick budget
// for a controller that never drains its queue.
//
// Called by TickUntilSteady and Advance after each tick, not by Flush, so a
// test can drain a queue by hand after Tick returns.
func (w *wrappedQueue[T]) waitForIdle(ctx context.Context) error {
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
	if !w.active && !w.workerSeenLocked() {
		return ctx.Err()
	}
	for w.busyLocked() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.idleCond.Wait()
	}
	return ctx.Err()
}

// checkDoneLocked closes the done channel once the tick is complete: the
// window is closed, every item dequeued during the tick is Done, and no
// queued item is waiting for a worker that is draining the queue. A worker
// whose Get lands after the window closes is still counted, because the tick
// stays open while it is parked with work queued.
//
// Items queued with no worker draining the queue do not block the tick, so a
// test can drain by hand after Tick returns. IsSteady reports them.
//
// Must be called with w.mu held.
func (w *wrappedQueue[T]) checkDoneLocked() {
	if w.tickOver || w.windowOpen || len(w.inFlight) > 0 {
		return
	}
	if len(w.queued) > 0 && w.workerSeenLocked() {
		return
	}
	w.tickOver = true
	close(w.done)
}
