package libctrl

import (
	"context"
	"fmt"

	"github.com/authzed/controller-idioms/handler"
)

// ComponentContextHandler fills the value for a ContextKey with the result of
// fetching a component.
type ComponentContextHandler[K KubeObject] struct {
	owner     fmt.Stringer
	ctxKey    SettableContext[[]K]
	component *Component[K]
	next      handler.ContextHandler
}

func NewComponentContextHandler[K KubeObject](contextKey SettableContext[[]K], component *Component[K], owner fmt.Stringer, next handler.ContextHandler) *ComponentContextHandler[K] {
	return &ComponentContextHandler[K]{
		owner:     owner,
		ctxKey:    contextKey,
		component: component,
		next:      next,
	}
}

func (h *ComponentContextHandler[K]) Handle(ctx context.Context) {
	ctx = h.ctxKey.WithValue(ctx, h.component.List(h.owner))
	h.next.Handle(ctx)
}
