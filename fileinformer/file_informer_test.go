package fileinformer

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2/textlogger"
)

func TestFileInformer(t *testing.T) {
	informerFactory, err := NewFileInformerFactory(textlogger.NewLogger(textlogger.NewConfig()))
	require.NoError(t, err)

	file, err := os.CreateTemp(t.TempDir(), "watched-file")
	require.NoError(t, err)

	file2, err := os.CreateTemp(t.TempDir(), "watched-file")
	require.NoError(t, err)
	defer require.NoError(t, file2.Close())

	eventHandlers := &MockEventHandlers{}
	eventHandlers2 := &MockEventHandlers{}

	// expect an initial `OnAdd` from starting the informer
	eventHandlers.On("OnAdd", file.Name()).Return()
	eventHandlers2.On("OnAdd", file2.Name()).Return()

	inf := informerFactory.ForResource(FileGroupVersion.WithResource(file.Name())).Informer()
	_, err = inf.AddEventHandler(eventHandlers)
	require.NoError(t, err)
	inf2 := informerFactory.ForResource(FileGroupVersion.WithResource(file2.Name())).Informer()
	_, err = inf2.AddEventHandler(eventHandlers2)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	eventHandlers.Lock()
	require.Len(t, eventHandlers.Calls, 1)
	eventHandlers.Unlock()
	eventHandlers2.Lock()
	require.Len(t, eventHandlers2.Calls, 1)
	eventHandlers2.Unlock()

	// expect an OnAdd when the file is written
	eventHandlers.On("OnAdd", file.Name()).Return()
	_, err = file.Write([]byte("test"))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		eventHandlers.Lock()
		defer eventHandlers.Unlock()
		return len(eventHandlers.Calls) == 2
	}, 500*time.Millisecond, 10*time.Millisecond)

	// expect an OnUpdate when permission is changed
	eventHandlers.On("OnUpdate", file.Name(), file.Name()).Return()
	require.NoError(t, file.Chmod(0o770))

	require.Eventually(t, func() bool {
		eventHandlers.Lock()
		defer eventHandlers.Unlock()
		return len(eventHandlers.Calls) == 3
	}, 500*time.Millisecond, 10*time.Millisecond)

	// expect an OnDelete when file is removed
	eventHandlers.On("OnDelete", file.Name()).Return()
	require.NoError(t, file.Close())
	require.NoError(t, os.Remove(file.Name()))
	require.Eventually(t, func() bool {
		eventHandlers.Lock()
		defer eventHandlers.Unlock()

		foundDelete := false
		for _, call := range eventHandlers.Calls {
			if call.Method == "OnDelete" {
				foundDelete = true
			}
		}
		return foundDelete
	}, 500*time.Millisecond, 10*time.Millisecond)

	cancel()

	eventHandlers.AssertExpectations(t)
	eventHandlers2.AssertExpectations(t)
}

type MockEventHandlers struct {
	mock.Mock
	sync.Mutex
}

var _ cache.ResourceEventHandler = &MockEventHandlers{}

func (m *MockEventHandlers) OnAdd(obj interface{}, _ bool) {
	m.Lock()
	defer m.Unlock()
	m.Called(obj)
}

func (m *MockEventHandlers) OnUpdate(oldObj, newObj interface{}) {
	m.Lock()
	defer m.Unlock()
	m.Called(oldObj, newObj)
}

func (m *MockEventHandlers) OnDelete(obj interface{}) {
	m.Lock()
	defer m.Unlock()
	m.Called(obj)
}
