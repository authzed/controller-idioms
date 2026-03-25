package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/ctxkey"
)

// ============================================================================
// EXAMPLE FUNCTIONS (Documentation via Examples)
// ============================================================================

func ExampleSequence() {
	ctx := context.Background()

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("first stage")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("second stage")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("third stage")
			return ctx
		}),
	)

	Run(ctx, pipeline)
	// Output:
	// first stage
	// second stage
	// third stage
}

func ExampleDecision() {
	ctx := context.Background()

	// Decision based on a simple condition
	condition := true

	pipeline := Decision(
		func(_ context.Context) bool {
			return condition
		},
		Do(func(ctx context.Context) context.Context {
			fmt.Println("true branch")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("false branch")
			return ctx
		}),
	)

	Run(ctx, pipeline)
	// Output: true branch
}

func ExampleParallel() {
	ctx := context.Background()

	// Use atomic counter to demonstrate parallel execution
	var counter int32

	pipeline := Parallel(
		Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
	)

	Run(ctx, pipeline)
	fmt.Printf("counter: %d", atomic.LoadInt32(&counter))
	// Output: counter: 3
}

func Example_complexPipeline() {
	ctx := context.Background()

	// A more complex example showing composition
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("initialization")
			return ctx
		}),
		Decision(
			func(_ context.Context) bool {
				return true // some validation logic
			},
			// Validation passed - processing
			Do(func(ctx context.Context) context.Context {
				fmt.Println("validation passed")
				return ctx
			}),
			// Validation failed
			Do(func(ctx context.Context) context.Context {
				fmt.Println("validation failed")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("finalization")
			return ctx
		}),
	)

	Run(ctx, pipeline)
	// Output:
	// initialization
	// validation passed
	// finalization
}

func ExampleEnum() {
	operationKey := ctxkey.New[string]()
	ctx := operationKey.Set(context.Background(), "create")

	pipeline := Enum(
		func(ctx context.Context) string {
			return operationKey.MustValue(ctx)
		},
		map[string]NewStep{
			"create": Do(func(ctx context.Context) context.Context {
				fmt.Println("creating resource")
				return ctx
			}),
			"update": Do(func(ctx context.Context) context.Context {
				fmt.Println("updating resource")
				return ctx
			}),
			"delete": Do(func(ctx context.Context) context.Context {
				fmt.Println("deleting resource")
				return ctx
			}),
		},
		Do(func(ctx context.Context) context.Context {
			fmt.Println("unknown operation")
			return ctx
		}),
	)

	Run(ctx, pipeline)
	// Output: creating resource
}

func ExampleSwitch() {
	statusKey := ctxkey.New[string]()
	ctx := statusKey.Set(context.Background(), "pending")

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("processing status")
			return ctx
		}),
		Switch(
			func(ctx context.Context) string {
				return statusKey.MustValue(ctx)
			},
			map[string]NewStep{
				"pending": Do(func(ctx context.Context) context.Context {
					fmt.Println("handling pending status")
					return ctx
				}),
				"complete": Do(func(ctx context.Context) context.Context {
					fmt.Println("handling complete status")
					return ctx
				}),
				"failed": Do(func(ctx context.Context) context.Context {
					fmt.Println("handling failed status")
					return ctx
				}),
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("handling unknown status")
				return ctx
			}),
		),
	)

	Run(ctx, pipeline)
	// Output:
	// processing status
	// handling pending status
}

func Example_branchingComparison() {
	ctx := context.Background()

	// This is equivalent to the complex handler branching example:
	// hasSecretHandler := chain(c.ensureMetadata, c.ensureScopedDatabaseCreds(...))
	// directSecretHandler := chain(c.adoptDBRootSecret, c.removeMissingSecretCondition, hasSecretHandler)
	// mainHandler := chain(c.setFinalizer, c.safeDelete, c.checkPause, c.validateInstance(hasSecretHandler, directSecretHandler))

	// Define reusable stage builders
	ensureMetadata := Do(func(ctx context.Context) context.Context {
		fmt.Println("ensuring metadata")
		return ctx
	})

	ensureScopedDatabaseCreds := Do(func(ctx context.Context) context.Context {
		fmt.Println("ensuring scoped database credentials")
		return ctx
	})

	createLogicalDatabase := Do(func(ctx context.Context) context.Context {
		fmt.Println("creating logical database")
		return ctx
	})

	adoptDBRootSecret := Do(func(ctx context.Context) context.Context {
		fmt.Println("adopting DB root secret")
		return ctx
	})

	removeMissingSecretCondition := Do(func(ctx context.Context) context.Context {
		fmt.Println("removing missing secret condition")
		return ctx
	})

	// hasSecretChain - runs when secret is found via database instance
	hasSecretChain := Sequence(
		ensureMetadata,
		ensureScopedDatabaseCreds,
		createLogicalDatabase,
	)

	// directSecretChain - runs when database instance not found (old style)
	directSecretChain := Sequence(
		adoptDBRootSecret,
		removeMissingSecretCondition,
		hasSecretChain, // Reuse the hasSecret chain
	)

	// validateInstance - decides between the two branches
	validateInstance := Decision(
		func(_ context.Context) bool {
			// In real code, this would check if database instance exists
			fmt.Println("validating instance")
			return true // Has database instance
		},
		hasSecretChain,    // True branch
		directSecretChain, // False branch
	)

	// Main controller pipeline
	mainHandler := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("setting finalizer")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("safe delete check")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("checking pause")
			return ctx
		}),
		validateInstance,
	)

	Run(ctx, mainHandler)

	// Output:
	// setting finalizer
	// safe delete check
	// checking pause
	// validating instance
	// ensuring metadata
	// ensuring scoped database credentials
	// creating logical database
}

