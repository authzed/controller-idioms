// Package state contains Step units used to compose reconciliation loops
// for controllers.
//
// You write functions or types that implement the Step interface, and
// then compose them via NewStep functions and composition operators.
//
// NewStep functions are used for creating Step instances, and Handlers
// are the things that actually process requests - each step returns
// the next step to execute, or nil to terminate.
//
// Writing controllers in this style permits composition patterns including Sequence,
// Parallel, Decision, and other operations like Map and Bind.
//
// # Why Context?
//
// This package uses context.Context as the state container instead of a custom
// type or generic map for several reasons:
//
//  1. **Integration**: Controllers already use context.Context for cancellation,
//     deadlines, and request-scoped values. Using it here means zero friction.
//
//  2. **Standardization**: Context is Go's standard way to pass request-scoped
//     data. Using it makes the pattern immediately familiar to Go developers.
//
//  3. **Immutability**: context.WithValue() returns a new context, encouraging
//     immutable transformations (though this isn't enforced - see ContextFunc docs).
//
//  4. **Ecosystem**: Existing middleware, logging, tracing, and client libraries
//     already understand context.Context.
//
//  5. **Cancellation**: Built-in support for cancellation and deadlines, crucial
//     for long-running controller operations.
//
// Think of Context as a "sufficiently capable state container" rather than as
// purely a cancellation mechanism. For type-safe operations, use typedctx.
package state

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/authzed/ctxkey"
)

// Step represents a step in a processing pipeline.
// Each step processes a context and returns the next step to execute, or nil to terminate.
type Step interface {
	Run(context.Context) Step
}

// StepFunc is a function type that implements Step
type StepFunc func(ctx context.Context) Step

func (f StepFunc) Run(ctx context.Context) Step {
	return f(ctx)
}

// ContextFunc is a function that does work with context.
// It takes a context, performs operations, and returns the (potentially modified) context
// so it can be threaded through the pipeline.
type ContextFunc func(context.Context) context.Context

// NewStep is a function that creates a Step when given the next step to execute.
// This is the basic building block for composing step pipelines using continuation-passing style.
type NewStep func(next Step) Step

// Middleware is an opaque, immutable stack of paired before/after hooks that
// dispatch applies around each step. The zero value is the identity
// middleware, and Compose makes Middleware a monoid.
//
// Middleware is sealed: the only constructors are the hook generators —
// Before, After, AfterOutcome, Deferred, and CrashHandler, plus the Around
// pair — and Compose. This is what makes the laws in FORMAL.md ("The
// Bracket Laws") theorems about the type rather than conventions for its
// users — a Middleware value is hook data, not code, so it cannot invoke a
// step twice, substitute a continuation, or resurrect a terminated
// pipeline. The one control capability a hook can hold is scoped and
// constructor-chosen: Deferred and CrashHandler hooks run on the unwinding
// defer chain and may recover a panic (converting it into a termination, or
// re-raising it), while Before/After/AfterOutcome hooks are pure
// observations and cannot. Everything else lives in the interpreter (see
// Wrap and AmbientDispatch).
//
// For step transformations that hooks cannot express — retries, panic
// recovery, custom scoping — write a plain func(NewStep) NewStep and apply it
// directly or with Map. Such wrappers are ordinary composition: they make no
// law claims and do not ride the ambient dispatch path.
//
// middleware.Log (in state/middleware) is an example of a Middleware.
type Middleware struct {
	// hooks[0] is outermost: its before hook runs first, its after hook last.
	hooks []hook
}

// hook is one paired before/after unit. Any field may be nil. The after
// forms are stored so runBracketed can defer them directly; the constructors
// decide whether the operand is a library adapter (After: observation-only)
// or the user's own function (Deferred and CrashHandler: on the defer
// chain, recover-capable).
type hook struct {
	before ContextFunc
	after  func(*context.Context)
	// outcomeAfter is an observation hook that also receives the Outcome of
	// the wrapped step (see AfterOutcome). Called via a library
	// closure, so it cannot recover.
	outcomeAfter func(context.Context, Outcome) context.Context
	// crashAfter is deferred with the bracket-entry context as a value.
	// Its shape matches crash-handler conventions (see CrashHandler).
	crashAfter func(context.Context, ...func(context.Context, any))
}

// hasAfter reports whether the hook carries any after form.
func (h hook) hasAfter() bool {
	return h.after != nil || h.outcomeAfter != nil || h.crashAfter != nil
}

