package adopt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/queue/fake"
	"github.com/authzed/controller-idioms/typed"
	"github.com/authzed/controller-idioms/typedctx"
)

const (
	ManagedLabelKey       = "example.com/managed-by"
	ManagedLabelValue     = "example-controller"
	OwnerAnnotationPrefix = "example.com/owner-obj-"
	IndexName             = "owner-index"

	EventSecretAdopted = "SecretAdopedByOwner"
)

var (
	QueueOps    = queue.NewQueueOperationsCtx()
	CtxSecretNN = typedctx.WithDefault[types.NamespacedName](types.NamespacedName{})
	CtxOwnerNN  = typedctx.WithDefault[types.NamespacedName](types.NamespacedName{})
	CtxSecret   = typedctx.WithDefault[*corev1.Secret](nil)
)

func TestSecretAdopterHandler(t *testing.T) {
	type applyCall struct {
		called bool
		input  *applycorev1.SecretApplyConfiguration
		result *corev1.Secret
		err    error
	}

	secretNotFound := func(_ string) error {
		return apierrors.NewNotFound(
			corev1.SchemeGroupVersion.WithResource("secrets").GroupResource(),
			"test")
	}

	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "test",
			Labels: map[string]string{
				ManagedLabelKey: ManagedLabelValue,
			},
			Annotations: map[string]string{
				OwnerAnnotationPrefix + "test": "owned",
			},
		},
	}
	tests := []struct {
		name                   string
		secretName             string
		cluster                types.NamespacedName
		secretInCache          *corev1.Secret
		cacheErr               error
		secretsInIndex         []*corev1.Secret
		secretExistsErr        error
		applyCalls             []*applyCall
		expectEvents           []string
		expectNext             bool
		expectRequeueErr       error
		expectRequeueAPIErr    error
		expectRequeue          bool
		expectObjectMissingErr error
		expectCtxSecret        *corev1.Secret
	}{
		{
			name: "no secret",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			secretName: "",
			applyCalls: []*applyCall{},
			expectNext: true,
		},
		{
			name:       "secret does not exist",
			secretName: "secret",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			cacheErr:               secretNotFound("test"),
			secretExistsErr:        secretNotFound("test"),
			secretsInIndex:         []*corev1.Secret{},
			applyCalls:             []*applyCall{},
			expectObjectMissingErr: secretNotFound("test"),
		},
		{
			name:       "secret needs adopting",
			secretName: "secret",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			cacheErr:       secretNotFound("test"),
			secretsInIndex: []*corev1.Secret{},
			applyCalls: []*applyCall{
				{
					input: applycorev1.Secret("secret", "test").
						WithLabels(map[string]string{
							ManagedLabelKey: ManagedLabelValue,
						}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret",
							Namespace: "test",
							Labels: map[string]string{
								ManagedLabelKey: ManagedLabelValue,
							},
						},
					},
				},
				{
					input: applycorev1.Secret("secret", "test").
						WithAnnotations(map[string]string{
							OwnerAnnotationPrefix + "test": "owned",
						}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret",
							Namespace: "test",
							Labels: map[string]string{
								ManagedLabelKey: ManagedLabelValue,
							},
							Annotations: map[string]string{
								OwnerAnnotationPrefix + "test": "owned",
							},
						},
					},
				},
			},
			expectEvents:    []string{"Normal SecretAdopedByOwner Secret was referenced by test/test; it has been labelled to mark it as part of the configuration for that controller."},
			expectCtxSecret: testSecret,
			expectNext:      true,
		},
		{
			name: "secret already adopted",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			secretName:      "secret",
			secretInCache:   testSecret,
			secretsInIndex:  []*corev1.Secret{testSecret},
			expectEvents:    []string{},
			expectNext:      true,
			expectCtxSecret: testSecret,
		},
		{
			name: "secret adopted by a second cluster",
			cluster: types.NamespacedName{
				Name:      "test2",
				Namespace: "test",
			},
			secretName:     "secret",
			secretInCache:  testSecret,
			secretsInIndex: []*corev1.Secret{testSecret},
			applyCalls: []*applyCall{
				{
					input: applycorev1.Secret("secret", "test").
						WithAnnotations(map[string]string{
							OwnerAnnotationPrefix + "test2": "owned",
						}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret",
							Namespace: "test",
							Labels: map[string]string{
								ManagedLabelKey: ManagedLabelValue,
							},
							Annotations: map[string]string{
								OwnerAnnotationPrefix + "test":  "owned",
								OwnerAnnotationPrefix + "test2": "owned",
							},
						},
					},
				},
			},
			expectEvents: []string{"Normal SecretAdopedByOwner Secret was referenced by test/test2; it has been labelled to mark it as part of the configuration for that controller."},
			expectCtxSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret",
					Namespace: "test",
					Labels: map[string]string{
						ManagedLabelKey: ManagedLabelValue,
					},
					Annotations: map[string]string{
						OwnerAnnotationPrefix + "test":  "owned",
						OwnerAnnotationPrefix + "test2": "owned",
					},
				},
			},
			expectNext: true,
		},
		{
			name: "transient error adopting secret",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			secretName: "secret",
			cacheErr:   secretNotFound("test"),
			applyCalls: []*applyCall{
				{
					input: applycorev1.Secret("secret", "test").
						WithLabels(map[string]string{
							ManagedLabelKey: ManagedLabelValue,
						}),
					err: apierrors.NewTooManyRequestsError("server having issues"),
				},
			},
			expectRequeueAPIErr: apierrors.NewTooManyRequestsError("server having issues"),
		},
		{
			name: "old secret still in index",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			secretName: "secret",
			secretsInIndex: []*corev1.Secret{testSecret, {
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret2",
					Namespace: "test",
					Labels: map[string]string{
						ManagedLabelKey: ManagedLabelValue,
					},
					Annotations: map[string]string{
						OwnerAnnotationPrefix + "test": "owned",
					},
				},
			}},
			applyCalls: []*applyCall{
				{
					input: applycorev1.Secret("secret2", "test").
						WithLabels(map[string]string{}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret2",
							Namespace: "test",
							Annotations: map[string]string{
								OwnerAnnotationPrefix + "test": "owned",
							},
						},
					},
				},
				{
					input: applycorev1.Secret("secret2", "test").
						WithAnnotations(map[string]string{}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret2",
							Namespace: "test",
						},
					},
				},
			},
			expectCtxSecret: testSecret,
			expectNext:      true,
		},
		{
			name: "old secret still in index, still has other owners",
			cluster: types.NamespacedName{
				Namespace: "test",
				Name:      "test",
			},
			secretName: "secret",
			secretsInIndex: []*corev1.Secret{testSecret, {
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret2",
					Namespace: "test",
					Labels: map[string]string{
						ManagedLabelKey: ManagedLabelValue,
					},
					Annotations: map[string]string{
						OwnerAnnotationPrefix + "test2": "owned",
						OwnerAnnotationPrefix + "test":  "owned",
					},
				},
			}},
			applyCalls: []*applyCall{
				{
					input: applycorev1.Secret("secret2", "test").
						WithAnnotations(map[string]string{}),
					result: &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "secret2",
							Namespace: "test",
						},
					},
				},
			},
			expectCtxSecret: testSecret,
			expectNext:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrls := &fake.FakeInterface{}
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{IndexName: OwnerKeysFromMeta(OwnerAnnotationPrefix)})
			IndexAddUnstructured(t, indexer, tt.secretsInIndex)

			recorder := record.NewFakeRecorder(1)
			nextCalled := false
			applyCallIndex := 0
			s := NewSecretAdoptionHandler(
				recorder,
				func(_ context.Context) (*corev1.Secret, error) {
					return tt.secretInCache, tt.cacheErr
				},
				func(_ context.Context, err error) {
					require.Equal(t, tt.expectObjectMissingErr, err)
				},
				typed.NewIndexer[*corev1.Secret](indexer),
				func(_ context.Context, secret *applycorev1.SecretApplyConfiguration, _ metav1.ApplyOptions) (result *corev1.Secret, err error) {
					defer func() { applyCallIndex++ }()
					call := tt.applyCalls[applyCallIndex]
					call.called = true
					require.Equal(t, call.input, secret, "error on call %d", applyCallIndex)
					return call.result, call.err
				},
				func(_ context.Context, _ types.NamespacedName) error {
					return tt.secretExistsErr
				},
				handler.NewHandlerFromFunc(func(ctx context.Context) {
					nextCalled = true
					require.Equal(t, tt.expectCtxSecret, CtxSecret.Value(ctx))
				}, "testnext"),
			)
			ctx := CtxOwnerNN.WithValue(t.Context(), tt.cluster)
			ctx = CtxSecretNN.WithValue(ctx, types.NamespacedName{Namespace: "test", Name: tt.secretName})
			ctx = QueueOps.WithValue(ctx, ctrls)
			s.Handle(ctx)
			for _, call := range tt.applyCalls {
				require.True(t, call.called)
			}
			ExpectEvents(t, recorder, tt.expectEvents)
			require.Equal(t, tt.expectNext, nextCalled)
			if tt.expectRequeueErr != nil {
				require.Equal(t, 1, ctrls.RequeueErrCallCount())
				require.Equal(t, tt.expectRequeueErr, ctrls.RequeueErrArgsForCall(0))
			}
			if tt.expectRequeueAPIErr != nil {
				require.Equal(t, 1, ctrls.RequeueAPIErrCallCount())
				require.Equal(t, tt.expectRequeueAPIErr, ctrls.RequeueAPIErrArgsForCall(0))
			}
			require.Equal(t, tt.expectRequeue, ctrls.RequeueCallCount() == 1)
		})
	}
}

