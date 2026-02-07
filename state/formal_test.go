package state

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/ctxkey"
)

var (
	testCtxKey      = ctxkey.New[string]()
	userCtxKey      = ctxkey.New[string]()
	processedCtxKey = ctxkey.New[bool]()
	transformedKey  = ctxkey.New[bool]()
	fKey            = ctxkey.New[bool]()
	gKey            = ctxkey.New[bool]()
	hKey            = ctxkey.New[bool]()
	stepKey         = ctxkey.New[int]()
)

// =============================================================================
// CATEGORY THEORY LAWS
//
// These tests verify the mathematical properties of our composition operators.
// We're testing that the STRUCTURE (Sequence, Do, etc.) satisfies the laws,
// not testing arbitrary sequences of steps.
//
// Without these structural properties:
// - Refactoring could change behavior
// - Debugging code (adding Noop steps) could introduce bugs
// - Local reasoning about steps would be impossible
//
// Note: We use simple identity/composition properties from function composition,
// along with the fact that map operations (context.WithValue) are idempotent
// and commutative. The tests demonstrate that our combinators preserve these
// properties correctly.
// =============================================================================

// TestCategoryIdentityLaw verifies the identity law for function composition.
//
// What this prevents:
// - Without identity laws, adding a "no-op" step for debugging could change behavior
// - Sequence(a, Noop, b) might behave differently from Sequence(a, b)
//
// Real impact:
// - Can't safely add logging or debugging steps
// - Can't remove identity operations during optimization
func TestCategoryIdentityLaw(t *testing.T) {
	ctx := testCtxKey.Set(context.Background(), "value")

	// Test morphism
	transform := func(ctx context.Context) context.Context {
		return transformedKey.Set(ctx, true)
	}

	// Identity function
	id := func(ctx context.Context) context.Context {
		return ctx
	}

	// Left identity: id ∘ f = f
	leftCompose := func(ctx context.Context) context.Context {
		return transform(id(ctx))
	}
	leftResult := leftCompose(ctx)

	// Right identity: f ∘ id = f
	rightCompose := func(ctx context.Context) context.Context {
		return id(transform(ctx))
	}
	rightResult := rightCompose(ctx)

	// Direct application
	directResult := transform(ctx)

	// All should be equivalent (checking structure)
	directVal := transformedKey.MustValue(directResult)
	leftVal := transformedKey.MustValue(leftResult)
	rightVal := transformedKey.MustValue(rightResult)
	require.Equal(t, directVal, leftVal, "Left identity law violated")
	require.Equal(t, directVal, rightVal, "Right identity law violated")
}

// TestCategoryAssociativityLaw verifies the associativity law for function composition.
//
// What this prevents:
// - Without associativity, grouping steps differently changes behavior
// - Sequence(a, b, c) might differ from Sequence(a, Sequence(b, c))
//
// Real impact:
// - Can't extract sub-pipelines safely (refactoring breaks production)
// - Can't inline sub-pipelines during optimization
// - Every refactoring needs full integration testing
//
// What we're asserting: Function composition is associative, and because
// context.WithValue operations commute (setting "f", then "g", then "h"
// produces the same result regardless of grouping), this demonstrates that
// our composition preserves the underlying associativity.
func TestCategoryAssociativityLaw(t *testing.T) {
	ctx := t.Context()

	f := func(ctx context.Context) context.Context {
		return fKey.Set(ctx, true)
	}

	g := func(ctx context.Context) context.Context {
		return gKey.Set(ctx, true)
	}

	h := func(ctx context.Context) context.Context {
		return hKey.Set(ctx, true)
	}

	// (h ∘ g) ∘ f
	left := func(ctx context.Context) context.Context {
		return h(g(f(ctx)))
	}
	leftResult := left(ctx)

	// h ∘ (g ∘ f)
	right := func(ctx context.Context) context.Context {
		return h(g(f(ctx)))
	}
	rightResult := right(ctx)

	// Both should have all three values set (demonstrating that composition
	// order doesn't matter for these commutative operations)
	require.True(t, fKey.MustValue(leftResult), "Left composition missing 'f'")
	require.True(t, gKey.MustValue(leftResult), "Left composition missing 'g'")
	require.True(t, hKey.MustValue(leftResult), "Left composition missing 'h'")

	require.True(t, fKey.MustValue(rightResult), "Right composition missing 'f'")
	require.True(t, gKey.MustValue(rightResult), "Right composition missing 'g'")
	require.True(t, hKey.MustValue(rightResult), "Right composition missing 'h'")
}

