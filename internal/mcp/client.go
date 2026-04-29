package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxMCPMessageBytes caps the size of a single JSON-RPC message body read
// from an MCP server's stdout. Hostile or buggy servers can declare
// arbitrarily large Content-Length values or stream gigabytes without a
// newline; without a cap, `ProbeStdio` would allocate or accumulate
// unbounded memory before the context timeout fires. 10 MiB is comfortably
// above any legitimate tools/list payload while keeping a single bad
// server from OOMing the aipack process.
const maxMCPMessageBytes = 10 * 1024 * 1024

// Tool represents a single tool from the MCP tools/list response.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ProbeResult holds the outcome of probing a single MCP server.
type ProbeResult struct {
	Tools    []Tool
	Duration time.Duration
}

// ProbeStdio starts an MCP server subprocess, performs the JSON-RPC 2.0
// handshake (initialize + initialized notification), calls tools/list,
// and returns the discovered tools. The process is shut down cleanly by
// closing stdin; if it doesn't exit within 5 seconds, it is killed.
//
// Messages are written with Content-Length headers plus a trailing newline
// so both Content-Length (2025-03-26 spec) and newline-delimited
// (2024-11-05 spec) servers can parse the input. Responses are auto-detected
// by peeking at the first byte: 'C'/'c' → Content-Length, '{' → newline.
//
// The env parameter overlays additional environment variables on top of
// the current process environment. Pass nil for no overlay.
//
// stdout (when non-nil) receives `  connected (stdio)` and `  listing tools`
// lines for live progress; events (when non-nil) receives the same
// milestones as ProbeEvent values. Lifecycle starting/done/error events are
// emitted by Probe.
func ProbeStdio(ctx context.Context, command []string, env map[string]string, stdoutW io.Writer, events chan<- ProbeEvent) (*ProbeResult, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	start := time.Now()

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = buildEnv(env)
	cmd.Stderr = nil // discard; captured via Wait if needed
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}

	// Ensure the process is cleaned up regardless of how we exit.
	var killOnce sync.Once
	cleanup := func() {
		killOnce.Do(func() {
			stdin.Close()
			done := make(chan struct{})
			go func() {
				cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// Kill the whole process group on Unix so worker subprocesses
				// spawned by the MCP server (node pools, Python
				// multiprocessing) don't outlive the probe. On non-unix
				// platforms this falls back to killing just the PID; see
				// proc_other.go.
				killProcessGroup(cmd)
				<-done
			}
		})
	}
	defer cleanup()

	reader := bufio.NewReaderSize(stdout, 1<<20) // 1MB buffer

	if err := writeMessage(stdin, &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      intID(1),
		Method:  "initialize",
		Params: initializeParams{
			ProtocolVersion: "2024-11-05",
			Capabilities:    map[string]any{},
			ClientInfo:      clientInfo{Name: "aipack", Version: "probe"},
		},
	}); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}

	initResp, err := readResponse(reader, 1)
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

	if err := writeMessage(stdin, &jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	if stdoutW != nil {
		fmt.Fprintln(stdoutW, "  connected (stdio)")
	}
	emitProbeEvent(events, ProbeEvent{Transport: "stdio", Phase: ProbePhaseConnected})
	if stdoutW != nil {
		fmt.Fprintln(stdoutW, "  listing tools")
	}
	emitProbeEvent(events, ProbeEvent{Phase: ProbePhaseListingTools})

	if err := writeMessage(stdin, &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      intID(2),
		Method:  "tools/list",
		Params:  map[string]any{},
	}); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}

	toolsResp, err := readResponse(reader, 2)
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

// ToolNames returns tool names from the probe result in discovery order.
func (r *ProbeResult) ToolNames() []string {
	names := make([]string, len(r.Tools))
	for i, t := range r.Tools {
		names[i] = t.Name
	}
	return names
}

// --- JSON-RPC 2.0 types ---

type intID int

