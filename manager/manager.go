package manager

import (
	"context"
	"net/http"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"
	componentconfig "k8s.io/component-base/config"
	genericcontrollermanager "k8s.io/controller-manager/app"
	"k8s.io/controller-manager/controller"
	controllerhealthz "k8s.io/controller-manager/pkg/healthz"
)

// Controller is the interface we require for all controllers this manager will
// manage.
type Controller interface {
	controller.Interface
	controller.Debuggable
	controller.HealthCheckable

	Start(ctx context.Context, numThreads int)
}

// Manager ties a set of controllers to be lifecyled together and exposes common
// metrics, debug information, and health endpoints for the set.
type Manager struct {
	healthzHandler *controllerhealthz.MutableHealthzHandler
	// this is the mux that serves healthz, metrics, pprof, etc
	srv *http.Server

	once sync.Once
}

// NewManager returns a Manager object with settings for what and how to expose
// information for its managed set of controllers.
func NewManager(debugConfig *componentconfig.DebuggingConfiguration, address string) *Manager {
	handler := controllerhealthz.NewMutableHealthzHandler()
	return &Manager{
		healthzHandler: handler,
		srv: &http.Server{
			Handler: genericcontrollermanager.NewBaseHandler(debugConfig, handler),
			Addr:    address,
		},
	}
}

// StartControllers starts a set of controllers in an errgroup and serves
// health / debug endpoints for them. It stops when the context is cancelled.
// It will only have an effect the first time it is called.
func (m *Manager) StartControllers(ctx context.Context, controllers ...Controller) error {
	errG, ctx := errgroup.WithContext(ctx)
	m.once.Do(func() {
		// start controllers
		for _, c := range controllers {
			c := c
			m.healthzHandler.AddHealthChecker(controllerhealthz.NamedHealthChecker(c.Name(), c.HealthChecker()))
			errG.Go(func() error {
				c.Start(ctx, runtime.GOMAXPROCS(0))
				return ctx.Err()
			})
			if ctx.Err() != nil {
				return
			}
		}
		// start health / debug server
		errG.Go(func() error {
			return m.srv.ListenAndServe()
		})

		// stop health / debug server when context is cancelled
		errG.Go(func() error {
			<-ctx.Done()
			return m.srv.Shutdown(ctx)
		})
	})
	if err := errG.Wait(); err != nil {
		return err
	}

	return ctx.Err()
}
