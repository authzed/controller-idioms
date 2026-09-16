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

func TestWhenPredicateTrue(t *testing.T) {
	ctx := t.Context()
	var handlerExecuted, afterExecuted bool

	pipeline := Sequence(
		When(
			func(_ context.Context) bool { return true },
			Do(func(ctx context.Context) context.Context {
				handlerExecuted = true
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			afterExecuted = true
			return ctx
		}),
	)

	Run(ctx, pipeline)

	require.True(t, handlerExecuted)
	require.True(t, afterExecuted, "pipeline continues after a non-terminal When handler")
}

func TestWhenPredicateFalse(t *testing.T) {
	ctx := t.Context()
	var handlerExecuted, afterExecuted bool

	pipeline := Sequence(
		When(
			func(_ context.Context) bool { return false },
			Do(func(ctx context.Context) context.Context {
				handlerExecuted = true
				return ctx
			}),
		),
		Do(func(ctx context.Context) context.Context {
			afterExecuted = true
			return ctx
		}),
	)

	Run(ctx, pipeline)

	require.False(t, handlerExecuted)
	require.True(t, afterExecuted, "pipeline continues when predicate is false")
}

func TestWhenWithTerminalHandlerStopsPipeline(t *testing.T) {
	ctx := t.Context()
	var afterExecuted bool

	pipeline := Sequence(
		When(
			func(_ context.Context) bool { return true },
			Terminal,
		),
		Do(func(ctx context.Context) context.Context {
			afterExecuted = true
			return ctx
		}),
	)

	Run(ctx, pipeline)

	require.False(t, afterExecuted, "terminal handler prevents continuation to subsequent steps")
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

// Parallel installs no recover of its own, so a branch panic crashes the
// process (verified by inspection: ParallelStep.Run has no recover, and Go
// does not allow cross-goroutine recovery). What this test pins is the
// escape hatch documented on Parallel: a caller who wants utilruntime's
// HandleCrash semantics can defer a handler inside the branch, and because
// steps run in continuation-passing style that single defer covers the whole
// branch. Modelled here with a plain recover so state's tests stay free of
// Kubernetes dependencies.
func TestParallelBranchPanicIsHandleableInsideTheBranch(t *testing.T) {
	var mu sync.Mutex
	var observed []any

	// Stand-in for `defer utilruntime.HandleCrashWithContext(ctx)`, minus the
	// re-panic that would take the test process down with it.
	handleCrash := func(step NewStep) NewStep {
		return func(next Step) Step {
			return StepFunc(func(ctx context.Context) Step {
				defer func() {
					if r := recover(); r != nil {
						mu.Lock()
						observed = append(observed, r)
						mu.Unlock()
					}
				}()
				return step(next).Run(ctx)
			})
		}
	}

	var downstreamRan atomic.Bool
	require.NotPanics(t, func() {
		Run(t.Context(), Parallel(Map(handleCrash,
			Do(func(c context.Context) context.Context { return c }),
			Sequence(
				NewStepFunc(func(_ context.Context, _ Step) Step { panic("branch boom") }),
				Do(func(c context.Context) context.Context { downstreamRan.Store(true); return c }),
			),
		)...))
	})

	require.Equal(t, []any{"branch boom"}, observed,
		"a handler deferred inside the branch must see the branch panic")
	require.False(t, downstreamRan.Load(),
		"the rest of the branch must not run after it panics")
}

// The branch handler must also cover panics raised downstream of the wrapped
// step, since CPS runs the continuation inside the same frame.
func TestParallelBranchHandlerCoversDownstreamOfTheBranch(t *testing.T) {
	var caught any
	handleCrash := func(step NewStep) NewStep {
		return func(next Step) Step {
			return StepFunc(func(ctx context.Context) Step {
				defer func() { caught = recover() }()
				return step(next).Run(ctx)
			})
		}
	}

	require.NotPanics(t, func() {
		Run(t.Context(), Parallel(handleCrash(Sequence(
			Do(func(c context.Context) context.Context { return c }),
			NewStepFunc(func(_ context.Context, _ Step) Step { panic("late boom") }),
		))))
	})
	require.Equal(t, "late boom", caught)
}

// A clean Parallel must not panic.
func TestParallelNoPanicOnCleanBranches(t *testing.T) {
	var count atomic.Int32
	pipeline := Parallel(
		Do(func(c context.Context) context.Context { count.Add(1); return c }),
		Do(func(c context.Context) context.Context { count.Add(1); return c }),
	)

	require.NotPanics(t, func() { Run(t.Context(), pipeline) })
	require.Equal(t, int32(2), count.Load())
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

func TestMapAppliesWrapperToEach(t *testing.T) {
	// Map applies the wrapper to each step.
	var wrappedCount int32
	countingWrapper := func(step NewStep) NewStep {
		atomic.AddInt32(&wrappedCount, 1)
		return step
	}

	Map(countingWrapper,
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)

	require.Equal(t, int32(3), atomic.LoadInt32(&wrappedCount), "wrapper should be applied to each step")
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
// AMBIENT DISPATCH TESTS
// ============================================================================

func TestAmbientDispatchNoMiddleware(t *testing.T) {
	// With no middleware registered, AmbientDispatch is transparent
	var executed bool
	pipeline := AmbientDispatch(Do(func(ctx context.Context) context.Context {
		executed = true
		return ctx
	}))
	Run(t.Context(), pipeline)
	require.True(t, executed)
}

func TestAmbientDispatchAppliesMiddleware(t *testing.T) {
	var log []string
	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "after"); return ctx },
	)
	ctx := WithAmbientMiddleware(t.Context(), mw)

	pipeline := AmbientDispatch(Do(func(ctx context.Context) context.Context {
		log = append(log, "step")
		return ctx
	}))
	Run(ctx, pipeline)
	require.Equal(t, []string{"before", "step", "after"}, log)
}

func TestAmbientDispatchPropagatesAcrossSteps(t *testing.T) {
	// Middleware registered before Run fires for every step in the pipeline
	var log []string
	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "after"); return ctx },
	)
	ctx := WithAmbientMiddleware(t.Context(), mw)

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context { log = append(log, "step1"); return ctx }),
		Do(func(ctx context.Context) context.Context { log = append(log, "step2"); return ctx }),
	)
	Run(ctx, pipeline)
	require.Equal(t, []string{
		"before", "step1", "after",
		"before", "step2", "after",
	}, log)
}

