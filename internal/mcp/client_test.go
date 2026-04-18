package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- Test helper: this binary re-execs itself as a mock MCP server ---
//
// When MCP_TEST_SERVER is set, the test binary becomes a tiny MCP server
// that responds to initialize and tools/list over stdin/stdout.

func TestMain(m *testing.M) {
	mode := os.Getenv("MCP_TEST_SERVER")
	if mode != "" {
		runTestServer(mode)
		return
	}
	os.Exit(m.Run())
}

func runTestServer(mode string) {
	switch mode {
	case "happy":
		testServerHappy(newNewlineReader(os.Stdin), newlineWriter{os.Stdout})
	case "happy_cl":
		testServerHappy(newContentLengthReader(os.Stdin), contentLengthWriter{os.Stdout})
	case "no_tools_cap":
		testServerNoToolsCap(newNewlineReader(os.Stdin), newlineWriter{os.Stdout})
	case "no_tools_cap_cl":
		testServerNoToolsCap(newContentLengthReader(os.Stdin), contentLengthWriter{os.Stdout})
	case "empty_tools":
		testServerEmptyTools(newNewlineReader(os.Stdin), newlineWriter{os.Stdout})
	case "crash":
		os.Exit(1)
	case "hang":
		select {} // block forever; context timeout kills us
	case "error_response":
		testServerErrorResponse(newNewlineReader(os.Stdin), newlineWriter{os.Stdout})
	case "error_response_cl":
		testServerErrorResponse(newContentLengthReader(os.Stdin), contentLengthWriter{os.Stdout})
	case "hostile_content_length":
		// Respond to initialize with a legitimate message so the handshake
		// progresses, then emit an oversized Content-Length on the
		// tools/list response. The client must reject without attempting
		// to allocate the claimed bytes.
		testServerHostileContentLength(newContentLengthReader(os.Stdin), contentLengthWriter{os.Stdout})
	case "hostile_no_newline":
		// Emit a valid initialize response then stream non-newline bytes
		// forever so the client must bound the line read.
		testServerHostileNoNewline(newNewlineReader(os.Stdin), newlineWriter{os.Stdout})
	case "spawn_grandchild":
		// Unix-only — registered by client_grandchild_unix_test.go. On
		// non-unix, no handler is registered and we just exit so the test
		// (skipped) doesn't hang.
		if spawnGrandchildServer != nil {
			spawnGrandchildServer()
			return
		}
		os.Exit(6)
	}
}

// spawnGrandchildServer is the spawn_grandchild handler, set from a
// build-tagged file on Unix. nil on non-unix.
var spawnGrandchildServer func()

// --- Test server transport abstraction ---

type testReader interface {
	readReq() testReq
}

type testWriter interface {
	writeResp(id int, result any)
	writeError(id int, code int, message string)
}

// newline reader/writer (2024-11-05 spec)

type newlineReader struct{ scanner *bufio.Scanner }

func newNewlineReader(r io.Reader) newlineReader {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20)
	return newlineReader{s}
}

func (nr newlineReader) readReq() testReq {
	for nr.scanner.Scan() {
		line := nr.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req testReq
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		return req
	}
	os.Exit(3)
	return testReq{}
}

type newlineWriter struct{ w io.Writer }

func (nw newlineWriter) writeResp(id int, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintf(nw.w, "%s\n", b)
}

func (nw newlineWriter) writeError(id int, code int, message string) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	})
	fmt.Fprintf(nw.w, "%s\n", b)
}

// Content-Length reader/writer (2025-03-26 spec)

type contentLengthReader struct{ r *bufio.Reader }

func newContentLengthReader(r io.Reader) contentLengthReader {
	return contentLengthReader{bufio.NewReaderSize(r, 1<<20)}
}

func (cr contentLengthReader) readReq() testReq {
	for {
		// Skip whitespace between messages.
		for {
			b, err := cr.r.Peek(1)
			if err != nil {
				os.Exit(3)
			}
			if b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n' {
				cr.r.ReadByte()
				continue
			}
			break
		}

		b, _ := cr.r.Peek(1)
		if b[0] == '{' {
			// Bare JSON (newline-delimited client fallback).
			line, err := cr.r.ReadBytes('\n')
			if err != nil && len(line) == 0 {
				os.Exit(3)
			}
			var req testReq
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			return req
		}

		// Content-Length header.
		var contentLength int
		for {
			line, err := cr.r.ReadString('\n')
			if err != nil {
				os.Exit(3)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if after, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				contentLength, _ = strconv.Atoi(strings.TrimSpace(after))
			}
		}
		if contentLength <= 0 {
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(cr.r, body); err != nil {
			os.Exit(3)
		}
		var req testReq
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}
		return req
	}
}

