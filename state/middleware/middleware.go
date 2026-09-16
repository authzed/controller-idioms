// Package middleware provides ready-made state.Middleware implementations
// for common cross-cutting concerns.
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/authzed/ctxkey"

	"github.com/authzed/controller-idioms/state"
)

var logStartKey = ctxkey.New[time.Time]()

// Log returns a Middleware that logs each step's name, elapsed time, and
// outcome after the step executes. It uses state.Named for the step name
// (empty string if the step was not annotated). Elapsed time is logged as
// duration_ms (float64, milliseconds); the outcome is logged as a stable
// label (continued, terminated, cancelled, panicked), plus the terminal
// operation's detail ("done", "requeued") and cause when recorded. The level
// escalates with the outcome: Info normally, Warn on cancellation or an
// error-carrying termination (an error requeue is a failed reconcile
// attempt), Error on panic — so the last line a crashing controller emits
// identifies the step that broke.
//
// Log is built on state.Before and state.AfterOutcome, so it is a bracket
// (see FORMAL.md, "The Bracket Laws"): the log line is emitted on every
// outcome — including while a panic unwinds — and the measured duration
// covers only the wrapped step, not the downstream steps that CPS would
// otherwise run inline within the step's frame.
//
// Typical controller usage:
//
//	ctx = state.WithAmbientMiddleware(ctx, middleware.Log(slog.Default()))
func Log(logger *slog.Logger) state.Middleware {
	return state.Compose(
		state.Before(func(ctx context.Context) context.Context {
			ctx = state.WithStepNameCapture(ctx)
			return logStartKey.Set(ctx, time.Now())
		}),
		state.AfterOutcome(func(ctx context.Context, outcome state.Outcome) context.Context {
			start, _ := logStartKey.Value(ctx)
			level := slog.LevelInfo
			switch {
			case outcome.Kind == state.OutcomePanicked:
				level = slog.LevelError
			case outcome.Kind == state.OutcomeCancelled,
				outcome.Kind == state.OutcomeTerminated && outcome.Cause != nil:
				level = slog.LevelWarn
			}
			attrs := []any{
				"step", state.CapturedStepName(ctx),
				"duration_ms", float64(time.Since(start).Microseconds()) / 1000.0,
				"outcome", outcome.Kind.String(),
			}
			if outcome.Detail != "" {
				attrs = append(attrs, "detail", outcome.Detail)
			}
			if outcome.Cause != nil {
				attrs = append(attrs, "cause", outcome.Cause.Error())
			}
			logger.Log(ctx, level, "step executed", attrs...)
			return ctx
		}),
	)
}

// Log must be usable wherever a state.Middleware is expected.
var _ state.Middleware = Log(slog.Default())
