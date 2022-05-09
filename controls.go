package libctrl

import (
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

//counterfeiter:generate -o ./fake . ControlDone
type ControlDone interface {
	Done()
}

//counterfeiter:generate -o ./fake . ControlRequeueAfter
type ControlRequeueAfter interface {
	RequeueAfter(duration time.Duration)
}

//counterfeiter:generate -o ./fake . ControlRequeue
type ControlRequeue interface {
	Requeue()
}

//counterfeiter:generate -o ./fake . ControlRequeueErr
type ControlRequeueErr interface {
	ControlRequeue
	RequeueErr(err error)
}

//counterfeiter:generate -o ./fake . ControlRequeueAPIErr
type ControlRequeueAPIErr interface {
	ControlDone
	ControlRequeue
	ControlRequeueAfter
	RequeueAPIErr(err error)
}

//counterfeiter:generate -o ./fake . ControlDoneRequeue
type ControlDoneRequeue interface {
	ControlDone
	ControlRequeue
}

//counterfeiter:generate -o ./fake . ControlDoneRequeueAfter
type ControlDoneRequeueAfter interface {
	ControlDone
	ControlRequeueAfter
}

//counterfeiter:generate -o ./fake . ControlDoneRequeueErr
type ControlDoneRequeueErr interface {
	ControlDone
	ControlRequeueErr
}

//counterfeiter:generate -o ./fake . ControlAll
type ControlAll interface {
	ControlDone
	ControlRequeue
	ControlRequeueAfter
	ControlRequeueErr
	ControlRequeueAPIErr
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

func (c HandlerControls) RequeueErr(err error) {
	utilruntime.HandleError(err)
	c.requeue()
}

func (c HandlerControls) RequeueAPIErr(err error) {
	retry, after := ShouldRetry(err)
	if retry && after > 0 {
		c.RequeueAfter(after)
	}
	if retry {
		c.Requeue()
	}
	c.Done()
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
