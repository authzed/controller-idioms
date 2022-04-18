package libctrl

import (
	"github.com/authzed/controller-idioms/handler"
)

// HandlerMiddleware returns a new (wrapped) Handler given a Handler
type HandlerMiddleware func(handler.Handler) handler.Handler

// BuilderMiddleware returns a new (wrapped) Builder given a Builder
type BuilderMiddleware func(handler.Builder) handler.Builder

// BuilderComposer is a function that composes sets of handler.Builder into
// one handler.Builder, see `Chain` and `Parallel`.
type BuilderComposer func(builder ...handler.Builder) handler.Builder

// Middleware operates on BuilderComposer (to wrap all underlying builders)
type Middleware func(BuilderComposer) BuilderComposer

// MakeMiddleware generates the corresponding Middleware for HandlerMiddleware
func MakeMiddleware(w HandlerMiddleware) Middleware {
	builderWrapper := MakeBuilderMiddleware(w)
	return func(f BuilderComposer) BuilderComposer {
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
func WithMiddleware(composer BuilderComposer, middleware ...Middleware) BuilderComposer {
	out := composer
	for _, m := range middleware {
		out = m(out)
	}
	return out
}
