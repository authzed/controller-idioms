package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/controller-idioms/state"
	"github.com/authzed/controller-idioms/state/middleware"
)

func TestRecoverStopsPipelineOnPanic(t *testing.T) {
	ctx := t.Context()
	var afterPanicExecuted bool

	pipeline := state.Sequence(
		middleware.Recover(state.Do(func(_ context.Context) context.Context {
			panic("intentional panic")
		})),
		state.Do(func(ctx context.Context) context.Context {
			afterPanicExecuted = true
			return ctx
		}),
	)

	require.NotPanics(t, func() {
		state.Run(ctx, pipeline)
	}, "Recover should suppress the panic")
	require.False(t, afterPanicExecuted, "pipeline should stop after recovered panic")
}

func TestRecoverContinuesNormally(t *testing.T) {
	ctx := t.Context()
	var executed []string

	pipeline := state.Sequence(
		middleware.Recover(state.Do(func(ctx context.Context) context.Context {
			executed = append(executed, "step1")
			return ctx
		})),
		state.Do(func(ctx context.Context) context.Context {
			executed = append(executed, "step2")
			return ctx
		}),
	)

	state.Run(ctx, pipeline)

	require.Equal(t, []string{"step1", "step2"}, executed)
}

func TestRecoverRoutesToErrorHandler(t *testing.T) {
	var handlerErr error
	ctx := state.WithErrorHandler(t.Context(), func(err error) state.Step {
		handlerErr = err
		return nil
	})

	pipeline := middleware.Recover(state.Do(func(_ context.Context) context.Context {
		panic("intentional panic")
	}))

	require.NotPanics(t, func() {
		state.Run(ctx, pipeline)
	})
	require.Error(t, handlerErr, "error handler should be called after recovered panic")
}

func TestRecoverPanicValueAvailableViaCause(t *testing.T) {
	type sentinelType struct{ msg string }
	panicValue := sentinelType{"something went wrong"}

	var causeErr error
	ctx := state.WithErrorHandler(t.Context(), func(err error) state.Step {
		causeErr = err
		return nil
	})

	pipeline := middleware.Recover(state.Do(func(_ context.Context) context.Context {
		panic(panicValue)
	}))

	state.Run(ctx, pipeline)

	var p *state.PanicError
	require.ErrorAs(t, causeErr, &p, "error should be a *state.PanicError")
	require.Equal(t, panicValue, p.Value, "PanicError.Value should be the original panic value")
}

func TestRecoverWrappingSequence(t *testing.T) {
	var innerAfterPanicExecuted bool
	var outerAfterPanicExecuted bool
	var handlerErr error

	ctx := state.WithErrorHandler(t.Context(), func(err error) state.Step {
		handlerErr = err
		return nil
	})

	pipeline := state.Sequence(
		middleware.Recover(state.Sequence(
			state.Do(func(ctx context.Context) context.Context { return ctx }),
			state.Do(func(_ context.Context) context.Context { panic("mid-sequence panic") }),
			state.Do(func(ctx context.Context) context.Context {
				innerAfterPanicExecuted = true
				return ctx
			}),
		)),
		state.Do(func(ctx context.Context) context.Context {
			outerAfterPanicExecuted = true
			return ctx
		}),
	)

	require.NotPanics(t, func() { state.Run(ctx, pipeline) })
	require.False(t, innerAfterPanicExecuted, "step inside recovered sequence after panic should not execute")
	require.False(t, outerAfterPanicExecuted, "step outside recovered sequence after panic should not execute")
	var p *state.PanicError
	require.ErrorAs(t, handlerErr, &p)
	require.Equal(t, "mid-sequence panic", p.Value)
}

func TestMapWithRecoverRunsAllBranches(t *testing.T) {
	// Verify that wrapping with Recover does not suppress branch execution.
	ctx := t.Context()
	var counter int32

	pipeline := state.Parallel(state.Map(middleware.Recover,
		state.Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
		state.Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
	)...)

	state.Run(ctx, pipeline)
	require.Equal(t, int32(2), atomic.LoadInt32(&counter))
}

func TestMapWithRecoverRoutesPanicToErrorHandler(t *testing.T) {
	var handlerErr error
	ctx := state.WithErrorHandler(t.Context(), func(err error) state.Step {
		handlerErr = err
		return nil
	})

	pipeline := state.Parallel(state.Map(middleware.Recover,
		state.Do(func(ctx context.Context) context.Context { return ctx }),
		state.Do(func(_ context.Context) context.Context { panic("branch panic") }),
		state.Do(func(ctx context.Context) context.Context { return ctx }),
	)...)

	require.NotPanics(t, func() { state.Run(ctx, pipeline) })

	var p *state.PanicError
	require.ErrorAs(t, handlerErr, &p, "error handler should receive a *state.PanicError from the panicking branch")
	require.Equal(t, "branch panic", p.Value)
}

func TestRecoverIsMiddleware(t *testing.T) {
	// Compile-time check: Recover must be assignable to state.Middleware.
	var _ state.Middleware = middleware.Recover
}

func TestLogRecordsStepNameAndDuration(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.AmbientDispatch(
		state.Named("myStep", state.Do(func(ctx context.Context) context.Context { return ctx })),
	))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	require.Equal(t, "myStep", entry["step"])
	_, hasDuration := entry["duration_ms"]
	require.True(t, hasDuration, "log entry should contain duration_ms")
}

func TestLogUnnamedStepLogsEmptyName(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.AmbientDispatch(
		state.Do(func(ctx context.Context) context.Context { return ctx }),
	))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	require.Equal(t, "", entry["step"])
}