func TestAmbientDispatchMidPipelineInjection(t *testing.T) {
	// Middleware registered inside a step applies to subsequent steps only
	var log []string
	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "mw-before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "mw-after"); return ctx },
	)

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step1")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "inject")
			return WithAmbientMiddleware(ctx, mw)
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step3")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step4")
			return ctx
		}),
	)
	Run(t.Context(), pipeline)
	require.Equal(t, []string{
		"step1",
		"inject",
		"mw-before", "step3", "mw-after",
		"mw-before", "step4", "mw-after",
	}, log)
}

func TestAmbientDispatchWorksWithStructStep(t *testing.T) {
	// AmbientDispatch works for steps that are raw StepFuncs (not NewStepFunc),
	// simulating the struct method pattern.
	var log []string
	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "after"); return ctx },
	)
	ctx := WithAmbientMiddleware(t.Context(), mw)

	// Simulate a struct-based step: raw NewStep returning a raw StepFunc
	structStep := NewStep(func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			log = append(log, "struct-step")
			return Continue(ctx, next)
		})
	})

	pipeline := AmbientDispatch(structStep)
	Run(ctx, pipeline)
	require.Equal(t, []string{"before", "struct-step", "after"}, log)
}

func TestAmbientDispatchNamedStepNameVisibleInStepBody(t *testing.T) {
	// Named sets the step name in ctx before the inner step executes.
	// Middleware wrapping the outer Named step fires before the name is set,
	// so middleware "before" hooks see "" for StepName. The name is visible
	// within the step body and to any middleware applied to the inner step.
	var nameInBody string
	pipeline := AmbientDispatch(
		Named("myStep", Do(func(ctx context.Context) context.Context {
			nameInBody = StepName(ctx)
			return ctx
		})),
	)
	Run(t.Context(), pipeline)
	require.Equal(t, "myStep", nameInBody)
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

// A non-nil Step returned by the error handler is executed as a recovery path.
func TestErrorHandlerRecoveryStepRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var cleanupRan bool
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		return StepFunc(func(_ context.Context) Step {
			cleanupRan = true
			return nil
		})
	})

	Run(ctx, NewStepFunc(Continue))
	require.True(t, cleanupRan, "a non-nil Step returned by the error handler must be executed")
}

