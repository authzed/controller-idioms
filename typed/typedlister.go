package typed

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

type TypedLister[K runtime.Object] struct {
	lister cache.GenericLister
}

func NewTypedLister[K runtime.Object](lister cache.GenericLister) *TypedLister[K] {
	return &TypedLister[K]{lister: lister}
}

func (t TypedLister[K]) List(selector labels.Selector) (ret []K, err error) {
	objs, err := t.lister.List(selector)
	if err != nil {
		return nil, err
	}

	return UnstructuredListToTypeList[K](objs)
}

func (t TypedLister[K]) Get(name string) (K, error) {
	var typedObj *K
	obj, err := t.lister.Get(name)
	if err != nil {
		return *typedObj, err
	}
	return UnstructuredObjToTypedObj[K](obj)
}

func (t TypedLister[K]) ByNamespace(namespace string) TypedNamespaceLister[K] {
	return TypedNamespaceLister[K]{
		lister: t.lister.ByNamespace(namespace),
	}
}

type TypedNamespaceLister[K runtime.Object] struct {
	lister cache.GenericNamespaceLister
}

func (t TypedNamespaceLister[K]) List(selector labels.Selector) (ret []K, err error) {
	objs, err := t.lister.List(selector)
	if err != nil {
		return nil, err
	}

	return UnstructuredListToTypeList[K](objs)
}

func (t TypedNamespaceLister[K]) Get(name string) (K, error) {
	var typedObj *K
	obj, err := t.lister.Get(name)
	if err != nil {
		return *typedObj, err
	}
	return UnstructuredObjToTypedObj[K](obj)
}

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

func UnstructuredObjToTypedObj[K runtime.Object](obj runtime.Object) (K, error) {
	var typedObj *K
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return *typedObj, fmt.Errorf("invalid object %T", obj)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &typedObj); err != nil {
		return *typedObj, fmt.Errorf("invalid object: %w", err)
	}
	return *typedObj, nil
}
