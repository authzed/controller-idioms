package middleware

import (
	"context"
	"encoding/base64"
	"math/rand"

	"k8s.io/klog/v2"

	"github.com/authzed/controller-idioms"
	"github.com/authzed/controller-idioms/handler"
)

var CtxSyncID = libctrl.NewContextKey[string]()

func NewSyncID(size uint8) string {
	buf := make([]byte, size)
	rand.Read(buf) // nolint
	str := base64.StdEncoding.EncodeToString(buf)
	return str[:size]
}

func SyncIDMiddleware(in handler.Handler) handler.Handler {
	return handler.NewHandlerFromFunc(func(ctx context.Context) {
		_, ok := CtxSyncID.Value(ctx)
		if !ok {
			ctx = CtxSyncID.WithValue(ctx, NewSyncID(5))
		}
		in.Handle(ctx)
	}, in.ID())
}

func KlogMiddleware(controllerName string, ref klog.ObjectRef) libctrl.HandlerMiddleware {
	return func(in handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			klog.V(4).InfoS("entering handler", "controller", controllerName, "syncID", CtxSyncID.MustValue(ctx), "object", ref, "handler", in.ID())
			in.Handle(ctx)
		}, in.ID())
	}
}

func NewHandlerLoggingMiddleware(level int) libctrl.Middleware {
	return libctrl.MakeMiddleware(HandlerLoggingMiddleware(level))
}

func HandlerLoggingMiddleware(level int) libctrl.HandlerMiddleware {
	return func(in handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			logger := klog.FromContext(ctx)
			logger = logger.WithValues("handler", in.ID())
			logger.V(level).Info("entering handler")
			in.Handle(ctx)
		}, in.ID())
	}
}
