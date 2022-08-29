package handler

import "context"

func ExampleChain() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Chain(
		firstStageBuilder,
		secondStageBuilder,
	).Handler("firstThenSecond").Handle(ctx)

	// Output: the first step
	// the second step
}
