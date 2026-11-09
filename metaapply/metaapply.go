// Package metaapply changes the metadata of an existing object with a
// server-side apply.
//
// An Applier writes only metadata, and it includes the uid of the object in
// each apply. Thus an apply cannot create an object, and it cannot change a
// different object with the same name. An Applier also does not write the
// complete object, so it cannot drop fields that a Go type does not model.
//
// Each Applier has its own field manager, which ManagerFor makes from a
// purpose. An apply sets the complete set of fields that its manager owns, so
// a manager that also owns spec fields would lose them. Apply refuses to write
// if the manager owns fields outside metadata.
package metaapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// maxManagerLength is the longest field manager name that the apiserver
// accepts.
var maxManagerLength = metav1validation.FieldManagerMaxLength

// ErrOwnsNonMetadata is returned when the field manager of an Applier owns
// fields outside metadata.
var ErrOwnsNonMetadata = errors.New("field manager owns fields outside metadata")

// ManagerFor returns the field manager name for purpose. A purpose that is too
// long is shortened and given a hash suffix, so that different purposes give
// different names.
func ManagerFor(purpose string) string {
	if len(purpose) <= maxManagerLength {
		return purpose
	}
	sum := sha256.Sum256([]byte(purpose))
	suffix := "-" + hex.EncodeToString(sum[:])[:16]
	return purpose[:maxManagerLength-len(suffix)] + suffix
}

// Applier applies metadata to objects of one resource.
type Applier struct {
	client  dynamic.Interface
	gvr     schema.GroupVersionResource
	gvk     schema.GroupVersionKind
	manager string
}

// New returns an Applier for the resource gvr, whose kind is gvk. Its field
// manager is ManagerFor(purpose). Use a different purpose for each set of
// metadata fields.
func New(client dynamic.Interface, gvr schema.GroupVersionResource, gvk schema.GroupVersionKind, purpose string) *Applier {
	return &Applier{client: client, gvr: gvr, gvk: gvk, manager: ManagerFor(purpose)}
}

// FieldManager returns the field manager name of the Applier.
func (a *Applier) FieldManager() string {
	return a.manager
}

// Apply applies the metadata that set writes. The body that set receives
// already has the kind, name, namespace, and uid of obj. Do not change them.
//
// If the object is gone, Apply returns NotFound. If the managed fields of obj
// show that the manager owns fields outside metadata, Apply returns
// ErrOwnsNonMetadata and writes nothing. If obj has no managed fields, Apply
// cannot do this check.
func (a *Applier) Apply(ctx context.Context, obj metav1.Object, set func(body *unstructured.Unstructured)) (*unstructured.Unstructured, error) {
	if err := checkOwnsOnlyMetadata(obj.GetManagedFields(), a.manager); err != nil {
		return nil, err
	}

	body := &unstructured.Unstructured{}
	body.SetGroupVersionKind(a.gvk)
	body.SetName(obj.GetName())
	body.SetNamespace(obj.GetNamespace())
	body.SetUID(obj.GetUID())
	set(body)

	u, err := a.client.Resource(a.gvr).Namespace(obj.GetNamespace()).Apply(ctx, obj.GetName(), body, metav1.ApplyOptions{FieldManager: a.manager, Force: true})
	// A forced apply of metadata has no field conflicts. A conflict here means
	// that the uid does not match because the object does not exist.
	if k8serrors.IsConflict(err) {
		return nil, k8serrors.NewNotFound(a.gvr.GroupResource(), obj.GetName())
	}
	return u, err
}

func checkOwnsOnlyMetadata(entries []metav1.ManagedFieldsEntry, manager string) error {
	for _, entry := range entries {
		if entry.Manager != manager || entry.Operation != metav1.ManagedFieldsOperationApply || entry.FieldsV1 == nil {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entry.FieldsV1.GetRawBytes(), &fields); err != nil {
			return fmt.Errorf("read managed fields of %s: %w", manager, err)
		}
		for key := range fields {
			if key != "f:metadata" && key != "." {
				return fmt.Errorf("%w: %s owns %s", ErrOwnsNonMetadata, manager, key)
			}
		}
	}
	return nil
}