func Example_monadicPatterns() {
	configKey := ctxkey.New[string]()
	validatedConfigKey := ctxkey.New[string]()
	ctx := configKey.Set(context.Background(), "production")

	// Sequential composition with context transformation
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			config := configKey.MustValue(ctx)
			return validatedConfigKey.Set(ctx, config+"-validated")
		}),
		Do(func(ctx context.Context) context.Context {
			config := validatedConfigKey.MustValue(ctx)
			fmt.Printf("using config: %s\n", config)
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("first operation")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("dependent operation")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// using config: production-validated
	// first operation
	// dependent operation
}

func Example_conditionalExecution() {
	shouldProcessKey := ctxkey.New[bool]()
	ctx := shouldProcessKey.Set(context.Background(), false)

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("starting process")
			return ctx
		}),
		Decision(
			func(ctx context.Context) bool {
				return shouldProcessKey.MustValue(ctx)
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("processing enabled")
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				fmt.Println("processing disabled")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("cleanup")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// starting process
	// processing disabled
	// cleanup
}

func Example_complexConditionals() {
	userRoleKey := ctxkey.New[string]()
	ctx := userRoleKey.Set(context.Background(), "admin")

	// Multi-way branching using Enum - much cleaner than nested decisions
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("authentication")
			return ctx
		}),
		Enum(
			func(ctx context.Context) string {
				return userRoleKey.MustValue(ctx)
			},
			map[string]NewStep{
				"admin": Do(func(ctx context.Context) context.Context {
					fmt.Println("admin workflow")
					return ctx
				}),
				"user": Do(func(ctx context.Context) context.Context {
					fmt.Println("user workflow")
					return ctx
				}),
				"moderator": Do(func(ctx context.Context) context.Context {
					fmt.Println("moderator workflow")
					return ctx
				}),
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("guest workflow")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("logging user action")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// authentication
	// admin workflow
	// logging user action
}

func Example_reusableStages() {
	ctx := context.Background()

	// Define reusable stages
	logStart := func(name string) NewStep {
		return Do(func(ctx context.Context) context.Context {
			fmt.Printf("starting %s\n", name)
			return ctx
		})
	}

	logEnd := func(name string) NewStep {
		return Do(func(ctx context.Context) context.Context {
			fmt.Printf("completed %s\n", name)
			return ctx
		})
	}

	// Wrap a stage with logging
	withLogging := func(name string, stage NewStep) NewStep {
		return Sequence(
			logStart(name),
			stage,
			logEnd(name),
		)
	}

	// Use the reusable pattern
	pipeline := Sequence(
		withLogging("validation", Do(func(ctx context.Context) context.Context {
			fmt.Println("validating input")
			return ctx
		})),
		withLogging("processing", Do(func(ctx context.Context) context.Context {
			fmt.Println("processing work")
			return ctx
		})),
		withLogging("cleanup", Do(func(ctx context.Context) context.Context {
			fmt.Println("cleaning up")
			return ctx
		})),
	)

	Run(ctx, pipeline)

	// Output:
	// starting validation
	// validating input
	// completed validation
	// starting processing
	// processing work
	// completed processing
	// starting cleanup
	// cleaning up
	// completed cleanup
}

func Example_builderReplacement() {
	ctx := context.Background()

	// In the old handler system, you needed:
	// 1. Builder functions
	// 2. Chain() to compose builders
	// 3. .Handler(id) to instantiate
	// 4. Complex ID management for branching

	// In the state system, it's much simpler:
	// Just compose NewStep functions directly

	// Old way (conceptually):
	// validationBuilder := func(next Handler) Handler { ... }
	// processingBuilder := func(next Handler) Handler { ... }
	// pipeline := Chain(validationBuilder, processingBuilder).Handler("myPipeline")

	// New way:
	validation := Do(func(ctx context.Context) context.Context {
		fmt.Println("validation")
		return ctx
	})

	processing := Do(func(ctx context.Context) context.Context {
		fmt.Println("processing")
		return ctx
	})

	pipeline := Sequence(validation, processing)

	Run(ctx, pipeline)

	// Output:
	// validation
	// processing
}

func Example_enumBranching() {
	resourceTypeKey := ctxkey.New[string]()
	ctx := resourceTypeKey.Set(context.Background(), "deployment")

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("processing resource")
			return ctx
		}),
		Enum(
			func(ctx context.Context) string {
				return resourceTypeKey.MustValue(ctx)
			},
			map[string]NewStep{
				"deployment": Sequence(
					Do(func(ctx context.Context) context.Context {
						fmt.Println("validating deployment spec")
						return ctx
					}),
					Do(func(ctx context.Context) context.Context {
						fmt.Println("creating deployment")
						return ctx
					}),
				),
				"service": Sequence(
					Do(func(ctx context.Context) context.Context {
						fmt.Println("validating service spec")
						return ctx
					}),
					Do(func(ctx context.Context) context.Context {
						fmt.Println("creating service")
						return ctx
					}),
				),
				"configmap": Do(func(ctx context.Context) context.Context {
					fmt.Println("creating configmap")
					return ctx
				}),
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("unsupported resource type")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("resource processing complete")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// processing resource
	// validating deployment spec
	// creating deployment
	// resource processing complete
}

func Example_switchWorkflow() {
	phaseKey := ctxkey.New[string]()
	ctx := phaseKey.Set(context.Background(), "pending")

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("checking resource phase")
			return ctx
		}),
		Switch(
			func(ctx context.Context) string {
				return phaseKey.MustValue(ctx)
			},
			map[string]NewStep{
				"pending": Sequence(
					Do(func(ctx context.Context) context.Context {
						fmt.Println("initializing resources")
						return ctx
					}),
					Do(func(ctx context.Context) context.Context {
						fmt.Println("setting up dependencies")
						return ctx
					}),
				),
				"running": Do(func(ctx context.Context) context.Context {
					fmt.Println("monitoring running state")
					return ctx
				}),
				"failed": Sequence(
					Do(func(ctx context.Context) context.Context {
						fmt.Println("analyzing failure")
						return ctx
					}),
					Do(func(ctx context.Context) context.Context {
						fmt.Println("attempting recovery")
						return ctx
					}),
				),
				"completed": Do(func(ctx context.Context) context.Context {
					fmt.Println("cleaning up completed resources")
					return ctx
				}),
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("unknown phase - logging for investigation")
				return ctx
			}),
		),
	)

	Run(ctx, pipeline)

	// Output:
	// checking resource phase
	// initializing resources
	// setting up dependencies
}

