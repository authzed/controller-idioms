//go:build dsk

package dsk

import (
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// trackedHandler wraps a cache.ResourceEventHandler so that DSK knows when
// each callback invocation has completed. handlerStart/handlerDone bracket
// every On* call, letting flush() wait until all handler goroutines have
// returned before declaring the tick window closed.
//
// handlerDoneCall is also called on completion: it debits the pendingCalls
// credit that flush() set before delivering the triggering event. This ensures
// waitForHandlersIdle blocks until all expected handler invocations are done,
// even when the reflector goroutine hasn't started processing the event yet.
type trackedHandler struct {
	inner cache.ResourceEventHandler
	eng   *engine
}

func (h *trackedHandler) OnAdd(obj any, isInInitialList bool) {
	h.eng.handlerStart()
	defer func() {
		h.eng.handlerDone()
		h.eng.handlerDoneCall()
	}()
	h.inner.OnAdd(obj, isInInitialList)
}

func (h *trackedHandler) OnUpdate(oldObj, newObj any) {
	h.eng.handlerStart()
	defer func() {
		h.eng.handlerDone()
		h.eng.handlerDoneCall()
	}()
	h.inner.OnUpdate(oldObj, newObj)
}

func (h *trackedHandler) OnDelete(obj any) {
	h.eng.handlerStart()
	defer func() {
		h.eng.handlerDone()
		h.eng.handlerDoneCall()
	}()
	h.inner.OnDelete(obj)
}

// compile-time check
var _ cache.ResourceEventHandler = (*trackedHandler)(nil)

// dskSharedIndexInformer wraps cache.SharedIndexInformer to intercept
// AddEventHandler calls, wrapping each handler with trackedHandler and
// registering the handler with its label selector so flush() can credit
// pendingCalls correctly, filtering by selector for real-cluster informers.
type dskSharedIndexInformer struct {
	cache.SharedIndexInformer
	eng       *engine
	gvr       schema.GroupVersionResource
	namespace string
	selector  labels.Selector // nil for fake clients (match-all); non-nil for real clusters
}

// dskHandlerRegistration wraps a cache.ResourceEventHandlerRegistration,
// carrying the gvr/ns/sel needed to call unregisterHandler on removal.
type dskHandlerRegistration struct {
	inner cache.ResourceEventHandlerRegistration
	eng   *engine
	gvr   schema.GroupVersionResource
	ns    string
	sel   labels.Selector
}

func (r *dskHandlerRegistration) HasSynced() bool { return r.inner.HasSynced() }

var _ cache.ResourceEventHandlerRegistration = (*dskHandlerRegistration)(nil)

func (i *dskSharedIndexInformer) addTracked(handler cache.ResourceEventHandler, addFn func(cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error)) (cache.ResourceEventHandlerRegistration, error) {
	i.eng.registerHandler(i.gvr, i.namespace, i.selector)
	reg, err := addFn(&trackedHandler{inner: handler, eng: i.eng})
	if err != nil {
		i.eng.unregisterHandler(i.gvr, i.namespace, i.selector)
		return nil, err
	}
	return &dskHandlerRegistration{inner: reg, eng: i.eng, gvr: i.gvr, ns: i.namespace, sel: i.selector}, nil
}

func (i *dskSharedIndexInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return i.addTracked(handler, i.SharedIndexInformer.AddEventHandler)
}

func (i *dskSharedIndexInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return i.addTracked(handler, func(h cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
		return i.SharedIndexInformer.AddEventHandlerWithResyncPeriod(h, resyncPeriod)
	})
}

func (i *dskSharedIndexInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	return i.addTracked(handler, func(h cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
		return i.SharedIndexInformer.AddEventHandlerWithOptions(h, opts)
	})
}

func (i *dskSharedIndexInformer) RemoveEventHandler(handle cache.ResourceEventHandlerRegistration) error {
	if dskHandle, ok := handle.(*dskHandlerRegistration); ok {
		i.eng.unregisterHandler(dskHandle.gvr, dskHandle.ns, dskHandle.sel)
		return i.SharedIndexInformer.RemoveEventHandler(dskHandle.inner)
	}
	return i.SharedIndexInformer.RemoveEventHandler(handle)
}

// dskGenericInformer wraps informers.GenericInformer so that Informer()
// returns a handler-tracking dskSharedIndexInformer.
type dskGenericInformer struct {
	informers.GenericInformer
	eng       *engine
	gvr       schema.GroupVersionResource
	namespace string
	selector  labels.Selector // nil for fake clients (match-all); non-nil for real clusters
}

func (g *dskGenericInformer) Informer() cache.SharedIndexInformer {
	return &dskSharedIndexInformer{
		SharedIndexInformer: g.GenericInformer.Informer(),
		eng:                 g.eng,
		gvr:                 g.gvr,
		namespace:           g.namespace,
		selector:            g.selector,
	}
}

// dskFactory wraps dynamicinformer.DynamicSharedInformerFactory so that
// ForResource() returns a handler-tracking dskGenericInformer.
//
// selector is computed once at construction. For fake clients it is always nil
// (match-all) because the fake tracker sends all events to all watchers
// regardless of label selectors. For real clients it is derived from
// tweakListOptions at construction time.
type dskFactory struct {
	dynamicinformer.DynamicSharedInformerFactory
	eng       *engine
	namespace string
	selector  labels.Selector // nil for fake clients (match-all); computed once for real clients
}

func (f *dskFactory) ForResource(resource schema.GroupVersionResource) informers.GenericInformer {
	return &dskGenericInformer{
		GenericInformer: f.DynamicSharedInformerFactory.ForResource(resource),
		eng:             f.eng,
		gvr:             resource,
		namespace:       f.namespace,
		selector:        f.selector,
	}
}

// compile-time checks
var (
	_ dynamicinformer.DynamicSharedInformerFactory = (*dskFactory)(nil)
	_ informers.GenericInformer                    = (*dskGenericInformer)(nil)
	_ cache.SharedIndexInformer                    = (*dskSharedIndexInformer)(nil)
)
