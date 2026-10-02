//go:build dsk

package dsk_test

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
)

var deplGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

func TestFault_ErrorMatchingVerb(t *testing.T) {
	d := dsk.New(t)
	d.InjectFault(
		dsk.ErrorFault(errors.New("simulated error")).OnVerb("patch"),
	)
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	ctx := t.Context()
	_, err := client.Resource(deplGVR).Namespace("default").Patch(
		ctx, "foo", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{},
	)
	if err == nil || err.Error() != "simulated error" {
		t.Fatalf("expected simulated error, got %v", err)
	}
}

func TestFault_DoesNotMatchDifferentVerb(t *testing.T) {
	d := dsk.New(t)
	d.InjectFault(
		dsk.ErrorFault(errors.New("simulated error")).OnVerb("patch"),
	)
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	ctx := t.Context()
	// GET should not be faulted; the fake returns "not found" for a missing resource.
	_, err := client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected not-found error from fake")
	}
	if err.Error() == "simulated error" {
		t.Fatal("fault should not apply to get verb")
	}
}

func TestFault_DelayAddsLatency(t *testing.T) {
	d := dsk.New(t)
	d.InjectFault(
		dsk.DelayFault(50 * time.Millisecond).OnVerb("get"),
	)
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	ctx := t.Context()
	start := time.Now()
	client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Fatalf("expected delay >= 50ms, got %v", elapsed)
	}
}

func TestFault_HoldBlocksUntilRelease(t *testing.T) {
	d := dsk.New(t)
	hold, release := dsk.HoldFault()
	d.InjectFault(hold.OnVerb("get"))
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	done := make(chan struct{})
	go func() {
		defer close(done)
		client.Resource(deplGVR).Namespace("default").Get(t.Context(), "foo", metav1.GetOptions{})
	}()

	// Verify the call is blocked. synctest is not usable here: the hold fault
	// blocks in a channel select, and release() (which unblocks it) is called
	// from within the same scope, making the channel signable from the bubble —
	// preventing synctest from considering the goroutine durably blocked.
	select {
	case <-done:
		t.Fatal("expected call to be blocked before release")
	case <-time.After(20 * time.Millisecond):
	}

	release()

	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("call did not unblock after release")
	}
}

func TestFault_HoldReleaseIdempotent(t *testing.T) {
	_, release := dsk.HoldFault()
	// Calling release multiple times must not panic.
	release()
	release()
	release()
}

func TestFault_WithFaultsOption(t *testing.T) {
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t, dsk.WithFaults(
		dsk.ErrorFault(errors.New("option error")).OnVerb("get"),
	))
	client := d.Dynamic(underlying)

	ctx := t.Context()
	_, err := client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err == nil || err.Error() != "option error" {
		t.Fatalf("expected option error, got %v", err)
	}
}

func TestFault_RemoveStopsFault(t *testing.T) {
	d := dsk.New(t)
	h := d.InjectFault(
		dsk.ErrorFault(errors.New("simulated error")).OnVerb("get"),
	)
	d.RemoveFault(h)

	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	ctx := t.Context()
	_, err := client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err != nil && err.Error() == "simulated error" {
		t.Fatal("fault should not apply after removal")
	}
}

func TestFault_RemoveMiddleElement(t *testing.T) {
	d := dsk.New(t)
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)
	ctx := t.Context()

	// Inject three faults, all matching "get".
	d.InjectFault(dsk.ErrorFault(errors.New("first fault")).OnVerb("get"))
	h2 := d.InjectFault(dsk.ErrorFault(errors.New("second fault")).OnVerb("get"))
	d.InjectFault(dsk.ErrorFault(errors.New("third fault")).OnVerb("get"))

	// Remove the middle fault.
	d.RemoveFault(h2)

	// Third (last remaining) fault should fire first.
	_, err := client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err == nil || err.Error() != "third fault" {
		t.Fatalf("expected third fault after removing second, got: %v", err)
	}

	// Remove third fault.
	d.ClearFaults()
	d.InjectFault(dsk.ErrorFault(errors.New("first fault")).OnVerb("get"))

	// Only first fault remains; it should fire.
	_, err = client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err == nil || err.Error() != "first fault" {
		t.Fatalf("expected first fault after clearing, got: %v", err)
	}
}

func TestFault_LIFOOrdering(t *testing.T) {
	d := dsk.New(t)
	underlying := fake.NewClient(runtime.NewScheme())
	client := d.Dynamic(underlying)

	// Inject two faults that both match "get" on any resource.
	d.InjectFault(dsk.ErrorFault(errors.New("first fault")).OnVerb("get"))
	d.InjectFault(dsk.ErrorFault(errors.New("second fault")).OnVerb("get"))

	// The second (last-injected) fault should fire first (LIFO).
	ctx := t.Context()
	_, err := client.Resource(deplGVR).Namespace("default").Get(ctx, "foo", metav1.GetOptions{})
	if err == nil || err.Error() != "second fault" {
		t.Fatalf("expected last-injected fault to fire first, got: %v", err)
	}
}
