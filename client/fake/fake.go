package fake

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/structured-merge-diff/v4/typed"
)

// defaultKubernetesOpenAPISpec contains the embedded Kubernetes OpenAPI schema (v1.33.2).
// This is approximately 3.7MB and provides strategic merge patch support for all built-in types.
// Users can override this by providing a custom spec path to NewClient with WithOpenAPISpec.
//
//go:embed swagger.json
var defaultKubernetesOpenAPISpec []byte

// ClientOption configures a fake dynamic client
type ClientOption func(*clientConfig)

// clientConfig holds configuration for creating a fake client
type clientConfig struct {
	crds        []*apiextensionsv1.CustomResourceDefinition
	crdBytes    [][]byte
	gvrMappings map[schema.GroupVersionResource]string
	openAPISpec string
	objects     []runtime.Object
}

// WithCRDs adds CRDs to the fake client
func WithCRDs(crds ...*apiextensionsv1.CustomResourceDefinition) ClientOption {
	return func(c *clientConfig) {
		c.crds = append(c.crds, crds...)
	}
}

// WithCRDBytes adds CRDs from byte data (YAML/JSON) to the fake client
func WithCRDBytes(crdData ...[]byte) ClientOption {
	return func(c *clientConfig) {
		c.crdBytes = append(c.crdBytes, crdData...)
	}
}

// WithCustomGVRMappings adds custom GVR to ListKind mappings
func WithCustomGVRMappings(mappings map[schema.GroupVersionResource]string) ClientOption {
	return func(c *clientConfig) {
		if c.gvrMappings == nil {
			c.gvrMappings = make(map[schema.GroupVersionResource]string)
		}
		for gvr, listKind := range mappings {
			c.gvrMappings[gvr] = listKind
		}
	}
}

// WithOpenAPISpec sets a custom OpenAPI spec file path
func WithOpenAPISpec(specPath string) ClientOption {
	return func(c *clientConfig) {
		c.openAPISpec = specPath
	}
}

// WithObjects adds initial objects to the fake client
func WithObjects(objects ...runtime.Object) ClientOption {
	return func(c *clientConfig) {
		c.objects = append(c.objects, objects...)
	}
}

// NewClient creates a fake dynamic client with the specified options.
// This is the main constructor that all other constructors delegate to.
func NewClient(scheme *runtime.Scheme, opts ...ClientOption) dynamic.Interface {
	config := &clientConfig{}
	for _, opt := range opts {
		opt(config)
	}

	// Parse CRD bytes if provided
	var allCRDs []*apiextensionsv1.CustomResourceDefinition
	for _, crdBytes := range config.crdBytes {
		crds, err := parseCRDsFromBytes(crdBytes)
		if err != nil {
			// panic, since this is used only in tests
			panic("failed to parse CRDs from byte data: " + err.Error())
		}
		allCRDs = append(allCRDs, crds...)
	}
	allCRDs = append(allCRDs, config.crds...)

	// Build GVR to ListKind mappings
	autoDetectedMappings, err := buildGVRToListKindMapping(scheme)
	if err != nil {
		// panic, since this is used only in tests
		panic("failed to build GVR to ListKind mapping: " + err.Error())
	}

	// Merge custom mappings (custom ones take precedence)
	finalMappings := make(map[schema.GroupVersionResource]string)
	for gvr, listKind := range autoDetectedMappings {
		finalMappings[gvr] = listKind
	}
	for gvr, listKind := range config.gvrMappings {
		finalMappings[gvr] = listKind
	}

	// Create client with appropriate spec
	if config.openAPISpec != "" {
		return newFakeClientWithSpec(scheme, finalMappings, config.openAPISpec, allCRDs, config.objects...)
	}
	return newFakeClient(scheme, finalMappings, allCRDs, config.objects...)
}

