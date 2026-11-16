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

type queueBuilder func(d *dsk.DSK, opts ...dsk.QueueOption) workqueue.TypedRateLimitingInterface[string]

// queueBuilders are the two ways to register a queue with DSK.
var queueBuilders = map[string]queueBuilder{
	"wrapped": func(d *dsk.DSK, opts ...dsk.QueueOption) workqueue.TypedRateLimitingInterface[string] {
		return d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()), opts...)
	},
	"built": func(d *dsk.DSK, opts ...dsk.QueueOption) workqueue.TypedRateLimitingInterface[string] {
		return d.NewRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{}, opts...)
	},
}

// forEachQueue runs body in a synctest bubble once per queueBuilder.
func forEachQueue(t *testing.T, body func(t *testing.T, newQueue queueBuilder)) {
	for name, newQueue := range queueBuilders {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { body(t, newQueue) })
		})
	}
}

// raceFixture is a controller in a synctest bubble whose single worker is held
// back until the first tick's window has closed, so its first Get always lands
// after closeWindow.
type raceFixture struct {
	d          *dsk.DSK
	q          workqueue.TypedRateLimitingInterface[string]
	reconciles atomic.Int32
	running    atomic.Int32
}

func newRaceFixture(t *testing.T, newQueue queueBuilder, opts []dsk.QueueOption, reconcile func(f *raceFixture, key string)) *raceFixture {
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
	f.q = newQueue(f.d, opts...)

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

func (f *raceFixture) settled(t *testing.T, after string, wantReconciles int32) {
	t.Helper()
	if n := f.running.Load(); n != 0 {
		t.Fatalf("%s returned with %d reconcile(s) still running", after, n)
	}
	if n := f.reconciles.Load(); n != wantReconciles {
		t.Fatalf("reconciles after %s: got %d, want %d", after, n, wantReconciles)
	}
}

// TickUntilSteady must not return while a reconcile is running, even when the
// worker dequeued the item after the tick window closed.
func TestTickUntilSteady_WaitsForGetAfterWindowCloses(t *testing.T) {
	forEachQueue(t, func(t *testing.T, newQueue queueBuilder) {
		f := newRaceFixture(t, newQueue, nil, func(*raceFixture, string) { time.Sleep(time.Second) })
		defer f.stop()
		f.d.TickUntilSteady(5)
		f.settled(t, "TickUntilSteady", 1)
	})
}

// An item re-added while it is being reconciled is handed out again after Done.
// TickUntilSteady must wait for that second reconcile too.
func TestTickUntilSteady_WaitsForReAddWhileProcessing(t *testing.T) {
	forEachQueue(t, func(t *testing.T, newQueue queueBuilder) {
		f := newRaceFixture(t, newQueue, nil, func(f *raceFixture, key string) {
			if f.reconciles.Load() == 0 {
				f.q.Add(key)
			}
			time.Sleep(time.Second)
		})
		defer f.stop()
		f.d.TickUntilSteady(5)
		f.settled(t, "TickUntilSteady", 2)
	})
}

// A requeue that comes due goes into the queue through the delaying queue's
// own Add. Advance must still wait for its reconcile.
func TestAdvance_WaitsForSurfacedRequeue(t *testing.T) {
	forEachQueue(t, func(t *testing.T, newQueue queueBuilder) {
		f := newRaceFixture(t, newQueue, nil, func(f *raceFixture, key string) {
			if f.reconciles.Load() == 0 {
				f.q.AddAfter(key, time.Minute)
			}
			time.Sleep(time.Second)
		})
		defer f.stop()
		f.d.TickUntilSteady(5)
		f.settled(t, "TickUntilSteady", 1)
		f.d.Advance(time.Minute)
		f.settled(t, "Advance", 2)
	})
}

// A rate-limited requeue on a queue built by NewRateLimitingQueue is waited
// for like any other.
func TestAdvance_WaitsForRateLimitedRequeue(t *testing.T) {
	// A first delay of a minute, so the requeue comes due after the first
	// reconcile has finished.
	slow := func(d *dsk.DSK, opts ...dsk.QueueOption) workqueue.TypedRateLimitingInterface[string] {
		rl := workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Minute, time.Hour)
		return d.NewRateLimitingQueue(rl, workqueue.TypedRateLimitingQueueConfig[string]{}, opts...)
	}
	synctest.Test(t, func(t *testing.T) {
		f := newRaceFixture(t, slow, nil, func(f *raceFixture, key string) {
			if f.reconciles.Load() == 0 {
				f.q.AddRateLimited(key)
			}
			time.Sleep(time.Second)
		})
		defer f.stop()
		f.d.TickUntilSteady(5)
		f.settled(t, "TickUntilSteady", 1)
		dur, ok := f.d.NextRequeue()
		if !ok {
			t.Fatal("expected NextRequeue to see the rate-limited requeue's deadline")
		}
		f.d.Advance(dur)
		f.settled(t, "Advance", 2)
	})
}

// With Drained, Tick itself waits for a worker that reaches Get only after the
// window closes.
func TestTick_DrainedWaitsForLateWorker(t *testing.T) {
	forEachQueue(t, func(t *testing.T, newQueue queueBuilder) {
		f := newRaceFixture(t, newQueue, []dsk.QueueOption{dsk.Drained()}, func(*raceFixture, string) { time.Sleep(time.Second) })
		defer f.stop()
		f.d.Tick()
		f.settled(t, "Tick", 1)
	})
}
