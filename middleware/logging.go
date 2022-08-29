package middleware

import (
	"context"
	"encoding/base64"
	"math/rand"

	"k8s.io/klog/v2"

	"github.com/authzed/controller-idioms/handler"
)

// NewSyncID returns a random string of length `size` that can be added to log
// messages. This is useful for identifying which logs came from which iteration
// of a reconciliation loop in a controller.
func NewSyncID(size uint8) string {
	buf := make([]byte, size)
	rand.Read(buf) // nolint
	str := base64.StdEncoding.EncodeToString(buf)
	return str[:size]
}

// NewHandlerLoggingMiddleware creates a new HandlerLoggingMiddleware for a
// particular klog log level.
func NewHandlerLoggingMiddleware(level int) Middleware {
	return MakeMiddleware(HandlerLoggingMiddleware(level))
}

// HandlerLoggingMiddleware logs on entry to a handler. It uses the klog logger
// found in the context.
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