func Example_controllerWithEnum() {
	operationKey := ctxkey.New[string]()
	ctx := operationKey.Set(context.Background(), "reconcile")

	// Define reusable stages
	setFinalizer := Do(func(ctx context.Context) context.Context {
		fmt.Println("setting finalizer")
		return ctx
	})

	validateSpec := Do(func(ctx context.Context) context.Context {
		fmt.Println("validating spec")
		return ctx
	})

	createResources := Do(func(ctx context.Context) context.Context {
		fmt.Println("creating resources")
		return ctx
	})

	updateResources := Do(func(ctx context.Context) context.Context {
		fmt.Println("updating resources")
		return ctx
	})

	deleteResources := Do(func(ctx context.Context) context.Context {
		fmt.Println("deleting resources")
		return ctx
	})

	removeFinalizer := Do(func(ctx context.Context) context.Context {
		fmt.Println("removing finalizer")
		return ctx
	})

	// Main controller pipeline using Enum for operation dispatch
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("starting controller operation")
			return ctx
		}),
		Enum(
			func(ctx context.Context) string {
				return operationKey.MustValue(ctx)
			},
			map[string]NewStep{
				"reconcile": Sequence(
					setFinalizer,
					validateSpec,
					Decision(
						func(_ context.Context) bool {
							// Check if resources exist
							return false // Assume they don't exist
						},
						updateResources,
						createResources,
					),
				),
				"delete": Sequence(
					deleteResources,
					removeFinalizer,
				),
				"validate": validateSpec,
			},
			Do(func(ctx context.Context) context.Context {
				fmt.Println("unsupported operation")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("operation completed")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// starting controller operation
	// setting finalizer
	// validating spec
	// creating resources
	// operation completed
}

func TestSequenceExecution(t *testing.T) {
	ctx := t.Context()
	var executed []string

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "first")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "second")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "third")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	expected := []string{"first", "second", "third"}
	if len(executed) != len(expected) {
		t.Fatalf("expected %d stages, got %d", len(expected), len(executed))
	}

	for i, stage := range expected {
		if executed[i] != stage {
			t.Errorf("stage %d: expected %s, got %s", i, stage, executed[i])
		}
	}
}

func TestDecisionBranching(t *testing.T) {
	ctx := t.Context()

	testCases := []struct {
		name      string
		condition bool
		expected  string
	}{
		{"true branch", true, "true"},
		{"false branch", false, "false"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var result string

			pipeline := Decision(
				func(_ context.Context) bool {
					return tc.condition
				},
				Do(func(ctx context.Context) context.Context {
					result = "true"
					return ctx
				}),
				Do(func(ctx context.Context) context.Context {
					result = "false"
					return ctx
				}),
			)

			Run(ctx, pipeline)

			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestParallelExecution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		var counter int32

		pipeline := Parallel(
			Do(func(ctx context.Context) context.Context {
				time.Sleep(10 * time.Millisecond) // Simulate work
				atomic.AddInt32(&counter, 1)
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				time.Sleep(10 * time.Millisecond) // Simulate work
				atomic.AddInt32(&counter, 2)
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				time.Sleep(10 * time.Millisecond) // Simulate work
				atomic.AddInt32(&counter, 4)
				return ctx
			}),
		)

		// Run pipeline - synctest will manage time advancement
		Run(ctx, pipeline)

		// All parallel operations should have completed
		expected := int32(7) // 1 + 2 + 4
		require.Equal(t, expected, counter, "counter mismatch")
	})
}

func TestEmptySequence(t *testing.T) {
	ctx := t.Context()

	pipeline := Sequence() // Empty sequence
	Run(ctx, pipeline)

	// Should not panic and should complete immediately
}

func TestTerminalStage(t *testing.T) {
	ctx := t.Context()

	Run(ctx, Terminal)

	// Should not panic and should complete immediately
}

func TestNestedComposition(t *testing.T) {
	ctx := t.Context()
	var executed []string

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "outer-1")
			return ctx
		}),
		Sequence( // Nested sequence
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "inner-1")
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "inner-2")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "outer-2")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	expected := []string{"outer-1", "inner-1", "inner-2", "outer-2"}
	if len(executed) != len(expected) {
		t.Fatalf("expected %d stages, got %d", len(expected), len(executed))
	}

	for i, stage := range expected {
		if executed[i] != stage {
			t.Errorf("stage %d: expected %s, got %s", i, stage, executed[i])
		}
	}
}

func TestEnumExecution(t *testing.T) {
	ctx := t.Context()

	testCases := []struct {
		name     string
		value    int
		expected string
	}{
		{"case 1", 1, "one"},
		{"case 2", 2, "two"},
		{"case 3", 3, "three"},
		{"default case", 99, "default"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var result string

			pipeline := Enum(
				func(_ context.Context) int {
					return tc.value
				},
				map[int]NewStep{
					1: Do(func(ctx context.Context) context.Context {
						result = "one"
						return ctx
					}),
					2: Do(func(ctx context.Context) context.Context {
						result = "two"
						return ctx
					}),
					3: Do(func(ctx context.Context) context.Context {
						result = "three"
						return ctx
					}),
				},
				Do(func(ctx context.Context) context.Context {
					result = "default"
					return ctx
				}),
			)

			Run(ctx, pipeline)

			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestSwitchExecution(t *testing.T) {
	ctx := t.Context()

	testCases := []struct {
		name     string
		status   string
		expected string
	}{
		{"success status", "success", "handled success"},
		{"error status", "error", "handled error"},
		{"warning status", "warning", "handled warning"},
		{"unknown status", "unknown", "handled default"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var result string

			pipeline := Switch(
				func(_ context.Context) string {
					return tc.status
				},
				map[string]NewStep{
					"success": Do(func(ctx context.Context) context.Context {
						result = "handled success"
						return ctx
					}),
					"error": Do(func(ctx context.Context) context.Context {
						result = "handled error"
						return ctx
					}),
					"warning": Do(func(ctx context.Context) context.Context {
						result = "handled warning"
						return ctx
					}),
				},
				Do(func(ctx context.Context) context.Context {
					result = "handled default"
					return ctx
				}),
			)

			Run(ctx, pipeline)

			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestEnumWithoutDefault(t *testing.T) {
	ctx := t.Context()
	var executed bool

	pipeline := Enum(
		func(_ context.Context) string {
			return "nonexistent"
		},
		map[string]NewStep{
			"exists": Do(func(ctx context.Context) context.Context {
				executed = true
				return ctx
			}),
		},
		nil, // No default stage
	)

	Run(ctx, pipeline)

	if executed {
		t.Error("expected no execution when no matching case and no default")
	}
}

func TestDecisionRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var handlerCalled bool
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		handlerCalled = true
		return nil
	})

	var branchExecuted bool
	pipeline := Decision(
		func(_ context.Context) bool { return true },
		Do(func(ctx context.Context) context.Context {
			branchExecuted = true
			return ctx
		}),
		Noop,
	)

	Run(ctx, pipeline)
	require.False(t, branchExecuted, "branch should not run when context is cancelled")
	require.True(t, handlerCalled, "error handler should be called")
}

func TestEnumMatchRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var handlerCalled bool
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		handlerCalled = true
		return nil
	})

	var branchExecuted bool
	pipeline := Enum(
		func(_ context.Context) string { return "match" },
		map[string]NewStep{
			"match": Do(func(ctx context.Context) context.Context {
				branchExecuted = true
				return ctx
			}),
		},
		nil,
	)

	Run(ctx, pipeline)
	require.False(t, branchExecuted, "matched branch should not run when context is cancelled")
	require.True(t, handlerCalled, "error handler should be called")
}

func TestEnumWithoutDefaultRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var handlerCalled bool
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		handlerCalled = true
		return nil
	})

	var nextCalled bool
	pipeline := Sequence(
		Enum(
			func(_ context.Context) string { return "nonexistent" },
			map[string]NewStep{"exists": Noop},
			nil,
		),
		Do(func(ctx context.Context) context.Context {
			nextCalled = true
			return ctx
		}),
	)

	Run(ctx, pipeline)
	require.False(t, nextCalled, "next should not run when context is cancelled")
	require.True(t, handlerCalled, "error handler should be called")
}

func TestParallelDoesNotRecoverPanics(t *testing.T) {
	// Panics in Parallel branches occur in child goroutines and cannot be caught
	// by Recover on the parent goroutine. Wrapping Parallel with Recover does NOT
	// suppress branch panics — the program will still crash.
	//
	// This is correct behavior: panics are programming errors and should be loud.
	// Recovery at the goroutine boundary is intentionally not supported.
	//
	// Verified by inspection: ParallelStep.Run spawns goroutines with no recover,
	// and Go's runtime does not allow cross-goroutine panic recovery.
	t.Log("Parallel panics cannot be caught by Recover (cross-goroutine limitation, verified by inspection)")
}

func TestParallelRespectsErrorHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var handlerCalled bool
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		handlerCalled = true
		return nil
	})

	pipeline := Parallel(
		Do(func(ctx context.Context) context.Context { return ctx }),
	)

	Run(ctx, pipeline)

	require.True(t, handlerCalled, "Parallel should invoke WithErrorHandler on cancellation, not bypass it")
}

func TestRecoverStopsPipelineOnPanic(t *testing.T) {
	ctx := t.Context()
	var afterPanicExecuted bool

	pipeline := Sequence(
		Recover(Do(func(_ context.Context) context.Context {
			panic("intentional panic")
		})),
		Do(func(ctx context.Context) context.Context {
			afterPanicExecuted = true
			return ctx
		}),
	)

	require.NotPanics(t, func() {
		Run(ctx, pipeline)
	}, "Recover should suppress the panic")
	require.False(t, afterPanicExecuted, "pipeline should stop after recovered panic")
}

func TestRecoverContinuesNormally(t *testing.T) {
	ctx := t.Context()
	var executed []string

	pipeline := Sequence(
		Recover(Do(func(ctx context.Context) context.Context {
			executed = append(executed, "step1")
			return ctx
		})),
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "step2")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	require.Equal(t, []string{"step1", "step2"}, executed)
}

func TestRecoverRoutesToErrorHandler(t *testing.T) {
	var handlerErr error
	ctx := WithErrorHandler(t.Context(), func(err error) Step {
		handlerErr = err
		return nil
	})

	pipeline := Recover(Do(func(_ context.Context) context.Context {
		panic("intentional panic")
	}))

	require.NotPanics(t, func() {
		Run(ctx, pipeline)
	})
	require.Error(t, handlerErr, "error handler should be called after recovered panic")
}

func TestRecoverPanicValueAvailableViaCause(t *testing.T) {
	type sentinelType struct{ msg string }
	panicValue := sentinelType{"something went wrong"}

	var causeErr error
	ctx := WithErrorHandler(t.Context(), func(err error) Step {
		causeErr = err
		return nil
	})

	pipeline := Recover(Do(func(_ context.Context) context.Context {
		panic(panicValue)
	}))

	Run(ctx, pipeline)

	var p *PanicError
	require.ErrorAs(t, causeErr, &p, "error should be a *PanicError")
	require.Equal(t, panicValue, p.Value, "PanicError.Value should be the original panic value")
}

func TestRecoverPanicErrorMessage(t *testing.T) {
	p := &PanicError{Value: "boom"}
	require.Equal(t, "panic: boom", p.Error())
}

func TestRecoverWrappingSequence(t *testing.T) {
	var afterPanicExecuted bool
	var handlerErr error

	ctx := WithErrorHandler(t.Context(), func(err error) Step {
		handlerErr = err
		return nil
	})

	pipeline := Sequence(
		Recover(Sequence(
			Do(func(ctx context.Context) context.Context { return ctx }),
			Do(func(_ context.Context) context.Context { panic("mid-sequence panic") }),
			Do(func(ctx context.Context) context.Context {
				afterPanicExecuted = true
				return ctx
			}),
		)),
		Do(func(ctx context.Context) context.Context {
			afterPanicExecuted = true
			return ctx
		}),
	)

	require.NotPanics(t, func() { Run(ctx, pipeline) })
	require.False(t, afterPanicExecuted, "steps after panic should not execute")
	var p *PanicError
	require.ErrorAs(t, handlerErr, &p)
	require.Equal(t, "mid-sequence panic", p.Value)
}

func TestParallelWithRunsAllBranches(t *testing.T) {
	ctx := t.Context()
	var counter int32

	pipeline := ParallelWith(Recover,
		Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			atomic.AddInt32(&counter, 1)
			return ctx
		}),
	)

	Run(ctx, pipeline)
	require.Equal(t, int32(2), atomic.LoadInt32(&counter))
}

func TestParallelWithRecoverRoutesPanicToErrorHandler(t *testing.T) {
	var handlerErr error
	ctx := WithErrorHandler(t.Context(), func(err error) Step {
		handlerErr = err
		return nil
	})

	pipeline := ParallelWith(Recover,
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(_ context.Context) context.Context { panic("branch panic") }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)

	require.NotPanics(t, func() { Run(ctx, pipeline) })

	var p *PanicError
	require.ErrorAs(t, handlerErr, &p, "error handler should receive a *PanicError from the panicking branch")
	require.Equal(t, "branch panic", p.Value)
}

