package libctrl

import (
	"context"
)

// ChainHandler runs all child handlers in series. Note that if you want context
// values to propagate, child handlers should only fill existing pointers in
// the context and not add new values.
type ChainHandler struct {
	handlers []Handler
}

var _ Handler = &ChainHandler{}

func (c *ChainHandler) Handle(ctx context.Context) {
	for _, h := range c.handlers {
		h.Handle(ctx)
	}
}

func NewChainHandler(handlers ...Handler) *ChainHandler {
	return &ChainHandler{
		handlers: handlers,
	}
}
