// Package queue provides helpers for working with client-go's `workqueues` and control flow handlers.
//
// This file provides handlers that integrate with the state package to provide clean
// control flow patterns for controllers. Instead of manually calling queue operations
// and returning, controllers can now use:
//
//	return queue.Done()
//	return queue.Requeue()
//	return queue.RequeueAfter(5 * time.Second)
//	return queue.RequeueErr(err)
//
// These handlers automatically call the appropriate queue operations and terminate
// the handler pipeline by returning nil.
//
// # Error Propagation Pattern
//
// Errors in this system are communicated via:
//
//  1. **Context Cancellation**: ctx.Err() is checked by Continue() and parallel execution.
//     When context is cancelled, the pipeline stops (unless WithErrorHandler is set).
//
//  2. **Explicit Error Handlers**: queue.RequeueErr(err) and queue.RequeueAPIErr(err)
//     take an explicit error and pass it to the queue for retry logic.
//
//  3. **Context Values**: Steps can store errors in context using typedctx.Box or
//     context.WithValue, then check them in subsequent steps or OnError handlers.
//
//  4. **Decision-based Error Handling**: Use Decision() to branch based on whether
//     an error occurred, or handle errors inline:
//
//     riskyOperation := state.NewStepFunc(func(ctx context.Context, next state.Step) state.Step {
//     result, err := doSomethingRisky()
//     if err != nil {
//     return queue.RequeueErr(err).Step().Run(ctx)  // Handle error inline
//     }
//     // Store result and continue
//     ctx = context.WithValue(ctx, "result", result)
//     return state.Continue(ctx, next)
//     })
//
// Or with Decision for conditional logic:
//
//	Sequence(
//	    validateInput,
//	    Decision(
//	        func(ctx context.Context) bool { return isValid(ctx) },
//	        continueProcessing,
//	        queue.RequeueErr(fmt.Errorf("validation failed")),
//	    ),
//	)
package queue

import (
	"context"
	"time"

	"github.com/authzed/controller-idioms/state"
)

// Done creates a handler that marks the current queue key as finished and terminates the pipeline.
// This is equivalent to calling queue.NewQueueOperationsCtx().Done(ctx) and returning.
//
// Usage:
//
//	pipeline := state.Sequence(
//	  validateInput,
//	  processResource,
//	  queue.Done(), // Stop here - processing complete
//	)
func Done() state.NewStep {
	return state.NewTerminalStepFunc(func(ctx context.Context) {
		NewQueueOperationsCtx().Done(ctx)
	})
}

// Requeue creates a handler that requeues the current key immediately and terminates the pipeline.
// This is equivalent to calling queue.NewQueueOperationsCtx().Requeue(ctx) and returning.
//
// Usage:
//
//	pipeline := state.Decision(
//	  resourceReady,
//	  continueProcessing,
//	  queue.Requeue(), // Not ready - try again immediately
//	)
func Requeue() state.NewStep {
	return state.NewTerminalStepFunc(func(ctx context.Context) {
		NewQueueOperationsCtx().Requeue(ctx)
	})
}

// RequeueAfter creates a handler that requeues the current key after the specified duration
// and terminates the pipeline.
// This is equivalent to calling queue.NewQueueOperationsCtx().RequeueAfter(ctx, duration) and returning.
//
// Usage:
//
//	pipeline := state.Decision(
//	  resourceReady,
//	  continueProcessing,
//	  queue.RequeueAfter(30 * time.Second), // Not ready - try again in 30s
//	)
func RequeueAfter(duration time.Duration) state.NewStep {
	return state.NewTerminalStepFunc(func(ctx context.Context) {
		NewQueueOperationsCtx().RequeueAfter(ctx, duration)
	})
}

// RequeueErr creates a handler that records an error and requeues the current key immediately,
// then terminates the pipeline.
// This is equivalent to calling queue.NewQueueOperationsCtx().RequeueErr(ctx, err) and returning.
//
// Usage - return directly from within a step when error occurs:
//
//	validateInput := state.NewStepFunc(func(ctx context.Context, next state.Step) state.Step {
//	    obj := getObjectFromContext(ctx)
//	    if err := validate(obj); err != nil {
//	        return queue.RequeueErr(fmt.Errorf("validation failed: %w", err)).Step().Run(ctx)
//	    }
//	    return state.Continue(ctx, next)
//	})
//
// Or use in Decision branches when the error is known statically:
//
//	state.Decision(
//	    inputValid,
//	    continueProcessing,
//	    queue.RequeueErr(fmt.Errorf("validation failed")), // Error known at composition time
//	)
func RequeueErr(err error) state.NewStep {
	return state.NewTerminalStepFunc(func(ctx context.Context) {
		NewQueueOperationsCtx().RequeueErr(ctx, err)
	})
}

// RequeueAPIErr creates a handler that handles API errors with appropriate retry logic
// and terminates the pipeline.
// This checks if the error contains retry information from the API server and requeues
// accordingly, equivalent to calling queue.NewQueueOperationsCtx().RequeueAPIErr(ctx, err).
//
// Usage - return directly from within a step when error occurs:
//
//	callKubernetesAPI := state.NewStepFunc(func(ctx context.Context, next state.Step) state.Step {
//	    result, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
//	    if err != nil {
//	        return queue.RequeueAPIErr(err).Step().Run(ctx) // execute inline, terminating the pipeline
//	    }
//	    // Store result in context and continue
//	    ctx = context.WithValue(ctx, "deployment", result)
//	    return state.Continue(ctx, next)
//	})
//
// Note: .Step() is equivalent to calling the NewStep with nil: queue.RequeueAPIErr(err)(nil)
// but is more readable and explicit about the conversion.
//
// This pattern keeps errors local to where they occur, avoiding the need to store
// errors in context or check them in Decision branches.
func RequeueAPIErr(err error) state.NewStep {
	return state.NewTerminalStepFunc(func(ctx context.Context) {
		NewQueueOperationsCtx().RequeueAPIErr(ctx, err)
	})
}

// OnError creates a handler that executes different queue operations based on whether
// an error occurred in the context.
//
// Usage:
//
//	pipeline := state.Sequence(
//	  riskyOperation,
//	  queue.OnError(
//	    queue.RequeueErr(fmt.Errorf("operation failed")), // If error
//	    queue.Done(), // If success
//	  ),
//	)
func OnError(errorHandler, successHandler state.NewStep) state.NewStep {
	return func(next state.Step) state.Step {
		errStep := errorHandler(next)
		okStep := successHandler(next)
		return state.StepFunc(func(ctx context.Context) state.Step {
			if ctx.Err() != nil {
				if errStep != nil {
					return errStep.Run(ctx)
				}
				return nil
			}
			if okStep != nil {
				return okStep.Run(ctx)
			}
			return nil
		})
	}
}
