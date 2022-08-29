package pause

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/authzed/controller-idioms/conditions"
	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/typedctx"
)

type MyObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// this implements the conditions interface for MyObject, but note that
	// this is not supported by kube codegen at the moment (don't try to use
	// this in a real controller)
	conditions.StatusWithConditions[MyObjectStatus] `json:"-"`
}
type MyObjectStatus struct {
	ObservedGeneration          int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
	conditions.StatusConditions `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

func ExampleNewPauseContextHandler() {
	queueOperations := queue.NewQueueOperationsCtx()
	ctxObject := typedctx.WithDefault[*MyObject](&MyObject{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pauseHandler := handler.NewHandler(NewPauseContextHandler(
		queueOperations.Key,
		"example.com/paused",
		ctxObject,
		func(ctx context.Context, patch *MyObject) error {
			// update status
			return nil
		},
		handler.NoopHandler,
	), "checkPause")
	pauseHandler.Handle(ctx)
	// Output:
}
