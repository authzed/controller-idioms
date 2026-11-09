package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/authzed/controller-idioms/component"
	"github.com/authzed/controller-idioms/metaapply"
	"github.com/authzed/controller-idioms/typed"
)

// ApplyAddFunc adds a finalizer to obj.
type ApplyAddFunc[K component.KubeObject] func(ctx context.Context, obj K) (K, error)

// FallbackFunc is called when Remove uses a JSON patch because other field
// managers also own the finalizer. owners are the names of those managers.
type FallbackFunc[K component.KubeObject] func(ctx context.Context, obj K, owners []string)

// Finalizer adds and removes one finalizer with server-side apply. Its field
// manager is "finalizer:" followed by the finalizer name, so each finalizer has
// its own manager.
type Finalizer[K component.KubeObject] struct {
	client     dynamic.Interface
	gvr        schema.GroupVersionResource
	name       string
	meta       *metaapply.Applier
	onFallback FallbackFunc[K]
}

// Option configures a Finalizer.
type Option[K component.KubeObject] func(*Finalizer[K])

// WithFallbackFunc sets a function that Remove calls each time it uses the
// JSON patch. Use it to count finalizers that are still owned by an Update or
// by a different controller.
func WithFallbackFunc[K component.KubeObject](fn FallbackFunc[K]) Option[K] {
	return func(f *Finalizer[K]) { f.onFallback = fn }
}

// New returns a Finalizer for objects of the resource gvr, whose kind is gvk.
func New[K component.KubeObject](client dynamic.Interface, gvr schema.GroupVersionResource, gvk schema.GroupVersionKind, name string, opts ...Option[K]) *Finalizer[K] {
	f := &Finalizer[K]{
		client: client,
		gvr:    gvr,
		name:   name,
		meta:   metaapply.New(client, gvr, gvk, "finalizer:"+name),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Name returns the finalizer name.
func (f *Finalizer[K]) Name() string {
	return f.name
}

// FieldManager returns the field manager name of the Finalizer.
func (f *Finalizer[K]) FieldManager() string {
	return f.meta.FieldManager()
}

// Add adds the finalizer to obj. It does nothing if obj already has the
// finalizer or is being deleted. If the object is gone, Add returns NotFound.
func (f *Finalizer[K]) Add(ctx context.Context, obj K) (K, error) {
	if slices.Contains(obj.GetFinalizers(), f.name) || obj.GetDeletionTimestamp() != nil {
		return obj, nil
	}
	return f.applyFinalizers(ctx, obj, []string{f.name})
}

// Remove removes the finalizer from obj if obj is being deleted.
//
// Remove first applies an empty list of finalizers. This releases the
// finalizer if Add added it. An apply cannot remove a finalizer that a
// different manager also owns, for example one added with an Update by
// GetAddFunc or by a different controller. If the finalizer is still present,
// Remove logs the other owners, calls the FallbackFunc, and removes the
// finalizer with a JSON patch. The patch tests the uid and the position of the
// finalizer before it removes the entry.
func (f *Finalizer[K]) Remove(ctx context.Context, obj K) (K, error) {
	var nilObj K
	if !slices.Contains(obj.GetFinalizers(), f.name) || obj.GetDeletionTimestamp() == nil {
		return obj, nil
	}
	applied, err := f.applyFinalizers(ctx, obj, []string{})
	if err != nil {
		return nilObj, err
	}
	idx := slices.Index(applied.GetFinalizers(), f.name)
	if idx == -1 {
		return applied, nil
	}

	owners := finalizerOwners(applied.GetManagedFields(), f.name)
	logr.FromContextOrDiscard(ctx).V(2).Info("removing finalizer with a JSON patch because other field managers own it",
		"finalizer", f.name, "object", types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, "owners", owners)
	if f.onFallback != nil {
		f.onFallback(ctx, obj, owners)
	}

	patch, err := removeFinalizerPatch(obj.GetUID(), idx, f.name)
	if err != nil {
		return nilObj, err
	}
	u, err := f.client.Resource(f.gvr).Namespace(obj.GetNamespace()).Patch(ctx, obj.GetName(), types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: f.FieldManager()})
	if err != nil {
		return nilObj, err
	}
	return typed.UnstructuredObjToTypedObj[K](u)
}

func (f *Finalizer[K]) applyFinalizers(ctx context.Context, obj K, finalizers []string) (K, error) {
	var nilObj K
	u, err := f.meta.Apply(ctx, obj, func(body *unstructured.Unstructured) {
		// An empty list releases the finalizer. A nil list would omit the field.
		body.SetFinalizers(finalizers)
	})
	if err != nil {
		return nilObj, err
	}
	return typed.UnstructuredObjToTypedObj[K](u)
}

// finalizerOwners returns the field managers that own finalizer.
func finalizerOwners(entries []metav1.ManagedFieldsEntry, finalizer string) []string {
	quoted, err := json.Marshal(finalizer)
	if err != nil {
		return nil
	}
	key := "v:" + string(quoted)
	var owners []string
	for _, entry := range entries {
		if entry.FieldsV1 == nil {
			continue
		}
		var fields struct {
			Metadata struct {
				Finalizers map[string]json.RawMessage `json:"f:finalizers"`
			} `json:"f:metadata"`
		}
		if err := json.Unmarshal(entry.FieldsV1.GetRawBytes(), &fields); err != nil {
			continue
		}
		if _, ok := fields.Metadata.Finalizers[key]; ok {
			owners = append(owners, entry.Manager)
		}
	}
	return owners
}

// removeFinalizerPatch returns a JSON patch that removes the entry at idx. The
// patch fails if the uid changed or if the entry at idx is not finalizer.
func removeFinalizerPatch(uid types.UID, idx int, finalizer string) ([]byte, error) {
	path := fmt.Sprintf("/metadata/finalizers/%d", idx)
	return json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": uid},
		{"op": "test", "path": path, "value": finalizer},
		{"op": "remove", "path": path},
	})
}
