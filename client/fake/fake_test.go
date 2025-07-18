package fake

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
)

// Example of embedding CRDs using go:embed directive
//
//go:embed testdata/example-crds.yaml
var embeddedCRDs []byte

// Example showing multiple embed files
//
//go:embed testdata/widget-crd.yaml
var widgetCRD []byte

//go:embed testdata/tool-crd.yaml
var toolCRD []byte

func newConfigMap(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app": "test",
				},
			},
			"data": map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
		},
	}
}

func newService(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"app": "test",
				},
				"ports": []interface{}{
					map[string]interface{}{
						"port":       int64(80),
						"targetPort": int64(8080),
						"protocol":   "TCP",
					},
				},
			},
		},
	}
}

func newCustomResource(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "test.example.com/v1",
			"kind":       "TestResource",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"replicas": int64(3),
				"image":    "nginx:latest",
			},
		},
	}
}

func getGVR(obj *unstructured.Unstructured) schema.GroupVersionResource {
	gv, err := schema.ParseGroupVersion(obj.GetAPIVersion())
	if err != nil {
		panic(err) // This should never happen in tests
	}

	kind := obj.GetKind()
	var resource string
	switch kind {
	case "ConfigMap":
		resource = "configmaps"
	case "Service":
		resource = "services"
	case "TestResource":
		resource = "testresources"
	default:
		// Simple pluralization fallback
		resource = strings.ToLower(kind) + "s"
	}

	return schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource,
	}
}

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := kscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Register custom TestResource types used in tests
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResourceList",
	}, &unstructured.UnstructuredList{})

	return scheme
}

func applyObj(t *testing.T, client dynamic.Interface, obj *unstructured.Unstructured, fieldManager string) *unstructured.Unstructured {
	gvr := getGVR(obj)
	result, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Apply(
		t.Context(),
		obj.GetName(),
		obj,
		metav1.ApplyOptions{
			FieldManager: fieldManager,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, obj.GetName(), result.GetName())
	require.Equal(t, obj.GetNamespace(), result.GetNamespace())
	return result
}

// applyObjWithForce is a test helper that applies an object with Force=true
func applyObjWithForce(t *testing.T, client dynamic.Interface, obj *unstructured.Unstructured, fieldManager string) *unstructured.Unstructured {
	t.Helper()
	gvr := getGVR(obj)
	result, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Apply(
		t.Context(),
		obj.GetName(),
		obj,
		metav1.ApplyOptions{
			FieldManager: fieldManager,
			Force:        true,
		},
	)
	require.NoError(t, err)
	return result
}

func getObj(t *testing.T, client dynamic.Interface, obj *unstructured.Unstructured) *unstructured.Unstructured {
	gvr := getGVR(obj)
	result, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Get(
		t.Context(),
		obj.GetName(),
		metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, obj.GetName(), result.GetName())
	require.Equal(t, obj.GetNamespace(), result.GetNamespace())
	return result
}

func listObjs(t *testing.T, client dynamic.Interface, obj *unstructured.Unstructured) *unstructured.UnstructuredList {
	gvr := getGVR(obj)
	list, err := client.Resource(gvr).Namespace(obj.GetNamespace()).List(
		t.Context(),
		metav1.ListOptions{},
	)
	require.NoError(t, err)
	return list
}

func assertGetNotFound(t *testing.T, client dynamic.Interface, obj *unstructured.Unstructured) {
	gvr := getGVR(obj)
	_, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Get(
		t.Context(),
		obj.GetName(),
		metav1.GetOptions{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func findInList(list *unstructured.UnstructuredList, name string) *unstructured.Unstructured {
	for i := range list.Items {
		if list.Items[i].GetName() == name {
			return &list.Items[i]
		}
	}
	return nil
}

// TestNewClientWithOpenAPISpec tests the constructor that accepts custom OpenAPI spec paths
func TestNewClientWithOpenAPISpec(t *testing.T) {
	scheme := setupScheme()
	ctx := t.Context()

	// Create a test CRD
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testapps.example.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "TestApp",
				Plural:   "testapps",
				Singular: "testapp",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"containers": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextensionsv1.JSONSchemaProps{
														"name":  {Type: "string"},
														"image": {Type: "string"},
													},
													Required: []string{"name"},
												},
											},
											XListType:    ptr.To("map"),
											XListMapKeys: []string{"name"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	t.Run("WithDefaultEmbeddedSpec", func(t *testing.T) {
		// Test with empty spec path (should use embedded default)
		client := NewClient(scheme, WithCRDs(crd))

		// Test that strategic merge patch works with built-in types (proving OpenAPI spec is loaded)
		deploymentGVR := schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		}

		deployment := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      "test-deployment",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"replicas": int64(1),
					"selector": map[string]interface{}{
						"matchLabels": map[string]interface{}{
							"app": "test",
						},
					},
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"app": "test",
							},
						},
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "app",
									"image": "nginx:1.21",
								},
							},
						},
					},
				},
			},
		}

		_, err := client.Resource(deploymentGVR).Namespace("default").Apply(
			ctx, "test-deployment", deployment,
			metav1.ApplyOptions{FieldManager: "test-manager"},
		)
		require.NoError(t, err, "Apply should work with embedded OpenAPI spec")

		// Test CRD strategic merge patch
		appGVR := schema.GroupVersionResource{
			Group:    "example.com",
			Version:  "v1",
			Resource: "testapps",
		}

		app1 := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1",
				"kind":       "TestApp",
				"metadata": map[string]interface{}{
					"name":      "test-app",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "main",
							"image": "nginx:1.21",
						},
					},
				},
			},
		}

		_, err = client.Resource(appGVR).Namespace("default").Apply(
			ctx, "test-app", app1,
			metav1.ApplyOptions{FieldManager: "team-a"},
		)
		require.NoError(t, err)

		// Add second container - should merge strategically
		app2 := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1",
				"kind":       "TestApp",
				"metadata": map[string]interface{}{
					"name":      "test-app",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "sidecar",
							"image": "envoy:1.18",
						},
					},
				},
			},
		}

		result, err := client.Resource(appGVR).Namespace("default").Apply(
			ctx, "test-app", app2,
			metav1.ApplyOptions{FieldManager: "team-b"},
		)
		require.NoError(t, err, "CRD strategic merge patch should work")

		containers, found, err := unstructured.NestedSlice(result.Object, "spec", "containers")
		require.NoError(t, err)
		require.True(t, found)
		require.Len(t, containers, 2, "Both containers should be present after strategic merge")
	})

	t.Run("WithExplicitSpecPath", func(t *testing.T) {
		// Test with explicit path to the swagger.json file
		client := NewClient(scheme, WithOpenAPISpec("swagger.json"), WithCRDs(crd))

		// Test that it works the same as the embedded version
		configMapGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		configMap := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-cm",
					"namespace": "default",
				},
				"data": map[string]interface{}{
					"key1": "value1",
				},
			},
		}

		result, err := client.Resource(configMapGVR).Namespace("default").Apply(
			ctx, "test-cm", configMap,
			metav1.ApplyOptions{FieldManager: "test-manager"},
		)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify managed fields are present
		managedFields := result.GetManagedFields()
		require.NotEmpty(t, managedFields, "Managed fields should be present")
		require.Equal(t, "test-manager", managedFields[0].Manager)
	})

	t.Run("WithNonExistentSpecPath", func(t *testing.T) {
		// This should panic since it's used in tests and the spec file doesn't exist
		require.Panics(t, func() {
			NewClient(scheme, WithOpenAPISpec("/non/existent/path.json"))
		}, "Should panic when OpenAPI spec file doesn't exist")
	})

	t.Run("WithCustomSpecFile", func(t *testing.T) {
		// Create a temporary custom OpenAPI spec file with minimal content
		tempDir := t.TempDir()
		customSpecPath := filepath.Join(tempDir, "custom-spec.json")

		// Create a minimal OpenAPI spec (just enough to not fail parsing)
		customSpec := `{
			"swagger": "2.0",
			"info": {
				"title": "Custom Kubernetes API",
				"version": "v1.0.0"
			},
			"definitions": {
				"io.k8s.api.core.v1.ConfigMap": {
					"type": "object",
					"x-kubernetes-group-version-kind": [
						{
							"group": "",
							"kind": "ConfigMap",
							"version": "v1"
						}
					],
					"properties": {
						"apiVersion": {"type": "string"},
						"kind": {"type": "string"},
						"metadata": {"type": "object"},
						"data": {
							"type": "object",
							"additionalProperties": {"type": "string"}
						}
					}
				}
			}
		}`

		err := os.WriteFile(customSpecPath, []byte(customSpec), 0o600)
		require.NoError(t, err)

		// Test with the custom spec
		client := NewClient(scheme, WithOpenAPISpec(customSpecPath))

		configMapGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		configMap := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "custom-spec-test",
					"namespace": "default",
				},
				"data": map[string]interface{}{
					"custom": "spec",
				},
			},
		}

		result, err := client.Resource(configMapGVR).Namespace("default").Apply(
			ctx, "custom-spec-test", configMap,
			metav1.ApplyOptions{FieldManager: "custom-test"},
		)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify the data was applied correctly
		data, found, err := unstructured.NestedStringMap(result.Object, "data")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "spec", data["custom"])
	})
}

