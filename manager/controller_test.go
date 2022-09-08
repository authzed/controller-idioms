package manager

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
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
	controller := NewOwnedResourceController(klogr.New(), "my-controller", gvr, CtxQueue, registry, broadcaster, func(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		// process object
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":", broadcaster, eventSink)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	_ = mgr.Start(ctx, controller)
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
	eventSink := &typedcorev1.EventSinkImpl{Interface: fake.NewSimpleClientset().CoreV1().Events("")}

	controller := NewOwnedResourceController(klogr.New(), "my-controller", gvr, CtxQueue, registry, broadcaster, func(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) {
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":", broadcaster, eventSink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = mgr.Start(ctx, controller)
	}()

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
