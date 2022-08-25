package middleware

import (
	"github.com/authzed/controller-idioms/handler"
)

// HandlerMiddleware returns a new (wrapped) Handler given a Handler
type HandlerMiddleware func(handler.Handler) handler.Handler

// BuilderMiddleware returns a new (wrapped) Builder given a Builder
type BuilderMiddleware func(handler.Builder) handler.Builder

// Middleware operates on BuilderComposer (to wrap all underlying builders)
type Middleware func(handler.BuilderComposer) handler.BuilderComposer

func ChainWithMiddleware(middleware ...Middleware) handler.BuilderComposer {
	return WithMiddleware(handler.Chain, middleware...)
}

func ParallelWithMiddleware(middleware ...Middleware) handler.BuilderComposer {
	return WithMiddleware(handler.Parallel, middleware...)
}

// MakeMiddleware generates the corresponding Middleware for HandlerMiddleware
func MakeMiddleware(w HandlerMiddleware) Middleware {
	builderWrapper := MakeBuilderMiddleware(w)
	return func(f handler.BuilderComposer) handler.BuilderComposer {
		return func(builder ...handler.Builder) handler.Builder {
			wrapped := make([]handler.Builder, 0, len(builder))
			for _, b := range builder {
				wrapped = append(wrapped, builderWrapper(b))
			}
			return f(wrapped...)
		}
	}
}

// MakeBuilderMiddleware generates the corresponding BuilderMiddleware from a
// HandlerMiddleware.
func MakeBuilderMiddleware(w HandlerMiddleware) BuilderMiddleware {
	return func(in handler.Builder) handler.Builder {
		return func(next ...handler.Handler) handler.Handler {
			return w(in(next...))
		}
	}
}

// WithMiddleware returns a new BuilderComposer with all middleware applied
func WithMiddleware(composer handler.BuilderComposer, middleware ...Middleware) handler.BuilderComposer {
	out := composer
	for _, m := range middleware {
		out = m(out)
	}
	return out
}