// OutcomeKind classifies how a bracketed step's extent ended.
type OutcomeKind int

const (
	// OutcomeContinued: the step invoked its continuation; the pipeline
	// carries on downstream.
	OutcomeContinued OutcomeKind = iota
	// OutcomeTerminated: the step ended the pipeline deliberately (a
	// terminal step such as Terminal, queue.Done, or queue.Requeue).
	OutcomeTerminated
	// OutcomeCancelled: the step stopped because its context was cancelled.
	OutcomeCancelled
	// OutcomePanicked: the step panicked; the hook observing this outcome is
	// running while the panic unwinds.
	OutcomePanicked
)

// String returns a stable lowercase label suitable for metrics and logs.
func (k OutcomeKind) String() string {
	switch k {
	case OutcomeContinued:
		return "continued"
	case OutcomeTerminated:
		return "terminated"
	case OutcomeCancelled:
		return "cancelled"
	case OutcomePanicked:
		return "panicked"
	default:
		return "unknown"
	}
}

// Outcome reports how a bracketed step's extent ended, as the pipeline
// itself acted on it (the fidelity law, B5 in FORMAL.md).
type Outcome struct {
	Kind OutcomeKind
	// Cause is the cancellation cause when Kind is OutcomeCancelled, or the
	// error recorded by the terminal operation (e.g. queue.RequeueErr) when
	// Kind is OutcomeTerminated with a Detail. The panic value is
	// deliberately not exposed here: observation hooks run during the unwind
	// without recovering, so the value remains attached to the propagating
	// panic.
	Cause error
	// Detail is a terminal operation's self-reported refinement of a
	// Terminated outcome — "done" or "requeued" from the queue package —
	// and empty otherwise. See RecordTermination.
	Detail string
}

// outcomeCapture is the slot a bracket plants in its step's context so the
// step's extent can report how it ended: the cancelled context and the
// terminal operation's label are both produced inside the step and would
// otherwise never escape it. The pointers are atomic because Parallel
// branches sharing the step's context may report concurrently; the first
// writer wins.
type outcomeCapture struct {
	cancelCause atomic.Pointer[error]
	term        atomic.Pointer[termination]
}

// termination is a terminal operation's attested outcome refinement.
type termination struct {
	detail string
	cause  error
}

var outcomeCaptureKey = ctxkey.New[*outcomeCapture]()

// recordCancellation fills the nearest outcome-capture slot with the
// cancellation cause, if a bracket planted one. Called by Continue on the
// cancellation path.
func recordCancellation(ctx context.Context, cause error) {
	if slot, ok := outcomeCaptureKey.Value(ctx); ok && slot != nil {
		if cause == nil {
			cause = context.Canceled
		}
		slot.cancelCause.CompareAndSwap(nil, &cause)
	}
}

// RecordTermination annotates the enclosing bracket's outcome: the pipeline
// is being terminated deliberately, with a short label for observability
// ("done", "requeued") and an optional error. Outcome-aware hooks then
// receive Outcome{Kind: OutcomeTerminated, Detail: detail, Cause: cause}
// instead of a bare termination.
//
// Detail is attested, not derived: the interpreter reports it only when the
// step in fact terminated — a step that records a termination and then
// continues anyway reports OutcomeContinued and the attestation is dropped.
// An attested termination also takes precedence over a recorded
// cancellation, because queue operations cancel the context as an
// implementation detail of stopping the pipeline.
//
// RecordTermination is a no-op when no outcome-aware hook is registered
// around the step. The queue package calls it from its OperationsContext
// methods, so queue.Done, queue.Requeue, and hand-rolled steps that invoke
// queue operations directly are all annotated without further wiring.
func RecordTermination(ctx context.Context, detail string, cause error) {
	if slot, ok := outcomeCaptureKey.Value(ctx); ok && slot != nil {
		slot.term.CompareAndSwap(nil, &termination{detail: detail, cause: cause})
	}
}

// Before returns a Middleware with a single before hook: fn runs ahead of
// the wrapped step, and the context it returns threads into the step. A nil
// fn returns the zero (identity) Middleware.
func Before(fn ContextFunc) Middleware {
	if fn == nil {
		return Middleware{}
	}
	return Middleware{hooks: []hook{{before: fn}}}
}