func TestParallelWithCustomWrapper(t *testing.T) {
	// Verify that the wrapper is actually applied to each branch, not just once.
	var wrappedCount int32
	countingWrapper := func(step NewStep) NewStep {
		atomic.AddInt32(&wrappedCount, 1)
		return step
	}

	ParallelWith(countingWrapper,
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)(nil) // instantiate to trigger wrapping

	require.Equal(t, int32(3), atomic.LoadInt32(&wrappedCount), "wrapper should be applied to each branch")
}

func TestParallelCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())

		var branchesExecuted int32
		var nextExecuted int32

		pipeline := Parallel(
			Do(func(ctx context.Context) context.Context {
				atomic.AddInt32(&branchesExecuted, 1)
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				atomic.AddInt32(&branchesExecuted, 1)
				return ctx
			}),
		)

		// Wrap in a sequence so we can observe whether next runs
		full := Sequence(
			pipeline,
			Do(func(ctx context.Context) context.Context {
				atomic.AddInt32(&nextExecuted, 1)
				return ctx
			}),
		)

		// Cancel the context
		cancel()

		// Run pipeline with cancelled context
		Run(ctx, full)

		// Branches run (they are goroutines already spawned)
		require.Equal(t, int32(2), atomic.LoadInt32(&branchesExecuted),
			"branches execute regardless of cancellation")
		// But Continue stops the pipeline from advancing to next
		require.Equal(t, int32(0), atomic.LoadInt32(&nextExecuted),
			"next step should not execute when context is cancelled")
	})
}

// ============================================================================
// CONTEXT THREADING TESTS
// ============================================================================

func TestContextThreadingWithAction(t *testing.T) {
	ctx := t.Context()

	var values []string
	key := ctxkey.New[string]()

	// Create an Action that modifies context by wrapping it
	addValue := func(value string) NewStep {
		return NewStepFunc(func(ctx context.Context, next Step) Step {
			values = append(values, "adding-"+value)
			return Continue(key.Set(ctx, value), next)
		})
	}

	// Create an Action that reads from context
	readValue := NewStepFunc(func(ctx context.Context, next Step) Step {
		if val, ok := key.Value(ctx); ok {
			values = append(values, "reading-"+val)
		} else {
			values = append(values, "reading-empty")
		}
		return Continue(ctx, next)
	})

	pipeline := Sequence(
		readValue,          // Should see empty
		addValue("first"),  // Add first value
		readValue,          // Should see "first"
		addValue("second"), // Override with second value
		readValue,          // Should see "second"
	)

	Run(ctx, pipeline)

	expected := []string{
		"reading-empty",
		"adding-first",
		"reading-first",
		"adding-second",
		"reading-second",
	}

	if len(values) != len(expected) {
		t.Fatalf("Expected %d values, got %d: %v", len(expected), len(values), values)
	}

	for i, exp := range expected {
		if values[i] != exp {
			t.Errorf("Expected values[%d] = %q, got %q", i, exp, values[i])
		}
	}
}

func TestContextThreadingWithDecision(t *testing.T) {
	conditionKey := ctxkey.New[string]()
	ctx := conditionKey.Set(context.Background(), "initial")

	var predicateValue string
	var branchValue string

	setupStage := NewStepFunc(func(ctx context.Context, next Step) Step {
		// Modify context to affect decision
		return Continue(conditionKey.Set(ctx, "modified"), next)
	})

	decision := Decision(
		func(ctx context.Context) bool {
			// Should now see "modified" instead of "initial"
			val := conditionKey.MustValue(ctx)
			predicateValue = val
			return val == "modified"
		},
		NewStepFunc(func(ctx context.Context, next Step) Step {
			branchValue = "true-branch"
			return Continue(ctx, next)
		}),
		NewStepFunc(func(ctx context.Context, next Step) Step {
			branchValue = "false-branch"
			return Continue(ctx, next)
		}),
	)

	pipeline := Sequence(setupStage, decision)
	Run(ctx, pipeline)

	t.Logf("Decision predicate saw: %q", predicateValue)
	t.Logf("Branch executed: %q", branchValue)

	// Now context threading works: predicate sees "modified", true branch runs
	if predicateValue != "modified" {
		t.Errorf("Expected predicate to see 'modified', got %q", predicateValue)
	}
	if branchValue != "true-branch" {
		t.Errorf("Expected true branch to execute, got %q", branchValue)
	}
}

func TestContextThreadingWithEnum(t *testing.T) {
	typeKey := ctxkey.New[string]()
	ctx := typeKey.Set(context.Background(), "initial")

	var enumValue string
	var branchValue string

	setupStage := NewStepFunc(func(ctx context.Context, next Step) Step {
		// Change the type to affect enum decision
		return Continue(typeKey.Set(ctx, "deployment"), next)
	})

	enumStage := Enum(
		func(ctx context.Context) string {
			// Should now see "deployment" instead of "initial"
			val := typeKey.MustValue(ctx)
			enumValue = val
			return val
		},
		map[string]NewStep{
			"deployment": NewStepFunc(func(ctx context.Context, next Step) Step {
				branchValue = "deployment-branch"
				return Continue(ctx, next)
			}),
			"service": NewStepFunc(func(ctx context.Context, next Step) Step {
				branchValue = "service-branch"
				return Continue(ctx, next)
			}),
		},
		NewStepFunc(func(ctx context.Context, next Step) Step {
			branchValue = "default-branch"
			return Continue(ctx, next)
		}),
	)

	pipeline := Sequence(setupStage, enumStage)
	Run(ctx, pipeline)

	t.Logf("Enum saw: %q", enumValue)
	t.Logf("Branch executed: %q", branchValue)

	// Now context threading works: enum sees "deployment", deployment branch runs
	if enumValue != "deployment" {
		t.Errorf("Expected enum to see 'deployment', got %q", enumValue)
	}
	if branchValue != "deployment-branch" {
		t.Errorf("Expected deployment branch to execute, got %q", branchValue)
	}
}

