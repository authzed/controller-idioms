package libctrl

import (
	"context"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// Handler is the interface for a "chunk" of reconciliation. It either returns,
// often by adjusting the current key's place in the queue (i.e. via requeue or
// done) or calls the Next handler in the chain.
type Handler interface {
	Handle(context.Context)
}

type HandlerFunc func(ctx context.Context)

func (f HandlerFunc) Handle(ctx context.Context) {
	f(ctx)
}

var NoopHandler HandlerFunc = func(ctx context.Context) {}

type ControlDone interface {
	Done()
}

type ControlRequeueAfter interface {
	RequeueAfter(duration time.Duration)
}

type ControlRequeue interface {
	Requeue()
}

type ControlRequeueErr interface {
	RequeueErr(err error)
}

type ControlDoneRequeue interface {
	Done()
	Requeue()
}

type HandlerControls struct {
	done         func()
	requeue      func()
	requeueAfter func(duration time.Duration)
}

func (c HandlerControls) Done() {
	c.done()
}

func (c HandlerControls) RequeueAfter(duration time.Duration) {
	c.requeueAfter(duration)
}

func (c HandlerControls) Requeue() {
	c.requeue()
}

// TODO: variant that understands kube api return codes
func (c HandlerControls) RequeueErr(err error) {
	utilruntime.HandleError(err)
	c.requeue()
}

type ControlOpt func(*HandlerControls)

func WithDone(doneFunc func()) ControlOpt {
	return func(c *HandlerControls) {
		c.done = doneFunc
	}
}

func WithRequeue(requeueFunc func()) ControlOpt {
	return func(c *HandlerControls) {
		c.requeue = requeueFunc
	}
}

func WithRequeueImmediate(requeueFunc func(duration time.Duration)) ControlOpt {
	return func(c *HandlerControls) {
		c.requeue = func() {
			requeueFunc(0)
		}
	}
}

func WithRequeueAfter(requeueAfterFunc func(duration time.Duration)) ControlOpt {
	return func(c *HandlerControls) {
		c.requeueAfter = requeueAfterFunc
		c.requeue = func() {
			requeueAfterFunc(0)
		}
	}
}

func HandlerControlsWith(opts ...ControlOpt) HandlerControls {
	ctrls := HandlerControls{}
	for _, o := range opts {
		o(&ctrls)
	}
	return ctrls
}
