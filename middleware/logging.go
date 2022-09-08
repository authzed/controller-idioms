package middleware

import (
	"context"
	"encoding/base64"
	"math/rand"

	"github.com/authzed/controller-idioms/handler"
	"github.com/go-logr/logr"
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
// particular logr log level.
func NewHandlerLoggingMiddleware(level int) Middleware {
	return MakeMiddleware(HandlerLoggingMiddleware(level))
}

// HandlerLoggingMiddleware logs on entry to a handler. It uses the logr logger
// found in the context.
func HandlerLoggingMiddleware(level int) HandlerMiddleware {
	return func(in handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			logger := logr.FromContextOrDiscard(ctx)
			logger = logger.WithValues("handler", in.ID())
			logger.V(level).Info("entering handler")
			in.Handle(ctx)
		}, in.ID())
	}
}
