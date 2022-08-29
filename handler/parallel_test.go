package handler

import "context"

func ExampleParallel() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// These handlers run in parallel, so their contexts are independent
	// i.e. FirstStage.next and SecondStage.next are both NoopHandlers
	// Any work that is done that needs to be used later on should use
	// typedctx.Boxed contexts so that the parallel steps can "fill in"
	// a predefined space.
	Parallel(
		firstStageBuilder,
		secondStageBuilder,
	).Handler("firstAndSecond").Handle(ctx)

	// Output: the second step
	// the first step
}
