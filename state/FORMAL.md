# Formal Mathematical Structure of `state`

The state package provides a mathematically rigorous foundation for compositional computation with context threading. This document formalizes the mathematical structure underlying the system and documents why we might care.

## Why These Properties Matter

The mathematical laws guarantee four practical benefits:

**1. Safe Refactoring** - These transformations are guaranteed identical:

```go
Sequence(a, b, c, d)     ≡  Sequence(a, Sequence(b, c), d)  // Extract/inline
Sequence(a, Noop, b)     ≡  Sequence(a, b)                  // Add/remove no-ops
Parallel(a, b, c)        ≡  Parallel(c, a, b)               // Reorder independent ops
```

Without these laws: refactoring could silently break production.

**2. Compositional Testing** - Test parts, trust composition:

- 3 steps = 3 unit tests (with laws) vs 6 integration tests (without laws)
- 10 steps = 10 tests vs 3,628,800 tests
- No "works alone, breaks when composed" bugs

**3. Correct Context Threading** - The monad structure ensures context flows through the pipeline:

```go
Sequence(
    Do(func(ctx) { return context.WithValue(ctx, "user", "alice") }),
    Do(func(ctx) { user := ctx.Value("user") /* guaranteed "alice" */ }),
)
```

Without laws: context values can vanish, causing hard-to-debug failures.

**4. Middleware Correctness** - Kleisli category structure tells us middleware must preserve composition:

- Build middleware from safe combinators (correct by construction)
- Or write property tests verifying endofunctor laws
- No ad-hoc wrappers that break when composed

## Category Theory Foundation

### Basic Category

Our system forms a **category** `𝒞` where:

- **Objects**: Context states (`context.Context`)
- **Morphisms**: Pure context transformations `ContextFunc = func(Context) Context`
- **Identity**: The identity function `func(ctx Context) Context { return ctx }`
- **Composition**: Function composition of `ContextFunc`

#### Category Laws

**Identity Laws**: For any `ContextFunc f`:

- Left identity: composing identity then `f` equals `f`
- Right identity: composing `f` then identity equals `f`

**Associativity**: For `ContextFunc` values `f`, `g`, `h`:

- `(f ∘ g) ∘ h = f ∘ (g ∘ h)` (standard function composition associativity)

These are the basic laws of function composition in Go.

## Monadic Structure

### Step Monad

A **monad** is a design pattern for chaining operations together in a composable way. It provides two key operations:

1. **Unit/Return** (our `Do`): wraps a plain value/function into the monadic context
2. **Bind** (our `Sequence`): chains monadic operations together, handling the "plumbing" automatically

**Why monads matter for us**: They let you write sequential operations (`Sequence(a, b, c)`) where each step can modify context, and the monad handles threading the context through automatically. You don't have to manually pass `ctx` through each step - the monad structure does it for you.

The Step system forms a **monad** with the following operations:

#### Unit (η): Do

```go
// Do is the unit operation - it lifts a pure context transformation into the Step monad
func Do(fn ContextFunc) NewStep
```

In our library, `Do` is the unit/return operation that lifts pure context transformations into the monadic context.

#### Bind (μ): NewStep Composition

```go
// NewStep is itself the bind operation - composing NewSteps is monadic bind
// Given: f NewStep, g NewStep
// Bind is: Sequence(f, g)
func Sequence(steps ...NewStep) NewStep
```

In our library, `NewStep` composition (via `Sequence`) is the bind operation. The `NewStep` type signature `func(next Step) Step` encodes continuation-passing, which is equivalent to monadic bind.

#### Monad Laws

**Left Identity**: `Sequence(Do(id), f) = f` where `id(ctx) = ctx`
**Right Identity**: `Sequence(f, Do(id)) = f` where `id(ctx) = ctx`
**Associativity**: `Sequence(Sequence(f, g), h) = Sequence(f, Sequence(g, h))`

### Kleisli Category

Now that we've defined the Step monad, we can derive its **Kleisli category**.

**Definition**: Given a category `C` and a monad `M` on `C`, the **Kleisli category** `𝒦(M)` is derived as follows:

- **Objects of 𝒦(M)**: Same as objects of `C`
- **Morphisms of 𝒦(M)**: For objects `A` and `B`, a morphism `A → B` in `𝒦(M)` is a morphism `A → M(B)` in `C`
- **Identity in 𝒦(M)**: The monadic unit `η: A → M(A)`
- **Composition in 𝒦(M)**: Given `f: A → M(B)` and `g: B → M(C)`, their Kleisli composition is `g ∘_K f = (μ ∘ M(g) ∘ f): A → M(C)`, where `μ` is the monadic join

**In simpler terms**: The Kleisli category lets you treat "functions that return wrapped values" as if they were regular functions you can compose. Instead of going `A → B → C`, you have functions `A → M(B)` and `B → M(C)`, and the Kleisli category gives you a way to compose them into `A → M(C)`.

**In our system**:

The Step monad gives rise to a **Kleisli category** `𝒦(M)` where:

