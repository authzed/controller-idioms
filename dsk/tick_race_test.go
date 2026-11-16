//go:build dsk

package dsk_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
	typed "github.com/authzed/controller-idioms/typed"
)

// raceFixture is a controller in a synctest bubble whose single worker is held
// back until the first tick's window has closed, so its first Get always lands
// after closeWindow.
type raceFixture struct {
	d          *dsk.DSK
	q          workqueue.TypedRateLimitingInterface[string]
	reconciles atomic.Int32
	running    atomic.Int32
}

func newRaceFixture(t *testing.T, reconcile func(f *raceFixture, key string)) *raceFixture {
	t.Helper()
	// The fake object tracker does not support the watchList protocol.
	t.Setenv("KUBE_FEATURE_WatchListClient", "false")

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	f := &raceFixture{d: dsk.New(t)}
	client := f.d.Dynamic(fake.NewClient(s))
	factory := dsk.NewRegistry(f.d).MustNewFilteredDynamicSharedInformerFactory(
		typed.NewFactoryKey("test", "cluster", "default"),
		client, 0, "default", nil,
	)
	// Callers defer f.stop: the worker must exit before the bubble's main
	// goroutine returns.
	f.q = f.d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()))

	inf := factory.ForResource(cmGVR).Informer()
	if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { f.q.Add(obj.(*unstructured.Unstructured).GetName()) },
	}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	synctest.Wait()

	release := make(chan struct{})
	var once sync.Once
	dsk.ExportAfterCloseWindow(f.d, func() { once.Do(func() { close(release) }) })
	go func() {
		<-release
		for {
			key, quit := f.q.Get()
			if quit {
				return
			}
			f.running.Add(1)
			reconcile(f, key)
			f.reconciles.Add(1)
			f.running.Add(-1)
			f.q.Done(key)
		}
	}()

	if _, err := client.Resource(cmGVR).Namespace("default").Create(ctx, testConfigMap("cm1"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return f
}

// stop shuts the queue down, then lets virtual time run so that a reconcile
// still running (after a failed assertion) can finish and the worker can exit.
func (f *raceFixture) stop() {
	f.q.ShutDown()
	time.Sleep(time.Hour)
}

// TickUntilSteady must not return while a reconcile is running, even when the
// worker dequeued the item after the tick window closed.
func TestTickUntilSteady_WaitsForGetAfterWindowCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRaceFixture(t, func(*raceFixture, string) { time.Sleep(time.Second) })
		defer f.stop()
		f.d.TickUntilSteady(5)
		if n := f.running.Load(); n != 0 {
			t.Fatalf("TickUntilSteady returned with %d reconcile(s) still running", n)
		}
		if n := f.reconciles.Load(); n != 1 {
			t.Fatalf("reconciles: got %d, want 1", n)
		}
	})
}

// An item re-added while it is being reconciled is handed out again after Done.
// TickUntilSteady must wait for that second reconcile too.
func TestTickUntilSteady_WaitsForReAddWhileProcessing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRaceFixture(t, func(f *raceFixture, key string) {
			if f.reconciles.Load() == 0 {
				f.q.Add(key)
			}
			time.Sleep(time.Second)
		})
		defer f.stop()
		f.d.TickUntilSteady(5)
		if n := f.running.Load(); n != 0 {
			t.Fatalf("TickUntilSteady returned with %d reconcile(s) still running", n)
		}
		if n := f.reconciles.Load(); n != 2 {
			t.Fatalf("reconciles: got %d, want 2", n)
		}
	})
}

// A requeue that surfaces from the delay heap goes into the queue without
// passing through the wrapper's Add. Advance must still wait for its reconcile.
func TestAdvance_WaitsForSurfacedRequeue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRaceFixture(t, func(f *raceFixture, key string) {
			if f.reconciles.Load() == 0 {
				f.q.AddAfter(key, time.Minute)
			}
			time.Sleep(time.Second)
		})
		defer f.stop()
		f.d.TickUntilSteady(5)
		if n := f.reconciles.Load(); n != 1 {
			t.Fatalf("reconciles after first tick: got %d, want 1", n)
		}
		f.d.Advance(time.Minute)
		if n := f.running.Load(); n != 0 {
			t.Fatalf("Advance returned with %d reconcile(s) still running", n)
		}
		if n := f.reconciles.Load(); n != 2 {
			t.Fatalf("reconciles after Advance: got %d, want 2", n)
		}
	})
}