// After returns a Middleware with a single after hook: fn runs once the
// wrapped step completes — including when the step is terminal, cancels the
// context, or panics, none of which invoke the continuation — so finalizers
// (stop a timer, end a span, record a metric) always fire.
//
// The hook is released by a defer, so it also runs while a panic unwinds. It
// is observation-only: a library adapter, not fn itself, is the defer
// operand, so recover inside fn is a no-op and the panic propagates with its
// value and stack untouched (see Deferred for the recover-capable form). As
// with any finalizer, a hook that itself panics masks the original panic.
//
// fn runs for its side effects: the context it returns is threaded to
// subsequent steps only when the wrapped step continued the pipeline.
//
// In a composed stack, before hooks execute in composition order and
// after-form hooks close in reverse composition order (see Compose). A nil
// fn returns the zero (identity) Middleware.
func After(fn ContextFunc) Middleware {
	if fn == nil {
		return Middleware{}
	}
	return Middleware{hooks: []hook{{
		after: func(p *context.Context) { *p = fn(*p) },
	}}}
}

// Around returns the classic paired bracket — Compose(Before(before),
// After(after)): before runs ahead of the step, and after is guaranteed once
// the step completes, on every outcome. Either hook may be nil; if both are
// nil the result is the zero (identity) Middleware.
func Around(before, after ContextFunc) Middleware {
	return Compose(Before(before), After(after))
}

// AfterOutcome returns a Middleware with a single after hook that also
// receives the Outcome of the wrapped step: continued, terminated (with any
// attested detail), cancelled (with the cancellation cause), or panicked.
// Use it for observability that should classify results — span statuses,
// outcome-labeled metrics, log levels that escalate on failure.
//
// AfterOutcome hooks hold the same capability as After hooks: observation
// only. On the panic path the hook runs while the panic unwinds with Kind
// OutcomePanicked, but the panic value is not exposed and recover inside the
// hook is a no-op — interception requires Deferred or CrashHandler.
//
// As with After, fn runs for its side effects: its returned context is
// threaded onward only when the step continued. A nil fn returns the zero
// (identity) Middleware.
func AfterOutcome(fn func(context.Context, Outcome) context.Context) Middleware {
	if fn == nil {
		return Middleware{}
	}
	return Middleware{hooks: []hook{{outcomeAfter: fn}}}
}

// Deferred returns a Middleware whose single after hook is the defer
// operand itself: it runs as `defer fn(&ctx)` around the wrapped step, so
// the hook sits on the panicking defer chain and a recover() written
// directly in its body is effective. This is a constructor for hand-written
// crash-handler middleware; registered ambient, it covers every Parallel
// branch on the branch's own goroutine.
//
// Semantics beyond After hooks:
//
//   - recover() in the hook body intercepts a panicking step. Recovering
//     without re-panicking converts the panic into a termination — the
//     pipeline stops as if the step were terminal, and responsibility for
//     the abandoned work is the hook's. Re-panic (panic(r)) to observe the
//     panic and keep the crash loud.
//   - The recover must appear literally in the hook body. Go makes recover
//     effective only when called directly by the deferred function, so
//     helpers that recover internally silently do nothing when called from
//     the hook. To use such a helper (utilruntime.HandleCrashWithContext,
//     for example) as the hook itself, see CrashHandler, whose hook
//     signature matches it directly.
//   - On the normal path the hook still runs (recover returns nil when no
//     panic is active); read and write the context through the pointer,
//     which observes the context the step threaded forward (B3).
//
// A nil fn returns the zero (identity) Middleware.
func Deferred(fn func(*context.Context)) Middleware {
	if fn == nil {
		return Middleware{}
	}
	return Middleware{hooks: []hook{{after: fn}}}
}

// CrashHandler is the deferred-hook variant whose signature matches
// crash-handler conventions: the hook receives the bracket-entry context by
// value and is deferred directly, so a recover() in its body — or in the
// body of a handler passed whole, since the handler itself is the defer
// operand — is effective.
//
// The shape is chosen so that utilruntime.HandleCrashWithContext is
// assignable as-is (the variadic parameter is why a plain func(ctx) variant
// would not accept it):
//
//	ctx = state.WithAmbientMiddleware(ctx, state.CrashHandler(utilruntime.HandleCrashWithContext))
//
// This must be the whole hook, not a call inside one: recover is effective
// only in the deferred function itself, so wrapping the handler in a closure
// silently disables it.
//
// Unlike Deferred's pointer hook, the context is passed by value as observed
// at bracket entry — the hook cannot amend the forwarded context, which
// suits crash handlers: on the panic path there is no threaded context
// anyway, and the entry context carries the loggers and values the handler
// needs.
//
// A nil fn returns the zero (identity) Middleware.
func CrashHandler(fn func(context.Context, ...func(context.Context, any))) Middleware {
	if fn == nil {
		return Middleware{}
	}
	return Middleware{hooks: []hook{{crashAfter: fn}}}
}

