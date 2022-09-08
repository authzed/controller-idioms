// Package fileinformer implements a kube-style Informer and InformerFactory
// that can be used to watch files instead of kube apis.
package fileinformer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// FileGroupVersion is a sythetic GroupVersion that the informers use.
var FileGroupVersion = schema.GroupVersion{
	Group:   "//LocalFile",
	Version: "v1",
}

// Factory implements dynamicinformer.DynamicSharedInformerFactory, but for
// starting and managing FileInformers.
type Factory struct {
	log logr.Logger
	sync.Mutex
	informers map[schema.GroupVersionResource]informers.GenericInformer
	// startedInformers is used for tracking which informers have been started.
	// This allows Start() to be called multiple times safely.
	startedInformers map[schema.GroupVersionResource]bool
}

var _ dynamicinformer.DynamicSharedInformerFactory = &Factory{}

// NewFileInformerFactory creates a new Factory.
func NewFileInformerFactory(log logr.Logger) (*Factory, error) {
	return &Factory{
		log:              log,
		informers:        make(map[schema.GroupVersionResource]informers.GenericInformer),
		startedInformers: make(map[schema.GroupVersionResource]bool),
	}, nil
}

// Start starts watching the files defined byt the informers.
func (f *Factory) Start(stopCh <-chan struct{}) {
	f.Lock()
	defer f.Unlock()

	for informerType, informer := range f.informers {
		if !f.startedInformers[informerType] {
			go informer.Informer().Run(stopCh)
			f.startedInformers[informerType] = true
		}
	}
}