func NewSecretAdoptionHandler(recorder record.EventRecorder, getFromCache func(ctx context.Context) (*corev1.Secret, error), missingFunc func(context.Context, error), secretIndexer *typed.Indexer[*corev1.Secret], secretApplyFunc ApplyFunc[*corev1.Secret, *applycorev1.SecretApplyConfiguration], secretExistsFunc ExistsFunc, next handler.Handler) handler.Handler {
	return handler.NewHandler(&AdoptionHandler[*corev1.Secret, *applycorev1.SecretApplyConfiguration]{
		OperationsContext:      QueueOps,
		ControllerFieldManager: "test-controller",
		AdopteeCtx:             CtxSecretNN,
		OwnerCtx:               CtxOwnerNN,
		AdoptedCtx:             CtxSecret,
		ObjectAdoptedFunc: func(ctx context.Context, secret *corev1.Secret) {
			recorder.Eventf(secret, corev1.EventTypeNormal, EventSecretAdopted, "Secret was referenced by %s; it has been labelled to mark it as part of the configuration for that controller.", CtxOwnerNN.MustValue(ctx).String())
		},
		ObjectMissingFunc: missingFunc,
		GetFromCache:      getFromCache,
		Indexer:           secretIndexer,
		IndexName:         IndexName,
		Labels:            map[string]string{ManagedLabelKey: ManagedLabelValue},
		NewPatch: func(nn types.NamespacedName) *applycorev1.SecretApplyConfiguration {
			return applycorev1.Secret(nn.Name, nn.Namespace)
		},
		OwnerAnnotationPrefix: OwnerAnnotationPrefix,
		OwnerAnnotationKeyFunc: func(owner types.NamespacedName) string {
			return OwnerAnnotationPrefix + owner.Name
		},
		OwnerFieldManagerFunc: func(owner types.NamespacedName) string {
			return "my-owner-" + owner.Namespace + "-" + owner.Name
		},
		ApplyFunc:  secretApplyFunc,
		ExistsFunc: secretExistsFunc,
		Next:       next,
	}, "adoptSecret")
}

func ExpectEvents(t *testing.T, recorder *record.FakeRecorder, expected []string) {
	close(recorder.Events)
	events := make([]string, 0)
	for e := range recorder.Events {
		events = append(events, e)
	}
	require.ElementsMatch(t, expected, events)
}

func IndexAddUnstructured[K runtime.Object](t *testing.T, indexer cache.Indexer, objs []K) {
	for _, s := range objs {
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(s)
		require.NoError(t, err)
		require.NoError(t, indexer.Add(&unstructured.Unstructured{Object: u}))
	}
}
