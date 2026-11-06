//go:build dsk

package dsk

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	k8stesting "k8s.io/client-go/testing"
)

// reactable is satisfied by any fake client whose reactor chain can be
// extended — *dynamicfake.FakeDynamicClient, *k8sfake.Clientset, and
// client/fake's *fakeDynamicClient all qualify.
type reactable interface {
	PrependWatchReactor(resource string, reaction k8stesting.WatchReactionFunc)
	PrependReactor(verb, resource string, reaction k8stesting.ReactionFunc)
	Tracker() k8stesting.ObjectTracker
}

// installReactors adds a universal watch interceptor and a write observer to
// fake. After this call, wrap the client with wrapApply to also intercept
// Apply/ApplyStatus, which bypass the reactor chain in client/fake.
func installReactors(ctx context.Context, fake reactable, e *engine, fs *faultSet, tb TB) {
	tracker := fake.Tracker()

	// Watch reactor: intercepts all Watch calls, returns a ControlledWatch.
	// Drains from the tracker's FakeWatcher so mutations still emit events.
	fake.PrependWatchReactor("*", func(action k8stesting.Action) (bool, watch.Interface, error) {
		wa, ok := action.(k8stesting.WatchAction)
		if !ok {
			return false, nil, nil
		}
		gvr := wa.GetResource()
		ns := wa.GetNamespace()
		// name is always "" for watch calls; OnName() selectors will not match watch faults.
		if err := fs.apply(ctx, "watch", gvr, ns, ""); err != nil {
			return true, nil, err
		}
		// Pass through ListOptions so the tracker can handle sendInitialEvents
		// (watchList protocol). Without this, the reflector's watchList never
		// receives the required BOOKMARK and the informer cache never syncs.
		var upstream watch.Interface
		var err error
		type listOptionsGetter interface {
			GetListOptions() metav1.ListOptions
		}
		if lo, ok := action.(listOptionsGetter); ok {
			opts := lo.GetListOptions()
			upstream, err = tracker.Watch(gvr, ns, opts)
		} else {
			upstream, err = tracker.Watch(gvr, ns)
		}
		if err != nil {
			return true, nil, err
		}
		// Fake tracker ignores label selectors — all events go to all watchers.
		// Register with nil selector (match-all) so preExpectWrite counts correctly.
		return true, interceptWatch(ctx, upstream, e, gvr, ns, nil, tb), nil
	})

	// Write observer: runs before the default ObjectReaction, calls through to
	// it, then records the returned RV so Tick/Buffer can wait for the event.
	//
	// preExpectWrite is called BEFORE the write to avoid a race between
	// event arrival in drainUpstream and the subsequent upgradeExpect/cancelPreExpect call.
	objectReaction := k8stesting.ObjectReaction(tracker)
	fake.PrependReactor("*", "*", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		gvr := action.GetResource()
		ns := action.GetNamespace()
		name := actionName(action)
		if err := fs.apply(ctx, action.GetVerb(), gvr, ns, name); err != nil {
			return true, nil, err
		}
		// Pre-increment pendingAnyEvent before the write so that the drain
		// goroutine cannot decrement before upgradeExpect/cancelPreExpect runs.
		// Pass the object's labels so preExpectWrite can filter out watches whose
		// selector does not match (no-op for fake clients where all watches use nil).
		preN := e.preExpectWrite(action.GetVerb(), gvr, ns, actionObjectLabels(action))
		handled, obj, err := objectReaction(action)
		if preN > 0 {
			if handled && err == nil {
				if mo, ok := obj.(metav1.Object); ok {
					rv := mo.GetResourceVersion()
					if rv != "" {
						// Upgrade to RV-based tracking; removes the pre-increment.
						e.upgradeExpect(gvr, rv, preN)
					}
					// If rv == "", pendingAnyEvent stays incremented — correct.
				} else if action.GetVerb() == "delete" {
					// Delete is the only verb that succeeds with a nil response object.
					// pendingAnyEvent stays incremented; it will be decremented when
					// the DELETED watch event arrives in drainUpstream.
				} else {
					// Unexpected: a non-delete verb succeeded but returned no object.
					// Cancel to avoid a permanent hang.
					e.cancelPreExpect(gvr, preN)
				}
			} else {
				e.cancelPreExpect(gvr, preN)
			}
		}
		return handled, obj, err
	})
}