// ForResource will create an informer for a specific file.
func (f *Factory) ForResource(gvr schema.GroupVersionResource) informers.GenericInformer {
	f.Lock()
	defer f.Unlock()

	key := gvr
	informer, exists := f.informers[key]
	if exists {
		return informer
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	informer, err = NewFileInformer(f.log, watcher, gvr)
	if err != nil {
		panic(err)
	}
	f.informers[key] = informer

	return informer
}

// WaitForCacheSync waits until all files in the informers have been synced once.
func (f *Factory) WaitForCacheSync(stopCh <-chan struct{}) map[schema.GroupVersionResource]bool {
	infs := func() map[schema.GroupVersionResource]cache.SharedIndexInformer {
		f.Lock()
		defer f.Unlock()

		infs := map[schema.GroupVersionResource]cache.SharedIndexInformer{}
		for informerType, informer := range f.informers {
			if f.startedInformers[informerType] {
				infs[informerType] = informer.Informer()
			}
		}
		return infs
	}()

	res := make(map[schema.GroupVersionResource]bool, len(infs))
	for informType, informer := range infs {
		res[informType] = cache.WaitForCacheSync(stopCh, informer.HasSynced)
	}
	return res
}

// FileInformer is an informer that watches files instead of the kube api.
type FileInformer struct {
	log      logr.Logger
	fileName string
	watcher  *fsnotify.Watcher
	informer cache.SharedIndexInformer
}

var _ informers.GenericInformer = &FileInformer{}

// NewFileInformer returns a new FileInformer.
func NewFileInformer(log logr.Logger, watcher *fsnotify.Watcher, gvr schema.GroupVersionResource) (*FileInformer, error) {
	return &FileInformer{
		log:      log,
		fileName: gvr.Resource,
		watcher:  watcher,
		informer: NewFileSharedIndexInformer(log, gvr.Resource, watcher, 1*time.Minute),
	}, nil
}

func (f *FileInformer) Informer() cache.SharedIndexInformer {
	return f.informer
}

func (f *FileInformer) Lister() cache.GenericLister {
	// TODO implement me
	panic("implement me")
}

type FileSharedIndexInformer struct {
	log logr.Logger
	sync.Once
	sync.RWMutex
	defaultEventHandlerResyncPeriod time.Duration
	fileName                        string
	watcher                         *fsnotify.Watcher
	started                         bool
	synced                          bool
	handlers                        []cache.ResourceEventHandler
}

var _ cache.SharedIndexInformer = &FileSharedIndexInformer{}

// NewFileSharedIndexInformer creates a new informer watching the file
// Note that currently all event handlers share the default resync period.
func NewFileSharedIndexInformer(log logr.Logger, fileName string, watcher *fsnotify.Watcher, defaultEventHandlerResyncPeriod time.Duration) *FileSharedIndexInformer {
	return &FileSharedIndexInformer{
		log:                             log.WithValues("file", fileName),
		fileName:                        fileName,
		watcher:                         watcher,
		handlers:                        []cache.ResourceEventHandler{},
		defaultEventHandlerResyncPeriod: defaultEventHandlerResyncPeriod,
	}
}

func (f *FileSharedIndexInformer) AddEventHandler(handler cache.ResourceEventHandler) {
	f.AddEventHandlerWithResyncPeriod(handler, f.defaultEventHandlerResyncPeriod)
}

func (f *FileSharedIndexInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) {
	f.RLock()
	if f.started {
		panic("cannot add event handlers after informer has started")
	}
	f.RUnlock()
	f.Lock()
	defer f.Unlock()
	f.handlers = append(f.handlers, handler)
	// TODO: non-default resync period
}

func (f *FileSharedIndexInformer) GetStore() cache.Store {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) GetController() cache.Controller {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) Run(stopCh <-chan struct{}) {
	f.Do(func() {
		defer utilruntime.HandleCrash()
		f.Lock()
		fileName := f.fileName
		utilruntime.HandleError(f.watcher.Add(fileName))
		f.started = true
		f.Unlock()
		f.log.V(4).Info("started watching")

		if len(fileName) == 0 {
			return
		}

		// do an initial read
		f.RLock()
		for _, h := range f.handlers {
			h.OnAdd(fileName)
		}
		f.RUnlock()

		f.Lock()
		f.synced = true
		f.Unlock()

		go func() {
			defer func() {
				f.Lock()
				defer f.Unlock()
				utilruntime.HandleError(f.watcher.Remove(fileName))
				utilruntime.HandleError(f.watcher.Close())
				f.log.V(4).Info("stopped watching")
			}()
			ctx, cancel := context.WithTimeout(context.Background(), f.defaultEventHandlerResyncPeriod)
			for {
				select {
				case <-ctx.Done():
					f.log.V(4).Info("resyncing file after %s", f.defaultEventHandlerResyncPeriod.String())
					f.RLock()
					for _, h := range f.handlers {
						h.OnUpdate(fileName, fileName)
					}
					f.RUnlock()
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), f.defaultEventHandlerResyncPeriod)
				case event, ok := <-f.watcher.Events:
					if !ok {
						cancel()
						return
					}
					f.log.V(8).Info("filewatcher got event %s for %q", event.String(), event.Name)
					if event.Name != fileName {
						continue
					}
					f.log.V(4).Info("filewatcher got event %s for %q", event.String(), event.Name)
					if event.Op&fsnotify.Write == fsnotify.Write ||
						event.Op&fsnotify.Create == fsnotify.Create {
						f.RLock()
						for _, h := range f.handlers {
							h.OnAdd(fileName)
						}
						f.RUnlock()
					}
					// chmod is the event from a configmap reload in kube
					if event.Op&fsnotify.Rename == fsnotify.Rename ||
						event.Op&fsnotify.Chmod == fsnotify.Chmod {
						f.RLock()
						for _, h := range f.handlers {
							h.OnUpdate(fileName, fileName)
						}
						f.RUnlock()
					}
					if event.Op&fsnotify.Remove == fsnotify.Remove {
						f.RLock()
						for _, h := range f.handlers {
							h.OnDelete(fileName)
						}
						// attempt to re-add the watch
						utilruntime.HandleError(f.watcher.Add(event.Name))
						f.RUnlock()
					}
				case err, ok := <-f.watcher.Errors:
					if !ok {
						cancel()
						return
					}
					utilruntime.HandleError(fmt.Errorf("error watching file: %w", err))
				case <-stopCh:
					cancel()
					return
				}
			}
		}()
	})
}

func (f *FileSharedIndexInformer) HasSynced() bool {
	f.RLock()
	defer f.RUnlock()
	if !f.started {
		return false
	}
	return f.synced
}

func (f *FileSharedIndexInformer) LastSyncResourceVersion() string {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) SetWatchErrorHandler(handler cache.WatchErrorHandler) error {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) AddIndexers(indexers cache.Indexers) error {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) GetIndexer() cache.Indexer {
	// TODO implement me
	panic("implement me")
}

func (f *FileSharedIndexInformer) SetTransform(handler cache.TransformFunc) error {
	// TODO implement me
	panic("implement me")
}
