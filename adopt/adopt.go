// Package adopt implements a generic adoption handler for use in controllers.
//
// Adoption is used when the controller needs to make use of some existing
// resource in a cluster, generally when a reference to an object is placed in
// an owned object's spec (i.e. a reference to a secret or a configmap).
//
// Adoption happens in two phases:
//
//  1. Labelling - the resource is labelled as managed by the controller. This
//     is primarily so that the controller an open a label-filtered watch against
//     the cluster instead of watching all objects, which keeps the cache small
//     and avoids unwanted data (i.e. watching all secrets).
//  2. Owner Annotation - the resource is annotated as owned by one or more
//     objects. The annotation is done using a unique field manager per owner,
//     which allows server-side-apply to reconcile the owner annotations without
//     providing the full list of owners every time.
//
// There are additional utilities for cleaning up old ownership labels and
// annotations and for constructing or consuming index and cache keys for
// adopted objects.
package adopt

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"

	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/typed"
	"github.com/authzed/controller-idioms/typedctx"
)

// TODO: a variant where there can only be one owner (label only, fail if labelled for someone else)
// TODO: a variant where a real client is used to check for existence before applying

// Annotator is any type that can have annotations added to it. All standard
// applyconfiguration packages from client-go implement this type. Custom types
// should implement it themselves.
type Annotator[T any] interface {
	WithAnnotations(entries map[string]string) T
}

// Labeler is any type that can have labels added to it. All standard
// applyconfiguration packages from client-go implement this type. Custom types
// should implement it themselves.
type Labeler[T any] interface {
	WithLabels(entries map[string]string) T
}

// Adoptable is any type that can be labelled and annotated.
// Labels are used for including in a watch stream, and annotations are used
// to indicated ownership by a specific object.
type Adoptable[T any] interface {
	Annotator[T]
	Labeler[T]
}

// Object is satisfied by any standard kube object.
type Object interface {
	comparable
	runtime.Object
	metav1.Object
}

// ApplyFunc should apply a patch defined by the object A with server-side apply
// and should return the base type. For example a SecretApplyConfiguration would
// be applied with a client-go Patch call, and return a Secret.
type ApplyFunc[K Object, A Adoptable[A]] func(ctx context.Context, object A, opts metav1.ApplyOptions) (result K, err error)

// ExistsFunc should return nil if the object exists in the cluster, and an
// error otherwise.
type ExistsFunc func(ctx context.Context, nn types.NamespacedName) error

// IndexKeyFunc returns the name of an index to use and the value to query it for.
type IndexKeyFunc func(ctx context.Context) (indexName string, indexValue string)

// Owned is used in object annotations to indicate an object is managed.
const Owned = "owned"

var (
	// AlwaysExistsFunc is an ExistsFunc that always returns nil
	AlwaysExistsFunc ExistsFunc = func(_ context.Context, _ types.NamespacedName) error {
		return nil
	}
	// NoopObjectMissingFunc is an ObjectMissing func that does nothing
	NoopObjectMissingFunc = func(_ context.Context, _ error) {}
)

// AdoptionHandler implements handler.Handler to "adopt" an existing resource
// under the controller's management. See the package description for more info.
type AdoptionHandler[K Object, A Adoptable[A]] struct {
	// OperationsContext allows the adoption handler to control the sync loop
	// it's called from to deal with transient errors.
	queue.OperationsContext

	// ControllerFieldManager is the value to use when adopting the object
	// for visibility by the controller
	// If adoption is the only thing the controller needs to do with this object,
	// then it's fine to set this value to the fieldmanager used for "owned"
	// types. But a new value should be used if the controller needs to modify
	// the adopted objects in other ways, so that server-side-apply doesn't
	// revert the changes performed elsewhere in the controller.
	ControllerFieldManager string

	// AdopteeCtx tells the handler how to fetch the adoptee from context
	AdopteeCtx typedctx.MustValueContext[types.NamespacedName]

	// OwnerCtx tells the handler how to fetch the owner from context
	OwnerCtx typedctx.MustValueContext[types.NamespacedName]

	// AdoptedCtx will store the object after it has been adopted
	AdoptedCtx typedctx.SettableContext[K]

	// ObjectAdoptedFunc is called when an adoption was performed
	ObjectAdoptedFunc func(ctx context.Context, obj K)

	// ObjectMissingFunc is called when the object cannot be found
	ObjectMissingFunc func(ctx context.Context, err error)

	// TODO: GetFromCache and Indexer could be replaced with an informerfactory
	//  that can be used to get both

	// GetFromCache is where we expect to find the object if it is being watched
	// This will usually be a wrapper around an informer cache `Get`.
	GetFromCache func(ctx context.Context) (K, error)

	// Indexer is the index we expect to find adopted objects
	// The absence of the object from this indexer triggers adoption.
	Indexer *typed.Indexer[K]

	// IndexName is the name of the index to look for owned objects
	IndexName string

	// Labels to add if the object is not found in the index
	// Note that this must include a label that matches the index, otherwise
	// the controller will never stop attempting to add labels.
	Labels map[string]string

	// NewPatch returns an empty object satisfying Adoptable
	// This is typically an apply configuration, like `applycorev1.Secret(name, namespace)`
	NewPatch func(types.NamespacedName) A

	// OwnerAnnotationPrefix is a common prefix for all owner annotations
	OwnerAnnotationPrefix string

	// OwnerAnnotationKeyFunc generates an ownership annotation key for a given owner
	OwnerAnnotationKeyFunc func(owner types.NamespacedName) string

	// OwnerFieldManagerFunc generates a field manager name for a given owner
	OwnerFieldManagerFunc func(owner types.NamespacedName) string

	// ApplyFunc applies adoption-related changes to the object to the cluster
	ApplyFunc ApplyFunc[K, A]

	// ExistsFunc checks if the object to be adopted exists in the cluster
	ExistsFunc ExistsFunc

	// Next is the next handler in the chain (use NoopHandler if not chaining)
	Next handler.ContextHandler
}

