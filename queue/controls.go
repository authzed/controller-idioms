// Package queue provides helpers for working with client-go's `workqueues`.
//
// `queue.OperationsContext` can be used from within a `Handler` to control
// the behavior of the queue that has called the handler.
//
// The queue operations are:
//
// - Done (stop processing the current key)
// - Requeue (requeue the current key)
// - RequeueAfter (wait for some period of time before requeuing the current key)
// - ReqeueueErr (record an error and requeue)
// - RequeueAPIError (requeue after waiting according to the priority and fairness response from the apiserver)
//
// If calling these controls from a handler, it's important to `return`
// immediately so that the handler does not continue processing a key that
// the queue thinks has stopped.
package queue

import (
	"context"
	"time"

	"github.com/go-logr/logr"

	"github.com/authzed/controller-idioms/typedctx"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

// OperationsContext is like Interface, but fetches the object from a context.
type OperationsContext struct {
	*typedctx.Key[Interface]
}

// NewQueueOperationsCtx returns a new OperationsContext
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
	logr.FromContextOrDiscard(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after error")
	h.MustValue(ctx).RequeueErr(err)
}

func (h OperationsContext) RequeueAPIErr(ctx context.Context, err error) {
	logr.FromContextOrDiscard(ctx).V(4).WithCallDepth(3).Error(err, "requeueing after api error")
	h.MustValue(ctx).RequeueAPIErr(err)
}

func (h OperationsContext) Error(ctx context.Context) error {
	return h.MustValue(ctx).Error()
}

func NewOperations(done func(), requeueAfter func(time.Duration), cancel context.CancelFunc) *Operations {
	return &Operations{
		done:         done,
		requeueAfter: requeueAfter,
		cancel:       cancel,
	}
}

// Interface is the standard queue control interface
//
//counterfeiter:generate -o ./fake/zz_generated.go . Interface
type Interface interface {
	Done()
	RequeueAfter(duration time.Duration)
	Requeue()
	RequeueErr(err error)
	RequeueAPIErr(err error)
	Error() error
}

// Operations deals with the current queue key and provides controls for
// requeueing or stopping reconciliation.
type Operations struct {
	done         func()
	requeueAfter func(duration time.Duration)
	cancel       context.CancelFunc
	err          error
}

// Done marks the current key as finished. Note that processing should stop
// as soon as possible after calling `Done`, since marking it as done frees the
// queue to potentially process the same key again.
func (c *Operations) Done() {
	defer c.cancel()
	c.done()
}

// RequeueAfter requeues the current key after duration.
func (c *Operations) RequeueAfter(duration time.Duration) {
	defer c.cancel()
	c.requeueAfter(duration)
}

// Requeue requeues the current key immediately.
func (c *Operations) Requeue() {
	defer c.cancel()
	c.requeueAfter(0)
}

// RequeueErr sets err on the object and requeues the current key.
func (c *Operations) RequeueErr(err error) {
	defer c.cancel()
	c.err = err
	c.requeueAfter(0)
}

// RequeueAPIErr checks to see if `err` is a kube api error with retry data.
// If so, it requeues after the wait period, otherwise, it requeues immediately.
func (c *Operations) RequeueAPIErr(err error) {
	defer c.cancel()
	c.err = err
	retry, after := ShouldRetry(err)
	if retry && after > 0 {
		c.RequeueAfter(after)
	}
	if retry {
		c.Requeue()
	}
	c.Done()
}

// Error returns the last recorded error, if any
func (c *Operations) Error() error {
	return c.err
}