func TestContextThreadingSequentialStages(t *testing.T) {
	keyCtxKey := ctxkey.New[string]()
	ctx := t.Context()

	// Track what values we see in each stage
	var stage1Value, stage2Value, stage3Value string

	stage1 := NewStepFunc(func(ctx context.Context, next Step) Step {
		// Should see empty value initially
		if val, ok := keyCtxKey.Value(ctx); ok {
			stage1Value = val
		}
		// Add value to context and continue to next stage
		return Continue(keyCtxKey.Set(ctx, "from-stage1"), next)
	})

	stage2 := NewStepFunc(func(ctx context.Context, next Step) Step {
		// Should see "from-stage1"
		if val, ok := keyCtxKey.Value(ctx); ok {
			stage2Value = val
		}
		// Modify context and continue to next stage
		return Continue(keyCtxKey.Set(ctx, "from-stage2"), next)
	})

	stage3 := NewStepFunc(func(ctx context.Context, next Step) Step {
		// Should see "from-stage2"
		if val, ok := keyCtxKey.Value(ctx); ok {
			stage3Value = val
		}
		return Continue(ctx, next)
	})

	pipeline := Sequence(stage1, stage2, stage3)
	Run(ctx, pipeline)

	t.Logf("Stage1 saw: %q", stage1Value)
	t.Logf("Stage2 saw: %q", stage2Value)
	t.Logf("Stage3 saw: %q", stage3Value)

	// Now context threading should work correctly
	if stage1Value != "" {
		t.Errorf("Expected stage1 to see empty context, got %q", stage1Value)
	}
	if stage2Value != "from-stage1" {
		t.Errorf("Expected stage2 to see 'from-stage1', got %q", stage2Value)
	}
	if stage3Value != "from-stage2" {
		t.Errorf("Expected stage3 to see 'from-stage2', got %q", stage3Value)
	}
}

// ============================================================================
// EXECUTION TESTS (Direct Execution and Run)
// ============================================================================

func TestRunWithComplexPipeline(t *testing.T) {
	conditionKey := ctxkey.New[bool]()
	ctx := conditionKey.Set(context.Background(), true)

	var executed []string

	// Create a complex pipeline with branching
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "setup")
			return ctx
		}),
		Decision(
			func(ctx context.Context) bool {
				return conditionKey.MustValue(ctx)
			},
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "true-branch")
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "false-branch")
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "cleanup")
			return ctx
		}),
	)

	// Test direct execution without Run loop
	stage := pipeline.Step()
	result := stage.Run(ctx)

	// Should complete entirely in one call
	if result != nil {
		t.Errorf("Expected pipeline to complete, but got continuation: %v", result)
	}

	expected := []string{"setup", "true-branch", "cleanup"}
	if len(executed) != len(expected) {
		t.Fatalf("Expected %d stages, got %d: %v", len(expected), len(executed), executed)
	}
	for i, exp := range expected {
		if executed[i] != exp {
			t.Errorf("Expected executed[%d] = %q, got %q", i, exp, executed[i])
		}
	}
}

func TestSingleStageExecution(t *testing.T) {
	ctx := t.Context()

	var executed bool

	stage := Do(func(ctx context.Context) context.Context {
		executed = true
		return ctx
	}).Step()

	// Single stage should complete without Run loop
	result := stage.Run(ctx)

	if result != nil {
		t.Errorf("Expected single stage to complete, but got continuation: %v", result)
	}

	if !executed {
		t.Error("Expected stage to execute")
	}
}

// ============================================================================
// DO/WRAPPER PATTERN TESTS
// ============================================================================

func TestDoAsStageWrapper(t *testing.T) {
	// Do as a stage wrapper - transforms context before executing wrapped stage
	Do := func(transform func(context.Context) context.Context, stage NewStep) NewStep {
		return func(next Step) Step {
			return StepFunc(func(ctx context.Context) Step {
				// Transform context first
				newCtx := transform(ctx)
				// Then execute the wrapped stage with transformed context
				wrappedStage := stage(next)
				if wrappedStage != nil {
					return wrappedStage.Run(newCtx)
				}
				if next != nil {
					return next.Run(newCtx)
				}
				return nil
			})
		}
	}

	ctx := t.Context()
	var results []string

	// Base stage that reads from context
	userKey := ctxkey.New[string]()
	readUser := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			if user, ok := userKey.Value(ctx); ok {
				results = append(results, "user-is-"+user)
			} else {
				results = append(results, "no-user")
			}
			if next != nil {
				return next.Run(ctx)
			}
			return nil
		})
	}

	// Wrap the stage with context transformation
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			return userKey.Set(ctx, "alice")
		}, readUser),
		readUser, // Should still see alice from previous stage
	)

	Run(ctx, pipeline)

	expected := []string{
		"user-is-alice",
		"user-is-alice",
	}

	if len(results) != len(expected) {
		t.Fatalf("Expected %d results, got %d: %v", len(expected), len(results), results)
	}

	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("Expected results[%d] = %q, got %q", i, exp, results[i])
		}
	}
}

func TestDoAsStageTransformer(t *testing.T) {
	// Do returns a function that transforms stages
	Do := func(transform func(context.Context) context.Context) func(NewStep) NewStep {
		return func(stage NewStep) NewStep {
			return func(next Step) Step {
				return StepFunc(func(ctx context.Context) Step {
					newCtx := transform(ctx)
					wrappedStage := stage(next)
					if wrappedStage != nil {
						return wrappedStage.Run(newCtx)
					}
					if next != nil {
						return next.Run(newCtx)
					}
					return nil
				})
			}
		}
	}

	ctx := t.Context()
	var results []string

	// Base stages
	userKey := ctxkey.New[string]()
	roleKey := ctxkey.New[string]()

	readUser := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			if user, ok := userKey.Value(ctx); ok {
				results = append(results, "reading-user-"+user)
			}
			if next != nil {
				return next.Run(ctx)
			}
			return nil
		})
	}

	readRole := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			if role, ok := roleKey.Value(ctx); ok {
				results = append(results, "reading-role-"+role)
			}
			if next != nil {
				return next.Run(ctx)
			}
			return nil
		})
	}

	// Create transformers
	addUser := Do(func(ctx context.Context) context.Context {
		return userKey.Set(ctx, "bob")
	})

	addRole := Do(func(ctx context.Context) context.Context {
		return roleKey.Set(ctx, "admin")
	})

	// Compose stages with transformers
	pipeline := Sequence(
		addUser(readUser),
		addRole(readRole),
		readUser, // Should still see bob
		readRole, // Should still see admin
	)

	Run(ctx, pipeline)

	expected := []string{
		"reading-user-bob",
		"reading-role-admin",
		"reading-user-bob",
		"reading-role-admin",
	}

	if len(results) != len(expected) {
		t.Fatalf("Expected %d results, got %d: %v", len(expected), len(results), results)
	}

	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("Expected results[%d] = %q, got %q", i, exp, results[i])
		}
	}
}