// NewFakeDynamicClient creates a fake dynamic client with apply support.
// The API mirrors the upstream dynamic fake client exactly.
func NewFakeDynamicClient(scheme *runtime.Scheme, objects ...runtime.Object) dynamic.Interface {
	return NewClient(scheme, WithObjects(objects...))
}

// NewFakeDynamicClientWithCustomListKinds creates a fake dynamic client with custom
// GVR to ListKind mappings, mirroring the upstream API exactly.
// This allows you to override or supplement the auto-detected mappings.
func NewFakeDynamicClientWithCustomListKinds(scheme *runtime.Scheme, gvrToListKind map[schema.GroupVersionResource]string, objects ...runtime.Object) dynamic.Interface {
	return NewClient(scheme,
		WithCustomGVRMappings(gvrToListKind),
		WithObjects(objects...),
	)
}

// buildGVRToListKindMapping automatically builds GVR to ListKind mappings from the scheme.
// This function uses the DefaultRESTMapper to ensure proper Kind-to-Resource conversion
// following Kubernetes conventions, rather than relying on simple string manipulation.
func buildGVRToListKindMapping(scheme *runtime.Scheme) (map[schema.GroupVersionResource]string, error) {
	gvrToListKind := make(map[schema.GroupVersionResource]string)

	// Create a DefaultRESTMapper for proper GVK to GVR conversion
	// TODO: if we find a need, this could be exposed to allow custom mappings
	restMapper := testrestmapper.TestOnlyStaticRESTMapper(scheme)

	// Get all known types from the scheme
	allKnownTypes := scheme.AllKnownTypes()

	// Process list types and build GVR mappings
	for gvk := range allKnownTypes {
		if !strings.HasSuffix(gvk.Kind, "List") {
			continue
		}

		// Add the singular kind, just in case a list type is registered
		// without a corresponding singular kind
		singularKind := strings.TrimSuffix(gvk.Kind, "List")
		singularGVK := schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    singularKind,
		}
		if !scheme.Recognizes(singularGVK) {
			continue
		}

		// Get the resource mapping from the REST mapper
		mapping, err := restMapper.RESTMapping(singularGVK.GroupKind(), singularGVK.Version)
		if err != nil {
			return nil, err
		}

		gvrToListKind[mapping.Resource] = gvk.Kind
	}

	return gvrToListKind, nil
}

// createDynamicScheme creates a scheme with only unstructured types for dynamic clients
func createDynamicScheme(originalScheme *runtime.Scheme) *runtime.Scheme {
	dynamicScheme := runtime.NewScheme()

	// Register only resource types as unstructured, avoiding metav1 conflicts
	for gvk := range originalScheme.AllKnownTypes() {
		// Skip metav1 built-in types completely to avoid conflicts
		if gvk.Group == "" && gvk.Version == "v1" &&
			(strings.HasSuffix(gvk.Kind, "Options") ||
				gvk.Kind == "Status" ||
				gvk.Kind == "WatchEvent" ||
				gvk.Kind == "InternalEvent" ||
				strings.Contains(gvk.Kind, "List") && !strings.HasSuffix(gvk.Kind, "List")) {
			continue
		}

		// Check if the type is already registered to avoid conflicts
		if dynamicScheme.Recognizes(gvk) {
			continue
		}

		// Register resource types as unstructured
		if strings.HasSuffix(gvk.Kind, "List") {
			dynamicScheme.AddKnownTypeWithName(gvk, &unstructured.UnstructuredList{})
		} else {
			dynamicScheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		}
	}

	return dynamicScheme
}

// fakeDynamicClient wraps an upstream fake client but overrides Apply to properly handle PatchOptions
type fakeDynamicClient struct {
	*dynamicfake.FakeDynamicClient
	fieldManagedTracker clientgotesting.ObjectTracker
}

// Resource returns a namespaceableResourceInterface for the given resource
func (c *fakeDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &fakeDynamicResourceClient{
		client:              c,
		resource:            resource,
		fieldManagedTracker: c.fieldManagedTracker,
	}
}

