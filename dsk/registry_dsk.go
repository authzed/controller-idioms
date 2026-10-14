//go:build dsk

package dsk

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"

	"github.com/authzed/controller-idioms/typed"
)

// Registry is a DSK-aware InformerRegistry. It embeds *typed.Registry and
// overrides factory construction to wrap each factory with dskFactory so that
// AddEventHandler calls are intercepted and tracked by the DSK engine.
//
// Use dsk.NewRegistry(d) in test setup wherever typed.NewRegistry() is used
// in production. *dsk.Registry satisfies typed.InformerRegistry.
type Registry struct {
	*typed.Registry
	d *DSK
}

// NewRegistry returns a DSK-aware Registry bound to d. All informer factories
// created through this registry will wrap their AddEventHandler calls so that
// Flush() can wait for all handler invocations to complete.
func NewRegistry(d *DSK) *Registry {
	return &Registry{Registry: typed.NewRegistry(), d: d}
}

// MustNewFilteredDynamicSharedInformerFactory overrides the embedded method
// to wrap the factory with DSK handler interception before registering it.
func (r *Registry) MustNewFilteredDynamicSharedInformerFactory(key typed.FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) dynamicinformer.DynamicSharedInformerFactory {
	factory, err := r.NewFilteredDynamicSharedInformerFactory(key, client, defaultResync, namespace, tweakListOptions)
	if err != nil {
		r.d.tb.Fatalf("dsk: MustNewFilteredDynamicSharedInformerFactory: %v", err)
		return nil
	}
	return factory
}

// NewFilteredDynamicSharedInformerFactory overrides the embedded method to
// wrap the factory with DSK handler interception before registering it.
func (r *Registry) NewFilteredDynamicSharedInformerFactory(key typed.FactoryKey, client dynamic.Interface, defaultResync time.Duration, namespace string, tweakListOptions dynamicinformer.TweakListOptionsFunc) (dynamicinformer.DynamicSharedInformerFactory, error) {
	raw := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, defaultResync, namespace, tweakListOptions)

	// Compute the label selector once at construction. For fake clients, always
	// nil (match-all): the fake tracker sends all events to all watchers
	// regardless of label selectors. For real clients, derive from tweakListOptions.
	var sel labels.Selector
	if _, isFake := client.(reactable); !isFake && tweakListOptions != nil {
		opts := &metav1.ListOptions{}
		tweakListOptions(opts)
		if opts.LabelSelector != "" {
			if parsed, err := labels.Parse(opts.LabelSelector); err == nil {
				sel = parsed
			}
		}
	}

	wrapped := &dskFactory{
		DynamicSharedInformerFactory: raw,
		eng:                          r.d.engine,
		namespace:                    namespace,
		selector:                     sel,
	}
	if err := r.Registry.Add(key, wrapped); err != nil {
		return nil, err
	}
	return wrapped, nil
}

// compile-time check: *Registry satisfies typed.InformerRegistry
var _ typed.InformerRegistry = (*Registry)(nil)