// TestOpenAPISpecVersionCompatibility tests that different OpenAPI spec versions work
func TestOpenAPISpecVersionCompatibility(t *testing.T) {
	t.Run("EmbeddedSpecVersion", func(t *testing.T) {
		scheme := setupScheme()

		// Create client with embedded spec
		client := NewClient(scheme)

		// Test with a resource that should exist in v1.33.2
		podGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods",
		}

		pod := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]interface{}{
					"name":      "test-pod",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "test-container",
							"image": "nginx:1.21",
						},
					},
				},
			},
		}

		result, err := client.Resource(podGVR).Namespace("default").Apply(
			t.Context(), "test-pod", pod,
			metav1.ApplyOptions{FieldManager: "test-manager"},
		)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify strategic merge patch works for containers
		containers, found, err := unstructured.NestedSlice(result.Object, "spec", "containers")
		require.NoError(t, err)
		require.True(t, found)
		require.Len(t, containers, 1)
	})
}

// TestEmbeddedOpenAPISpecExists verifies that the embedded OpenAPI spec is properly included
func TestEmbeddedOpenAPISpecExists(t *testing.T) {
	// Verify the embedded spec is not empty
	require.NotEmpty(t, defaultKubernetesOpenAPISpec, "Embedded OpenAPI spec should not be empty")

	// Verify it's a reasonable size (should be several MB)
	require.Greater(t, len(defaultKubernetesOpenAPISpec), 1000000, "Embedded spec should be at least 1MB")

	// Verify it's valid JSON
	var spec map[string]interface{}
	err := json.Unmarshal(defaultKubernetesOpenAPISpec, &spec)
	require.NoError(t, err, "Embedded spec should be valid JSON")

	// Verify it has expected OpenAPI structure
	require.Contains(t, spec, "definitions", "Spec should have definitions section")
	require.Contains(t, spec, "swagger", "Spec should have swagger version")

	definitions, ok := spec["definitions"].(map[string]interface{})
	require.True(t, ok, "Definitions should be a map")

	// Verify it contains some common K8s types
	require.Contains(t, definitions, "io.k8s.api.core.v1.ConfigMap", "Should contain ConfigMap definition")
	require.Contains(t, definitions, "io.k8s.api.apps.v1.Deployment", "Should contain Deployment definition")
	require.Contains(t, definitions, "io.k8s.api.core.v1.Pod", "Should contain Pod definition")

	t.Logf("Embedded OpenAPI spec size: %d bytes", len(defaultKubernetesOpenAPISpec))
	t.Logf("Embedded OpenAPI spec contains %d type definitions", len(definitions))
}

