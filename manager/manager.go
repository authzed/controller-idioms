package manager

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	componentconfig "k8s.io/component-base/config"
	genericcontrollermanager "k8s.io/controller-manager/app"
	"k8s.io/controller-manager/controller"
	controllerhealthz "k8s.io/controller-manager/pkg/healthz"

	"github.com/authzed/controller-idioms/healthz"
)

// Controller is the interface we require for all controllers this manager will
// manage.
type Controller interface {
	controller.Interface
	controller.Debuggable
	controller.HealthCheckable

	Start(ctx context.Context, numThreads int)
}

// Manager ties a set of controllers to be lifecycled together and exposes common
// metrics, debug information, and health endpoints for the set.
type Manager struct {
	// serving pprof, debug, and health endpoints
	healthzHandler *healthz.MutableHealthzHandler
	srv            *http.Server

	// once prevents concurrent initialization
	once sync.Once

	// a single errG is used for all managed controllers, including those
	// that are added after initialization
	errG    *errgroup.Group
	errGCtx context.Context

	// a registry of cancel functions for each individual controller
	sync.RWMutex
	cancelFuncs map[Controller]func()
}

// NewManager returns a Manager object with settings for what and how to expose
// information for its managed set of controllers.
func NewManager(debugConfig *componentconfig.DebuggingConfiguration, address string) *Manager {
	handler := healthz.NewMutableHealthzHandler()
	return &Manager{
		healthzHandler: handler,
		srv: &http.Server{
			Handler:           genericcontrollermanager.NewBaseHandler(debugConfig, handler),
			Addr:              address,
			ReadHeaderTimeout: 20 * time.Second,
		},
		cancelFuncs: make(map[Controller]func(), 0),
	}
}

// Start starts a set of controllers in an errgroup and serves
// health / debug endpoints for them. It stops when the context is cancelled.
// It will only have an effect the first time it is called.
func (m *Manager) Start(ctx context.Context, controllers ...Controller) error {
	if m.errG != nil {
		return fmt.Errorf("manager already started")
	}
	m.once.Do(func() {
		m.errG, ctx = errgroup.WithContext(ctx)
		m.errGCtx = ctx
		// start controllers
		for _, c := range controllers {
			c := c
			m.healthzHandler.AddHealthChecker(controllerhealthz.NamedHealthChecker(c.Name(), c.HealthChecker()))
			m.errG.Go(func() error {
				ctx, cancel := context.WithCancel(ctx)
				m.Lock()
				m.cancelFuncs[c] = cancel
				m.Unlock()
				c.Start(ctx, runtime.GOMAXPROCS(0))
				return nil
			})
			if ctx.Err() != nil {
				return
			}
		}
		// start health / debug server
		m.errG.Go(func() error {
			return m.srv.ListenAndServe()
		})

		// stop health / debug server when context is cancelled
		m.errG.Go(func() error {
			<-ctx.Done()
			return m.srv.Shutdown(ctx)
		})
	})
	if err := m.errG.Wait(); err != nil {
		return err
	}

	return ctx.Err()
}

// Go adds controllers into the existing manager's errgroup
func (m *Manager) Go(controllers ...Controller) error {
	if m.errG == nil {
		return fmt.Errorf("cannot add controllers to an unstarted manager")
	}

	ctx := m.errGCtx

	// start newly added controllers
	for _, c := range controllers {
		c := c
		m.healthzHandler.AddHealthChecker(controllerhealthz.NamedHealthChecker(c.Name(), c.HealthChecker()))
		m.errG.Go(func() error {
			ctx, cancel := context.WithCancel(ctx)
			m.Lock()
			m.cancelFuncs[c] = cancel
			m.Unlock()
			c.Start(ctx, runtime.GOMAXPROCS(0))
			return nil
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	// no need to wait, the wait from `Start` will wait for the new goroutines
	return nil
}

// Cancel stops the controllers that are passed in
func (m *Manager) Cancel(controllers ...Controller) {
	names := make([]string, 0, len(controllers))
	for _, c := range controllers {
		m.RLock()
		cancel, ok := m.cancelFuncs[c]
		m.RUnlock()
		if ok {
			cancel()
		}
		names = append(names, c.Name())
		m.Lock()
		delete(m.cancelFuncs, c)
		m.Unlock()
	}
	m.healthzHandler.RemoveHealthChecker(names...)
}