- **Base category `C`**: Objects are `Context` states, morphisms are `ContextFunc = func(Context) Context`
- **Monad `M`**: The Step monad (as defined above with `Do` and `Sequence`)
- **Objects of 𝒦(M)**: Context states (same as the base category)
- **Kleisli Arrows (morphisms of 𝒦(M))**: `NewStep = func(next Step) Step`
  - In the base category, this would be `Context → M(Context)` where `M` is the Step monad
  - In Go, we encode it as `func(next Step) Step` using continuation-passing style
- **Identity in 𝒦(M)**: `Noop` (corresponds to the monadic unit `Do(id)`)
- **Composition in 𝒦(M)**: `Sequence` (the Kleisli composition operator)

**Why the Kleisli category perspective matters:**

Every monad automatically generates a Kleisli category - so just having one isn't special. What matters is **what we do with the categorical structure**.

The Kleisli category perspective becomes valuable when we have **transformations on `NewStep` that preserve composition**:

**Middleware as Endofunctors**: Functions of type `func(NewStep) NewStep` are endofunctors on the Kleisli category. When middleware preserves the categorical structure (identity and composition), we get:

```go
// Middleware transforms NewSteps while preserving composition
type Middleware func(NewStep) NewStep

// Example: Logging middleware
func LoggingMiddleware(name string) Middleware {
    return func(step NewStep) NewStep {
        return func(next Step) Step {
            return StepFunc(func(ctx context.Context) Step {
                log.Printf("entering %s", name)
                result := step(next).Run(ctx)
                log.Printf("exiting %s", name)
                return result
            })
        }
    }
}

// Middleware composition preserves the categorical laws:
// middleware(Sequence(a, b)) ≈ Sequence(middleware(a), middleware(b))
```

The Kleisli category perspective tells us that **correct middleware must be an endofunctor** - it must preserve identity and composition. While Go's type system doesn't enforce this, the categorical perspective guides us to:

**1. Build safe middleware combinators** that structurally preserve composition:

```go
// This combinator guarantees correct behavior by construction
func MakeMiddleware(
    before func(context.Context) context.Context,
    after func(context.Context) context.Context,
) Middleware {
    return func(step NewStep) NewStep {
        return Sequence(
            Do(before),  // ← Built from composition primitives
            step,        // ← that already satisfy the laws
            Do(after),
        )
    }
}
```

Since `Sequence` and `Do` already satisfy the categorical laws, middleware built from them **inherits** those properties. No manual proof needed - it's correct by construction.

**2. Test endofunctor properties** for custom middleware:

```go
// Property-based test that middleware is an endofunctor
func TestMiddlewarePreservesComposition(t *testing.T, mw Middleware) {
    // Law 1: mw(Sequence(a, b)) ≈ Sequence(mw(a), mw(b))
    // Law 2: mw(Noop) ≈ Noop
}
```

**Without the Kleisli perspective**: We'd write ad-hoc wrappers and hope they work correctly when composed.

**With the Kleisli perspective**: We know middleware must preserve categorical structure, so we either build it from compositional primitives (correct by construction) or write property tests to verify the endofunctor laws.

#### Kleisli Laws

**Identity Laws**: For any NewStep `f`:

- Left identity: `Sequence(Noop, f) = f`
- Right identity: `Sequence(f, Noop) = f`

**Associativity**: For NewSteps `f`, `g`, `h`:

- `Sequence(f, Sequence(g, h)) = Sequence(Sequence(f, g), h)`

These laws are verified in our test suite (see `formal_test.go`).

## Algebraic Operations

These operations give us the algebraic structure to build complex pipelines. They correspond to fundamental categorical constructions.

### Sequential Composition (Product): Sequence

```go
// Sequence executes steps sequentially with context threading
func Sequence(steps ...NewStep) NewStep
```

**What it does**: Chains steps together - output of one becomes input to the next.

**Why "product"**: In category theory, the product represents "and then" - you do the first thing AND THEN the second thing. For Kleisli arrows, this is sequential composition.

**Properties**:

- **Associative**: `Sequence(a, Sequence(b, c)) = Sequence(Sequence(a, b), c)`
  - Grouping doesn't matter: `(a; b); c` is the same as `a; (b; c)`
  - This means you can refactor by extracting/inlining subsequences without changing behavior
- **Identity**: `Sequence(Noop, a) = a` and `Sequence(a, Noop) = a`
  - Adding a no-op doesn't change behavior
  - Safe to add/remove no-ops for debugging

**Why this matters**: You can compose pipelines like functions, and the same algebraic laws apply.

### Choice Composition (Coproduct): Decision

```go
// Decision provides conditional branching (binary choice)
func Decision(predicate func(Context) bool, ifTrue, ifFalse NewStep) NewStep
```

**What it does**: Runs one branch or the other based on a condition - exactly one path executes.

**Why "coproduct"**: In category theory, the coproduct represents "or" - you do the first thing OR the second thing. This is case analysis / conditional branching.

**Properties**:

- **Exhaustive**: Exactly one branch executes (no fall-through)
- **Disjoint**: The branches are independent - each sees the same input context
- **Compositional**: Can nest decisions or put them in sequences

