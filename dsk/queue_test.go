//go:build dsk

package dsk

import (
	"testing"

	"k8s.io/client-go/util/workqueue"
)

func TestWrappedQueue_CooperativeCompletion(t *testing.T) {
	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()

	wq := newWrappedQueue(underlying)

	wq.beginTick()

	// Simulate informer calling Add during tick window
	wq.Add("key1")

	// Get during open window so item is tracked in inFlight
	item, _ := wq.Get()
	if item != "key1" {
		t.Fatalf("expected key1, got %v", item)
	}

	// Close the window
	wq.closeWindow()

	// Before Done, tick should not be complete (item still in flight)
	select {
	case <-wq.doneCh():
		t.Fatal("expected tick not complete before Done")
	default:
	}

	wq.Done(item)

	// After Done, tick should be complete
	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected tick to complete after Done")
	}
}

func TestWrappedQueue_RequeueNotTracked(t *testing.T) {
	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()

	wq := newWrappedQueue(underlying)

	wq.beginTick()
	wq.Add("key1")
	wq.closeWindow()

	item, _ := wq.Get()
	// Simulate controller requeueing the key
	wq.AddRateLimited(item)
	wq.Done(item)

	// Tick should complete — the requeue is not counted
	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected tick to complete even with pending requeue")
	}
}

func TestWrappedQueue_EmptyTickCompletes(t *testing.T) {
	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()

	wq := newWrappedQueue(underlying)

	wq.beginTick()
	// No Add() calls — nothing to wait for
	wq.closeWindow()

	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected empty tick to complete immediately")
	}
}

func TestWrappedQueue_IsIdle_WithItemInUnderlyingQueue(t *testing.T) {
	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()

	wq := newWrappedQueue(underlying)

	// Add directly to the underlying queue (bypassing the wrapped Add) to
	// simulate an item that has already cleared the rate limiter and is
	// sitting in the FIFO ready to be Get'd. Using wq.AddRateLimited would
	// introduce timing sensitivity from the rate-limiter delay.
	underlying.Add("key1")

	if wq.isIdle() {
		t.Fatal("expected non-idle: item is in underlying queue, waiting to be Get'd")
	}

	item, _ := wq.Get()
	wq.Done(item)

	if !wq.isIdle() {
		t.Fatal("expected idle after item processed")
	}
}

func TestWrappedQueue_IsIdle(t *testing.T) {
	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()

	wq := newWrappedQueue(underlying)
	if !wq.isIdle() {
		t.Fatal("expected idle queue to be idle")
	}

	wq.beginTick()
	wq.Add("key1")
	// Keep window open during Get() so item is tracked in inFlight
	item, _ := wq.Get()
	if wq.isIdle() {
		t.Fatal("expected non-idle queue during inflight reconcile")
	}
	wq.Done(item)
	if !wq.isIdle() {
		t.Fatal("expected idle queue after Done")
	}
}
