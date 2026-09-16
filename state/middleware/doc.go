// Package middleware provides ready-made state.Middleware implementations
// for common cross-cutting concerns.
//
// # Available Middleware
//
//   - Log: logs each step's name, elapsed time, and outcome, escalating the
//     level on cancellation and panic
//
// # Usage with AmbientDispatch
//
// The typical pattern is to register middleware via WithAmbientMiddleware and
// run the pipeline with AmbientDispatch:
//
//	ctx = state.WithAmbientMiddleware(ctx, middleware.Log(slog.Default()))
//	state.Run(ctx, state.AmbientDispatch(step1, step2, step3))
//
// # Panics
//
// This package deliberately ships no panic-recovery middleware. A panic in a
// step is a programming error, and controller runtimes generally install their
// own crash handler around the worker loop — one that logs the panic with its
// stack and then lets the process die. Recovering inside the pipeline would
// convert that loud, debuggable crash into a silently dropped reconcile.
//
// state.Parallel branches run on their own goroutines, so their panics bypass
// such a handler when it wraps the pipeline from outside — recover cannot
// cross a goroutine boundary. To cover branches, register the crash handler
// as ambient middleware: state.CrashHandler accepts
// utilruntime.HandleCrashWithContext directly, and state.Parallel applies the
// ambient stack inside every branch. Observation hooks like Log's still close
// during the unwind, so the crashing step is logged on the way out.
package middleware
