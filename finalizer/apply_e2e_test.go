//go:build e2e

package finalizer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/utils/ptr"

	"github.com/authzed/controller-idioms/finalizer"
	"github.com/authzed/controller-idioms/typed"
)

const (
	testFinalizer  = "example.com/finalizer"
	otherFinalizer = "example.com/other"
)

var (
	configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	configMapGVK = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
)

func e2eClient(t *testing.T) dynamic.Interface {
	t.Helper()
	opts := genericclioptions.NewConfigFlags(true)
	opts.KubeConfig = ptr.To("../controller-idioms-e2e.kubeconfig")
	client, err := cmdutil.NewFactory(opts).DynamicClient()
	require.NoError(t, err)
	return client
}

// createConfigMap creates a ConfigMap with data and the given finalizers. The
// data shows if a write drops fields.
func createConfigMap(t *testing.T, client dynamic.Interface, finalizers ...string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{GenerateName: "finalizer-", Namespace: "default", Finalizers: finalizers},
		Data:       map[string]string{"key": "value"},
	}
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
	require.NoError(t, err)
	created, err := client.Resource(configMapGVR).Namespace("default").Create(t.Context(), &unstructured.Unstructured{Object: u}, metav1.CreateOptions{FieldManager: "creator"})
	require.NoError(t, err)
	t.Cleanup(func() {
		// Remove all finalizers so that a failed test does not leave the object.
		_, _ = client.Resource(configMapGVR).Namespace("default").Patch(context.Background(), created.GetName(), types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{})
		_ = client.Resource(configMapGVR).Namespace("default").Delete(context.Background(), created.GetName(), metav1.DeleteOptions{})
	})
	out, err := typed.UnstructuredObjToTypedObj[*corev1.ConfigMap](created)
	require.NoError(t, err)
	return out
}

func getConfigMap(t *testing.T, client dynamic.Interface, name string) (*corev1.ConfigMap, error) {
	t.Helper()
	u, err := client.Resource(configMapGVR).Namespace("default").Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return typed.UnstructuredObjToTypedObj[*corev1.ConfigMap](u)
}

func deleteConfigMap(t *testing.T, client dynamic.Interface, name string) *corev1.ConfigMap {
	t.Helper()
	require.NoError(t, client.Resource(configMapGVR).Namespace("default").Delete(t.Context(), name, metav1.DeleteOptions{}))
	cm, err := getConfigMap(t, client, name)
	require.NoError(t, err, "a finalizer keeps the object")
	require.NotNil(t, cm.DeletionTimestamp)
	return cm
}