func TestFakeDynamicClientBasicOperations(t *testing.T) {
	tests := []struct {
		name           string
		testObjFactory func(name, namespace string) *unstructured.Unstructured
	}{
		{
			name:           "ConfigMap",
			testObjFactory: newConfigMap,
		},
		{
			name:           "Service",
			testObjFactory: newService,
		},
		{
			name:           "CustomResource",
			testObjFactory: newCustomResource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := setupScheme()
			client := NewFakeDynamicClient(scheme)
			testObj := tt.testObjFactory("test-resource", "default")

			// Test: Get on non-existing object should return error
			assertGetNotFound(t, client, testObj)

			// Test: Create via Apply
			applyResult := applyObj(t, client, testObj, "test-controller")

			// Test: Get existing object
			getResult := getObj(t, client, testObj)

			// Verify the objects match
			require.Equal(t, applyResult.GetName(), getResult.GetName())
			require.Equal(t, applyResult.GetNamespace(), getResult.GetNamespace())

			// Test: List operations
			list := listObjs(t, client, testObj)
			require.GreaterOrEqual(t, len(list.Items), 1)

			listItem := findInList(list, testObj.GetName())
			require.NotNil(t, listItem, "Should find created object in list")
			require.Equal(t, testObj.GetName(), listItem.GetName())
		})
	}
}

func TestFakeDynamicClientMultipleOperations(t *testing.T) {
	scheme := setupScheme()
	client := NewFakeDynamicClient(scheme)
	testObj := newConfigMap("multi-op-config", "default")

	// Step 1: Get on non-existing object should return error
	assertGetNotFound(t, client, testObj)

	// Step 2: Create the object using Apply
	applyObj(t, client, testObj, "test-controller")

	// Step 3: Get the object and verify it exists
	retrieved := getObj(t, client, testObj)
	require.Equal(t, "test", retrieved.GetLabels()["app"])

	// Verify data content
	data, found, err := unstructured.NestedStringMap(retrieved.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "value1", data["key1"])
	require.Equal(t, "value2", data["key2"])

	// Step 4: List objects and verify our object is there
	list := listObjs(t, client, testObj)
	listItem := findInList(list, testObj.GetName())
	require.NotNil(t, listItem)
	require.Equal(t, "test", listItem.GetLabels()["app"])

	// Step 5: Update the object with an Apply patch
	updatedTestObj := newConfigMap(testObj.GetName(), testObj.GetNamespace())
	updatedTestObj.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"app":     "test",
		"version": "v2",
		"env":     "production",
	}
	updatedTestObj.Object["data"] = map[string]interface{}{
		"key1":       "updated_value1",
		"key2":       "updated_value2",
		"newSetting": "newValue",
	}

	updateResult := applyObj(t, client, updatedTestObj, "test-controller")

	// Verify the update was applied
	require.Equal(t, "v2", updateResult.GetLabels()["version"])
	require.Equal(t, "production", updateResult.GetLabels()["env"])

	updatedData, found, err := unstructured.NestedStringMap(updateResult.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "updated_value1", updatedData["key1"])
	require.Equal(t, "updated_value2", updatedData["key2"])
	require.Equal(t, "newValue", updatedData["newSetting"])

	// Step 6: List again and verify the updated object
	finalList := listObjs(t, client, testObj)
	finalItem := findInList(finalList, testObj.GetName())
	require.NotNil(t, finalItem)

	// Verify all the updated fields are present in the list result
	require.Equal(t, "test", finalItem.GetLabels()["app"])
	require.Equal(t, "v2", finalItem.GetLabels()["version"])
	require.Equal(t, "production", finalItem.GetLabels()["env"])

	finalData, found, err := unstructured.NestedStringMap(finalItem.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "updated_value1", finalData["key1"])
	require.Equal(t, "updated_value2", finalData["key2"])
	require.Equal(t, "newValue", finalData["newSetting"])

	// Additional verification: Test list with label selector
	gvr := getGVR(testObj)
	labeledList, err := client.Resource(gvr).Namespace(testObj.GetNamespace()).List(
		t.Context(),
		metav1.ListOptions{
			LabelSelector: "app=test,env=production",
		},
	)
	require.NoError(t, err)
	require.Len(t, labeledList.Items, 1)
	require.Equal(t, testObj.GetName(), labeledList.Items[0].GetName())
}

func TestFakeDynamicClientApplyPatch(t *testing.T) {
	scheme := setupScheme()
	client := NewFakeDynamicClient(scheme)
	testObj := newService("test-service", "default")

	// Apply a Service using raw patch
	applyData, err := testObj.MarshalJSON()
	require.NoError(t, err)

	gvr := getGVR(testObj)
	result, err := client.Resource(gvr).Namespace(testObj.GetNamespace()).Patch(
		t.Context(),
		testObj.GetName(),
		types.ApplyPatchType,
		applyData,
		metav1.PatchOptions{
			FieldManager: "test-controller",
		},
	)
	require.NoError(t, err)

	// Verify the result directly from the patch operation
	require.Equal(t, "test-service", result.GetName())

	spec, found, err := unstructured.NestedMap(result.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)

	selector, found, err := unstructured.NestedStringMap(spec, "selector")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "test", selector["app"])
}

func TestFakeDynamicClientFieldManagers(t *testing.T) {
	scheme := setupScheme()
	client := NewFakeDynamicClient(scheme)

	// Create initial object with first field manager
	configMap1 := newConfigMap("shared-config", "default")
	configMap1.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"owner": "controller-1",
	}
	configMap1.Object["data"] = map[string]interface{}{
		"config1": "value1",
		"shared":  "initial",
	}

	result1 := applyObj(t, client, configMap1, "controller-1")

	// Verify first apply worked and check managed fields
	managedFields1 := result1.GetManagedFields()
	require.GreaterOrEqual(t, len(managedFields1), 1, "Should have managed fields after first apply")

	// Apply with second field manager, modifying some fields and adding new ones
	// Use Force=true to handle conflicts properly
	configMap2 := newConfigMap("shared-config", "default")
	configMap2.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"owner":      "controller-1", // Keep existing
		"managed-by": "controller-2", // New field
	}
	configMap2.Object["data"] = map[string]interface{}{
		"config1": "value1",  // Keep existing
		"shared":  "updated", // Update existing (conflicts with controller-1)
		"config2": "value2",  // New field
	}

	result2 := applyObjWithForce(t, client, configMap2, "controller-2")

	// Verify both field managers are tracked
	managedFields2 := result2.GetManagedFields()
	require.GreaterOrEqual(t, len(managedFields2), 1, "Should have managed fields after second apply")

	// Verify the final object has fields from both managers
	labels := result2.GetLabels()
	require.Equal(t, "controller-1", labels["owner"])
	require.Equal(t, "controller-2", labels["managed-by"])

	data, found, err := unstructured.NestedStringMap(result2.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "value1", data["config1"])
	require.Equal(t, "updated", data["shared"])
	require.Equal(t, "value2", data["config2"])

	// Verify that managed fields contain field manager information
	foundFieldManager := false
	for _, mf := range managedFields2 {
		if mf.Manager != "" {
			foundFieldManager = true
			break
		}
	}
	require.True(t, foundFieldManager, "Should have field manager information in managed fields")
}

