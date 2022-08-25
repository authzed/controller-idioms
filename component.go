package libctrl

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/authzed/ktrllib/typed"
)

type KubeObject interface {
	metav1.Object
	runtime.Object
}

// Component represents an KubeObject in the cluster that is "related" to another
// i.e. a pod would be a component of a deployment (though direct ownership is
// not required).
type Component[K KubeObject] struct {
	indexer   *typed.Indexer[K]
	selector  labels.Selector
	indexName string
}

// NewIndexedComponent creates a component from an index
func NewIndexedComponent[K KubeObject](indexer *typed.Indexer[K], indexName string, selector labels.Selector) *Component[K] {
	return &Component[K]{
		indexer:   indexer,
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
		ls := d.GetLabels()
		if ls == nil {
			continue
		}
		if c.selector.Matches(labels.Set(ls)) {
			out = append(out, d)
		}
	}
	return out
}

// HashableComponent is a Component with an annotation that stores a hash of the
// previous configuration the controller wrote
type HashableComponent[K KubeObject] struct {
	Component[K]
	ObjectHasher
	HashAnnotationKey string
}

func NewHashableComponent[K KubeObject](component Component[K], hasher ObjectHasher, key string) *HashableComponent[K] {
	return &HashableComponent[K]{
		Component:         component,
		ObjectHasher:      hasher,
		HashAnnotationKey: key,
	}
}
