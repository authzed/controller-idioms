package typedctx

import (
	"context"
	"fmt"

	"github.com/authzed/controller-idioms/handler"
)

func ExampleKey() {
	type ExpensiveComputation struct {
		result string
	}
	var CtxExpensiveObject = NewKey[*ExpensiveComputation]()

	useHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		// fetch the computed value after the computation
		fmt.Println(CtxExpensiveObject.MustValue(ctx).result)
	}, "use")

	// the compute handler performs some computation that we wish to re-use
	computeHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		myComputedExpensiveObject := ExpensiveComputation{result: "computed"}
		ctx = CtxExpensiveObject.WithValue(ctx, &myComputedExpensiveObject)

		useHandler.Handle(ctx)
	}, "compute")

	ctx := context.Background()
	computeHandler.Handle(ctx)
	// Output: computed
}

func ExampleWithDefault() {
	type ExpensiveComputation struct {
		result string
	}
	var CtxExpensiveObject = WithDefault[*ExpensiveComputation](&ExpensiveComputation{result: "pending"})

	useHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		// fetch the computed value after the computation
		fmt.Println(CtxExpensiveObject.MustValue(ctx).result)
	}, "use")

	// the compute handler performs some computation that we wish to re-use
	computeHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		// fetch the default value before the computation
		fmt.Println(CtxExpensiveObject.MustValue(ctx).result)

		myComputedExpensiveObject := ExpensiveComputation{result: "computed"}
		ctx = CtxExpensiveObject.WithValue(ctx, &myComputedExpensiveObject)

		useHandler.Handle(ctx)
	}, "compute")

	ctx := context.Background()
	computeHandler.Handle(ctx)
	// Output: pending
	// computed
}

func ExampleBoxed() {
	type ExpensiveComputation struct {
		result string
	}
	var CtxExpensiveObject = Boxed[*ExpensiveComputation](nil)

	// the compute handler performs some computation that we wish to re-use
	computeHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		myComputedExpensiveObject := ExpensiveComputation{result: "computed"}
		ctx = CtxExpensiveObject.WithValue(ctx, &myComputedExpensiveObject)
	}, "compute")

	decorateHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		// adds an empty box
		ctx = CtxExpensiveObject.WithBox(ctx)

		// fills in the box with the value
		computeHandler.Handle(ctx)

		// returns the unboxed value
		fmt.Println(CtxExpensiveObject.MustValue(ctx).result)
	}, "decorated")

	ctx := context.Background()
	decorateHandler.Handle(ctx)
	// Output: computed
}