func TestFakeDynamicClientAutoDetection(t *testing.T) {
	scheme := setupScheme()

	// Test GVR mapping auto-detection
	testCases := []struct {
		gvr      schema.GroupVersionResource
		expected bool
	}{
		{
			gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
			expected: true,
		},
		{
			gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"},
			expected: true,
		},
		{
			gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
			expected: true,
		},
		{
			gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			expected: true,
		},
		{
			gvr:      schema.GroupVersionResource{Group: "test.example.com", Version: "v1", Resource: "testresources"},
			expected: true,
		},
	}

	// Build the mapping to verify auto-detection
	gvrToListKind, err := buildGVRToListKindMapping(scheme)
	require.NoError(t, err)

	for _, tc := range testCases {
		if tc.expected {
			_, exists := gvrToListKind[tc.gvr]
			require.True(t, exists, "GVR should be auto-detected: %v", tc.gvr)
		}
	}

	// Test that the client works with auto-detected custom resource
	client := NewFakeDynamicClient(scheme)
	testObj := newCustomResource("test-cr", "default")

	applyObj(t, client, testObj, "test-controller")
	result := getObj(t, client, testObj)

	spec, found, err := unstructured.NestedMap(result.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)

	replicas, found, err := unstructured.NestedInt64(spec, "replicas")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(3), replicas)
}

func TestFakeDynamicClientWithCustomListKinds(t *testing.T) {
	scheme := setupScheme()

	// Define custom GVR mappings that override auto-detection
	customMappings := map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "configmaps"}:                "CustomConfigMapList",
		{Group: "custom.example.com", Version: "v1", Resource: "gadgets"}: "GadgetList",
	}

	// Register the custom resource types
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "custom.example.com", Version: "v1", Kind: "Gadget",
	}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "custom.example.com", Version: "v1", Kind: "GadgetList",
	}, &unstructured.UnstructuredList{})

	client := NewFakeDynamicClientWithCustomListKinds(scheme, customMappings)

	// Test with standard resource using custom mapping
	configMapTestObj := newConfigMap("test-config", "default")
	applyObj(t, client, configMapTestObj, "test-controller")
	getObj(t, client, configMapTestObj)

	// Test that custom resource works
	gadgetGVR := schema.GroupVersionResource{
		Group: "custom.example.com", Version: "v1", Resource: "gadgets",
	}
	gadgetObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "custom.example.com/v1",
			"kind":       "Gadget",
			"metadata": map[string]interface{}{
				"name":      "test-gadget",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"size": "large",
			},
		},
	}

	_, err := client.Resource(gadgetGVR).Namespace("default").Apply(
		t.Context(),
		"test-gadget",
		gadgetObj,
		metav1.ApplyOptions{FieldManager: "test-controller"},
	)
	require.NoError(t, err)
}

func TestFakeDynamicClientWithInitialObjects(t *testing.T) {
	scheme := setupScheme()
	testObj := newConfigMap("existing-config", "default")

	// Create client with initial object
	client := NewFakeDynamicClient(scheme, testObj)

	// Test Get on pre-existing object
	result := getObj(t, client, testObj)
	data, found, err := unstructured.NestedStringMap(result.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "value1", data["key1"])

	// Test Apply on pre-existing object (should update)
	testObj.Object["data"] = map[string]interface{}{
		"key1":   "value1",
		"key2":   "value2",
		"newkey": "newvalue",
	}
	updateResult := applyObj(t, client, testObj, "test-controller")

	updatedData, found, err := unstructured.NestedStringMap(updateResult.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "newvalue", updatedData["newkey"])
}