// Compose combines middleware into one stack. The first argument is outermost
// — its before hook fires first and its after hook last — matching the
// registration order of WithAmbientMiddleware. In general: before hooks
// execute in composition order, and after-form hooks close in reverse
// composition order, like nested defers. Compose is associative and the
// zero Middleware is its identity, making Middleware a monoid; these laws are
// verified in formal_test.go.
func Compose(mws ...Middleware) Middleware {
	var hooks []hook
	for _, m := range mws {
		hooks = append(hooks, m.hooks...)
	}
	return Middleware{hooks: hooks}
}

// IsZero reports whether m is the identity middleware (no hooks).
func (m Middleware) IsZero() bool { return len(m.hooks) == 0 }

// Wrap applies the middleware to a single step: m's before hooks run
// (outermost first), then the step, then m's after hooks (innermost first),
// with the after hooks guaranteed on every outcome — continue, terminal,
// cancel, or panic unwind. Wrap is the interpreter for Middleware, and it
// uses the least machinery each stack requires:
//
//   - zero middleware: returns step unchanged — exact identity.
//   - before hooks only: stays in the pure algebra — the result is literally
//     Sequence(Do(before)..., step), so it inherits the Kleisli laws.
//   - any after hook: delimits the step with the package's one control
//     operator (see runBracketed), because a finalizer that survives terminal
//     steps is not expressible by Kleisli composition (the Absorption Theorem
//     in FORMAL.md).
func (m Middleware) Wrap(step NewStep) NewStep {
	if len(m.hooks) == 0 {
		return step
	}
	hasAfter := false
	for _, h := range m.hooks {
		if h.hasAfter() {
			hasAfter = true
			break
		}
	}
	if !hasAfter {
		steps := make([]NewStep, 0, len(m.hooks)+1)
		for _, h := range m.hooks {
			if h.before != nil {
				steps = append(steps, Do(h.before))
			}
		}
		return Sequence(append(steps, step)...)
	}
	hooks := m.hooks
	return func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			continued, fwd := runBracketed(ctx, hooks, step)
			if !continued {
				return nil
			}
			return Continue(fwd, next)
		})
	}
}

// runBracketed is the single control operator in the package. It runs step
// against a sentinel continuation so control returns here even when the step
// is terminal or cancels the context, and it defers the after hooks so they
// close in LIFO order and also run while a panic unwinds. The named result
// fwd is threaded through the hooks by pointer: a hook observes the context
// the step threaded forward and may amend it, on the normal path and during
// unwind alike.
//
// The defer operand determines who may recover: Go makes recover effective
// only when called directly by a function on the panicking defer chain, and
// h.after is deferred directly — so the constructors allocate the
// capability. After and AfterOutcome interpose an adapter closure, keeping
// their hooks observation-only (recover inside them is a no-op), while
// Deferred and CrashHandler store the user's function as the operand itself,
// granting it recover deliberately. runBracketed never recovers on its own:
// an unintercepted panic propagates with value and stack untouched, and a
// deferred hook that recovers without re-panicking leaves continued=false,
// which Wrap reports as a termination.
func runBracketed(ctx context.Context, hooks []hook, step NewStep) (continued bool, fwd context.Context) {
	fwd = ctx
	var completed bool
	// Plant an outcome-capture slot only when an outcome-aware hook will
	// need to discriminate how the step ended. A fresh slot per bracket
	// shadows any slot inherited from an enclosing or preceding bracket, so
	// outcomes never bleed across steps or branches.
	var slot *outcomeCapture
	for _, h := range hooks {
		if h.outcomeAfter != nil {
			slot = &outcomeCapture{}
			fwd = outcomeCaptureKey.Set(fwd, slot)
			break
		}
	}
	// outcome is evaluated lazily inside the deferred hooks, when the
	// extent's fate is known: a panic leaves completed false; otherwise the
	// sentinel and the capture slot discriminate the remaining kinds. An
	// attested termination outranks a recorded cancellation, because queue
	// operations cancel the context as an implementation detail of stopping
	// the pipeline.
	outcome := func() Outcome {
		switch {
		case !completed:
			return Outcome{Kind: OutcomePanicked}
		case continued:
			return Outcome{Kind: OutcomeContinued}
		default:
			if slot != nil {
				if t := slot.term.Load(); t != nil {
					return Outcome{Kind: OutcomeTerminated, Detail: t.detail, Cause: t.cause}
				}
				if cause := slot.cancelCause.Load(); cause != nil {
					return Outcome{Kind: OutcomeCancelled, Cause: *cause}
				}
			}
			return Outcome{Kind: OutcomeTerminated}
		}
	}
	for _, h := range hooks {
		if h.before != nil {
			fwd = h.before(fwd)
		}
		if h.after != nil {
			defer h.after(&fwd)
		}
		if h.outcomeAfter != nil {
			after := h.outcomeAfter
			defer func() { fwd = after(fwd, outcome()) }()
		}
		if h.crashAfter != nil {
			// Deferred with the entry context by value: crash handlers
			// observe, they do not thread. The handler itself is the operand,
			// so its own recover is effective.
			defer h.crashAfter(fwd)
		}
	}
	// Delimit: the sentinel records whether the step continued and the
	// context it threaded forward, and stops the step's dynamic extent there
	// so downstream steps run outside it.
	sentinel := StepFunc(func(c context.Context) Step {
		continued, fwd = true, c
		return nil
	})
	step(sentinel).Run(fwd)
	completed = true
	return continued, fwd
}