func (id intID) MarshalJSON() ([]byte, error) { return json.Marshal(int(id)) }

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      intID  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type jsonrpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP-specific types ---

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      clientInfo     `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	Capabilities struct {
		Tools *json.RawMessage `json:"tools"`
	} `json:"capabilities"`
}

type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

// --- helpers ---

// writeMessage sends a JSON-RPC message using Content-Length framing with a
// trailing newline. Content-Length servers parse the header and body normally
// (the trailing newline falls between messages as whitespace). Newline servers
// skip the header lines (they fail json.Unmarshal) and parse the JSON body
// that appears on its own line.
func writeMessage(w io.Writer, msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

// readResponse reads JSON-RPC responses, auto-detecting framing. It peeks at
// the next non-whitespace byte: 'C'/'c' → Content-Length header, '{' → bare
// JSON line. Notifications (no id) and non-matching IDs are skipped.
func readResponse(r *bufio.Reader, wantID int) (*jsonrpcResponse, error) {
	for {
		if err := skipWhitespace(r); err != nil {
			return nil, fmt.Errorf("read stdout: %w", err)
		}

		b, err := r.Peek(1)
		if err != nil {
			return nil, fmt.Errorf("read stdout: %w", err)
		}

		var msg []byte
		switch {
		case b[0] == 'C' || b[0] == 'c':
			msg, err = readContentLengthBody(r)
		case b[0] == '{':
			msg, err = readNewlinebody(r)
		default:
			// Unrecognized prefix — discard the line and retry. Use the
			// bounded line reader so a hostile server streaming non-newline
			// bytes under a non-JSON prefix can't drag us into an OOM.
			_, err = readNewlinebody(r)
			if err == nil {
				continue
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read stdout: %w", err)
		}
		if len(msg) == 0 {
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue // skip non-JSON content
		}
		if resp.ID == nil {
			continue // notification, not a response
		}
		if *resp.ID != wantID {
			continue // response to a different request
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return &resp, nil
	}
}

// readContentLengthBody parses Content-Length headers then reads exactly that
// many bytes as the message body.
func readContentLengthBody(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line separates headers from body
		}
		if after, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, perr := strconv.Atoi(strings.TrimSpace(after))
			if perr != nil {
				return nil, fmt.Errorf("bad Content-Length value: %w", perr)
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing or zero Content-Length")
	}
	if contentLength > maxMCPMessageBytes {
		return nil, fmt.Errorf("Content-Length %d exceeds max message size %d", contentLength, maxMCPMessageBytes)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// readNewlinebody reads a single newline-delimited message, capped at
// maxMCPMessageBytes. A hostile server that streams bytes without a newline
// would otherwise grow `bufio.Reader.ReadBytes`'s internal buffer until
// the parent OOMs; read byte-by-byte with an explicit counter so we can
// surface a clean error at the cap.
func readNewlinebody(r *bufio.Reader) ([]byte, error) {
	buf := make([]byte, 0, 512)
	for {
		b, err := r.ReadByte()
		if err != nil {
			if len(buf) == 0 {
				return nil, err
			}
			return bytes.TrimSpace(buf), nil
		}
		if b == '\n' {
			return bytes.TrimSpace(buf), nil
		}
		if len(buf) >= maxMCPMessageBytes {
			return nil, fmt.Errorf("message exceeds max size %d bytes without newline", maxMCPMessageBytes)
		}
		buf = append(buf, b)
	}
}

// skipWhitespace discards whitespace bytes (space, tab, \r, \n) from the
// reader until a non-whitespace byte is available or EOF.
func skipWhitespace(r *bufio.Reader) error {
	for {
		b, err := r.Peek(1)
		if err != nil {
			return err
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			r.ReadByte()
		default:
			return nil
		}
	}
}

func buildEnv(overlay map[string]string) []string {
	base := os.Environ()
	if len(overlay) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(overlay))
	env = append(env, base...)
	for k, v := range overlay {
		env = append(env, k+"="+v)
	}
	return env
}
