package handler

import (
	"context"
	"fmt"
)

// FirstStage implements Handler
// It often makes sense to store state that can be known at controller start
// in the handler struct (such as informers, api clients, etc), while state
// that is computed per-key is stored in the context.
type FirstStage struct {
	config string
	next   Handler
}

func (f *FirstStage) Handle(ctx context.Context) {
	fmt.Println("the first step")
	f.next.Handle(ctx)
}

func ExampleNewHandler() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// NewHandler assigns an id to a ContextHandler
	// IDs are most helpful for complex / branching state machines - a simple
	// chain of handlers may not need them. `NewTypeHandler` will assign a
	// default ID based on the type.
	handler := NewHandler(&FirstStage{config: "example", next: NoopHandler}, "theFirstStage")
	handler.Handle(ctx)
	// Output: the first step
}

func ExampleNewTypeHandler() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// NewTypeHandler will assign a default ID based on the type.
	handler := NewTypeHandler(&FirstStage{config: "example", next: NoopHandler})
	handler.Handle(ctx)
	// Output: the first step
}

// SecondStage implements Handler
type SecondStage struct {
	config string
	next   Handler
}

func (s *SecondStage) Handle(ctx context.Context) {
	fmt.Println("the second step")
	s.next.Handle(ctx)
}

// firstStageBuilder implements a Builder that returns FirstStage handlers.
// Builders allow easy chaining and re-ordering of Handlers.
func firstStageBuilder(next ...Handler) Handler {
	return NewTypeHandler(&FirstStage{
		config: "example",
		next:   Handlers(next).MustOne(),
	})
}

// secondStageBuilder implements a Builder that returns FirstStage handlers.
func secondStageBuilder(next ...Handler) Handler {
	return NewTypeHandler(&SecondStage{
		config: "example",
		next:   Handlers(next).MustOne(),
	})
}

func ExampleBuilder() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Builders allow for relatively painless restructuring of the handler
	// state machine:
	firstStageBuilder(secondStageBuilder(NoopHandler)).Handle(ctx)

	// reverse the order:
	secondStageBuilder(firstStageBuilder(NoopHandler)).Handle(ctx)

	// Builders are a lower-level building block
	// See Chain and Parallel for helpers that make building larger state
	// machines more ergonomic.

	// Output: the first step
	// the second step
	// the second step
	// the first step
}

// DecisionHandler is an example of a handler that chooses between multiple
// "next" handlers to call.
type DecisionHandler struct {
	nextFirstStage  Handler
	nextSecondStage Handler
}

func (d *DecisionHandler) Handle(ctx context.Context) {
	if true {
		d.nextSecondStage.Handle(ctx)
		return
	}
	d.nextFirstStage.Handle(ctx)
}

func decisionHandlerBuilder(next ...Handler) Handler {
	return NewTypeHandler(&DecisionHandler{
		nextFirstStage:  Handlers(next).MustFind(firstKey),
		nextSecondStage: Handlers(next).MustFind(secondKey),
	})
}

var (
	firstKey  Key = "first"
	secondKey Key = "second"
)

func ExampleHandlers_MustFind() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handler IDs allow a Builder to pick out a specific handler from a set
	// This allows building complex or branching state machines
	secondStage := secondStageBuilder(NoopHandler).WithID(secondKey)
	firstStage := firstStageBuilder(secondStage).WithID(firstKey)
	decisionHandlerBuilder(firstStage, secondStage).Handle(ctx)

	// Output: the second step
}
