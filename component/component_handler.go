package component

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	"github.com/authzed/ktrllib/handler"
	"github.com/authzed/ktrllib/typedctx"
)

// ContextHandler fills the value for a Key with the result of
// fetching a component.
type ContextHandler[K KubeObject] struct {
	owner     typedctx.MustValueContext[types.NamespacedName]
	ctxKey    typedctx.SettableContext[[]K]
	component *Component[K]
	next      handler.ContextHandler
}

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
