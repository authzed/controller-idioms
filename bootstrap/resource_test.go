package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/testing"
)

func ExampleResourceFromFile() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := secretApplyPatchHandlingFakeClient(scheme)

	// create the object from the file
	// the example is a secret, but it could be any built-in or CRD-defined type
	_, err := ResourceFromFile[*corev1.Secret](ctx, "bootstrapped-secret", secretGVR, client, "./example.yaml", 0)
	if err != nil {
		panic(err)
	}

	for {
		secret, err := client.Resource(secretGVR).Namespace("test").Get(ctx, "example", metav1.GetOptions{})
		if err == nil {
			fmt.Printf("%s/%s", secret.GetNamespace(), secret.GetName())
			break
		}
		time.Sleep(1 * time.Millisecond)
	}
	// Output: test/example
}

// secretApplyPatchHandlingFakeClient creates a fake client that handles
// apply patch types (for corev1.Secret only).
func secretApplyPatchHandlingFakeClient(scheme *runtime.Scheme) *fake.FakeDynamicClient {
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{})
	client.PrependReactor("patch", "secrets", func(action testing.Action) (handled bool, ret runtime.Object, err error) {
		decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(action.(testing.PatchAction).GetPatch()))
		var secret corev1.Secret
		if err := decoder.Decode(&secret); err != nil {
			return true, nil, err
		}
		// server-side apply creates the object if it doesn't exist
		if err := client.Tracker().Add(&secret); err != nil {
			return true, nil, err
		}
		return true, &secret, nil
	})
	return client
}