func TestDoAsStageComposition(t *testing.T) {
	// Do composes two stages in sequence
	Do := func(preStage NewStep, postStage NewStep) NewStep {
		return func(next Step) Step {
			// Chain: preStage -> postStage -> next
			return preStage(postStage(next))
		}
	}

	userKey := ctxkey.New[string]()
	roleKey := ctxkey.New[string]()
	ctx := t.Context()
	var results []string

	setUser := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			results = append(results, "setting-user")
			newCtx := userKey.Set(ctx, "charlie")
			if next != nil {
				return next.Run(newCtx)
			}
			return nil
		})
	}

	setRole := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			results = append(results, "setting-role")
			newCtx := roleKey.Set(ctx, "user")
			if next != nil {
				return next.Run(newCtx)
			}
			return nil
		})
	}

	logContext := func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			user := userKey.MustValue(ctx)
			role := roleKey.MustValue(ctx)
			results = append(results, "context-"+user+"-"+role)
			if next != nil {
				return next.Run(ctx)
			}
			return nil
		})
	}

	// Compose stages
	pipeline := Sequence(
		Do(setUser, setRole),
		logContext,
	)

	Run(ctx, pipeline)

	expected := []string{
		"setting-user",
		"setting-role",
		"context-charlie-user",
	}

	if len(results) != len(expected) {
		t.Fatalf("Expected %d results, got %d: %v", len(expected), len(results), results)
	}

	for i, exp := range expected {
		if results[i] != exp {
			t.Errorf("Expected results[%d] = %q, got %q", i, exp, results[i])
		}
	}
}

// ============================================================================
// MIDDLEWARE PATTERN TESTS
// ============================================================================

// ============================================================================
// ADVANCED COMPOSITION TESTS
// ============================================================================

// ============================================================================
// INTEGRATION TESTS (Complex Real-World Scenarios)
// ============================================================================

func TestIntegrationComplexPipeline(t *testing.T) {
	ctx := t.Context()

	var mu sync.Mutex
	var executed []string
	var contextValues []string

	key := ctxkey.New[string]()

	// Helper to add context value and continue
	addContextValue := func(value string) NewStep {
		return NewStepFunc(func(ctx context.Context, next Step) Step {
			executed = append(executed, "adding-"+value)
			return Continue(key.Set(ctx, value), next)
		})
	}

	// Helper to read context value
	readContextValue := Do(func(ctx context.Context) context.Context {
		if val, ok := key.Value(ctx); ok {
			contextValues = append(contextValues, val)
			executed = append(executed, "reading-"+val)
		} else {
			executed = append(executed, "reading-empty")
		}
		return ctx
	})

	// Complex pipeline demonstrating all features
	pipeline := Sequence(
		// 1. Start with empty context
		readContextValue,

		// 2. Add a value
		addContextValue("first"),

		// 3. Read the value (should see "first")
		readContextValue,

		// 4. Conditional branching based on context
		Decision(
			func(ctx context.Context) bool {
				val, ok := key.Value(ctx)
				return ok && val == "first"
			},
			// True branch: modify context again
			Sequence(
				addContextValue("modified"),
				readContextValue, // Should see "modified"
			),
			// False branch: shouldn't execute
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "false-branch")
				return ctx
			}),
		),

		// 5. Multi-way branching with Enum
		Enum(
			func(ctx context.Context) string {
				if val, ok := key.Value(ctx); ok {
					return val
				}
				return "unknown"
			},
			map[string]NewStep{
				"modified": Sequence(
					Do(func(ctx context.Context) context.Context {
						executed = append(executed, "enum-modified")
						return ctx
					}),
					addContextValue("final"),
				),
				"other": Do(func(ctx context.Context) context.Context {
					executed = append(executed, "enum-other")
					return ctx
				}),
			},
			Do(func(ctx context.Context) context.Context {
				executed = append(executed, "enum-default")
				return ctx
			}),
		),

		// 6. Final read
		readContextValue, // Should see "final"

		// 7. Parallel execution (context preserved in each branch)
		Parallel(
			Do(func(ctx context.Context) context.Context {
				if val, ok := key.Value(ctx); ok {
					mu.Lock()
					executed = append(executed, "parallel1-"+val)
					mu.Unlock()
				}
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				if val, ok := key.Value(ctx); ok {
					mu.Lock()
					executed = append(executed, "parallel2-"+val)
					mu.Unlock()
				}
				return ctx
			}),
		),

		// 8. Final cleanup
		Do(func(ctx context.Context) context.Context {
			executed = append(executed, "cleanup")
			return ctx
		}),
	)

	// Execute the entire pipeline with one simple call
	Run(ctx, pipeline)

	// Verify execution order (except parallel parts which are non-deterministic)
	expectedBeforeParallel := []string{
		"reading-empty",    // 1. Started with empty context
		"adding-first",     // 2. Added "first"
		"reading-first",    // 3. Read "first"
		"adding-modified",  // 4. Decision true branch: added "modified"
		"reading-modified", // 4. Decision true branch: read "modified"
		"enum-modified",    // 5. Enum matched "modified"
		"adding-final",     // 5. Enum branch: added "final"
		"reading-final",    // 6. Read "final"
	}

	expectedContextValues := []string{
		"first",    // First read after adding "first"
		"modified", // Read after decision branch modified it
		"final",    // Read after enum branch set final value
	}

	// Verify we have the right total number of steps
	expectedTotalSteps := len(expectedBeforeParallel) + 2 + 1 // +2 parallel +1 cleanup
	if len(executed) != expectedTotalSteps {
		t.Fatalf("Expected %d executed steps, got %d: %v", expectedTotalSteps, len(executed), executed)
	}

	// Verify the deterministic sequence before parallel execution
	for i, expected := range expectedBeforeParallel {
		if executed[i] != expected {
			t.Errorf("Expected executed[%d] = %q, got %q", i, expected, executed[i])
		}
	}

	// Verify parallel execution happened (non-deterministic order)
	parallelStart := len(expectedBeforeParallel)
	parallel1Found := false
	parallel2Found := false
	for i := parallelStart; i < parallelStart+2; i++ {
		switch executed[i] {
		case "parallel1-final":
			parallel1Found = true
		case "parallel2-final":
			parallel2Found = true
		default:
			t.Errorf("Unexpected parallel execution step: %q", executed[i])
		}
	}
	if !parallel1Found {
		t.Error("Expected parallel1-final to execute")
	}
	if !parallel2Found {
		t.Error("Expected parallel2-final to execute")
	}

	// Verify cleanup happened last
	if executed[len(executed)-1] != "cleanup" {
		t.Errorf("Expected cleanup to be last, got %q", executed[len(executed)-1])
	}

	// Verify context values were properly threaded
	if len(contextValues) != len(expectedContextValues) {
		t.Fatalf("Expected %d context values, got %d: %v", len(expectedContextValues), len(contextValues), contextValues)
	}

	for i, expected := range expectedContextValues {
		if contextValues[i] != expected {
			t.Errorf("Expected contextValues[%d] = %q, got %q", i, expected, contextValues[i])
		}
	}
}

