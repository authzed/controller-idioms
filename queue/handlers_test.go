package queue_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/ctxkey"

	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/queue/fake"
	"github.com/authzed/controller-idioms/state"
)

func TestDone(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

	var afterExecuted bool
	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context { return ctx }),
		queue.Done,
		state.Do(func(ctx context.Context) context.Context {
			afterExecuted = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.Equal(t, 1, fakeQueue.DoneCallCount())
	require.False(t, afterExecuted, "pipeline should terminate after Done")
}

// Queue operations annotate the outcome observed by outcome-aware
// middleware: Done attests detail "done", the requeue family attests
// "requeued" — with the error, for the error variants.
func TestQueueOperationsAnnotateOutcome(t *testing.T) {
	observe := func(step state.NewStep) state.Outcome {
		fakeQueue := &fake.FakeInterface{}
		ctx := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)
		var got state.Outcome
		mw := state.AfterOutcome(func(ctx context.Context, o state.Outcome) context.Context {
			got = o
			return ctx
		})
		state.Run(ctx, mw.Wrap(step))
		return got
	}

	t.Run("Done", func(t *testing.T) {
		got := observe(queue.Done)
		require.Equal(t, state.OutcomeTerminated, got.Kind)
		require.Equal(t, "done", got.Detail)
		require.NoError(t, got.Cause)
	})

	t.Run("Requeue", func(t *testing.T) {
		got := observe(queue.Requeue)
		require.Equal(t, state.OutcomeTerminated, got.Kind)
		require.Equal(t, "requeued", got.Detail)
	})

	t.Run("RequeueAfter", func(t *testing.T) {
		got := observe(queue.RequeueAfter(time.Minute))
		require.Equal(t, state.OutcomeTerminated, got.Kind)
		require.Equal(t, "requeued", got.Detail)
	})

	t.Run("RequeueErr carries the error", func(t *testing.T) {
		cause := errors.New("sync failed")
		got := observe(queue.RequeueErr(cause))
		require.Equal(t, state.OutcomeTerminated, got.Kind)
		require.Equal(t, "requeued", got.Detail)
		require.Equal(t, cause, got.Cause)
	})
}

func TestRequeue(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

	var afterExecuted bool
	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context { return ctx }),
		queue.Requeue,
		state.Do(func(ctx context.Context) context.Context {
			afterExecuted = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.Equal(t, 1, fakeQueue.RequeueCallCount())
	require.False(t, afterExecuted, "pipeline should terminate after Requeue")
}

func TestRequeueAfter(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)
	duration := 30 * time.Second

	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context { return ctx }),
		queue.RequeueAfter(duration),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("pipeline should terminate after RequeueAfter")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.Equal(t, 1, fakeQueue.RequeueAfterCallCount())
	require.Equal(t, duration, fakeQueue.RequeueAfterArgsForCall(0))
}

func TestRequeueErr(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)
	testErr := errors.New("test error")

	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context { return ctx }),
		queue.RequeueErr(testErr),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("pipeline should terminate after RequeueErr")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.Equal(t, 1, fakeQueue.RequeueErrCallCount())
	require.ErrorIs(t, fakeQueue.RequeueErrArgsForCall(0), testErr)
}