var ambientMiddlewareKey = ctxkey.New[Middleware]()

// AmbientMiddleware returns the composed middleware from context.
// Returns the zero (identity) Middleware if none has been registered.
func AmbientMiddleware(ctx context.Context) Middleware {
	mw, _ := ambientMiddlewareKey.Value(ctx)
	return mw
}

// WithAmbientMiddleware composes mw into the ambient middleware stack and
// returns an updated context. The first-registered middleware is outermost.
// Passing the zero Middleware is a no-op.
func WithAmbientMiddleware(ctx context.Context, mw Middleware) context.Context {
	if mw.IsZero() {
		return ctx
	}
	return ambientMiddlewareKey.Set(ctx, Compose(AmbientMiddleware(ctx), mw))
}

// WithoutAmbientMiddleware clears the ambient middleware stack, so steps run
// under the returned context — including branches of a Parallel reached from
// it — run with no ambient middleware.
//
// Its signature is a ContextFunc, so it drops straight into a pipeline to
// exempt everything downstream:
//
//	Sequence(Do(WithoutAmbientMiddleware), Parallel(a, b, c))
//
// Use it to carve a subtree out of middleware that is registered globally.
func WithoutAmbientMiddleware(ctx context.Context) context.Context {
	return ambientMiddlewareKey.Set(ctx, Middleware{})
}

// WithoutAmbientMiddleware must be usable wherever a ContextFunc is expected.
var _ ContextFunc = WithoutAmbientMiddleware

// AmbientDispatch wraps each step so that ambient middleware registered in
// context fires around every step as the pipeline executes. It is the opt-in
// mechanism for ambient middleware support:
//
//	state.Run(ctx, state.AmbientDispatch(a, b, c))
//
// Each step argument is wrapped individually — middleware fires once per step,
// not once for the whole group. Middleware registered via WithAmbientMiddleware
// before Run applies to all steps. Middleware registered mid-pipeline (inside a
// step) applies to all subsequent steps in the same AmbientDispatch call.
//
// AmbientDispatch works for any NewStep, including raw StepFunc closures and
// struct method steps — no special constructor is required.
//
// Composite steps are opaque to it: a Sequence or Parallel passed to
// AmbientDispatch is one step, so middleware fires once around the whole group
// rather than once per inner step. Parallel additionally dispatches into its own
// branches, so under AmbientDispatch a Parallel fires middleware once for the
// group and once more inside each branch.
func AmbientDispatch(steps ...NewStep) NewStep {
	return func(outerNext Step) Step {
		// Build the chain right-to-left. Each step's "next" is the already-dispatch-
		// wrapped Step for the following step, so middleware fires exactly once per
		// step. Wrap of the zero Middleware returns the step unchanged, so dispatch
		// is inert when nothing is registered.
		current := outerNext
		for _, step := range slices.Backward(steps) {
			s := step
			n := current
			current = StepFunc(func(ctx context.Context) Step {
				return AmbientMiddleware(ctx).Wrap(s)(n).Run(ctx)
			})
		}
		return current
	}
}

