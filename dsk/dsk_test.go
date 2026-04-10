//go:build dsk

package dsk_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/workqueue"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
)

var podsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

func TestDSK_TickDeliversFlushedEvents(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)

	ctx := t.Context()

	client := d.Dynamic(underlying)
	w, err := client.Resource(podsGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Create through the DSK-wrapped client so preExpectWrite fires and
	// Buffer() reliably waits for the event before returning.
	_, err = client.Resource(podsGVR).Namespace("default").Create(ctx, testPod("pod1"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	buf := d.Buffer()
	if len(buf) == 0 {
		t.Fatal("expected buffered event before Tick")
	}

	d.Tick()

	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("expected Added, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("expected event after Tick")
	}

	if len(d.Buffer()) != 0 {
		t.Fatal("expected empty buffer after Tick")
	}
}

func TestDSK_DeleteBuffersDeletedEvent(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)
	client := d.Dynamic(underlying)
	ctx := t.Context()

	// Open watch before writes so all events are captured.
	w, err := client.Resource(podsGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Create through the wrapped client (so preExpectWrite fires).
	_, err = client.Resource(podsGVR).Namespace("default").Create(ctx, testPod("pod1"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Deliver the ADDED event.
	d.Tick()
	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("expected Added, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("timeout waiting for Added event")
	}

	// Delete through the wrapped client — should buffer a DELETED event.
	err = client.Resource(podsGVR).Namespace("default").Delete(ctx, "pod1", metav1.DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Buffer() must wait for the DELETED event to arrive before returning.
	buf := d.Buffer()
	if len(buf) != 1 {
		t.Fatalf("expected 1 buffered DELETED event, got %d", len(buf))
	}
	if buf[0].Type != watch.Deleted {
		t.Fatalf("expected Deleted, got %v", buf[0].Type)
	}
}

func TestDSK_IsSteady(t *testing.T) {
	d := dsk.New(t)
	q := d.WrapQueue(workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()))
	defer q.ShutDown()

	if !d.IsSteady() {
		t.Fatal("expected steady with empty queue and no buffered events")
	}
}

func TestDSK_FlushEmptyIsNoop(t *testing.T) {
	d := dsk.New(t)
	// Neither nil nor an empty slice should panic, block, or return an error.
	d.Flush(nil)
	d.Flush([]dsk.PendingEvent{})
}
