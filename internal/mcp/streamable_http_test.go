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
	"sync/atomic"
	"testing"
	"time"
)

// streamableFakeServer is a minimal MCP streamable-http server wired for
// tests. It accepts POSTs, decodes JSON-RPC, and dispatches to the configured
// reply function. The reply function chooses which content type to respond
// with, letting a single test table exercise both JSON and SSE response shapes.
type streamableFakeServer struct {
	// reply decides the response for each incoming request. Return true from
	// useSSE to frame the response as text/event-stream; false for application/json.
	reply func(req decodedRequest) (statusCode int, useSSE bool, body any, sessionID string)
	// observedSessionID is the Mcp-Session-Id header seen on each request.
	// Populated in request order so tests can verify threading.
	observedSessionID []string
	// requestCount increments on every POST that reaches the server; lets
	// tests assert how many round trips happened without scanning request body.
	requestCount atomic.Int32
}

type decodedRequest struct {
	Method   string          `json:"method"`
	ID       *int            `json:"id"`
	Params   json.RawMessage `json:"params"`
	Raw      []byte          `json:"-"`
	SessID   string          `json:"-"`
	Accept   string          `json:"-"`
	CType    string          `json:"-"`
	UserAuth string          `json:"-"`
}

func (s *streamableFakeServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.requestCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		var dec decodedRequest
		_ = json.Unmarshal(body, &dec)
		dec.Raw = body
		dec.SessID = r.Header.Get("Mcp-Session-Id")
		dec.Accept = r.Header.Get("Accept")
		dec.CType = r.Header.Get("Content-Type")
		dec.UserAuth = r.Header.Get("Authorization")
		s.observedSessionID = append(s.observedSessionID, dec.SessID)

		status, useSSE, replyBody, newSessID := s.reply(dec)
		if newSessID != "" {
			w.Header().Set("Mcp-Session-Id", newSessID)
		}

		if useSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(status)
			if replyBody != nil {
				raw, _ := json.Marshal(replyBody)
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if replyBody != nil {
			raw, _ := json.Marshal(replyBody)
			w.Write(raw)
		}
	}
}

// happyStreamableReply returns a working initialize + tools/list exchange. The
// useSSEResponses flag lets tests flip the server between the two response
// shapes with a single helper.
func happyStreamableReply(useSSEResponses bool, sessionID string) func(decodedRequest) (int, bool, any, string) {
	return func(req decodedRequest) (int, bool, any, string) {
		switch req.Method {
		case "initialize":
			return 200, useSSEResponses, map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result": map[string]any{
					"protocolVersion": "2025-03-26",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "test-http", "version": "1.0"},
				},
			}, sessionID
		case "notifications/initialized":
			return 202, false, nil, ""
		case "tools/list":
			return 200, useSSEResponses, map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "tool_alpha"},
						{"name": "tool_beta"},
					},
				},
			}, ""
		}
		return 400, false, nil, ""
	}
}

func TestProbeStreamableHTTP_HappyJSON(t *testing.T) {
	t.Parallel()
	fake := &streamableFakeServer{reply: happyStreamableReply(false, "")}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("ProbeStreamableHTTP: %v", err)
	}
	names := result.ToolNames()
	slices.Sort(names)
	if want := []string{"tool_alpha", "tool_beta"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
	if result.Duration < 0 {
		t.Errorf("duration should be non-negative, got %v", result.Duration)
	}
}

func TestProbeStreamableHTTP_HappySSEFraming(t *testing.T) {
	t.Parallel()
	// Servers that only know how to answer in SSE must still work — the client
	// advertises both shapes on Accept and auto-detects from Content-Type.
	fake := &streamableFakeServer{reply: happyStreamableReply(true, "")}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("ProbeStreamableHTTP with SSE responses: %v", err)
	}
	if len(result.Tools) != 2 {
		t.Errorf("tool count = %d, want 2", len(result.Tools))
	}
}

func TestProbeStreamableHTTP_ThreadsSessionID(t *testing.T) {
	t.Parallel()
	// Initialize returns Mcp-Session-Id: "S-1"; every subsequent request
	// (initialized notification + tools/list) must carry it or the server
	// rejects with 404 per spec. Tests the full threading path.
	const sessID = "S-1"
	fake := &streamableFakeServer{
		reply: func(req decodedRequest) (int, bool, any, string) {
			if req.Method != "initialize" && req.SessID != sessID {
				return 404, false, map[string]any{"error": "missing session id"}, ""
			}
			return happyStreamableReply(false, sessID)(req)
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil); err != nil {
		t.Fatalf("ProbeStreamableHTTP with session threading: %v", err)
	}

	// observedSessionID[0] is initialize → no session yet (server issues it);
	// observedSessionID[1..] should all carry the issued session.
	if len(fake.observedSessionID) < 2 {
		t.Fatalf("want at least 2 requests, got %d", len(fake.observedSessionID))
	}
	if fake.observedSessionID[0] != "" {
		t.Errorf("initialize should have empty session id, got %q", fake.observedSessionID[0])
	}
	for i, got := range fake.observedSessionID[1:] {
		if got != sessID {
			t.Errorf("request %d session id = %q, want %q", i+1, got, sessID)
		}
	}
}

func TestProbeStreamableHTTP_SendsCustomHeaders(t *testing.T) {
	t.Parallel()
	// Auth tokens belong in Headers, not URL — verify the probe threads them
	// through on every request.
	fake := &streamableFakeServer{reply: happyStreamableReply(false, "")}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	var seenAuth []string
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		fake.handler()(w, r)
	})
	srv.Config.Handler = wrapped

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ProbeStreamableHTTP(ctx, srv.URL, map[string]string{"Authorization": "Bearer secret-123"}, nil, nil); err != nil {
		t.Fatalf("ProbeStreamableHTTP: %v", err)
	}
	if len(seenAuth) < 1 {
		t.Fatal("expected at least one request")
	}
	for i, got := range seenAuth {
		if got != "Bearer secret-123" {
			t.Errorf("request %d auth = %q, want Bearer secret-123", i, got)
		}
	}
}

func TestProbeStreamableHTTP_HTTPErrorSurfacesStatusAndBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"token expired"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include 401: %v", err)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error should include body snippet: %v", err)
	}
}

func TestProbeStreamableHTTP_NoToolsCapability(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req decodedRequest
		json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": *req.ID,
			"result": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{}, // no tools
			},
		})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "tools capability") {
		t.Errorf("expected tools-capability error, got %v", err)
	}
}

func TestProbeStreamableHTTP_JSONRPCErrorResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req decodedRequest
		json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": *req.ID,
			"error": map[string]any{"code": -32601, "message": "method not found"},
		})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Errorf("expected method-not-found error, got %v", err)
	}
}

func TestProbeStreamableHTTP_EmptyURL(t *testing.T) {
	t.Parallel()
	_, err := ProbeStreamableHTTP(context.Background(), "", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestProbeStreamableHTTP_OversizeResponseRejected(t *testing.T) {
	t.Parallel()
	// Server streams maxMCPMessageBytes + padding of garbage in a single JSON
	// body. The probe must refuse rather than buffer the full claimed size.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		garbage := strings.Repeat("x", maxMCPMessageBytes+1024)
		w.Write([]byte(garbage))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := ProbeStreamableHTTP(ctx, srv.URL, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for oversize response")
	}
	if !strings.Contains(err.Error(), "max") && !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected cap-triggered error, got: %v", err)
	}
}