// Dispatch is the single-step form of AmbientDispatch. It carries the ambient
// middleware stack across a boundary that middleware applied outside cannot
// reach.
//
// Dispatch is a plain step transformer, not a Middleware: it is part of the
// interpreter that applies Middleware, one level up from the sealed type.
//
// Parallel already applies it to its own branches, so most code never needs it
// directly. Reach for it when writing a combinator of your own that crosses a
// goroutine boundary.
//
// Like AmbientDispatch, Dispatch is inert when no middleware is registered.
func Dispatch(step NewStep) NewStep { return AmbientDispatch(step) }

var (
	stepNameKey        = ctxkey.NewWithDefault[string]("")
	stepNameCaptureKey = ctxkey.New[*atomic.Pointer[string]]()
)

// StepName returns the name of the currently-executing step from context.
// Returns the name set by Named if present, otherwise "".
func StepName(ctx context.Context) string {
	return stepNameKey.Value(ctx)
}

// WithStepNameCapture allocates a capture slot in context that the first
// (outermost) Named step fills with its name. This lets observability
// middleware read the step name even for terminal steps that never invoke
// their continuation. The slot is a pointer to an atomic, so concurrent Named
// branches (e.g. under Parallel) fill it without racing and the first writer
// wins. Pair with CapturedStepName to read the result.
func WithStepNameCapture(ctx context.Context) context.Context {
	return stepNameCaptureKey.Set(ctx, &atomic.Pointer[string]{})
}

// CapturedStepName returns the step name written into the capture slot set up
// by WithStepNameCapture. Returns "" if no slot was allocated or no Named step
// ran.
func CapturedStepName(ctx context.Context) string {
	if slot, ok := stepNameCaptureKey.Value(ctx); ok && slot != nil {
		if name := slot.Load(); name != nil {
			return *name
		}
	}
	return ""
}

// Named annotates a step with a human-readable name for observability.
// The name is stored in context before the step executes, making it
// available to middleware via StepName(ctx).
//
// Named is entirely optional — pipelines behave identically without it.
func Named(name string, step NewStep) NewStep {
	return func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			ctx = stepNameKey.Set(ctx, name)
			// Fill the capture slot (if a middleware allocated one). First
			// writer wins, so an outer Named is recorded over any nested ones,
			// and concurrent Named branches do not race.
			if slot, ok := stepNameCaptureKey.Value(ctx); ok && slot != nil {
				slot.CompareAndSwap(nil, &name)
			}
			return step(next).Run(ctx)
		})
	}
}

// Step converts a NewStep to a Step by calling it with nil as the next step.
func (ns NewStep) Step() Step {
	return ns(nil)
}

// NewStepFunc creates a NewStep from a function that takes both context and next step.
// This is a convenience wrapper that eliminates boilerplate:
//
//	func MyStep() NewStep {
//	    return NewStepFunc(func(ctx context.Context, next Step) Step {
//	        // your logic here
//	        return Continue(ctx, next)
//	    })
//	}
func NewStepFunc(fn func(ctx context.Context, next Step) Step) NewStep {
	return func(next Step) Step {
		return StepFunc(func(ctx context.Context) Step {
			return fn(ctx, next)
		})
	}
}

// NewTerminalStepFunc creates a NewStep that executes a side-effect function and terminates the pipeline.
// This is a convenience wrapper for steps that don't need to continue to the next step.
//
// Example:
//
//	markComplete := state.NewTerminalStepFunc(func(ctx context.Context) {
//	    log.Println("Processing complete")
//	    queue.NewQueueOperationsCtx().Done(ctx)
//	})
func NewTerminalStepFunc(fn func(ctx context.Context)) NewStep {
	return NewStepFunc(func(ctx context.Context, _ Step) Step {
		fn(ctx)
		return nil
	})
}

// Run executes a NewStep pipeline until completion.
// With continuation-passing style, the pipeline executes completely in one call.
func Run(ctx context.Context, newStep NewStep) {
	step := newStep.Step()
	if step != nil {
		step.Run(ctx)
	}
}

var errorHandlerKey = ctxkey.New[func(error) Step]()