// fakeDynamicResourceClient wraps the upstream resource client to properly handle Apply operations
type fakeDynamicResourceClient struct {
	client              *fakeDynamicClient
	resource            schema.GroupVersionResource
	namespace           string
	fieldManagedTracker clientgotesting.ObjectTracker
}

// Namespace returns a resource interface for the given namespace
func (c *fakeDynamicResourceClient) Namespace(ns string) dynamic.ResourceInterface {
	return &fakeDynamicResourceClient{
		client:              c.client,
		resource:            c.resource,
		namespace:           ns,
		fieldManagedTracker: c.fieldManagedTracker,
	}
}

// Apply performs server-side apply with proper PatchOptions handling
func (c *fakeDynamicResourceClient) Apply(_ context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions, _ ...string) (*unstructured.Unstructured, error) {
	// Convert the apply options to patch options to preserve field manager
	patchOptions := metav1.PatchOptions{
		FieldManager: options.FieldManager,
		Force:        &options.Force,
		DryRun:       options.DryRun,
	}

	// Get the original object from upstream tracker to check if it exists
	upstreamTracker := c.client.Tracker()
	originalObj, getErr := upstreamTracker.Get(c.resource, c.namespace, name)

	// If object exists in upstream but not in field managed tracker, add it
	if getErr == nil {
		// Check if object exists in field managed tracker
		_, fmtGetErr := c.fieldManagedTracker.Get(c.resource, c.namespace, name)
		if fmtGetErr != nil {
			// Object doesn't exist in field managed tracker, create it
			if createErr := c.fieldManagedTracker.Create(c.resource, originalObj, c.namespace); createErr != nil {
				return nil, createErr
			}
		}
	}

	// Apply using the field managed tracker with proper PatchOptions
	err := c.fieldManagedTracker.Apply(c.resource, obj, c.namespace, patchOptions)
	if err != nil {
		return nil, err
	}

	// Get the result back from field managed tracker
	result, err := c.fieldManagedTracker.Get(c.resource, c.namespace, name)
	if err != nil {
		return nil, err
	}

	// Ensure result is unstructured for consistency
	var finalResult *unstructured.Unstructured
	if unstructuredResult, ok := result.(*unstructured.Unstructured); ok {
		finalResult = unstructuredResult
	} else {
		// Convert typed object to unstructured
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(result)
		if err != nil {
			return nil, err
		}
		finalResult = &unstructured.Unstructured{Object: unstructuredObj}
	}

	// Sync with upstream client storage so Get operations work
	// Always try update first since the object should exist
	updateErr := upstreamTracker.Update(c.resource, finalResult, c.namespace)
	if updateErr != nil {
		// If update fails, try create (object might not exist in upstream tracker)
		createErr := upstreamTracker.Create(c.resource, finalResult, c.namespace)
		if createErr != nil {
			// Both operations failed, return the update error as it's more informative
			return nil, updateErr
		}
	}

	return finalResult, nil
}

// ApplyStatus applies the status subresource
func (c *fakeDynamicResourceClient) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return c.Apply(ctx, name, obj, options, "status")
}

// All other methods delegate to the upstream client
func (c *fakeDynamicResourceClient) Create(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Create(ctx, obj, options, subresources...)
}

func (c *fakeDynamicResourceClient) Update(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Update(ctx, obj, options, subresources...)
}

func (c *fakeDynamicResourceClient) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).UpdateStatus(ctx, obj, options)
}

func (c *fakeDynamicResourceClient) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Delete(ctx, name, options, subresources...)
}

func (c *fakeDynamicResourceClient) DeleteCollection(ctx context.Context, options metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).DeleteCollection(ctx, options, listOptions)
}

func (c *fakeDynamicResourceClient) Get(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Get(ctx, name, options, subresources...)
}

func (c *fakeDynamicResourceClient) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).List(ctx, opts)
}

