package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

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

	_, _, code := runApp(t, "mcp", "inspect-tools", "does-not-exist", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("expected ExitUsage (%d) for unknown server, got %d", cmdutil.ExitUsage, code)
	}
}