// The recovery step runs with the handler cleared from context: the context is
// still cancelled, so leaving the handler registered would re-enter it on the
// recovery path's first Continue, forever.
func TestErrorHandlerRecoveryDoesNotReenterHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var handlerCalls int
	var recoveryTrace []string
	ctx = WithErrorHandler(ctx, func(_ error) Step {
		handlerCalls++
		// A recovery path with its own Continue links: the context is still
		// cancelled, so it must stop after the first step body instead of
		// re-entering the handler.
		return Sequence(
			Do(func(ctx context.Context) context.Context {
				recoveryTrace = append(recoveryTrace, "cleanup")
				return ctx
			}),
			Do(func(ctx context.Context) context.Context {
				recoveryTrace = append(recoveryTrace, "unreachable")
				return ctx
			}),
		).Step()
	})

	Run(ctx, NewStepFunc(Continue))
	require.Equal(t, 1, handlerCalls, "recovery must not re-enter the error handler")
	require.Equal(t, []string{"cleanup"}, recoveryTrace,
		"recovery runs under the cancelled context, so Continue stops it after the first step body")
}

// ============================================================================
// MIDDLEWARE TESTS
// ============================================================================

func TestNewStepFuncIsConvenienceWrapper(t *testing.T) {
	// NewStepFunc(fn) should be equivalent to:
	//   func(next Step) Step { return StepFunc(func(ctx context.Context) Step { return fn(ctx, next) }) }
	var called bool
	step := NewStepFunc(func(ctx context.Context, next Step) Step {
		called = true
		return Continue(ctx, next)
	})
	Run(t.Context(), step)
	require.True(t, called)
}

func TestMiddlewareZeroValueIsIdentity(t *testing.T) {
	// Middleware is a sealed type: the zero value is the identity, and Wrap
	// of the zero value returns the step unchanged.
	var mw Middleware
	require.True(t, mw.IsZero())

	var executed bool
	step := Do(func(ctx context.Context) context.Context { executed = true; return ctx })
	Run(t.Context(), mw.Wrap(step))
	require.True(t, executed)
}

func TestMiddlewareWrapsStep(t *testing.T) {
	// A Middleware's hooks fire around the wrapped step.
	var called bool
	mw := Before(func(ctx context.Context) context.Context { called = true; return ctx })

	pipeline := mw.Wrap(Do(func(ctx context.Context) context.Context { return ctx }))
	Run(t.Context(), pipeline)
	require.True(t, called)
}

func TestMapAcceptsMiddlewareWrap(t *testing.T) {
	// Map accepts a Middleware's Wrap method as the wrapper.
	var fired int32
	mw := Before(func(ctx context.Context) context.Context { atomic.AddInt32(&fired, 1); return ctx })

	Run(t.Context(), Sequence(Map(mw.Wrap,
		Do(func(ctx context.Context) context.Context { return ctx }),
		Do(func(ctx context.Context) context.Context { return ctx }),
	)...))

	require.Equal(t, int32(2), atomic.LoadInt32(&fired), "hooks fire once per wrapped step")
}

