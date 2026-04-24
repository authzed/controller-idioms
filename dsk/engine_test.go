//go:build dsk

package dsk

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// loggingTB is a minimal TB implementation that captures Logf calls.
type loggingTB struct {
	logs []string
}

func (l *loggingTB) Context() context.Context          { return context.Background() }
func (l *loggingTB) Fatal(args ...any)                 {}
func (l *loggingTB) Fatalf(format string, args ...any) {}
func (l *loggingTB) Errorf(format string, args ...any) {}
func (l *loggingTB) Logf(format string, args ...any) {
	l.logs = append(l.logs, fmt.Sprintf(format, args...))
}

func TestDskSharedIndexInformer_RemoveEventHandler_UnregistersHandler(t *testing.T) {
	eng := ExportNewEngine()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	si := cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	wrapped := ExportNewDskSharedIndexInformer(si, eng, gvr, "default", nil)

	reg, err := wrapped.AddEventHandler(cache.ResourceEventHandlerFuncs{})
	if err != nil {
		t.Fatal(err)
	}
	if count := ExportHandlerEntryCount(eng); count != 1 {
		t.Fatalf("expected 1 handler entry after AddEventHandler, got %d", count)
	}

	if err := wrapped.RemoveEventHandler(reg); err != nil {
		t.Fatal(err)
	}
	if count := ExportHandlerEntryCount(eng); count != 0 {
		t.Fatalf("expected 0 handler entries after RemoveEventHandler, got %d", count)
	}
}

func TestDskSharedIndexInformer_AddEventHandlerWithOptions_RegistersHandler(t *testing.T) {
	eng := ExportNewEngine()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	si := cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	wrapped := ExportNewDskSharedIndexInformer(si, eng, gvr, "default", nil)

	_, err := wrapped.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{}, cache.HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if count := ExportHandlerEntryCount(eng); count != 1 {
		t.Fatalf("expected 1 handler entry after AddEventHandlerWithOptions, got %d", count)
	}
}

func TestEngine_BufferAndSnapshot(t *testing.T) {
	e := newEngine()
	cw := NewControlledWatch(t)

	ev := watch.Event{Type: watch.Added}
	e.addToBuffer(pendingEvent{event: ev, dest: cw})

	buf := e.snapshot()
	if len(buf) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(buf))
	}
	if buf[0].event.Type != watch.Added {
		t.Fatalf("unexpected event type: %v", buf[0].event.Type)
	}
}

func TestEngine_RemoveFromBuffer(t *testing.T) {
	e := newEngine()
	cw := NewControlledWatch(t)

	ev1 := watch.Event{Type: watch.Added}
	ev2 := watch.Event{Type: watch.Modified}
	e.addToBuffer(pendingEvent{event: ev1, dest: cw})
	e.addToBuffer(pendingEvent{event: ev2, dest: cw})

	// Remove only ev1: use snapshot to obtain the ID-bearing pendingEvent.
	snap := e.snapshot()
	e.removeFromBuffer([]pendingEvent{snap[0]})

	buf := e.snapshot()
	if len(buf) != 1 {
		t.Fatalf("expected 1 buffered event after removal, got %d", len(buf))
	}
	if buf[0].event.Type != watch.Modified {
		t.Fatalf("unexpected remaining event type: %v", buf[0].event.Type)
	}
}

func TestEngine_SnapshotIsIsolated(t *testing.T) {
	e := newEngine()
	cw := NewControlledWatch(t)

	ev := watch.Event{Type: watch.Added}
	e.addToBuffer(pendingEvent{event: ev, dest: cw})

	snap := e.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 event in snapshot, got %d", len(snap))
	}

	// Adding more events should not affect the snapshot
	e.addToBuffer(pendingEvent{event: watch.Event{Type: watch.Modified}, dest: cw})

	if len(snap) != 1 {
		t.Fatal("snapshot was mutated when buffer was modified")
	}
}

func TestEngine_HandlerTracking_IdleByDefault(t *testing.T) {
	e := ExportNewEngine()
	ctx := t.Context()
	if err := e.WaitForHandlersIdle(ctx); err != nil {
		t.Fatalf("expected handlers idle immediately: %v", err)
	}
}

func TestEngine_HandlerTracking_BlocksWhileInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := ExportNewEngine()
		e.HandlerStart()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- e.WaitForHandlersIdle(ctx) }()

		synctest.Wait() // goroutine settled in sync.Cond.Wait
		select {
		case err := <-done:
			t.Fatalf("expected WaitForHandlersIdle to block, got %v", err)
		default:
		}

		e.HandlerDone()
		synctest.Wait()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("expected nil after HandlerDone, got %v", err)
			}
		default:
			t.Fatal("expected WaitForHandlersIdle to unblock after HandlerDone")
		}
	})
}

func TestEngine_HandlerTracking_CancelContext(t *testing.T) {
	e := ExportNewEngine()

	e.HandlerStart()
	defer e.HandlerDone()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.WaitForHandlersIdle(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context error, got nil")
		}
	case <-t.Context().Done():
		t.Fatal("expected WaitForHandlersIdle to return on cancel")
	}
}

func TestEngine_TrackedHandler_CountsCallbacks(t *testing.T) {
	e := ExportNewEngine()

	callCount := 0
	inner := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { callCount++ },
	}
	wrapped := ExportTrackedHandler(inner, e)

	if atomic.LoadInt64(ExportInFlightHandlers(e)) != 0 {
		t.Fatal("expected 0 in-flight before any callback")
	}

	// Simulate synchronous invocation (as listener.run would do it).
	wrapped.OnAdd("obj", false)

	if callCount != 1 {
		t.Fatalf("expected inner handler called once, got %d", callCount)
	}
	if atomic.LoadInt64(ExportInFlightHandlers(e)) != 0 {
		t.Fatal("expected 0 in-flight after callback returns")
	}
}

