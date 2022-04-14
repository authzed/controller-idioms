package libctrl

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Component represents an object in the cluster that is "related" to another
// i.e. a pod would be a component of a deployment (though direct ownership is
// not required).
type Component[K metav1.Object] struct {
	indexer   cache.Indexer
	selector  labels.Selector
	indexName string
	gvr       schema.GroupVersionResource
}

// TODO: may want other constructors

// NewComponent creates a component from an index name, a gvr, a selector, and a
// set of informers.
func NewComponent[K metav1.Object](informers map[schema.GroupVersionResource]dynamicinformer.DynamicSharedInformerFactory, gvr schema.GroupVersionResource, indexName string, selector labels.Selector) *Component[K] {
	return &Component[K]{
		indexer:   informers[gvr].ForResource(gvr).Informer().GetIndexer(),
		gvr:       gvr,
		indexName: indexName,
		selector:  selector,
	}
}

// List all objects that match the component's specification.
// Components are expected to be unique (per label), but List returns a slice
// so that controllers can handle duplicates appropriately.
func (c *Component[K]) List(indexValue fmt.Stringer) (out []K) {
	out = make([]K, 0)
	ownedObjects, err := c.indexer.ByIndex(c.indexName, indexValue.String())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, d := range ownedObjects {
		unst, ok := d.(*unstructured.Unstructured)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("invalid object returned from index, expected unstructured, got: %T", d))
			continue
		}
		var obj *K
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unst.Object, &obj); err != nil {
			utilruntime.HandleError(fmt.Errorf("invalid object returned from index, expected %s, got: %s: %w", c.gvr.Resource, unst.GroupVersionKind(), err))
			continue
		}
		ls := (*obj).GetLabels()
		if ls == nil {
			continue
		}
		if c.selector.Matches(labels.Set(ls)) {
			out = append(out, *obj)
		}
	}
	return out
}

// HashableComponent is a Component with an annotation that stores a hash of the
// previous configuration the controller wrote
type HashableComponent[K metav1.Object] struct {
	Component[K]
	ObjectHasher
	HashAnnotationKey string
}

func NewHashableComponent[K metav1.Object](component Component[K], hasher ObjectHasher, key string) *HashableComponent[K] {
	return &HashableComponent[K]{
		Component:         component,
		ObjectHasher:      hasher,
		HashAnnotationKey: key,
	}
}
