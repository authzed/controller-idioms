package libctrl

import (
	"context"
	"fmt"

	"github.com/authzed/ktrllib/handler"
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

// ContextKey is a type that is used as a key in a context.Context for a
// specific type of value V. It mimics the context.Context interface
type ContextKey[V any] struct{}

func NewContextKey[V any]() *ContextKey[V] {
	return &ContextKey[V]{}
}

func (k *ContextKey[V]) WithValue(ctx context.Context, val V) context.Context {
	return context.WithValue(ctx, k, val)
}

func (k *ContextKey[V]) Value(ctx context.Context) (V, bool) {
	v, ok := ctx.Value(k).(V)
	return v, ok
}

func (k *ContextKey[V]) MustValue(ctx context.Context) V {
	v, ok := k.Value(ctx)
	if !ok {
		panic(fmt.Sprintf("could not find value for key %T in context", k))
	}
	return v
}

// ContextDefaultingKey is a type that is used as a key in a context.Context for
// a specific type of value, but returns the default value for V if unset.
type ContextDefaultingKey[V comparable] struct {
	defaultValue V
}

func NewContextDefaultingKey[V comparable](defaultValue V) *ContextDefaultingKey[V] {
	return &ContextDefaultingKey[V]{
		defaultValue: defaultValue,
	}
}

func (k *ContextDefaultingKey[V]) WithValue(ctx context.Context, val V) context.Context {
	return context.WithValue(ctx, k, val)
}

func (k *ContextDefaultingKey[V]) Value(ctx context.Context) V {
	v, ok := ctx.Value(k).(V)
	if !ok {
		return k.defaultValue
	}
	return v
}

func (k *ContextDefaultingKey[V]) MustValue(ctx context.Context) V {
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

type ContextHandle[V any] struct {
	value V
}

// ContextHandleDefaultingKey is a type that is used as a key in a
// context.Context that points to a handle containing the desired value.
// This allows a handler higher up in the chain to carve out a spot to be
// filled in by other handlers.
// It can also be used to hold non-comparable objects by wrapping them with a
// pointer.
type ContextHandleDefaultingKey[V any] struct {
	defaultValue V
}

func NewContextHandleDefaultingKey[V any](defaultValue V) *ContextHandleDefaultingKey[V] {
	return &ContextHandleDefaultingKey[V]{
		defaultValue: defaultValue,
	}
}

func (k *ContextHandleDefaultingKey[V]) WithValue(ctx context.Context, val V) context.Context {
	handle, ok := ctx.Value(k).(*ContextHandle[V])
	if ok {
		handle.value = val
		return ctx
	}
	return context.WithValue(ctx, k, &ContextHandle[V]{value: val})
}

func (k *ContextHandleDefaultingKey[V]) WithHandle(ctx context.Context) context.Context {
	return context.WithValue(ctx, k, &ContextHandle[V]{value: k.defaultValue})
}

func (k *ContextHandleDefaultingKey[V]) Value(ctx context.Context) V {
	handle, ok := ctx.Value(k).(*ContextHandle[V])
	if !ok {
		return k.defaultValue
	}
	return handle.value
}

func (k *ContextHandleDefaultingKey[V]) MustValue(ctx context.Context) V {
	return k.Value(ctx)
}

// HandleBuilder returns a HandleBuilder that calls WithHandle before calling
// the next handler in the chain.
func (k *ContextHandleDefaultingKey[V]) HandleBuilder(id handler.Key) handler.Builder {
	return func(next ...handler.Handler) handler.Handler {
		return handler.NewHandlerFromFunc(func(ctx context.Context) {
			ctx = k.WithHandle(ctx)
			handler.Handlers(next).MustOne().Handle(ctx)
		}, id)
	}
}