type contentLengthWriter struct{ w io.Writer }

func (cw contentLengthWriter) writeResp(id int, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintf(cw.w, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (cw contentLengthWriter) writeError(id int, code int, message string) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	})
	fmt.Fprintf(cw.w, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

// --- Test server scenarios (transport-agnostic) ---

type testReq struct {
	Method string `json:"method"`
	ID     *int   `json:"id"`
}

func testServerHappy(r testReader, w testWriter) {
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	r.readReq() // initialized notification
	req = r.readReq()
	if req.Method != "tools/list" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"tools": []map[string]any{
			{"name": "tool_alpha", "description": "Alpha tool"},
			{"name": "tool_beta", "description": "Beta tool"},
			{"name": "tool_gamma", "description": "Gamma tool"},
		},
	})
}

func testServerNoToolsCap(r testReader, w testWriter) {
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "test-no-tools", "version": "1.0"},
	})
}

func testServerEmptyTools(r testReader, w testWriter) {
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "test-empty", "version": "1.0"},
	})
	r.readReq() // initialized notification
	req = r.readReq()
	if req.Method != "tools/list" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{"tools": []map[string]any{}})
}

func testServerErrorResponse(r testReader, w testWriter) {
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "test-error", "version": "1.0"},
	})
	r.readReq() // initialized notification
	req = r.readReq()
	w.writeError(*req.ID, -32601, "method not found")
}

// testServerHostileContentLength finishes the handshake normally then emits
// a Content-Length header claiming an astronomically large body. A correct
// client rejects before allocating.
func testServerHostileContentLength(r testReader, _ contentLengthWriter) {
	cw := contentLengthWriter{os.Stdout}
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	cw.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "hostile", "version": "1.0"},
	})
	r.readReq() // initialized
	_ = r.readReq()
	// 1 GiB — well above any legitimate MCP payload, and above our cap.
	// We never send the body, so if the client allocates we still OOM; the
	// client's job is to refuse based on the header alone.
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", 1024*1024*1024)
	// Hold the pipe open briefly so the client has time to read the header.
	time.Sleep(2 * time.Second)
}

// testServerHostileNoNewline emits a plausible initialize response then
// streams non-newline bytes forever to force the client's line reader to
// either grow unbounded or hit a cap and return. We cap the total output
// at 16 MiB so the test doesn't actually blow up the machine if the cap
// regresses — the test assertion on client behavior (error within the
// context window) still pins the contract.
func testServerHostileNoNewline(r testReader, w testWriter) {
	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "hostile-newline", "version": "1.0"},
	})
	r.readReq() // initialized
	_ = r.readReq()
	// Write a blob of x's without any newline, then hold.
	chunk := bytes.Repeat([]byte{'x'}, 1024)
	for range 16 * 1024 { // 16 MiB total — above the 10 MiB client cap
		if _, err := os.Stdout.Write(chunk); err != nil {
			return
		}
	}
	time.Sleep(2 * time.Second)
}

// --- Actual tests ---

func testCommand(_ string) []string {
	exe, _ := os.Executable()
	return []string{exe, "-test.run=^$"}
}

func testEnv(mode string) map[string]string {
	return map[string]string{"MCP_TEST_SERVER": mode}
}

func TestProbeStdio_HappyPath(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := ProbeStdio(ctx, testCommand("happy"), testEnv("happy"))
	if err != nil {
		t.Fatalf("ProbeStdio: %v", err)
	}
	if len(result.Tools) != 3 {
		t.Fatalf("tools count = %d, want 3", len(result.Tools))
	}
	names := result.ToolNames()
	slices.Sort(names)
	want := []string{"tool_alpha", "tool_beta", "tool_gamma"}
	if !slices.Equal(names, want) {
		t.Errorf("tool names = %v, want %v", names, want)
	}
	if result.Duration <= 0 {
		t.Errorf("duration should be positive, got %v", result.Duration)
	}
}