func (c *fakeDynamicResourceClient) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Watch(ctx, opts)
}

func (c *fakeDynamicResourceClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	// Handle ApplyPatchType by redirecting to Apply method which has proper SSA conflict handling
	if pt == types.ApplyPatchType {
		// Convert patch data to unstructured object
		var obj unstructured.Unstructured
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil, fmt.Errorf("failed to unmarshal apply patch data: %w", err)
		}

		// Convert PatchOptions to ApplyOptions
		applyOptions := metav1.ApplyOptions{
			FieldManager: options.FieldManager,
			DryRun:       options.DryRun,
		}
		if options.Force != nil {
			applyOptions.Force = *options.Force
		}

		// Use Apply method for proper SSA handling
		return c.Apply(ctx, name, &obj, applyOptions, subresources...)
	}

	// For other patch types, delegate to upstream client
	return c.client.FakeDynamicClient.Resource(c.resource).Namespace(c.namespace).Patch(ctx, name, pt, data, options, subresources...)
}

// createHybridTypeConverter creates a type converter that uses OpenAPI schema for built-in types
// and falls back to deduced type converter for custom types
func createHybridTypeConverter(specPath string, crds []*apiextensionsv1.CustomResourceDefinition) managedfields.TypeConverter {
	// Try to create OpenAPI-based type converter with CRD support
	openapiConverter, err := createTypeConverterWithOpenAPISpec(specPath, crds)
	if err != nil {
		if specPath != "" {
			// If user provided a custom spec path and it failed, panic since this is used only in tests
			panic("failed to create type converter with custom OpenAPI spec: " + err.Error())
		}
		// If default embedded spec fails, use deduced type converter for everything
		return managedfields.NewDeducedTypeConverter()
	}

	// Create a hybrid converter that prefers OpenAPI but falls back to deduced
	return &hybridTypeConverter{
		openapiConverter: openapiConverter,
		deducedConverter: managedfields.NewDeducedTypeConverter(),
	}
}

// createTypeConverterWithOpenAPISchema creates a type converter using the Kubernetes OpenAPI schema
// and optionally includes schemas generated from provided CRDs
func createTypeConverterWithOpenAPISpec(specPath string, crds []*apiextensionsv1.CustomResourceDefinition) (managedfields.TypeConverter, error) {
	var schemaBytes []byte
	var err error

	if specPath == "" {
		// Use embedded default OpenAPI schema
		schemaBytes = defaultKubernetesOpenAPISpec
	} else {
		// Load the Kubernetes OpenAPI schema from specified file
		schemaBytes, err = os.ReadFile(specPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read OpenAPI schema file %q: %w", specPath, err)
		}
	}

	// Parse the OpenAPI schema
	var swaggerDoc map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &swaggerDoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAPI schema: %w", err)
	}

	// Extract definitions
	definitions, ok := swaggerDoc["definitions"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("OpenAPI schema missing definitions")
	}

	// Convert to spec.Schema format
	openapiSpec := make(map[string]*spec.Schema)
	for name, def := range definitions {
		defBytes, err := json.Marshal(def)
		if err != nil {
			continue
		}

		var schema spec.Schema
		if err := json.Unmarshal(defBytes, &schema); err != nil {
			continue
		}

		openapiSpec[name] = &schema
	}

	// Add CRD schemas if provided
	if len(crds) > 0 {
		crdSchemas, err := generateCRDSchemas(crds)
		if err != nil {
			return nil, fmt.Errorf("failed to generate CRD schemas: %w", err)
		}

		// Merge CRD schemas with the base OpenAPI schema
		for name, schema := range crdSchemas {
			openapiSpec[name] = schema
		}
	}

	// Create type converter with the OpenAPI schema
	return managedfields.NewTypeConverter(openapiSpec, false)
}

