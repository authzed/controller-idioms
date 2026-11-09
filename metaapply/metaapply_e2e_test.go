//go:build e2e

package metaapply_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/utils/ptr"

	"github.com/authzed/controller-idioms/metaapply"
	"github.com/authzed/controller-idioms/typed"
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

// createConfigMap creates a ConfigMap with data, applied by manager. The data
// shows if a write drops fields.
func createConfigMap(t *testing.T, client dynamic.Interface, name, manager string) *corev1.ConfigMap {
	t.Helper()
	body := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"data":       map[string]any{"key": "value"},
	}}
	u, err := client.Resource(configMapGVR).Namespace("default").Apply(t.Context(), name, body, metav1.ApplyOptions{FieldManager: manager, Force: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Resource(configMapGVR).Namespace("default").Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	cm, err := typed.UnstructuredObjToTypedObj[*corev1.ConfigMap](u)
	require.NoError(t, err)
	return cm
}

func setLabel(body *unstructured.Unstructured) {
	body.SetLabels(map[string]string{"example.com/label": "on"})
}

func TestApply(t *testing.T) {
	client := e2eClient(t)

	t.Run("applies metadata and keeps other fields", func(t *testing.T) {
		cm := createConfigMap(t, client, "metaapply-keeps", "owner")
		a := metaapply.New(client, configMapGVR, configMapGVK, "label")

		u, err := a.Apply(t.Context(), cm, setLabel)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"example.com/label": "on"}, u.GetLabels())
		data, _, err := unstructured.NestedStringMap(u.Object, "data")
		require.NoError(t, err)
		require.Equal(t, map[string]string{"key": "value"}, data)
	})

	t.Run("refuses when the manager owns fields outside metadata", func(t *testing.T) {
		// The manager applied data, so an apply of metadata alone would prune it.
		cm := createConfigMap(t, client, "metaapply-refuses", "shared")
		a := metaapply.New(client, configMapGVR, configMapGVK, "shared")

		_, err := a.Apply(t.Context(), cm, setLabel)
		require.ErrorIs(t, err, metaapply.ErrOwnsNonMetadata)
		u, err := client.Resource(configMapGVR).Namespace("default").Get(t.Context(), cm.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Empty(t, u.GetLabels(), "nothing is written")
	})

	t.Run("does not recreate an object that is gone", func(t *testing.T) {
		cm := createConfigMap(t, client, "metaapply-gone", "owner")
		require.NoError(t, client.Resource(configMapGVR).Namespace("default").Delete(t.Context(), cm.Name, metav1.DeleteOptions{}))
		a := metaapply.New(client, configMapGVR, configMapGVK, "label")

		_, err := a.Apply(t.Context(), cm, setLabel)
		require.True(t, k8serrors.IsNotFound(err), "got %v", err)
		_, err = client.Resource(configMapGVR).Namespace("default").Get(t.Context(), cm.Name, metav1.GetOptions{})
		require.True(t, k8serrors.IsNotFound(err), "the apply must not upsert")
	})

	t.Run("does not change an object replaced under the same name", func(t *testing.T) {
		stale := createConfigMap(t, client, "metaapply-replaced", "owner")
		require.NoError(t, client.Resource(configMapGVR).Namespace("default").Delete(t.Context(), stale.Name, metav1.DeleteOptions{}))
		replacement := createConfigMap(t, client, stale.Name, "owner")
		require.NotEqual(t, stale.UID, replacement.UID)
		a := metaapply.New(client, configMapGVR, configMapGVK, "label")

		_, err := a.Apply(t.Context(), stale, setLabel)
		require.Error(t, err)
		u, err := client.Resource(configMapGVR).Namespace("default").Get(t.Context(), stale.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, replacement.UID, u.GetUID())
		require.Empty(t, u.GetLabels())
	})
}