// Parallel applies the ambient stack inside each branch. This is the default
// because the alternative fails silently for goroutine-local middleware.
func TestParallelAppliesAmbientMiddlewarePerBranch(t *testing.T) {
	var fired atomic.Int32
	mw := Before(func(ctx context.Context) context.Context { fired.Add(1); return ctx })

	noop := Do(func(c context.Context) context.Context { return c })
	Run(WithAmbientMiddleware(t.Context(), mw), Parallel(noop, noop, noop))

	require.Equal(t, int32(3), fired.Load(), "middleware must fire once per branch")
}

// Under AmbientDispatch, a Parallel fires middleware once for the group and
// once more inside each branch — the granularities compose like nested spans.
func TestParallelUnderAmbientDispatchFiresGroupAndBranches(t *testing.T) {
	var fired atomic.Int32
	mw := Before(func(ctx context.Context) context.Context { fired.Add(1); return ctx })

	noop := Do(func(c context.Context) context.Context { return c })
	Run(WithAmbientMiddleware(t.Context(), mw), AmbientDispatch(Parallel(noop, noop)))

	require.Equal(t, int32(3), fired.Load(), "1 for the group + 1 per branch")
}

// Dispatching must stay inert when no middleware is registered.
func TestParallelInertWithoutAmbientMiddleware(t *testing.T) {
	var ran atomic.Int32
	step := Do(func(c context.Context) context.Context { ran.Add(1); return c })

	require.NotPanics(t, func() { Run(t.Context(), Parallel(step, step)) })
	require.Equal(t, int32(2), ran.Load(), "branches still run with no middleware registered")
}

// WithoutAmbientMiddleware is the escape hatch: it clears the stack for
// everything downstream, including Parallel branches reached from it.
func TestWithoutAmbientMiddlewareSeversParallelBranches(t *testing.T) {
	var fired, ran atomic.Int32
	mw := Before(func(ctx context.Context) context.Context { fired.Add(1); return ctx })
	step := Do(func(c context.Context) context.Context { ran.Add(1); return c })

	Run(WithAmbientMiddleware(t.Context(), mw), Sequence(
		Do(WithoutAmbientMiddleware),
		Parallel(step, step, step),
	))

	require.Equal(t, int32(3), ran.Load(), "branches must still run")
	require.Zero(t, fired.Load(), "cleared stack means no middleware anywhere downstream")
}

func TestWithoutAmbientMiddlewareClearsTheStack(t *testing.T) {
	mw := Around(func(ctx context.Context) context.Context { return ctx }, nil)
	ctx := WithAmbientMiddleware(t.Context(), mw)
	require.False(t, AmbientMiddleware(ctx).IsZero())
	require.True(t, AmbientMiddleware(WithoutAmbientMiddleware(ctx)).IsZero())
}

// Clearing is scoped: it must not leak back out to the enclosing context.
func TestWithoutAmbientMiddlewareIsScoped(t *testing.T) {
	var fired atomic.Int32
	mw := Before(func(ctx context.Context) context.Context { fired.Add(1); return ctx })
	noop := Do(func(c context.Context) context.Context { return c })

	ctx := WithAmbientMiddleware(t.Context(), mw)
	// A cleared context used for one subtree leaves the original untouched.
	_ = WithoutAmbientMiddleware(ctx)
	Run(ctx, Parallel(noop, noop))

	require.Equal(t, int32(2), fired.Load(), "the original context still carries middleware")
}

func TestAmbientMiddlewareZeroByDefault(t *testing.T) {
	require.True(t, AmbientMiddleware(t.Context()).IsZero())
}

func TestWithAmbientMiddlewareSingle(t *testing.T) {
	var called bool
	mw := Before(func(ctx context.Context) context.Context { called = true; return ctx })
	ctx := WithAmbientMiddleware(t.Context(), mw)
	got := AmbientMiddleware(ctx)
	require.False(t, got.IsZero())
	Run(t.Context(), got.Wrap(Noop)) // trigger it
	require.True(t, called)
}

