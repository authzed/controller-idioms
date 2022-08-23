package middleware

import (
	"context"
	"encoding/base64"
	"math/rand"

	"k8s.io/klog/v2"

	"github.com/authzed/ktrllib/handler"
)

func NewSyncID(size uint8) string {
	buf := make([]byte, size)
	rand.Read(buf) // nolint
	str := base64.StdEncoding.EncodeToString(buf)
	return str[:size]
}

func NewHandlerLoggingMiddleware(level int) Middleware {
	return MakeMiddleware(HandlerLoggingMiddleware(level))
}

func HandlerLoggingMiddleware(level int) HandlerMiddleware {
	return func(in handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			logger := klog.FromContext(ctx)
			logger = logger.WithValues("handler", in.ID())
			logger.V(level).Info("entering handler")
			in.Handle(ctx)
		}, in.ID())
	}
}
