// Package state provides a continuation-passing style framework for building
// composable state machines and processing pipelines.
//
// # Core Concepts
//
// # Step - The Building Block
//
// A Step represents a single operation that processes a context and returns
// the next step to execute (or nil to terminate):
//
//	type Step interface {
//	    Run(context.Context) Step
//	}
//
// Example - Writing a custom Step:
//
//	// A simple step that prints and continues
//	type PrintStep struct {
//	    message string
//	    next    Step
//	}
//
//	func (p *PrintStep) Run(ctx context.Context) Step {
//	    fmt.Println(p.message)
//	    if p.next != nil {
//	        return p.next.Run(ctx)
//	    }
//	    return nil
//	}
//
// # NewStep - The Composition Unit
//
// A NewStep is a factory function that creates a Step when given the next
// step to execute. This enables continuation-passing style composition:
//
//	type NewStep func(next Step) Step
//
// # ContextFunc - The Work Unit
//
// A ContextFunc is a function that does work with context. It takes a context,
// performs operations, and returns the context (potentially modified) so it
// can be threaded through the pipeline:
//
//	type ContextFunc func(context.Context) context.Context
//
// Use with Do to add work to the pipeline.
//
// Example - Creating reusable NewSteps:
//
//	type ctxKey string
//
//	// A NewStep that adds a value to context
//	func AddValue(key ctxKey, value string) NewStep {
//	    return NewStepFunc(func(ctx context.Context, next Step) Step {
//	        fmt.Printf("adding %s=%s to context\n", key, value)
//	        newCtx := context.WithValue(ctx, key, value)
//	        return Continue(newCtx, next)
//	    })
//	}
//
//	// A NewStep that checks a condition
//	func CheckError() NewStep {
//	    return NewStepFunc(func(ctx context.Context, next Step) Step {
//	        if ctx.Err() != nil {
//	            fmt.Println("Error detected, stopping")
//	            return nil
//	        }
//	        return Continue(ctx, next)
//	    })
//	}
//
//	// A NewStep that reads from context
//	func PrintUser() NewStep {
//	    return NewStepFunc(func(ctx context.Context, next Step) Step {
//	        user := ctx.Value(ctxKey("user")).(string)
//	        fmt.Printf("current user: %s\n", user)
//	        return Continue(ctx, next)
//	    })
//	}
//
//	// Compose them - context values flow through the pipeline:
//	pipeline := Sequence(
//	    AddValue(ctxKey("user"), "alice"),
//	    CheckError(),
//	    PrintUser(),  // Can access "user" value set earlier
//	    AddValue(ctxKey("processed"), "true"),
//	)
//	Run(context.Background(), pipeline)
//	// Output:
//	// adding user=alice to context
//	// current user: alice
//	// adding processed=true to context
//
// # Running a Pipeline
//
// Use Run to execute a NewStep pipeline:
//
//	func Run(ctx context.Context, newStep NewStep)
//
// The pipeline executes to completion using continuation-passing style.
//
// # Error Handling
//
// By default, Continue stops the pipeline when context is cancelled.
// You can customize this behavior:
//
//	// Default: stops on cancellation
//	ctx := context.Background()
//	pipeline := Sequence(step1, step2, step3)
//	Run(ctx, pipeline)
//
//	// With error handler: cleanup on cancellation
//	ctx = WithErrorHandler(ctx, func(err error) Step {
//	    log.Printf("pipeline cancelled: %v", err)
//	    return cleanupStep.Step()
//	})
//	Run(ctx, pipeline)
//
// # Helper Functions
//
// The package provides helper functions for common patterns. These are all
// built on top of Step and NewStep:
//
// Basic Helpers:
//   - NewStepFunc(fn) - Create a NewStep from func(context.Context, next Step) Step
//   - Do(fn ContextFunc) - Lift a work function into a pipeline step
//   - Continue(ctx, next) - Run the next step if it exists and context is not cancelled
//   - WithErrorHandler(ctx, handler) - Set an error handler for cancelled contexts
//   - Terminal - Stop pipeline execution
//   - Noop - Continue to next step without doing anything
//
// Composition Operators:
//   - Sequence(...steps) - Execute steps sequentially
//   - Parallel(...steps) - Execute steps concurrently
//   - Decision(predicate, ifTrue, ifFalse) - Binary branching (if/else)
//   - Enum(selector, cases, default) - Multi-way branching on comparable types
//   - Switch(selector, cases, default) - Multi-way branching on strings
//   - Recover(step) - Wrap a step to suppress panics (opt-in; panics propagate by default)
//
// # Choosing a Pattern
//
//	What do you need to do?
//
//	├─ Run steps in order?
//	│  └─ Use: Sequence(a, b, c)
//	│
//	├─ Run steps concurrently?
//	│  └─ Use: Parallel(a, b, c)
//	│
//	├─ Make a binary choice (if/else)?
//	│  └─ Use: Decision(predicate, ifTrue, ifFalse)
//	│
//	├─ Choose from multiple options (switch/case)?
//	│  ├─ String-based? → Use: Switch(selector, cases, default)
//	│  └─ Generic type?  → Use: Enum(selector, cases, default)
//	│
//	└─ Do work in a step?
//	   └─ Use: Do(contextFunc)
//
// # Basic Usage
//
// Simple Sequential Pipeline:
//
//	type ctxKey string
//
//	pipeline := state.Sequence(
//	    state.Do(func(ctx context.Context) context.Context {
//	        fmt.Println("step 1: setting user")
//	        return context.WithValue(ctx, ctxKey("user"), "alice")
//	    }),
//	    state.Do(func(ctx context.Context) context.Context {
//	        user := ctx.Value(ctxKey("user")).(string)
//	        fmt.Printf("step 2: user is %s, adding request ID\n", user)
//	        return context.WithValue(ctx, ctxKey("requestID"), "req-123")
//	    }),
//	    state.Do(func(ctx context.Context) context.Context {
//	        user := ctx.Value(ctxKey("user")).(string)
//	        reqID := ctx.Value(ctxKey("requestID")).(string)
//	        fmt.Printf("step 3: processing request %s for user %s\n", reqID, user)
//	        return ctx
//	    }),
//	)
//
//	state.Run(context.Background(), pipeline)
//	// Output:
//	// step 1: setting user
//	// step 2: user is alice, adding request ID
//	// step 3: processing request req-123 for user alice
//
// Conditional Branching:
//
//	type ctxKey string
//
//	pipeline := state.Sequence(
//	    state.Do(func(ctx context.Context) context.Context {
//	        fmt.Println("checking authorization")
//	        return context.WithValue(ctx, ctxKey("authorized"), true)
//	    }),
//	    state.Decision(
//	        func(ctx context.Context) bool {
//	            return ctx.Value(ctxKey("authorized")).(bool)
//	        },
//	        // True branch - user is authorized
//	        state.Do(func(ctx context.Context) context.Context {
//	            fmt.Println("access granted, setting permissions")
//	            return context.WithValue(ctx, ctxKey("permissions"), "read,write")
//	        }),
//	        // False branch - user is not authorized
//	        state.Do(func(ctx context.Context) context.Context {
//	            fmt.Println("access denied")
//	            return context.WithValue(ctx, ctxKey("permissions"), "none")
//	        }),
//	    ),
//	    state.Do(func(ctx context.Context) context.Context {
//	        perms := ctx.Value(ctxKey("permissions")).(string)
//	        fmt.Printf("final permissions: %s\n", perms)
//	        return ctx
//	    }),
//	)
//
// Parallel Execution:
//
//	type ctxKey string
//
//	pipeline := state.Sequence(
//	    state.Do(func(ctx context.Context) context.Context {
//	        fmt.Println("initializing request context")
//	        ctx = context.WithValue(ctx, ctxKey("requestID"), "req-456")
//	        return context.WithValue(ctx, ctxKey("userID"), "user-789")
//	    }),
//	    state.Parallel(
//	        // Each parallel branch receives the same context
//	        state.Do(func(ctx context.Context) context.Context {
//	            reqID := ctx.Value(ctxKey("requestID")).(string)
//	            fmt.Printf("validating request %s\n", reqID)
//	            return ctx
//	        }),
//	        state.Do(func(ctx context.Context) context.Context {
//	            userID := ctx.Value(ctxKey("userID")).(string)
//	            fmt.Printf("loading user profile %s\n", userID)
//	            return ctx
//	        }),
//	        state.Do(func(ctx context.Context) context.Context {
//	            reqID := ctx.Value(ctxKey("requestID")).(string)
//	            fmt.Printf("logging request %s\n", reqID)
//	            return ctx
//	        }),
//	    ),
//	    state.Do(func(ctx context.Context) context.Context {
//	        reqID := ctx.Value(ctxKey("requestID")).(string)
//	        fmt.Printf("all parallel work complete for %s\n", reqID)
//	        return ctx
//	    }),
//	)
//
// To collect results from parallel branches, use typedctx.Box:
//
//	import "github.com/authzed/controller-idioms/typedctx"
//
//	type ValidationResult struct { Valid bool }
//	type PermissionsResult struct { Allowed bool }
//
//	pipeline := state.Sequence(
//	    // Set up boxes before parallel execution
//	    state.Do(func(ctx context.Context) context.Context {
//	        ctx = typedctx.WithBox[ValidationResult](ctx)
//	        ctx = typedctx.WithBox[PermissionsResult](ctx)
//	        return ctx
//	    }),
//	    // Parallel branches write to their boxes
//	    state.Parallel(
//	        state.Do(func(ctx context.Context) context.Context {
//	            result := validate(ctx)
//	            typedctx.MustStore(ctx, ValidationResult{Valid: result})
//	            return ctx
//	        }),
//	        state.Do(func(ctx context.Context) context.Context {
//	            allowed := checkPermissions(ctx)
//	            typedctx.MustStore(ctx, PermissionsResult{Allowed: allowed})
//	            return ctx
//	        }),
//	    ),
//	    // After parallel completes, read the results
//	    state.Do(func(ctx context.Context) context.Context {
//	        validation := typedctx.MustValue[ValidationResult](ctx)
//	        permissions := typedctx.MustValue[PermissionsResult](ctx)
//	        fmt.Printf("Valid: %v, Allowed: %v\n", validation.Valid, permissions.Allowed)
//	        return ctx
//	    }),
//	)
//
// Multi-way Branching with Enum:
//
//	type ctxKey string
//
//	pipeline := state.Sequence(
//	    state.Do(func(ctx context.Context) context.Context {
//	        // Resource type set by earlier processing
//	        return context.WithValue(ctx, ctxKey("resourceType"), "deployment")
//	    }),
//	    state.Enum(
//	        func(ctx context.Context) string {
//	            return ctx.Value(ctxKey("resourceType")).(string)
//	        },
//	        map[string]state.NewStep{
//	            "deployment": state.Do(func(ctx context.Context) context.Context {
//	                fmt.Println("handling deployment")
//	                return context.WithValue(ctx, ctxKey("replicas"), 3)
//	            }),
//	            "service": state.Do(func(ctx context.Context) context.Context {
//	                fmt.Println("handling service")
//	                return context.WithValue(ctx, ctxKey("port"), 8080)
//	            }),
//	            "configmap": state.Do(func(ctx context.Context) context.Context {
//	                fmt.Println("handling configmap")
//	                return context.WithValue(ctx, ctxKey("dataKeys"), []string{"config.yaml"})
//	            }),
//	        },
//	        // Default case
//	        state.Do(func(ctx context.Context) context.Context {
//	            fmt.Println("unknown resource type")
//	            return ctx
//	        }),
//	    ),
//	    state.Do(func(ctx context.Context) context.Context {
//	        // Values set by the enum branch are available here
//	        if replicas, ok := ctx.Value(ctxKey("replicas")).(int); ok {
//	            fmt.Printf("deployment configured with %d replicas\n", replicas)
//	        }
//	        return ctx
//	    }),
//	)
//
// String-based Branching with Switch:
//
//	pipeline := state.Switch(
//	    func(ctx context.Context) string {
//	        return getOperationPhase(ctx)
//	    },
//	    map[string]state.NewStep{
//	        "pending":   initializationStep,
//	        "running":   monitoringStep,
//	        "completed": cleanupStep,
//	        "failed":    recoveryStep,
//	    },
//	    unknownPhaseStep, // Default case
//	)
//
// # Controller Example
//
// A typical controller pipeline:
//
//	// Main controller pipeline
//	mainPipeline := state.Sequence(
//	    state.Do(func(ctx context.Context) context.Context {
//	        // Set finalizer
//	        return ctx
//	    }),
//	    state.Do(func(ctx context.Context) context.Context {
//	        // Check for safe deletion
//	        return ctx
//	    }),
//	    state.Do(func(ctx context.Context) context.Context {
//	        // Check pause condition
//	        return ctx
//	    }),
//	    state.Decision(
//	        func(ctx context.Context) bool {
//	            return hasDatabaseInstance(ctx)
//	        },
//	        // New style: has database instance
//	        state.Sequence(
//	            ensureMetadata,
//	            createScopedCredentials,
//	        ),
//	        // Old style: direct secret reference
//	        state.Sequence(
//	            adoptExistingSecret,
//	            ensureMetadata,
//	        ),
//	    ),
//	)
//
//	// Execute the pipeline
//	state.Run(ctx, mainPipeline)
//
// # Key Benefits
//
//  1. No ID Management: Direct references eliminate the need for step IDs and lookup
//  2. Simpler Composition: Steps compose naturally without complex builder patterns
//  3. Type Safety: Factory functions provide compile-time safety
//  4. Multi-way Branching: Enum and Switch operators handle complex branching logic elegantly
//  5. Clear Control Flow: Continuation-passing makes execution flow explicit
//  6. Reusability: Steps can be easily reused across different pipelines
//  7. Testability: Individual steps and compositions are easy to test
//
// # Integration with Queue Operations
//
// The state package works seamlessly with the existing queue operations:
//
//	import "github.com/authzed/controller-idioms/queue"
//
//	validationStep := state.Do(func(ctx context.Context) context.Context {
//	    if err := validateResource(ctx); err != nil {
//	        queue.NewQueueOperationsCtx().RequeueErr(ctx, err)
//	        return ctx
//	    }
//	    // Continue processing...
//	    return ctx
//	})
//
// The continuation-passing style provides a clean, functional approach to
// building complex state machines.
package state
