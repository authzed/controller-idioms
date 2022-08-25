package component

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/typedctx"
)

// ContextHandler fills the value for a context.Key with the result of
// fetching a component.
type ContextHandler[K KubeObject] struct {
	owner     typedctx.MustValueContext[types.NamespacedName]
	ctxKey    typedctx.SettableContext[[]K]
	component *Component[K]
	next      handler.ContextHandler
}

// NewComponentContextHandler creates a new ContextHandler.
func NewComponentContextHandler[K KubeObject](contextKey typedctx.SettableContext[[]K], component *Component[K], owner typedctx.MustValueContext[types.NamespacedName], next handler.ContextHandler) *ContextHandler[K] {
	return &ContextHandler[K]{
		owner:     owner,
		ctxKey:    contextKey,
		component: component,
		next:      next,
	}
}

func (h *ContextHandler[K]) Handle(ctx context.Context) {
	ctx = h.ctxKey.WithValue(ctx, h.component.List(ctx, h.owner.MustValue(ctx)))
	h.next.Handle(ctx)
}
