//go:build dsk

package dsk_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"

	dsk "github.com/authzed/controller-idioms/dsk"
)

var transportCmGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

// fakeWatchServer returns a minimal HTTP server that:
// - On GET ?watch=true: immediately streams one watch event then blocks
// - On POST: returns a created object with a resourceVersion
func fakeWatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			w.Write([]byte(`{"type":"ADDED","object":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"42"}}}` + "\n"))
			flusher.Flush()
			<-r.Context().Done()
			return
		}
		// POST (create)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"42"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeWriteTriggeredWatchServer returns a server where:
// - On GET ?watch=true: sends headers immediately, waits for POST trigger, then streams event
// - On POST: signals the watch to emit one event with the given RV, then responds
func fakeWriteTriggeredWatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	trigger := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Flush headers immediately so Watch() call can return.
			flusher := w.(http.Flusher)
			flusher.Flush()
			// Wait for the POST to trigger before streaming the event.
			select {
			case <-trigger:
				w.Write([]byte(`{"type":"ADDED","object":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"42"}}}` + "\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
			<-r.Context().Done()
			return
		}
		// POST (create): signal watcher, then respond
		select {
		case trigger <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"42"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTransport_BuffersWatchEvents(t *testing.T) {
	srv := fakeWatchServer(t)
	cfg := testRestConfig(t, srv.URL)

	d := dsk.New(t)
	dynClient, err := d.DynamicFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		w, err := dynClient.Resource(transportCmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// Wait for the drain goroutine to process the event and buffer it.
		// WaitForEvents uses sync.Cond.Wait which is durably blocking for synctest,
		// avoiding the hang that synctest.Wait() would cause due to network I/O.
		d.WaitForEvents(1)

		buf := d.Buffer()
		if len(buf) == 0 {
			t.Fatal("expected buffered event after drain goroutine settled")
		}
		d.Flush(buf)

		select {
		case ev := <-w.ResultChan():
			if ev.Type != watch.Added {
				t.Fatalf("want Added, got %v", ev.Type)
			}
		case <-t.Context().Done():
			t.Fatal("event not delivered after Flush")
		}
	})
}

func TestTransport_ExpectsRVAfterWrite(t *testing.T) {
	srv := fakeWriteTriggeredWatchServer(t)
	cfg := testRestConfig(t, srv.URL)

	d := dsk.New(t)
	dynClient, err := d.DynamicFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	// Open a watch so watchCounts > 0 (expectRV is no-op otherwise)
	w, err := dynClient.Resource(transportCmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Create should call expectRV("42", configmapGVR)
	_, err = dynClient.Resource(transportCmGVR).Namespace("default").Create(ctx,
		testConfigMap("cm1"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Buffer() waits for the RV — should not block forever
	buf := d.Buffer()
	if len(buf) == 0 {
		t.Fatal("expected at least one buffered event")
	}
}

// fakeDeleteTriggeredWatchServer returns a server where:
// - On GET ?watch=true: sends headers immediately, waits for DELETE trigger, then streams a DELETED event
// - On POST: creates the object (so there is something to delete)
// - On DELETE: signals the watch to emit a DELETED event, then responds
func fakeDeleteTriggeredWatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	deleteReceived := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			flusher.Flush()
			// Wait for DELETE to be signaled, then emit the DELETED event.
			select {
			case <-deleteReceived:
				io.WriteString(w, `{"type":"DELETED","object":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"43"}}}`+"\n") //nolint:errcheck
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodDelete {
			// Signal the watch handler to emit the DELETED event.
			select {
			case <-deleteReceived:
				// already closed; nothing to do
			default:
				close(deleteReceived)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`)) //nolint:errcheck
			return
		}
		// POST (create)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm1","namespace":"default","resourceVersion":"42"}}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTransport_DeleteTriggersWatchEvent(t *testing.T) {
	srv := fakeDeleteTriggeredWatchServer(t)
	// Disable HTTP keepalives so the DELETE connection's readLoop goroutine exits
	// promptly after the response is consumed. Without this, the readLoop joins the
	// synctest bubble and blocks in keepalive-wait, preventing synctest.Test from
	// returning (synctest.Test waits for all bubble goroutines to exit).
	cfg := &rest.Config{
		Host: srv.URL,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	d := dsk.New(t)
	dynClient, err := d.DynamicFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		w, err := dynClient.Resource(transportCmGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// registerWatch is called synchronously in RoundTrip before the drain
		// goroutine starts, so the watch is registered by the time Watch() returns.
		// No additional synchronization is needed before issuing the Delete.

		err = dynClient.Resource(transportCmGVR).Namespace("default").Delete(ctx, "cm1", metav1.DeleteOptions{})
		if err != nil {
			t.Fatal(err)
		}

		// Buffer waits for the DELETED event via pendingAnyEvent tracking.
		buf := d.Buffer()
		if len(buf) != 1 {
			t.Fatalf("expected 1 DELETED event in buffer, got %d", len(buf))
		}
		if buf[0].Type != watch.Deleted {
			t.Fatalf("expected Deleted event, got %v", buf[0].Type)
		}
	})
}

func TestTransport_TypedBuffersWatchEvents(t *testing.T) {
	srv := fakeWatchServer(t)
	cfg := &rest.Config{Host: srv.URL}

	d := dsk.New(t)
	typedClient, err := d.TypedFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		w, err := typedClient.CoreV1().Pods("default").Watch(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// Wait for the drain goroutine to buffer the event.
		d.WaitForEvents(1)

		buf := d.Buffer()
		if len(buf) == 0 {
			t.Fatal("event not buffered after drain goroutine settled")
		}
		d.Flush(buf)

		select {
		case ev := <-w.ResultChan():
			if ev.Type != watch.Added {
				t.Fatalf("want Added, got %v", ev.Type)
			}
		case <-t.Context().Done():
			t.Fatal("event not delivered after Flush")
		}
	})
}

func TestTransport_WatchFaultFires(t *testing.T) {
	watchErr := errors.New("simulated watch error")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the transport lets the request through, return a valid watch response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cfg := testRestConfig(t, srv.URL)
	d := dsk.New(t, dsk.WithFaults(
		dsk.ErrorFault(watchErr).OnVerb("watch"),
	))
	client, err := d.DynamicFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Resource(transportCmGVR).Namespace("default").Watch(t.Context(), metav1.ListOptions{})
	if !errors.Is(err, watchErr) {
		t.Fatalf("expected watch fault error %q, got %v", watchErr, err)
	}
}

// TestFault_HoldUnblocksOnContextCancel verifies that cancelling the per-request
// context unblocks a HoldFault in the transport path. The reactor (fake client)
// path does not expose the per-request context to reactors, so this must be
// tested via DynamicFromConfig.
func TestFault_HoldUnblocksOnContextCancel(t *testing.T) {
	// Minimal server that blocks GET requests until the client disconnects.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	d := dsk.New(t)
	hold, _ := dsk.HoldFault()
	d.InjectFault(hold.OnVerb("get"))

	client, err := d.DynamicFromConfig(testRestConfig(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Resource(transportCmGVR).Namespace("default").Get(ctx, "cm1", metav1.GetOptions{})
		done <- err
	}()

	// Verify the call is blocked. synctest cannot be used here: the hold fault
	// blocks in a select on ctx.Done(), and cancel() (which fires ctx.Done()) is
	// called from the same scope, making the channel signable from the bubble —
	// preventing synctest from considering the goroutine durably blocked.
	select {
	case <-done:
		t.Fatal("expected call to be blocked before cancel")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after context cancel, got nil")
		}
	case <-t.Context().Done():
		t.Fatal("call did not unblock after context cancel")
	}
}

func TestGVRFromPath(t *testing.T) {
	podsGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	nodesGVR := schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	deploysGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	crdsGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

	tests := []struct {
		path    string
		wantGVR schema.GroupVersionResource
		wantNS  string
		wantOK  bool
	}{
		// core group, namespaced
		{"/api/v1/namespaces/default/pods", podsGVR, "default", true},
		{"/api/v1/namespaces/kube-system/pods", podsGVR, "kube-system", true},
		// core group, namespaced with object name
		{"/api/v1/namespaces/default/pods/mypod", podsGVR, "default", true},
		// core group, namespaced, subresource
		{"/api/v1/namespaces/default/pods/mypod/status", podsGVR, "default", true},
		// core group, cluster-scoped
		{"/api/v1/nodes", nodesGVR, "", true},
		{"/api/v1/nodes/mynode", nodesGVR, "", true},
		// named group, namespaced
		{"/apis/apps/v1/namespaces/default/deployments", deploysGVR, "default", true},
		{"/apis/apps/v1/namespaces/default/deployments/myapp", deploysGVR, "default", true},
		// named group, cluster-scoped
		{"/apis/apiextensions.k8s.io/v1/customresourcedefinitions", crdsGVR, "", true},
		{"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/foo.example.com", crdsGVR, "", true},
		// namespaces resource — collection and specific object are both the cluster-scoped namespaces resource
		{"/api/v1/namespaces", schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", true},
		{"/api/v1/namespaces/default", schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", true},
		// /apis/{group}/{version}/namespaces is not a valid resource path — the
		// "namespaces" resource only exists under /api/v1/. Group-versioned APIs
		// that happen to be named "namespaces" would require a /apis/.../namespaces/{ns}/{resource}
		// path which is handled above.
		{"/apis/apps/v1/namespaces", schema.GroupVersionResource{}, "", false},
		// non-resource paths
		{"/healthz", schema.GroupVersionResource{}, "", false},
		{"/version", schema.GroupVersionResource{}, "", false},
		{"/metrics", schema.GroupVersionResource{}, "", false},
		{"", schema.GroupVersionResource{}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			gotGVR, gotNS, gotOK := dsk.GVRFromPath(tt.path)
			if gotOK != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if gotGVR != tt.wantGVR {
				t.Errorf("gvr: got %v, want %v", gotGVR, tt.wantGVR)
			}
			if gotNS != tt.wantNS {
				t.Errorf("ns: got %q, want %q", gotNS, tt.wantNS)
			}
		})
	}
}

func TestObjectNameFromPath(t *testing.T) {
	podsGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	nodesGVR := schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	deploysGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	tests := []struct {
		path     string
		gvr      schema.GroupVersionResource
		wantName string
	}{
		// collection (no object name)
		{"/api/v1/namespaces/default/pods", podsGVR, ""},
		{"/api/v1/nodes", nodesGVR, ""},
		{"/apis/apps/v1/namespaces/default/deployments", deploysGVR, ""},
		// named object
		{"/api/v1/namespaces/default/pods/mypod", podsGVR, "mypod"},
		{"/api/v1/nodes/mynode", nodesGVR, "mynode"},
		{"/apis/apps/v1/namespaces/default/deployments/myapp", deploysGVR, "myapp"},
		// subresource — should return the object name, not the subresource segment
		{"/api/v1/namespaces/default/pods/mypod/status", podsGVR, "mypod"},
		{"/api/v1/namespaces/default/pods/mypod/log", podsGVR, "mypod"},
		{"/apis/apps/v1/namespaces/default/deployments/myapp/scale", deploysGVR, "myapp"},
		// unrecognised path
		{"/healthz", podsGVR, ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := dsk.ObjectNameFromPath(tt.gvr, tt.path)
			if got != tt.wantName {
				t.Errorf("got %q, want %q", got, tt.wantName)
			}
		})
	}
}
