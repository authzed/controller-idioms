// Package static implements a controller for "static" resources that should
// always exist on startup. It makes use of the bootstrap package to ensure
// objects exist.
package static

import (
	"context"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/go-logr/logr"

	"github.com/authzed/controller-idioms/bootstrap"
	"github.com/authzed/controller-idioms/fileinformer"
	"github.com/authzed/controller-idioms/manager"
)

type Controller[K bootstrap.KubeResourceObject] struct {
	*manager.BasicController

	path                  string
	fileInformerFactory   *fileinformer.Factory
	staticClusterResource schema.GroupVersionResource
	gvr                   schema.GroupVersionResource
	client                dynamic.Interface

	lastStaticHash atomic.Uint64
}

func NewStaticController[K bootstrap.KubeResourceObject](log logr.Logger, name string, path string, gvr schema.GroupVersionResource, client dynamic.Interface) (*Controller[K], error) {
	fileInformerFactory, err := fileinformer.NewFileInformerFactory(log)
	if err != nil {
		return nil, err
	}
	return &Controller[K]{
		BasicController:       manager.NewBasicController(name),
		path:                  path,
		fileInformerFactory:   fileInformerFactory,
		staticClusterResource: fileinformer.FileGroupVersion.WithResource(path),
		gvr:                   gvr,
		client:                client,
	}, nil
}

func (c *Controller[K]) Start(ctx context.Context, _ int) {
	inf := c.fileInformerFactory.ForResource(c.staticClusterResource).Informer()
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.handleStaticResource(ctx) },
		UpdateFunc: func(_, obj interface{}) { c.handleStaticResource(ctx) },
		DeleteFunc: func(obj interface{}) { c.handleStaticResource(ctx) },
	})
	if err != nil {
		panic("failed to add handlers: " + err.Error())
	}
	c.fileInformerFactory.Start(ctx.Done())
}

func (c *Controller[K]) handleStaticResource(ctx context.Context) {
	hash, err := bootstrap.ResourceFromFile[K](ctx, c.Name(), c.gvr, c.client, c.path, c.lastStaticHash.Load())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.lastStaticHash.Store(hash)
}