// WithErrorHandler adds an error handler to the context.
// When Continue encounters a cancelled context, it calls this handler with the
// cancellation cause instead of stopping the pipeline. A non-nil Step returned
// by the handler is run as a recovery path; see Continue for its semantics.
func WithErrorHandler(ctx context.Context, handler func(error) Step) context.Context {
	return errorHandlerKey.Set(ctx, handler)
}

// Continue runs the next step if it exists and context is not cancelled.
// If context is cancelled:
//   - If an error handler is set via WithErrorHandler, calls the handler and,
//     when the handler returns a non-nil recovery Step, runs it with the
//     handler cleared from context. The context is still cancelled while the
//     recovery step runs, so any Continue inside it stops the recovery path
//     rather than re-entering the handler; build recovery from a terminal
//     step or a single step that does its work in the body.
//   - Otherwise, stops the pipeline (returns nil)
//
// This is a helper to avoid the common pattern:
//
//	if ctx.Err() != nil {
//	    return nil
//	}
//	if next != nil {
//	    return next.Run(ctx)
//	}
//	return nil
func Continue(ctx context.Context, next Step) Step {
	if ctx.Err() != nil {
		err := context.Cause(ctx)
		// Report the cancellation to the enclosing bracket's outcome slot
		// (if any): the cancelled context never escapes the step, so this is
		// how outcome-aware hooks learn the pipeline stopped for
		// cancellation rather than termination.
		recordCancellation(ctx, err)
		if handler, ok := errorHandlerKey.Value(ctx); ok && handler != nil {
			if recovery := handler(err); recovery != nil {
				// The context is still cancelled: clear the handler so the
				// recovery path's own Continue calls stop instead of
				// re-entering it.
				return recovery.Run(errorHandlerKey.Set(ctx, nil))
			}
		}
		return nil
	}
	if next != nil {
		return next.Run(ctx)
	}
	return nil
}

// Terminal creates a step that terminates the pipeline.
var Terminal = NewTerminalStepFunc(func(context.Context) {})

// Noop creates a step that does nothing and continues to the next step.
var Noop = NewStepFunc(Continue)

// Do lifts a ContextFunc into a pipeline step.
// This is the primary way to add work to a pipeline.
func Do(fn ContextFunc) NewStep {
	return NewStepFunc(func(ctx context.Context, next Step) Step {
		return Continue(fn(ctx), next)
	})
}

// Sequence composes multiple NewStep functions into a sequential pipeline.
// Each step in the sequence executes in order with proper context threading.
func Sequence(steps ...NewStep) NewStep {
	return func(next Step) Step {
		if len(steps) == 0 {
			if next != nil {
				return next
			}
			return nil
		}

		// Build the chain right-to-left (continuation-passing style)
		current := next
		for _, step := range slices.Backward(steps) {
			current = step(current)
		}
		return current
	}
}

// Map applies wrapper to each step and returns the resulting slice.
// This is useful for applying a uniform policy to a set of steps before
// passing them to Parallel or other combinators. The wrapper is a plain step
// transformer; pass a Middleware's Wrap method to apply sealed middleware:
//
//	Parallel(Map(instrument.Wrap, step1, step2, step3)...)
//
// or pass a raw func(NewStep) NewStep for transformations that hooks cannot
// express, such as a deferred crash handler.
//
// The wrapper is called once per step when Map is called.
func Map(wrapper func(NewStep) NewStep, steps ...NewStep) []NewStep {
	result := make([]NewStep, len(steps))
	for i, s := range steps {
		result[i] = wrapper(s)
	}
	return result
}

// Parallel composes multiple NewStep functions to run in parallel, then
// continues to the next step after all complete. Each step runs in a new
// goroutine.
//
// Ambient middleware registered with WithAmbientMiddleware is applied inside
// each branch, on that branch's own goroutine. This is the default because the
// alternative fails silently: middleware whose effect is goroutine-local — a
// deferred crash handler, say — would look installed while never covering a
// branch. It follows that such middleware runs concurrently, once per branch,
// so it must be safe for concurrent use. It is inert when none is registered.
// To run branches without it, clear the stack first:
//
//	Sequence(Do(WithoutAmbientMiddleware), Parallel(a, b, c))
//
// A panicking step crashes the process. Parallel installs no recover, and
// recover cannot cross a goroutine boundary, so a branch panic bypasses any
// recover the caller installed around the pipeline: a crash handler wrapping
// Run never observes it. No information is lost — the process dies with the
// branch's stack. Because each branch is bracketed on its own goroutine,
// ambient after-hooks close during the unwind, and a crash handler
// registered as an ambient CrashHandler observes branch panics from
// inside each branch (see CrashHandler and the state/middleware package
// docs). A raw wrapper applied with Map works too.
func Parallel(steps ...NewStep) NewStep {
	return func(next Step) Step {
		return &ParallelStep{
			// Dispatch at composition time, so each branch runs the ambient
			// stack on its own goroutine and ParallelStep.Run stays unaware
			// that middleware exists. Inert when none is registered.
			steps: Map(Dispatch, steps...),
			next:  next,
		}
	}
}

