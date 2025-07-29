package finalizer

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"

	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/typedctx"
)

func ExampleGetAddFunc() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test object without a finalizer
	testObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "MyObject",
			"metadata": map[string]interface{}{
				"name":      "my-object",
				"namespace": "default",
			},
		},
	}

	// Create a fake dynamic client with a basic scheme
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, testObj)

	// Define the GVR for our resource
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myobjects",
	}

	// Create an add function for finalizers
	addFunc := GetAddFunc[*metav1.PartialObjectMetadata](
		dynamicClient,
		gvr,
		"my-controller.example.com/finalizer",
		"my-controller",
	)

	// Use the function to add a finalizer
	result, err := addFunc(ctx, types.NamespacedName{
		Name:      "my-object",
		Namespace: "default",
	})
	if err != nil {
		fmt.Printf("Error: %v", err)
		return
	}

	fmt.Printf("Has finalizer: %v", len(result.GetFinalizers()) > 0)

	// Output: Has finalizer: true
}

func ExampleGetRemoveFunc() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := metav1.Now()

	// Create a test object with a finalizer and deletion timestamp
	testObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "MyObject",
			"metadata": map[string]interface{}{
				"name":      "my-object",
				"namespace": "default",
				"finalizers": []interface{}{
					"my-controller.example.com/finalizer",
				},
				"deletionTimestamp": now.Format("2006-01-02T15:04:05Z"),
			},
		},
	}

	// Create a fake dynamic client with a basic scheme
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, testObj)

	// Define the GVR for our resource
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myobjects",
	}

	// Create a remove function for finalizers
	removeFunc := GetRemoveFunc[*metav1.PartialObjectMetadata](
		dynamicClient,
		gvr,
		"my-controller.example.com/finalizer",
		"my-controller",
	)

	// Use the function to remove a finalizer
	result, err := removeFunc(ctx, types.NamespacedName{
		Name:      "my-object",
		Namespace: "default",
	})
	if err != nil {
		fmt.Printf("Error: %v", err)
		return
	}

	fmt.Printf("Finalizer removed: %v", len(result.GetFinalizers()) == 0)

	// Output: Finalizer removed: true
}

func ExampleSetFinalizerHandler() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create context key for our object
	CtxObject := typedctx.WithDefault[*metav1.PartialObjectMetadata](nil)

	// Mock object without finalizer
	obj := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-object",
			Namespace: "default",
		},
	}

	// Add object to context
	ctx = CtxObject.WithValue(ctx, obj)

	// Create the handler
	handler := &SetFinalizerHandler[*metav1.PartialObjectMetadata]{
		FinalizeableObjectCtxKey: CtxObject,
		Finalizer:                "my-controller.example.com/finalizer",
		AddFinalizer: func(_ context.Context, nn types.NamespacedName) (*metav1.PartialObjectMetadata, error) {
			// Mock successful finalizer addition
			return &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:       nn.Name,
					Namespace:  nn.Namespace,
					Finalizers: []string{"my-controller.example.com/finalizer"},
				},
			}, nil
		},
		RequeueAPIErr: func(_ context.Context, err error) {
			fmt.Printf("Requeue due to error: %v", err)
		},
		Next: handler.NewHandlerFromFunc(func(_ context.Context) {
			fmt.Println("Processing next handler")
		}, "next"),
	}

	// Execute the handler
	handler.Handle(ctx)

	// Output: Processing next handler
}

func TestSetFinalizer(t *testing.T) {
	t.Parallel()

	CtxObject := typedctx.WithDefault[*metav1.PartialObjectMetadata](nil)

	now := metav1.Now()
	var nextKey handler.Key = "next"
	tests := []struct {
		name string

		obj      *metav1.PartialObjectMetadata
		patchErr error

		expectNext          handler.Key
		expectPatch         bool
		expectRequeueAPIErr bool
	}{
		{
			name: "missing finalizer",
			obj: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			expectPatch: true,
			expectNext:  nextKey,
		},
		{
			name: "has finalizer",
			obj: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "test",
					Finalizers: []string{"finalizer"},
				},
			},
			expectNext: nextKey,
		},
		{
			name: "doesn't add to deleted object",
			obj: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test",
					Namespace:         "test",
					DeletionTimestamp: &now,
				},
			},
			expectPatch: false,
			expectNext:  nextKey,
		},
		{
			name: "patch err",
			obj: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			expectPatch:         true,
			patchErr:            fmt.Errorf("error"),
			expectRequeueAPIErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			patchCalled := false
			requeueCalled := false

			ctx := t.Context()
			ctx = CtxObject.WithValue(ctx, tt.obj)

			var called handler.Key
			h := &SetFinalizerHandler[*metav1.PartialObjectMetadata]{
				AddFinalizer: func(_ context.Context, nn types.NamespacedName) (*metav1.PartialObjectMetadata, error) {
					patchCalled = true
					return &metav1.PartialObjectMetadata{
						ObjectMeta: metav1.ObjectMeta{
							Name:       nn.Name,
							Namespace:  nn.Namespace,
							Finalizers: []string{"finalizer"},
						},
					}, tt.patchErr
				},
				FinalizeableObjectCtxKey: CtxObject,
				Finalizer:                "finalizer",
				RequeueAPIErr: func(_ context.Context, _ error) {
					requeueCalled = true
				},
				Next: handler.NewHandlerFromFunc(func(_ context.Context) {
					called = nextKey
				}, "next"),
			}
			h.Handle(ctx)

			require.Equal(t, tt.expectPatch, patchCalled)
			require.Equal(t, tt.expectNext, called)
			require.Equal(t, tt.expectRequeueAPIErr, requeueCalled)
		})
	}
}
