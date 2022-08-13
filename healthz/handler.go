package healthz

import (
	"net/http"
	"sync"

	"golang.org/x/exp/maps"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
)

// NOTE: this is a modified version of k8s.io/controller-manager/pkg/healthz/handler.go
// that permits removing healthchecks

// MutableHealthzHandler returns a http.Handler that handles "/healthz"
// following the standard healthz mechanism.
type MutableHealthzHandler struct {
	// handler is the underlying handler that will be replaced every time
	// new checks are added.
	handler http.Handler
	// mutex is a RWMutex that allows concurrent health checks (read)
	// but disallow replacing the handler at the same time (write).
	mutex  sync.RWMutex
	checks map[string]healthz.HealthChecker
}

func (h *MutableHealthzHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	h.handler.ServeHTTP(writer, request)
}

// AddHealthChecker adds health check(s) to the handler.
//
// Every time this function is called, the handler have to be re-initiated.
// It is advised to add as many checks at once as possible.
//
// If multiple health checks are added with the same name, only the last one
// will be stored.
func (h *MutableHealthzHandler) AddHealthChecker(checks ...healthz.HealthChecker) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.checks == nil {
		h.checks = make(map[string]healthz.HealthChecker, len(checks))
	}
	for _, c := range checks {
		h.checks[c.Name()] = c
	}
	newMux := mux.NewPathRecorderMux("healthz")
	healthz.InstallHandler(newMux, maps.Values(h.checks)...)
	h.handler = newMux
}

// RemoveHealthChecker removes health check(s) from the handler by name.
//
// Every time this function is called, the handler have to be re-initiated.
// It is advised to remove as many checks at once as possible.
func (h *MutableHealthzHandler) RemoveHealthChecker(names ...string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for _, n := range names {
		delete(h.checks, n)
	}
	newMux := mux.NewPathRecorderMux("healthz")
	healthz.InstallHandler(newMux, maps.Values(h.checks)...)
	h.handler = newMux
}

func NewMutableHealthzHandler(checks ...healthz.HealthChecker) *MutableHealthzHandler {
	h := &MutableHealthzHandler{}
	h.AddHealthChecker(checks...)

	return h
}
