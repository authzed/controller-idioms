// Package typed converts (dynamic) kube informers, listers, and indexers into
// typed counterparts via generics.
//
// It can be useful to access the informer cache of one controller from another
// place, so that multiple controllers in the same binary don't need to open
// separate connections against the kube apiserver and maintain separate caches
// of the same objects.
//
// The `typed` package also provides a `Registry` that synchronizes access to
// shared informer factories across multiple controllers.
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

// RegistryKey identifies a specific GVR within a factory provided by a Registry
type RegistryKey struct {
	schema.GroupVersionResource
	FactoryKey
}

// NewRegistryKey creates a RegistryKey from a FactoryKey
func NewRegistryKey(key FactoryKey, gvr schema.GroupVersionResource) RegistryKey {
	return RegistryKey{
		GroupVersionResource: gvr,
		FactoryKey:           key,
	}
}

func (k RegistryKey) String() string {
	return fmt.Sprintf("%s/%s", k.GroupVersionResource.String(), k.FactoryKey)
}

// Registry is a threadsafe map of DynamicSharedInformerFactory
// By registering informer factories with the registry, handlers from other
// controllers can easily access the cached resources held by the informer.
type Registry struct {
	sync.RWMutex
	factories map[any]dynamicinformer.DynamicSharedInformerFactory
}

// NewRegistry returns a new, empty Registry
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[any]dynamicinformer.DynamicSharedInformerFactory),
	}
}

// MustNewFilteredDynamicSharedInformerFactory creates a new SharedInformerFactory
// and registers it under the given FactoryKey. It panics if there is already
// an entry with that key.
func (r *Registry) MustNewFilteredDynamicSharedInformerFactory(key FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) dynamicinformer.DynamicSharedInformerFactory {
	factory, err := r.NewFilteredDynamicSharedInformerFactory(key, client, defaultResync, namespace, tweakListOptions)
	if err != nil {
		panic(err)
	}
	return factory
}

// NewFilteredDynamicSharedInformerFactory creates a new SharedInformerFactory
// and registers it under the given FactoryKey
func (r *Registry) NewFilteredDynamicSharedInformerFactory(key FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) (dynamicinformer.DynamicSharedInformerFactory, error) {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, defaultResync, namespace, tweakListOptions)
	if err := r.Add(key, factory); err != nil {
		return nil, err
	}
	return factory, nil
}

// ListerFor returns a typed Lister from a Registry
// Deprecated: Use MustListerForKey instead
func ListerFor[K runtime.Object](r *Registry, key RegistryKey) *Lister[K] {
	return MustListerForKey[K](r, key)
}

// MustListerForKey returns a typed Lister from a Registry, or panics if the key is not found
func MustListerForKey[K runtime.Object](r *Registry, key RegistryKey) *Lister[K] {
	return NewLister[K](r.MustListerForKey(key))
}

// ListerForKey returns a typed Lister from a Registry, or an error if the key is not found
func ListerForKey[K runtime.Object](r *Registry, key RegistryKey) (*Lister[K], error) {
	lister, err := r.ListerForKey(key)
	if err != nil {
		return nil, err
	}
	return NewLister[K](lister), nil
}

// IndexerFor returns a typed Indexer from a Registry
// Deprecated: Use MustIndexerForKey instead
func IndexerFor[K runtime.Object](r *Registry, key RegistryKey) *Indexer[K] {
	return MustIndexerForKey[K](r, key)
}

// MustIndexerForKey returns a typed Indexer from a Registry, or panics if the key is not found
func MustIndexerForKey[K runtime.Object](r *Registry, key RegistryKey) *Indexer[K] {
	return NewIndexer[K](r.MustIndexerForKey(key))
}

// IndexerForKey returns a typed Indexer from a Registry, or an error if the key is not found
func IndexerForKey[K runtime.Object](r *Registry, key RegistryKey) (*Indexer[K], error) {
	indexer, err := r.IndexerForKey(key)
	if err != nil {
		return nil, err
	}
	return NewIndexer[K](indexer), nil
}

