package typed

// NOTE: this is not used yet, API and implementation may change

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

const AllResources = "all"

type RegistryKey struct {
	schema.GroupVersionResource
	name string
}

func (k RegistryKey) String() string {
	return fmt.Sprintf("%s/%s", k.GroupVersionResource.String(), k.name)
}

func NewGVRRegistryKey(gvr schema.GroupVersionResource) RegistryKey {
	return RegistryKey{
		GroupVersionResource: gvr,
		name:                 AllResources,
	}
}

func NewRegistryKey(gvr schema.GroupVersionResource, name string) RegistryKey {
	return RegistryKey{
		GroupVersionResource: gvr,
		name:                 name,
	}
}

type Registry struct {
	mapper    meta.RESTMapper
	informers map[any]dynamicinformer.DynamicSharedInformerFactory
}

func TypedListerFor[K runtime.Object](r *Registry, key RegistryKey) *TypedLister[K] {
	return NewTypedLister[K](r.ListerFor(key))
}

func TypedIndexerFor[K runtime.Object](r *Registry, key RegistryKey) *TypedIndexer[K] {
	return NewTypedIndexer[K](r.InformerFor(key).GetIndexer())
}

func (r *Registry) InformerFactoryFor(key RegistryKey) informers.GenericInformer {
	factory, ok := r.informers[key]
	if !ok {
		utilruntime.HandleError(fmt.Errorf("InformerFactoryFor called with unknown key %s", key))
		return nil
	}
	return factory.ForResource(key.GroupVersionResource)
}

func (r *Registry) ListerFor(key RegistryKey) cache.GenericLister {
	return r.InformerFactoryFor(key).Lister()
}

func (r *Registry) InformerFor(key RegistryKey) cache.SharedIndexInformer {
	return r.InformerFactoryFor(key).Informer()
}

func (r *Registry) IndexerFor(key RegistryKey) cache.Indexer {
	return r.InformerFactoryFor(key).Informer().GetIndexer()
}
