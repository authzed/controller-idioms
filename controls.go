package libctrl

import (
	"context"
	"time"

	"k8s.io/klog/v2"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

type HandlerControlContext struct {
	*ContextKey[ControlAll]
}

func (h HandlerControlContext) Done(ctx context.Context) {
	h.MustValue(ctx).Done()
}

func (h HandlerControlContext) RequeueAfter(ctx context.Context, duration time.Duration) {
	h.MustValue(ctx).RequeueAfter(duration)
}

func (h HandlerControlContext) Requeue(ctx context.Context) {
	h.MustValue(ctx).Requeue()
}

func (h HandlerControlContext) RequeueErr(ctx context.Context, err error) {
	klog.FromContext(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after error")
	h.MustValue(ctx).RequeueErr(err)
}

func (h HandlerControlContext) RequeueAPIErr(ctx context.Context, err error) {
	klog.FromContext(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after api error")
	h.MustValue(ctx).RequeueAPIErr(err)
}

func NewHandlerControls(done func(), requeueAfter func(time.Duration)) *HandlerControls {
	return &HandlerControls{
		done:         done,
		requeueAfter: requeueAfter,
	}
}

//counterfeiter:generate -o ./fake . ControlAll
type ControlAll interface {
	Done()
	RequeueAfter(duration time.Duration)
	Requeue()
	RequeueErr(err error)
	RequeueAPIErr(err error)
}

type HandlerControls struct {
	done         func()
	requeueAfter func(duration time.Duration)
}

func (c *HandlerControls) Done() {
	c.done()
}

func (c *HandlerControls) RequeueAfter(duration time.Duration) {
	c.requeueAfter(duration)
}

func (c *HandlerControls) Requeue() {
	c.requeueAfter(0)
}

func (c *HandlerControls) RequeueErr(err error) {
	c.requeueAfter(0)
}

func (c *HandlerControls) RequeueAPIErr(err error) {
	retry, after := ShouldRetry(err)
	if retry && after > 0 {
		c.RequeueAfter(after)
	}
	if retry {
		c.Requeue()
	}
	c.Done()
}