func TestFinalizer(t *testing.T) {
	client := e2eClient(t)

	// newFinalizer returns a Finalizer that records the owners passed to each
	// fallback.
	newFinalizer := func() (*finalizer.Finalizer[*corev1.ConfigMap], *[][]string) {
		var fallbacks [][]string
		f := finalizer.New[*corev1.ConfigMap](client, configMapGVR, configMapGVK, testFinalizer,
			finalizer.WithFallbackFunc(func(_ context.Context, _ *corev1.ConfigMap, owners []string) {
				fallbacks = append(fallbacks, owners)
			}))
		return f, &fallbacks
	}

	t.Run("added and removed by apply", func(t *testing.T) {
		f, fallbacks := newFinalizer()
		cm := createConfigMap(t, client)

		added, err := f.Add(t.Context(), cm)
		require.NoError(t, err)
		require.Equal(t, []string{testFinalizer}, added.Finalizers)
		require.Equal(t, map[string]string{"key": "value"}, added.Data)

		terminating := deleteConfigMap(t, client, cm.Name)
		_, err = f.Remove(t.Context(), terminating)
		require.NoError(t, err)
		_, err = getConfigMap(t, client, cm.Name)
		require.True(t, k8serrors.IsNotFound(err), "releasing the only finalizer lets the delete finish")
		require.Empty(t, *fallbacks, "the apply releases its own finalizer")
	})

	t.Run("keeps fields the object type does not model", func(t *testing.T) {
		// GetAddFunc writes the complete object as K. With a metadata-only K, it
		// drops the data. The apply sends only the finalizers.
		cm := createConfigMap(t, client)
		metaOnly := finalizer.New[*metav1.PartialObjectMetadata](client, configMapGVR, configMapGVK, testFinalizer)

		_, err := metaOnly.Add(t.Context(), &metav1.PartialObjectMetadata{ObjectMeta: cm.ObjectMeta})
		require.NoError(t, err)
		live, err := getConfigMap(t, client, cm.Name)
		require.NoError(t, err)
		require.Equal(t, []string{testFinalizer}, live.Finalizers)
		require.Equal(t, map[string]string{"key": "value"}, live.Data)
	})

	t.Run("removes a finalizer added by GetAddFunc", func(t *testing.T) {
		f, fallbacks := newFinalizer()
		cm := createConfigMap(t, client)
		_, err := finalizer.GetAddFunc[*corev1.ConfigMap](client, configMapGVR, testFinalizer, "legacy")(t.Context(), types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}) //nolint:staticcheck // tests objects from the deprecated function
		require.NoError(t, err)

		terminating := deleteConfigMap(t, client, cm.Name)
		_, err = f.Remove(t.Context(), terminating)
		require.NoError(t, err)
		_, err = getConfigMap(t, client, cm.Name)
		require.True(t, k8serrors.IsNotFound(err))
		require.Equal(t, [][]string{{"legacy"}}, *fallbacks, "the Update owner is reported")
	})

	t.Run("removes a finalizer another manager added, and only that one", func(t *testing.T) {
		f, fallbacks := newFinalizer()
		cm := createConfigMap(t, client, otherFinalizer, testFinalizer)

		terminating := deleteConfigMap(t, client, cm.Name)
		removed, err := f.Remove(t.Context(), terminating)
		require.NoError(t, err)
		require.Equal(t, []string{otherFinalizer}, removed.Finalizers)
		require.Equal(t, map[string]string{"key": "value"}, removed.Data)
		require.Equal(t, [][]string{{"creator"}}, *fallbacks)
	})

	t.Run("GetRemoveFunc removes a finalizer added by Add", func(t *testing.T) {
		// A controller that rolls back to GetRemoveFunc must still release
		// finalizers that Add owns with an Apply entry.
		f, _ := newFinalizer()
		cm := createConfigMap(t, client)
		_, err := f.Add(t.Context(), cm)
		require.NoError(t, err)

		deleteConfigMap(t, client, cm.Name)
		_, err = finalizer.GetRemoveFunc[*corev1.ConfigMap](client, configMapGVR, testFinalizer, "legacy")(t.Context(), types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}) //nolint:staticcheck // tests objects from the deprecated function
		require.NoError(t, err)
		_, err = getConfigMap(t, client, cm.Name)
		require.True(t, k8serrors.IsNotFound(err))
	})

	t.Run("does not remove from an object that is not being deleted", func(t *testing.T) {
		f, _ := newFinalizer()
		cm := createConfigMap(t, client, testFinalizer)

		_, err := f.Remove(t.Context(), cm)
		require.NoError(t, err)
		live, err := getConfigMap(t, client, cm.Name)
		require.NoError(t, err)
		require.Equal(t, []string{testFinalizer}, live.Finalizers)
	})

	t.Run("does not recreate an object that is gone", func(t *testing.T) {
		f, _ := newFinalizer()
		cm := createConfigMap(t, client)
		require.NoError(t, client.Resource(configMapGVR).Namespace("default").Delete(t.Context(), cm.Name, metav1.DeleteOptions{}))

		_, err := f.Add(t.Context(), cm)
		require.True(t, k8serrors.IsNotFound(err), "got %v", err)
		_, err = getConfigMap(t, client, cm.Name)
		require.True(t, k8serrors.IsNotFound(err), "the apply must not upsert")
	})

	t.Run("does not change an object replaced under the same name", func(t *testing.T) {
		f, _ := newFinalizer()
		stale := createConfigMap(t, client, testFinalizer)
		terminating := deleteConfigMap(t, client, stale.Name)
		_, err := client.Resource(configMapGVR).Namespace("default").Patch(t.Context(), stale.Name, types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{})
		require.NoError(t, err)

		replacement := &corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: stale.Name, Namespace: "default", Finalizers: []string{testFinalizer}},
		}
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(replacement)
		require.NoError(t, err)
		_, err = client.Resource(configMapGVR).Namespace("default").Create(t.Context(), &unstructured.Unstructured{Object: u}, metav1.CreateOptions{})
		require.NoError(t, err)
		live := deleteConfigMap(t, client, stale.Name)

		_, err = f.Remove(t.Context(), terminating)
		require.Error(t, err, "the stale uid must not match the replacement")
		after, err := getConfigMap(t, client, stale.Name)
		require.NoError(t, err)
		require.Equal(t, live.UID, after.UID)
		require.Equal(t, []string{testFinalizer}, after.Finalizers, "the replacement keeps its finalizer")
	})
}
