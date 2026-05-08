package example

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

func SyncHandler(q workqueue.Interface) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			q.Add(obj) // ok: synchronous
		},
	}
}

func AsyncHandler(q workqueue.Interface) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			go func() { // want "go statement in event handler"
				q.Add(obj)
			}()
		},
	}
}

// Task 2: Positive test cases

func AsyncDirectGo(q workqueue.Interface) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			go q.Add(obj) // want "go statement in event handler"
		},
	}
}

type fakeInformer struct{}

func (fakeInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func DirectAddEventHandler(inf fakeInformer, q workqueue.Interface) {
	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			go q.Add(obj) // want "go statement in event handler"
		},
	})
}

func NestedGo(q workqueue.Interface) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			go func() { // want "go statement in event handler"
				helper(q, obj)
			}()
		},
	}
}

func helper(q workqueue.Interface, obj any) {
	q.Add(obj)
}

// ResourceEventHandlerDetailedFuncs with go stmt — should report.
func AsyncDetailedHandler(q workqueue.Interface) cache.ResourceEventHandlerDetailedFuncs {
	return cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			go q.Add(obj) // want "go statement in event handler"
		},
	}
}

// Sync DetailedFuncs — no diagnostic.
func SyncDetailedHandler(q workqueue.Interface) cache.ResourceEventHandlerDetailedFuncs {
	return cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			q.Add(obj)
		},
	}
}

// Struct implementing ResourceEventHandler with go stmt in OnAdd — should report.
type asyncHandlerImpl struct {
	q workqueue.Interface
}

func (h *asyncHandlerImpl) OnAdd(obj any, isInInitialList bool) {
	go h.q.Add(obj) // want "go statement in event handler"
}

func (h *asyncHandlerImpl) OnUpdate(oldObj, newObj any) {
	h.q.Add(newObj) // ok: synchronous
}

func (h *asyncHandlerImpl) OnDelete(obj any) {
	h.q.Add(obj) // ok: synchronous
}

// Struct implementing ResourceEventHandler with sync methods — no diagnostic.
type syncHandlerImpl struct {
	q workqueue.Interface
}

func (h *syncHandlerImpl) OnAdd(obj any, isInInitialList bool) {
	h.q.Add(obj)
}

func (h *syncHandlerImpl) OnUpdate(oldObj, newObj any) {
	h.q.Add(newObj)
}

func (h *syncHandlerImpl) OnDelete(obj any) {
	h.q.Add(obj)
}

// Task 3: Negative test cases

// Synchronous Add on all three fields — no diagnostic.
func SyncAllFields(q workqueue.Interface) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			q.Add(obj)
		},
		UpdateFunc: func(oldObj, newObj any) {
			q.Add(newObj)
		},
		DeleteFunc: func(obj any) {
			q.Add(obj)
		},
	}
}

// Go statement outside a handler — no diagnostic.
func NotAHandler(q workqueue.Interface) {
	go q.Add("key")
}

// ResourceEventHandlerFuncs with nil fields — no diagnostic.
func NilFields() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{}
}
