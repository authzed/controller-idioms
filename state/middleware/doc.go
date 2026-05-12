// Package middleware provides ready-made state.Middleware implementations
// for common cross-cutting concerns.
//
// # Available Middleware
//
//   - Log: logs each step's name and elapsed time
//   - Recover: catches panics and routes them through the error handler
//
// # Usage with AmbientDispatch
//
// The typical pattern is to register middleware via WithAmbientMiddleware and
// run the pipeline with AmbientDispatch:
//
//	ctx = state.WithAmbientMiddleware(ctx, middleware.Log(slog.Default()))
//	state.Run(ctx, state.AmbientDispatch(step1, step2, step3))
//
// # Recover
//
// Recover is applied per-step, not ambient. Use it explicitly on steps that
// may panic:
//
//	state.Parallel(state.Map(middleware.Recover, step1, step2, step3)...)
package middleware
