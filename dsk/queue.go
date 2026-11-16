//go:build dsk

package dsk

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"
)

// tickableQueue is the internal interface the engine uses to coordinate ticks
// across registered queues. queueTracker implements it.
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
// heap, where Len() cannot see them. It is guarded by the owning tracker's mu.
type requeueTracker[T comparable] struct {
	// after holds the requeue deadline per item. The delaying queue keeps only
	// the earliest deadline for an item that is already waiting, so this does too.
	after map[T]time.Time
	// rateLimited holds AddRateLimited items whose delay DSK cannot see: the
	// rate limiter of a queue passed to WrapQueue or WrapTypedQueue.
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

// due reports whether a requeue deadline has passed for an item that has not
// been dequeued since. The delaying queue moves such an item into the queue
// with its own Add, which a wrapper around the finished queue does not see,
// so the item is owed work from its deadline until a Get resolves it.
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

// QueueOption configures a queue registered with DSK.
type QueueOption func(*queueOptions)

type queueOptions struct {
	drained bool
}

// Drained declares that worker goroutines drain the queue. Tick, Flush,
// TickUntilSteady and Advance then wait for every queued item to be
// reconciled, even when a worker has not yet reached its first Get.
//
// Without Drained, DSK infers a worker from a goroutine parked in Get, holding
// an item, or taking one during the tick. That inference misses a worker that
// starts late, and it is what lets a test drain a queue by hand after Tick. Do
// not use Drained on a queue that the test drains by hand: Tick would wait for
// a worker that never comes.
func Drained() QueueOption {
	return func(o *queueOptions) { o.drained = true }
}

// queueTracker keeps the ledger and tick state for one queue. The engine
// registers it as a tickableQueue, and the queue wrappers feed it.
//
// The ledger records the work each item is owed, from Add to Done, and mirrors
// the workqueue's own dirty and processing sets:
//
//   - Add puts an item in queued, or in dirty if it is processing.
//   - Get moves it from queued to processing.
//   - Done removes it from processing, and moves it back to queued if it is
//     dirty: the workqueue hands a dirty item out again after Done.
//
// The wrappers update the ledger before the underlying Add and Done and after
// the underlying Get, so it never shows an item as settled while the workqueue
// still owes it work.
//
// A tracker fed by a base queue (NewRateLimitingQueue) is exact: every Add
// passes through it, including the delaying queue's Add of a requeue that has
// come due. A tracker fed by a wrapper around a finished queue (WrapQueue,
// WrapTypedQueue) misses those Adds and falls back on requeue deadlines and
// Len; see busyLocked.
//
// Lock ordering: mu → workqueue lock (via len). The wrappers never hold mu
// while they call Add, Get or Done on the underlying queue.
type queueTracker[T comparable] struct {
	exact   bool
	drained bool
	len     func() int // the underlying queue's Len; unused when exact

	// afterGet, if set, runs after the underlying Get returns and before the
	// ledger records the item. Tests use it to inspect that moment.
	afterGet func()

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

func newQueueTracker[T comparable](exact bool, length func() int, opts []QueueOption) *queueTracker[T] {
	var o queueOptions
	for _, opt := range opts {
		opt(&o)
	}
	t := &queueTracker[T]{
		exact:      exact,
		drained:    o.drained,
		len:        length,
		requeues:   newRequeueTracker[T](),
		queued:     make(map[T]struct{}),
		processing: make(map[T]struct{}),
		dirty:      make(map[T]struct{}),
		inFlight:   make(map[T]struct{}),
		done:       make(chan struct{}),
	}
	t.idleCond = sync.NewCond(&t.mu)
	return t
}

// beginTick opens the tick window and resets tick state.
// Called at the start of each Flush.
func (t *queueTracker[T]) beginTick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inFlight = make(map[T]struct{})
	t.active = false
	t.gotThisTick = false
	t.windowOpen = true
	t.tickOver = false
	t.done = make(chan struct{})
}

// closeWindow closes the tick window and checks for completion.
// Called after events are flushed and handlers have returned.
func (t *queueTracker[T]) closeWindow() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.windowOpen = false
	t.checkDoneLocked()
}

