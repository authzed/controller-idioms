package handler

import (
	"context"
	"fmt"
)

// This package contains the basic Handler units used to compose reconciliation
// loops for a controller.
//
// You write functions or types that implement the ContextHandler interface, and
// then compose them via Handlers and Builders.
//
// Builders are used for chaining Handler construction together, and Handlers
// are the things that actually process requests. It can be useful to go back
// and forth between Builders and Handlers (to wrap startup behavior or runtime
// behavior respectively), so each type has methods to convert between them.
//
// See other files for helpers to compose or wrap handlers and builders.

// ContextHandler is the interface for a "chunk" of reconciliation. It either
// returns, often by adjusting the current key's place in the queue (i.e. via
// requeue or done) or calls another handler in the chain.
type ContextHandler interface {
	Handle(context.Context)
}

// ContextHandlerFunc is a function type that implements ContextHandler
type ContextHandlerFunc func(ctx context.Context)

func (f ContextHandlerFunc) Handle(ctx context.Context) {
	f(ctx)
}

// Handler wraps a  ContextHandler and adds a method to create a corresponding
// Builder. Handler has a Key id so that t can be dereferenced by handlers
// that branch into multiple options and need to choose a specific one.
type Handler struct {
	ContextHandler
	id Key
}

// Builder returns the "natural" Builder formed by returning the existing
// handler.
func (h Handler) Builder() Builder {
	return func(...Handler) Handler {
		return h
	}
}

// ID returns the Key identifier for this handler.
func (h Handler) ID() Key {
	return h.id
}

// WithID returns a copy of this handler with a new ID.
func (h Handler) WithID(id Key) Handler {
	return Handler{
		ContextHandler: h.ContextHandler,
		id:             id,
	}
}

// Handlers adds methods to a list of Handler objects and makes it easy to pick
// a Handler with a specific ID out of the list.
type Handlers []Handler

// ToSet converts the list into a map from Key to Handler.
// If there are duplicate keys, the later value is picked.
func (h Handlers) ToSet() map[Key]Handler {
	set := make(map[Key]Handler)
	for _, handler := range h {
		set[handler.id] = handler
	}
	return set
}

// Find returns the Handler for the given Key and a boolean that returns true
// if it was found or false otherwise.
func (h Handlers) Find(id Key) (Handler, bool) {
	found, ok := h.ToSet()[id]
	return found, ok
}

// MustFind returns the Handler for the given Key and panics if none is found.
func (h Handlers) MustFind(id Key) Handler {
	found, ok := h.Find(id)
	if !ok {
		panic(fmt.Sprintf("handler with id %s not found", id))
	}
	return found
}

// MustOne returns a single Handler from the list and panics if the list does
// not have exactly one Handler.
func (h Handlers) MustOne() Handler {
	if len(h) != 1 {
		panic("more than one handler found")
	}
	return h[0]
}

// NewHandler assigns an id to a ContextHandler implementation and returns a
// Handler.
func NewHandler(h ContextHandler, id Key) Handler {
	return Handler{ContextHandler: h, id: id}
}

// NewHandlerFromFunc creates a new Handler from a ContextHandlerFunc.
func NewHandlerFromFunc(h ContextHandlerFunc, id Key) Handler {
	return Handler{ContextHandler: h, id: id}
}

// Builder is a function that takes a list of "next" Handlers and returns a
// single Handler.
type Builder func(next ...Handler) Handler

// Handler returns the "natural" Handler from the Builder by passing an empty
// handler to the builder.
func (f Builder) Handler(id Key) Handler {
	return f(NoopHandler).WithID(id)
}

// NoopHandler is a handler that does nothing
var NoopHandler = NewHandler(ContextHandlerFunc(func(ctx context.Context) {}), NextKey)

// Key is used to identify a given Handler in a set of Handlers
type Key string

// Find calls Find on the passed in Handlers with the current Key as argument.
func (k Key) Find(handlers Handlers) (Handler, bool) {
	return handlers.Find(k)
}

// MustFind calls MustFind on the passed in Handlers with the current Key as
// argument.
func (k Key) MustFind(handlers Handlers) Handler {
	return handlers.MustFind(k)
}

// NextKey is a standard key for a list of Handler of length 1
var NextKey Key = "next"
