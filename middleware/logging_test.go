package middleware

import (
	"context"

	"k8s.io/klog/v2"

	"github.com/authzed/controller-idioms/handler"
)

func ExampleHandlerLoggingMiddleware() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := klog.LoggerWithValues(klog.Background(),
		"syncID", NewSyncID(5),
		"controller", "my-controller",
	)
	ctx = klog.NewContext(ctx, logger)

	ChainWithMiddleware(NewHandlerLoggingMiddleware(4))(
		firstStage,
		secondStage,
	).Handler("chained").Handle(ctx)
	// Output:
}

func firstStage(next ...handler.Handler) handler.Handler {
	return handler.NewHandlerFromFunc(func(ctx context.Context) {
		klog.FromContext(ctx).V(4).Info("first")
		handler.Handlers(next).MustOne().Handle(ctx)
	}, "first")
}

func secondStage(next ...handler.Handler) handler.Handler {
	return handler.NewHandlerFromFunc(func(ctx context.Context) {
		klog.FromContext(ctx).V(4).Info("second")
		handler.Handlers(next).MustOne().Handle(ctx)
	}, "second")
}
