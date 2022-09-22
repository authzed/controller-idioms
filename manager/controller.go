// Package manager implements some basic abstractions around controllers and
// lifecycling them.
//
// `Manager` provides a way to start and stop a collection of controllers
// together. Controllers can be dynamically added/removed after the manager has
// started, and it also starts health / debug servers that tie into the
// health of the underlying controllers.
//
// `BasicController` provides default implementations of health and debug
// servers.
//
// `OwnedResourceController` implements the most common pattern for a
// controller: reconciling a single resource type via a workqueue. On Start,
// it begins processing objects from the queue, but it doesn't start any
// informers itself; that is the responsibility of the caller.
package manager

import (
	"context"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	apisrvhealthz "k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/controller-manager/controller"
	controllerhealthz "k8s.io/controller-manager/pkg/healthz"

	"github.com/go-logr/logr"

	"github.com/authzed/controller-idioms/cachekeys"
	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/typed"
)

// SyncFunc is a function called when an event needs processing
type SyncFunc func(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string)

// Controller is the interface we require for all controllers this manager will
// manage.
type Controller interface {
	controller.Interface
	controller.Debuggable
	controller.HealthCheckable

	Start(ctx context.Context, numThreads int)
}

// BasicController implements Controller with a no-op control loop and simple
// health check and debug handlers.
type BasicController struct {
	name string
}

var _ Controller = &BasicController{}

func NewBasicController(name string) *BasicController {
	return &BasicController{name: name}
}

func (c *BasicController) Name() string {
	return c.name
}

func (c *BasicController) DebuggingHandler() http.Handler {
	return http.NotFoundHandler()
}

func (c *BasicController) HealthChecker() controllerhealthz.UnnamedHealthChecker {
	return apisrvhealthz.PingHealthz
}

func (c *BasicController) Start(ctx context.Context, numThreads int) {}

// OwnedResourceController implements a Controller that implements our standard
// controller pattern:
//   - A single GVR is "owned" and watched
//   - Changes to objects of that type are processed via a workqueue
//   - Other resources are watched, but only for the purpose of requeueing the
//     primary object type.
//   - The owned object has standard meta.Conditions and emits metrics for them
type OwnedResourceController struct {
	*BasicController
	log logr.Logger
	queue.OperationsContext
	Registry *typed.Registry
	Recorder record.EventRecorder
	Owned    schema.GroupVersionResource
	Queue    workqueue.RateLimitingInterface
	sync     SyncFunc
}

func NewOwnedResourceController(log logr.Logger, name string, owned schema.GroupVersionResource, key queue.OperationsContext, registry *typed.Registry, broadcaster record.EventBroadcaster, syncFunc SyncFunc) *OwnedResourceController {
	return &OwnedResourceController{
		log:               log,
		BasicController:   NewBasicController(name),
		OperationsContext: key,
		Registry:          registry,
		Owned:             owned,
		Queue:             workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), name+"_queue"),
		Recorder:          broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: name}),
		sync:              syncFunc,
	}
}

func (c *OwnedResourceController) Start(ctx context.Context, numThreads int) {
	defer utilruntime.HandleCrash()
	defer c.Queue.ShutDown()

	c.log.V(3).Info("starting controller", "resource", c.Owned)
	defer c.log.V(3).Info("stopping controller", "resource", c.Owned)

	for i := 0; i < numThreads; i++ {
		go wait.Until(func() { c.startWorker(ctx) }, time.Second, ctx.Done())
	}

	<-ctx.Done()
}

func (c *OwnedResourceController) startWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *OwnedResourceController) processNext(ctx context.Context) bool {
	k, quit := c.Queue.Get()
	defer c.Queue.Done(k)
	if quit {
		return false
	}
	key, ok := k.(string)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("non-string key found in queue, %T", key))
		return true
	}

	gvr, namespace, name, err := cachekeys.SplitGVRMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("error parsing key %q, skipping", key))
		return true
	}

	ctx, cancel := context.WithCancel(ctx)

	done := func() {
		logr.FromContextOrDiscard(ctx).V(5).Info("done", "key", key)
		cancel()
		c.Queue.Forget(key)
	}
	requeue := func(after time.Duration) {
		logr.FromContextOrDiscard(ctx).V(5).Info("requeue", "key", key, "after", after)
		cancel()
		if after == 0 {
			c.Queue.AddRateLimited(key)
			return
		}
		c.Queue.AddAfter(key, after)
	}

	ctx = c.OperationsContext.WithValue(ctx, queue.NewOperations(done, requeue, cancel))

	c.sync(ctx, *gvr, namespace, name)
	done()
	<-ctx.Done()

	return true
}