// TestServerSideApplyBehavior tests various server-side apply scenarios to
// confirm the fake dynamic client behaves like a real Kubernetes API server
// with respect to server-side apply semantics.
func TestDoubleRegistrationWithCRD(t *testing.T) {
	// Create a test CRD
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapps.example.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "MyApp",
				Plural:   "myapps",
				Singular: "myapp",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"replicas": {Type: "integer"},
										"image":    {Type: "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create scheme and manually register the same types that the CRD will register
	scheme := runtime.NewScheme()
	if err := kscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Manually register the CRD types in the scheme (simulating what might happen
	// if someone pre-registers types before creating the fake client)
	gvk := schema.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    "MyApp",
	}
	listGVK := schema.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    "MyAppList",
	}

	// This should not cause issues even though NewClient
	// will also try to register the same types
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	// Create the fake client with both the scheme that already has the types
	// registered AND the CRD (which will attempt to register them again)
	client := NewClient(scheme, WithCRDs(crd))

	// Test basic operations to ensure everything works despite potential double registration
	appGVR := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "myapps",
	}

	myApp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "MyApp",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"replicas": int64(3),
				"image":    "nginx:latest",
			},
		},
	}

	// Test Create operation
	created, err := client.Resource(appGVR).Namespace("default").Create(
		t.Context(), myApp, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, "test-app", created.GetName())

	// Test Get operation
	retrieved, err := client.Resource(appGVR).Namespace("default").Get(
		t.Context(), "test-app", metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.Equal(t, "test-app", retrieved.GetName())

	// Test List operation
	list, err := client.Resource(appGVR).Namespace("default").List(
		t.Context(), metav1.ListOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Len(t, list.Items, 1)
	require.Equal(t, "test-app", list.Items[0].GetName())

	// Test Apply operation (server-side apply)
	updatedApp := myApp.DeepCopy()
	if err := unstructured.SetNestedField(updatedApp.Object, int64(5), "spec", "replicas"); err != nil {
		t.Fatalf("failed to set replicas field: %v", err)
	}

	applied, err := client.Resource(appGVR).Namespace("default").Apply(
		t.Context(), "test-app", updatedApp, metav1.ApplyOptions{
			FieldManager: "test-manager",
			Force:        true,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, applied)

	// Verify the update took effect
	replicas, found, err := unstructured.NestedInt64(applied.Object, "spec", "replicas")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(5), replicas)
}

func TestMultiVersionCRD(t *testing.T) {
	// Create a CRD with multiple versions
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "widgets.example.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Widget",
				Plural:   "widgets",
				Singular: "widget",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"size": {Type: "string"},
									},
								},
							},
						},
					},
				},
				{
					Name:    "v1beta1",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"size":     {Type: "string"},
										"replicas": {Type: "integer"},
									},
								},
							},
						},
					},
				},
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"size":     {Type: "string"},
										"replicas": {Type: "integer"},
										"image":    {Type: "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	scheme := setupScheme()
	client := NewClient(scheme, WithCRDs(crd))

	// Test creating resources with different versions
	versions := []string{"v1alpha1", "v1beta1", "v1"}

	for _, version := range versions {
		t.Run("Version_"+version, func(t *testing.T) {
			gvr := schema.GroupVersionResource{
				Group:    "example.com",
				Version:  version,
				Resource: "widgets",
			}

			widget := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "example.com/" + version,
					"kind":       "Widget",
					"metadata": map[string]interface{}{
						"name":      "test-widget-" + version,
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"size": "large",
					},
				},
			}

			// Add version-specific fields
			if version == "v1beta1" || version == "v1" {
				if err := unstructured.SetNestedField(widget.Object, int64(3), "spec", "replicas"); err != nil {
					t.Fatalf("failed to set replicas field: %v", err)
				}
			}
			if version == "v1" {
				if err := unstructured.SetNestedField(widget.Object, "nginx:latest", "spec", "image"); err != nil {
					t.Fatalf("failed to set image field: %v", err)
				}
			}

			// Test Create
			created, err := client.Resource(gvr).Namespace("default").Create(
				t.Context(), widget, metav1.CreateOptions{},
			)
			require.NoError(t, err, "Failed to create widget with version %s", version)
			require.NotNil(t, created)
			require.Equal(t, "test-widget-"+version, created.GetName())
			require.Equal(t, "example.com/"+version, created.GetAPIVersion())

			// Test Get
			retrieved, err := client.Resource(gvr).Namespace("default").Get(
				t.Context(), "test-widget-"+version, metav1.GetOptions{},
			)
			require.NoError(t, err, "Failed to get widget with version %s", version)
			require.NotNil(t, retrieved)
			require.Equal(t, "test-widget-"+version, retrieved.GetName())
			require.Equal(t, "example.com/"+version, retrieved.GetAPIVersion())

			// Test List
			list, err := client.Resource(gvr).Namespace("default").List(
				t.Context(), metav1.ListOptions{},
			)
			require.NoError(t, err, "Failed to list widgets with version %s", version)
			require.NotNil(t, list)
			require.Len(t, list.Items, 1)
			require.Equal(t, "test-widget-"+version, list.Items[0].GetName())
		})
	}

	// Test that all versions are properly registered and accessible
	t.Run("CrossVersionAccess", func(t *testing.T) {
		// List widgets across all versions to ensure they're all accessible
		for _, version := range versions {
			gvr := schema.GroupVersionResource{
				Group:    "example.com",
				Version:  version,
				Resource: "widgets",
			}

			list, err := client.Resource(gvr).Namespace("default").List(
				t.Context(), metav1.ListOptions{},
			)
			require.NoError(t, err, "Failed to list widgets with version %s", version)
			require.NotNil(t, list)
			require.Len(t, list.Items, 1, "Expected 1 widget for version %s", version)

			// Verify the widget has the correct version
			widget := list.Items[0]
			require.Equal(t, "example.com/"+version, widget.GetAPIVersion())
			require.Equal(t, "test-widget-"+version, widget.GetName())
		}
	})
}

func TestGoEmbedExample(t *testing.T) {
	scheme := setupScheme()

	// Create fake client using embedded CRDs
	client := NewClient(scheme, WithCRDBytes(embeddedCRDs))

	// Test the embedded Widget CRD
	widgetGVR := schema.GroupVersionResource{
		Group:    "embed.example.com",
		Version:  "v1",
		Resource: "widgets",
	}

	widget := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "embed.example.com/v1",
			"kind":       "Widget",
			"metadata": map[string]interface{}{
				"name":      "embedded-widget",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"size":     "large",
				"replicas": int64(3),
				"enabled":  true,
			},
		},
	}

	// Create the widget
	created, err := client.Resource(widgetGVR).Namespace("default").Create(
		t.Context(), widget, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, "embedded-widget", created.GetName())

	// Verify we can retrieve it
	retrieved, err := client.Resource(widgetGVR).Namespace("default").Get(
		t.Context(), "embedded-widget", metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, "embedded-widget", retrieved.GetName())

	// Test the embedded Tool CRD with multiple versions
	toolGVRv1alpha1 := schema.GroupVersionResource{
		Group:    "embed.example.com",
		Version:  "v1alpha1",
		Resource: "tools",
	}

	toolGVRv1 := schema.GroupVersionResource{
		Group:    "embed.example.com",
		Version:  "v1",
		Resource: "tools",
	}

	// Create tool with v1alpha1
	toolV1Alpha1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "embed.example.com/v1alpha1",
			"kind":       "Tool",
			"metadata": map[string]interface{}{
				"name":      "tool-v1alpha1",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"name": "hammer",
			},
		},
	}

	created, err = client.Resource(toolGVRv1alpha1).Namespace("default").Create(
		t.Context(), toolV1Alpha1, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, "embed.example.com/v1alpha1", created.GetAPIVersion())

	// Create tool with v1 (has additional fields)
	toolV1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "embed.example.com/v1",
			"kind":       "Tool",
			"metadata": map[string]interface{}{
				"name":      "tool-v1",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"name":        "screwdriver",
				"description": "A Phillips head screwdriver",
				"weight":      int64(250),
			},
		},
	}

	created, err = client.Resource(toolGVRv1).Namespace("default").Create(
		t.Context(), toolV1, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, "embed.example.com/v1", created.GetAPIVersion())

	// Verify both versions are accessible independently
	listV1Alpha1, err := client.Resource(toolGVRv1alpha1).Namespace("default").List(
		t.Context(), metav1.ListOptions{},
	)
	require.NoError(t, err)
	require.Len(t, listV1Alpha1.Items, 1)

	listV1, err := client.Resource(toolGVRv1).Namespace("default").List(
		t.Context(), metav1.ListOptions{},
	)
	require.NoError(t, err)
	require.Len(t, listV1.Items, 1)
}

