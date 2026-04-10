//go:build dsk

package dsk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
)

// transportCodecs is used for content-negotiated stream decoding (JSON and protobuf).
var transportCodecs = scheme.Codecs

// dskTransport wraps an http.RoundTripper to intercept watch requests (buffering
// events) and write requests (tracking pending event arrivals via preExpectWrite).
type dskTransport struct {
	underlying http.RoundTripper
	engine     *engine
	faults     *faultSet
	tb         TB
}

func (t *dskTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	gvr, ns, ok := gvrFromPath(req.URL.Path)
	isWatch := req.URL.Query().Get("watch") == "true"

	if ok {
		verb := "watch"
		if !isWatch {
			verb = httpMethodToVerb(req.Method)
		}
		// Watch and list requests have no name segment in the path, so name is
		// always "" for watches. OnName() selectors will not match watch faults.
		name := objectNameFromPath(gvr, req.URL.Path)
		if err := t.faults.apply(req.Context(), verb, gvr, ns, name); err != nil {
			return nil, err
		}
	}

	// Pre-increment pendingAnyEvent BEFORE the write to avoid a race between
	// the response arriving and the watch event arriving.
	// For POST and PUT we can extract the object's labels from the request body
	// to filter watches by label selector. For PATCH and DELETE, labels are
	// unknown at this point, so we pass nil (conservative match-all).
	var preN int
	if ok && !isWatch {
		var objectLabels map[string]string
		switch req.Method {
		case http.MethodPost, http.MethodPut:
			objectLabels = extractLabelsFromBody(req)
		}
		preN = t.engine.preExpectWrite(httpMethodToVerb(req.Method), gvr, ns, objectLabels)
	}

	resp, err := t.underlying.RoundTrip(req)
	if err != nil {
		if preN > 0 {
			t.engine.cancelPreExpect(gvr, preN)
		}
		return nil, err
	}

	if !ok {
		return resp, nil
	}

	// Watch: replace body with a pipe; drain upstream into engine buffer.
	// Parse the label selector from the URL so preExpectWrite can filter writes
	// to objects that do not match this watch's selector.
	if isWatch {
		pr, pw := io.Pipe()
		sink := &streamSink{pw: pw}
		sel := parseSelectorFromQuery(req.URL.Query().Get("labelSelector"))
		t.engine.registerWatch(gvr, ns, sel)
		go drainHTTPWatch(t.tb, req.Context(), resp.Body, resp.Header.Get("Content-Type"), sink, t.engine, gvr, ns, sel)
		resp.Body = io.NopCloser(pr)
		return resp, nil
	}

	// Write: inspect response status; cancel pre-expect if the write failed.
	// On success, pendingAnyEvent already tracks the expected events (RV-agnostic).
	// We deliberately do NOT call upgradeExpect here: in the HTTP transport case
	// the watch event can arrive before we parse the response body, so upgrading
	// to pendingRVs after the event already decremented pendingAnyEvent would
	// re-add a non-zero pendingRVs entry and cause waitForPendingRVs to hang.
	if preN > 0 {
		switch req.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				// Write failed; no event will arrive.
				t.engine.cancelPreExpect(gvr, preN)
			}
			// On success: leave pendingAnyEvent incremented; the arriving event will decrement it.
		}
	}

	return resp, nil
}

// streamSink is the shared pipe writer for a single watch connection.
type streamSink struct {
	pw *io.PipeWriter
}

// rawFrameSink implements eventSink for a single watch event. It holds both
// the pipe to write to and the raw frame bytes to write, captured at decode
// time. The original encoding (JSON or protobuf) is preserved — no re-serialization.
type rawFrameSink struct {
	sink     *streamSink
	rawFrame []byte
	tb       TB
}

func (r *rawFrameSink) deliver(_ watch.Event) {
	// Write returns an error only if the pipe read end is closed (watch stopped).
	if _, err := r.sink.pw.Write(r.rawFrame); err != nil {
		r.tb.Logf("dsk: rawFrameSink.deliver: watch stopped, dropping frame (%d bytes): %v", len(r.rawFrame), err)
	}
}

