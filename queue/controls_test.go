package queue

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/authzed/controller-idioms/handler"
)

func ExampleNewOperations() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

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
	handler.NewHandlerFromFunc(func(ctx context.Context) {
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

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// queue has an object in it
	queue.Add("current_key")

	key, _ := queue.Get()

	// operations are per-key
	CtxQueue := NewQueueOperationsCtx().WithValue(ctx, NewOperations(func() {
		queue.Done(key)
	}, func(duration time.Duration) {
		queue.AddAfter(key, duration)
	}, cancel))

	// queue controls are passed via context
	handler.NewHandlerFromFunc(func(ctx context.Context) {
		// do some work
		CtxQueue.Done()
	}, "example").Handle(ctx)

	fmt.Println(queue.Len())
	// Output: 0
}
