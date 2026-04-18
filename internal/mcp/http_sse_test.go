package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseFakeServer simulates the legacy HTTP+SSE transport:
//   - GET <base>/ opens the SSE stream; first event is "endpoint", subsequent
//     events are "message" with JSON-RPC responses.
//   - POST <base>/post receives JSON-RPC requests; the handler enqueues the
//     corresponding response onto the SSE stream.
//
// The server owns the lifetime of the SSE goroutine via a done channel so
// tests can close the stream early to exercise reader-exit paths.
type sseFakeServer struct {
	mu          sync.Mutex
	writer      http.ResponseWriter
	flusher     http.Flusher
	connected   chan struct{} // closed when a GET connects
	postHandler func(req decodedRequest) (*jsonrpcResponse, bool)
	// If postHandler returns (nil, false) for a given request, the server
	// discards it (simulates server-side drop). Otherwise writes the response
	// as an SSE "message" event on the active stream.
	closeAfterFirst bool // when true, close the stream after pushing the first message response
}

func (s *sseFakeServer) streamHandler(postPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)

		s.mu.Lock()
		s.writer = w
		s.flusher = flusher
		s.mu.Unlock()

		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", postPath)
		flusher.Flush()

		close(s.connected)
		<-r.Context().Done()
	}
}

func newSSEFakeServer(
	t *testing.T,
	postReply func(req decodedRequest) (*jsonrpcResponse, bool),
) (*httptest.Server, *sseFakeServer) {
	t.Helper()
	s := &sseFakeServer{connected: make(chan struct{}), postHandler: postReply}

	mux := http.NewServeMux()
	var srv *httptest.Server
	// Post handler pushes responses via the stream writer.
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req decodedRequest
		_ = json.Unmarshal(body, &req)
		resp, keep := s.postHandler(req)
		w.WriteHeader(202)
		if !keep || resp == nil {
			return
		}
		s.mu.Lock()
		w2, flusher := s.writer, s.flusher
		s.mu.Unlock()
		if w2 == nil || flusher == nil {
			return
		}
		raw, _ := json.Marshal(resp)
		fmt.Fprintf(w2, "event: message\ndata: %s\n\n", raw)
		flusher.Flush()
		if s.closeAfterFirst {
			// Closing the underlying connection is non-trivial; use hijacker.
			if hj, ok := w2.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				if conn != nil {
					conn.Close()
				}
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// We need the POST URL to be accessible from the base; build it from srv.URL + "/post".
			s.streamHandler(srv.URL+"/post")(w, r)
			return
		}
		http.NotFound(w, r)
	})
	srv = httptest.NewServer(mux)
	return srv, s
}

func respResult(id int, result any) *jsonrpcResponse {
	raw, _ := json.Marshal(result)
	return &jsonrpcResponse{JSONRPC: "2.0", ID: &id, Result: raw}
}

func respError(id int, code int, message string) *jsonrpcResponse {
	return &jsonrpcResponse{JSONRPC: "2.0", ID: &id, Error: &jsonrpcError{Code: code, Message: message}}
}

func TestProbeSSE_HappyPath(t *testing.T) {
	t.Parallel()
	srv, _ := newSSEFakeServer(t, func(req decodedRequest) (*jsonrpcResponse, bool) {
		switch req.Method {
		case "initialize":
			return respResult(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test-sse", "version": "1.0"},
			}), true
		case "notifications/initialized":
			return nil, false // no response for notifications
		case "tools/list":
			return respResult(*req.ID, map[string]any{
				"tools": []map[string]any{
					{"name": "tool_alpha"},
					{"name": "tool_beta"},
					{"name": "tool_gamma"},
				},
			}), true
		}
		return nil, false
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := ProbeSSE(ctx, srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("ProbeSSE: %v", err)
	}
	names := result.ToolNames()
	slices.Sort(names)
	if want := []string{"tool_alpha", "tool_beta", "tool_gamma"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

func TestProbeSSE_JSONRPCError(t *testing.T) {
	t.Parallel()
	srv, _ := newSSEFakeServer(t, func(req decodedRequest) (*jsonrpcResponse, bool) {
		switch req.Method {
		case "initialize":
			return respResult(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}), true
		case "tools/list":
			return respError(*req.ID, -32601, "method not found"), true
		}
		return nil, false
	})
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeSSE(ctx, srv.URL+"/", nil)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Errorf("expected method-not-found error, got %v", err)
	}
}

func TestProbeSSE_NoToolsCapability(t *testing.T) {
	t.Parallel()
	srv, _ := newSSEFakeServer(t, func(req decodedRequest) (*jsonrpcResponse, bool) {
		if req.Method == "initialize" {
			return respResult(*req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{}, // no tools
			}), true
		}
		return nil, false
	})
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeSSE(ctx, srv.URL+"/", nil)
	if err == nil || !strings.Contains(err.Error(), "tools capability") {
		t.Errorf("expected tools-capability error, got %v", err)
	}
}

func TestProbeSSE_FirstEventNotEndpoint(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "event: welcome\ndata: hi\n\n")
		flusher.Flush()
		time.Sleep(100 * time.Millisecond) // give client time to read
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeSSE(ctx, srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("expected error about endpoint event, got %v", err)
	}
}

func TestProbeSSE_HTTPErrorOnStreamOpen(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, "forbidden")
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeSSE(ctx, srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 error, got %v", err)
	}
}

func TestProbeSSE_EmptyURL(t *testing.T) {
	t.Parallel()
	_, err := ProbeSSE(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestProbeSSE_StreamClosesMidFlight(t *testing.T) {
	t.Parallel()
	// Reader exits (EOF on stream) while initialize is waiting for its
	// response. The waiting sendRequest must wake with a clear error rather
	// than hanging until context deadline.
	srv, s := newSSEFakeServer(t, func(req decodedRequest) (*jsonrpcResponse, bool) {
		// Drop all POSTs so initialize's response never arrives, then we
		// close the stream to trigger reader exit.
		return nil, false
	})
	s.closeAfterFirst = false
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close the server in a separate goroutine to trigger reader exit while
	// ProbeSSE is blocked waiting for the initialize response.
	go func() {
		<-s.connected
		time.Sleep(100 * time.Millisecond)
		srv.CloseClientConnections()
	}()

	start := time.Now()
	_, err := ProbeSSE(ctx, srv.URL+"/", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error when stream closes mid-flight")
	}
	if elapsed > 4*time.Second {
		t.Errorf("took %v — stream-close should wake waiters well inside ctx window", elapsed)
	}
}