func TestProbeStdio_HappyPathContentLength(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := ProbeStdio(ctx, testCommand("happy_cl"), testEnv("happy_cl"))
	if err != nil {
		t.Fatalf("ProbeStdio against Content-Length server: %v", err)
	}
	if len(result.Tools) != 3 {
		t.Fatalf("tools count = %d, want 3", len(result.Tools))
	}
	names := result.ToolNames()
	slices.Sort(names)
	want := []string{"tool_alpha", "tool_beta", "tool_gamma"}
	if !slices.Equal(names, want) {
		t.Errorf("tool names = %v, want %v", names, want)
	}
}

func TestProbeStdio_NoToolsCapability(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("no_tools_cap"), testEnv("no_tools_cap"))
	if err == nil {
		t.Fatal("expected error for server without tools capability")
	}
	if !strings.Contains(err.Error(), "tools capability") {
		t.Errorf("error = %q, want mention of tools capability", err)
	}
}

func TestProbeStdio_NoToolsCapabilityContentLength(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("no_tools_cap_cl"), testEnv("no_tools_cap_cl"))
	if err == nil {
		t.Fatal("expected error for server without tools capability")
	}
	if !strings.Contains(err.Error(), "tools capability") {
		t.Errorf("error = %q, want mention of tools capability", err)
	}
}

func TestProbeStdio_EmptyTools(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := ProbeStdio(ctx, testCommand("empty_tools"), testEnv("empty_tools"))
	if err != nil {
		t.Fatalf("ProbeStdio: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("tools = %v, want empty", result.Tools)
	}
}

func TestProbeStdio_ServerCrash(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("crash"), testEnv("crash"))
	if err == nil {
		t.Fatal("expected error for crashing server")
	}
}

func TestProbeStdio_Timeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("hang"), testEnv("hang"))
	if err == nil {
		t.Fatal("expected error for hanging server")
	}
}

func TestProbeStdio_ErrorResponse(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("error_response"), testEnv("error_response"))
	if err == nil {
		t.Fatal("expected error for JSON-RPC error response")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error = %q, want mention of 'method not found'", err)
	}
}

func TestProbeStdio_ErrorResponseContentLength(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := ProbeStdio(ctx, testCommand("error_response_cl"), testEnv("error_response_cl"))
	if err == nil {
		t.Fatal("expected error for JSON-RPC error response")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error = %q, want mention of 'method not found'", err)
	}
}

func TestProbeStdio_EmptyCommand(t *testing.T) {
	t.Parallel()
	_, err := ProbeStdio(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestProbeStdio_BadCommand(t *testing.T) {
	t.Parallel()
	_, err := ProbeStdio(context.Background(), []string{"/nonexistent/binary"}, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}

// TestProbeStdio_HostileContentLengthRejectedFast pins that an MCP server
// declaring an absurd Content-Length is refused before the client tries
// to allocate. The error lands well inside the context budget and mentions
// the cap so operators know why.
func TestProbeStdio_HostileContentLengthRejectedFast(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err := ProbeStdio(ctx, testCommand("hostile_content_length"), testEnv("hostile_content_length"))
	if err == nil {
		t.Fatal("expected error for hostile Content-Length")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — hostile Content-Length should fail fast, not time out", elapsed)
	}
	if !strings.Contains(err.Error(), "Content-Length") || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %q, want mention of Content-Length cap", err)
	}
}

// TestProbeStdio_HostileNoNewlineRejectedFast pins that an MCP server in
// newline framing that streams non-newline bytes forever is refused when
// the per-message cap is exceeded, well before context timeout.
func TestProbeStdio_HostileNoNewlineRejectedFast(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err := ProbeStdio(ctx, testCommand("hostile_no_newline"), testEnv("hostile_no_newline"))
	if err == nil {
		t.Fatal("expected error for non-newline flood")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("took %v — line-length cap should fire well inside context window", elapsed)
	}
	if !strings.Contains(err.Error(), "max size") && !strings.Contains(err.Error(), "newline") {
		t.Errorf("error = %q, want cap-triggered wording", err)
	}
}
