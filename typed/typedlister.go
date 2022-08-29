package typed

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// Lister provides a generically typed interface for cache.GenericLister
// It assumes the objects are unstructured.Unstructured, as you would get
// from a dynamic informer.
type Lister[K runtime.Object] struct {
	lister cache.GenericLister
}

// NewLister returns a Lister for a cache.GenericLister
func NewLister[K runtime.Object](lister cache.GenericLister) *Lister[K] {
	return &Lister[K]{lister: lister}
}

func (t Lister[K]) List(selector labels.Selector) (ret []K, err error) {
	objs, err := t.lister.List(selector)
	if err != nil {
		return nil, err
	}

	return UnstructuredListToTypeList[K](objs)
}

func (t Lister[K]) Get(name string) (K, error) {
	obj, err := t.lister.Get(name)
	if err != nil {
		var nilObj K
		return nilObj, err
	}
	return UnstructuredObjToTypedObj[K](obj)
}

func (t Lister[K]) ByNamespace(namespace string) NamespaceLister[K] {
	return NamespaceLister[K]{
		lister: t.lister.ByNamespace(namespace),
	}
}

// NamespaceLister provides a generically typed interface for
// cache.GenericNamespaceLister.
// It assumes the objects are unstructured.Unstructured, as you would get
// from a dynamic informer.
type NamespaceLister[K runtime.Object] struct {
	lister cache.GenericNamespaceLister
}

func (t NamespaceLister[K]) List(selector labels.Selector) (ret []K, err error) {
	objs, err := t.lister.List(selector)
	if err != nil {
		return nil, err
	}

	return UnstructuredListToTypeList[K](objs)
}

func (t NamespaceLister[K]) Get(name string) (K, error) {
	obj, err := t.lister.Get(name)
	if err != nil {
		var nilObj K
		return nilObj, err
	}
	return UnstructuredObjToTypedObj[K](obj)
}

// UnstructuredListToTypeList is a helper that converts a list of unstructured
// to a particular type.
func UnstructuredListToTypeList[K runtime.Object](objs []runtime.Object) ([]K, error) {
	typedObjs := make([]K, 0, len(objs))
	for _, obj := range objs {
		typedObj, err := UnstructuredObjToTypedObj[K](obj)
		if err != nil {
			return nil, fmt.Errorf("list conversion error: %w", err)
		}
		typedObjs = append(typedObjs, typedObj)
	}
	return typedObjs, nil
}

// UnstructuredObjToTypedObj is a helper that converts an unstructured object
// to a particular type.
func UnstructuredObjToTypedObj[K runtime.Object](obj runtime.Object) (K, error) {
	var typedObj *K
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		var nilObj K
		return nilObj, fmt.Errorf("invalid object %T", obj)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &typedObj); err != nil {
		var nilObj K
		return nilObj, fmt.Errorf("invalid object: %w", err)
	}
	return *typedObj, nil
}