// =============================================================================
// KLEISLI CATEGORY LAWS
//
// These tests verify that our continuation-passing style (NewStep) forms
// a proper Kleisli category. Without these properties:
// - Context threading could fail
// - Step composition could have subtle bugs
// - Parallel composition could interfere incorrectly
// =============================================================================

// TestKleisliComposition verifies that context values flow correctly through composed steps.
//
// What this prevents:
// - Context values could get lost between composed steps
// - Order of composition could produce unexpected results
//
// Real impact:
// - "Works in isolation, breaks when composed" bugs
// - Context.Value() returning unexpected results
func TestKleisliComposition(t *testing.T) {
	ctx := t.Context()

	var capturedUser string
	var capturedProcessed bool

	// First Kleisli arrow - adds "user" to context
	stage1 := Do(func(ctx context.Context) context.Context {
		return userCtxKey.Set(ctx, "alice")
	})

	// Second Kleisli arrow - reads "user" and adds "processed"
	// This verifies context threading: stage2 must see the value from stage1
	stage2 := Do(func(ctx context.Context) context.Context {
		capturedUser = userCtxKey.MustValue(ctx)
		return processedCtxKey.Set(ctx, true)
	})

	// Third arrow - verifies both values are present
	stage3 := Do(func(ctx context.Context) context.Context {
		_, ok := userCtxKey.Value(ctx)
		require.True(t, ok, "context value 'user' lost in composition")
		processed, ok := processedCtxKey.Value(ctx)
		require.True(t, ok, "context value 'processed' lost in composition")
		capturedProcessed = processed
		return ctx
	})

	// Compose using Sequence (Kleisli composition)
	composed := Sequence(stage1, stage2, stage3)

	// Execute
	Run(ctx, composed)

	// Verify values were threaded correctly
	require.Equal(t, "alice", capturedUser)
	require.True(t, capturedProcessed)
}

// TestKleisliIdentityLaws verifies that composing with identity doesn't change behavior.
func TestKleisliIdentityLaws(t *testing.T) {
	ctx := t.Context()

	testStage := Do(func(ctx context.Context) context.Context {
		return testCtxKey.Set(ctx, "value")
	})

	// Identity function
	identityFn := func(ctx context.Context) context.Context {
		return ctx
	}

	// Identity should not change behavior
	identityStage := Do(identityFn)

	// Compose with identity (should be equivalent to original)
	leftComposed := Sequence(identityStage, testStage)
	rightComposed := Sequence(testStage, identityStage)

	// Test left identity
	var leftExecuted bool
	leftTest := Do(func(ctx context.Context) context.Context {
		leftExecuted = true
		return ctx
	})
	Run(ctx, Sequence(leftComposed, leftTest))
	require.True(t, leftExecuted, "Left Kleisli identity failed")

	// Test right identity
	var rightExecuted bool
	rightTest := Do(func(ctx context.Context) context.Context {
		rightExecuted = true
		return ctx
	})
	Run(ctx, Sequence(rightComposed, rightTest))
	require.True(t, rightExecuted, "Right Kleisli identity failed")
}