// compile-time check
var _ eventSink = (*rawFrameSink)(nil)

// drainHTTPWatch reads framed watch events from the upstream response body,
// buffers them in the engine, and unregisters the watch when done.
// It uses runtime.NewClientNegotiator with the response Content-Type to support
// both JSON and protobuf without re-serialization.
func drainHTTPWatch(tb TB, ctx context.Context, upstream io.ReadCloser, contentType string, sink *streamSink, e *engine, gvr schema.GroupVersionResource, ns string, sel labels.Selector) {
	var closeOnce sync.Once
	closeUpstream := func() { closeOnce.Do(func() { upstream.Close() }) }
	defer closeUpstream()
	defer e.unregisterWatch(gvr, ns, sel)
	defer sink.pw.Close()

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		tb.Errorf("dsk: watch for %v: cannot parse Content-Type %q: %v — watch events will not be buffered", gvr, contentType, err)
		return
	}

	negotiator := k8sruntime.NewClientNegotiator(transportCodecs, schema.GroupVersion{})
	// StreamDecoder returns (Decoder, Serializer, Framer, error).
	// We only need the Serializer (streaming decoder) and Framer.
	_, streamingSerializer, framer, err := negotiator.StreamDecoder(mediaType, params)
	if err != nil || streamingSerializer == nil || framer == nil {
		tb.Errorf("dsk: watch for %v: cannot negotiate stream decoder for %q: %v — watch events will not be buffered", gvr, contentType, err)
		return
	}

	// framer.NewFrameReader returns exactly one complete frame per Read call.
	frameReader := framer.NewFrameReader(upstream)
	defer frameReader.Close()

	// Close the upstream body when ctx is cancelled so frameReader.Read unblocks promptly.
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeUpstream()
		case <-ctxDone:
		}
	}()
	defer close(ctxDone)

	// 1.5 MB matches etcd's default object size limit. Frames that exactly fill
	// the buffer may have been truncated; the decode below will fail and log a
	// hint pointing to the buffer size as the likely cause.
	buf := make([]byte, 1536*1024)
	for {
		n, err := frameReader.Read(buf)
		if err != nil {
			return
		}
		if n == len(buf) {
			tb.Logf("dsk: watch for %v: frame exactly filled %d-byte buffer — may be truncated; objects exceeding etcd's 1.5 MB limit will not be buffered correctly", gvr, n)
		}
		rawFrame := make([]byte, n)
		copy(rawFrame, buf[:n])

		var watchEvent metav1.WatchEvent
		obj, _, decErr := streamingSerializer.Decode(rawFrame, nil, &watchEvent)
		if decErr != nil || obj != &watchEvent {
			tb.Logf("dsk: watch for %v: skipping malformed frame (%d bytes): %v", gvr, n, decErr)
			continue
		}
		// Try to decode as a known typed object; fall back to unstructured.
		actualObj, _, decErr := scheme.Codecs.UniversalDeserializer().Decode(watchEvent.Object.Raw, nil, nil)
		if decErr != nil {
			u := &unstructured.Unstructured{}
			if jsonErr := u.UnmarshalJSON(watchEvent.Object.Raw); jsonErr != nil {
				continue
			}
			actualObj = u
		}
		e.addToBuffer(pendingEvent{
			event: watch.Event{Type: watch.EventType(watchEvent.Type), Object: actualObj},
			dest:  &rawFrameSink{sink: sink, rawFrame: rawFrame, tb: tb},
			gvr:   gvr,
		})
	}
}

// parseSelectorFromQuery parses a labelSelector query-parameter value into a
// labels.Selector. Returns nil (match-all) if the string is empty or invalid.
func parseSelectorFromQuery(labelSelector string) labels.Selector {
	if labelSelector == "" {
		return nil
	}
	sel, err := labels.Parse(labelSelector)
	if err != nil {
		return nil
	}
	return sel
}

// extractLabelsFromBody reads the request body, parses it as a Kubernetes
// object (or any JSON with a .metadata.labels field), and returns the labels.
// The request body is fully restored so the underlying transport can re-read it.
// Returns nil if the body cannot be read or does not contain metadata.labels.
func extractLabelsFromBody(req *http.Request) map[string]string {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }

	var obj struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil
	}
	return obj.Metadata.Labels
}

