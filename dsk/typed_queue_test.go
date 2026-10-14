//go:build dsk

package dsk

import (
	"testing"

	"k8s.io/client-go/util/workqueue"
)

func TestTypedWrappedQueue_CooperativeCompletion(t *testing.T) {
	underlying := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer underlying.ShutDown()

	wq := newTypedWrappedQueue(underlying)

	wq.beginTick()

	wq.Add("key1")

	item, _ := wq.Get()
	if item != "key1" {
		t.Fatalf("expected key1, got %v", item)
	}

	wq.closeWindow()

	select {
	case <-wq.doneCh():
		t.Fatal("expected tick not complete before Done")
	default:
	}

	wq.Done(item)

	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected tick to complete after Done")
	}
}

func TestTypedWrappedQueue_RequeueNotTracked(t *testing.T) {
	underlying := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer underlying.ShutDown()

	wq := newTypedWrappedQueue(underlying)

	wq.beginTick()
	wq.Add("key1")
	wq.closeWindow()

	item, _ := wq.Get()
	wq.AddRateLimited(item)
	wq.Done(item)

	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected tick to complete even with pending requeue")
	}
}

func TestTypedWrappedQueue_EmptyTickCompletes(t *testing.T) {
	underlying := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer underlying.ShutDown()

	wq := newTypedWrappedQueue(underlying)

	wq.beginTick()
	wq.closeWindow()

	select {
	case <-wq.doneCh():
	case <-t.Context().Done():
		t.Fatal("expected empty tick to complete immediately")
	}
}

func TestTypedWrappedQueue_IsIdle_WithItemInUnderlyingQueue(t *testing.T) {
	underlying := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer underlying.ShutDown()

	wq := newTypedWrappedQueue(underlying)

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

func TestTypedWrappedQueue_IsIdle(t *testing.T) {
	underlying := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer underlying.ShutDown()

	wq := newTypedWrappedQueue(underlying)
	if !wq.isIdle() {
		t.Fatal("expected idle queue to be idle")
	}

	wq.beginTick()
	wq.Add("key1")
	item, _ := wq.Get()
	if wq.isIdle() {
		t.Fatal("expected non-idle queue during inflight reconcile")
	}
	wq.Done(item)
	if !wq.isIdle() {
		t.Fatal("expected idle queue after Done")
	}
}
