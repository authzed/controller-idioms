//go:build dsk

// Package dsk provides deterministic simulated Kubernetes (DSK) for controller
// testing. It wraps Kubernetes clients and informers to buffer watch events,
// delivering them only when the test calls Tick or Flush. This makes
// controller reconcile loops deterministic and eliminates timing-sensitive
// polling in tests.
//
// All DSK types and functions are only available when compiled with -tags dsk.
// Importing this package without the build tag produces a build error because
// all source files carry the tag. Test files that import dsk must also carry
// the build tag.
package dsk
