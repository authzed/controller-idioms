package libctrl

import (
	"context"
	"sync"
)

// ParallelHandler runs all child handlers in parallel before running the next
// handler. Note that if you want context values to propagate, child handlers
// should only fill existing pointers in the context and not add new values.
type ParallelHandler struct {
	children []Handler
}

var _ Handler = &ParallelHandler{}

func (e *ParallelHandler) Handle(ctx context.Context) {
	var g sync.WaitGroup
	for _, c := range e.children {
		c := c
		g.Add(1)
		go func() {
			// TODO: what happens to the key if one child requeues and the other calls done()?
			c.Handle(ctx)
			g.Done()
		}()
	}
	g.Wait()
}

func NewParallelHandler(children ...Handler) *ParallelHandler {
	return &ParallelHandler{
		children: children,
	}
}
