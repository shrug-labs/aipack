package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeStreamableHTTP probes an MCP server via the streamable-http transport
// (MCP spec 2025-03-26). Each JSON-RPC message is delivered as a POST to the
// single endpoint; the server responds with either `application/json` (one
// message) or `text/event-stream` (one or more messages as SSE events). The
// client sends `Accept: application/json, text/event-stream` so servers on
// either shape answer without extra round-trips.
//
// Session handling: if the server returns `Mcp-Session-Id` on the initialize
// response, the client must include it on every subsequent request or the
// server rejects them. We thread the header through notifications and tool
// requests automatically.
//
// Context cancellation propagates to all in-flight requests via
// http.NewRequestWithContext. Per-response body reads are capped at
// maxMCPMessageBytes so a hostile server cannot OOM the probe with a
// gigabyte JSON blob or an unbounded SSE stream.
//
// stdout (when non-nil) receives `  connected (<transport>)` and
// `  listing tools` lines for live progress; events (when non-nil) receives
// the same milestones as ProbeEvent values. Lifecycle starting/done/error
// events are emitted by Probe.
func ProbeStreamableHTTP(ctx context.Context, url string, headers map[string]string, stdout io.Writer, events chan<- ProbeEvent) (*ProbeResult, error) {
	if url == "" {
		return nil, fmt.Errorf("empty URL")
	}
	start := time.Now()
	client := &http.Client{} // rely on ctx for deadline

	initResp, sessionID, err := httpRPCRequest(ctx, client, url, "", headers, &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      intID(1),
		Method:  "initialize",
		Params: initializeParams{
			ProtocolVersion: "2025-03-26",
			Capabilities:    map[string]any{},
			ClientInfo:      clientInfo{Name: "aipack", Version: "probe"},
		},
	}, 1)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	var initResult initializeResult
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		return nil, fmt.Errorf("parse initialize result: %w", err)
	}
	if initResult.Capabilities.Tools == nil {
		return nil, fmt.Errorf("server does not advertise tools capability")
	}

	if err := httpRPCNotification(ctx, client, url, sessionID, headers, &jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	if stdout != nil {
		fmt.Fprintln(stdout, "  connected (streamable-http)")
	}
	emitProbeEvent(events, ProbeEvent{Transport: "streamable-http", Phase: ProbePhaseConnected})
	if stdout != nil {
		fmt.Fprintln(stdout, "  listing tools")
	}
	emitProbeEvent(events, ProbeEvent{Phase: ProbePhaseListingTools})

	toolsResp, _, err := httpRPCRequest(ctx, client, url, sessionID, headers, &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      intID(2),
		Method:  "tools/list",
		Params:  map[string]any{},
	}, 2)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var toolsResult toolsListResult
	if err := json.Unmarshal(toolsResp.Result, &toolsResult); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	return &ProbeResult{
		Tools:    toolsResult.Tools,
		Duration: time.Since(start),
	}, nil
}

// httpRPCRequest POSTs one JSON-RPC request and returns the matching response.
// sessionID (if non-empty) is sent as Mcp-Session-Id; the server's response
// Mcp-Session-Id is returned so callers can thread it forward. Accept header
// lists both response shapes so the server can choose.
func httpRPCRequest(
	ctx context.Context,
	client *http.Client,
	url, sessionID string,
	headers map[string]string,
	msg *jsonrpcRequest,
	wantID int,
) (*jsonrpcResponse, string, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, "", err
	}
	httpResp, err := doHTTPPOST(ctx, client, url, sessionID, headers, body)
	if err != nil {
		return nil, "", err
	}
	defer httpResp.Body.Close()

	newSessionID := httpResp.Header.Get("Mcp-Session-Id")
	if newSessionID == "" {
		newSessionID = sessionID
	}
	if err := checkHTTPStatus(url, httpResp); err != nil {
		return nil, newSessionID, err
	}

	resp, err := decodeHTTPJSONRPC(httpResp, wantID)
	if err != nil {
		return nil, newSessionID, err
	}
	if resp.Error != nil {
		return nil, newSessionID, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp, newSessionID, nil
}

// httpRPCNotification POSTs a JSON-RPC notification (no response body
// required) and drains whatever the server sends back. Per spec the server
// may reply 202 Accepted with an empty body or 200 with a JSON/SSE payload;
// we discard either outcome — a notification has no reply to correlate.
func httpRPCNotification(
	ctx context.Context,
	client *http.Client,
	url, sessionID string,
	headers map[string]string,
	msg *jsonrpcNotification,
) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	httpResp, err := doHTTPPOST(ctx, client, url, sessionID, headers, body)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if err := checkHTTPStatus(url, httpResp); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(httpResp.Body, maxMCPMessageBytes))
	return nil
}

// doHTTPPOST issues the actual HTTP POST with the required MCP headers.
// Caller-provided headers are applied BEFORE framing headers so a caller
// cannot accidentally override Accept/Content-Type and break the protocol.
func doHTTPPOST(
	ctx context.Context,
	client *http.Client,
	url, sessionID string,
	headers map[string]string,
	body []byte,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return client.Do(req)
}

// checkHTTPStatus turns non-2xx responses into a readable error that includes
// the URL, status, and the first bytes of the response body. Auth failures
// (401/403) are the typical cause on internal endpoints — the body snippet
// lets operators distinguish auth, routing, and backend errors without extra
// tooling. Body is capped to avoid dumping a multi-kilobyte HTML error page.
func checkHTTPStatus(url string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	snippetStr := strings.TrimSpace(string(snippet))
	if snippetStr == "" {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, snippetStr)
}

// decodeHTTPJSONRPC reads the response body either as a single JSON message
// or as an SSE stream, returning the first JSON-RPC response whose ID matches
// wantID. SSE is auto-detected from Content-Type because servers may answer
// unary requests in either shape per the 2025-03-26 spec.
func decodeHTTPJSONRPC(resp *http.Response, wantID int) (*jsonrpcResponse, error) {
	ct := resp.Header.Get("Content-Type")
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))

	if ct == "text/event-stream" {
		return readSSEForJSONRPC(resp.Body, wantID)
	}

	// Default to JSON for application/json and unset/unknown Content-Type.
	// LimitReader caps at maxMCPMessageBytes+1 so an oversized body is
	// detected and rejected without allocating the full claimed size.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPMessageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxMCPMessageBytes {
		return nil, fmt.Errorf("response body exceeds max %d bytes", maxMCPMessageBytes)
	}
	var parsed jsonrpcResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse response body: %w", err)
	}
	if parsed.ID == nil {
		return nil, fmt.Errorf("response missing id")
	}
	if *parsed.ID != wantID {
		return nil, fmt.Errorf("response id %d does not match request id %d", *parsed.ID, wantID)
	}
	return &parsed, nil
}

// readSSEForJSONRPC scans SSE events for the first JSON-RPC response matching
// wantID. Events without valid JSON-RPC data are skipped (SSE streams can
// carry heartbeats, comments, or prefix events). io.EOF with no match surfaces
// as a clean error so callers know the stream closed unexpectedly.
func readSSEForJSONRPC(r io.Reader, wantID int) (*jsonrpcResponse, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		ev, err := readSSEEvent(br)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("SSE stream ended before response to request %d", wantID)
			}
			return nil, err
		}
		if ev.data == "" {
			continue
		}
		var parsed jsonrpcResponse
		if err := json.Unmarshal([]byte(ev.data), &parsed); err != nil {
			continue
		}
		if parsed.ID == nil || *parsed.ID != wantID {
			continue
		}
		return &parsed, nil
	}
}