// doneCh returns the current tick's completion channel.
// Callers must not invoke beginTick concurrently while waiting on the returned channel.
func (t *queueTracker[T]) doneCh() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// workerSeenLocked reports whether a goroutine drains this queue: the queue is
// declared Drained, or a goroutine is parked in Get, holds an item, or took
// one during this tick. A test that drains the queue by hand after Tick
// returns has no such goroutine.
func (t *queueTracker[T]) workerSeenLocked() bool {
	return (t.drained && !t.shutDown) || t.parked > 0 || len(t.processing) > 0 || t.gotThisTick
}

// busyLocked reports whether the queue owes any work: an item is queued or
// processing in the ledger. After ShutDown the queue owes only the reconciles
// already processing: workers stop dequeuing, so items left in the queue would
// never drain.
//
// A tracker that is not exact also counts an item whose requeue deadline has
// passed and that has not been dequeued since, and any item in the underlying
// queue (Len). Len catches rate-limited requeues that have left the delay
// heap, and items added before the queue was wrapped. One case remains: a
// rate-limited requeue between the underlying Get returning it and the
// tracker recording it. NewRateLimitingQueue closes that case.
func (t *queueTracker[T]) busyLocked() bool {
	if len(t.processing) > 0 {
		return true
	}
	if t.shutDown {
		return false
	}
	if len(t.queued) > 0 {
		return true
	}
	if t.exact {
		return false
	}
	return t.requeues.due() || t.len() > 0
}

// isIdle reports whether the queue owes no work. Items still in the delay heap
// before their deadline do not count; pendingRequeues reports them.
func (t *queueTracker[T]) isIdle() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.busyLocked()
}

func (t *queueTracker[T]) pendingRequeues() requeueStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requeues.status()
}

// addLocked records that item is owed work.
func (t *queueTracker[T]) addLocked(item T) {
	if t.shutDown {
		return
	}
	if _, ok := t.processing[item]; ok {
		t.dirty[item] = struct{}{}
	} else {
		t.queued[item] = struct{}{}
	}
	if t.windowOpen {
		t.active = true
	}
}

// recordAdd must be called before the underlying Add, so a Get that races
// with it finds the item in queued.
func (t *queueTracker[T]) recordAdd(item T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addLocked(item)
}

// recordDeadline records a requeue that the delaying queue will add at
// now+d. A non-positive d is added at once, through Add.
func (t *queueTracker[T]) recordDeadline(item T, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.shutDown {
		t.requeues.addAfter(item, d)
	}
}

// recordRateLimited records an AddRateLimited whose delay DSK cannot see.
func (t *queueTracker[T]) recordRateLimited(item T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.shutDown {
		t.requeues.addRateLimited(item)
	}
}

func (t *queueTracker[T]) beforeGet() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parked++
}

// recordGet moves item from queued to processing. While a caller is parked in
// the underlying Get, the tick does not complete with work queued.
func (t *queueTracker[T]) recordGet(item T, shutdown bool) {
	if t.afterGet != nil {
		t.afterGet()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parked--
	if !shutdown {
		delete(t.queued, item)
		t.processing[item] = struct{}{}
		t.requeues.got(item)
		t.gotThisTick = true
		if t.windowOpen {
			t.active = true
		}
		if !t.tickOver {
			t.inFlight[item] = struct{}{}
		}
	}
	t.checkDoneLocked()
	t.idleCond.Broadcast()
}

// recordDone must be called before the underlying Done. A dirty item goes back
// to queued: the workqueue hands it out again.
func (t *queueTracker[T]) recordDone(item T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.processing, item)
	delete(t.inFlight, item)
	if _, ok := t.dirty[item]; ok {
		delete(t.dirty, item)
		t.queued[item] = struct{}{}
	}
	t.checkDoneLocked()
	t.idleCond.Broadcast()
}

