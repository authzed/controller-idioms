//go:build dsk

package dsk_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
)

func TestDynamicWrapper_BuffersWatchEvents(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)
	client := d.Dynamic(underlying)

	ctx := t.Context()
	w, err := client.Resource(podsGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	_, err = client.Resource(podsGVR).Namespace("default").Create(ctx, testPod("pod1"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Event should be buffered, not immediately delivered.
	select {
	case <-w.ResultChan():
		t.Fatal("expected event to be buffered, not delivered")
	default:
	}

	d.Tick()

	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("expected Added event, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("expected event after Tick")
	}
}
