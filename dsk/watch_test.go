//go:build dsk

package dsk_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/watch"

	dsk "github.com/authzed/controller-idioms/dsk"
)

func TestControlledWatch_DeliverSendsEvent(t *testing.T) {
	cw := dsk.NewControlledWatch(t)
	ev := watch.Event{Type: watch.Added}

	// ResultChan should not have events before delivery
	select {
	case <-cw.ResultChan():
		t.Fatal("expected no event before delivery")
	default:
	}

	// Deliver from a goroutine since the channel is unbuffered
	go cw.Deliver(ev)

	got := <-cw.ResultChan()
	if got.Type != watch.Added {
		t.Fatalf("expected Added, got %v", got.Type)
	}
}

func TestControlledWatch_StopClosesChannel(t *testing.T) {
	cw := dsk.NewControlledWatch(t)
	cw.Stop()

	_, ok := <-cw.ResultChan()
	if ok {
		t.Fatal("expected channel to be closed after Stop")
	}
}

// TestControlledWatch_DeliverBlocksWhenNoConsumer verifies that the channel is
// unbuffered: Deliver must block until a consumer reads from ResultChan.
func TestControlledWatch_DeliverBlocksWhenNoConsumer(t *testing.T) {
	cw := dsk.NewControlledWatch(t)

	deliverDone := make(chan struct{})
	go func() {
		defer close(deliverDone)
		cw.Deliver(watch.Event{Type: watch.Added})
	}()

	// Verify Deliver is blocking (no consumer).
	select {
	case <-deliverDone:
		t.Fatal("expected Deliver to block when no consumer is reading")
	default:
	}

	// Drain one event — Deliver should unblock.
	<-cw.ResultChan()
	select {
	case <-deliverDone:
	case <-t.Context().Done():
		t.Fatal("expected Deliver to complete after consumer drained")
	}
}

func TestControlledWatch_DeliverReturnsOnStop(t *testing.T) {
	cw := dsk.NewControlledWatch(t)

	deliverDone := make(chan struct{})
	go func() {
		defer close(deliverDone)
		cw.Deliver(watch.Event{Type: watch.Modified})
	}()

	// Verify the goroutine is blocking before calling Stop.
	select {
	case <-deliverDone:
		t.Fatal("expected Deliver to block when no consumer is reading")
	default:
	}

	cw.Stop()

	select {
	case <-deliverDone:
	case <-t.Context().Done():
		t.Fatal("expected Deliver to return after Stop()")
	}
}
