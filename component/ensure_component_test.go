package component

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/dynamic/dynamicinformer"
	clientfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/authzed/controller-idioms/conditions"
	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/hash"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/queue/fake"
	"github.com/authzed/controller-idioms/typed"
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

func TestEnsureServiceHandler(t *testing.T) {
	var (
		hashKey    = "example.com/component-hash"
		ownerIndex = "owner"
	)
	tests := []struct {
		name string

		existingServices []runtime.Object

		expectRequeueErr error
		expectApply      bool
		expectDelete     bool
	}{
		{
			name:        "creates if no services",
			expectApply: true,
		},
		{
			name: "creates if no matching services",
			existingServices: []runtime.Object{&corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name:      "unrelated",
				Namespace: "test",
			}}},
			expectApply: true,
		},
		{
			name: "no-ops if one matching service",
			existingServices: []runtime.Object{
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{
					Name:      "unrelated",
					Namespace: "test",
				}},
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Labels: map[string]string{
						"example.com/component": "the-main-service-component",
					},
					Annotations: map[string]string{
						hashKey: "76251aaa1ff6c84f",
					},
				}},
			},
		},
		{
			name: "deletes extra services if a matching service exists",
			existingServices: []runtime.Object{&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Labels: map[string]string{
						"example.com/component": "the-main-service-component",
					},
					Annotations: map[string]string{
						hashKey: "76251aaa1ff6c84f",
					},
				},
			}, &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name:      "extra",
				Namespace: "test",
				Labels: map[string]string{
					"example.com/component": "the-main-service-component",
				},
			}}},
			expectDelete: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			ctrls := &fake.FakeInterface{}
			applyCalled := false
			deleteCalled := false

			serviceGVR := corev1.SchemeGroupVersion.WithResource("services")

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			client := clientfake.NewSimpleDynamicClient(scheme, tt.existingServices...)
			informerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
			require.NoError(t, informerFactory.ForResource(serviceGVR).Informer().AddIndexers(map[string]cache.IndexFunc{
				ownerIndex: func(_ interface{}) ([]string, error) {
					return []string{types.NamespacedName{Namespace: "test", Name: "owner"}.String()}, nil
				},
			}))
			informerFactory.Start(ctx.Done())
			informerFactory.WaitForCacheSync(ctx.Done())
			indexer := typed.NewIndexer[*corev1.Service](informerFactory.ForResource(serviceGVR).Informer().GetIndexer())
			ctxOwner := typedctx.WithDefault[types.NamespacedName](types.NamespacedName{Namespace: "test", Name: "owner"})
			queueOps := queue.NewQueueOperationsCtx()

			h := handler.NewHandler(NewEnsureComponentByHash(
				NewHashableComponent[*corev1.Service](
					NewIndexedComponent(
						indexer,
						ownerIndex,
						func(_ context.Context) labels.Selector {
							return labels.SelectorFromSet(map[string]string{
								"example.com/component": "the-main-service-component",
							})
						}),
					hash.NewObjectHash(), hashKey),
				ctxOwner,
				queueOps,
				func(_ context.Context, sac *applycorev1.ServiceApplyConfiguration) (*corev1.Service, error) {
					applyCalled = true
					fmt.Print(sac.Annotations)
					return nil, nil
				},
				func(_ context.Context, _ types.NamespacedName) error {
					deleteCalled = true
					return nil
				},
				func(_ context.Context) *applycorev1.ServiceApplyConfiguration {
					return applycorev1.Service("test", "test").
						WithLabels(map[string]string{
							"example.com/component": "the-main-service-component",
						}).
						WithSpec(applycorev1.ServiceSpec().WithType(corev1.ServiceTypeClusterIP))
				}), "ensureService")

			h.Handle(ctx)

			require.Equal(t, tt.expectApply, applyCalled)
			require.Equal(t, tt.expectDelete, deleteCalled)
			if tt.expectRequeueErr != nil {
				require.Equal(t, 1, ctrls.RequeueErrCallCount())
				require.Equal(t, tt.expectRequeueErr, ctrls.RequeueErrArgsForCall(0))
			}
		})
	}
}

// TestEnsureComponentByHashStopsWhenNewObjSignalsRequeue guards against a panic
// where a newObj factory both calls a queue operation (e.g. RequeueAPIErr) and
// returns nil. RequeueAPIErr cancels the handler context but does not unwind
// the goroutine, so without a ctx.Err() check the framework would dereference
// the nil to attach a hash annotation and segfault. Reproduces the panic
// observed at authzed/internal#10787.
func TestEnsureComponentByHashStopsWhenNewObjSignalsRequeue(t *testing.T) {
	const (
		hashKey    = "example.com/component-hash"
		ownerIndex = "owner"
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	serviceGVR := corev1.SchemeGroupVersion.WithResource("services")
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	client := clientfake.NewSimpleDynamicClient(scheme)
	informerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	require.NoError(t, informerFactory.ForResource(serviceGVR).Informer().AddIndexers(map[string]cache.IndexFunc{
		ownerIndex: func(_ interface{}) ([]string, error) {
			return []string{types.NamespacedName{Namespace: "test", Name: "owner"}.String()}, nil
		},
	}))
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())
	indexer := typed.NewIndexer[*corev1.Service](informerFactory.ForResource(serviceGVR).Informer().GetIndexer())
	ctxOwner := typedctx.WithDefault[types.NamespacedName](types.NamespacedName{Namespace: "test", Name: "owner"})

	queueOps := queue.NewQueueOperationsCtx()

	// Simulate the manager plumbing: a real Operations whose cancel func
	// cancels the handler-scoped context, so RequeueAPIErr propagates via
	// ctx.Err() the same way it does in production.
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	defer cancelHandler()
	ops := queue.NewOperations(func() {}, func(time.Duration) {}, cancelHandler)
	handlerCtx = queueOps.WithValue(handlerCtx, ops)

	simulatedErr := errors.New("simulated upstream failure")
	applyCalled := false
	deleteCalled := false

	h := handler.NewHandler(NewEnsureComponentByHash(
		NewHashableComponent[*corev1.Service](
			NewIndexedComponent(
				indexer,
				ownerIndex,
				func(_ context.Context) labels.Selector {
					return labels.SelectorFromSet(map[string]string{
						"example.com/component": "the-main-service-component",
					})
				}),
			hash.NewObjectHash(), hashKey),
		ctxOwner,
		queueOps,
		func(_ context.Context, _ *applycorev1.ServiceApplyConfiguration) (*corev1.Service, error) {
			applyCalled = true
			return nil, nil
		},
		func(_ context.Context, _ types.NamespacedName) error {
			deleteCalled = true
			return nil
		},
		func(ctx context.Context) *applycorev1.ServiceApplyConfiguration {
			queueOps.RequeueAPIErr(ctx, simulatedErr)
			return nil
		}), "ensureService")

	require.NotPanics(t, func() {
		h.Handle(handlerCtx)
	})

	require.False(t, applyCalled, "must not apply when newObj signaled requeue")
	require.False(t, deleteCalled, "must not delete when newObj signaled requeue")
	require.Equal(t, simulatedErr, ops.Error(), "Operations must record the requeue error")
	require.Error(t, handlerCtx.Err(), "handler context must be cancelled by RequeueAPIErr")
}
