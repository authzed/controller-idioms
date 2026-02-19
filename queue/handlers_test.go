package queue_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/authzed/ctxkey"

	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/queue/fake"
	"github.com/authzed/controller-idioms/state"
)

func TestDone(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	// Create and run pipeline
	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context {
			// This should execute
			return ctx
		}),
		queue.Done(),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("This should not execute - pipeline should have terminated")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify Done was called
	if fakeQueue.DoneCallCount() != 1 {
		t.Errorf("Expected Done to be called once, got %d calls", fakeQueue.DoneCallCount())
	}
}

func TestRequeue(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	executed := false
	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context {
			executed = true
			return ctx
		}),
		queue.Requeue(),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("This should not execute - pipeline should have terminated")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify execution and requeue
	if !executed {
		t.Error("Expected first action to execute")
	}
	if fakeQueue.RequeueCallCount() != 1 {
		t.Errorf("Expected Requeue to be called once, got %d calls", fakeQueue.RequeueCallCount())
	}
}

func TestRequeueAfter(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}
	duration := 30 * time.Second

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context {
			// Setup action
			return ctx
		}),
		queue.RequeueAfter(duration),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("This should not execute - pipeline should have terminated")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify RequeueAfter was called with correct duration
	if fakeQueue.RequeueAfterCallCount() != 1 {
		t.Errorf("Expected RequeueAfter to be called once, got %d calls", fakeQueue.RequeueAfterCallCount())
	}
	if fakeQueue.RequeueAfterArgsForCall(0) != duration {
		t.Errorf("Expected RequeueAfter duration %v, got %v", duration, fakeQueue.RequeueAfterArgsForCall(0))
	}
}