// gvrFromPath parses a Kubernetes API path into a GVR and namespace.
// Returns ok=false for paths it cannot parse (e.g. /healthz, /version).
//
//	/api/{version}/namespaces/{ns}/{resource}[/{name}]
//	/api/{version}/{resource}[/{name}]
//	/apis/{group}/{version}/namespaces/{ns}/{resource}[/{name}]
//	/apis/{group}/{version}/{resource}[/{name}]
func gvrFromPath(path string) (gvr schema.GroupVersionResource, ns string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch {
	case len(parts) >= 3 && parts[0] == "api":
		version := parts[1]
		if len(parts) >= 5 && parts[2] == "namespaces" {
			// /api/{version}/namespaces/{ns}/{resource}[/{name}...]
			return schema.GroupVersionResource{Version: version, Resource: parts[4]}, parts[3], true
		}
		if parts[2] != "namespaces" {
			// /api/{version}/{resource}[/{name}...]
			return schema.GroupVersionResource{Version: version, Resource: parts[2]}, "", true
		}
		// /api/{version}/namespaces          (collection)
		// /api/{version}/namespaces/{name}   (specific namespace object)
		// Both are the cluster-scoped "namespaces" resource.
		return schema.GroupVersionResource{Version: version, Resource: "namespaces"}, "", true
	case len(parts) >= 4 && parts[0] == "apis":
		group, version := parts[1], parts[2]
		if len(parts) >= 6 && parts[3] == "namespaces" {
			return schema.GroupVersionResource{Group: group, Version: version, Resource: parts[5]}, parts[4], true
		}
		if len(parts) >= 4 && parts[3] != "namespaces" {
			return schema.GroupVersionResource{Group: group, Version: version, Resource: parts[3]}, "", true
		}
		// /apis/{group}/{version}/namespaces[/{name}] is not a valid resource path
		// in the named-group API (unlike /api/v1/namespaces, which is the cluster-scoped
		// namespaces resource in the core group). Fall through to return ok=false.
	}
	return schema.GroupVersionResource{}, "", false
}

func httpMethodToVerb(method string) string {
	switch method {
	case http.MethodGet:
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// objectNameFromPath returns the object name for a named Kubernetes API request,
// correctly handling subresource paths by returning the name segment rather than
// the subresource segment.
//
// /api/v1/namespaces/{ns}/pods/{name}           → "name"
// /api/v1/namespaces/{ns}/pods/{name}/status    → "name" (not "status")
// /api/v1/nodes/{name}                          → "name"
//
// Returns "" for collection requests (no object name) or unrecognised paths.
func objectNameFromPath(gvr schema.GroupVersionResource, path string) string {
	// A named request is one where the path extends beyond the resource collection
	// by at least one segment (the object name). A subresource request extends by two.
	// We want the first extra segment (the object name), not the subresource.
	//
	// /api/v1/namespaces/{ns}/pods/{name}           → parts after resource: ["name"]
	// /api/v1/namespaces/{ns}/pods/{name}/status    → parts after resource: ["name", "status"]
	// /api/v1/nodes/{name}                          → parts after resource: ["name"]
	//
	// Parse the resource collection path length and index into parts accordingly.
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	// Determine how many parts the "base" collection path occupies.
	var baseLen int
	switch {
	case len(parts) >= 5 && parts[0] == "api" && parts[2] == "namespaces":
		baseLen = 5 // api / version / namespaces / ns / resource
	case len(parts) >= 3 && parts[0] == "api":
		baseLen = 3 // api / version / resource
	case len(parts) >= 6 && parts[0] == "apis" && parts[3] == "namespaces":
		baseLen = 6 // apis / group / version / namespaces / ns / resource
	case len(parts) >= 4 && parts[0] == "apis":
		baseLen = 4 // apis / group / version / resource
	default:
		return ""
	}
	if len(parts) > baseLen {
		return parts[baseLen] // object name
	}
	return "" // collection request (no object name)
}
