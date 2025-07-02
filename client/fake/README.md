# Fake Dynamic Client

Enhanced fake dynamic client for testing Kubernetes controllers with Custom Resource Definitions (CRDs).

## Features

- **Server-Side Apply**: Full field management and strategic merge patch support
- **CRD Support**: Automatic registration and schema validation
- **Multi-Version CRDs**: Support for CRDs with multiple API versions
- **Embedded CRDs**: Load CRDs from `go:embed` byte data
- **Flexible API**: Functional options for easy configuration

## Quick Start

### Basic Usage

```go
import "github.com/authzed/controller-idioms/client/fake"

// Basic client
client := fake.NewClient(scheme)

// With CRDs and initial objects
client := fake.NewClient(scheme,
    fake.WithCRDs(crd1, crd2),
    fake.WithObjects(existingObject),
)

// With embedded CRD files
client := fake.NewClient(scheme,
    fake.WithCRDBytes(embeddedCRDs1, embeddedCRDs2),
)
```

### Using go:embed

```go
//go:embed testdata/my-crds.yaml
var embeddedCRDs []byte

func TestMyController(t *testing.T) {
    client := fake.NewClient(scheme, fake.WithCRDBytes(embeddedCRDs))
    
    // Create custom resources
    myResource := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "example.com/v1",
            "kind":       "MyResource",
            "metadata":   map[string]interface{}{"name": "test"},
            "spec":       map[string]interface{}{"replicas": 3},
        },
    }
    
    gvr := schema.GroupVersionResource{
        Group: "example.com", Version: "v1", Resource: "myresources",
    }
    
    created, err := client.Resource(gvr).Namespace("default").Create(
        context.TODO(), myResource, metav1.CreateOptions{},
    )
    // Test your controller logic...
}
```

## API Reference

### NewClient (Recommended)

```go
func NewClient(scheme *runtime.Scheme, opts ...ClientOption) dynamic.Interface
```

Main constructor with functional options:

- `WithCRDs(crds...)` - Add CRD objects
- `WithCRDBytes(data...)` - Add CRDs from YAML/JSON bytes
- `WithCustomGVRMappings(mappings)` - Custom GVR to ListKind mappings
- `WithOpenAPISpec(path)` - Custom OpenAPI spec file
- `WithObjects(objects...)` - Initial objects in the client

### Other Constructors

These constructors are provided to mirror the upstream dynamic client interface,
but they are less flexible than `NewClient`:

```go
func NewFakeDynamicClient(scheme *runtime.Scheme, objects ...runtime.Object) dynamic.Interface
func NewFakeDynamicClientWithCustomListKinds(scheme *runtime.Scheme, gvrToListKind map[schema.GroupVersionResource]string, objects ...runtime.Object) dynamic.Interface
```

## CRD Format

Supports YAML and JSON, single or multi-document:

```yaml
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
  # ... rest of CRD spec
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
# ... second CRD
```

## Examples

### Multiple CRD Sources

```go
client := fake.NewClient(scheme,
    fake.WithCRDs(parsedCRD),                    // From CRD objects
    fake.WithCRDBytes(widgetCRDs, gadgetCRDs),   // From embedded bytes
    fake.WithObjects(existingResources...),      // Pre-existing objects
)
```

### Server-Side Apply

```go
applied, err := client.Resource(gvr).Namespace("default").Apply(
    context.TODO(), "resource-name", resource,
    metav1.ApplyOptions{
        FieldManager: "my-controller",
        Force:        true,
    },
)
```

### Multi-Version CRDs

```go
// Different versions of the same resource
v1alpha1GVR := schema.GroupVersionResource{Group: "example.com", Version: "v1alpha1", Resource: "apps"}
v1GVR := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "apps"}

// Both work independently
client.Resource(v1alpha1GVR).Create(...)
client.Resource(v1GVR).Create(...)
```
