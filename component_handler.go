package libctrl

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	"github.com/authzed/controller-idioms/handler"
)

// ComponentContextHandler fills the value for a ContextKey with the result of
// fetching a component.
type ComponentContextHandler[K KubeObject] struct {
	owner     MustValueContext[types.NamespacedName]
	ctxKey    SettableContext[[]K]
	component *Component[K]
	next      handler.ContextHandler
}

func NewComponentContextHandler[K KubeObject](contextKey SettableContext[[]K], component *Component[K], owner MustValueContext[types.NamespacedName], next handler.ContextHandler) *ComponentContextHandler[K] {
	return &ComponentContextHandler[K]{
		owner:     owner,
		ctxKey:    contextKey,
		component: component,
		next:      next,
	}
}

func (h *ComponentContextHandler[K]) Handle(ctx context.Context) {
	ctx = h.ctxKey.WithValue(ctx, h.component.List(ctx, h.owner.MustValue(ctx)))
	h.next.Handle(ctx)
}
