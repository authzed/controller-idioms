# dsk — Deterministic Simulated Kubernetes

DSK is an in-process Go simulation layer for testing Kubernetes controllers. It sits between a controller and a real Kubernetes client (envtest, kind, or a fake), mediating all API calls. The central abstraction is the **tick** — a deterministic delta cycle that controls exactly when watch events are delivered to controllers.

## Why

Some classes of controller bugs are notoriously hard to test with standard approaches:

- **Convergence**: tick until `IsSteady()` or fail at a max-tick limit, catching both slow convergence (assert on tick count) and infinite requeue loops (never reaches steady state)
- **Ordering races between controllers**: `Buffer()` returns pending events as a slice; tests can explicitly deliver them in any order or use a property-testing library to enumerate all permutations

DSK makes these testable:

1. **Steady-state detection**: tick until `IsSteady()` returns true, or fail if you hit a max-tick limit.
2. **Event ordering control**: `Buffer()` gives you all pending events as a plain slice; `Flush()` delivers any subset. Test both orderings explicitly, or feed the slice to a property testing library to generate all permutations.
3. **Fault injection** — inject errors or delays on specific verbs/resources without modifying controller code.

## How it works

DSK wraps `dynamic.Interface` and `kubernetes.Interface`. Writes and reads pass through to the underlying client immediately. `Watch` calls are intercepted: events from upstream are buffered inside DSK and not forwarded to the controller until `Flush` is called.

Cooperative tick completion uses a queue wrapper (`WrapQueue`) that tracks items between `Get()` and `Done()` during each tick. `Flush` returns only after every in-flight item has called `Done()`. Steady-state detection (`IsSteady()`) additionally checks that the underlying queue is fully drained.

```text
test code
   │
   ├── d.Dynamic(fake)              ← fake client (reactor path)
   ├── d.DynamicFromConfig(cfg)     ← real client (transport path)
   ├── d.Typed(fake)                ← fake typed client (reactor path)
   ├── d.TypedFromConfig(cfg)       ← real typed client (transport path)
   ├── d.WrapQueue(q)               ← wraps workqueue.RateLimitingInterface
   └── dsk.NewRegistry(d)           ← wraps typed.Registry with handler tracking
          │
    DSK tick engine                  ← buffers watch events, tracks queue completion
          │
    underlying client                ← envtest / kind / fake
```

## Inspiration

