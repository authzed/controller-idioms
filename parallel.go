package libctrl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/authzed/controller-idioms/handler"
)

// Parallel creates a new handler.Builder that runs a set of handler.Builder in
// parallel
func Parallel(children ...handler.Builder) handler.Builder {
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, string(c(handler.NoopHandler).ID()))
	}
	return func(next ...handler.Handler) handler.Handler {
		return handler.NewHandler(handler.ContextHandlerFunc(func(ctx context.Context) {
			var g sync.WaitGroup
			for _, c := range children {
				c := c
				g.Add(1)
				go func() {
					c(handler.NoopHandler).Handle(ctx)
					g.Done()
				}()
			}
			g.Wait()
			handler.Handlers(next).MustOne().Handle(ctx)
		}), handler.Key(fmt.Sprintf("parallel[%s]", strings.Join(ids, ","))))
	}
}

var _ BuilderComposer = Parallel

func ParallelWithMiddleware(middleware ...Middleware) BuilderComposer {
	return WithMiddleware(Parallel, middleware...)
}