// recordShutDown drops the work the queue will no longer hand out. Items
// already processing stay in the ledger until Done.
func (t *queueTracker[T]) recordShutDown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shutDown = true
	t.requeues.clear()
	clear(t.queued)
	clear(t.dirty)
	t.checkDoneLocked()
	t.idleCond.Broadcast()
}

// waitForIdle blocks until the queue owes no work (see busyLocked), or ctx is
// cancelled. It waits only if Add or Get was called while this tick's window
// was open, or a worker drains the queue. A queue whose items are never
// dequeued does not block, so TickUntilSteady still exhausts its tick budget
// for a controller that never drains its queue.
//
// Called by TickUntilSteady and Advance after each tick, not by Flush, so a
// test can drain a queue by hand after Tick returns.
func (t *queueTracker[T]) waitForIdle(ctx context.Context) error {
	stopCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			t.idleCond.Broadcast()
		case <-stopCh:
		}
	}()
	defer close(stopCh)

	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active && !t.workerSeenLocked() {
		return ctx.Err()
	}
	for t.busyLocked() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.idleCond.Wait()
	}
	return ctx.Err()
}

// checkDoneLocked closes the done channel once the tick is complete: the
// window is closed, every item dequeued during the tick is Done, and no
// queued item is waiting for a worker that drains the queue. A worker whose
// Get lands after the window closes is still counted, because the tick stays
// open while a worker is seen with work queued.
//
// Items queued with no worker seen do not block the tick, so a test can drain
// by hand after Tick returns. IsSteady reports them.
//
// Must be called with t.mu held.
func (t *queueTracker[T]) checkDoneLocked() {
	if t.tickOver || t.windowOpen || len(t.inFlight) > 0 {
		return
	}
	if len(t.queued) > 0 && t.workerSeenLocked() {
		return
	}
	t.tickOver = true
	close(t.done)
}

// wrappedQueue wraps a finished workqueue.TypedRateLimitingInterface[T] for
// WrapQueue and WrapTypedQueue. WrapQueue uses wrappedQueue[any]: client-go
// defines RateLimitingInterface as TypedRateLimitingInterface[any].
type wrappedQueue[T comparable] struct {
	workqueue.TypedRateLimitingInterface[T]
	*queueTracker[T]
}

func newWrappedQueue[T comparable](q workqueue.TypedRateLimitingInterface[T], opts ...QueueOption) *wrappedQueue[T] {
	return &wrappedQueue[T]{
		TypedRateLimitingInterface: q,
		queueTracker:               newQueueTracker[T](false, q.Len, opts),
	}
}

func (w *wrappedQueue[T]) Add(item T) {
	w.recordAdd(item)
	w.TypedRateLimitingInterface.Add(item)
}

// AddRateLimited is recorded as a pending requeue until the item is next
// dequeued. Its delay comes from the rate limiter, which DSK cannot see here.
func (w *wrappedQueue[T]) AddRateLimited(item T) {
	w.recordRateLimited(item)
	w.TypedRateLimitingInterface.AddRateLimited(item)
}

// AddAfter with a positive duration is recorded as a pending requeue with its
// deadline. The delaying queue adds a non-positive duration at once, so that
// is recorded as an Add.
func (w *wrappedQueue[T]) AddAfter(item T, duration time.Duration) {
	if duration <= 0 {
		w.recordAdd(item)
	} else {
		w.recordDeadline(item, duration)
	}
	w.TypedRateLimitingInterface.AddAfter(item, duration)
}

func (w *wrappedQueue[T]) Get() (T, bool) {
	w.beforeGet()
	item, shutdown := w.TypedRateLimitingInterface.Get()
	w.recordGet(item, shutdown)
	return item, shutdown
}