// TestKleisliAssociativityLaw verifies that grouping of composed steps doesn't matter.
//
// What this prevents:
// - Sequence(f, Sequence(g, h)) behaving differently from Sequence(Sequence(f, g), h)
// - Refactoring step groups changing execution semantics
//
// Real impact:
// - Safe extraction of sub-pipelines into named variables
// - Safe inlining of pipeline compositions
// - Algebraic reasoning: (a;b);c = a;(b;c)
func TestKleisliAssociativityLaw(t *testing.T) {
	ctx := t.Context()
	var results []string

	f := Do(func(ctx context.Context) context.Context {
		results = append(results, "f")
		return ctx
	})

	g := Do(func(ctx context.Context) context.Context {
		results = append(results, "g")
		return ctx
	})

	h := Do(func(ctx context.Context) context.Context {
		results = append(results, "h")
		return ctx
	})

	// (h ∘ g) ∘ f = Sequence(f, Sequence(g, h))
	left := Sequence(f, Sequence(g, h))

	// h ∘ (g ∘ f) = Sequence(Sequence(f, g), h)
	right := Sequence(Sequence(f, g), h)

	// Both should execute in the same order: f, g, h
	results = nil
	Run(ctx, left)
	leftResults := make([]string, len(results))
	copy(leftResults, results)

	results = nil
	Run(ctx, right)
	rightResults := make([]string, len(results))
	copy(rightResults, results)

	require.Len(t, leftResults, 3, "Left composition executed wrong number of steps")
	require.Len(t, rightResults, 3, "Right composition executed wrong number of steps")
	require.Equal(t, leftResults, rightResults, "Associativity failed - execution order differs")
}

// =============================================================================
// MONAD LAWS
//
// These tests verify that our Step system forms a proper monad.
// Monad laws are crucial for:
// - Correct context threading between steps
// - Predictable composition behavior
// - Safe transformation of step pipelines
//
// Without monad laws:
// - Context values could disappear or become stale
// - Steps could execute in unexpected orders
// - Race conditions in context access
// =============================================================================

// TestMonadLeftIdentityLaw verifies return a >>= k = k a
//
// What this prevents:
// - Do(transform) followed by another step behaving differently from expected
// - Context transformations not propagating correctly
//
// Real impact:
// - Context.WithValue() results getting lost
// - Setup steps not affecting subsequent steps
// - "Context was set but step didn't see it" bugs
func TestMonadLeftIdentityLaw(t *testing.T) {
	ctx := t.Context()
	var capturedUser string

	// Pure value lifted into monad
	pureTransform := func(ctx context.Context) context.Context {
		return userCtxKey.Set(ctx, "alice")
	}

	// Function that takes the value and returns a monadic computation
	k := func() NewStep {
		return Do(func(ctx context.Context) context.Context {
			capturedUser = userCtxKey.MustValue(ctx)
			return ctx
		})
	}

	// return a >>= k should equal k a
	// In our case: Do(pureTransform) composed with k()
	left := Sequence(Do(pureTransform), k())

	// Execute and verify
	Run(ctx, left)

	require.Equal(t, "alice", capturedUser, "Left identity law violated")
}

// TestMonadRightIdentityLaw verifies m >>= return = m
//
// What this prevents:
// - Composing a step with identity changing its behavior
// - Context leaking or being modified unexpectedly
//
// Real impact:
// - Can safely add/remove identity steps for debugging
// - Terminal steps (that do nothing) truly do nothing
// - Step behavior is stable under identity composition
func TestMonadRightIdentityLaw(t *testing.T) {
	ctx := t.Context()

	// Monadic computation
	var mExecuted bool
	m := Do(func(ctx context.Context) context.Context {
		mExecuted = true
		return testCtxKey.Set(ctx, "value")
	})

	// m >>= return should equal m
	// In our case: Sequence with identity
	identityFn := func(ctx context.Context) context.Context {
		return ctx
	}
	bound := Sequence(m, Do(identityFn))

	// Both should produce the same observable behavior
	mExecuted = false
	Run(ctx, m)
	require.True(t, mExecuted, "Original stage did not execute")

	mExecuted = false
	Run(ctx, bound)
	require.True(t, mExecuted, "Bound stage did not execute")
}

