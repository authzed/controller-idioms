package libctrl

import (
	"fmt"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var FileGroupVersion = schema.GroupVersion{
	Group:   "//LocalFile",
	Version: "v1",
}

type FileInformerFactory struct {
	sync.Mutex
	watcher   *fsnotify.Watcher
	informers map[schema.GroupVersionResource]informers.GenericInformer
	// startedInformers is used for tracking which informers have been started.
	// This allows Start() to be called multiple times safely.
	startedInformers map[schema.GroupVersionResource]bool
}

func NewFileInformerFactory() (dynamicinformer.DynamicSharedInformerFactory, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &FileInformerFactory{
		watcher:          watcher,
		informers:        make(map[schema.GroupVersionResource]informers.GenericInformer),
		startedInformers: make(map[schema.GroupVersionResource]bool),
	}, nil
}

func (f *FileInformerFactory) Start(stopCh <-chan struct{}) {
	f.Lock()
	defer f.Unlock()

	for informerType, informer := range f.informers {
		if !f.startedInformers[informerType] {
			go informer.Informer().Run(stopCh)
			f.startedInformers[informerType] = true
		}
	}
	go func() {
		<-stopCh
		utilruntime.HandleError(f.watcher.Close())
	}()
}

func (f *FileInformerFactory) ForResource(gvr schema.GroupVersionResource) informers.GenericInformer {
	f.Lock()
	defer f.Unlock()

	key := gvr
	informer, exists := f.informers[key]
	if exists {
		return informer
	}

	informer, err := NewFileInformer(f.watcher, gvr)
	if err != nil {
		panic(err)
	}
	f.informers[key] = informer

	return informer
}

func (f *FileInformerFactory) WaitForCacheSync(stopCh <-chan struct{}) map[schema.GroupVersionResource]bool {
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

	res := map[schema.GroupVersionResource]bool{}
	for informType, informer := range infs {
		res[informType] = cache.WaitForCacheSync(stopCh, informer.HasSynced)
	}
	return res
}

type FileInformer struct {
	fileName string
	watcher  *fsnotify.Watcher
	informer cache.SharedIndexInformer
}

func NewFileInformer(watcher *fsnotify.Watcher, gvr schema.GroupVersionResource) (*FileInformer, error) {
	return &FileInformer{
		fileName: gvr.Resource,
		watcher:  watcher,
		informer: NewFileSharedIndexInformer(gvr.Resource, watcher),
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
	sync.Once
	sync.RWMutex
	defaultEventHandlerResyncPeriod time.Duration
	fileName                        string
	watcher                         *fsnotify.Watcher
	started                         bool
	synced                          bool
	handlers                        []cache.ResourceEventHandler
}

func NewFileSharedIndexInformer(fileName string, watcher *fsnotify.Watcher) *FileSharedIndexInformer {
	return &FileSharedIndexInformer{
		fileName: fileName,
		watcher:  watcher,
		handlers: []cache.ResourceEventHandler{},
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
	// TODO: resync
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
		klog.V(4).Infof("started watching %q", fileName)

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
				klog.V(4).Infof("stopped watching %q", fileName)
			}()
			for {
				select {
				case event, ok := <-f.watcher.Events:
					if !ok {
						return
					}
					klog.V(8).Infof("filewatcher got event %s for %q", event.String(), event.Name)
					if event.Name != fileName {
						continue
					}
					klog.V(4).Infof("filewatcher got event %s for %q", event.String(), event.Name)
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
						return
					}
					utilruntime.HandleError(fmt.Errorf("error watching file: %w", err))
				case <-stopCh:
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