func (w *wrappedQueue[T]) Done(item T) {
	w.recordDone(item)
	w.TypedRateLimitingInterface.Done(item)
}

func (w *wrappedQueue[T]) ShutDown() {
	w.TypedRateLimitingInterface.ShutDown()
	w.recordShutDown()
}

func (w *wrappedQueue[T]) ShutDownWithDrain() {
	w.TypedRateLimitingInterface.ShutDownWithDrain()
	w.recordShutDown()
}

// baseQueue is the FIFO at the bottom of a queue built by NewRateLimitingQueue.
// The delaying queue adds requeues that come due through baseQueue.Add, so the
// tracker sees every Add.
type baseQueue[T comparable] struct {
	workqueue.TypedInterface[T]
	t *queueTracker[T]
}

func (b *baseQueue[T]) Add(item T) {
	b.t.recordAdd(item)
	b.TypedInterface.Add(item)
}

func (b *baseQueue[T]) Get() (T, bool) {
	b.t.beforeGet()
	item, shutdown := b.TypedInterface.Get()
	b.t.recordGet(item, shutdown)
	return item, shutdown
}

func (b *baseQueue[T]) Done(item T) {
	b.t.recordDone(item)
	b.TypedInterface.Done(item)
}

func (b *baseQueue[T]) ShutDown() {
	b.TypedInterface.ShutDown()
	b.t.recordShutDown()
}

func (b *baseQueue[T]) ShutDownWithDrain() {
	b.TypedInterface.ShutDownWithDrain()
	b.t.recordShutDown()
}

// deadlineLimiter reports each AddRateLimited delay to the tracker, so a
// rate-limited requeue has a deadline that NextRequeue can see.
type deadlineLimiter[T comparable] struct {
	workqueue.TypedRateLimiter[T]
	t *queueTracker[T]
}

func (l *deadlineLimiter[T]) When(item T) time.Duration {
	d := l.TypedRateLimiter.When(item)
	l.t.recordDeadline(item, d)
	return d
}

// requeueQueue is the top of a queue built by NewRateLimitingQueue. It records
// AddAfter deadlines for HasPendingRequeues and NextRequeue; the base queue
// records everything else.
type requeueQueue[T comparable] struct {
	workqueue.TypedRateLimitingInterface[T]
	t *queueTracker[T]
}

func (r *requeueQueue[T]) AddAfter(item T, duration time.Duration) {
	r.t.recordDeadline(item, duration)
	r.TypedRateLimitingInterface.AddAfter(item, duration)
}

// newRateLimitingQueue builds the queue stack for DSK.NewRateLimitingQueue.
// cfg.DelayingQueue and cfg.Clock must be nil.
func newRateLimitingQueue[T comparable](rl workqueue.TypedRateLimiter[T], cfg workqueue.TypedRateLimitingQueueConfig[T], opts []QueueOption) (workqueue.TypedRateLimitingInterface[T], *queueTracker[T]) {
	t := newQueueTracker[T](true, nil, opts)
	base := &baseQueue[T]{
		TypedInterface: workqueue.NewTypedWithConfig(workqueue.TypedQueueConfig[T]{
			Name:            cfg.Name,
			MetricsProvider: cfg.MetricsProvider,
		}),
		t: t,
	}
	delaying := workqueue.NewTypedDelayingQueueWithConfig(workqueue.TypedDelayingQueueConfig[T]{
		Name:            cfg.Name,
		MetricsProvider: cfg.MetricsProvider,
		Queue:           base,
	})
	q := workqueue.NewTypedRateLimitingQueueWithConfig[T](&deadlineLimiter[T]{TypedRateLimiter: rl, t: t}, workqueue.TypedRateLimitingQueueConfig[T]{
		Name:            cfg.Name,
		MetricsProvider: cfg.MetricsProvider,
		DelayingQueue:   delaying,
	})
	return &requeueQueue[T]{TypedRateLimitingInterface: q, t: t}, t
}
