// Package state contains Step units used to compose reconciliation loops
// for controllers.
//
// You write functions or types that implement the Step interface, and
// then compose them via NewStep functions and composition operators.
//
// NewStep functions are used for creating Step instances, and Handlers
// are the things that actually process requests - each step returns
// the next step to execute, or nil to terminate.
//
// Writing controllers in this style permits composition patterns including Sequence,
// Parallel, Decision, and other operations like Map and Bind.
//
// # Why Context?
//
// This package uses context.Context as the state container instead of a custom
// type or generic map for several reasons:
//
//  1. **Integration**: Controllers already use context.Context for cancellation,
//     deadlines, and request-scoped values. Using it here means zero friction.
//
//  2. **Standardization**: Context is Go's standard way to pass request-scoped
//     data. Using it makes the pattern immediately familiar to Go developers.
//
//  3. **Immutability**: context.WithValue() returns a new context, encouraging
//     immutable transformations (though this isn't enforced - see ContextFunc docs).
//
//  4. **Ecosystem**: Existing middleware, logging, tracing, and client libraries
//     already understand context.Context.
//
//  5. **Cancellation**: Built-in support for cancellation and deadlines, crucial
//     for long-running controller operations.
//
// Think of Context as a "sufficiently capable state container" rather than as
// purely a cancellation mechanism. For type-safe operations, use typedctx.
package state

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Step represents a step in a processing pipeline.
// Each step processes a context and returns the next step to execute, or nil to terminate.
type Step interface {
	Run(context.Context) Step
}

// StepFunc is a function type that implements Step
type StepFunc func(ctx context.Context) Step

func (f StepFunc) Run(ctx context.Context) Step {
	return f(ctx)
}

// ContextFunc is a function that does work with context.
// It takes a context, performs operations, and returns the (potentially modified) context
// so it can be threaded through the pipeline.
type ContextFunc func(context.Context) context.Context

// NewStep is a function that creates a Step when given the next step to execute.
// This is the basic building block for composing step pipelines using continuation-passing style.
type NewStep func(next Step) Step

// Step converts a NewStep to a Step by calling it with nil as the next step.
func (ns NewStep) Step() Step {
	return ns(nil)
}

// NewStepFunc creates a NewStep from a function that takes both context and next step.
// This is a convenience wrapper that eliminates boilerplate:
//
//	func MyStep() NewStep {
//	    return NewStepFunc(func(ctx context.Context, next Step) Step {
//	        // your logic here
//	        return Continue(ctx, next)
//	    })
//	}
//
// Instead of the more verbose:
//
//	func MyStep() NewStep {
//	    return func(next Step) Step {
//	        return StepFunc(func(ctx context.Context) Step {
//	            // your logic here
//	            return Continue(ctx, next)
//	        })
//	    }
//	}
func NewStepFunc(fn func(ctx context.Context, next Step) Step) NewStep {
	return func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			return fn(ctx, next)
		})
	}
}

// NewTerminalStepFunc creates a NewStep that executes a side-effect function and terminates the pipeline.
// This is a convenience wrapper for steps that don't need to continue to the next step.
//
// Example:
//
//	markComplete := state.NewTerminalStepFunc(func(ctx context.Context) {
//	    log.Println("Processing complete")
//	    queue.NewQueueOperationsCtx().Done(ctx)
//	})
func NewTerminalStepFunc(fn func(ctx context.Context)) NewStep {
	return NewStepFunc(func(ctx context.Context, _ Step) Step {
		fn(ctx)
		return nil
	})
}

// Run executes a NewStep pipeline until completion.
// With continuation-passing style, the pipeline executes completely in one call.
func Run(ctx context.Context, newStep NewStep) {
	step := newStep.Step()
	if step != nil {
		step.Run(ctx)
	}
}

type errorHandlerKey struct{}

// WithErrorHandler adds an error handler to the context.
// When Continue encounters a cancelled context, it will call this handler
// instead of stopping the pipeline.
func WithErrorHandler(ctx context.Context, handler func(error) Step) context.Context {
	return context.WithValue(ctx, errorHandlerKey{}, handler)
}

// Continue runs the next step if it exists and context is not cancelled.
// If context is cancelled:
//   - If an error handler is set via WithErrorHandler, calls the handler
//   - Otherwise, stops the pipeline (returns nil)
//
// This is a helper to avoid the common pattern:
//
//	if ctx.Err() != nil {
//	    return nil
//	}
//	if next != nil {
//	    return next.Run(ctx)
//	}
//	return nil
func Continue(ctx context.Context, next Step) Step {
	if ctx.Err() != nil {
		err := context.Cause(ctx)
		if handler, ok := ctx.Value(errorHandlerKey{}).(func(error) Step); ok && handler != nil {
			return handler(err)
		}
		return nil
	}
	if next != nil {
		return next.Run(ctx)
	}
	return nil
}

// Terminal creates a step that terminates the pipeline.
var Terminal = NewTerminalStepFunc(func(context.Context) {})

// Noop creates a step that does nothing and continues to the next step.
var Noop = NewStepFunc(Continue)

// Do lifts a ContextFunc into a pipeline step.
// This is the primary way to add work to a pipeline.
func Do(fn ContextFunc) NewStep {
	return NewStepFunc(func(ctx context.Context, next Step) Step {
		return Continue(fn(ctx), next)
	})
}

// Sequence composes multiple NewStep functions into a sequential pipeline.
// Each step in the sequence executes in order with proper context threading.
func Sequence(steps ...NewStep) NewStep {
	return func(next Step) Step {
		if len(steps) == 0 {
			if next != nil {
				return next
			}
			return nil
		}

		// Build the chain right-to-left (continuation-passing style)
		current := next
		for _, step := range slices.Backward(steps) {
			current = step(current)
		}
		return current
	}
}

