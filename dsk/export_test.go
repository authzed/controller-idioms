//go:build dsk

package dsk

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// ExportNewEngine creates a new engine for testing. Not for production use.
func ExportNewEngine() *engine { return newEngine() }

// Snapshot returns a copy of the current event buffer. For testing.
func (e *engine) Snapshot() []pendingEvent { return e.snapshot() }

// FlushAll delivers all buffered events and waits for completion. For testing.
func (e *engine) FlushAll(ctx context.Context) error {
	return e.flush(ctx, e.snapshot())
}

func (e *engine) HandlerStart()                                 { e.handlerStart() }
func (e *engine) HandlerDone()                                  { e.handlerDone() }
func (e *engine) HandlerDoneCall()                              { e.handlerDoneCall() }
func (e *engine) WaitForHandlersIdle(ctx context.Context) error { return e.waitForHandlersIdle(ctx) }
func (e *engine) RegisterHandler(gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	e.registerHandler(gvr, ns, sel)
}

func ExportTrackedHandler(h cache.ResourceEventHandler, e *engine) cache.ResourceEventHandler {
	return &trackedHandler{inner: h, eng: e}
}
func ExportInFlightHandlers(e *engine) *int64 { return &e.inFlightHandlers }
func ExportPendingCalls(e *engine) *int64     { return &e.pendingCalls }

var (
	GVRFromPath        = gvrFromPath
	ObjectNameFromPath = objectNameFromPath
)

func ExportHandlerEntryCount(e *engine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.handlerEntries)
}

func ExportNewDskSharedIndexInformer(si cache.SharedIndexInformer, e *engine, gvr schema.GroupVersionResource, ns string, sel labels.Selector) cache.SharedIndexInformer {
	return &dskSharedIndexInformer{SharedIndexInformer: si, eng: e, gvr: gvr, namespace: ns, selector: sel}
}