func TestRequeueErr(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}
	testErr := errors.New("test error")

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	pipeline := state.Sequence(
		state.Do(func(ctx context.Context) context.Context {
			// Setup action
			return ctx
		}),
		queue.RequeueErr(testErr),
		state.Do(func(ctx context.Context) context.Context {
			t.Error("This should not execute - pipeline should have terminated")
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify RequeueErr was called with correct error
	if fakeQueue.RequeueErrCallCount() != 1 {
		t.Errorf("Expected RequeueErr to be called once, got %d calls", fakeQueue.RequeueErrCallCount())
	}
	if !errors.Is(fakeQueue.RequeueErrArgsForCall(0), testErr) {
		t.Errorf("Expected RequeueErr error %v, got %v", testErr, fakeQueue.RequeueErrArgsForCall(0))
	}
}

func TestRequeueErrFromWithinStep(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	var executedBefore bool
	var executedAfter bool

	// Simulate a step that encounters an error and returns the error handler directly
	riskyOperation := state.NewStepFunc(func(ctx context.Context, next state.Step) state.Step {
		executedBefore = true
		// Simulate some operation that fails
		err := errors.New("operation failed")
		if err != nil {
			// Return the error handler directly, short-circuiting the pipeline
			return queue.RequeueErr(err).Step().Run(ctx)
		}
		return state.Continue(ctx, next)
	})

	pipeline := state.Sequence(
		riskyOperation,
		state.Do(func(ctx context.Context) context.Context {
			executedAfter = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify the operation executed but subsequent steps did not
	if !executedBefore {
		t.Error("Expected risky operation to execute")
	}
	if executedAfter {
		t.Error("Expected pipeline to terminate after error, but subsequent step executed")
	}

	// Verify RequeueErr was called
	if fakeQueue.RequeueErrCallCount() != 1 {
		t.Errorf("Expected RequeueErr to be called once, got %d calls", fakeQueue.RequeueErrCallCount())
	}
}

func TestRequeueAPIErr(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}
	testErr := errors.New("API error")

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	pipeline := queue.RequeueAPIErr(testErr)
	state.Run(ctxWithQueue, pipeline)

	// Verify RequeueAPIErr was called with correct error
	if fakeQueue.RequeueAPIErrCallCount() != 1 {
		t.Errorf("Expected RequeueAPIErr to be called once, got %d calls", fakeQueue.RequeueAPIErrCallCount())
	}
	if !errors.Is(fakeQueue.RequeueAPIErrArgsForCall(0), testErr) {
		t.Errorf("Expected RequeueAPIErr error %v, got %v", testErr, fakeQueue.RequeueAPIErrArgsForCall(0))
	}
}

func TestRequeueAPIErrFromWithinStep(t *testing.T) {
	ctx := t.Context()
	fakeQueue := &fake.FakeInterface{}

	// Set up context with fake queue
	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	var executedBefore bool
	var executedAfter bool

	// Simulate a Kubernetes API call that fails
	callKubernetesAPI := state.NewStepFunc(func(ctx context.Context, next state.Step) state.Step {
		executedBefore = true
		// Simulate an API error
		apiErr := errors.New("deployments.apps \"myapp\" not found")
		if apiErr != nil {
			// Return the API error handler directly
			return queue.RequeueAPIErr(apiErr).Step().Run(ctx)
		}
		return state.Continue(ctx, next)
	})

	pipeline := state.Sequence(
		callKubernetesAPI,
		state.Do(func(ctx context.Context) context.Context {
			executedAfter = true
			return ctx
		}),
	)

	state.Run(ctxWithQueue, pipeline)

	// Verify the API call executed but subsequent steps did not
	if !executedBefore {
		t.Error("Expected API call to execute")
	}
	if executedAfter {
		t.Error("Expected pipeline to terminate after API error, but subsequent step executed")
	}

	// Verify RequeueAPIErr was called
	if fakeQueue.RequeueAPIErrCallCount() != 1 {
		t.Errorf("Expected RequeueAPIErr to be called once, got %d calls", fakeQueue.RequeueAPIErrCallCount())
	}
}

func TestConditionalRequeue(t *testing.T) {
	tests := []struct {
		name               string
		condition          bool
		expectRequeue      bool
		expectContinuation bool
	}{
		{
			name:               "requeue when condition is true",
			condition:          true,
			expectRequeue:      true,
			expectContinuation: false,
		},
		{
			name:               "continue when condition is false",
			condition:          false,
			expectRequeue:      false,
			expectContinuation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeQueue := &fake.FakeInterface{}

			// Set up context with fake queue
			queueCtx := queue.NewQueueOperationsCtx()
			ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

			continued := false
			pipeline := state.Sequence(
				state.When(
					func(_ context.Context) bool { return tt.condition },
					queue.Requeue(),
				),
				state.Do(func(ctx context.Context) context.Context {
					continued = true
					return ctx
				}),
			)

			state.Run(ctxWithQueue, pipeline)

			// Verify requeue behavior
			requeueCount := fakeQueue.RequeueCallCount()
			if tt.expectRequeue && requeueCount != 1 {
				t.Errorf("Expected requeue to be called once, got %d calls", requeueCount)
			}
			if !tt.expectRequeue && requeueCount != 0 {
				t.Errorf("Expected requeue not to be called, got %d calls", requeueCount)
			}

			// Verify continuation behavior
			if tt.expectContinuation && !continued {
				t.Error("Expected pipeline to continue")
			}
			if !tt.expectContinuation && continued {
				t.Error("Expected pipeline to terminate")
			}
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
		{
			name:               "requeue after when condition is true",
			condition:          true,
			expectRequeue:      true,
			expectContinuation: false,
		},
		{
			name:               "continue when condition is false",
			condition:          false,
			expectRequeue:      false,
			expectContinuation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeQueue := &fake.FakeInterface{}

			// Set up context with fake queue
			queueCtx := queue.NewQueueOperationsCtx()
			ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

			continued := false
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

			// Verify requeue behavior
			requeueCount := fakeQueue.RequeueAfterCallCount()
			if tt.expectRequeue && requeueCount != 1 {
				t.Errorf("Expected requeueAfter to be called once, got %d calls", requeueCount)
			}
			if !tt.expectRequeue && requeueCount != 0 {
				t.Errorf("Expected requeueAfter not to be called, got %d calls", requeueCount)
			}

			// Verify duration if requeued
			if tt.expectRequeue && fakeQueue.RequeueAfterArgsForCall(0) != duration {
				t.Errorf("Expected duration %v, got %v", duration, fakeQueue.RequeueAfterArgsForCall(0))
			}

			// Verify continuation behavior
			if tt.expectContinuation && !continued {
				t.Error("Expected pipeline to continue")
			}
			if !tt.expectContinuation && continued {
				t.Error("Expected pipeline to terminate")
			}
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
		{
			name:               "done when condition is true",
			condition:          true,
			expectDone:         true,
			expectContinuation: false,
		},
		{
			name:               "continue when condition is false",
			condition:          false,
			expectDone:         false,
			expectContinuation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeQueue := &fake.FakeInterface{}

			// Set up context with fake queue
			queueCtx := queue.NewQueueOperationsCtx()
			ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

			continued := false
			pipeline := state.Sequence(
				state.When(
					func(_ context.Context) bool { return tt.condition },
					queue.Done(),
				),
				state.Do(func(ctx context.Context) context.Context {
					continued = true
					return ctx
				}),
			)

			state.Run(ctxWithQueue, pipeline)

			// Verify done behavior
			doneCount := fakeQueue.DoneCallCount()
			if tt.expectDone && doneCount != 1 {
				t.Errorf("Expected done to be called once, got %d calls", doneCount)
			}
			if !tt.expectDone && doneCount != 0 {
				t.Errorf("Expected done not to be called, got %d calls", doneCount)
			}

			// Verify continuation behavior
			if tt.expectContinuation && !continued {
				t.Error("Expected pipeline to continue")
			}
			if !tt.expectContinuation && continued {
				t.Error("Expected pipeline to terminate")
			}
		})
	}
}

func TestOnError(t *testing.T) {
	tests := []struct {
		name          string
		hasError      bool
		expectDone    bool
		expectRequeue bool
	}{
		{
			name:          "error handler when error present",
			hasError:      true,
			expectDone:    false,
			expectRequeue: true,
		},
		{
			name:          "success handler when no error",
			hasError:      false,
			expectDone:    true,
			expectRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeQueue := &fake.FakeInterface{}

			// Set up context with fake queue
			queueCtx := queue.NewQueueOperationsCtx()
			ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

			// Add error to context if needed
			if tt.hasError {
				cancelCtx, cancel := context.WithCancel(ctxWithQueue)
				cancel() // This sets ctx.Err()
				ctxWithQueue = cancelCtx
			}

			pipeline := queue.OnError(
				queue.Requeue(), // Error handler
				queue.Done(),    // Success handler
			)

			state.Run(ctxWithQueue, pipeline)

			// Verify behavior based on error state
			doneCount := fakeQueue.DoneCallCount()
			requeueCount := fakeQueue.RequeueCallCount()

			if tt.expectDone && doneCount != 1 {
				t.Errorf("Expected done to be called once, got %d calls", doneCount)
			}
			if !tt.expectDone && doneCount != 0 {
				t.Errorf("Expected done not to be called, got %d calls", doneCount)
			}

			if tt.expectRequeue && requeueCount != 1 {
				t.Errorf("Expected requeue to be called once, got %d calls", requeueCount)
			}
			if !tt.expectRequeue && requeueCount != 0 {
				t.Errorf("Expected requeue not to be called, got %d calls", requeueCount)
			}
		})
	}
}

// Test realistic controller scenarios
func TestControllerScenarios(t *testing.T) {
	t.Run("successful processing flow", func(t *testing.T) {
		ctx := context.Background()
		fakeQueue := &fake.FakeInterface{}

		queueCtx := queue.NewQueueOperationsCtx()
		ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

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
			queue.Done(),
		)

		state.Run(ctxWithQueue, pipeline)

		expectedOrder := []string{"validate", "process", "finalize"}
		if len(executionOrder) != len(expectedOrder) {
			t.Fatalf("Expected %d executions, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
		}

		for i, expected := range expectedOrder {
			if executionOrder[i] != expected {
				t.Errorf("Expected execution[%d] = %q, got %q", i, expected, executionOrder[i])
			}
		}

		if fakeQueue.DoneCallCount() != 1 {
			t.Errorf("Expected done to be called once, got %d calls", fakeQueue.DoneCallCount())
		}
	})

	t.Run("resource not ready - requeue after delay", func(t *testing.T) {
		ctx := context.Background()
		fakeQueue := &fake.FakeInterface{}

		queueCtx := queue.NewQueueOperationsCtx()
		ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

		var executionOrder []string

		pipeline := state.Sequence(
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "check-dependencies")
				return ctx
			}),
			state.Decision(
				func(_ context.Context) bool {
					return false // Dependencies not ready
				},
				// Dependencies ready
				state.Sequence(
					state.Do(func(ctx context.Context) context.Context {
						executionOrder = append(executionOrder, "process")
						return ctx
					}),
					queue.Done(),
				),
				// Dependencies not ready
				queue.RequeueAfter(5*time.Minute),
			),
		)

		state.Run(ctxWithQueue, pipeline)

		expectedOrder := []string{"check-dependencies"}
		if len(executionOrder) != len(expectedOrder) {
			t.Fatalf("Expected %d executions, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
		}

		if fakeQueue.RequeueAfterCallCount() != 1 {
			t.Errorf("Expected requeueAfter to be called once, got %d calls", fakeQueue.RequeueAfterCallCount())
		}

		if fakeQueue.RequeueAfterArgsForCall(0) != 5*time.Minute {
			t.Errorf("Expected 5 minute delay, got %v", fakeQueue.RequeueAfterArgsForCall(0))
		}
	})

	t.Run("validation error - requeue with error", func(t *testing.T) {
		ctx := context.Background()
		fakeQueue := &fake.FakeInterface{}

		queueCtx := queue.NewQueueOperationsCtx()
		ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

		testErr := errors.New("validation failed")
		var executionOrder []string

		pipeline := state.Sequence(
			state.Do(func(ctx context.Context) context.Context {
				executionOrder = append(executionOrder, "validate")
				return ctx
			}),
			state.Decision(
				func(_ context.Context) bool {
					return false // Validation failed
				},
				// Validation passed
				state.Sequence(
					state.Do(func(ctx context.Context) context.Context {
						executionOrder = append(executionOrder, "process")
						return ctx
					}),
					queue.Done(),
				),
				// Validation failed
				queue.RequeueErr(testErr),
			),
		)

		state.Run(ctxWithQueue, pipeline)

		expectedOrder := []string{"validate"}
		if len(executionOrder) != len(expectedOrder) {
			t.Fatalf("Expected %d executions, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
		}

		if fakeQueue.RequeueErrCallCount() != 1 {
			t.Errorf("Expected requeueErr to be called once, got %d calls", fakeQueue.RequeueErrCallCount())
		}

		if !errors.Is(fakeQueue.RequeueErrArgsForCall(0), testErr) {
			t.Errorf("Expected error %v, got %v", testErr, fakeQueue.RequeueErrArgsForCall(0))
		}
	})
}

// Example demonstrating the clean controller pattern this enables
func Example() {
	ctx := context.Background()
	fakeQueue := &fake.FakeInterface{}

	queueCtx := queue.NewQueueOperationsCtx()
	ctxWithQueue := queueCtx.WithValue(ctx, fakeQueue)

	// Simulate resource state
	resourceReadyKey := ctxkey.New[bool]()
	ctxWithResource := resourceReadyKey.Set(ctxWithQueue, true)

	// Clean controller pipeline using queue handlers
	controllerPipeline := state.Sequence(
		// Set finalizer
		state.Do(func(ctx context.Context) context.Context {
			fmt.Println("Setting finalizer")
			return ctx
		}),

		// Check if resource is ready to process
		state.When(
			func(ctx context.Context) bool {
				return !resourceReadyKey.MustValue(ctx)
			},
			queue.RequeueAfter(30*time.Second), // Wait 30 seconds if not ready
		),

		// Process the resource
		state.Do(func(ctx context.Context) context.Context {
			fmt.Println("Processing resource")
			return ctx
		}),

		// Validate processing completed successfully
		state.When(
			func(_ context.Context) bool {
				// In real code, this would check if processing completed
				return false // Assume success
			},
			queue.Requeue(),
		),

		// Mark as done
		queue.Done(),
	)

	state.Run(ctxWithResource, controllerPipeline)

	// Output:
	// Setting finalizer
	// Processing resource
}