func TestMultipleEmbedFiles(t *testing.T) {
	scheme := setupScheme()

	// Combine multiple embedded CRDs
	combinedCRDs := widgetCRD
	combinedCRDs = append(combinedCRDs, []byte("\n---\n")...)
	combinedCRDs = append(combinedCRDs, toolCRD...)

	client := NewClient(scheme, WithCRDBytes(combinedCRDs))

	// Test that both CRDs are loaded and functional
	widgetGVR := schema.GroupVersionResource{
		Group:    "multi.example.com",
		Version:  "v1",
		Resource: "widgets",
	}

	toolGVR := schema.GroupVersionResource{
		Group:    "multi.example.com",
		Version:  "v1",
		Resource: "tools",
	}

	// Create a widget
	widget := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "multi.example.com/v1",
			"kind":       "Widget",
			"metadata": map[string]interface{}{
				"name":      "multi-widget",
				"namespace": "default",
			},
		},
	}

	_, err := client.Resource(widgetGVR).Namespace("default").Create(
		t.Context(), widget, metav1.CreateOptions{},
	)
	require.NoError(t, err)

	// Create a tool
	tool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "multi.example.com/v1",
			"kind":       "Tool",
			"metadata": map[string]interface{}{
				"name":      "multi-tool",
				"namespace": "default",
			},
		},
	}

	_, err = client.Resource(toolGVR).Namespace("default").Create(
		t.Context(), tool, metav1.CreateOptions{},
	)
	require.NoError(t, err)
}

func TestNewClientWithCRDBytes(t *testing.T) {
	// Test CRD data as bytes (simulating go:embed)
	crdYAML := `---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
    singular: widget
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          apiVersion:
            type: string
          kind:
            type: string
          metadata:
            type: object
          spec:
            type: object
            properties:
              size:
                type: string
              replicas:
                type: integer
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
spec:
  group: example.com
  names:
    kind: Gadget
    plural: gadgets
    singular: gadget
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          apiVersion:
            type: string
          kind:
            type: string
          metadata:
            type: object
          spec:
            type: object
            properties:
              color:
                type: string`

	scheme := setupScheme()
	client := NewClient(scheme, WithCRDBytes([]byte(crdYAML)))

	// Test Widget CRD
	t.Run("Widget", func(t *testing.T) {
		widgetGVR := schema.GroupVersionResource{
			Group:    "example.com",
			Version:  "v1",
			Resource: "widgets",
		}

		widget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1",
				"kind":       "Widget",
				"metadata": map[string]interface{}{
					"name":      "test-widget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"size":     "large",
					"replicas": int64(3),
				},
			},
		}

		created, err := client.Resource(widgetGVR).Namespace("default").Create(
			t.Context(), widget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "test-widget", created.GetName())
	})

	// Test Gadget CRD
	t.Run("Gadget", func(t *testing.T) {
		gadgetGVR := schema.GroupVersionResource{
			Group:    "example.com",
			Version:  "v1alpha1",
			Resource: "gadgets",
		}

		gadget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1alpha1",
				"kind":       "Gadget",
				"metadata": map[string]interface{}{
					"name":      "test-gadget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"color": "blue",
				},
			},
		}

		created, err := client.Resource(gadgetGVR).Namespace("default").Create(
			t.Context(), gadget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "test-gadget", created.GetName())
	})
}

