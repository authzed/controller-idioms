//go:build dsk

// dsk/integration_test.go
package dsk_test

import (
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/authzed/controller-idioms/client/fake"
	dsk "github.com/authzed/controller-idioms/dsk"
	typed "github.com/authzed/controller-idioms/typed"
)

var cmGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

func TestIntegration_SteadyStateDetection(t *testing.T) {
	// Disable WatchListClient so the informer uses traditional List+Watch.
	// The fake object tracker does not support the watchList protocol
	// (sendInitialEvents / BOOKMARK), so watchList hangs indefinitely.
	t.Setenv("KUBE_FEATURE_WatchListClient", "false")

	s := runtime.NewScheme()
	corev1.AddToScheme(s)
	underlying := fake.NewClient(s)

	d := dsk.New(t)
	client := d.Dynamic(underlying)
	registry := dsk.NewRegistry(d)

	factory := registry.MustNewFilteredDynamicSharedInformerFactory(
		typed.NewFactoryKey("test", "cluster", "default"),
		client, 0, "default", nil,
	)

	q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	wrappedQ := d.WrapQueue(q)
	defer wrappedQ.ShutDown()

	var processed atomic.Int32

	inf := factory.ForResource(cmGVR).Informer()
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			u := obj.(*unstructured.Unstructured)
			wrappedQ.Add(u.GetName())
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	// Informer has synced against an empty store; watch is now open.

	// Worker starts here, blocked on Get (queue is empty). When Tick delivers
	// the Added event below, AddFunc signals the workqueue cond and the worker
	// unblocks inside the tick's open window — so the tick tracks Get→Done and
	// waits for the worker before declaring the tick complete.
	requeued := make(map[string]bool)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			key, quit := wrappedQ.Get()
			if quit {
				return
			}
			processed.Add(1)
			name := key.(string)
			firstTime := !requeued[name]
			if firstTime {
				requeued[name] = true
				wrappedQ.AddAfter(name, 0)
			}
			wrappedQ.Done(key)
			if !firstTime {
				return
			}
		}
	}()

	// Create the configmap AFTER the watch is open so the Added event is
	// buffered by DSK. Tick delivers it, AddFunc fires (tracked by flush via
	// pendingCalls), and the worker — already blocked on an empty queue —
	// unblocks inside the tick window rather than racing against a pre-loaded queue.
	_, err = client.Resource(cmGVR).Namespace("default").Create(ctx, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]interface{}{"name": "cm1", "namespace": "default"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	d.TickUntilSteady(20)
	<-workerDone

	if processed.Load() < 2 {
		t.Fatalf("expected processed >= 2, got %d", processed.Load())
	}
}

func TestIntegration_OrderingMatters(t *testing.T) {
	// Two watchers A and B both open before an object is created.
	// Flush delivers events one at a time; we verify that the second watcher
	// does not receive its event until after the first flush.
	// This exercises DSK's selective Flush capability.
	underlying := fake.NewClient(runtime.NewScheme())
	d := dsk.New(t)
	client := d.Dynamic(underlying)

	ctx := t.Context()

	// Open both watches BEFORE creating the object so that creation triggers
	// an Added event on each watch stream.
	watchA, err := client.Resource(cmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	watchB, err := client.Resource(cmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer watchA.Stop()
	defer watchB.Stop()

	// Create the object after both watches are open. Writing through the DSK-wrapped
	// client so Buffer() automatically waits for watch events to arrive before returning.
	_, err = client.Resource(cmGVR).Namespace("default").Create(ctx, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]interface{}{"name": "shared", "namespace": "default"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	buf := d.Buffer()
	// Two pending events: one per watch connection.
	if len(buf) < 2 {
		t.Fatalf("expected >=2 buffered events, got %d", len(buf))
	}

	// Flush only the first buffered event. One of the two watchers will receive it.
	d.Flush(buf[:1])

	// Exactly one watcher should have received the event; the other should not have.
	// We don't know which buffer slot maps to which watcher, so we check both.
	var firstToReceive, secondWatcher watch.Interface
	select {
	case ev := <-watchA.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("A expected Added, got %v", ev.Type)
		}
		firstToReceive = watchA
		secondWatcher = watchB
	case ev := <-watchB.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("B expected Added, got %v", ev.Type)
		}
		firstToReceive = watchB
		secondWatcher = watchA
	case <-t.Context().Done():
		t.Fatal("expected one watcher to receive event after partial flush")
	}
	_ = firstToReceive

	// The second watcher should NOT have received its event yet.
	select {
	case <-secondWatcher.ResultChan():
		t.Fatal("second watcher should not have received event yet")
	default:
	}

	// Now flush the remaining buffered event.
	d.Flush(d.Buffer())

	select {
	case ev := <-secondWatcher.ResultChan():
		if ev.Type != watch.Added {
			t.Fatalf("second watcher expected Added, got %v", ev.Type)
		}
	case <-t.Context().Done():
		t.Fatal("second watcher expected event after second flush")
	}
}

func TestDSK_Registry_InformerHandlerTracking(t *testing.T) {
	// Verifies that dsk.Registry wraps informer factories so that
	// AddEventHandler callbacks go through trackedHandler. The informer
	// pipeline is asynchronous (deliver → reflector → deltaFIFO → handler),
	// so after Tick delivers the event we poll for the handler to fire.

	// Disable WatchListClient so the informer uses traditional List+Watch.
	// The fake object tracker does not support the watchList protocol
	// (sendInitialEvents / BOOKMARK), so watchList hangs indefinitely.
	t.Setenv("KUBE_FEATURE_WatchListClient", "false")

	s := runtime.NewScheme()
	// Register core v1 types so the fake client's List mapper knows about
	// ConfigMapList. Without this, the informer's initial List panics.
	corev1.AddToScheme(s)
	underlying := fake.NewClient(s)

	d := dsk.New(t)
	client := d.Dynamic(underlying)
	registry := dsk.NewRegistry(d)

	factory := registry.MustNewFilteredDynamicSharedInformerFactory(
		typed.NewFactoryKey("test", "cluster", "default"),
		client,
		0,
		"default",
		nil,
	)

	q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	wrappedQ := d.WrapQueue(q)
	defer wrappedQ.ShutDown()

	var handlerCalled atomic.Int32
	inf := factory.ForResource(cmGVR).Informer()
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			handlerCalled.Add(1)
			u := obj.(*unstructured.Unstructured)
			wrappedQ.Add(u.GetName())
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Create a configmap through the DSK-wrapped client.
	_, err = client.Resource(cmGVR).Namespace("default").Create(ctx, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]interface{}{"name": "tracked", "namespace": "default"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Tick delivers the buffered event and waits for all AddEventHandler
	// callbacks to complete before returning. dsk.NewRegistry registers handler
	// counts so flush() credits pendingCalls before delivery — waitForHandlersIdle
	// blocks until the informer pipeline fires and finishes the callback. No
	// polling needed.
	d.Tick()

	if handlerCalled.Load() == 0 {
		t.Fatal("handler was never called — Registry wrapping may be broken")
	}

	// Drain the queue and verify steady state.
	item, shutdown := wrappedQ.Get()
	if shutdown {
		t.Fatal("queue shut down unexpectedly")
	}
	wrappedQ.Done(item)

	if !d.IsSteady() {
		t.Fatal("expected steady after item drained")
	}
}
