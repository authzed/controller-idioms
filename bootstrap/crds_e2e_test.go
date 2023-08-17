//go:build e2e

package bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/utils/pointer"
)

func TestCRD(t *testing.T) {
	opts := genericclioptions.NewConfigFlags(true)
	opts.KubeConfig = pointer.String("../controller-idioms-e2e.kubeconfig")
	factory := cmdutil.NewFactory(opts)
	restConfig, err := factory.ToRESTConfig()
	require.NoError(t, err)

	// ensure CRDs
	require.NoError(t, CRDs(context.Background(), restConfig, crdFS, "example"))

	// create an object
	client, err := factory.DynamicClient()
	require.NoError(t, err)
	_, err = client.Resource(schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "mytypes",
	}).Namespace("default").Create(context.Background(), &unstructured.Unstructured{Object: map[string]any{
		"kind":       "MyType",
		"apiVersion": "example.com/v1",
		"metadata":   map[string]any{"generateName": "test"},
	}}, metav1.CreateOptions{})
	require.NoError(t, err)
}