func TestNewClientWithCRDBytesAndSpec(t *testing.T) {
	crdYAML := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: apps.example.com
spec:
  group: example.com
  names:
    kind: App
    plural: apps
    singular: app
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          apiVersion:
            type: string
          kind:
            type: string
          metadata:
            type: object
          spec:
            type: object
            properties:
              image:
                type: string`

	scheme := setupScheme()
	// Test with embedded spec (empty string means use default embedded spec)
	client := NewClient(scheme, WithCRDBytes([]byte(crdYAML)))

	appGVR := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "apps",
	}

	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "App",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"image": "nginx:latest",
			},
		},
	}

	created, err := client.Resource(appGVR).Namespace("default").Create(
		t.Context(), app, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, "test-app", created.GetName())
}

func TestParseCRDsFromBytesErrorHandling(t *testing.T) {
	t.Run("InvalidYAML", func(t *testing.T) {
		invalidYAML := `invalid: yaml: content: [unclosed`
		scheme := setupScheme()

		require.Panics(t, func() {
			NewClient(scheme, WithCRDBytes([]byte(invalidYAML)))
		})
	})

	t.Run("NonCRDDocument", func(t *testing.T) {
		nonCRDYAML := `apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
  - name: test
    image: nginx`

		scheme := setupScheme()
		// Should not panic, just skip non-CRD documents
		client := NewClient(scheme, WithCRDBytes([]byte(nonCRDYAML)))
		require.NotNil(t, client)
	})

	t.Run("MixedDocuments", func(t *testing.T) {
		mixedYAML := `apiVersion: v1
kind: Pod
metadata:
  name: test-pod
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
    singular: widget
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
---
apiVersion: v1
kind: Service
metadata:
  name: test-service`

		scheme := setupScheme()
		client := NewClient(scheme, WithCRDBytes([]byte(mixedYAML)))

		// Should work with the one valid CRD
		widgetGVR := schema.GroupVersionResource{
			Group:    "example.com",
			Version:  "v1",
			Resource: "widgets",
		}

		widget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1",
				"kind":       "Widget",
				"metadata": map[string]interface{}{
					"name":      "test-widget",
					"namespace": "default",
				},
			},
		}

		created, err := client.Resource(widgetGVR).Namespace("default").Create(
			t.Context(), widget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
	})
}

func TestDoubleRegistrationWithTypedStruct(t *testing.T) {
	// Create a test CRD
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "clusters.cockroach.cloud",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cockroach.cloud",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Cluster",
				Plural:   "clusters",
				Singular: "cluster",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"apiVersion": {Type: "string"},
								"kind":       {Type: "string"},
								"metadata":   {Type: "object"},
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"name": {Type: "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create scheme
	scheme := runtime.NewScheme()
	if err := kscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Pre-register the same GVK that the CRD will try to register
	// This simulates the case where a consuming application has already
	// registered types in the scheme before creating the fake client
	gvk := schema.GroupVersionKind{
		Group:   "cockroach.cloud",
		Version: "v1alpha1",
		Kind:    "Cluster",
	}
	listGVK := schema.GroupVersionKind{
		Group:   "cockroach.cloud",
		Version: "v1alpha1",
		Kind:    "ClusterList",
	}

	// Register as unstructured types first (simulating pre-registration)
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	// This should NOT panic due to double registration
	// Our fix should prevent the fake client from trying to register the same GVK
	// again when it's already registered in the scheme
	client := NewClient(scheme, WithCRDs(crd))

	// Test that the client works correctly
	clusterGVR := schema.GroupVersionResource{
		Group:    "cockroach.cloud",
		Version:  "v1alpha1",
		Resource: "clusters",
	}

	cluster := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cockroach.cloud/v1alpha1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":      "test-cluster",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"name": "my-cluster",
			},
		},
	}

	// Test Create operation
	created, err := client.Resource(clusterGVR).Namespace("default").Create(
		t.Context(), cluster, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Test Get operation
	retrieved, err := client.Resource(clusterGVR).Namespace("default").Get(
		t.Context(), "test-cluster", metav1.GetOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.Equal(t, "test-cluster", retrieved.GetName())
}

func TestTypedObjectsSupport(t *testing.T) {
	scheme := setupScheme()

	// Create a typed Kubernetes object (Deployment)
	typedDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(3)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx:1.21",
						},
					},
				},
			},
		},
	}

	// Create a typed ConfigMap
	typedConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	client := NewFakeDynamicClient(scheme, typedDeployment, typedConfigMap)
	require.NotNil(t, client)

	// Test that we can interact with the typed objects
	deploymentGVR := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	configMapGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	// Test List operation
	deploymentList, err := client.Resource(deploymentGVR).Namespace("default").List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, deploymentList.Items, 1)
	require.Equal(t, "test-deployment", deploymentList.Items[0].GetName())

	configMapList, err := client.Resource(configMapGVR).Namespace("default").List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, configMapList.Items, 1)
	require.Equal(t, "test-config", configMapList.Items[0].GetName())

	// Test Get operation
	deployment, err := client.Resource(deploymentGVR).Namespace("default").Get(t.Context(), "test-deployment", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "test-deployment", deployment.GetName())

	// Verify the deployment has the correct spec
	replicas, found, err := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(3), replicas)

	configMap, err := client.Resource(configMapGVR).Namespace("default").Get(t.Context(), "test-config", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "test-config", configMap.GetName())

	// Verify the configmap has the correct data
	data, found, err := unstructured.NestedStringMap(configMap.Object, "data")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "value1", data["key1"])
	require.Equal(t, "value2", data["key2"])
}

func TestMixedTypedAndUnstructuredObjects(t *testing.T) {
	scheme := setupScheme()

	// Create a typed object
	typedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "typed-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nginx:1.21",
				},
			},
		},
	}

	// Create an unstructured object
	unstructuredConfigMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "unstructured-config",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		},
	}

	// This should handle both typed and unstructured objects correctly
	client := NewFakeDynamicClient(scheme, typedPod, unstructuredConfigMap)
	require.NotNil(t, client)

	// Test that both objects are accessible
	podGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	}

	configMapGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	// Get the typed pod
	pod, err := client.Resource(podGVR).Namespace("default").Get(t.Context(), "typed-pod", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "typed-pod", pod.GetName())

	// Get the unstructured configmap
	configMap, err := client.Resource(configMapGVR).Namespace("default").Get(t.Context(), "unstructured-config", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "unstructured-config", configMap.GetName())
}

func TestNewClientWithOptions(t *testing.T) {
	scheme := setupScheme()

	t.Run("WithNoOptions", func(t *testing.T) {
		client := NewClient(scheme)
		require.NotNil(t, client)

		// Test basic operations work
		configMapGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		cm := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-cm",
					"namespace": "default",
				},
				"data": map[string]interface{}{
					"key": "value",
				},
			},
		}

		created, err := client.Resource(configMapGVR).Namespace("default").Create(
			t.Context(), cm, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
	})

	t.Run("WithObjects", func(t *testing.T) {
		existingCM := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "existing-cm",
					"namespace": "default",
				},
				"data": map[string]interface{}{
					"key": "value",
				},
			},
		}

		client := NewClient(scheme, WithObjects(existingCM))

		configMapGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		// Should be able to get the existing object
		retrieved, err := client.Resource(configMapGVR).Namespace("default").Get(
			t.Context(), "existing-cm", metav1.GetOptions{},
		)
		require.NoError(t, err)
		require.Equal(t, "existing-cm", retrieved.GetName())
	})

	t.Run("WithCRDs", func(t *testing.T) {
		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "widgets.test.example.com",
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "test.example.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Kind:     "Widget",
					Plural:   "widgets",
					Singular: "widget",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"apiVersion": {Type: "string"},
									"kind":       {Type: "string"},
									"metadata":   {Type: "object"},
									"spec": {
										Type: "object",
										Properties: map[string]apiextensionsv1.JSONSchemaProps{
											"name": {Type: "string"},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		client := NewClient(scheme, WithCRDs(crd))

		widgetGVR := schema.GroupVersionResource{
			Group:    "test.example.com",
			Version:  "v1",
			Resource: "widgets",
		}

		widget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "test.example.com/v1",
				"kind":       "Widget",
				"metadata": map[string]interface{}{
					"name":      "test-widget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-widget",
				},
			},
		}

		created, err := client.Resource(widgetGVR).Namespace("default").Create(
			t.Context(), widget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "test-widget", created.GetName())
	})

	t.Run("WithCRDBytes", func(t *testing.T) {
		crdYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "gadgets.test.example.com"
spec:
  group: "test.example.com"
  names:
    kind: "Gadget"
    plural: "gadgets"
    singular: "gadget"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"
              properties:
                name:
                  type: "string"`

		client := NewClient(scheme, WithCRDBytes([]byte(crdYAML)))

		gadgetGVR := schema.GroupVersionResource{
			Group:    "test.example.com",
			Version:  "v1",
			Resource: "gadgets",
		}

		gadget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "test.example.com/v1",
				"kind":       "Gadget",
				"metadata": map[string]interface{}{
					"name":      "test-gadget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-gadget",
				},
			},
		}

		created, err := client.Resource(gadgetGVR).Namespace("default").Create(
			t.Context(), gadget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "test-gadget", created.GetName())
	})

	t.Run("WithCustomGVRMappings", func(t *testing.T) {
		customMappings := map[schema.GroupVersionResource]string{
			{Group: "custom.example.com", Version: "v1", Resource: "myresources"}: "MyResourceList",
		}

		client := NewClient(scheme, WithCustomGVRMappings(customMappings))
		require.NotNil(t, client)

		// The custom mapping should be available (though we can't easily test it without actually using it)
		// This test mainly ensures the option works without panicking
	})

	t.Run("WithMultipleOptions", func(t *testing.T) {
		existingCM := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "multi-cm",
					"namespace": "default",
				},
			},
		}

		crdYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "tools.multi.example.com"