// hookPair builds an Around middleware whose hooks append to the given log.
func hookPair(log *[]string, name string) Middleware {
	return Around(
		func(ctx context.Context) context.Context {
			*log = append(*log, name+"-before")
			return ctx
		},
		func(ctx context.Context) context.Context {
			*log = append(*log, name+"-after")
			return ctx
		},
	)
}

func TestWithAmbientMiddlewareComposes(t *testing.T) {
	// Middleware registered first is outermost at pipeline run time.
	// "Outermost" means its before-logic runs first, after-logic runs last.
	var log []string

	ctx := WithAmbientMiddleware(t.Context(), hookPair(&log, "mw1"))
	ctx = WithAmbientMiddleware(ctx, hookPair(&log, "mw2"))

	// AmbientDispatch is required to apply ambient middleware to each step.
	Run(ctx, AmbientDispatch(Do(func(ctx context.Context) context.Context {
		log = append(log, "step")
		return ctx
	})))

	// mw1 registered first = outermost: Compose(mw1, mw2).
	require.Equal(t, []string{"mw1-before", "mw2-before", "step", "mw2-after", "mw1-after"}, log)
}

func TestWithAmbientMiddlewareComposesDirectly(t *testing.T) {
	var log []string

	ctx := WithAmbientMiddleware(t.Context(), hookPair(&log, "mw1"))
	ctx = WithAmbientMiddleware(ctx, hookPair(&log, "mw2"))

	// Apply composed middleware directly to a step and run it
	composed := AmbientMiddleware(ctx)
	require.False(t, composed.IsZero())

	pipeline := composed.Wrap(Do(func(ctx context.Context) context.Context {
		log = append(log, "step")
		return ctx
	}))
	Run(t.Context(), pipeline)

	// mw1 registered first = outermost
	require.Equal(t, []string{"mw1-before", "mw2-before", "step", "mw2-after", "mw1-after"}, log)
}

func TestWithAmbientMiddlewareZeroIsNoOp(t *testing.T) {
	// Registering the zero (identity) Middleware should be a no-op.
	mw := Around(func(ctx context.Context) context.Context { return ctx }, nil)
	ctx := WithAmbientMiddleware(t.Context(), mw)
	ctx2 := WithAmbientMiddleware(ctx, Middleware{})
	// zero is no-op — same middleware still present
	require.False(t, AmbientMiddleware(ctx2).IsZero())
}

func TestAmbientMiddlewareMidPipelineInjection(t *testing.T) {
	// Middleware registered inside a step during execution should
	// affect all subsequent steps.
	var log []string

	mw := hookPair(&log, "mw")

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step1")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			// Inject mid-pipeline
			log = append(log, "inject")
			return WithAmbientMiddleware(ctx, mw)
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step3")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step4")
			return ctx
		}),
	)

	Run(t.Context(), pipeline)
	// step1 and inject are before middleware registration — no wrapping.
	// step3 and step4 get middleware applied. The interpreter brackets each
	// step, so a step's after-hook fires when that step completes, before
	// downstream steps run — per-step attribution.
	require.Equal(t, []string{
		"step1",
		"inject",
		"mw-before", "step3", "mw-after",
		"mw-before", "step4", "mw-after",
	}, log)
}

func TestAroundFiresBeforeAndAfter(t *testing.T) {
	var log []string

	mw := Around(
		func(ctx context.Context) context.Context {
			log = append(log, "before")
			return ctx
		},
		func(ctx context.Context) context.Context {
			log = append(log, "after")
			return ctx
		},
	)

	pipeline := mw.Wrap(Do(func(ctx context.Context) context.Context {
		log = append(log, "step")
		return ctx
	}))

	Run(t.Context(), pipeline)
	require.Equal(t, []string{"before", "step", "after"}, log)
}

