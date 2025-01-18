package manager

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	ctrlmanageropts "k8s.io/controller-manager/options"
	"k8s.io/klog/v2/klogr"

	"github.com/authzed/controller-idioms/cachekeys"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/typed"
)

func ExampleNewOwnedResourceController() {
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "mytypes",
	}
	CtxQueue := queue.NewQueueOperationsCtx()
	registry := typed.NewRegistry()
	broadcaster := record.NewBroadcaster()
	eventSink := &typedcorev1.EventSinkImpl{Interface: fake.NewSimpleClientset().CoreV1().Events("")}

	// the controller processes objects on the queue, but doesn't set up any
	// informers by default.
	controller := NewOwnedResourceController(klogr.New(), "my-controller", gvr, CtxQueue, registry, broadcaster, func(_ context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		fmt.Println("processing", gvr, namespace, name)
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":", broadcaster, eventSink)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	readyc := make(chan struct{})
	_ = mgr.Start(ctx, readyc, controller)
	<-readyc
	// Output:
}

func TestControllerQueueDone(t *testing.T) {
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "mytypes",
	}
	CtxQueue := queue.NewQueueOperationsCtx()
	registry := typed.NewRegistry()
	broadcaster := record.NewBroadcaster()
	eventSink := newFakeEventSink()

	controller := NewOwnedResourceController(klogr.New(), "my-controller", gvr, CtxQueue, registry, broadcaster, func(_ context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		fmt.Println("processing", gvr, namespace, name)
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":", broadcaster, eventSink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyc := make(chan struct{})
	go func() {
		_ = mgr.Start(ctx, readyc, controller)
	}()
	<-readyc

	// add many keys
	for i := 0; i < 10; i++ {
		controller.Queue.Add(cachekeys.GVRMetaNamespaceKeyer(gvr, fmt.Sprintf("test/%d", i)))
	}
	require.Eventually(t, func() bool {
		return controller.Queue.Len() == 0
	}, 1*time.Second, 1*time.Millisecond)

	// add the same key many times
	for i := 0; i < 10; i++ {
		controller.Queue.Add(cachekeys.GVRMetaNamespaceKeyer(gvr, "test/a"))
	}
	require.Eventually(t, func() bool {
		return controller.Queue.Len() == 0
	}, 1*time.Second, 1*time.Millisecond)
}

func TestControllerEventsBroadcast(t *testing.T) {
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "mytypes",
	}
	CtxQueue := queue.NewQueueOperationsCtx()
	registry := typed.NewRegistry()
	broadcaster := record.NewBroadcaster()
	eventSink := newFakeEventSink()
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "my-controller"})

	controller := NewOwnedResourceController(klogr.New(), "my-controller", gvr, CtxQueue, registry, broadcaster, func(_ context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		fmt.Println("processing", gvr, namespace, name)
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":8888", broadcaster, eventSink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyc := make(chan struct{})
	go func() {
		_ = mgr.Start(ctx, readyc, controller)
	}()
	<-readyc
	require.Eventually(t, healthCheckPassing(), 1*time.Second, 50*time.Millisecond)

	recorder.Event(&v1.ObjectReference{Namespace: "test", Name: "a"}, v1.EventTypeNormal, "test", "test")

	require.Eventually(t, func() bool {
		return len(eventSink.Events) > 0
	}, 5*time.Second, 1*time.Millisecond)
}

type fakeEventSink struct {
	Events map[types.UID]*v1.Event
}

func newFakeEventSink() *fakeEventSink {
	return &fakeEventSink{
		Events: make(map[types.UID]*v1.Event),
	}
}

func (f *fakeEventSink) Create(event *v1.Event) (*v1.Event, error) {
	f.Events[event.UID] = event
	return event, nil
}

func (f *fakeEventSink) Update(event *v1.Event) (*v1.Event, error) {
	f.Events[event.UID] = event
	return event, nil
}

func (f *fakeEventSink) Patch(oldEvent *v1.Event, _ []byte) (*v1.Event, error) {
	f.Events[oldEvent.UID] = oldEvent
	return oldEvent, nil
}

func healthCheckPassing() func() bool {
	return func() bool {
		resp, err := http.Get("http://localhost:8888/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}
}
