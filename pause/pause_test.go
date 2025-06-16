package pause

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/authzed/controller-idioms/conditions"
	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/queue/fake"
	"github.com/authzed/controller-idioms/typedctx"
)

type MyObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// this implements the conditions interface for MyObject, but note that
	// this is not supported by kube codegen at the moment (don't try to use
	// this in a real controller)
	conditions.StatusWithConditions[*MyObjectStatus] `json:"-"`
}

type MyObjectStatus struct {
	ObservedGeneration          int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
	conditions.StatusConditions `json:"conditions,omitempty"         patchMergeKey:"type"                            patchStrategy:"merge" protobuf:"bytes,1,rep,name=conditions"`
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
		func(_ context.Context, _ *MyObject) error {
			// update status
			return nil
		},
		handler.NoopHandler,
	), "checkPause")
	pauseHandler.Handle(ctx)
	// Output:
}

func TestPauseHandler(t *testing.T) {
	var nextKey handler.Key = "next"
	const PauseLabelKey = "com.my-controller/controller-paused"
	tests := []struct {
		name string

		obj        *MyObject
		patchError error

		expectNext        handler.Key
		expectEvents      []string
		expectPatchStatus bool
		expectConditions  []metav1.Condition
		expectRequeue     bool
		expectDone        bool
	}{
		{
			name: "pauses when label found",
			obj: &MyObject{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PauseLabelKey: "",
					},
				},
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{},
						},
					},
				},
			},
			expectPatchStatus: true,
			expectConditions:  []metav1.Condition{NewPausedCondition(PauseLabelKey)},
			expectDone:        true,
		},
		{
			name: "requeues on pause patch error",
			obj: &MyObject{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PauseLabelKey: "",
					},
				},
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{},
						},
					},
				},
			},
			patchError:        fmt.Errorf("error patching"),
			expectPatchStatus: true,
			expectRequeue:     true,
		},
		{
			name: "no-op when label found and status is already paused",
			obj: &MyObject{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PauseLabelKey: "",
					},
				},
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{NewPausedCondition(PauseLabelKey)},
						},
					},
				},
			},
			expectDone: true,
		},
		{
			name: "removes condition when label is removed",
			obj: &MyObject{
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{NewPausedCondition(PauseLabelKey)},
						},
					},
				},
			},
			expectPatchStatus: true,
			expectConditions:  []metav1.Condition{},
			expectNext:        nextKey,
		},
		{
			name: "removes self-pause condition when label is removed",
			obj: &MyObject{
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{NewSelfPausedCondition(PauseLabelKey)},
						},
					},
				},
			},
			expectPatchStatus: true,
			expectConditions:  []metav1.Condition{},
			expectNext:        nextKey,
		},
		{
			name: "requeues on unpause patch error",
			obj: &MyObject{
				StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{
					Status: &MyObjectStatus{
						StatusConditions: conditions.StatusConditions{
							Conditions: []metav1.Condition{NewPausedCondition(PauseLabelKey)},
						},
					},
				},
			},
			patchError:        fmt.Errorf("error patching"),
			expectPatchStatus: true,
			expectRequeue:     true,
		},
		{
			name:       "no-op, no pause label, no pause status",
			obj:        &MyObject{},
			expectNext: nextKey,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrls := &fake.FakeInterface{}
			patchCalled := false

			patchStatus := func(_ context.Context, patch *MyObject) error {
				patchCalled = true

				if tt.patchError != nil {
					return tt.patchError
				}

				require.Truef(t, slices.EqualFunc(tt.expectConditions, patch.Status.Conditions, func(a, b metav1.Condition) bool {
					return a.Type == b.Type &&
						a.Status == b.Status &&
						a.ObservedGeneration == b.ObservedGeneration &&
						a.Message == b.Message &&
						a.Reason == b.Reason
				}), "conditions not equal:\na: %#v\nb: %#v", tt.expectConditions, patch.Status.Conditions)

				return nil
			}
			queueOps := queue.NewQueueOperationsCtx()
			ctxMyObject := typedctx.WithDefault[*MyObject](nil)

			ctx := t.Context()
			ctx = queueOps.WithValue(ctx, ctrls)
			ctx = ctxMyObject.WithValue(ctx, tt.obj)
			var called handler.Key

			NewPauseContextHandler(queueOps.Key, PauseLabelKey, ctxMyObject, patchStatus, handler.ContextHandlerFunc(func(_ context.Context) {
				called = nextKey
			})).Handle(ctx)

			require.Equal(t, tt.expectPatchStatus, patchCalled)
			require.Equal(t, tt.expectNext, called)
			require.Equal(t, tt.expectRequeue, ctrls.RequeueAPIErrCallCount() == 1)
			require.Equal(t, tt.expectDone, ctrls.DoneCallCount() == 1)
		})
	}
}