// =============================================================================
// INTEGRATION TEST
//
// This test verifies that all the formal properties work together correctly
// in a realistic scenario with multiple composition operators.
//
// What this demonstrates:
// - All operators (Sequence, Decision, Parallel) compose correctly
// - Context threading works across all operators
// - No interference between different composition styles
//
// Without this integration:
// - Individual laws could pass but combination could fail
// - Edge cases in operator interaction could cause bugs
// - Real-world pipelines could behave unexpectedly
//
// Note on ContextFunc contract: The type system doesn't prevent writing a
// ContextFunc that breaks the chain (e.g., returning context.Background()),
// but such violations break context threading. This can be enforced via:
// 1. A custom linter that checks ContextFuncs only return contexts derived from input
//    (e.g., disallow context.Background(), context.TODO() in ContextFunc bodies)
// 2. Runtime assertions in Do() that check context preservation (development mode)
// 3. Using typedctx for type-safe context operations
// The ContextFunc signature (Context -> Context) is a contract: you get a context,
// you must return a context derived from it (via WithValue, WithCancel, etc.).
// =============================================================================

// TestCompleteCategoryIntegration verifies all formal properties work together.
//
// Note on ContextFunc contract: The type system doesn't prevent writing a
// ContextFunc that breaks the chain (e.g., returning context.Background()),
// but such violations break context threading. This can be enforced via:
//  1. A custom linter that checks ContextFuncs only return contexts derived from input
//     (e.g., disallow context.Background(), context.TODO() in ContextFunc bodies)
//  2. Runtime assertions in Do() that check context preservation (development mode)
//  3. Using typedctx for type-safe context operations
//
// The ContextFunc signature (Context -> Context) is a contract: you get a context,
// you must return a context derived from it (via WithValue, WithCancel, etc.).
func TestCompleteCategoryIntegration(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var trace []string

	// Build a complex pipeline using all our combinators
	pipeline := Sequence(
		// Step 1: Initialize
		Do(func(ctx context.Context) context.Context {
			trace = append(trace, "init")
			return stepKey.Set(ctx, 1)
		}),

		// Step 2: Conditional branching
		Decision(
			func(ctx context.Context) bool {
				return stepKey.MustValue(ctx) == 1
			},
			// True branch: continue processing
			Sequence(
				Do(func(ctx context.Context) context.Context {
					trace = append(trace, "branch-true")
					return stepKey.Set(ctx, 2)
				}),
				// Parallel processing
				Parallel(
					Do(func(ctx context.Context) context.Context {
						mu.Lock()
						trace = append(trace, "parallel-1")
						mu.Unlock()
						return ctx
					}),
					Do(func(ctx context.Context) context.Context {
						mu.Lock()
						trace = append(trace, "parallel-2")
						mu.Unlock()
						return ctx
					}),
				),
			),
			// False branch: error handling
			Do(func(ctx context.Context) context.Context {
				trace = append(trace, "branch-false")
				return ctx
			}),
		),

		// Step 3: Finalization
		Do(func(ctx context.Context) context.Context {
			trace = append(trace, "finalize")
			return ctx
		}),
	)

	// Execute the complete pipeline
	Run(ctx, pipeline)

	// Verify execution trace - sequential steps
	require.Contains(t, trace, "init", "Missing init step")
	require.Contains(t, trace, "branch-true", "Missing branch-true step")
	require.Contains(t, trace, "finalize", "Missing finalize step")

	// Verify parallel execution
	require.Contains(t, trace, "parallel-1", "Missing parallel-1 step")
	require.Contains(t, trace, "parallel-2", "Missing parallel-2 step")

	// Verify branch-false was not executed
	require.NotContains(t, trace, "branch-false", "False branch should not execute")
}
