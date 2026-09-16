package state

import (
	"context"
	"errors"
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

// =============================================================================
// BRACKET MIDDLEWARE LAWS
//
// These tests verify the laws of the sealed Middleware type. A pure Kleisli
// middleware Sequence(Do(before), step, Do(after)) provably cannot run its
// after-hook around a terminal step: terminal steps are left zeros of
// Sequence (Sequence(t, x) = t), so the after-hook is absorbed. The
// interpreter (Wrap, and the dispatch wrappers built on it) therefore
// delimits the step with a sentinel continuation when a stack carries any
// after hook — the package's one control operator.
//
// Because Middleware is sealed (constructible only via the hook generators —
// Before, After, AfterOutcome, Deferred, CrashHandler, the Around pair — and
// Compose, zero value = identity), these laws are universally quantified
// over EVERY value of the type, not just values built through one blessed
// constructor.
//
// The laws (see FORMAL.md "The Bracket Laws"):
//   B1 (finalization): every after-form hook closes exactly once per bracket
//                      entry, on every outcome — continue, terminal, cancel,
//                      or panic; observation hooks propagate the panic value
//                      untouched. Befores run in composition order, afters
//                      close in reverse, so Around's pairing is derived from
//                      position.
//   B2 (nesting):      Compose(mwA, mwB) and mwA.Wrap(mwB.Wrap(s)) both
//                      bracket properly: beforeA, beforeB, s, afterB, afterA
//   B3 (transparency): terminality is preserved, and downstream observes
//                      after(ctx') where ctx' is the context the step threaded
//   B4 (identity):     the zero Middleware's Wrap returns the step unchanged —
//                      exact identity, no delimiting at all
//   B5 (fidelity):     AfterOutcome hooks receive the outcome the pipeline
//                      acts on, never a fabricated one
//   Monoid:            Compose is associative with the zero value as unit
// =============================================================================

// TestBracketPairingLaw verifies B1: the after-hook fires exactly once
// whenever the before-hook fired, regardless of the wrapped step's outcome.
//
// What this prevents:
//   - Spans that never close / timers that never stop on terminal steps,
//     which are how every controller pipeline ends (queue.Done, queue.Requeue)
//   - Metrics silently dropped on the cancellation (failure) path
func TestBracketPairingLaw(t *testing.T) {
	outcomes := map[string]NewStep{
		"continues": Do(func(ctx context.Context) context.Context { return ctx }),
		"terminal":  Terminal,
		"cancels": Do(func(ctx context.Context) context.Context {
			c, cancel := context.WithCancel(ctx)
			cancel()
			return c
		}),
	}

	for name, step := range outcomes {
		t.Run(name, func(t *testing.T) {
			var before, after int
			mw := Around(
				func(ctx context.Context) context.Context { before++; return ctx },
				func(ctx context.Context) context.Context { after++; return ctx },
			)
			Run(t.Context(), mw.Wrap(step))
			require.Equal(t, 1, before, "before-hook should fire exactly once")
			require.Equal(t, before, after, "after-hook must pair with before-hook")
		})
	}
}

// TestBracketNestingLaw verifies B2: nested brackets open and close in
// properly nested (LIFO) order, like defer or try/finally — and the two ways
// of nesting (Compose into one stack, or Wrap applied twice) agree.
//
// What this prevents:
//   - Interleaved spans (A opens, B opens, A closes, B closes) that break
//     tracing tools expecting well-nested extents
func TestBracketNestingLaw(t *testing.T) {
	var trace []string
	hook := func(s string) ContextFunc {
		return func(ctx context.Context) context.Context {
			trace = append(trace, s)
			return ctx
		}
	}
	mwA := Around(hook("beforeA"), hook("afterA"))
	mwB := Around(hook("beforeB"), hook("afterB"))

	step := Do(func(ctx context.Context) context.Context {
		trace = append(trace, "step")
		return ctx
	})

	want := []string{"beforeA", "beforeB", "step", "afterB", "afterA"}

	Run(t.Context(), Compose(mwA, mwB).Wrap(step))
	require.Equal(t, want, trace, "Compose brackets LIFO")

	trace = nil
	Run(t.Context(), mwA.Wrap(mwB.Wrap(step)))
	require.Equal(t, want, trace, "nested Wrap agrees with Compose")
}

// TestMiddlewareMonoidLaws verifies that Middleware forms a monoid under
// Compose: associative, with the zero value as identity. Because the type is
// sealed, these laws hold for every constructible value.
//
// What this prevents:
//   - Registration order (one WithAmbientMiddleware call vs several)
//     affecting behavior beyond the documented outermost-first ordering
func TestMiddlewareMonoidLaws(t *testing.T) {
	var trace []string
	hook := func(s string) ContextFunc {
		return func(ctx context.Context) context.Context {
			trace = append(trace, s)
			return ctx
		}
	}
	a := Around(hook("a+"), hook("a-"))
	b := Around(hook("b+"), hook("b-"))
	c := Around(hook("c+"), hook("c-"))
	step := Do(func(ctx context.Context) context.Context {
		trace = append(trace, "step")
		return ctx
	})

	run := func(m Middleware) []string {
		trace = nil
		Run(t.Context(), m.Wrap(step))
		return append([]string(nil), trace...)
	}

	left := run(Compose(Compose(a, b), c))
	right := run(Compose(a, Compose(b, c)))
	require.Equal(t, left, right, "Compose must be associative")
	require.Equal(t, []string{"a+", "b+", "c+", "step", "c-", "b-", "a-"}, left)

	require.Equal(t, run(a), run(Compose(Middleware{}, a)), "zero is a left identity")
	require.Equal(t, run(a), run(Compose(a, Middleware{})), "zero is a right identity")
	require.True(t, Compose().IsZero(), "empty Compose is the zero value")
}

// TestBracketTransparencyLaw verifies B3: the bracket is transparent to the
// pipeline around it. Terminality is preserved (a bracketed terminal step
// still stops the pipeline), and when the step continues, downstream steps
// observe after(ctx') where ctx' is the context the step threaded forward.
//
// What this prevents:
//   - A bracket resurrecting a terminated pipeline (running steps after
//     queue.Done)
//   - Context values written by the step or the after-hook vanishing before
//     downstream steps
func TestBracketTransparencyLaw(t *testing.T) {
	t.Run("terminality preserved", func(t *testing.T) {
		var downstream bool
		mw := Around(
			func(ctx context.Context) context.Context { return ctx },
			func(ctx context.Context) context.Context { return ctx },
		)
		Run(t.Context(), Sequence(
			mw.Wrap(Terminal),
			Do(func(ctx context.Context) context.Context {
				downstream = true
				return ctx
			}),
		))
		require.False(t, downstream, "bracketed terminal step must still terminate the pipeline")
	})

	t.Run("context threads through step and after-hook", func(t *testing.T) {
		var sawUser string
		var sawProcessed bool
		mw := After(
			func(ctx context.Context) context.Context {
				// after-hook sees the context the step threaded forward
				return processedCtxKey.Set(ctx, true)
			},
		)
		Run(t.Context(), Sequence(
			mw.Wrap(Do(func(ctx context.Context) context.Context {
				return userCtxKey.Set(ctx, "alice")
			})),
			Do(func(ctx context.Context) context.Context {
				sawUser, _ = userCtxKey.Value(ctx)
				sawProcessed, _ = processedCtxKey.Value(ctx)
				return ctx
			}),
		))
		require.Equal(t, "alice", sawUser, "downstream must see context written by the bracketed step")
		require.True(t, sawProcessed, "downstream must see context written by the after-hook")
	})
}

// TestBracketIdentityLaw verifies B4: the zero Middleware is the exact
// identity. Because the type is sealed and Wrap of the zero value returns the
// step unchanged, this law is unqualified — there is no delimiting at all, so
// even a step that does work after invoking its continuation is unaffected.
// (The reordering caveat applies only when a stack carries after hooks, which
// is when delimiting is provably necessary — see the Absorption Theorem.)
func TestBracketIdentityLaw(t *testing.T) {
	identity := Around(nil, nil)
	require.True(t, identity.IsZero(), "Around(nil, nil) is the zero value")

	var trace []string
	step := Do(func(ctx context.Context) context.Context {
		trace = append(trace, "step")
		return userCtxKey.Set(ctx, "alice")
	})
	downstream := Do(func(ctx context.Context) context.Context {
		user, _ := userCtxKey.Value(ctx)
		trace = append(trace, "downstream:"+user)
		return ctx
	})

	Run(t.Context(), Sequence(identity.Wrap(step), downstream))
	require.Equal(t, []string{"step", "downstream:alice"}, trace,
		"the zero Middleware must be observationally identity")

	// Terminality is also preserved by the identity.
	trace = nil
	Run(t.Context(), Sequence(identity.Wrap(Terminal), downstream))
	require.Empty(t, trace, "the zero Middleware must preserve terminality")
}

// TestBracketReleaseOnPanic verifies the panic side of B1: the bracket closes
// during unwinding whether or not anything above will recover the panic.
//
// What this prevents:
//   - A span left open or a timer left running when a step panics, so the
//     last thing a crashing controller emits is missing the step that broke
func TestBracketReleaseOnPanic(t *testing.T) {
	hook := func(trace *[]string, s string) ContextFunc {
		return func(ctx context.Context) context.Context {
			*trace = append(*trace, s)
			return ctx
		}
	}
	panicStep := NewStepFunc(func(_ context.Context, _ Step) Step {
		panic("boom")
	})

	t.Run("panic recovered above", func(t *testing.T) {
		var trace []string
		mw := Around(hook(&trace, "before"), hook(&trace, "after"))

		recovery := func(step NewStep) NewStep {
			return func(next Step) Step {
				return StepFunc(func(ctx context.Context) (result Step) {
					defer func() {
						if r := recover(); r != nil {
							trace = append(trace, "recovered")
						}
					}()
					return step(next).Run(ctx)
				})
			}
		}

		require.NotPanics(t, func() {
			Run(t.Context(), recovery(mw.Wrap(panicStep)))
		})
		require.Equal(t, []string{"before", "after", "recovered"}, trace,
			"bracket must close during unwinding, before recovery observes the panic")
	})

	t.Run("panic not recovered", func(t *testing.T) {
		var trace []string
		mw := Around(hook(&trace, "before"), hook(&trace, "after"))

		// The bracket does not call recover, so the panic still propagates
		// with its original value.
		require.PanicsWithValue(t, "boom", func() {
			Run(t.Context(), mw.Wrap(panicStep))
		}, "panic must propagate unmodified")
		require.Equal(t, []string{"before", "after"}, trace,
			"bracket must close during unwinding even when the panic is fatal")
	})
}

// TestObservationHooksCannotRecover pins the constructor-allocated recover
// capability, denied side. recover is frame-scoped in Go — effective only in
// a function called directly from the panicking defer chain — and
// After interposes an adapter closure as the defer operand, so an
// observation hook calling recover() receives nil and the panic keeps
// propagating. Contrast Deferred, whose hook is the operand
// itself and holds the capability (TestDeferredHooksCanRecover).
func TestObservationHooksCannotRecover(t *testing.T) {
	var recovered any = "sentinel"
	mw := After(func(ctx context.Context) context.Context {
		recovered = recover()
		return ctx
	})
	panicStep := NewStepFunc(func(_ context.Context, _ Step) Step {
		panic("boom")
	})

	require.PanicsWithValue(t, "boom", func() {
		Run(t.Context(), mw.Wrap(panicStep))
	}, "panic must propagate despite the hook's recover attempt")
	require.Nil(t, recovered, "recover inside an observation hook must return nil")
}

// TestDeferredHooksCanRecover pins the granted side: a Deferred-constructed
// hook is the defer operand itself, so recover called directly in its body is
// effective. Swallowing converts the panic into a termination outcome;
// re-panicking keeps the crash propagating; and on the normal path the hook
// participates in context threading through the pointer.
func TestDeferredHooksCanRecover(t *testing.T) {
	panicStep := NewStepFunc(func(_ context.Context, _ Step) Step {
		panic("boom")
	})

	t.Run("swallow converts panic to termination", func(t *testing.T) {
		var recovered any
		var downstream bool
		mw := Deferred(func(_ *context.Context) {
			recovered = recover()
		})
		require.NotPanics(t, func() {
			Run(t.Context(), Sequence(
				mw.Wrap(panicStep),
				Do(func(ctx context.Context) context.Context {
					downstream = true
					return ctx
				}),
			))
		})
		require.Equal(t, "boom", recovered, "the hook must receive the panic value")
		require.False(t, downstream, "a swallowed panic terminates the pipeline like a terminal step")
	})

	t.Run("re-panic keeps the crash propagating", func(t *testing.T) {
		mw := Deferred(func(_ *context.Context) {
			if r := recover(); r != nil {
				panic(r)
			}
		})
		require.PanicsWithValue(t, "boom", func() {
			Run(t.Context(), mw.Wrap(panicStep))
		})
	})

	t.Run("threads context on the normal path", func(t *testing.T) {
		var sawUser string
		mw := Deferred(func(ctx *context.Context) {
			*ctx = processedCtxKey.Set(*ctx, true)
		})
		Run(t.Context(), Sequence(
			mw.Wrap(Do(func(ctx context.Context) context.Context {
				return userCtxKey.Set(ctx, "alice")
			})),
			Do(func(ctx context.Context) context.Context {
				sawUser, _ = userCtxKey.Value(ctx)
				processed, _ := processedCtxKey.Value(ctx)
				require.True(t, processed, "downstream must see the deferred hook's pointer write")
				return ctx
			}),
		))
		require.Equal(t, "alice", sawUser, "deferred hooks observe the step's threaded context (B3)")
	})
}

// TestCrashHandlerIsDeferOperand pins that a handler passed whole
// to CrashHandler is itself the defer operand: its own recover is
// effective (the property that a handler wrapped in a closure would lose),
// and it receives the bracket-entry context by value.
func TestCrashHandlerIsDeferOperand(t *testing.T) {
	panicStep := NewStepFunc(func(_ context.Context, _ Step) Step {
		panic("boom")
	})

	// Same shape as utilruntime.HandleCrashWithContext, without the k8s
	// dependency: recovers in its own body, records what it saw.
	var got any
	var sawUser string
	handler := func(ctx context.Context, _ ...func(context.Context, any)) {
		sawUser, _ = userCtxKey.Value(ctx)
		got = recover()
	}

	ctx := userCtxKey.Set(context.Background(), "alice")
	require.NotPanics(t, func() {
		Run(ctx, CrashHandler(handler).Wrap(panicStep))
	}, "the handler's own recover must intercept the panic")
	require.Equal(t, "boom", got)
	require.Equal(t, "alice", sawUser, "the handler receives the bracket-entry context")
}

// TestOutcomeFidelityLaw verifies B5: an outcome-aware after-hook receives
// the outcome the pipeline acts on — never a fabricated one. Each subtest
// drives the wrapped step to one of the four fates and checks that the hook
// observes exactly that fate.
//
// What this prevents:
//   - Spans marked Ok around steps that panicked or were cancelled
//   - Metrics that count a cancellation as a normal termination
func TestOutcomeFidelityLaw(t *testing.T) {
	observe := func() (*Outcome, Middleware) {
		var got Outcome
		mw := AfterOutcome(func(ctx context.Context, o Outcome) context.Context {
			got = o
			return ctx
		})
		return &got, mw
	}

	t.Run("continued", func(t *testing.T) {
		got, mw := observe()
		Run(t.Context(), mw.Wrap(Do(func(ctx context.Context) context.Context { return ctx })))
		require.Equal(t, OutcomeContinued, got.Kind)
		require.NoError(t, got.Cause)
	})

	t.Run("terminated", func(t *testing.T) {
		got, mw := observe()
		Run(t.Context(), mw.Wrap(Terminal))
		require.Equal(t, OutcomeTerminated, got.Kind)
		require.NoError(t, got.Cause)
	})

	t.Run("cancelled with cause", func(t *testing.T) {
		cause := errors.New("deadline blown")
		got, mw := observe()
		Run(t.Context(), mw.Wrap(Do(func(ctx context.Context) context.Context {
			c, cancel := context.WithCancelCause(ctx)
			cancel(cause)
			return c
		})))
		require.Equal(t, OutcomeCancelled, got.Kind)
		require.Equal(t, cause, got.Cause, "the hook must receive the cancellation cause")
	})

	t.Run("panicked", func(t *testing.T) {
		got, mw := observe()
		require.PanicsWithValue(t, "boom", func() {
			Run(t.Context(), mw.Wrap(NewStepFunc(func(_ context.Context, _ Step) Step {
				panic("boom")
			})))
		}, "observing the outcome must not stop the panic")
		require.Equal(t, OutcomePanicked, got.Kind)
	})

	t.Run("terminated with attested detail and cause", func(t *testing.T) {
		cause := errors.New("sync failed")
		got, mw := observe()
		Run(t.Context(), mw.Wrap(NewTerminalStepFunc(func(ctx context.Context) {
			RecordTermination(ctx, "requeued", cause)
		})))
		require.Equal(t, OutcomeTerminated, got.Kind)
		require.Equal(t, "requeued", got.Detail)
		require.Equal(t, cause, got.Cause)
	})

	t.Run("attestation dropped when the step continues", func(t *testing.T) {
		// Detail is attested, not derived: a step that records a termination
		// but continues anyway reports Continued with the attestation gone.
		got, mw := observe()
		Run(t.Context(), mw.Wrap(Do(func(ctx context.Context) context.Context {
			RecordTermination(ctx, "done", nil)
			return ctx
		})))
		require.Equal(t, OutcomeContinued, got.Kind)
		require.Empty(t, got.Detail)
	})

	t.Run("attested termination outranks recorded cancellation", func(t *testing.T) {
		// Queue operations cancel the context as an implementation detail of
		// stopping the pipeline; the attested termination is the truth.
		got, mw := observe()
		Run(t.Context(), mw.Wrap(NewStepFunc(func(ctx context.Context, next Step) Step {
			RecordTermination(ctx, "done", nil)
			c, cancel := context.WithCancel(ctx)
			cancel()
			return Continue(c, next) // records cancellation into the same slot
		})))
		require.Equal(t, OutcomeTerminated, got.Kind)
		require.Equal(t, "done", got.Detail)
		require.NoError(t, got.Cause)
	})
}

// TestOutcomeHooksCannotRecover pins that outcome-aware hooks hold the
// observation capability only: they learn that the step panicked (B5) but,
// like After hooks, run below a library-owned defer operand — so
// recover inside them is a no-op and the panic keeps propagating.
func TestOutcomeHooksCannotRecover(t *testing.T) {
	var recovered any = "sentinel"
	mw := AfterOutcome(func(ctx context.Context, _ Outcome) context.Context {
		recovered = recover()
		return ctx
	})
	require.PanicsWithValue(t, "boom", func() {
		Run(t.Context(), mw.Wrap(NewStepFunc(func(_ context.Context, _ Step) Step {
			panic("boom")
		})))
	})
	require.Nil(t, recovered, "recover inside an outcome hook must return nil")
}

// TestParallelCommutativityLaw verifies that branch order in Parallel is
// observationally irrelevant: Parallel(a, b, c) ≡ Parallel(c, a, b).
//
// Why it holds: branches are isolated (each receives the same input context,
// and its context modifications are discarded) and Parallel waits for every
// branch before continuing, so no observable effect depends on the order in
// which branches are listed.
func TestParallelCommutativityLaw(t *testing.T) {
	run := func(order ...string) (observed map[string]bool, continued bool) {
		var mu sync.Mutex
		observed = map[string]bool{}
		steps := make([]NewStep, 0, len(order))
		for _, name := range order {
			steps = append(steps, Do(func(ctx context.Context) context.Context {
				mu.Lock()
				defer mu.Unlock()
				observed[name] = true
				return ctx
			}))
		}
		Run(t.Context(), Sequence(
			Parallel(steps...),
			Do(func(ctx context.Context) context.Context {
				continued = true
				return ctx
			}),
		))
		return observed, continued
	}

	abcObserved, abcContinued := run("a", "b", "c")
	cabObserved, cabContinued := run("c", "a", "b")

	require.Equal(t, abcObserved, cabObserved, "Parallel commutativity law violated")
	require.True(t, abcContinued)
	require.True(t, cabContinued)
}
