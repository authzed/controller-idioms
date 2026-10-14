//go:build dsk

package dsk_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	dsk "github.com/authzed/controller-idioms/dsk"
)

func TestTypedWrapper_BuffersWatchEvents(t *testing.T) {
	underlying := k8sfake.NewSimpleClientset()
	d := dsk.New(t)
	client := d.Typed(underlying)

	ctx := t.Context()

	w, err := client.CoreV1().Pods("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Write through the DSK-wrapped client so expectRV fires.
	_, err = client.CoreV1().Pods("default").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Event must be buffered, not immediately delivered.
	select {
	case <-w.ResultChan():
		t.Fatal("expected event to be buffered, not delivered")
	default:
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
}
