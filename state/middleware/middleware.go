// Package middleware provides ready-made state.Middleware implementations
// for common cross-cutting concerns.
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/authzed/controller-idioms/state"
)

// Log returns a Middleware that logs each step's name and elapsed time after
// each step executes. It uses state.StepName(ctx) for the step name (empty
// string if the step was not annotated with state.Named). Elapsed time is
// logged as duration_ms (float64, milliseconds) at Info level.
//
// Note: because this wraps a NewStep, the measured duration includes the step
// body and any continuation steps that execute inline via CPS.
//
// Typical controller usage:
//
//	ctx = state.WithAmbientMiddleware(ctx, middleware.Log(slog.Default()))
func Log(logger *slog.Logger) state.Middleware {
	return func(step state.NewStep) state.NewStep {
		return func(next state.Step) state.Step {
			return state.StepFunc(func(ctx context.Context) state.Step {
				// Intercept the context that flows through the step so we can
				// read the step name after state.Named (if present) enriches it.
				var enrichedCtx context.Context
				wrappedNext := state.StepFunc(func(innerCtx context.Context) state.Step {
					enrichedCtx = innerCtx
					if next != nil {
						return next.Run(innerCtx)
					}
					return nil
				})

				start := time.Now()
				result := step(wrappedNext).Run(ctx)
				elapsed := time.Since(start)

				// Prefer the enriched context (set by Named) for the step name.
				nameCtx := ctx
				if enrichedCtx != nil {
					nameCtx = enrichedCtx
				}
				logger.InfoContext(nameCtx, "step executed",
					"step", state.StepName(nameCtx),
					"duration_ms", float64(elapsed.Microseconds())/1000.0,
				)
				return result
			})
		}
	}
}

// Recover is a Middleware that wraps a step with panic recovery. On panic,
// it cancels a child context with a *state.PanicError cause and routes through
// state.Continue, so any state.WithErrorHandler registered on the context will
// be called with the *state.PanicError. The pipeline does not continue past the
// recovered panic.
//
// In most cases, panics should propagate naturally — only use Recover when
// you explicitly need to handle a panic from a specific step.
//
// Note: Recover cannot catch panics from goroutines spawned by the wrapped
// step (e.g. a Parallel step). Go's runtime does not allow cross-goroutine
// panic recovery; those panics will still crash the program.
func Recover(step state.NewStep) state.NewStep {
	return func(next state.Step) state.Step {
		return state.StepFunc(func(ctx context.Context) (result state.Step) {
			childCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			defer func() {
				if r := recover(); r != nil {
					cancel(&state.PanicError{Value: r})
					result = state.Continue(childCtx, next)
				}
			}()
			return step(next).Run(childCtx)
		})
	}
}
