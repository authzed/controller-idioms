package libctrl

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/authzed/ktrllib/handler"
)

// ComponentContextHandler fills the value for a ContextKey with the result of
// fetching a component.
type ComponentContextHandler[K metav1.Object] struct {
	HandlerControls
	owner     fmt.Stringer
	ctxKey    SettableContext[[]K]
	component *Component[K]
	next      handler.ContextHandler
}

func NewComponentContextHandler[K metav1.Object](ctrls HandlerControls, contextKey SettableContext[[]K], component *Component[K], owner fmt.Stringer, next handler.ContextHandler) *ComponentContextHandler[K] {
	return &ComponentContextHandler[K]{
		HandlerControls: ctrls,
		owner:           owner,
		ctxKey:          contextKey,
		component:       component,
		next:            next,
	}
}

func (h *ComponentContextHandler[K]) Handle(ctx context.Context) {
	ctx = h.ctxKey.WithValue(ctx, h.component.List(h.owner))
	h.next.Handle(ctx)
}