// ParallelStep implements parallel execution of steps.
type ParallelStep struct {
	steps []NewStep
	next  Step
}

func (p *ParallelStep) Run(ctx context.Context) Step {
	var wg sync.WaitGroup

	// Branch panics are deliberately not recovered and re-raised here.
	// x/sync/errgroup rejects that design for reasons that apply equally to
	// this loop: it delays the panic until every sibling finishes, it reduces
	// the panic stack to a mere value that crash-monitoring tools cannot see,
	// and it risks deadlocking in a way that hides the panic entirely. Callers
	// who want structured crash reporting defer a handler inside the branch
	// instead — see the Parallel doc comment.
	for _, step := range p.steps {
		wg.Go(func() {
			if s := step.Step(); s != nil {
				s.Run(ctx)
			}
		})
	}

	wg.Wait()
	return Continue(ctx, p.next)
}

// Decision creates a conditional step that chooses between two paths.
func Decision(predicate func(context.Context) bool, trueHandler, falseHandler NewStep) NewStep {
	return func(next Step) Step {
		return &DecisionStep{
			predicate:    predicate,
			trueHandler:  trueHandler,
			falseHandler: falseHandler,
			next:         next,
		}
	}
}

// DecisionStep implements conditional execution.
type DecisionStep struct {
	predicate    func(context.Context) bool
	trueHandler  NewStep
	falseHandler NewStep
	next         Step
}

func (d *DecisionStep) Run(ctx context.Context) Step {
	var chosen NewStep
	if d.predicate(ctx) {
		chosen = d.trueHandler
	} else {
		chosen = d.falseHandler
	}

	return Continue(ctx, chosen(d.next))
}

// When creates a conditional step with only a true branch.
// If the predicate returns true, the trueHandler is executed and the pipeline terminates.
// If the predicate returns false, execution continues to the next step.
//
// This is equivalent to Decision(predicate, trueHandler, Noop) but more readable
// when there is no meaningful false branch:
//
//	state.When(
//	    func(ctx context.Context) bool { return !resourceReady(ctx) },
//	    queue.RequeueAfter(30 * time.Second),
//	)
func When(predicate func(context.Context) bool, trueHandler NewStep) NewStep {
	return Decision(predicate, trueHandler, Noop)
}

// Enum creates a multi-way branching step based on a selector function.
// The selector function returns a value that is matched against the cases map.
// If no match is found, the defaultHandler is executed.
func Enum[T comparable](
	selector func(context.Context) T,
	cases map[T]NewStep,
	defaultHandler NewStep,
) NewStep {
	return func(next Step) Step {
		return &EnumStep[T]{
			selector:       selector,
			cases:          cases,
			defaultHandler: defaultHandler,
			next:           next,
		}
	}
}

// EnumStep implements multi-way branching based on enum values.
type EnumStep[T comparable] struct {
	selector       func(context.Context) T
	cases          map[T]NewStep
	defaultHandler NewStep
	next           Step
}

func (e *EnumStep[T]) Run(ctx context.Context) Step {
	value := e.selector(ctx)
	var chosen NewStep
	if step, ok := e.cases[value]; ok {
		chosen = step
	} else if e.defaultHandler != nil {
		chosen = e.defaultHandler
	} else {
		// No match and no default, continue to next
		return Continue(ctx, e.next)
	}

	return Continue(ctx, chosen(e.next))
}

// Switch creates a multi-way branching step based on string values.
// This is a convenience function for the common case of string-based branching.
func Switch(
	selector func(context.Context) string,
	cases map[string]NewStep,
	defaultHandler NewStep,
) NewStep {
	return Enum(selector, cases, defaultHandler)
}
