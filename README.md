# ktrllib

`ktrllib` (**k**ubernetes con**tr**o**l**ler **lib**rary) is a library and microframework for building kubernetes controllers and operators.

It is used extensively by [spicedb-operator](https://github.com/authzed/spicedb-operator) and several other closed-source operators.

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
See the [docs]() for more details.

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

The `manager` package provides an optional lightweight controller `Manager` abstraction (similar to kubernetes controller manager, or the manager from controller runtime). It also provides a simple `Controller` abstraction and some basic implementations. Both are optional, `ktrllib` can be used without these.

The rest of `ktrllib` can be used without using these if you are already using another solution.

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

## Common Patterns

`ktrllib` also comes with some re-usable components that implement common controller patterns.

- `adopt` - implements a pattern of "adopting" an external resource. 
This is especially important for dealing with references to types that the operator doesn't directly own (think: a reference to an existing configmap or secret).
You don't want the controller to watch _all_ secrets in a cluster, so instead you have it watch a label-filtered set. 
Then an explicitly referenced secret can be labelled to make it appear in the informer cache for the controller.
- `bootstrap` - implements a pattern of bootstrapping CRDs and "default" CRs.
This can be valuable if you are deploying an operator via a CD pipeline; it allows the operator to manage the creation of the CRD, followed by a set of default objects of that type, which can allow an operator to make backwards-incompatible changes to an API. 
It is not something that should generally be used by end-users, but can be helpful for developing operators.
- `component` - implements a pattern of objects that are created on behalf of another.
- `fileinformer` - implements an informerfactory that can watch local files; useful for global config that will be mounted as a volume. It allows the operator to respond to config changes without restarting.
- `hash` - provides utilities for hashing a kube object
- `metrics` - provides condition metrics for objects that implement standard `metav1.Condition` arrays.
- `pause` - implements a pause handler that allows a user to tell the controller to stop processing a single object (pause reconciliation) without spinning down the whole controller.
- `static` - implements a controller for "static" objects; objects that the controller determines on startup should always exist.
