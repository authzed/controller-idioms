package handler

// BuilderComposer is a function that composes sets of handler.Builder into
// one handler.Builder, see `Chain` and `Parallel`.
type BuilderComposer func(builder ...Builder) Builder

// Chain chains a set of handler.Builder together
func Chain(children ...Builder) Builder {
	return func(...Handler) Handler {
		next := NoopHandler
		for i := len(children) - 1; i >= 0; i-- {
			next = children[i](next)
		}
		return next
	}
}

var _ BuilderComposer = Chain