func (s *AdoptionHandler[K, A]) Handle(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)
	adoptee := s.AdopteeCtx.MustValue(ctx)
	owner := s.OwnerCtx.MustValue(ctx)

	if s.ExistsFunc == nil {
		s.ExistsFunc = AlwaysExistsFunc
	}

	if s.ObjectMissingFunc == nil {
		s.ObjectMissingFunc = NoopObjectMissingFunc
	}

	// adoptee may be empty, but the handler still runs so that it can clean up
	// any old references that may remain.

	_, err := s.GetFromCache(ctx)
	if err != nil && !errors.IsNotFound(err) {
		s.RequeueErr(ctx, err)
		return
	}

	// object is not in cache, which means it's not labelled for the controller
	// this apply uses the controller as field manager, since it will be the
	// same for all owners.
	if len(adoptee.Name) > 0 && errors.IsNotFound(err) {
		// check if object exists at all before applying
		logger.V(5).Info("checking if object exists", "object", adoptee)
		if err := s.ExistsFunc(ctx, adoptee); err != nil {
			s.ObjectMissingFunc(ctx, err)
			return
		}
		logger.V(5).Info("labelling object to make it visible to the index",
			"adoptee", adoptee.String(),
			"manager", s.ControllerFieldManager,
			"labels", s.Labels)
		_, err := s.ApplyFunc(ctx,
			s.NewPatch(adoptee).WithLabels(s.Labels),
			metav1.ApplyOptions{Force: true, FieldManager: s.ControllerFieldManager})
		if err != nil {
			s.RequeueAPIErr(ctx, err)
			return
		}
	}

	// TODO: should the index value be configurable?

	// If the object is not in the index, it needs to be annotated for this
	// owner
	objects, err := s.Indexer.ByIndex(s.IndexName, owner.String())
	if err != nil {
		s.RequeueErr(ctx, err)
		return
	}

	foundMatchingObject := false
	var matchingObject K
	extraObjects := make([]K, 0)
	for _, obj := range objects {
		if obj.GetName() == adoptee.Name && obj.GetNamespace() == adoptee.Namespace {
			matchingObject = obj
			foundMatchingObject = true
		} else {
			extraObjects = append(extraObjects, obj)
		}
	}

	ownerAnnotationKey := s.OwnerAnnotationKeyFunc(owner)

	// Annotate it with an owner-specific annotation and fieldmanager.
	// This allows each owner to sync annotations independently and prevents
	// server-side-apply from wiping out other owner's annotations.
	if !foundMatchingObject && len(adoptee.Name) > 0 {
		logger.V(5).Info("annotating object to adopt it",
			"adoptee", adoptee.String(),
			"owner", owner.String())
		obj, err := s.ApplyFunc(ctx, s.NewPatch(adoptee).
			WithAnnotations(map[string]string{ownerAnnotationKey: Owned}),
			metav1.ApplyOptions{Force: true, FieldManager: s.OwnerFieldManagerFunc(owner)})
		if err != nil {
			s.RequeueAPIErr(ctx, err)
			return
		}

		s.ObjectAdoptedFunc(ctx, obj)
		ctx = s.AdoptedCtx.WithValue(ctx, obj)
	} else {
		ctx = s.AdoptedCtx.WithValue(ctx, matchingObject)
	}

	// Remove annotations from non-matching objects (i.e. objects that were
	// previously owned, so exist in the index, but are no longer referenced by
	// the owner).
	for _, old := range extraObjects {
		nn := types.NamespacedName{Namespace: old.GetNamespace(), Name: old.GetName()}
		hasOtherOwner := false
		for k, v := range old.GetAnnotations() {
			// remove annotation to this owner using the owner fieldmanager
			if k == ownerAnnotationKey && v == Owned {
				logger.V(5).Info("marking object unowned",
					"object", nn.String(),
					"manager", s.OwnerFieldManagerFunc(owner))
				_, err := s.ApplyFunc(ctx,
					s.NewPatch(nn).WithAnnotations(map[string]string{}),
					metav1.ApplyOptions{Force: true, FieldManager: s.OwnerFieldManagerFunc(owner)})
				if err != nil {
					s.RequeueAPIErr(ctx, err)
					return
				}
				continue
			}
			if strings.HasPrefix(k, s.OwnerAnnotationPrefix) {
				hasOtherOwner = true
			}
		}

		// if object is not owned by any other object, remove the controller Labels
		labels := old.GetLabels()
		if !hasOtherOwner {
			for k := range s.Labels {
				delete(labels, k)
			}

			// remove labels with the controller fieldmanager
			logger.V(5).Info("removing controller label",
				"object", nn.String(),
				"manager", s.ControllerFieldManager)
			_, err := s.ApplyFunc(ctx,
				s.NewPatch(nn).WithLabels(map[string]string{}),
				metav1.ApplyOptions{Force: true, FieldManager: s.ControllerFieldManager})
			if err != nil {
				s.RequeueAPIErr(ctx, err)
				return
			}
		}
	}

	s.Next.Handle(ctx)
}
