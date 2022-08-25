// Package typedctx provides generic helpers for storing / retrieving values
// from a `context.Context`.
//
// Breaking a controller down into small pieces with `Handler`s means that each
// piece either needs to re-calculate results from other stages or fetch the
// previously computed result from `context`.
//
//	    var CtxExpensiveObject = typedctx.NewKey[ExpensiveComputation]()
//
//		func (h *ComputeHandler) Handle(ctx context.Context) {
//		   ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
//		}
//
//		func (h *UseHandler) Handle(ctx context.Context) {
//		   precomputedExpensiveObject = CtxExpensiveObject.MustValue(ctx)
//			// do something with object
//		}
//
// `Handlers` are typically chained in a way that preserves the context between
// handlers, but not always.
//
// For example:
//
//	    var CtxExpensiveObject = typedctx.NewKey[ExpensiveComputation]()
//
//		func (h *ComputeHandler) Handle(ctx context.Context) {
//		   ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
//		}
//
//		func (h *DecorateHandler) Handle(ctx context.Context) {
//			ComputeHandler{}.Handle(ctx)
//
//		   // this fails, because the ctx passed into the wrapped handler isn't passed back out
//		   CtxExpensiveObject.MustValue(ctx)
//		}
//
// To deal with these cases, `typedctx` provides a `Boxed` context type that
// instead stores a pointer to the object, with additional helpers for making a
// "space" for the pointer to be filled in later.
//
//	    var CtxExpensiveObject = typedctx.Boxed[ExpensiveComputation](nil)
//
//		func (h *ComputeHandler) Handle(ctx context.Context) {
//		   ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
//		}
//
//		func (h *DecorateHandler) Handle(ctx context.Context) {
//			// adds an empty pointer
//			ctx = CtxExpensiveObject.WithBox(ctx)
//
//			// fills in the pointer - note that the implementation of ComputeHandler didn't change
//			ComputeHandler{}.Handle(ctx)
//
//			// now this succeeds, and returns the unboxed value
//			CtxExpensiveObject.MustValue(ctx)
//		}
package typedctx

import (
	"context"
	"fmt"

	"github.com/authzed/controller-idioms/handler"
)

type SettableContext[V any] interface {
	WithValue(ctx context.Context, val V) context.Context
}

type ValueContext[V any] interface {
	Value(ctx context.Context) (V, bool)
}

type MustValueContext[V any] interface {
	MustValue(ctx context.Context) V
}

// Key is a type that is used as a key in a context.Context for a
// specific type of value V. It mimics the context.Context interface
type Key[V any] struct{}

func NewKey[V any]() *Key[V] {
	return &Key[V]{}
}

func (k *Key[V]) WithValue(ctx context.Context, val V) context.Context {
	return context.WithValue(ctx, k, val)
}

func (k *Key[V]) Value(ctx context.Context) (V, bool) {
	v, ok := ctx.Value(k).(V)
	return v, ok
}

func (k *Key[V]) MustValue(ctx context.Context) V {
	v, ok := k.Value(ctx)
	if !ok {
		panic(fmt.Sprintf("could not find value for key %T in context", k))
	}
	return v
}

// DefaultingKey is a type that is used as a key in a context.Context for
// a specific type of value, but returns the default value for V if unset.
type DefaultingKey[V comparable] struct {
	defaultValue V
}

func WithDefault[V comparable](defaultValue V) *DefaultingKey[V] {
	return &DefaultingKey[V]{
		defaultValue: defaultValue,
	}
}

func (k *DefaultingKey[V]) WithValue(ctx context.Context, val V) context.Context {
	return context.WithValue(ctx, k, val)
}

func (k *DefaultingKey[V]) Value(ctx context.Context) V {
	v, ok := ctx.Value(k).(V)
	if !ok {
		return k.defaultValue
	}
	return v
}

func (k *DefaultingKey[V]) MustValue(ctx context.Context) V {
	v, ok := ctx.Value(k).(V)
	if !ok {
		v = k.defaultValue
	}
	var empty V
	if v == empty {
		panic(fmt.Sprintf("could not find non-nil value for key %T in context", k))
	}
	return v
}

type Box[V any] struct {
	value V
}

// BoxedKey is a type that is used as a key in a
// context.Context that points to a handle containing the desired value.
// This allows a handler higher up in the chain to carve out a spot to be
// filled in by other handlers.
// It can also be used to hold non-comparable objects by wrapping them with a
// pointer.
type BoxedKey[V any] struct {
	defaultValue V
}

func Boxed[V any](defaultValue V) *BoxedKey[V] {
	return &BoxedKey[V]{
		defaultValue: defaultValue,
	}
}

func (k *BoxedKey[V]) WithValue(ctx context.Context, val V) context.Context {
	handle, ok := ctx.Value(k).(*Box[V])
	if ok {
		handle.value = val
		return ctx
	}
	return context.WithValue(ctx, k, &Box[V]{value: val})
}

func (k *BoxedKey[V]) WithBox(ctx context.Context) context.Context {
	return context.WithValue(ctx, k, &Box[V]{value: k.defaultValue})
}

func (k *BoxedKey[V]) Value(ctx context.Context) V {
	handle, ok := ctx.Value(k).(*Box[V])
	if !ok {
		return k.defaultValue
	}
	return handle.value
}

func (k *BoxedKey[V]) MustValue(ctx context.Context) V {
	return k.Value(ctx)
}

// BoxBuilder returns a BoxBuilder that calls Boxed before calling
// the next handler in the chain.
func (k *BoxedKey[V]) BoxBuilder(id handler.Key) handler.Builder {
	return func(next ...handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			ctx = k.WithBox(ctx)
			handler.Handlers(next).MustOne().Handle(ctx)
		}, id)
	}
}
