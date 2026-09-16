package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/controller-idioms/state"
	"github.com/authzed/controller-idioms/state/middleware"
)

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
	name, ok := entry["step"]
	require.True(t, ok, "log entry should contain step")
	require.Empty(t, name)
}

func TestLogRecordsNameForTerminalStep(t *testing.T) {
	// Named terminal steps (those that never call their continuation) must still
	// log the step name correctly.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.AmbientDispatch(
		state.Named("cleanup", state.Terminal),
	))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	require.Equal(t, "cleanup", entry["step"])
	_, hasDuration := entry["duration_ms"]
	require.True(t, hasDuration, "log entry should contain duration_ms")
}

// decodeEntries reads the JSON log lines emitted by Log.
func decodeEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var out []map[string]any
	for dec.More() {
		var e map[string]any
		require.NoError(t, dec.Decode(&e))
		out = append(out, e)
	}
	return out
}

// Log runs inside each branch goroutine and allocates its own name-capture
// slot per branch, so every branch is reported separately with its own name
// and duration.
func TestLogReportsEachParallelBranch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	branch := func(name string) state.NewStep {
		return state.Named(name, state.Do(func(c context.Context) context.Context { return c }))
	}

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.Parallel(branch("alpha"), branch("beta"), branch("gamma")))

	entries := decodeEntries(t, &buf)
	require.Len(t, entries, 3, "each branch should be logged")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e["step"].(string))
		require.Contains(t, e, "duration_ms")
	}
	require.ElementsMatch(t, []string{"alpha", "beta", "gamma"}, names)
}

// Each step is logged individually, and a step's log line is emitted when the
// step completes — before downstream steps run — because Log brackets the step
// rather than timing its whole CPS continuation.
func TestLogAttributesPerStep(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.AmbientDispatch(
		state.Named("first", state.Do(func(c context.Context) context.Context { return c })),
		state.Named("second", state.Do(func(c context.Context) context.Context { return c })),
	))

	entries := decodeEntries(t, &buf)
	require.Len(t, entries, 2)
	require.Equal(t, "first", entries[0]["step"], "first step's line is emitted before downstream runs")
	require.Equal(t, "second", entries[1]["step"])
}

// Log classifies each step's result: the outcome label reflects the fate the
// pipeline acted on, the level escalates on failure, and a cancellation
// carries its cause.
func TestLogRecordsOutcome(t *testing.T) {
	newLogger := func() (*bytes.Buffer, *slog.Logger) {
		var buf bytes.Buffer
		return &buf, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	run := func(logger *slog.Logger, step state.NewStep) {
		ctx := state.WithAmbientMiddleware(context.Background(), middleware.Log(logger))
		state.Run(ctx, state.AmbientDispatch(step))
	}

	t.Run("continued at info", func(t *testing.T) {
		buf, logger := newLogger()
		run(logger, state.Do(func(c context.Context) context.Context { return c }))
		entry := decodeEntries(t, buf)[0]
		require.Equal(t, "continued", entry["outcome"])
		require.Equal(t, "INFO", entry["level"])
	})

	t.Run("terminated at info", func(t *testing.T) {
		buf, logger := newLogger()
		run(logger, state.Terminal)
		entry := decodeEntries(t, buf)[0]
		require.Equal(t, "terminated", entry["outcome"])
		require.Equal(t, "INFO", entry["level"])
	})

	t.Run("cancelled at warn with cause", func(t *testing.T) {
		buf, logger := newLogger()
		run(logger, state.Do(func(c context.Context) context.Context {
			cc, cancel := context.WithCancelCause(c)
			cancel(errors.New("deadline blown"))
			return cc
		}))
		entry := decodeEntries(t, buf)[0]
		require.Equal(t, "cancelled", entry["outcome"])
		require.Equal(t, "WARN", entry["level"])
		require.Equal(t, "deadline blown", entry["cause"])
	})

	t.Run("terminated with detail and cause at warn", func(t *testing.T) {
		buf, logger := newLogger()
		run(logger, state.NewTerminalStepFunc(func(ctx context.Context) {
			state.RecordTermination(ctx, "requeued", errors.New("sync failed"))
		}))
		entry := decodeEntries(t, buf)[0]
		require.Equal(t, "terminated", entry["outcome"])
		require.Equal(t, "requeued", entry["detail"])
		require.Equal(t, "sync failed", entry["cause"])
		require.Equal(t, "WARN", entry["level"])
	})

	t.Run("panicked at error", func(t *testing.T) {
		buf, logger := newLogger()
		require.PanicsWithValue(t, "boom", func() {
			run(logger, state.Named("culprit", state.NewStepFunc(
				func(_ context.Context, _ state.Step) state.Step { panic("boom") },
			)))
		})
		entry := decodeEntries(t, buf)[0]
		require.Equal(t, "panicked", entry["outcome"])
		require.Equal(t, "ERROR", entry["level"])
		require.Equal(t, "culprit", entry["step"], "the last line before the crash names the step")
	})
}

// Clearing the stack exempts a subtree, so nothing downstream is logged.
func TestLogSilencedByWithoutAmbientMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	branch := func(name string) state.NewStep {
		return state.Named(name, state.Do(func(c context.Context) context.Context { return c }))
	}

	ctx := state.WithAmbientMiddleware(t.Context(), middleware.Log(logger))
	state.Run(ctx, state.Sequence(
		state.Do(state.WithoutAmbientMiddleware),
		state.Parallel(branch("alpha"), branch("beta")),
	))

	require.Empty(t, decodeEntries(t, &buf), "cleared stack should log nothing")
}
