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
//
// This package is a thin compatibility layer over
// [github.com/authzed/ctxkey] and will be removed in v2. To migrate:
//
//   - NewKey -> ctxkey.New; Key.WithValue -> Key.Set
//   - WithDefault -> ctxkey.NewWithDefault; DefaultingKey.WithValue -> Set;
//     DefaultingKey.MustValue -> MustNonEmptyValue
//   - Boxed -> ctxkey.NewBoxedWithDefault; BoxedKey.WithValue -> Set;
//     BoxedKey.WithBox(ctx) -> SetBox(ctx) (ctxkey's WithBox() returns a
//     func(context.Context) context.Context); BoxedKey.MustValue -> Value
//   - Box -> ctxkey.Box
//   - SettableContext -> ctxkey.ContextSet (method Set instead of WithValue)
//   - ValueContext, MustValueContext, BoxBuilder have no ctxkey equivalent;
//     define them locally if needed.
//
// Deprecated: use github.com/authzed/ctxkey instead.
package typedctx

import (
	"context"
	"fmt"

	"github.com/authzed/ctxkey"

	"github.com/authzed/controller-idioms/handler"
)

// SettableContext is implemented by keys that can store a value in a context.
//
// Deprecated: use github.com/authzed/ctxkey.ContextSet.
type SettableContext[V any] interface {
	WithValue(ctx context.Context, val V) context.Context
}

// ValueContext is implemented by keys that can fetch a value from a context.
//
// Deprecated: use github.com/authzed/ctxkey; this interface has no direct
// equivalent there.
type ValueContext[V any] interface {
	Value(ctx context.Context) (V, bool)
}

// MustValueContext is implemented by keys that fetch a value from a context
// or panic.
//
// Deprecated: use github.com/authzed/ctxkey; this interface has no direct
// equivalent there.
type MustValueContext[V any] interface {
	MustValue(ctx context.Context) V
}

// Key is a type that is used as a key in a context.Context for a
// specific type of value V. It mimics the context.Context interface
//
// A nil *Key[V] is usable: all nil keys of the same V share one slot.
//
// Deprecated: use github.com/authzed/ctxkey.Key.
type Key[V any] struct {
	key ctxkey.Key[V]
}

// NewKey creates a new Key
//
// Deprecated: use github.com/authzed/ctxkey.New.
func NewKey[V any]() *Key[V] {
	return &Key[V]{}
}

func (k *Key[V]) WithValue(ctx context.Context, val V) context.Context {
	if k == nil {
		return context.WithValue(ctx, k, val)
	}
	return k.key.Set(ctx, val)
}

func (k *Key[V]) Value(ctx context.Context) (V, bool) {
	if k == nil {
		v, ok := ctx.Value(k).(V)
		return v, ok
	}
	return k.key.Value(ctx)
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
//
// Deprecated: use github.com/authzed/ctxkey.DefaultingKey.
type DefaultingKey[V comparable] struct {
	key ctxkey.DefaultingKey[V]
}

// WithDefault creates a new DefaultingKey with the given default value
//
// Deprecated: use github.com/authzed/ctxkey.NewWithDefault.
func WithDefault[V comparable](defaultValue V) *DefaultingKey[V] {
	return &DefaultingKey[V]{
		key: *ctxkey.NewWithDefault(defaultValue),
	}
}

func (k *DefaultingKey[V]) WithValue(ctx context.Context, val V) context.Context {
	if k == nil {
		return context.WithValue(ctx, k, val)
	}
	return k.key.Set(ctx, val)
}

func (k *DefaultingKey[V]) Value(ctx context.Context) V {
	if k == nil {
		if v, ok := ctx.Value(k).(V); ok {
			return v
		}
	}
	return k.key.Value(ctx)
}

// MustValue returns the stored value (or the default if unset), and panics
// if that value is the zero value for V.
func (k *DefaultingKey[V]) MustValue(ctx context.Context) V {
	v := k.Value(ctx)
	var empty V
	if v == empty {
		panic(fmt.Sprintf("could not find non-nil value for key %T in context", k))
	}
	return v
}

// Box holds the value stored by a BoxedKey.
//
// Deprecated: use github.com/authzed/ctxkey.Box.
type Box[V any] = ctxkey.Box[V]

// BoxedKey is a type that is used as a key in a
// context.Context that points to a handle containing the desired value.
// This allows a handler higher up in the chain to carve out a spot to be
// filled in by other handlers.
// It can also be used to hold non-comparable objects by wrapping them with a
// pointer.
//
// Deprecated: use github.com/authzed/ctxkey.BoxedKey.
type BoxedKey[V any] struct {
	key ctxkey.BoxedKey[V]
}

// Boxed creates a new BoxedKey with a default value
//
// Deprecated: use github.com/authzed/ctxkey.NewBoxedWithDefault.
func Boxed[V any](defaultValue V) *BoxedKey[V] {
	return &BoxedKey[V]{
		key: *ctxkey.NewBoxedWithDefault(defaultValue),
	}
}

func (k *BoxedKey[V]) WithValue(ctx context.Context, val V) context.Context {
	return k.key.Set(ctx, val)
}

func (k *BoxedKey[V]) WithBox(ctx context.Context) context.Context {
	return k.key.SetBox(ctx)
}

func (k *BoxedKey[V]) Value(ctx context.Context) V {
	return k.key.Value(ctx)
}

func (k *BoxedKey[V]) MustValue(ctx context.Context) V {
	return k.Value(ctx)
}

// BoxBuilder returns a handler.Builder that calls Boxed before calling
// the next handler in the chain.
// This is a shortcut for using Boxed context values in handler chains, i.e.
// by
func (k *BoxedKey[V]) BoxBuilder(id handler.Key) handler.Builder {
	return func(next ...handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			ctx = k.WithBox(ctx)
			handler.Handlers(next).MustOne().Handle(ctx)
		}, id)
	}
}
