package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestImportedMCPInspectToolsCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage
			Method string
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{"tools": []map[string]any{{"name": "owned-tool", "inputSchema": map[string]any{"type": "object"}}}}
		if request.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer api.Close()
	src, cfg := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, ".claude-plugin/plugin.json"), []byte(`{"name":"probe","version":"1.0.0"}`))
	writeFile(t, filepath.Join(src, ".mcp.json"), []byte(`{"mcpServers":{"probe":{"type":"http","url":"`+api.URL+`"}}}`))
	run := func(args ...string) string {
		t.Helper()
		args = append(args, "--config-dir", cfg)
		if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %v: %s", args, err, out)
			}
			return string(out)
		}
		out, stderr, code := runApp(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d: %s %s", args, code, out, stderr)
		}
		return out
	}
	run("pack", "install", src)
	var report app.MCPInspectToolsResult
	if err := json.Unmarshal([]byte(run("mcp", "inspect-tools", "probe/probe", "--save", "--dry-run", "--json")), &report); err != nil || !report.OK || !report.Results[0].WouldSave {
		t.Fatalf("dry-run failed: %+v %v", report, err)
	}
	if _, err := os.Stat(app.MCPProbeCachePath(cfg)); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote probe cache", err)
	}
	if err := json.Unmarshal([]byte(run("mcp", "inspect-tools", "probe/probe", "--save", "--json")), &report); err != nil || !report.OK || !report.Results[0].Saved || report.Results[0].InventoryPath != app.MCPProbeCachePath(cfg) || report.Results[0].ToolCount != 1 {
		t.Fatalf("probe/cache failed: %+v %v", report, err)
	}
}

func TestMCPInspectTools_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "mcp", "inspect-tools", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("mcp inspect-tools --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestMCPInspectTools_ServerAndAllAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "mcp", "inspect-tools", "demo", "--all", "--config-dir", t.TempDir())
	if code == cmdutil.ExitOK {
		t.Fatal("mcp inspect-tools demo --all should fail")
	}
	if !strings.Contains(stderr, "cannot be combined") {
		t.Fatalf("expected mutual exclusion message, got: %s", stderr)
	}
}

func TestMCPInspectTools_RejectsNonPositiveTimeout(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "mcp", "inspect-tools", "--timeout", "0", "--config-dir", t.TempDir())
	if code == cmdutil.ExitOK {
		t.Fatal("mcp inspect-tools --timeout 0 should fail")
	}
	if !strings.Contains(stderr, "must be > 0") {
		t.Fatalf("expected timeout validation message, got: %s", stderr)
	}
}

func TestMCPInspectTools_ProbeFailureReturnsExitFail(t *testing.T) {
	t.Parallel()

	// An SSE server pointing at a port with nothing listening should fail to
	// connect. v0.23 dispatches on transport, so SSE is no longer a silent
	// skip — the error status still maps to ExitFail because it's a runtime
	// outcome, not bad user input.
	configDir := t.TempDir()
	mcpDir := filepath.Join(configDir, "packs", "test-pack", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "demo.json"), []byte(`{
  "name": "demo",
  "transport": "sse",
  "url": "http://127.0.0.1:1/mcp"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "mcp", "inspect-tools", "demo", "--config-dir", configDir, "--timeout", "3")
	if code != cmdutil.ExitFail {
		t.Fatalf("expected ExitFail (%d) for probe failure, got %d; stdout=%q stderr=%q", cmdutil.ExitFail, code, stdout, stderr)
	}
	if !strings.Contains(stderr, "error") {
		t.Fatalf("expected error output on stderr, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestMCPInspectTools_UnknownServer_ExitUsage(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	mcpDir := filepath.Join(configDir, "packs", "test-pack", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "real.json"), []byte(`{
  "name": "real",
  "transport": "stdio",
  "command": ["echo", "hi"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "mcp", "inspect-tools", "does-not-exist", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("expected ExitUsage (%d) for unknown server, got %d", cmdutil.ExitUsage, code)
	}
	if stdout != "" {
		t.Fatalf("unknown-server error should not write stdout, got: %q", stdout)
	}
	if !strings.Contains(stderr, "does-not-exist") {
		t.Fatalf("unknown-server error should be reported on stderr, got: %q", stderr)
	}
}