// Add adds a factory to the registry under the given FactoryKey
func (r *Registry) Add(key FactoryKey, factory dynamicinformer.DynamicSharedInformerFactory) error {
	r.Lock()
	defer r.Unlock()
	if _, ok := r.factories[key]; ok {
		return fmt.Errorf("cannot register two InformerFactories with the same key: %s", key)
	}
	r.factories[key] = factory
	return nil
}

// Remove removes a factory from the registry. Note that it does not stop any
// informers that were started via the factory; they should be stopped via
// context cancellation.
func (r *Registry) Remove(key FactoryKey) {
	r.Lock()
	defer r.Unlock()
	delete(r.factories, key)
}

// InformerFactoryFor returns GVR-specific InformerFactory from the Registry.
// Deprecated: use MustInformerFactoryForKey instead.
func (r *Registry) InformerFactoryFor(key RegistryKey) informers.GenericInformer {
	return r.MustInformerFactoryForKey(key)
}

// MustInformerFactoryForKey returns GVR-specific InformerFactory from the Registry
// or panics if the key is not found.
func (r *Registry) MustInformerFactoryForKey(key RegistryKey) informers.GenericInformer {
	informer, err := r.InformerFactoryForKey(key)
	if err != nil {
		panic(err)
	}
	return informer
}

// InformerFactoryForKey returns GVR-specific InformerFactory from the Registry
// or returns an error if the key is not found.
func (r *Registry) InformerFactoryForKey(key RegistryKey) (informers.GenericInformer, error) {
	r.RLock()
	defer r.RUnlock()
	factory, ok := r.factories[key.FactoryKey]
	if !ok {
		return nil, fmt.Errorf("InformerFactoryFor called with unknown key %s", key)
	}
	return factory.ForResource(key.GroupVersionResource), nil
}

// ListerFor returns the GVR-specific Lister from the Registry
// Deprecated: use MustListerForKey instead.
func (r *Registry) ListerFor(key RegistryKey) cache.GenericLister {
	return r.MustInformerFactoryForKey(key).Lister()
}

// MustListerForKey returns the GVR-specific Lister from the Registry, or panics
// if the key is not found.
func (r *Registry) MustListerForKey(key RegistryKey) cache.GenericLister {
	return r.MustInformerFactoryForKey(key).Lister()
}

// ListerForKey returns the GVR-specific Lister from the Registry, or an error
// if the key is not found.
func (r *Registry) ListerForKey(key RegistryKey) (cache.GenericLister, error) {
	factory, err := r.InformerFactoryForKey(key)
	if err != nil {
		return nil, err
	}
	return factory.Lister(), nil
}

// InformerFor returns the GVR-specific Informer from the Registry
// Deprecated: use MustInformerForKey instead.
func (r *Registry) InformerFor(key RegistryKey) cache.SharedIndexInformer {
	return r.MustInformerFactoryForKey(key).Informer()
}

// MustInformerForKey returns the GVR-specific Informer from the Registry, or panics
// if the key is not found.
func (r *Registry) MustInformerForKey(key RegistryKey) cache.SharedIndexInformer {
	return r.MustInformerFactoryForKey(key).Informer()
}

// InformerForKey returns the GVR-specific Informer from the Registry, or an error
// if the key is not found.
func (r *Registry) InformerForKey(key RegistryKey) (cache.SharedIndexInformer, error) {
	factory, err := r.InformerFactoryForKey(key)
	if err != nil {
		return nil, err
	}
	return factory.Informer(), nil
}

// IndexerFor returns the GVR-specific Indexer from the Registry
// Deprecated: use MustIndexerForKey instead.
func (r *Registry) IndexerFor(key RegistryKey) cache.Indexer {
	return r.MustInformerForKey(key).GetIndexer()
}

// MustIndexerForKey returns the GVR-specific Indexer from the Registry, or panics
// if the key is not found.
func (r *Registry) MustIndexerForKey(key RegistryKey) cache.Indexer {
	return r.MustInformerForKey(key).GetIndexer()
}

// IndexerForKey returns the GVR-specific Indexer from the Registry, or an error
// if the key is not found.
func (r *Registry) IndexerForKey(key RegistryKey) (cache.Indexer, error) {
	informer, err := r.InformerForKey(key)
	if err != nil {
		return nil, err
	}
	return informer.GetIndexer(), nil
}
