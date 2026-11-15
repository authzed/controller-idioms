//go:build dsk

package dsk_test

import (
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

// A controller that requeues with AddAfter(5m) is steady with a requeue
// pending; Advance(5m) fires the requeue and the controller reconciles again.
func TestAdvance_ReconcilesAfterRequeueDelay(t *testing.T) {
	// The fake object tracker does not support the watchList protocol.
	t.Setenv("KUBE_FEATURE_WatchListClient", "false")

	synctest.Test(t, func(t *testing.T) {
		s := runtime.NewScheme()
		if err := corev1.AddToScheme(s); err != nil {
			t.Fatal(err)
		}

		// New, the queue and the controller all live inside the bubble.
		d := dsk.New(t)
		client := d.Dynamic(fake.NewClient(s))
		factory := dsk.NewRegistry(d).MustNewFilteredDynamicSharedInformerFactory(
			typed.NewFactoryKey("test", "cluster", "default"),
			client, 0, "default", nil,
		)

		q := d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()))
		defer q.ShutDown()

		inf := factory.ForResource(cmGVR).Informer()
		if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) { q.Add(obj.(*unstructured.Unstructured).GetName()) },
		}); err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		factory.Start(ctx.Done())
		factory.WaitForCacheSync(ctx.Done())
		// The reflector opens its watch after the initial list that
		// WaitForCacheSync waits for. Let it block on that watch before the
		// Create below, or the Added event can fall between list and watch.
		synctest.Wait()

		// The controller requeues each key once, five minutes out.
		var reconciles atomic.Int32
		go func() {
			for {
				key, quit := q.Get()
				if quit {
					return
				}
				if reconciles.Add(1) == 1 {
					q.AddAfter(key, 5*time.Minute)
				}
				q.Done(key)
			}
		}()

		if _, err := client.Resource(cmGVR).Namespace("default").Create(ctx, testConfigMap("cm1"), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		d.TickUntilSteady(5)
		// A worker whose Get lands after the tick window closes is not tracked,
		// so TickUntilSteady can return before the reconcile finishes. Settle
		// the bubble before counting. (Advance does this itself.)
		synctest.Wait()

		if got := reconciles.Load(); got != 1 {
			t.Fatalf("reconciles after first tick: got %d, want 1", got)
		}
		if !d.HasPendingRequeues() {
			t.Fatal("expected a pending requeue after AddAfter(5m)")
		}
		dur, ok := d.NextRequeue()
		if !ok || dur != 5*time.Minute {
			t.Fatalf("NextRequeue: got (%v, %v), want (5m, true)", dur, ok)
		}

		// One minute short of the deadline, nothing fires.
		d.Advance(4 * time.Minute)
		if got := reconciles.Load(); got != 1 {
			t.Fatalf("reconciles after 4m: got %d, want 1", got)
		}
		if dur, ok := d.NextRequeue(); !ok || dur != time.Minute {
			t.Fatalf("NextRequeue after 4m: got (%v, %v), want (1m, true)", dur, ok)
		}

		d.Advance(time.Minute)
		if got := reconciles.Load(); got != 2 {
			t.Fatalf("reconciles after 5m: got %d, want 2", got)
		}
		if !d.IsSteady() || d.HasPendingRequeues() {
			t.Fatalf("expected truly steady: IsSteady=%v HasPendingRequeues=%v", d.IsSteady(), d.HasPendingRequeues())
		}
	})
}

// IsSteady alone cannot see delayed requeues; HasPendingRequeues and
// NextRequeue can.
func TestAdvance_SteadyVersusPendingRequeue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := dsk.New(t)
		q := d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()))
		defer q.ShutDown()

		if !d.IsSteady() || d.HasPendingRequeues() {
			t.Fatal("expected truly steady before any requeue")
		}
		if _, ok := d.NextRequeue(); ok {
			t.Fatal("expected no next requeue before any requeue")
		}

		q.AddAfter("a", 2*time.Minute)
		q.AddAfter("a", time.Minute) // earlier deadline wins, as in the delaying queue
		q.AddAfter("b", 3*time.Minute)
		if !d.IsSteady() {
			t.Fatal("IsSteady should not see items in the delay heap")
		}
		if !d.HasPendingRequeues() {
			t.Fatal("expected pending requeues")
		}
		if dur, ok := d.NextRequeue(); !ok || dur != time.Minute {
			t.Fatalf("NextRequeue: got (%v, %v), want (1m, true)", dur, ok)
		}

		// A Get of "a" before its deadline comes from another Add and does not
		// resolve the delayed requeue.
		q.Add("a")
		item, _ := q.Get()
		q.Done(item)
		if dur, ok := d.NextRequeue(); !ok || dur != time.Minute {
			t.Fatalf("NextRequeue after early Get: got (%v, %v), want (1m, true)", dur, ok)
		}

		// No worker runs, so "a" surfaces in the queue and stays there.
		d.Advance(time.Minute)
		if d.IsSteady() {
			t.Fatal("expected not steady: requeued item is now in the queue")
		}
		item, _ = q.Get()
		if item != "a" {
			t.Fatalf("got %q, want a", item)
		}
		q.Done(item)
		if !d.IsSteady() || !d.HasPendingRequeues() {
			t.Fatalf("expected steady with b pending: IsSteady=%v HasPendingRequeues=%v", d.IsSteady(), d.HasPendingRequeues())
		}
		if dur, ok := d.NextRequeue(); !ok || dur != 2*time.Minute {
			t.Fatalf("NextRequeue for b: got (%v, %v), want (2m, true)", dur, ok)
		}

		d.Advance(2 * time.Minute)
		item, _ = q.Get()
		q.Done(item)
		if !d.IsSteady() || d.HasPendingRequeues() {
			t.Fatalf("expected truly steady: IsSteady=%v HasPendingRequeues=%v", d.IsSteady(), d.HasPendingRequeues())
		}
	})
}

// AddRateLimited requeues are pending but have no deadline DSK can see.
func TestAdvance_RateLimitedRequeueHasNoDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := dsk.New(t)
		q := d.WrapQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[any]()))
		defer q.ShutDown()

		q.AddRateLimited("a")
		if !d.HasPendingRequeues() {
			t.Fatal("expected a pending rate-limited requeue")
		}
		if _, ok := d.NextRequeue(); ok {
			t.Fatal("rate-limited requeues have no known deadline")
		}

		// The default controller rate limiter's first delay is 5ms.
		d.Advance(time.Second)
		item, _ := q.Get()
		q.Done(item)
		if !d.IsSteady() || d.HasPendingRequeues() {
			t.Fatalf("expected truly steady: IsSteady=%v HasPendingRequeues=%v", d.IsSteady(), d.HasPendingRequeues())
		}
	})
}

func TestAdvance_ShutDownClearsPendingRequeues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := dsk.New(t)
		q := d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()))
		q.AddAfter("a", time.Hour)
		q.ShutDown()
		if d.HasPendingRequeues() {
			t.Fatal("expected ShutDown to clear pending requeues")
		}
	})
}

func TestAdvance_OutsideBubbleCallsFatal(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)
	d.Advance(time.Hour) // must not sleep for real
	if !ftb.hasFatal() {
		t.Fatal("expected Fatal when Advance is called outside a synctest bubble")
	}
}

func TestAdvance_NegativeDurationCallsFatal(t *testing.T) {
	ftb := &fakeTB{ctx: t.Context()}
	d := dsk.New(ftb)
	d.Advance(-time.Second)
	if !ftb.hasFatal() {
		t.Fatal("expected Fatal for a negative duration")
	}
}
