package queue

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"

	"github.com/authzed/controller-idioms/handler"
)

func ExampleNewOperations() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]())

	// queue has an object in it
	queue.Add("current_key")
	key, _ := queue.Get()

	// operations are per-key
	operations := NewOperations(func() {
		queue.Done(key)
	}, func(duration time.Duration) {
		queue.AddAfter(key, duration)
	}, cancel)

	// typically called from a handler
	handler.NewHandlerFromFunc(func(_ context.Context) {
		// do some work
		operations.Done()
	}, "example").Handle(ctx)
	fmt.Println(queue.Len())

	operations.Requeue()
	fmt.Println(queue.Len())

	// Output: 0
	// 1
}

func ExampleNewQueueOperationsCtx() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]())

	// queue has an object in it
	queue.Add("current_key")

	key, _ := queue.Get()

	// operations are per-key
	ctxWithQueue := NewQueueOperationsCtx().WithValue(ctx, NewOperations(func() {
		queue.Done(key)
	}, func(duration time.Duration) {
		queue.AddAfter(key, duration)
	}, cancel))

	// queue controls are passed via context
	handler.NewHandlerFromFunc(func(hctx context.Context) {
		// do some work
		NewQueueOperationsCtx().Done(hctx)
	}, "example").Handle(ctxWithQueue)

	fmt.Println(queue.Len())
	// Output: 0
}

// RequeueAPIErr must fire exactly one queue operation per call: the
// server-suggested delay, an immediate requeue, or done — never a
// combination.
func TestOperationsRequeueAPIErrFiresExactlyOnce(t *testing.T) {
	setup := func() (*Operations, *int, *[]time.Duration) {
		var doneCalls int
		var requeues []time.Duration
		ops := NewOperations(
			func() { doneCalls++ },
			func(d time.Duration) { requeues = append(requeues, d) },
			func() {},
		)
		return ops, &doneCalls, &requeues
	}

	t.Run("retryable with server delay requeues after, once", func(t *testing.T) {
		ops, done, requeues := setup()
		ops.RequeueAPIErr(apierrors.NewTooManyRequests("slow down", 7))
		require.Equal(t, []time.Duration{7 * time.Second}, *requeues)
		require.Zero(t, *done)
	})

	t.Run("retryable without delay requeues immediately, once", func(t *testing.T) {
		ops, done, requeues := setup()
		ops.RequeueAPIErr(apierrors.NewInternalError(errors.New("boom")))
		require.Equal(t, []time.Duration{0}, *requeues)
		require.Zero(t, *done)
	})

	t.Run("non-retryable marks done, once", func(t *testing.T) {
		ops, done, requeues := setup()
		ops.RequeueAPIErr(errors.New("permanent"))
		require.Empty(t, *requeues)
		require.Equal(t, 1, *done)
	})
}