// newFakeClient creates a dynamic.Interface with apply support configured.
func newFakeClient(scheme *runtime.Scheme, gvrToListKind map[schema.GroupVersionResource]string, crds []*apiextensionsv1.CustomResourceDefinition, objects ...runtime.Object) dynamic.Interface {
	return newFakeClientWithSpec(scheme, gvrToListKind, "", crds, objects...)
}

// newFakeClientWithSpec creates a dynamic.Interface with apply support configured using a custom OpenAPI spec.
func newFakeClientWithSpec(scheme *runtime.Scheme, gvrToListKind map[schema.GroupVersionResource]string, specPath string, crds []*apiextensionsv1.CustomResourceDefinition, objects ...runtime.Object) dynamic.Interface {
	// Register CRD types with the scheme if provided
	if len(crds) > 0 {
		registerCRDTypesWithScheme(scheme, crds, gvrToListKind)
	}

	// Create a dynamic scheme with only unstructured types to prevent type conversion issues
	dynamicScheme := createDynamicScheme(scheme)

	// FieldManagedObjectTracker is used to track field-managed objects for server-side apply.
	// Use the original scheme for proper type handling
	codecFactory := serializer.NewCodecFactory(scheme)
	decoder := codecFactory.UniversalDecoder()

	// Create type converter with Kubernetes OpenAPI schema for strategic merge patch support
	// Fall back to deduced type converter if OpenAPI schema fails or for unsupported types
	typeConverter := createHybridTypeConverter(specPath, crds)

	// Create a hybrid field managed tracker that can handle both OpenAPI schema types
	// and custom types not in the schema
	fieldManagedTracker := clientgotesting.NewFieldManagedObjectTracker(scheme, decoder, typeConverter)

	// Create upstream dynamic client with dynamic scheme
	upstreamClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(dynamicScheme, gvrToListKind, objects...)

	// Add reactor to handle raw Apply Patch operations that bypass our Apply method
	upstreamClient.PrependReactor("patch", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		patchAction, ok := action.(clientgotesting.PatchActionImpl)
		if !ok {
			return false, nil, nil
		}

		// Only handle ApplyPatchType
		if patchAction.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}

		// Convert patch data to unstructured apply configuration
		unstructuredApply := &unstructured.Unstructured{}
		if err := unstructuredApply.UnmarshalJSON(patchAction.GetPatch()); err != nil {
			return true, nil, err
		}

		// Get the original object from upstream tracker to check if it exists
		upstreamTracker := upstreamClient.Tracker()
		originalObj, getErr := upstreamTracker.Get(
			patchAction.GetResource(),
			patchAction.GetNamespace(),
			patchAction.GetName(),
		)

		// If object exists in upstream but not in field managed tracker, add it
		if getErr == nil {
			// Check if object exists in field managed tracker
			_, fmtGetErr := fieldManagedTracker.Get(
				patchAction.GetResource(),
				patchAction.GetNamespace(),
				patchAction.GetName(),
			)
			if fmtGetErr != nil {
				// Object doesn't exist in field managed tracker, create it
				if createErr := fieldManagedTracker.Create(patchAction.GetResource(), originalObj, patchAction.GetNamespace()); createErr != nil {
					return true, nil, createErr
				}
			}
		}

		// Apply using field managed tracker
		err = fieldManagedTracker.Apply(
			patchAction.GetResource(),
			unstructuredApply,
			patchAction.GetNamespace(),
			patchAction.PatchOptions,
		)
		if err != nil {
			return true, nil, err
		}

		// Get the result back from field managed tracker
		result, err := fieldManagedTracker.Get(
			patchAction.GetResource(),
			patchAction.GetNamespace(),
			patchAction.GetName(),
		)
		if err != nil {
			return true, nil, err
		}

		// Ensure result is unstructured for consistency
		var finalResult *unstructured.Unstructured
		if unstructuredResult, ok := result.(*unstructured.Unstructured); ok {
			finalResult = unstructuredResult
		} else {
			// Convert typed object to unstructured
			unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(result)
			if err != nil {
				return true, nil, err
			}
			finalResult = &unstructured.Unstructured{Object: unstructuredObj}
		}

		// Sync with upstream client storage so Get operations work
		updateErr := upstreamTracker.Update(patchAction.GetResource(), finalResult, patchAction.GetNamespace())
		if updateErr != nil {
			// If update fails, try create (object might not exist in upstream tracker)
			createErr := upstreamTracker.Create(patchAction.GetResource(), finalResult, patchAction.GetNamespace())
			if createErr != nil {
				// Both operations failed, return the update error as it's more informative
				return true, nil, updateErr
			}
		}

		return true, finalResult, nil
	})

	// Create our wrapper that properly handles Apply operations
	return &fakeDynamicClient{
		FakeDynamicClient:   upstreamClient,
		fieldManagedTracker: fieldManagedTracker,
	}
}