The tick/delta-cycle model is borrowed from VHDL simulation. In VHDL, a simulator advances time in discrete steps; within each step, signal changes propagate through zero-time "delta cycles" until the design reaches quiescence. DSK applies the same idea to controller testing: a `Tick` is a delta cycle in which all pending watch events are delivered and all resulting reconciles complete before the next tick begins. This gives controller tests the same reproducibility that hardware engineers rely on for gate-level simulation. Sigasi's [VHDL's crown jewel](https://www.sigasi.com/opinion/jan/vhdls-crown-jewel/) is a good primer on the concept.

## Installation

```sh
go get github.com/authzed/controller-idioms/dsk
```

All tests using DSK must be run with the `dsk` build tag:

```sh
go test -tags dsk ./...
```

## Quick start

```go
import (
    "github.com/authzed/controller-idioms/client/fake"
    dsk "github.com/authzed/controller-idioms/dsk"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/client-go/util/workqueue"
)

func TestReachSteadyState(t *testing.T) {
    d := dsk.New(t)

    // Fake client path — works for all resource types, no per-type boilerplate.
    client := d.Dynamic(fake.NewClient(runtime.NewScheme()))
    q := d.WrapQueue(workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()))
    defer q.ShutDown()

    go myController.Run(t.Context(), client, q)

    // Trigger an event — writes pass through immediately; the watch event is buffered.
    // Tick automatically waits for write-triggered watch events to arrive before flushing.
    createResource(client, myResource)

    d.TickUntilSteady(20)
}
```

For real clusters (envtest, kind), use `DynamicFromConfig` or `TypedFromConfig` instead:

```go
// Real cluster path — wraps the HTTP transport, covers all resource types.
client, err := d.DynamicFromConfig(restConfig)
typedClient, err := d.TypedFromConfig(restConfig)
```

## Core API

### `DSK`

```go
d := dsk.New(t)                              // one per test scenario
client := d.Dynamic(fake)                    // fake dynamic.Interface (reactor path)
client, err := d.DynamicFromConfig(cfg)      // real dynamic client (transport path)
client := d.Typed(fake)                      // fake kubernetes.Interface (reactor path)
client, err := d.TypedFromConfig(cfg)        // real typed client (transport path)
q      := d.WrapQueue(q)                     // wrap workqueue.RateLimitingInterface
q      := d.WrapTypedQueue(q)                // wrap workqueue.TypedRateLimitingInterface[T]

buf := d.Buffer()                       // []PendingEvent — snapshot of buffered events
d.WaitForEvents(n)                      // block until ≥n events are buffered (no prior write needed)
d.Flush(buf)                            // deliver events, wait for reconciles
d.Tick()                                // flush all buffered events (= Flush(Buffer()))
d.Ticks(n)                              // Tick n times
d.TickUntilSteady(n)                    // Tick up to n times; tb.Fatal if not steady
ok  := d.IsSteady()                    // true when buffer empty and all queues idle
ok  := d.HasPendingRequeues()          // true when an AddAfter/AddRateLimited requeue has not fired yet
dur, ok := d.NextRequeue()             // time until the earliest pending AddAfter requeue
d.Advance(dur)                         // synctest only: move virtual time by dur, then tick
```

### `PendingEvent`

`Buffer()` returns `[]PendingEvent`. Each value embeds `watch.Event` (giving access to `Type` and `Object`) and carries an unexported routing pointer. Slices of `PendingEvent` can be filtered, sorted, or passed directly to `pgregory.net/rapid` generators.

Pass values from `Buffer()` directly to `Flush()` — do not reconstruct them.

### Fault injection

```go
h := d.InjectFault(
    dsk.ErrorFault(errors.NewServiceUnavailable("simulated outage")).
        OnVerb("patch").
        OnResource(deploymentGVR),
)
// ... run test ...
d.RemoveFault(h)      // or d.ClearFaults() to remove all
```

`ErrorFault` returns an error for matching calls. `DelayFault` adds latency before forwarding the call. Both implement `FaultBuilder` with chainable selectors:

| Selector | Matches on |
|---|---|
| `OnVerb(verb)` | HTTP verb: `"get"`, `"list"`, `"watch"`, `"create"`, `"update"`, `"patch"`, `"delete"` |
| `OnResource(gvr)` | `schema.GroupVersionResource` |
| `OnNamespace(ns)` | namespace string |
| `OnName(name)` | object name |

Unset selectors match everything. Selectors compose with AND.

## Use cases

### Steady-state detection

Tick until `IsSteady()` or fail at a max-tick limit. The test fails clearly instead of hanging.

```go
d.TickUntilSteady(20)
```

### Delayed requeues and virtual time

`IsSteady()` cannot see requeues that wait in the delaying queue (`AddAfter` with a positive duration, or `AddRateLimited`). The queue wrappers record them, so `HasPendingRequeues()` separates "steady" from "steady until a requeue fires". `NextRequeue()` returns the time until the earliest `AddAfter` deadline. `Advance(dur)` runs `synctest.Sleep(dur)` on the bubble's clock, so due requeues fire and the controller reconciles them. Then it ticks.

```go
func TestRequeue(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        d := dsk.New(t) // the bubble's t, not the outer one
        client := d.Dynamic(fake.NewClient(scheme))
        q := d.WrapTypedQueue(workqueue.NewTypedRateLimitingQueue(
            workqueue.DefaultTypedControllerRateLimiter[string]()))
        defer q.ShutDown()
        go myController.Run(t.Context(), client, q) // calls q.AddAfter(key, 5*time.Minute)

        createResource(client, myResource)
        d.TickUntilSteady(20)

        for d.HasPendingRequeues() {
            dur, ok := d.NextRequeue()
            if !ok {
                t.Fatal("only rate-limited requeues pending; Advance by the limiter's delay")
            }
            d.Advance(dur) // virtual time; returns immediately
            d.TickUntilSteady(20)
        }
    })
}
```

`Advance` must run inside a `testing/synctest` bubble, and calls `tb.Fatal` otherwise. The bubble has these requirements:

- Call `dsk.New` with the bubble's `t`. DSK's watch drain goroutines select on `tb.Context().Done()`. A context from outside the bubble never blocks durably, so `synctest.Sleep` never returns. `New` with an outer `t` still works for `Tick`, `Flush` and `WaitForEvents`, because `New` initialises `tb.Context().Done()` before the bubble starts. It does not work for `Advance`.
- Create the work queues and start the controllers inside the bubble, so their timers use the bubble's clock.
- Every goroutine in the bubble must eventually block durably. Network I/O does not, so use fake clients. `DynamicFromConfig` and `TypedFromConfig` with an open watch do not work with `Advance`.

`AddRateLimited` delays come from the rate limiter, which DSK cannot see. These requeues count in `HasPendingRequeues()` until their item is next dequeued, but `NextRequeue()` ignores them. Advance by a duration that covers the limiter's delay.

### Event ordering (manual)

```go
events := d.Buffer()

// Deliver controller A's events first, then B's
d.Flush(filterFor(events, controllerA))
d.Flush(filterFor(events, controllerB))
assertInvariant(t, client)

// Then test the reverse ordering
d.Flush(filterFor(events, controllerB))
d.Flush(filterFor(events, controllerA))
assertInvariant(t, client)
```

### Property testing with `pgregory.net/rapid`

`Buffer()` returns a plain Go slice, making it a first-class citizen in property testing:

```go
rapid.Check(t, func(t *rapid.T) {
    d := dsk.New(t)
    go controllerA.Run(t.Context(), d.Dynamic(underlying), d.WrapQueue(qA))
    go controllerB.Run(t.Context(), d.Dynamic(underlying), d.WrapQueue(qB))

    // Write through the DSK-wrapped client so Buffer() waits for the
    // watch event to arrive before returning — no sleep needed.
    createSharedResource(d.Dynamic(underlying), ...)

    // rapid generates a random ordering of buffered events
    ordering := rapid.SliceOf(rapid.SampledFrom(d.Buffer())).Draw(t, "ordering")
    d.Flush(ordering)

    assertInvariant(t, underlying)
})
```

## Client coverage

`d.Dynamic()` and `d.Typed()` intercept Watch calls for **all resource types**:

- **Fake clients** (those satisfying the reactor interface, e.g. `fake.NewClient(scheme)`, `k8sfake.NewSimpleClientset()`): a universal watch reactor and write observer are installed on the underlying client. No per-type wrapper code is needed.
- **Real clients** (`DynamicFromConfig`, `TypedFromConfig`): the HTTP `RoundTripper` is wrapped at the config level, so all group-version-resource paths are intercepted transparently.

`Apply` and `ApplyStatus` are also tracked on fake clients via a thin wrapper that calls `preExpectWrite`/`upgradeExpect` around the operation.

## Limitations

- **In-process only.** DSK mediates calls to an existing Kubernetes client — it does not run its own API server. This is by design: DSK works with whatever client your controller already uses (envtest, kind, fake), so there is nothing new to deploy or configure.
- **`IsSteady()` ignores the delay heap.** `Len()` does not count items queued via `AddRateLimited` or `AddAfter(d > 0)` until they leave the delay heap, so `IsSteady()` can be true while a requeue is still due. Check `HasPendingRequeues()` too, and use `Advance` inside a `testing/synctest` bubble to fire the requeue (see [Delayed requeues and virtual time](#delayed-requeues-and-virtual-time)). `synctest.Wait()` by itself does not advance time.
- **Rate-limited delays are not known.** `NextRequeue()` reports only `AddAfter` deadlines. The rate limiter picks the `AddRateLimited` delay, and DSK cannot see it.

## Static analysis

The `asynchandler` analyzer detects `go` statements inside Kubernetes event handler callbacks (`AddFunc`, `UpdateFunc`, `DeleteFunc` in `cache.ResourceEventHandlerFuncs`). Goroutines spawned from a handler body can call `queue.Add()` after the handler returns, making those enqueues invisible to DSK's handler tracking.

Run it standalone:

```sh
go build -o asynchandler ./analyzers/asynchandler/cmd/asynchandler
go vet -vettool=./asynchandler -tags dsk ./...
```

Or use it programmatically in a test:

```go
import "github.com/authzed/controller-idioms/analyzers/asynchandler"

func TestNoAsyncHandlers(t *testing.T) {
    // use analysistest.Run or multichecker
}
```
