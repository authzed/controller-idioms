package manager

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/component-base/config"
	"k8s.io/klog/v2/textlogger"

	"github.com/authzed/controller-idioms/queue"
	"github.com/authzed/controller-idioms/typed"
)

func TestManager(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())

	m := NewManager(&config.DebuggingConfiguration{
		EnableProfiling:           false,
		EnableContentionProfiling: false,
	}, ":"+getFreePort(t), nil, nil)

	ready := make(chan struct{})
	var err error
	go func() {
		err = m.Start(ctx, ready, testController(t, "a"))
	}()
	<-ready
	require.NoError(t, err)

	requireCancelFnCount(t, m, 1)

	// Ensure that the manager can't be started twice.
	require.Error(t, m.Start(ctx, ready), "manager already started")

	// Add some controllers after start
	require.NoError(t, m.Go(testController(t, "b"), testController(t, "c")))
	requireCancelFnCount(t, m, 3)

	specificCtrl := testController(t, "d")
	require.NoError(t, m.Go(specificCtrl))
	requireCancelFnCount(t, m, 4)

	// stop a specific controller
	m.Cancel(specificCtrl)
	requireCancelFnCount(t, m, 3)

	// cancel the manager's context, which will clean up all controllers
	cancel()

	requireCancelFnCount(t, m, 0)
}

func getFreePort(t testing.TB) string {
	t.Helper()

	a, err := net.ResolveTCPAddr("tcp", "localhost:0")
	require.NoError(t, err)
	l, err := net.ListenTCP("tcp", a)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, l.Close())
	}()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func testController(t *testing.T, name string) Controller {
	gvr := schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "mytypes",
	}
	CtxQueue := queue.NewQueueOperationsCtx()
	registry := typed.NewRegistry()
	broadcaster := record.NewBroadcaster()

	return NewOwnedResourceController(textlogger.NewLogger(textlogger.NewConfig()), name, gvr, CtxQueue, registry, broadcaster, func(_ context.Context, gvr schema.GroupVersionResource, namespace, name string) {
		t.Log("processing", gvr, namespace, name)
	})
}

func requireCancelFnCount(t *testing.T, m *Manager, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		m.RLock()
		defer m.RUnlock()
		return len(m.cancelFuncs) == count
	}, 100*time.Second, 10*time.Millisecond)
}