func TestAroundNilBeforeOrAfter(t *testing.T) {
	// nil before or after should not panic
	mw := Around(nil, nil)
	pipeline := mw.Wrap(Do(func(ctx context.Context) context.Context { return ctx }))
	require.NotPanics(t, func() { Run(t.Context(), pipeline) })
}

func TestAroundWrapsSequenceAsUnit(t *testing.T) {
	// mw.Wrap(Sequence(a, b)) wraps the whole sequence with one before/after
	// pair. This differs from Sequence(mw.Wrap(a), mw.Wrap(b)) which wraps
	// each step individually. Wrap treats its step argument as a single unit.
	var log []string
	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "after"); return ctx },
	)
	a := Do(func(ctx context.Context) context.Context { log = append(log, "a"); return ctx })
	b := Do(func(ctx context.Context) context.Context { log = append(log, "b"); return ctx })

	Run(t.Context(), mw.Wrap(Sequence(a, b)))
	require.Equal(t, []string{"before", "a", "b", "after"}, log)
}

func TestAmbientMiddlewareFiresAroundSubsequentSteps(t *testing.T) {
	// Middleware injected at step N fires before/after steps N+1, N+2, etc.
	var log []string

	loggingMW := Around(
		func(ctx context.Context) context.Context {
			log = append(log, "before")
			return ctx
		},
		func(ctx context.Context) context.Context {
			log = append(log, "after")
			return ctx
		},
	)

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			return WithAmbientMiddleware(ctx, loggingMW)
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step2")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step3")
			return ctx
		}),
	)

	Run(t.Context(), pipeline)
	require.Equal(t, []string{"before", "step2", "after", "before", "step3", "after"}, log)
}

func TestAmbientMiddlewareNotAppliedBeforeInjection(t *testing.T) {
	// Steps before the injection point are NOT wrapped.
	var log []string

	loggingMW := Before(func(ctx context.Context) context.Context {
		log = append(log, "mw")
		return ctx
	})

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step1")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			return WithAmbientMiddleware(ctx, loggingMW)
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step3")
			return ctx
		}),
	)

	Run(t.Context(), pipeline)
	// mw should appear only around step3, not step1
	require.Equal(t, []string{"step1", "mw", "step3"}, log)
}

func TestNamedStepNameAvailableInStepBody(t *testing.T) {
	// Named sets the step name in ctx before the inner step body executes.
	// The name is visible within the step body via StepName(ctx).
	// Middleware wrapping the outer Named step fires before the name is set,
	// so outer middleware "before" hooks see "". Use Named to annotate steps
	// for structured logging within the step body, not for middleware observability.
	var nameInBody string

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			return WithAmbientMiddleware(ctx, Before(func(ctx context.Context) context.Context { return ctx }))
		}),
		Named("myStep", Do(func(ctx context.Context) context.Context {
			nameInBody = StepName(ctx)
			return ctx
		})),
	)

	Run(t.Context(), pipeline)
	require.Equal(t, "myStep", nameInBody)
}

func TestUnnamedStepHasEmptyName(t *testing.T) {
	// Unnamed steps have StepName == "" — no reflection-based fallback.
	// Use Named() to provide an explicit name for observability.
	var observedName string

	namingMW := Before(func(ctx context.Context) context.Context {
		observedName = StepName(ctx)
		return ctx
	})

	ctx := WithAmbientMiddleware(t.Context(), namingMW)
	Run(ctx, AmbientDispatch(Do(func(ctx context.Context) context.Context { return ctx })))
	require.Empty(t, observedName)
}

func TestNamedPipelineWorksWithoutMiddleware(t *testing.T) {
	// Named should be transparent — pipeline works identically with or without middleware
	var executed bool
	pipeline := Named("myStep", Do(func(ctx context.Context) context.Context {
		executed = true
		return ctx
	}))
	Run(t.Context(), pipeline)
	require.True(t, executed)
}

