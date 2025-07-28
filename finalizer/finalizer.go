// Package finalizer provides utilities for managing Kubernetes finalizers in
// controller applications.
//
// The core functions GetAddFunc and GetRemoveFunc return functions that can
// add or remove finalizers from Kubernetes objects. These use get-modify-write
// instead of apply/patch operations to ensure they cannot recreate objects
// that have been marked for deletion.
//
// The SetFinalizerHandler provides a handler that automatically adds
// finalizers to objects that don't already have them, excluding objects
// with deletion timestamps.
package finalizer

import (
	"context"

	"golang.org/x/exp/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/authzed/controller-idioms/component"
	"github.com/authzed/controller-idioms/typed"
)

type (
	AddFunc[K component.KubeObject]    func(ctx context.Context, nn types.NamespacedName) (K, error)
	RemoveFunc[K component.KubeObject] func(ctx context.Context, nn types.NamespacedName) (K, error)
)

// GetAddFunc returns a function that does a get-modify-write instead of an apply, so that it can't accidentally
// re-create a deleted object (apply does an upsert).
func GetAddFunc[K component.KubeObject](client dynamic.Interface, gvr schema.GroupVersionResource, finalizer, fieldManager string) AddFunc[K] {
	return func(ctx context.Context, nn types.NamespacedName) (K, error) {
		// get latest object
		u, err := client.Resource(gvr).Namespace(nn.Namespace).Get(ctx, nn.Name, metav1.GetOptions{})
		if err != nil {
			var nilObj K
			return nilObj, err
		}
		obj, err := typed.UnstructuredObjToTypedObj[K](u)
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		if slices.Contains(obj.GetFinalizers(), finalizer) || obj.GetDeletionTimestamp() != nil {
			return obj, nil
		}

		obj.SetFinalizers(append(obj.GetFinalizers(), finalizer))

		finalized, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		updated, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Update(ctx, &unstructured.Unstructured{Object: finalized}, metav1.UpdateOptions{FieldManager: fieldManager})
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		return typed.UnstructuredObjToTypedObj[K](updated)
	}
}

// GetRemoveFunc returns a function that does a get-modify-write instead of an apply. Unlike when adding, this
// won't be called in cases that could potentially re-create a deleted object, but we do it this way so that the field
// manager operation matches what created the finalizer (`Update`).
func GetRemoveFunc[K component.KubeObject](client dynamic.Interface, gvr schema.GroupVersionResource, finalizer, fieldManager string) RemoveFunc[K] {
	return func(ctx context.Context, nn types.NamespacedName) (K, error) {
		// get latest object
		u, err := client.Resource(gvr).Namespace(nn.Namespace).Get(ctx, nn.Name, metav1.GetOptions{})
		if err != nil {
			var nilObj K
			return nilObj, err
		}
		obj, err := typed.UnstructuredObjToTypedObj[K](u)
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		finalizerIdx := slices.Index(obj.GetFinalizers(), finalizer)

		if finalizerIdx == -1 || obj.GetDeletionTimestamp() == nil {
			return obj, nil
		}
		obj.SetFinalizers(slices.Delete(obj.GetFinalizers(), finalizerIdx, finalizerIdx+1))

		definalized, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		updated, err := client.Resource(gvr).Namespace(obj.GetNamespace()).Update(ctx, &unstructured.Unstructured{Object: definalized}, metav1.UpdateOptions{FieldManager: fieldManager})
		if err != nil {
			var nilObj K
			return nilObj, err
		}

		return typed.UnstructuredObjToTypedObj[K](updated)
	}
}
