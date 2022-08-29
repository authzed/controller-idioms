package handler

import (
	"context"
	"time"
)

func ExampleParallel() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// slow down the first stage to get a deterministic output
	slowFirstStage := func(next ...Handler) Handler {
		time.Sleep(5 * time.Millisecond)
		return firstStageBuilder(next...)
	}

	// These handlers run in parallel, so their contexts are independent
	// i.e. FirstStage.next and SecondStage.next are both NoopHandlers
	// Any work that is done that needs to be used later on should use
	// typedctx.Boxed contexts so that the parallel steps can "fill in"
	// a predefined space.
	Parallel(
		slowFirstStage,
		secondStageBuilder,
	).Handler("firstAndSecond").Handle(ctx)

	// Output: the second step
	// the first step
}