// hybridTypeConverter combines OpenAPI schema converter with deduced converter
type hybridTypeConverter struct {
	openapiConverter managedfields.TypeConverter
	deducedConverter managedfields.TypeConverter
}

// ObjectToTyped converts an object to a typed value, trying OpenAPI first, then deduced
func (h *hybridTypeConverter) ObjectToTyped(obj runtime.Object, opts ...typed.ValidationOptions) (*typed.TypedValue, error) {
	// Try OpenAPI converter first
	if typedVal, err := h.openapiConverter.ObjectToTyped(obj, opts...); err == nil {
		return typedVal, nil
	}

	// Fall back to deduced converter for custom types
	return h.deducedConverter.ObjectToTyped(obj, opts...)
}

// TypedToObject converts a typed value back to an object
func (h *hybridTypeConverter) TypedToObject(typedVal *typed.TypedValue) (runtime.Object, error) {
	// The typed value knows which converter created it, but we need to try both
	if obj, err := h.openapiConverter.TypedToObject(typedVal); err == nil {
		return obj, nil
	}

	// Fall back to deduced converter
	return h.deducedConverter.TypedToObject(typedVal)
}

// generateCRDSchemas generates OpenAPI schemas from CRD specifications
func generateCRDSchemas(crds []*apiextensionsv1.CustomResourceDefinition) (map[string]*spec.Schema, error) {
	schemas := make(map[string]*spec.Schema)

	for _, crd := range crds {
		for _, version := range crd.Spec.Versions {
			if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
				continue
			}

			// Convert CRD OpenAPI schema to our format
			schema, err := convertCRDSchemaToSpec(version.Schema.OpenAPIV3Schema, crd.Spec.Group, version.Name, crd.Spec.Names.Kind)
			if err != nil {
				return nil, fmt.Errorf("failed to convert CRD %s schema: %w", crd.Name, err)
			}

			// Use a simple schema name that follows Kubernetes patterns
			// The actual GVK mapping is handled by the x-kubernetes-group-version-kind extension
			schemaName := fmt.Sprintf("%s.%s.%s",
				crd.Spec.Group,
				version.Name,
				crd.Spec.Names.Kind)

			schemas[schemaName] = schema
		}
	}

	return schemas, nil
}

// convertCRDSchemaToSpec converts a CRD JSONSchemaProps to an OpenAPI spec.Schema
func convertCRDSchemaToSpec(crdSchema *apiextensionsv1.JSONSchemaProps, group, version, kind string) (*spec.Schema, error) {
	// Marshal the CRD schema to JSON
	schemaBytes, err := json.Marshal(crdSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CRD schema: %w", err)
	}

	// Unmarshal into OpenAPI spec.Schema
	var schema spec.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into spec.Schema: %w", err)
	}

	// Add the crucial x-kubernetes-group-version-kind extension
	// This is what the TypeConverter uses to map objects to schemas
	if schema.Extensions == nil {
		schema.Extensions = make(map[string]interface{})
	}
	schema.Extensions["x-kubernetes-group-version-kind"] = []interface{}{
		map[string]interface{}{
			"group":   group,
			"version": version,
			"kind":    kind,
		},
	}

	// Process strategic merge patch annotations if present
	processStrategicMergePatchAnnotations(&schema, crdSchema)

	return &schema, nil
}

