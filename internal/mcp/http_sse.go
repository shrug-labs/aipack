package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ProbeSSE probes an MCP server via the legacy HTTP+SSE transport (MCP spec
// 2024-11-05). The client opens a long-lived GET to the SSE endpoint; the
// first event carries the POST URL used for all subsequent JSON-RPC messages,
// whose responses come back as `message` events on the same SSE stream.
//
// The transport is bidirectional with request/response correlation by ID, so
// a background reader goroutine dispatches responses to per-request channels
// from a pending-map. Reader exit (EOF or read error) cancels an internal
// context, waking every outstanding sendRequest with a clear error instead of
// letting them hang until the caller's context deadline.
//
// Per-event body size is bounded by maxMCPMessageBytes (via readSSEEvent) so
// a malicious or runaway server cannot grow the parent process by streaming
// without framing — matching the same cap stdio and streamable-http enforce.
func ProbeSSE(ctx context.Context, endpointURL string, headers map[string]string) (*ProbeResult, error) {
	if endpointURL == "" {
		return nil, fmt.Errorf("empty URL")
	}
	start := time.Now()

	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	client := &http.Client{}

	streamReq, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		streamReq.Header.Set(k, v)
	}
	streamReq.Header.Set("Accept", "text/event-stream")

	streamResp, err := client.Do(streamReq)
	if err != nil {
		return nil, fmt.Errorf("open SSE stream: %w", err)
	}
	defer streamResp.Body.Close()
	if err := checkHTTPStatus(endpointURL, streamResp); err != nil {
		return nil, err
	}

	br := bufio.NewReaderSize(streamResp.Body, 1<<20)

	// First event must carry the POST URL as `endpoint` data. Everything else
	// is advisory; if the first event isn't endpoint, the server isn't
	// speaking the HTTP+SSE transport and we can't proceed.
	ev, err := readSSEEvent(br)
	if err != nil {
		return nil, fmt.Errorf("read endpoint event: %w", err)
	}
	if ev.event != "endpoint" {
		return nil, fmt.Errorf(`first SSE event must be "endpoint", got %q`, ev.event)
	}
	if ev.data == "" {
		return nil, fmt.Errorf("endpoint event missing URL")
	}

	base, err := url.Parse(endpointURL)
	if err != nil {
		return nil, fmt.Errorf("parse stream URL: %w", err)
	}
	endpointRef, err := base.Parse(ev.data)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint URL: %w", err)
	}
	postURL := endpointRef.String()

	// pending maps JSON-RPC request ID → delivery channel. Held while a
	// request is in flight; removed either by the response arriving or by
	// the defer in sendRequest on any exit path. pendingMu also guards
	// readerErr so a sendRequest that wakes on probeCtx can report the
	// exact reader failure rather than a generic "stream closed".
	pending := map[int]chan *jsonrpcResponse{}
	var pendingMu sync.Mutex
	var readerErr error

	go func() {
		defer cancel() // wake every pending sendRequest when reader exits
		for {
			ev, err := readSSEEvent(br)
			if err != nil {
				pendingMu.Lock()
				readerErr = err
				pendingMu.Unlock()
				return
			}
			// SSE `event:` is optional; default event type is "message". We
			// accept either shape so servers that omit the event field still work.
			if ev.event != "" && ev.event != "message" {
				continue
			}
			if ev.data == "" {
				continue
			}
			var resp jsonrpcResponse
			if err := json.Unmarshal([]byte(ev.data), &resp); err != nil {
				continue
			}
			if resp.ID == nil {
				continue
			}
			pendingMu.Lock()
			ch, ok := pending[*resp.ID]
			if ok {
				delete(pending, *resp.ID)
			}
			pendingMu.Unlock()
			if ok {
				ch <- &resp
			}
		}
	}()

	sendRequest := func(id int, method string, params any) (*jsonrpcResponse, error) {
		ch := make(chan *jsonrpcResponse, 1)
		pendingMu.Lock()
		pending[id] = ch
		pendingMu.Unlock()
		defer func() {
			pendingMu.Lock()
			delete(pending, id)
			pendingMu.Unlock()
		}()

		if err := sseSendPOST(probeCtx, client, postURL, headers, &jsonrpcRequest{
			JSONRPC: "2.0",
			ID:      intID(id),
			Method:  method,
			Params:  params,
		}); err != nil {
			return nil, err
		}

		select {
		case resp := <-ch:
			if resp.Error != nil {
				return nil, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp, nil
		case <-probeCtx.Done():
			pendingMu.Lock()
			rerr := readerErr
			pendingMu.Unlock()
			if rerr != nil {
				return nil, fmt.Errorf("SSE stream closed: %w", rerr)
			}
			return nil, probeCtx.Err()
		}
	}

	initResp, err := sendRequest(1, "initialize", initializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo:      clientInfo{Name: "aipack", Version: "probe"},
	})
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

	if err := sseSendPOST(probeCtx, client, postURL, headers, &jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	toolsResp, err := sendRequest(2, "tools/list", map[string]any{})
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

// sseSendPOST serializes msg as JSON and POSTs it to postURL. Both requests
// and notifications share this code path because the transport-level framing
// is identical; response correlation (or absence, for notifications) is the
// caller's concern. Response body is drained and discarded — over the HTTP+SSE
// transport, the POST HTTP response carries no JSON-RPC content; the response
// comes back on the SSE stream.
func sseSendPOST(ctx context.Context, client *http.Client, postURL string, headers map[string]string, msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMCPMessageBytes))
	return checkHTTPStatus(postURL, resp)
}
