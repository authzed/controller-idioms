// Package component implements a common controller pattern: an object is
// specified by a user, and another kube object needs to be created in response.
//
// `Component`specifies how to find these related objects in the cluster.
//
// `ContextHandler` is a handler.Handler implementation that can pick out
// a component from a cluster and add it into a context.Context key.
//
// `EnsureComponent` is a handler.Handler implementation that ensures that a
// component with a given specification exists. It handles creating the object
// if it doesn't exist and updating it only if the calculated object has
// changed. It also cleans up duplicate matching component objects by deleting
// any that match the component selector but do not match the calculated object.
package component

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/authzed/controller-idioms/hash"
	"github.com/authzed/controller-idioms/typed"
)

// KubeObject is satisfied by any standard kube object.
type KubeObject interface {
	metav1.Object
	runtime.Object
}

// Component represents a KubeObject in the cluster that is "related" to another
// i.e. a pod could be a component of a deployment (though direct ownership is
// not required).
type Component[K KubeObject] struct {
	indexer      *typed.Indexer[K]
	selectorFunc func(ctx context.Context) labels.Selector
	indexName    string
}

// NewIndexedComponent creates a Component from an index
func NewIndexedComponent[K KubeObject](indexer *typed.Indexer[K], indexName string, selectorFunc func(ctx context.Context) labels.Selector) *Component[K] {
	return &Component[K]{
		indexer:      indexer,
		indexName:    indexName,
		selectorFunc: selectorFunc,
	}
}

// List all objects that match the component's specification.
// Components are expected to be unique (per label), but List returns a slice
// so that controllers can handle duplicates appropriately.
func (c *Component[K]) List(ctx context.Context, indexValue fmt.Stringer) (out []K) {
	out = make([]K, 0)
	ownedObjects, err := c.indexer.ByIndex(c.indexName, indexValue.String())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, d := range ownedObjects {
		ls := d.GetLabels()
		if ls == nil {
			continue
		}
		if c.selectorFunc(ctx).Matches(labels.Set(ls)) {
			out = append(out, d)
		}
	}
	return out
}

// HashableComponent is a Component with an annotation that stores a hash of the
// previous configuration written by the controller. The hash is used to
// determine if work still needs to be done.
type HashableComponent[K KubeObject] struct {
	*Component[K]
	hash.ObjectHasher
	HashAnnotationKey string
}

// NewHashableComponent creates HashableComponent from a Component and a
// hash.ObjectHasher, plus an annotation key to use to store the hash on the
// object.
func NewHashableComponent[K KubeObject](component *Component[K], hasher hash.ObjectHasher, key string) *HashableComponent[K] {
	return &HashableComponent[K]{
		Component:         component,
		ObjectHasher:      hasher,
		HashAnnotationKey: key,
	}
}
