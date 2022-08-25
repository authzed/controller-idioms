package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Parallel creates a new handler.Builder that runs a set of handler.Builder in
// parallel
func Parallel(children ...Builder) Builder {
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, string(c(NoopHandler).ID()))
	}
	return func(next ...Handler) Handler {
		return NewHandler(ContextHandlerFunc(func(ctx context.Context) {
			var g sync.WaitGroup
			for _, c := range children {
				c := c
				g.Add(1)
				go func() {
					c(NoopHandler).Handle(ctx)
					g.Done()
				}()
			}
			g.Wait()
			Handlers(next).MustOne().Handle(ctx)
		}), Key(fmt.Sprintf("parallel[%s]", strings.Join(ids, ","))))
	}
}

var _ BuilderComposer = Parallel