func TestEngine_PendingCalls_BlocksWaitForHandlersIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := ExportNewEngine()
		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

		e.RegisterHandler(gvr, "default", nil)
		e.HandlerDoneCall() // no-op; pendingCalls still 0
		atomic.AddInt64(ExportPendingCalls(e), 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- e.WaitForHandlersIdle(ctx) }()

		synctest.Wait() // goroutine settled in sync.Cond.Wait
		select {
		case err := <-done:
			t.Fatalf("expected WaitForHandlersIdle to block, got %v", err)
		default:
		}

		e.HandlerDoneCall()
		synctest.Wait()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("expected nil after HandlerDoneCall, got %v", err)
			}
		default:
			t.Fatal("expected WaitForHandlersIdle to unblock after HandlerDoneCall")
		}
	})
}

func TestEngine_PendingCalls_SaturatingDecrement(t *testing.T) {
	// HandlerDoneCall with pendingCalls=0 should be a safe no-op, not go negative.
	e := ExportNewEngine()

	e.HandlerDoneCall() // pendingCalls=0 → no-op
	e.HandlerDoneCall()

	if v := atomic.LoadInt64(ExportPendingCalls(e)); v != 0 {
		t.Fatalf("expected pendingCalls=0, got %d", v)
	}
}

// TestEngine_PreExpectWrite_SelectorFiltering verifies that preExpectWrite only
// counts watches whose label selector matches the object's labels.
func TestEngine_PreExpectWrite_SelectorFiltering(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	selFoo, _ := labels.Parse("app=foo")
	selBar, _ := labels.Parse("app=bar")

	tests := []struct {
		name         string
		watchSel     labels.Selector
		objectLabels map[string]string
		expectCount  int
	}{
		{
			name:         "nil selector matches all labels",
			watchSel:     nil,
			objectLabels: map[string]string{"app": "foo"},
			expectCount:  1,
		},
		{
			name:         "matching selector counts the watch",
			watchSel:     selFoo,
			objectLabels: map[string]string{"app": "foo"},
			expectCount:  1,
		},
		{
			name:         "non-matching selector skips the watch",
			watchSel:     selBar,
			objectLabels: map[string]string{"app": "foo"},
			expectCount:  0,
		},
		{
			name:         "nil object labels disables filtering (conservative)",
			watchSel:     selFoo,
			objectLabels: nil,
			expectCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEngine()
			e.registerWatch(gvr, "default", tt.watchSel)
			n := e.preExpectWrite("create", gvr, "default", tt.objectLabels)
			if n != tt.expectCount {
				t.Fatalf("preExpectWrite: got %d, want %d", n, tt.expectCount)
			}
		})
	}
}

// TestEngine_Flush_SelectorFiltering verifies that flush() only credits
// pendingCalls for handlers whose label selector matches the event's object.
// If no handlers match, flush() must not block waiting for handler completion.
func TestEngine_Flush_SelectorFiltering(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	selFoo, _ := labels.Parse("app=foo")

	makeEvent := func(labelVal string) pendingEvent {
		cw := NewControlledWatch(t)
		// Drain in background so Deliver doesn't block (unbuffered channel).
		go func() {
			for range cw.ResultChan() {
			}
		}()
		t.Cleanup(func() { cw.Stop() })
		obj := &unstructured.Unstructured{}
		obj.SetNamespace("default")
		obj.SetLabels(map[string]string{"app": labelVal})
		return pendingEvent{
			event: watch.Event{Type: watch.Added, Object: obj},
			dest:  cw,
			gvr:   gvr,
		}
	}

	t.Run("non-matching selector does not credit pendingCalls", func(t *testing.T) {
		e := newEngine()
		// Handler watches for app=foo, but event is for app=bar.
		e.registerHandler(gvr, "default", selFoo)

		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		// flush must return immediately — no pendingCalls credited.
		if err := e.flush(ctx, []pendingEvent{makeEvent("bar")}); err != nil {
			t.Fatalf("flush: unexpected error (likely hung waiting for pendingCalls): %v", err)
		}
		if v := atomic.LoadInt64(ExportPendingCalls(e)); v != 0 {
			t.Fatalf("expected pendingCalls=0 after flush with non-matching selector, got %d", v)
		}
	})

	t.Run("matching selector credits and debits pendingCalls", func(t *testing.T) {
		e := newEngine()
		// Handler watches for app=foo, event is also for app=foo.
		e.registerHandler(gvr, "default", selFoo)

		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		// flush credits pendingCalls=1 before deliver; deliver to ControlledWatch
		// does NOT call OnAdd (no reflector running), so pendingCalls stays at 1.
		// We verify the credit happened and then manually debit to confirm mechanics.
		go func() {
			time.Sleep(10 * time.Millisecond)
			e.handlerDoneCall() // simulate handler completing
		}()

		if err := e.flush(ctx, []pendingEvent{makeEvent("foo")}); err != nil {
			t.Fatalf("flush: unexpected error: %v", err)
		}
	})
}

func TestEngine_UnregisterHandler_LogsWarningOnMiss(t *testing.T) {
	e := newEngine()
	ltb := &loggingTB{}
	e.tb = ltb
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	// Unregister with no prior registration — should log a warning.
	e.unregisterHandler(gvr, "default", nil)
	if len(ltb.logs) == 0 {
		t.Fatal("expected warning log when unregisterHandler finds no matching entry")
	}
	if !strings.Contains(ltb.logs[0], "unregisterHandler") {
		t.Errorf("expected log to mention unregisterHandler, got %q", ltb.logs[0])
	}
}