// processStrategicMergePatchAnnotations processes x-kubernetes-* annotations for strategic merge patch
func processStrategicMergePatchAnnotations(schema *spec.Schema, crdSchema *apiextensionsv1.JSONSchemaProps) {
	// Copy strategic merge patch annotations from CRD schema
	if crdSchema.XListType != nil {
		if schema.Extensions == nil {
			schema.Extensions = make(map[string]interface{})
		}

		listType := *crdSchema.XListType
		if listType == "map" {
			schema.Extensions["x-kubernetes-patch-strategy"] = "merge"
			if len(crdSchema.XListMapKeys) > 0 {
				// Use the first map key as the merge key
				schema.Extensions["x-kubernetes-patch-merge-key"] = crdSchema.XListMapKeys[0]
			}
		}
	}

	// Recursively process nested properties
	if schema.Properties != nil {
		for propName, propSchema := range schema.Properties {
			if crdSchema.Properties != nil {
				if crdProp, exists := crdSchema.Properties[propName]; exists {
					processStrategicMergePatchAnnotations(&propSchema, &crdProp)
					schema.Properties[propName] = propSchema
				}
			}
		}
	}

	// Process array items
	if schema.Items != nil && schema.Items.Schema != nil && crdSchema.Items != nil && crdSchema.Items.Schema != nil {
		processStrategicMergePatchAnnotations(schema.Items.Schema, crdSchema.Items.Schema)
	}
}

// parseCRDsFromBytes parses CRDs from byte data that may contain multiple YAML documents
func parseCRDsFromBytes(data []byte) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	// Split by YAML document separator
	documents := strings.Split(string(data), "---")
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(documents))

	for _, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		// Parse each document as a CRD
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal([]byte(doc), &crd); err != nil {
			return nil, fmt.Errorf("failed to unmarshal CRD document: %w", err)
		}

		// Validate that this is actually a CRD
		if crd.Kind != "CustomResourceDefinition" ||
			(crd.APIVersion != "apiextensions.k8s.io/v1" && crd.APIVersion != "apiextensions.k8s.io/v1beta1") {
			continue // Skip non-CRD documents
		}

		crds = append(crds, &crd)
	}

	return crds, nil
}

// registerCRDTypesWithScheme registers CRD types and list kinds with the scheme
func registerCRDTypesWithScheme(scheme *runtime.Scheme, crds []*apiextensionsv1.CustomResourceDefinition, gvrToListKind map[schema.GroupVersionResource]string) {
	for _, crd := range crds {
		for _, version := range crd.Spec.Versions {
			gvk := schema.GroupVersionKind{
				Group:   crd.Spec.Group,
				Version: version.Name,
				Kind:    crd.Spec.Names.Kind,
			}

			listGVK := schema.GroupVersionKind{
				Group:   crd.Spec.Group,
				Version: version.Name,
				Kind:    crd.Spec.Names.Kind + "List",
			}

			// Register the types as unstructured for dynamic client compatibility
			// Check if the type is already registered to avoid conflicts with consuming
			// applications that may have pre-registered the same GVK with typed structs.
			// This prevents "Double registration of different types" panics when the same
			// GVK is registered both as a typed struct and as unstructured.Unstructured.
			if !scheme.Recognizes(gvk) {
				scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
			}
			if !scheme.Recognizes(listGVK) {
				scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
			}

			// Add to GVR to ListKind mapping
			gvr := schema.GroupVersionResource{
				Group:    crd.Spec.Group,
				Version:  version.Name,
				Resource: crd.Spec.Names.Plural,
			}
			gvrToListKind[gvr] = crd.Spec.Names.Kind + "List"
		}
	}
}
