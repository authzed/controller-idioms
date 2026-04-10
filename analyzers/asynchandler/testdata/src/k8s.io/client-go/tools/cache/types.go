package cache

type ResourceEventHandler interface {
	OnAdd(obj any, isInInitialList bool)
	OnUpdate(oldObj, newObj any)
	OnDelete(obj any)
}

type ResourceEventHandlerFuncs struct {
	AddFunc    func(obj any)
	UpdateFunc func(oldObj, newObj any)
	DeleteFunc func(obj any)
}

func (r ResourceEventHandlerFuncs) OnAdd(obj any, isInInitialList bool) {}
func (r ResourceEventHandlerFuncs) OnUpdate(oldObj, newObj any)         {}
func (r ResourceEventHandlerFuncs) OnDelete(obj any)                    {}

type ResourceEventHandlerDetailedFuncs struct {
	AddFunc    func(obj any, isInInitialList bool)
	UpdateFunc func(oldObj, newObj any)
	DeleteFunc func(obj any, tombstone bool)
}

func (r ResourceEventHandlerDetailedFuncs) OnAdd(obj any, isInInitialList bool) {}
func (r ResourceEventHandlerDetailedFuncs) OnUpdate(oldObj, newObj any)         {}
func (r ResourceEventHandlerDetailedFuncs) OnDelete(obj any)                    {}

type ResourceEventHandlerRegistration interface{}

type SharedIndexInformer interface {
	AddEventHandler(handler ResourceEventHandler) (ResourceEventHandlerRegistration, error)
}
