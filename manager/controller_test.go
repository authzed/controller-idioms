package manager

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	ctrlmanageropts "k8s.io/controller-manager/options"

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
	controller := NewOwnedResourceController("my-controller", gvr, CtxQueue, registry, broadcaster, func(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		// process object
	})

	mgr := NewManager(ctrlmanageropts.RecommendedDebuggingOptions().DebuggingConfiguration, ":", broadcaster, eventSink)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	_ = mgr.Start(ctx, controller)
	// Output:
}