spec:
  group: "multi.example.com"
  names:
    kind: "Tool"
    plural: "tools"
    singular: "tool"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"`

		customMappings := map[schema.GroupVersionResource]string{
			{Group: "multi.example.com", Version: "v1", Resource: "tools"}: "ToolList",
		}

		client := NewClient(scheme,
			WithObjects(existingCM),
			WithCRDBytes([]byte(crdYAML)),
			WithCustomGVRMappings(customMappings),
		)

		// Test the existing object
		configMapGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		retrieved, err := client.Resource(configMapGVR).Namespace("default").Get(
			t.Context(), "multi-cm", metav1.GetOptions{},
		)
		require.NoError(t, err)
		require.Equal(t, "multi-cm", retrieved.GetName())

		// Test the CRD from bytes
		toolGVR := schema.GroupVersionResource{
			Group:    "multi.example.com",
			Version:  "v1",
			Resource: "tools",
		}

		tool := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "multi.example.com/v1",
				"kind":       "Tool",
				"metadata": map[string]interface{}{
					"name":      "multi-tool",
					"namespace": "default",
				},
			},
		}

		created, err := client.Resource(toolGVR).Namespace("default").Create(
			t.Context(), tool, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.Equal(t, "multi-tool", created.GetName())
	})

	t.Run("WithMultipleCRDBytes", func(t *testing.T) {
		widgetCRDYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "widgets.multi.example.com"
spec:
  group: "multi.example.com"
  names:
    kind: "Widget"
    plural: "widgets"
    singular: "widget"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"
              properties:
                name:
                  type: "string"`

		gadgetCRDYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "gadgets.multi.example.com"
spec:
  group: "multi.example.com"
  names:
    kind: "Gadget"
    plural: "gadgets"
    singular: "gadget"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"
              properties:
                name:
                  type: "string"`

		// Create client with multiple CRD byte slices
		client := NewClient(scheme, WithCRDBytes([]byte(widgetCRDYAML), []byte(gadgetCRDYAML)))

		// Test Widget CRD
		widgetGVR := schema.GroupVersionResource{
			Group:    "multi.example.com",
			Version:  "v1",
			Resource: "widgets",
		}

		widget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "multi.example.com/v1",
				"kind":       "Widget",
				"metadata": map[string]interface{}{
					"name":      "multi-widget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-widget",
				},
			},
		}

		created, err := client.Resource(widgetGVR).Namespace("default").Create(
			t.Context(), widget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "multi-widget", created.GetName())

		// Test Gadget CRD
		gadgetGVR := schema.GroupVersionResource{
			Group:    "multi.example.com",
			Version:  "v1",
			Resource: "gadgets",
		}

		gadget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "multi.example.com/v1",
				"kind":       "Gadget",
				"metadata": map[string]interface{}{
					"name":      "multi-gadget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-gadget",
				},
			},
		}

		created, err = client.Resource(gadgetGVR).Namespace("default").Create(
			t.Context(), gadget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "multi-gadget", created.GetName())
	})

	t.Run("WithMultipleCRDBytesCallsChained", func(t *testing.T) {
		widgetCRDYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "widgets.chained.example.com"
spec:
  group: "chained.example.com"
  names:
    kind: "Widget"
    plural: "widgets"
    singular: "widget"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"
              properties:
                name:
                  type: "string"`

		gadgetCRDYAML := `---
apiVersion: "apiextensions.k8s.io/v1"
kind: "CustomResourceDefinition"
metadata:
  name: "gadgets.chained.example.com"
spec:
  group: "chained.example.com"
  names:
    kind: "Gadget"
    plural: "gadgets"
    singular: "gadget"
  scope: "Namespaced"
  versions:
    - name: "v1"
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: "object"
          properties:
            apiVersion:
              type: "string"
            kind:
              type: "string"
            metadata:
              type: "object"
            spec:
              type: "object"
              properties:
                name:
                  type: "string"`

		// Create client with chained WithCRDBytes calls
		client := NewClient(scheme,
			WithCRDBytes([]byte(widgetCRDYAML)),
			WithCRDBytes([]byte(gadgetCRDYAML)),
		)

		// Test Widget CRD
		widgetGVR := schema.GroupVersionResource{
			Group:    "chained.example.com",
			Version:  "v1",
			Resource: "widgets",
		}

		widget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "chained.example.com/v1",
				"kind":       "Widget",
				"metadata": map[string]interface{}{
					"name":      "chained-widget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-chained-widget",
				},
			},
		}

		created, err := client.Resource(widgetGVR).Namespace("default").Create(
			t.Context(), widget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "chained-widget", created.GetName())

		// Test Gadget CRD
		gadgetGVR := schema.GroupVersionResource{
			Group:    "chained.example.com",
			Version:  "v1",
			Resource: "gadgets",
		}

		gadget := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "chained.example.com/v1",
				"kind":       "Gadget",
				"metadata": map[string]interface{}{
					"name":      "chained-gadget",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"name": "my-chained-gadget",
				},
			},
		}

		created, err = client.Resource(gadgetGVR).Namespace("default").Create(
			t.Context(), gadget, metav1.CreateOptions{},
		)
		require.NoError(t, err)
		require.NotNil(t, created)
		require.Equal(t, "chained-gadget", created.GetName())
	})
}