func TestNamedSetsNameInContext(t *testing.T) {
	var observedName string
	step := Named("myStep", NewStepFunc(func(ctx context.Context, next Step) Step {
		observedName = StepName(ctx)
		return Continue(ctx, next)
	}))
	Run(t.Context(), step)
	require.Equal(t, "myStep", observedName)
}

func TestNamedTransparentWithoutMiddleware(t *testing.T) {
	var executed bool
	pipeline := Named("myStep", Do(func(ctx context.Context) context.Context {
		executed = true
		return ctx
	}))
	Run(t.Context(), pipeline)
	require.True(t, executed)
}

// ============================================================================
// MIDDLEWARE AMBIENT INTEGRATION TESTS
// ============================================================================

func TestMiddlewareAmbient(t *testing.T) {
	// Middleware registered as ambient applies to each subsequent step. The
	// interpreter brackets each step individually, so afters fire per step —
	// not once at the end of the whole remaining pipeline.
	var log []string

	mw := Around(
		func(ctx context.Context) context.Context { log = append(log, "before"); return ctx },
		func(ctx context.Context) context.Context { log = append(log, "after"); return ctx },
	)

	pipeline := AmbientDispatch(
		Do(func(ctx context.Context) context.Context {
			return WithAmbientMiddleware(ctx, mw)
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step2")
			return ctx
		}),
		Do(func(ctx context.Context) context.Context {
			log = append(log, "step3")
			return ctx
		}),
	)

	Run(t.Context(), pipeline)
	require.Equal(t, []string{"before", "step2", "after", "before", "step3", "after"}, log)
}

// ============================================================================
// STEP NAME CAPTURE TESTS (regression: nested clobber + parallel race)
// ============================================================================

// Regression for the capture-clobber bug: when an outer Named step composes an
// inner Named step, observability middleware reading the capture slot must see
// the OUTER (the step it wrapped) name, not the innermost one.
func TestCapturedStepNameUsesOutermostName(t *testing.T) {
	ctx := WithStepNameCapture(t.Context())
	pipeline := Named("outer", Sequence(
		Named("inner", Do(func(c context.Context) context.Context { return c })),
	))
	pipeline.Step().Run(ctx)
	require.Equal(t, "outer", CapturedStepName(ctx))
}

// Regression for the capture-race bug: parallel Named branches sharing one
// capture slot must not race when writing the name. Run under -race.
func TestCapturedStepNameNoRaceUnderParallel(t *testing.T) {
	ctx := WithStepNameCapture(t.Context())
	branches := make([]NewStep, 0, 8)
	for i := 0; i < 8; i++ {
		branches = append(branches, Named("branch", Do(func(c context.Context) context.Context { return c })))
	}
	Parallel(branches...).Step().Run(ctx)
	require.Equal(t, "branch", CapturedStepName(ctx))
}

// Regression for the after-drop bug: the after hook must fire
// even when the wrapped step is terminal (never invokes its continuation).
func TestAroundAfterFiresOnTerminalStep(t *testing.T) {
	var ran []string
	mw := Around(
		func(ctx context.Context) context.Context { ran = append(ran, "before"); return ctx },
		func(ctx context.Context) context.Context { ran = append(ran, "after"); return ctx },
	)
	Run(t.Context(), mw.Wrap(Terminal))
	require.Equal(t, []string{"before", "after"}, ran)
}

// Regression: the after hook must fire even when the wrapped step cancels the
// context (the failure path), so metrics/tracing record the step.
func TestAroundAfterFiresOnCancelledStep(t *testing.T) {
	var ran []string
	mw := Around(
		func(ctx context.Context) context.Context { ran = append(ran, "before"); return ctx },
		func(ctx context.Context) context.Context { ran = append(ran, "after"); return ctx },
	)
	canceller := Do(func(ctx context.Context) context.Context {
		c, cancel := context.WithCancel(ctx)
		cancel()
		return c
	})
	Run(t.Context(), mw.Wrap(canceller))
	require.Equal(t, []string{"before", "after"}, ran)
}
