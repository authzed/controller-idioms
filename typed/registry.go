package typed

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// FactoryKey gives a name to a SharedInformerFactory. SharedInformerFactories
// can be instantiated against different kube apis or with different filters;
// the key should uniquely identify the factory in the registry.
//
// For example, one factory might be watching all objects in a namespace, while
// another might be watching all objects in a cluster with a specific label.
//
// It's a good idea to include the name of the controller doing initialization.
type FactoryKey string

// NewFactoryKey generates a simple FactoryKey from an id for the controller,
// the cluster it watches, and an extra value.
func NewFactoryKey(controllerName, clusterName, id string) FactoryKey {
	return FactoryKey(fmt.Sprintf("%s/%s/%s", controllerName, clusterName, id))
}

type RegistryKey struct {
	schema.GroupVersionResource
	FactoryKey
}

func NewRegistryKey(key FactoryKey, gvr schema.GroupVersionResource) RegistryKey {
	return RegistryKey{
		GroupVersionResource: gvr,
		FactoryKey:           key,
	}
}

func (k RegistryKey) String() string {
	return fmt.Sprintf("%s/%s", k.GroupVersionResource.String(), k.FactoryKey)
}

type Registry struct {
	sync.RWMutex
	factories map[any]dynamicinformer.DynamicSharedInformerFactory
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[any]dynamicinformer.DynamicSharedInformerFactory),
	}
}

func (r *Registry) MustNewFilteredDynamicSharedInformerFactory(key FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) dynamicinformer.DynamicSharedInformerFactory {
	factory, err := r.NewFilteredDynamicSharedInformerFactory(key, client, defaultResync, namespace, tweakListOptions)
	if err != nil {
		panic(err)
	}
	return factory
}

func (r *Registry) NewFilteredDynamicSharedInformerFactory(key FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) (dynamicinformer.DynamicSharedInformerFactory, error) {
	r.Lock()
	defer r.Unlock()
	if _, ok := r.factories[key]; ok {
		return nil, fmt.Errorf("cannot register two InformerFactories with the same key: %s", key)
	}
	r.factories[key] = dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, defaultResync, namespace, tweakListOptions)
	return r.factories[key], nil
}

func ListerFor[K runtime.Object](r *Registry, key RegistryKey) *Lister[K] {
	return NewLister[K](r.ListerFor(key))
}

func IndexerFor[K runtime.Object](r *Registry, key RegistryKey) *Indexer[K] {
	return NewIndexer[K](r.InformerFor(key).GetIndexer())
}

func (r *Registry) Add(key RegistryKey, factory dynamicinformer.DynamicSharedInformerFactory) {
	r.Lock()
	defer r.Unlock()
	r.factories[key] = factory
}

func (r *Registry) InformerFactoryFor(key RegistryKey) informers.GenericInformer {
	r.RLock()
	defer r.RUnlock()
	factory, ok := r.factories[key.FactoryKey]
	if !ok {
		panic(fmt.Errorf("InformerFactoryFor called with unknown key %s", key))
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
