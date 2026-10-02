//go:build dsk

package dsk_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
)

var reactorCmGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

// TestReactor_BuffersWatchEventsForAllResourceTypes verifies that
// d.Dynamic(fake) buffers events for arbitrary resource types, not just pods.
func TestReactor_BuffersWatchEventsForAllResourceTypes(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)
	client := d.Dynamic(underlying)

	ctx := t.Context()
	w, err := client.Resource(reactorCmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	_, err = client.Resource(reactorCmGVR).Namespace("default").Create(ctx,
		testConfigMap("cm1"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Event must be buffered, not immediately delivered.
	select {
	case <-w.ResultChan():
		t.Fatal("event should be buffered until Tick")
	default:
	}

	d.Tick()

	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("want Added, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("event not delivered after Tick")
	}
}

func TestReactor_ApplyBuffersWatchEvent(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)
	client := d.Dynamic(underlying)

	ctx := t.Context()
	w, err := client.Resource(reactorCmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Apply bypasses the reactor chain in client/fake — applyInterceptor must
	// call expectRV so that Buffer() waits for the resulting watch event.
	_, err = client.Resource(reactorCmGVR).Namespace("default").Apply(ctx, "cm1",
		&unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "cm1", "namespace": "default"},
			},
		}, metav1.ApplyOptions{FieldManager: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Buffer() must not return until the Apply watch event has arrived.
	buf := d.Buffer()
	if len(buf) == 0 {
		t.Fatal("expected Apply watch event in buffer")
	}
	d.Flush(buf)

	select {
	case ev := <-w.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("want Added, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("event not delivered after Flush")
	}
}
