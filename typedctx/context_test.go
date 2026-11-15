package typedctx

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/controller-idioms/handler"
)

// Before typedctx wrapped ctxkey, Key[V] was zero-size, so two keys created
// by NewKey could share an address and overwrite each other's values.
func TestKeysOfSameTypeDoNotCollide(t *testing.T) {
	t.Run("Key", func(t *testing.T) {
		k1, k2 := NewKey[int](), NewKey[int]()
		ctx := k1.WithValue(context.Background(), 1)
		ctx = k2.WithValue(ctx, 2)
		require.Equal(t, 1, k1.MustValue(ctx))
		require.Equal(t, 2, k2.MustValue(ctx))
	})

	t.Run("DefaultingKey", func(t *testing.T) {
		k1, k2 := WithDefault(0), WithDefault(0)
		ctx := k1.WithValue(context.Background(), 1)
		ctx = k2.WithValue(ctx, 2)
		require.Equal(t, 1, k1.MustValue(ctx))
		require.Equal(t, 2, k2.MustValue(ctx))
	})

	t.Run("BoxedKey", func(t *testing.T) {
		k1, k2 := Boxed(0), Boxed(0)
		ctx := k1.WithBox(context.Background())
		ctx = k2.WithBox(ctx)
		k1.WithValue(ctx, 1)
		k2.WithValue(ctx, 2)
		require.Equal(t, 1, k1.MustValue(ctx))
		require.Equal(t, 2, k2.MustValue(ctx))
	})
}

// queue.OperationsContext embeds a nil *Key, so nil keys must keep working.
func TestNilKey(t *testing.T) {
	var k *Key[int]
	_, ok := k.Value(context.Background())
	require.False(t, ok)
	require.Panics(t, func() { k.MustValue(context.Background()) })

	ctx := k.WithValue(context.Background(), 1)
	require.Equal(t, 1, k.MustValue(ctx))

	var other *Key[int]
	require.Equal(t, 1, other.MustValue(ctx))
	_, ok = NewKey[int]().Value(ctx)
	require.False(t, ok)
}

func TestDefaultingKeyMustValue(t *testing.T) {
	k := WithDefault(0)
	require.Equal(t, 0, k.Value(context.Background()))
	require.Panics(t, func() { k.MustValue(context.Background()) })
	require.Equal(t, 5, WithDefault(5).MustValue(context.Background()))
}

func TestBoxedKeyMustValueReturnsDefault(t *testing.T) {
	require.Equal(t, 0, Boxed(0).MustValue(context.Background()))
}

func ExampleKey() {
	type ExpensiveComputation struct {
		result string
	}
	CtxExpensiveObject := NewKey[*ExpensiveComputation]()

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
	CtxExpensiveObject := WithDefault[*ExpensiveComputation](&ExpensiveComputation{result: "pending"})

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
	CtxExpensiveObject := Boxed[*ExpensiveComputation](nil)

	// the compute handler performs some computation that we wish to re-use
	computeHandler := handler.NewHandlerFromFunc(func(ctx context.Context) {
		myComputedExpensiveObject := ExpensiveComputation{result: "computed"}
		CtxExpensiveObject.WithValue(ctx, &myComputedExpensiveObject)
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
