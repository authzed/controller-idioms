package static

import (
	"context"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/authzed/ktrllib"
	"github.com/authzed/ktrllib/bootstrap"
	"github.com/authzed/ktrllib/manager"
)

type Controller[K bootstrap.KubeResourceObject] struct {
	*manager.BasicController

	path                  string
	fileInformerFactory   *libctrl.FileInformerFactory
	staticClusterResource schema.GroupVersionResource
	gvr                   schema.GroupVersionResource
	client                dynamic.Interface

	lastStaticHash atomic.Uint64
}

func NewStaticController[K bootstrap.KubeResourceObject](name string, path string, gvr schema.GroupVersionResource, client dynamic.Interface) (*Controller[K], error) {
	fileInformerFactory, err := libctrl.NewFileInformerFactory()
	if err != nil {
		return nil, err
	}
	return &Controller[K]{
		BasicController:       manager.NewBasicController(name),
		path:                  path,
		fileInformerFactory:   fileInformerFactory,
		staticClusterResource: libctrl.FileGroupVersion.WithResource(path),
		gvr:                   gvr,
		client:                client,
	}, nil
}

func (c *Controller[K]) Start(ctx context.Context, numThreads int) {
	inf := c.fileInformerFactory.ForResource(c.staticClusterResource).Informer()
	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.handleStaticResource(ctx) },
		UpdateFunc: func(_, obj interface{}) { c.handleStaticResource(ctx) },
		DeleteFunc: func(obj interface{}) { c.handleStaticResource(ctx) },
	})
}

func (c *Controller[K]) handleStaticResource(ctx context.Context) {
	hash, err := bootstrap.ResourceFromFile[K](ctx, c.gvr, c.client, c.path, c.lastStaticHash.Load())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.lastStaticHash.Store(hash)
}