**Why this matters**: Conditional logic composes cleanly with sequential logic:

```go
Sequence(
    validate,
    Decision(isValid, processData, handleError),
    cleanup,  // This runs after whichever branch executes
)
```

### Multi-way Choice: Enum and Switch

```go
// Enum provides multi-way branching on comparable types
func Enum[T comparable](
    selector func(Context) T,
    cases map[T]NewStep,
    defaultHandler NewStep,
) NewStep

// Switch is a convenience wrapper for string-based branching
func Switch(
    selector func(Context) string,
    cases map[string]NewStep,
    defaultHandler NewStep,
) NewStep
```

**What it does**: Generalizes `Decision` to n-way branching - like a switch statement.

**Why it exists**: Real-world logic often has more than two cases. Without `Enum`, you'd nest `Decision` calls:

```go
// Without Enum - messy nested decisions
Decision(
    func(ctx) { return getType(ctx) == "A" },
    handleA,
    Decision(
        func(ctx) { return getType(ctx) == "B" },
        handleB,
        Decision(...) // Gets worse with each case
    )
)

// With Enum - clean and flat
Enum(
    getType,
    map[string]NewStep{
        "A": handleA,
        "B": handleB,
        "C": handleC,
    },
    handleDefault,
)
```

**Why this matters**: Keeps branching logic flat and readable. Each case is at the same level, making the structure clear.

### Parallel Composition: Parallel

```go
// Parallel executes stages concurrently while preserving context
func Parallel(steps ...NewStep) NewStep
```

**What it does**: Runs multiple steps concurrently, waiting for all to complete before continuing.

**Why it exists**: Some operations are independent and can run simultaneously:

```go
Parallel(
    validateSchema,    // These three operations
    checkPermissions,  // don't depend on each other
    logRequest,        // so run them in parallel
)
```

**Important limitation**: Each parallel branch receives the same input context. Context modifications within parallel branches are **not** propagated to subsequent steps or to each other (would cause race conditions).

**Collecting results from parallel branches**: Use `typedctx.Box` to create a shared concurrent-safe space:

```go
import "github.com/authzed/controller-idioms/typedctx"

type ValidationResult struct { Valid bool; Errors []error }
type PermissionsResult struct { Allowed bool; Reason string }

Sequence(
    // Set up boxes before parallel execution
    Do(func(ctx context.Context) context.Context {
        ctx = typedctx.WithBox[ValidationResult](ctx)
        ctx = typedctx.WithBox[PermissionsResult](ctx)
        return ctx
    }),
    // Parallel branches write to their boxes
    Parallel(
        Do(func(ctx context.Context) context.Context {
            result := validateSchema(ctx)
            typedctx.MustStore(ctx, ValidationResult{Valid: result.Valid, Errors: result.Errors})
            return ctx
        }),
        Do(func(ctx context.Context) context.Context {
            allowed, reason := checkPermissions(ctx)
            typedctx.MustStore(ctx, PermissionsResult{Allowed: allowed, Reason: reason})
            return ctx
        }),
    ),
    // After parallel completes, read the results
    Do(func(ctx context.Context) context.Context {
        validation := typedctx.MustValue[ValidationResult](ctx)
        permissions := typedctx.MustValue[PermissionsResult](ctx)
        // Both results are now available
        if !validation.Valid || !permissions.Allowed {
            // handle errors
        }
        return ctx
    }),
)
```

The `Box` is thread-safe, so parallel branches can safely write to it without races.

**Failure Handling in Parallel**:

- **Panics**: Goroutine panics propagate naturally and crash the program (programming errors should be loud). Wrap a step with `Recover(step)` to opt into panic suppression for that specific step.
- **Context Cancellation**: Checked after all branches complete via `Continue`; cancellation stops the pipeline from advancing to the next step but does not prevent branches from running.
- **Errors**: Should be communicated via typedctx.Box or context values, then handled after parallel completes
- **Assumption**: All branches run to completion. Context cancellation stops the continuation but not the in-flight goroutines.

**Use patterns**:

- **Good**: Parallel queries/validations that read context or write to boxes
- **Bad**: Parallel steps that modify shared context values (use Sequence instead)

**Why this matters**:

- Performance: Independent operations run concurrently
- Safety: Isolation + typedctx.Box prevents race conditions
- Compositionality: `Parallel` can be nested in `Sequence`, and vice versa

## Core Abstraction: Do

The `Do` function is the fundamental lifting operation:

```go
// Do lifts a pure context transformation into the Step monad
func Do(fn func(Context) Context) NewStep
```

**Mathematical Significance**:

- **Unit Operation**: Lifts pure transformations into the Step monad (the monadic return)
- **Preserves Composition**: Composing functions then lifting is equivalent to lifting then sequencing
- **Context Threading**: Ensures modified contexts flow to subsequent stages in the pipeline

### Relationship Hierarchy

```text
Level 1: Pure Functions
func(Context) Context

        ↓ Do (Unit/Return)

Level 2: Step Monad
NewStep = func(next Step) Step

        ↓ Composition Operators

Level 3: Complex Workflows
Sequence, Decision, Parallel, etc.
```