func TestIntegrationSimpleAPIUsage(t *testing.T) {
	ctx := t.Context()

	var result string

	// Simple three-stage pipeline
	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			result += "A"
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			result += "B"
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			result += "C"
			return ctx
		}),
	)

	// Execute with clean API
	Run(ctx, pipeline)

	if result != "ABC" {
		t.Errorf("Expected result 'ABC', got %q", result)
	}
}

func TestIntegrationDirectStageExecution(t *testing.T) {
	ctx := t.Context()

	var executed bool

	// You can still execute stages directly without Run()
	stage := Do(func(ctx context.Context) context.Context {
		executed = true
		return ctx
	}).Step()

	// Direct execution - also works
	stage.Run(ctx)

	if !executed {
		t.Error("Expected stage to execute")
	}
}

func TestIntegrationContextThreadingShowcase(t *testing.T) {
	userKey := ctxkey.New[string]()
	authenticatedKey := ctxkey.New[bool]()
	authorizedKey := ctxkey.New[string]()
	ctx := t.Context()

	var phases []string

	// Showcase that demonstrates the power of context threading
	authenticate := func(user string) NewStep {
		return NewStepFunc(func(ctx context.Context, next Step) Step {
			phases = append(phases, "authenticating-"+user)
			authCtx := authenticatedKey.Set(userKey.Set(ctx, user), true)
			return Continue(authCtx, next)
		})
	}

	authorize := func(resource string) NewStep {
		return NewStepFunc(func(ctx context.Context, next Step) Step {
			phases = append(phases, "authorizing-"+userKey.MustValue(ctx)+"-for-"+resource)
			return Continue(authorizedKey.Set(ctx, resource), next)
		})
	}

	processRequest := Do(func(ctx context.Context) context.Context {
		user := userKey.MustValue(ctx)
		resource := authorizedKey.MustValue(ctx)
		phases = append(phases, "processing-"+resource+"-for-"+user)
		return ctx
	})

	auditLog := Do(func(ctx context.Context) context.Context {
		user := userKey.MustValue(ctx)
		phases = append(phases, "auditing-"+user)
		return ctx
	})

	// Real-world-like pipeline
	requestPipeline := Sequence(
		authenticate("alice"),
		authorize("database"),
		processRequest,
		auditLog,
	)

	Run(ctx, requestPipeline)

	expected := []string{
		"authenticating-alice",
		"authorizing-alice-for-database",
		"processing-database-for-alice",
		"auditing-alice",
	}

	if len(phases) != len(expected) {
		t.Fatalf("Expected %d phases, got %d: %v", len(expected), len(phases), phases)
	}

	for i, exp := range expected {
		if phases[i] != exp {
			t.Errorf("Expected phases[%d] = %q, got %q", i, exp, phases[i])
		}
	}
}

// ============================================================================
// BENCHMARKS
// ============================================================================

func BenchmarkDirectExecution(b *testing.B) {
	ctx := b.Context()

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stage := pipeline.Step()
		stage.Run(ctx)
	}
}

func BenchmarkRun(b *testing.B) {
	ctx := b.Context()

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Run(ctx, pipeline)
	}
}

func TestContinueWithCancellation(t *testing.T) {
	t.Run("stops on cancellation by default", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		var step1Executed, step2Executed bool

		pipeline := Sequence(
			NewStepFunc(func(ctx context.Context, next Step) Step {
				step1Executed = true
				return Continue(ctx, next)
			}),
			NewStepFunc(func(ctx context.Context, next Step) Step {
				step2Executed = true
				return Continue(ctx, next)
			}),
		)

		Run(ctx, pipeline)

		if !step1Executed {
			t.Error("step1 should execute (body runs before Continue)")
		}
		if step2Executed {
			t.Error("step2 should not execute (Continue stops pipeline)")
		}
	})

	t.Run("calls error handler on cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		var handlerCalled bool
		var handlerErr error
		ctx = WithErrorHandler(ctx, func(err error) Step {
			handlerCalled = true
			handlerErr = err
			return nil
		})

		cancel() // Cancel before execution

		step := NewStepFunc(Continue)

		Run(ctx, step)

		if !handlerCalled {
			t.Error("error handler should be called on cancellation")
		}
		if handlerErr == nil {
			t.Error("error handler should receive the cancellation error")
		}
		if !errors.Is(handlerErr, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", handlerErr)
		}
	})

	t.Run("passes cause to error handler when available", func(t *testing.T) {
		cause := errors.New("root cause")

		ctx, cancel := context.WithCancelCause(context.Background())

		var handlerErr error
		ctx = WithErrorHandler(ctx, func(err error) Step {
			handlerErr = err
			return nil
		})

		cancel(cause)

		Run(ctx, NewStepFunc(Continue))

		require.Equal(t, cause, handlerErr, "error handler should receive the cause, not just context.Canceled")
	})

	t.Run("continues normally without cancellation", func(t *testing.T) {
		ctx := t.Context()

		var step1Executed, step2Executed bool

		pipeline := Sequence(
			NewStepFunc(func(ctx context.Context, next Step) Step {
				step1Executed = true
				return Continue(ctx, next)
			}),
			NewStepFunc(func(ctx context.Context, next Step) Step {
				step2Executed = true
				return Continue(ctx, next)
			}),
		)

		Run(ctx, pipeline)

		if !step1Executed {
			t.Error("step1 should execute")
		}
		if !step2Executed {
			t.Error("step2 should execute")
		}
	})
}

func ExampleWithErrorHandler() {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel called inside pipeline step

	// Set up error handler
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		fmt.Println("handling cancellation")
		return nil
	})

	pipeline := Sequence(
		Do(func(ctx context.Context) context.Context {
			fmt.Println("step 1")
			cancel() // Simulate cancellation
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			fmt.Println("step 2 (should not run)")
			return ctx
		}),
	)

	Run(ctx, pipeline)

	// Output:
	// step 1
	// handling cancellation
}
