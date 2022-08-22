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
	"k8s.io/klog/v2"

	"github.com/authzed/spicedb-operator/pkg/controller/handlers"
	"github.com/authzed/ktrllib"
	"github.com/authzed/ktrllib/cachekeys"
	"github.com/authzed/ktrllib/typed"
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

func (c *BasicController) Start(ctx context.Context, numThreads int) {
	return
}

// OwnedResourceController implements Controller that implements our standard
// controller pattern:
//   - A single GVR is "owned" and watched
//   - Changes to objects of that type are processed via a workqueue
//   - Other resources are watched, but only for the purpose of requeueing the
//     primary object type.
//   - The owned object has standard meta.Conditions and emits metrics for them
type OwnedResourceController struct {
	*BasicController
	Registry *typed.Registry
	Recorder record.EventRecorder
	Owned    schema.GroupVersionResource
	Queue    workqueue.RateLimitingInterface
	sync     SyncFunc
}

func NewOwnedResourceController(name string, owned schema.GroupVersionResource, registry *typed.Registry, broadcaster record.EventBroadcaster, syncFunc SyncFunc) *OwnedResourceController {
	return &OwnedResourceController{
		BasicController: NewBasicController(name),
		Registry:        registry,
		Owned:           owned,
		Queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), name+"_queue"),
		Recorder:        broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: name}),
		sync:            syncFunc,
	}
}

func (c *OwnedResourceController) Start(ctx context.Context, numThreads int) {
	defer utilruntime.HandleCrash()
	defer c.Queue.ShutDown()

	klog.V(3).InfoS("starting controller", "resource", c.Owned)
	defer klog.V(3).InfoS("stopping controller", "resource", c.Owned)

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
		cancel()
		c.Queue.Forget(key)
	}
	requeue := func(after time.Duration) {
		cancel()
		if after == 0 {
			c.Queue.AddRateLimited(key)
			return
		}
		c.Queue.AddAfter(key, after)
	}

	ctx = handlers.CtxHandlerControls.WithValue(ctx, libctrl.NewHandlerControls(done, requeue))

	c.sync(ctx, *gvr, namespace, name)

	cancel()
	<-ctx.Done()

	return true
}