func TestRequeueErrFromWithinStep(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

	var executedBefore, executedAfter bool

	riskyOperation := state.NewStepFunc(func(ctx context.Context, _ state.Step) state.Step {
		executedBefore = true
		err := errors.New("operation failed")
		return queue.RequeueErr(err).Step().Run(ctx)
	})

	pipeline := state.Sequence(
		riskyOperation,
		state.Do(func(ctx context.Context) context.Context {
			executedAfter = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.True(t, executedBefore)
	require.False(t, executedAfter, "pipeline should terminate after inline error")
	require.Equal(t, 1, fakeQueue.RequeueErrCallCount())
}

func TestRequeueAPIErr(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)
	testErr := errors.New("API error")

	state.Run(ctxWithQueue, queue.RequeueAPIErr(testErr))

	require.Equal(t, 1, fakeQueue.RequeueAPIErrCallCount())
	require.ErrorIs(t, fakeQueue.RequeueAPIErrArgsForCall(0), testErr)
}

func TestRequeueAPIErrFromWithinStep(t *testing.T) {
	fakeQueue := &fake.FakeInterface{}
	ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

	var executedBefore, executedAfter bool

	callKubernetesAPI := state.NewStepFunc(func(ctx context.Context, _ state.Step) state.Step {
		executedBefore = true
		apiErr := errors.New("deployments.apps \"myapp\" not found")
		return queue.RequeueAPIErr(apiErr).Step().Run(ctx)
	})

	pipeline := state.Sequence(
		callKubernetesAPI,
		state.Do(func(ctx context.Context) context.Context {
			executedAfter = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	require.True(t, executedBefore)
	require.False(t, executedAfter, "pipeline should terminate after inline API error")
	require.Equal(t, 1, fakeQueue.RequeueAPIErrCallCount())
}

func TestConditionalRequeue(t *testing.T) {
	tests := []struct {
		name               string
		condition          bool
		expectRequeue      bool
		expectContinuation bool
	}{
		{"requeue when condition is true", true, true, false},
		{"continue when condition is false", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeQueue := &fake.FakeInterface{}
			ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

			var continued bool
			pipeline := state.Sequence(
				state.When(
					func(_ context.Context) bool { return tt.condition },
					queue.Requeue,
				),
				state.Do(func(ctx context.Context) context.Context {
					continued = true
					return ctx
				}),
			)

			state.Run(ctxWithQueue, pipeline)

			if tt.expectRequeue {
				require.Equal(t, 1, fakeQueue.RequeueCallCount())
			} else {
				require.Equal(t, 0, fakeQueue.RequeueCallCount())
			}
			require.Equal(t, tt.expectContinuation, continued)
		})
	}
}

func TestConditionalRequeueAfter(t *testing.T) {
	duration := 15 * time.Second

	tests := []struct {
		name               string
		condition          bool
		expectRequeue      bool
		expectContinuation bool
	}{
		{"requeue after when condition is true", true, true, false},
		{"continue when condition is false", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeQueue := &fake.FakeInterface{}
			ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

			var continued bool
			pipeline := state.Sequence(
				state.When(
					func(_ context.Context) bool { return tt.condition },
					queue.RequeueAfter(duration),
				),
				state.Do(func(ctx context.Context) context.Context {
					continued = true
					return ctx
				}),
			)

			state.Run(ctxWithQueue, pipeline)

			if tt.expectRequeue {
				require.Equal(t, 1, fakeQueue.RequeueAfterCallCount())
				require.Equal(t, duration, fakeQueue.RequeueAfterArgsForCall(0))
			} else {
				require.Equal(t, 0, fakeQueue.RequeueAfterCallCount())
			}
			require.Equal(t, tt.expectContinuation, continued)
		})
	}
}

func TestConditionalDone(t *testing.T) {
	tests := []struct {
		name               string
		condition          bool
		expectDone         bool
		expectContinuation bool
	}{
		{"done when condition is true", true, true, false},
		{"continue when condition is false", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeQueue := &fake.FakeInterface{}
			ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

			var continued bool
			pipeline := state.Sequence(
				state.When(
					func(_ context.Context) bool { return tt.condition },
					queue.Done,
				),
				state.Do(func(ctx context.Context) context.Context {
					continued = true
					return ctx
				}),
			)

			state.Run(ctxWithQueue, pipeline)

			if tt.expectDone {
				require.Equal(t, 1, fakeQueue.DoneCallCount())
			} else {
				require.Equal(t, 0, fakeQueue.DoneCallCount())
			}
			require.Equal(t, tt.expectContinuation, continued)
		})
	}
}

// TestControllerScenarios tests realistic controller pipelines.
func TestControllerScenarios(t *testing.T) {
	t.Run("successful processing flow", func(t *testing.T) {
		fakeQueue := &fake.FakeInterface{}
		ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

		var executionOrder []string

		pipeline := state.Sequence(
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "validate")
				return ctx
			}),
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "process")
				return ctx
			}),
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "finalize")
				return ctx
			}),
			queue.Done,
		)

		state.Run(ctxWithQueue, pipeline)

		require.Equal(t, []string{"validate", "process", "finalize"}, executionOrder)
		require.Equal(t, 1, fakeQueue.DoneCallCount())
	})

	t.Run("resource not ready - requeue after delay", func(t *testing.T) {
		fakeQueue := &fake.FakeInterface{}
		ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)

		var executionOrder []string

		pipeline := state.Sequence(
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "check-dependencies")
				return ctx
			}),
			state.Decision(
				func(_ context.Context) bool { return false },
				state.Sequence(
					state.Do(func(ctx context.Context) context.Context {
						executionOrder = append(executionOrder, "process")
						return ctx
					}),
					queue.Done,
				),
				queue.RequeueAfter(5*time.Minute),
			),
		)

		state.Run(ctxWithQueue, pipeline)

		require.Equal(t, []string{"check-dependencies"}, executionOrder)
		require.Equal(t, 1, fakeQueue.RequeueAfterCallCount())
		require.Equal(t, 5*time.Minute, fakeQueue.RequeueAfterArgsForCall(0))
	})

	t.Run("validation error - requeue with error", func(t *testing.T) {
		fakeQueue := &fake.FakeInterface{}
		ctxWithQueue := queue.NewQueueOperationsCtx().WithValue(t.Context(), fakeQueue)
		testErr := errors.New("validation failed")

		var executionOrder []string

		pipeline := state.Sequence(
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "validate")
				return ctx
			}),
			state.Decision(
				func(_ context.Context) bool { return false },
				state.Sequence(
					state.Do(func(ctx context.Context) context.Context {
						executionOrder = append(executionOrder, "process")
						return ctx
					}),
					queue.Done,
				),
				queue.RequeueErr(testErr),
			),
		)

		state.Run(ctxWithQueue, pipeline)

		require.Equal(t, []string{"validate"}, executionOrder)
		require.Equal(t, 1, fakeQueue.RequeueErrCallCount())
		require.ErrorIs(t, fakeQueue.RequeueErrArgsForCall(0), testErr)
	})
}

// Example demonstrates the clean controller pattern this enables.
func Example() {
	ctx := context.Background()
	fakeQueue := &fake.FakeInterface{}

	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	resourceReadyKey := ctxkey.New[bool]()
	ctxWithResource := resourceReadyKey.Set(ctxWithQueue, true)

	controllerPipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context {
			fmt.Println("Setting finalizer")
			return ctx
		}),
		state.When(
			func(ctx context.Context) bool {
				return !resourceReadyKey.MustValue(ctx)
			},
			queue.RequeueAfter(30*time.Second),
		),
		state.Do(func(ctx context.Context) context.Context {
			fmt.Println("Processing resource")
			return ctx
		}),
		state.When(
			func(_ context.Context) bool { return false },
			queue.Requeue,
		),
		queue.Done,
	)

	state.Run(ctxWithResource, controllerPipeline)

	// Output:
	// Setting finalizer
	// Processing resource
}