// ParallelWith composes multiple NewStep functions to run in parallel,
// applying wrapper to each branch before execution. This is useful for
// applying a uniform policy to all branches, such as panic recovery:
//
//	ParallelWith(Recover, step1, step2, step3)
//
// The wrapper is called once per branch at instantiation time (when the
// returned NewStep is called), not at execution time.
//
// If using Recover as the wrapper and multiple branches panic concurrently,
// the error handler may be called multiple times — once per panicking branch.
// The context cause will reflect whichever panic cancelled the context first.
func ParallelWith(wrapper func(NewStep) NewStep, steps ...NewStep) NewStep {
	wrapped := make([]NewStep, len(steps))
	for i, s := range steps {
		wrapped[i] = wrapper(s)
	}
	return Parallel(wrapped...)
}

// Parallel composes multiple NewStep functions to run in parallel,
// then continues to the next step after all complete.
func Parallel(steps ...NewStep) NewStep {
	return func(next Step) Step {
		return &ParallelStep{
			steps: steps,
			next:  next,
		}
	}
}

// ParallelStep implements parallel execution of steps.
type ParallelStep struct {
	steps []NewStep
	next  Step
}

func (p *ParallelStep) Run(ctx context.Context) Step {
	var wg sync.WaitGroup

	for _, step := range p.steps {
		wg.Go(func() {
			if s := step.Step(); s != nil {
				s.Run(ctx)
			}
		})
	}

	wg.Wait()
	return Continue(ctx, p.next)
}

// PanicError wraps a recovered panic value as an error.
// It is set as the cause on the context passed to WithErrorHandler when Recover
// catches a panic, so callers can distinguish panics from normal cancellations
// and recover the original panic value via errors.As.
type PanicError struct {
	Value any
}

func (p *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", p.Value)
}

// Recover wraps a step with panic recovery. On panic, it cancels a child
// context with a *PanicError cause and routes through Continue, so any
// WithErrorHandler registered on the context will be called with the
// *PanicError. The pipeline does not continue past the recovered panic.
// In most cases, panics should propagate naturally — only use Recover when
// you explicitly need to handle a panic from a specific step.
//
// Note: Recover cannot catch panics from goroutines spawned by the wrapped
// step (e.g. a Parallel step). Go's runtime does not allow cross-goroutine
// panic recovery; those panics will still crash the program.
func Recover(step NewStep) NewStep {
	return func(next Step) Step {
		return StepFunc(func(ctx context.Context) (result Step) {
			childCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			defer func() {
				if r := recover(); r != nil {
					cancel(&PanicError{Value: r})
					result = Continue(childCtx, next)
				}
			}()
			return step(next).Run(childCtx)
		})
	}
}

// Decision creates a conditional step that chooses between two paths.
func Decision(predicate func(context.Context) bool, trueHandler, falseHandler NewStep) NewStep {
	return func(next Step) Step {
		return &DecisionStep{
			predicate:    predicate,
			trueHandler:  trueHandler,
			falseHandler: falseHandler,
			next:         next,
		}
	}
}

// DecisionStep implements conditional execution.
type DecisionStep struct {
	predicate    func(context.Context) bool
	trueHandler  NewStep
	falseHandler NewStep
	next         Step
}

func (d *DecisionStep) Run(ctx context.Context) Step {
	var chosen NewStep
	if d.predicate(ctx) {
		chosen = d.trueHandler
	} else {
		chosen = d.falseHandler
	}

	return Continue(ctx, chosen(d.next))
}

// When creates a conditional step with only a true branch.
// If the predicate returns true, the trueHandler is executed and the pipeline terminates.
// If the predicate returns false, execution continues to the next step.
//
// This is equivalent to Decision(predicate, trueHandler, Noop) but more readable
// when there is no meaningful false branch:
//
//	state.When(
//	    func(ctx context.Context) bool { return !resourceReady(ctx) },
//	    queue.RequeueAfter(30 * time.Second),
//	)
func When(predicate func(context.Context) bool, trueHandler NewStep) NewStep {
	return Decision(predicate, trueHandler, Noop)
}

// Enum creates a multi-way branching step based on a selector function.
// The selector function returns a value that is matched against the cases map.
// If no match is found, the defaultHandler is executed.
func Enum[T comparable](
	selector func(context.Context) T,
	cases map[T]NewStep,
	defaultHandler NewStep,
) NewStep {
	return func(next Step) Step {
		return &EnumStep[T]{
			selector:       selector,
			cases:          cases,
			defaultHandler: defaultHandler,
			next:           next,
		}
	}
}

// EnumStep implements multi-way branching based on enum values.
type EnumStep[T comparable] struct {
	selector       func(context.Context) T
	cases          map[T]NewStep
	defaultHandler NewStep
	next           Step
}

func (e *EnumStep[T]) Run(ctx context.Context) Step {
	value := e.selector(ctx)
	var chosen NewStep
	if step, ok := e.cases[value]; ok {
		chosen = step
	} else if e.defaultHandler != nil {
		chosen = e.defaultHandler
	} else {
		// No match and no default, continue to next
		return Continue(ctx, e.next)
	}

	return Continue(ctx, chosen(e.next))
}

// Switch creates a multi-way branching step based on string values.
// This is a convenience function for the common case of string-based branching.
func Switch(
	selector func(context.Context) string,
	cases map[string]NewStep,
	defaultHandler NewStep,
) NewStep {
	return Enum(selector, cases, defaultHandler)
}
