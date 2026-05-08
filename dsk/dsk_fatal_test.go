//go:build dsk

package dsk_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	dsk "github.com/authzed/controller-idioms/dsk"
)

// fakeTB implements dsk.TB for testing fatal-path behavior without stopping
// the test goroutine. Fatalf/Fatal record messages instead of calling Goexit.
type fakeTB struct {
	ctx      context.Context
	messages []string
}

func (f *fakeTB) Context() context.Context { return f.ctx }
func (f *fakeTB) Fatal(args ...any)        { f.messages = append(f.messages, fmt.Sprint(args...)) }
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}
func (f *fakeTB) Errorf(format string, args ...any) {}
func (f *fakeTB) Logf(format string, args ...any)   {}

func (f *fakeTB) hasFatal() bool { return len(f.messages) > 0 }
func (f *fakeTB) lastMessage() string {
	if len(f.messages) == 0 {
		return ""
	}
	return f.messages[len(f.messages)-1]
}

// nonReactableDynamic satisfies dynamic.Interface but not the dsk reactable interface.
type nonReactableDynamic struct{ dynamic.Interface }

// nonReactableTyped satisfies kubernetes.Interface but not the dsk reactable interface.
type nonReactableTyped struct{ kubernetes.Interface }

func TestDSK_Dynamic_UnsupportedClientCallsFatalf(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Dynamic panicked instead of calling Fatalf: %v", r)
			}
		}()
		d.Dynamic(&nonReactableDynamic{})
	}()

	if !ftb.hasFatal() {
		t.Fatal("expected Fatalf to be called for unsupported dynamic client")
	}
}

func TestDSK_Typed_UnsupportedClientCallsFatalf(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Typed panicked instead of calling Fatalf: %v", r)
			}
		}()
		d.Typed(&nonReactableTyped{})
	}()

	if !ftb.hasFatal() {
		t.Fatal("expected Fatalf to be called for unsupported typed client")
	}
}

func TestDSK_TickUntilSteady_FatalsOnZeroN(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)

	d.TickUntilSteady(0)

	if !ftb.hasFatal() {
		t.Fatal("expected Fatalf to be called for n=0")
	}
	if !strings.Contains(ftb.lastMessage(), "n > 0") {
		t.Errorf("expected 'n > 0' in fatal message, got %q", ftb.lastMessage())
	}
}

func TestDSK_TickUntilSteady_FatalsIfNeverSteady(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)

	underlying := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	defer underlying.ShutDown()
	q := d.WrapQueue(underlying)

	// Add an item that is never Get'd — queue stays non-idle every tick.
	q.Add("stuck-key")

	d.TickUntilSteady(2)

	if !ftb.hasFatal() {
		t.Fatal("expected Fatalf when controller never reaches steady state")
	}
	if !strings.Contains(ftb.lastMessage(), "steady state") {
		t.Errorf("expected 'steady state' in fatal message, got %q", ftb.lastMessage())
	}
}
