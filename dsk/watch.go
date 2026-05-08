//go:build dsk

package dsk

import (
	"sync"

	"k8s.io/apimachinery/pkg/watch"
)

// ControlledWatch implements watch.Interface with DSK-controlled event delivery.
// Events are only sent to ResultChan when Deliver is called explicitly.
type ControlledWatch struct {
	resultCh chan watch.Event
	// stopCh is closed by Stop() to signal the upstream drain goroutine to exit.
	stopCh   chan struct{}
	stopOnce sync.Once
	// mu guards stopped and coordinates with wg to prevent close(resultCh) racing with Deliver.
	mu      sync.Mutex
	stopped bool // true after Stop() is called; checked by Deliver before wg.Add
	// wg tracks in-flight Deliver calls; Stop waits for it before closing resultCh.
	wg sync.WaitGroup
	tb TB
}

// NewControlledWatch creates a new ControlledWatch ready for use.
// The channel is unbuffered: Deliver blocks until the consumer reads from ResultChan
// or Stop is called. In practice the consumer (informer reflector) runs in its own
// goroutine, so Deliver only blocks until the next goroutine scheduling point.
func NewControlledWatch(tb TB) *ControlledWatch {
	return &ControlledWatch{
		resultCh: make(chan watch.Event),
		stopCh:   make(chan struct{}),
		tb:       tb,
	}
}

// ResultChan returns the channel that delivers watch events to the consumer.
// Implements watch.Interface.
func (w *ControlledWatch) ResultChan() <-chan watch.Event {
	return w.resultCh
}

// Stop signals the upstream drain goroutine to exit and closes the result
// channel, signalling the consumer that the watch ended.
// Implements watch.Interface. Safe to call multiple times.
func (w *ControlledWatch) Stop() {
	w.stopOnce.Do(func() {
		// Mark stopped under the lock so Deliver can't start a new wg.Add after
		// we call wg.Wait().
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()

		close(w.stopCh) // unblocks any Deliver call waiting in its blocking select
		w.wg.Wait()     // wait for all in-flight Deliver calls to finish
		close(w.resultCh)
	})
}

// Done returns a channel that is closed when Stop() is called.
// Use this to detect watch termination without participating in the Deliver WaitGroup.
func (w *ControlledWatch) Done() <-chan struct{} {
	return w.stopCh
}

// Deliver writes ev to the result channel. Deliver blocks until the consumer
// reads from ResultChan or the watch is stopped via Stop().
// A stopped watch drops the event and logs a diagnostic message.
func (w *ControlledWatch) Deliver(ev watch.Event) {
	// Atomically check stopped and increment wg so that Stop() cannot close
	// resultCh between our stopped-check and our send.
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.tb.Logf("dsk: Deliver called on stopped watch — event %v dropped", ev.Type)
		return
	}
	w.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()

	// Block until the consumer reads or the watch is stopped.
	select {
	case w.resultCh <- ev:
	case <-w.stopCh:
		// Watch stopped while blocking; event dropped.
		w.tb.Logf("dsk: Deliver unblocked by Stop() — event %v dropped", ev.Type)
	case <-w.tb.Context().Done():
		// Test ended while delivery was still pending. This typically means
		// Flush/Tick was called without a concurrent consumer reading from
		// ResultChan (e.g. no running informer reflector goroutine).
		w.tb.Logf("dsk: event %v was never consumed — ResultChan() must be "+
			"read concurrently with Flush/Tick; check that an informer reflector "+
			"goroutine is running when Tick/Flush is called", ev.Type)
	}
}

// deliver implements eventSink. Called by engine.flush to push ev to the result channel.
func (w *ControlledWatch) deliver(ev watch.Event) {
	w.Deliver(ev)
}

// compile-time check
var _ eventSink = (*ControlledWatch)(nil)
