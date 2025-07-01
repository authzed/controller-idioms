# Fake Dynamic Client

This package provides enhanced fake dynamic clients for testing Kubernetes controllers with Custom Resource Definitions (CRDs).

## Features

- **CRD Support**: Automatic registration of CRD types with proper schema validation
- **Multi-Version CRDs**: Full support for CRDs with multiple API versions
- **Server-Side Apply**: Proper field management and strategic merge patch support
- **Embedded CRDs**: Load CRDs from embedded byte data using `go:embed`
- **OpenAPI Integration**: Custom OpenAPI spec support for different Kubernetes versions

## Quick Start

### Basic Usage

```go
import (
    "github.com/authzed/controller-idioms/client/fake"
    apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
    "k8s.io/apimachinery/pkg/runtime"
)

func TestMyController(t *testing.T) {
    scheme := runtime.NewScheme()
    
    // Create a fake client with CRDs
    client := fake.NewFakeDynamicClientWithCRDs(scheme, []*apiextensionsv1.CustomResourceDefinition{
        myCRD,
    })
    
    // Use the client in your tests...
}
```

### Using Embedded CRDs

The most convenient way to use CRDs in tests is with `go:embed`:

```go
package mycontroller_test

import (
    "context"
    _ "embed"
    "testing"
    
    "github.com/authzed/controller-idioms/client/fake"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
)

// Embed your CRDs directly into the test binary
//go:embed testdata/my-crds.yaml
var embeddedCRDs []byte

func TestWithEmbeddedCRDs(t *testing.T) {
    scheme := runtime.NewScheme()
    
    // Create client with embedded CRDs
    client := fake.NewFakeDynamicClientWithCRDBytes(scheme, embeddedCRDs)
    
    // Create a custom resource
    myResource := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "example.com/v1",
            "kind":       "MyResource",
            "metadata": map[string]interface{}{
                "name":      "test-resource",
                "namespace": "default",
            },
            "spec": map[string]interface{}{
                "replicas": 3,
            },
        },
    }
    
    gvr := schema.GroupVersionResource{
        Group:    "example.com",
        Version:  "v1",
        Resource: "myresources",
    }
    
    // Test CRUD operations
    created, err := client.Resource(gvr).Namespace("default").Create(
        context.TODO(), myResource, metav1.CreateOptions{},
    )
    // ... rest of test
}
```

### Multiple CRD Files

You can combine multiple embedded files:

```go
//go:embed testdata/widgets.yaml
var widgetCRD []byte

//go:embed testdata/gadgets.yaml
var gadgetCRD []byte

func TestMultipleCRDs(t *testing.T) {
    // Combine multiple CRD files
    combinedCRDs := append(widgetCRD, []byte("\n---\n")...)
    combinedCRDs = append(combinedCRDs, gadgetCRD...)
    
    scheme := runtime.NewScheme()
    client := fake.NewFakeDynamicClientWithCRDBytes(scheme, combinedCRDs)
    
    // Both CRDs are now available...
}
```

### Multi-Version CRDs

The fake client fully supports CRDs with multiple versions:

```yaml
# testdata/multi-version-crd.yaml
apiVersion: apiextensions.k8s.io/v1
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
  - name: v1alpha1
    served: true
    storage: false
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              image:
                type: string
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              image:
                type: string
              replicas:
                type: integer
```

```go
func TestMultiVersion(t *testing.T) {
    client := fake.NewFakeDynamicClientWithCRDBytes(scheme, embeddedCRD)
    
    // Create resources using different API versions
    v1alpha1GVR := schema.GroupVersionResource{
        Group: "example.com", Version: "v1alpha1", Resource: "apps",
    }
    
    v1GVR := schema.GroupVersionResource{
        Group: "example.com", Version: "v1", Resource: "apps",
    }
    
    // Both versions work independently
    _, err := client.Resource(v1alpha1GVR).Create(/* ... */)
    _, err = client.Resource(v1GVR).Create(/* ... */)
}
```

## Available Constructors

### `NewFakeDynamicClient(scheme)`

Basic fake client without CRD support.

### `NewFakeDynamicClientWithCRDs(scheme, crds, objects...)`

Fake client with CRD support using parsed CRD objects.

### `NewFakeDynamicClientWithCRDBytes(scheme, crdData, objects...)`

Fake client with CRDs loaded from embedded byte data. Supports YAML and JSON, multiple documents separated by `---`.

### `NewFakeDynamicClientWithCRDBytesAndSpec(scheme, crdData, specPath, objects...)`

Like `NewFakeDynamicClientWithCRDBytes` but allows specifying a custom OpenAPI spec file.

### `NewFakeDynamicClientWithOpenAPISpec(scheme, specPath, crds, objects...)`

Fake client with custom OpenAPI spec support for testing against different Kubernetes versions.

## CRD File Format

The `crdData` parameter accepts:

- Single CRD in YAML or JSON format
- Multiple CRDs separated by `---` (YAML multi-document format)
- Mixed documents (non-CRD documents are ignored)

Example multi-document format:

```yaml
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
# ... widget CRD spec
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
# ... gadget CRD spec
```

## Error Handling

All constructors panic on errors since they're designed for test usage. Common issues:

- **Invalid YAML/JSON**: Ensure your embedded files are valid
- **Missing schema**: CRDs without OpenAPI schemas are supported but won't have validation
- **Invalid CRD**: Non-CRD documents in the input are silently ignored

## Best Practices

1. **Use `go:embed`**: Embed CRDs directly in your test files for better maintainability
2. **Version your CRDs**: Test against multiple API versions if your controller supports them
3. **Separate test data**: Keep CRD files in a `testdata/` directory
4. **Validate schemas**: Include proper OpenAPI v3 schemas in your CRDs for realistic testing
5. **Test field management**: Use server-side apply to test field management behavior

## Server-Side Apply Support

The fake client supports server-side apply with proper field management:

```go
// Apply with field manager
applied, err := client.Resource(gvr).Namespace("default").Apply(
    context.TODO(), "resource-name", resource, 
    metav1.ApplyOptions{
        FieldManager: "my-controller",
        Force:        true, // Use when conflicts are expected
    },
)
```

The fake client will track field ownership and handle strategic merge patches according to the CRD's OpenAPI schema.
