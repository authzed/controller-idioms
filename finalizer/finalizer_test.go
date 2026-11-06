package finalizer_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"

	"github.com/authzed/controller-idioms/component"
	"github.com/authzed/controller-idioms/finalizer"
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
				"name":      "test-object",
				"namespace": "default",
			},
		},
	}

	// Create a fake dynamic client with the test object
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, testObj)

	// Define the GVR for our resource
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myobjects",
	}

	// Create an add function for finalizers
	addFunc := finalizer.GetAddFunc[*metav1.PartialObjectMetadata](
		dynamicClient,
		gvr,
		"my-controller.example.com/finalizer",
		"my-controller",
	)

	// Use the function to add a finalizer
	result, err := addFunc(ctx, types.NamespacedName{
		Name:      "test-object",
		Namespace: "default",
	})
	if err != nil {
		fmt.Printf("Error adding finalizer: %v\n", err)
		return
	}

	fmt.Printf("Finalizer added: %v\n", len(result.GetFinalizers()) > 0)
	fmt.Printf("Finalizer name: %s\n", result.GetFinalizers()[0])

	// Output: Finalizer added: true
	// Finalizer name: my-controller.example.com/finalizer
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
				"name":      "test-object",
				"namespace": "default",
				"finalizers": []interface{}{
					"my-controller.example.com/finalizer",
				},
				"deletionTimestamp": now.Format("2006-01-02T15:04:05Z"),
			},
		},
	}

	// Create a fake dynamic client with the test object
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, testObj)

	// Define the GVR for our resource
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myobjects",
	}

	// Create a remove function for finalizers
	removeFunc := finalizer.GetRemoveFunc[*metav1.PartialObjectMetadata](
		dynamicClient,
		gvr,
		"my-controller.example.com/finalizer",
		"my-controller",
	)

	// Use the function to remove a finalizer
	result, err := removeFunc(ctx, types.NamespacedName{
		Name:      "test-object",
		Namespace: "default",
	})
	if err != nil {
		fmt.Printf("Error removing finalizer: %v\n", err)
		return
	}

	fmt.Printf("Finalizer removed: %v\n", len(result.GetFinalizers()) == 0)
	fmt.Printf("Has deletion timestamp: %v\n", result.GetDeletionTimestamp() != nil)

	// Output: Finalizer removed: true
	// Has deletion timestamp: true
}

func ExampleGetAddFunc_alreadyHasFinalizer() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test object that already has the finalizer
	testObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "MyObject",
			"metadata": map[string]interface{}{
				"name":      "test-object",
				"namespace": "default",
				"finalizers": []interface{}{
					"my-controller.example.com/finalizer",
				},
			},
		},
	}

	// Create a fake dynamic client with the test object
	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, testObj)

	// Define the GVR for our resource
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myobjects",
	}

	// Create an add function for finalizers
	addFunc := finalizer.GetAddFunc[*metav1.PartialObjectMetadata](
		dynamicClient,
		gvr,
		"my-controller.example.com/finalizer",
		"my-controller",
	)

	// Use the function - it should be a no-op since finalizer already exists
	result, err := addFunc(ctx, types.NamespacedName{
		Name:      "test-object",
		Namespace: "default",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Finalizer count: %d\n", len(result.GetFinalizers()))
	fmt.Printf("Still has finalizer: %v\n", result.GetFinalizers()[0] == "my-controller.example.com/finalizer")

	// Output: Finalizer count: 1
	// Still has finalizer: true
}

const testFinalizer = "example.com/finalizer"

var (
	podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	podNN  = types.NamespacedName{Namespace: "default", Name: "test"}
)

func podWithCPU(finalizers []any, deleting bool) *unstructured.Unstructured {
	meta := map[string]any{"name": podNN.Name, "namespace": podNN.Namespace}
	if finalizers != nil {
		meta["finalizers"] = finalizers
	}
	if deleting {
		meta["deletionTimestamp"] = "2026-10-29T10:41:00Z"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   meta,
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name":      "c",
				"resources": map[string]any{"requests": map[string]any{"cpu": "15000m"}},
			}},
		},
	}}
}

func requireSpecUnchanged(t *testing.T, client dynamic.Interface, wantFinalizers []string) {
	t.Helper()
	got, err := client.Resource(podGVR).Namespace(podNN.Namespace).Get(context.Background(), podNN.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, wantFinalizers, got.GetFinalizers())
	containers, found, err := unstructured.NestedSlice(got.Object, "spec", "containers")
	require.NoError(t, err)
	require.True(t, found)
	cpu, _, err := unstructured.NestedString(containers[0].(map[string]any), "resources", "requests", "cpu")
	require.NoError(t, err)
	require.Equal(t, "15000m", cpu)
}

func TestFinalizerPreservesObject(t *testing.T) {
	t.Parallel()
	t.Run("typed", func(t *testing.T) {
		t.Parallel()
		testPreservesObject[*corev1.Pod](t)
	})
	t.Run("partial metadata", func(t *testing.T) {
		t.Parallel()
		testPreservesObject[*metav1.PartialObjectMetadata](t)
	})
}

func testPreservesObject[K component.KubeObject](t *testing.T) {
	t.Run("add", func(t *testing.T) {
		t.Parallel()
		client := fake.NewSimpleDynamicClient(runtime.NewScheme(), podWithCPU(nil, false))
		obj, err := finalizer.GetAddFunc[K](client, podGVR, testFinalizer, "test")(context.Background(), podNN)
		require.NoError(t, err)
		require.Equal(t, []string{testFinalizer}, obj.GetFinalizers())
		requireSpecUnchanged(t, client, []string{testFinalizer})
	})
	t.Run("remove", func(t *testing.T) {
		t.Parallel()
		client := fake.NewSimpleDynamicClient(runtime.NewScheme(), podWithCPU([]any{"other", testFinalizer}, true))
		obj, err := finalizer.GetRemoveFunc[K](client, podGVR, testFinalizer, "test")(context.Background(), podNN)
		require.NoError(t, err)
		require.Equal(t, []string{"other"}, obj.GetFinalizers())
		requireSpecUnchanged(t, client, []string{"other"})
	})
	t.Run("remove last", func(t *testing.T) {
		t.Parallel()
		client := fake.NewSimpleDynamicClient(runtime.NewScheme(), podWithCPU([]any{testFinalizer}, true))
		obj, err := finalizer.GetRemoveFunc[K](client, podGVR, testFinalizer, "test")(context.Background(), podNN)
		require.NoError(t, err)
		require.Nil(t, obj.GetFinalizers())
		requireSpecUnchanged(t, client, nil)
	})
}
