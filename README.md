# controller-idioms

[![Go Report Card](https://goreportcard.com/badge/github.com/authzed/controller-idioms)](https://goreportcard.com/report/github.com/authzed/controller-idioms)
[![Go Documentation](https://pkg.go.dev/badge/github.com/authzed/controller-idioms)](https://pkg.go.dev/github.com/authzed/controller-idioms)
[![Discord Server](https://img.shields.io/discord/844600078504951838?color=7289da&logo=discord "Discord Server")](https://discord.gg/jTysUaxXzM)
[![Twitter](https://img.shields.io/twitter/follow/authzed?color=%23179CF0&logo=twitter&style=flat-square&label=@authzed "@authzed on Twitter")](https://twitter.com/authzed)

controller-idioms is a collection of generic libraries that complement and extend fundamental Kubernetes libraries (e.g. [controller-runtime]) to implement best practices for Kubernetes controllers.

These libraries were originally developed by [Authzed] to build the [SpiceDB Operator] and their internal projects powering [Authzed Dedicated].

[controller-runtime]: https://github.com/kubernetes-sigs/controller-runtime
[Authzed]: https://authzed.com
[SpiceDB Operator]: https://github.com/authzed/spicedb-operator
[Authzed Dedicated]: https://authzed.com/pricing

Available idioms include:

- **[adopt]**: efficiently watch resources the controller doesn't own (e.g. references to a secret or configmap)
- **[bootstrap]**: install required CRDs and default CRs typically for CD pipelines
- **[component]**: manage and aggregate resources that are created on behalf of another resource
- **[fileinformer]**: an InformerFactory that watches local files typically for loading config without restarting
- **[hash]**: hashing resources to detect modifications
- **[metrics]**: metrics for resources that implement standard `metav1.Condition` arrays
- **[pause]**: handler that allows users stop the controller reconciling a particular resource without stopping the controller
- **[static]**: controller for "static" resources that should always exist on startup

[adopt]: https://pkg.go.dev/github.com/authzed/controller-idioms/adopt
[bootstrap]: https://pkg.go.dev/github.com/authzed/controller-idioms/bootstrap
[component]: https://pkg.go.dev/github.com/authzed/controller-idioms/component
[fileinformer]: https://pkg.go.dev/github.com/authzed/controller-idioms/fileinformer
[hash]: https://pkg.go.dev/github.com/authzed/controller-idioms/hash
[metrics]: https://pkg.go.dev/github.com/authzed/controller-idioms/metrics
[pause]: https://pkg.go.dev/github.com/authzed/controller-idioms/pause
[static]: https://pkg.go.dev/github.com/authzed/controller-idioms/static

Have questions? Join our [Discord].

Looking to contribute? See [CONTRIBUTING.md].

[Discord]: https://authzed.com/discord
[CONTRIBUTING.md]: https://github.com/authzed/spicedb/blob/main/CONTRIBUTING.md

## Overview

### Handlers

A `Handler` is a small, composable, reusable piece of a controller's state machine.
It has a simple, familiar signature:

```go
func (h *MyHandler) Handle(context.Context) {
	// do some work
}
```

Handlers are similar to an [`http.Handler`](https://pkg.go.dev/net/http#Handler) or a [`grpc.UnaryHandler`](https://pkg.go.dev/google.golang.org/grpc#UnaryHandler), but can pass control to another handler as needed.  
This allows handlers to compose in nice ways:

```go
func mainControlLoop(ctx context.Context) {
	handler.Chain(
		validateServiceAccount,
		handler.Parallel(
			createJob,
			createPersistentVolume, 
        )
    ).Handle(ctx)
}
```

The `handler` package contains utilities for building, composing, and decorating handlers, and for building large state machines with them.
See the [docs](https://pkg.go.dev/github.com/authzed/controller-idioms/handler) for more details.

Handlers take some inspiration from [statecharts](https://statecharts.dev/) to deal with the complexity of writing and maintaining controllers, while staying close to golang idioms.

### Typed Context

Breaking a controller down into small pieces with `Handler`s means that each piece either needs to re-calculate results from other stages or fetch the previously computed result from `context`.

The `typedctx` package provides generic helpers for storing / retrieving values from a `context.Context`.

```go
var CtxExpensiveObject = typedctx.NewKey[ExpensiveComputation]()

func (h *ComputeHandler) Handle(ctx context.Context) {
    ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
}

func (h *UseHandler) Handle(ctx context.Context) {
    precomputedExpensiveObject = CtxExpensiveObject.MustValue(ctx)
	// do something with object
}
```

`Handlers` are typically chained in a way that preserves the context between handlers, but not always.

For example:

```go
var CtxExpensiveObject = typedctx.NewKey[ExpensiveComputation]()

func (h *ComputeHandler) Handle(ctx context.Context) {
    ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
}

func (h *DecorateHandler) Handle(ctx context.Context) {
	ComputeHandler{}.Handle(ctx)

    // this fails, because the ctx passed into the wrapped handler isn't passed back out 
    CtxExpensiveObject.MustValue(ctx)	
}
```

To deal with these cases, `typedctx` provides a `Boxed` context type that instead stores a pointer to the object, with additional helpers for making a "space" for the pointer to be filled in later.

```go
var CtxExpensiveObject = typedctx.Boxed[ExpensiveComputation](nil)

func (h *ComputeHandler) Handle(ctx context.Context) {
    ctx = CtxExpensiveObject.WithValue(ctx, myComputedExpensiveObject)
}

func (h *DecorateHandler) Handle(ctx context.Context) {
	// adds an empty pointer
	ctx = CtxExpensiveObject.WithBox(ctx)
	
	// fills in the pointer - note that the implementation of ComputeHandler didn't change
	ComputeHandler{}.Handle(ctx)

	// now this succeeds, and returns the unboxed value 
	CtxExpensiveObject.MustValue(ctx)	
}
```

### Typed Informers, Listers, Indexers

The `typed` package converts (dynamic) kube informers, listers, and indexers into typed counterparts via generics.

```go
informerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, defaultResync, namespace, tweakListOptions)
indexer := informerFactory.ForResource(corev1.SchemeGroupVersion.WithResource("secrets")).Informer().Indexer()
secretIndexer := typed.NewIndexer[*corev1.Secret](indexer)

// secrets is []*corev1.Secret instead of unstructured
secrets, err := secretIndexer.ByIndex("my-index-name", "my-index-value")
```

### Controllers and Managers

The `manager` package provides an optional lightweight controller `Manager` abstraction (similar to kubernetes controller manager, or the manager from controller runtime). It also provides a simple `Controller` abstraction and some basic implementations.

The rest of `controller-idioms` can be used without using these if you are already using another solution.

#### `Manager`

- provides a way to start and stop a collection of controllers together
- controllers can be dynamically added/removed after the manager has started
- starts health / debug servers that tie into the health of the underlying controllers

#### `BasicController`

- provides default implementations of health and debug servers

#### `OwnedResourceController`

- implements the most common pattern for a controller: reconciling a single resource type via a workqueue
- has a single queue managing a single type
- on start, starts processing objects from the queue
- doesn't start any informers

### Informer Factory Registry

It can be useful to access the informer cache of one controller from another place, so that multiple controllers in the same binary don't need to open separate connections against the kube apiserver and maintain separate caches of the same objects.

The `typed` package provides a `Registry` that synchronizes access to shared informer factories across multiple controllers.

### Queue

The `queue` package provides helpers for working with client-go's `workqueues`.

`queue.OperationsContext` can be used from within a `Handler` to control the behavior of the queue that has called the handler.

The queue operations are:

- Done (stop processing the current key)
- Requeue (requeue the current key)
- RequeueAfter (wait for some period of time before requeuing the current key)
- ReqeueueErr (record an error and requeue)
- RequeueAPIError (requeue after waiting according to the priority and fairness response from the apiserver)

If calling these controls from a handler, it's important to `return` immediately so that the handler does not continue processing a key that the queue thinks has stopped.

### Middleware

Middleware can be injected between handlers with the `middleware` package.

```go
middleware.ChainWithMiddleware(
    middleware.NewHandlerLoggingMiddleware(4),
)(
    c.checkPause,
    c.adoptSecret,
    c.validate
).Handle(ctx)
```
