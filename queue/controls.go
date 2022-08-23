package queue

import (
	"context"
	"time"

	"k8s.io/klog/v2"

	"github.com/authzed/ktrllib/typedctx"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

type OperationsContext struct {
	*typedctx.Key[Interface]
}

func NewQueueOperationsCtx() OperationsContext {
	return OperationsContext{}
}

func (h OperationsContext) Done(ctx context.Context) {
	h.MustValue(ctx).Done()
}

func (h OperationsContext) RequeueAfter(ctx context.Context, duration time.Duration) {
	h.MustValue(ctx).RequeueAfter(duration)
}

func (h OperationsContext) Requeue(ctx context.Context) {
	h.MustValue(ctx).Requeue()
}

func (h OperationsContext) RequeueErr(ctx context.Context, err error) {
	klog.FromContext(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after error")
	h.MustValue(ctx).RequeueErr(err)
}

func (h OperationsContext) RequeueAPIErr(ctx context.Context, err error) {
	klog.FromContext(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after api error")
	h.MustValue(ctx).RequeueAPIErr(err)
}

func NewOperations(done func(), requeueAfter func(time.Duration)) *Operations {
	return &Operations{
		done:         done,
		requeueAfter: requeueAfter,
	}
}

//counterfeiter:generate -o ./fake . Interface
type Interface interface {
	Done()
	RequeueAfter(duration time.Duration)
	Requeue()
	RequeueErr(err error)
	RequeueAPIErr(err error)
}

type Operations struct {
	done         func()
	requeueAfter func(duration time.Duration)
}

func (c *Operations) Done() {
	c.done()
}

func (c *Operations) RequeueAfter(duration time.Duration) {
	c.requeueAfter(duration)
}

func (c *Operations) Requeue() {
	c.requeueAfter(0)
}

func (c *Operations) RequeueErr(err error) {
	c.requeueAfter(0)
}

func (c *Operations) RequeueAPIErr(err error) {
	retry, after := ShouldRetry(err)
	if retry && after > 0 {
		c.RequeueAfter(after)
	}
	if retry {
		c.Requeue()
	}
	c.Done()
}
