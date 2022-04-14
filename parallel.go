package libctrl

import (
	"context"
	"sync"

	"github.com/authzed/ktrllib/handler"
)

// ParallelHandler runs all child handlers in parallel before running the next
// handler. Note that if you want context values to propagate, child handlers
// should only fill existing pointers in the context and not add new values.
type ParallelHandler struct {
	children []handler.ContextHandler
}

var _ handler.ContextHandler = &ParallelHandler{}

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

func NewParallelHandler(children ...handler.ContextHandler) *ParallelHandler {
	return &ParallelHandler{
		children: children,
	}
}

var ParallelKey handler.Key = "parallel"

func Parallel(children ...handler.Builder) handler.Builder {
	handlers := make([]handler.ContextHandler, 0)
	for _, c := range children {
		handlers = append(handlers, c(handler.NoopHandler))
	}
	return handler.NewNextBuilder(NewParallelHandler(handlers...))
}