// actionName extracts the object name from an action if available.
func actionName(action k8stesting.Action) string {
	if na, ok := action.(interface{ GetName() string }); ok {
		return na.GetName()
	}
	return ""
}

// actionObjectLabels returns the label map of the object in a create or update
// action, or nil for patch, delete, or actions without an accessible object.
// Used by preExpectWrite to filter watches by label selector.
func actionObjectLabels(action k8stesting.Action) map[string]string {
	type objectGetter interface {
		GetObject() k8sruntime.Object
	}
	if og, ok := action.(objectGetter); ok {
		if meta, ok := og.GetObject().(interface{ GetLabels() map[string]string }); ok {
			return meta.GetLabels()
		}
	}
	return nil
}

// applyInterceptor wraps a dynamic.Interface to track write expectations after
// Apply and ApplyStatus. These bypass the reactor chain in client/fake.NewClient()
// because fakeDynamicResourceClient.Apply() calls the tracker directly without
// going through Invoke(). All other verbs are handled by the reactor.
//
// tracker is held so that Apply can fall back to a direct Create/Update when
// the underlying fake client's field-managed tracker needs scheme knowledge
// that is not available (e.g. when runtime.NewScheme() is used in tests).
type applyInterceptor struct {
	dynamic.Interface
	engine  *engine
	tracker k8stesting.ObjectTracker
	faults  *faultSet
}

// IsWatchListSemanticsUnSupported forwards the optional WatchList capability
// check to the underlying client. Without this, embedding dynamic.Interface
// hides the concrete FakeDynamicClient method, causing the reflector to assume
// WatchList is supported and wait for a BOOKMARK that the fake tracker never sends.
func (a *applyInterceptor) IsWatchListSemanticsUnSupported() bool {
	type watchListChecker interface {
		IsWatchListSemanticsUnSupported() bool
	}
	if c, ok := a.Interface.(watchListChecker); ok {
		return c.IsWatchListSemanticsUnSupported()
	}
	return false
}

func (a *applyInterceptor) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &applyInterceptorResource{
		NamespaceableResourceInterface: a.Interface.Resource(gvr),
		engine:                         a.engine,
		tracker:                        a.tracker,
		faults:                         a.faults,
		gvr:                            gvr,
	}
}

type applyInterceptorResource struct {
	dynamic.NamespaceableResourceInterface
	engine  *engine
	tracker k8stesting.ObjectTracker
	faults  *faultSet
	gvr     schema.GroupVersionResource
}

func (r *applyInterceptorResource) Namespace(ns string) dynamic.ResourceInterface {
	return &applyInterceptorNamespacedResource{
		ResourceInterface: r.NamespaceableResourceInterface.Namespace(ns),
		engine:            r.engine,
		tracker:           r.tracker,
		faults:            r.faults,
		gvr:               r.gvr,
		namespace:         ns,
	}
}

// Apply intercepts cluster-scoped Apply calls (i.e. without a prior .Namespace()
// call). Without this override, Apply would go directly to the embedded
// NamespaceableResourceInterface, bypassing preExpectWrite/upgradeExpect.
func (r *applyInterceptorResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return doApplyWithTracking(ctx, r.faults, r.engine, r.tracker, r.gvr, "", name, obj, func() (*unstructured.Unstructured, error) {
		return r.NamespaceableResourceInterface.Apply(ctx, name, obj, opts, subresources...)
	})
}

// ApplyStatus intercepts cluster-scoped ApplyStatus calls (i.e. without a prior
// .Namespace() call). Without this override, ApplyStatus would go directly to
// the embedded NamespaceableResourceInterface, bypassing preExpectWrite/upgradeExpect.
func (r *applyInterceptorResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return doApplyWithTracking(ctx, r.faults, r.engine, r.tracker, r.gvr, "", name, obj, func() (*unstructured.Unstructured, error) {
		return r.NamespaceableResourceInterface.ApplyStatus(ctx, name, obj, opts)
	})
}

type applyInterceptorNamespacedResource struct {
	dynamic.ResourceInterface
	engine    *engine
	tracker   k8stesting.ObjectTracker
	faults    *faultSet
	gvr       schema.GroupVersionResource
	namespace string
}

func (r *applyInterceptorNamespacedResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return doApplyWithTracking(ctx, r.faults, r.engine, r.tracker, r.gvr, r.namespace, name, obj, func() (*unstructured.Unstructured, error) {
		return r.ResourceInterface.Apply(ctx, name, obj, opts, subresources...)
	})
}

func (r *applyInterceptorNamespacedResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return doApplyWithTracking(ctx, r.faults, r.engine, r.tracker, r.gvr, r.namespace, name, obj, func() (*unstructured.Unstructured, error) {
		return r.ResourceInterface.ApplyStatus(ctx, name, obj, opts)
	})
}

// doApplyWithTracking wraps an Apply or ApplyStatus call with fault injection
// and preExpectWrite/upgradeExpect tracking, falling back to trackerApply on
// error (to support fake clients backed by runtime.NewScheme() where the
// field-managed tracker cannot resolve the GVK).
func doApplyWithTracking(ctx context.Context, fs *faultSet, eng *engine, tracker k8stesting.ObjectTracker, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured, apply func() (*unstructured.Unstructured, error)) (*unstructured.Unstructured, error) {
	if err := fs.apply(ctx, "patch", gvr, namespace, name); err != nil {
		return nil, err
	}
	// Apply/ApplyStatus is a patch; labels are in obj but the final merged
	// state is unknown before the call, so pass nil (conservative match-all).
	preN := eng.preExpectWrite("patch", gvr, namespace, nil)
	result, err := apply()
	if err != nil {
		result, err = trackerApply(tracker, gvr, namespace, name, obj)
	}
	if preN > 0 {
		if err == nil {
			if rv := result.GetResourceVersion(); rv != "" {
				eng.upgradeExpect(gvr, rv, preN)
			}
			// If rv == "", pendingAnyEvent stays incremented — correct.
		} else {
			eng.cancelPreExpect(gvr, preN)
		}
	}
	return result, err
}

// trackerApply stores obj in the plain tracker (Create if new, Update if
// existing) and returns the stored copy. Used as a fallback when the
// field-managed tracker is unavailable due to a missing scheme registration,
// while still emitting watch events via the plain tracker's watcher.
func trackerApply(tracker k8stesting.ObjectTracker, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	_, getErr := tracker.Get(gvr, namespace, name)
	if apierrors.IsNotFound(getErr) {
		if err := tracker.Create(gvr, obj, namespace); err != nil {
			return nil, err
		}
	} else if getErr == nil {
		if err := tracker.Update(gvr, obj, namespace); err != nil {
			return nil, err
		}
	} else {
		return nil, getErr
	}
	stored, err := tracker.Get(gvr, namespace, name)
	if err != nil {
		return nil, err
	}
	if u, ok := stored.(*unstructured.Unstructured); ok {
		return u, nil
	}
	m, err := k8sruntime.DefaultUnstructuredConverter.ToUnstructured(stored)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// wrapApply wraps a fake dynamic.Interface so Apply/ApplyStatus trigger fault
// injection and RV tracking.
func wrapApply(underlying dynamic.Interface, e *engine, tracker k8stesting.ObjectTracker, fs *faultSet) dynamic.Interface {
	return &applyInterceptor{Interface: underlying, engine: e, tracker: tracker, faults: fs}
}

// interceptWatch wraps an upstream watch.Interface in a ControlledWatch and
// starts the drain goroutine. ns is the namespace this watch was opened for
// ("" means all namespaces / cluster-scoped). sel is the label selector for
// this watch; nil means match-all (correct for fake clients).
func interceptWatch(ctx context.Context, upstream watch.Interface, e *engine, gvr schema.GroupVersionResource, ns string, sel labels.Selector, tb TB) *ControlledWatch {
	cw := NewControlledWatch(tb)
	e.registerWatch(gvr, ns, sel)
	go drainUpstream(ctx, upstream, cw, e, gvr, ns, sel)
	return cw
}

// drainUpstream reads events from upstream and appends them to the engine
// buffer. Stops when ctx is done, the upstream channel closes, or cw.Stop()
// is called.
func drainUpstream(ctx context.Context, upstream watch.Interface, cw *ControlledWatch, e *engine, gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	defer upstream.Stop()
	defer e.unregisterWatch(gvr, ns, sel)
	for {
		select {
		case <-ctx.Done():
			return
		case <-cw.Done():
			return
		case ev, ok := <-upstream.ResultChan():
			if !ok {
				return
			}
			e.addToBuffer(pendingEvent{event: ev, dest: cw, gvr: gvr})
		}
	}
}
