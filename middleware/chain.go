package middleware

import "github.com/authzed/ktrllib/handler"

// Chain chains a set of BoxBuilder together
func Chain(children ...handler.Builder) handler.Builder {
	return func(...handler.Handler) handler.Handler {
		next := handler.NoopHandler
		for i := len(children) - 1; i >= 0; i-- {
			next = children[i](next)
		}
		return next
	}
}

var _ BuilderComposer = Chain

func ChainWithMiddleware(middleware ...Middleware) BuilderComposer {
	return WithMiddleware(Chain, middleware...)
}
