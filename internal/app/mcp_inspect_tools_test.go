package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/testutil"
)

func TestResolveServerRef(t *testing.T) {
	t.Parallel()
	all := []mcpServerRef{
		{serverName: "alpha", packName: "pack-a"},
		{serverName: "beta", packName: "pack-a"},
		{serverName: "alpha", packName: "pack-b"},
	}

	t.Run("bare name unique", func(t *testing.T) {
		t.Parallel()
		got, err := resolveServerRef(all, "beta")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].packName != "pack-a" {
			t.Errorf("got %+v, want pack-a/beta", got)
		}
	})

	t.Run("bare name ambiguous", func(t *testing.T) {
		t.Parallel()
		_, err := resolveServerRef(all, "alpha")
		if err == nil {
			t.Fatal("expected error for ambiguous name")
		}
	})

	t.Run("pack/server qualified", func(t *testing.T) {
		t.Parallel()
		got, err := resolveServerRef(all, "pack-b/alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].packName != "pack-b" {
			t.Errorf("got %+v, want pack-b/alpha", got)
		}
	})

	t.Run("not found bare", func(t *testing.T) {
		t.Parallel()
		_, err := resolveServerRef(all, "missing")
		if err == nil {
			t.Fatal("expected error for missing server")
		}
	})

	t.Run("not found qualified", func(t *testing.T) {
		t.Parallel()
		_, err := resolveServerRef(all, "pack-a/missing")
		if err == nil {
			t.Fatal("expected error for missing qualified ref")
		}
	})
}

func TestSaveInventoryTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")

	original := map[string]any{
		"command":         []string{"node", "server.js"},
		"env":             map[string]string{"TOKEN": "{env:MY_TOKEN}"},
		"available_tools": []string{"old_tool"},
		"notes":           "keep me",
	}
	b, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	newTools := []string{"tool_a", "tool_b", "tool_c"}
	if err := saveInventoryTools(path, newTools); err != nil {
		t.Fatalf("saveInventoryTools: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("parse saved file: %v", err)
	}

	var tools []string
	if err := json.Unmarshal(result["available_tools"], &tools); err != nil {
		t.Fatalf("parse available_tools: %v", err)
	}
	if len(tools) != 3 || tools[0] != "tool_a" || tools[1] != "tool_b" || tools[2] != "tool_c" {
		t.Errorf("tools = %v, want [tool_a tool_b tool_c]", tools)
	}

	var notes string
	if err := json.Unmarshal(result["notes"], &notes); err != nil {
		t.Fatalf("parse notes: %v", err)
	}
	if notes != "keep me" {
		t.Errorf("notes = %q, want %q (other fields should be preserved)", notes, "keep me")
	}
}

func TestDiscoverMCPServers(t *testing.T) {
	t.Parallel()
	packsDir := t.TempDir()

	// Create two packs with MCP servers.
	for _, pack := range []struct {
		name    string
		servers map[string]domain.MCPServer
	}{
		{
			name: "pack-a",
			servers: map[string]domain.MCPServer{
				"srv-one": {Command: []string{"srv-one"}},
				"srv-two": {Command: []string{"srv-two"}, Transport: "sse", URL: "http://localhost"},
			},
		},
		{
			name: "pack-b",
			servers: map[string]domain.MCPServer{
				"srv-one": {Command: []string{"srv-one-b"}},
			},
		},
	} {
		mcpDir := filepath.Join(packsDir, pack.name, "mcp")
		if err := os.MkdirAll(mcpDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, srv := range pack.servers {
			b, _ := json.Marshal(srv)
			if err := os.WriteFile(filepath.Join(mcpDir, name+".json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	refs, warnings, err := discoverMCPServers(packsDir)
	if err != nil {
		t.Fatalf("discoverMCPServers: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(refs) != 3 {
		t.Fatalf("got %d servers, want 3", len(refs))
	}

	// Sorted by pack then server name.
	if refs[0].packName != "pack-a" || refs[0].serverName != "srv-one" {
		t.Errorf("refs[0] = %s/%s, want pack-a/srv-one", refs[0].packName, refs[0].serverName)
	}
	if refs[1].packName != "pack-a" || refs[1].serverName != "srv-two" {
		t.Errorf("refs[1] = %s/%s, want pack-a/srv-two", refs[1].packName, refs[1].serverName)
	}
	if refs[2].packName != "pack-b" || refs[2].serverName != "srv-one" {
		t.Errorf("refs[2] = %s/%s, want pack-b/srv-one", refs[2].packName, refs[2].serverName)
	}
}

func TestServerNeedsProfileParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		server domain.MCPServer
		want   bool
	}{
		{
			name: "stdio with {env:*} only does NOT need profile params",
			server: domain.MCPServer{
				Transport: "stdio",
				Command:   []string{"my-mcp", "--home", "{env:HOME}"},
				Env:       map[string]string{"TOKEN": "{env:MCP_TOKEN}"},
			},
			want: false,
		},
		{
			name: "stdio with {params.*} in command needs profile params",
			server: domain.MCPServer{
				Transport: "stdio",
				Command:   []string{"my-mcp", "--workspace", "{params.workspace}"},
			},
			want: true,
		},
		{
			name: "stdio with {params.*} in env needs profile params",
			server: domain.MCPServer{
				Transport: "stdio",
				Command:   []string{"my-mcp"},
				Env:       map[string]string{"TOKEN": "{params.token}"},
			},
			want: true,
		},
		{
			name: "HTTP with {params.*} in URL needs profile params",
			server: domain.MCPServer{
				Transport: "sse",
				URL:       "https://mcp.example.com/{params.tenant}",
			},
			want: true,
		},
		{
			name: "HTTP with {params.*} in Headers needs profile params",
			server: domain.MCPServer{
				Transport: "sse",
				URL:       "https://mcp.example.com",
				Headers:   map[string]string{"Authorization": "Bearer {params.token}"},
			},
			want: true,
		},
		{
			name: "Literal command and env do not need profile params",
			server: domain.MCPServer{
				Transport: "stdio",
				Command:   []string{"my-mcp", "--port", "8080"},
				Env:       map[string]string{"DEBUG": "1"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := serverNeedsProfileParams(tc.server); got != tc.want {
				t.Errorf("serverNeedsProfileParams = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunMCPInspectToolsUsesConfigDotEnvForEnvRefs(t *testing.T) {
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	envKey := "AIPACK_TEST_INSPECT_DOTENV_CMD"
	unsetEnvForTest(t, envKey)
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte(envKey+"=definitely-not-a-real-aipack-mcp-command\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mcpDir := filepath.Join(configDir, "packs", "demo", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "srv.json"), []byte(`{"transport":"stdio","command":["{env:AIPACK_TEST_INSPECT_DOTENV_CMD}"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result := RunMCPInspectTools(t.Context(), MCPInspectToolsRequest{
		ConfigDir: configDir,
		ServerRef: "srv",
		Timeout:   time.Second,
	})
	if len(result.Results) != 1 {
		t.Fatalf("Results = %d, want 1: %+v", len(result.Results), result.Results)
	}
	got := result.Results[0]
	if got.Status == InspectStatusSkipped {
		t.Fatalf("Status = skipped, want probe to reach command execution via .env; error=%q", got.Error)
	}
	if strings.Contains(got.Error, "env var "+envKey+" is not set") {
		t.Fatalf("Error = %q, want .env value to satisfy env ref", got.Error)
	}
}

func TestDiscoverMCPServers_SurfacesMalformedInventoryAsWarning(t *testing.T) {
	t.Parallel()

	packsDir := t.TempDir()
	mcpDir := filepath.Join(packsDir, "broken-pack", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One valid, one malformed. The valid one must still list; the malformed
	// one must surface a warning rather than silently drop.
	validPath := filepath.Join(mcpDir, "good.json")
	if err := os.WriteFile(validPath, []byte(`{"name":"good","transport":"stdio","command":["echo"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(mcpDir, "bad.json")
	if err := os.WriteFile(badPath, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	refs, warnings, err := discoverMCPServers(packsDir)
	if err != nil {
		t.Fatalf("discoverMCPServers: %v", err)
	}
	if len(refs) != 1 || refs[0].serverName != "good" {
		t.Errorf("got refs=%v, want single valid server", refs)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "bad.json") || !strings.Contains(warnings[0], "parse") {
		t.Errorf("warning should mention parse failure for bad.json, got: %s", warnings[0])
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestRunMCPInspectTools_ListMode_AllowsMissingPacksDir(t *testing.T) {
	t.Parallel()

	result := RunMCPInspectTools(context.Background(), MCPInspectToolsRequest{
		ConfigDir: t.TempDir(),
	})

	if !result.OK {
		t.Fatalf("OK = false, want true; results=%+v", result.Results)
	}
	if !result.ListMode {
		t.Fatal("ListMode = false, want true")
	}
	if len(result.Servers) != 0 {
		t.Fatalf("got %d servers, want 0", len(result.Servers))
	}
}

func TestRunMCPInspectTools_ListMode_IncludesSymlinkedPacks(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)

	configDir := t.TempDir()
	packsDir := filepath.Join(configDir, "packs")
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	packDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "demo.json"), []byte(`{
  "name": "demo",
  "transport": "stdio",
  "command": ["echo"],
  "available_tools": ["tool_a"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, packDir, filepath.Join(packsDir, "linked-pack"))

	result := RunMCPInspectTools(context.Background(), MCPInspectToolsRequest{
		ConfigDir: configDir,
	})

	if !result.OK {
		t.Fatalf("OK = false, want true; results=%+v", result.Results)
	}
	if len(result.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(result.Servers))
	}
	if result.Servers[0].PackName != "linked-pack" || result.Servers[0].ServerName != "demo" {
		t.Fatalf("server = %+v, want linked-pack/demo", result.Servers[0])
	}
}

// mcpTestServerScript emits a POSIX /bin/sh fixture that speaks the exact wire
// format ProbeStdio writes: every outbound message is `Content-Length: N\r\n\r\n<body>\n`,
// which reads as three newline-terminated lines (header / blank / body). The
// script reads three lines per expected message — initialize, initialized
// notification, tools/list — and replies to the request messages with
// newline-framed JSON-RPC responses. An earlier version of this fixture read
// one line per message (matching the 2024-11-05 newline-only spec) and failed
// on CI once Content-Length framing was added: the shell exited after reading
// only the initialize header line, so by the time the probe tried to send the
// initialized notification the pipe was already closed.
func mcpTestServerScript() string {
	const initResp = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1.0"}}}`
	const toolsResp = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"tool_alpha"},{"name":"tool_beta"}]}}`
	return "#!/bin/sh\n" +
		"readmsg() {\n" +
		"  IFS= read -r _ || exit 1\n" + // Content-Length header
		"  IFS= read -r _ || exit 1\n" + // blank separator
		"  IFS= read -r _ || exit 1\n" + // body
		"}\n" +
		"readmsg\n" + // initialize request
		"printf '%s\\n' '" + initResp + "'\n" +
		"readmsg\n" + // initialized notification (no response)
		"readmsg\n" + // tools/list request
		"printf '%s\\n' '" + toolsResp + "'\n"
}

func TestRunMCPInspectTools_SaveFailureMarksServerError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not reliable for this failure mode on Windows")
	}

	configDir := t.TempDir()
	mcpDir := filepath.Join(configDir, "packs", "test-pack", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte("schema_version: 2\npacks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(t.TempDir(), "mcp-server.sh")
	script := mcpTestServerScript()
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "demo.json"), []byte(`{
  "name": "demo",
  "transport": "stdio",
  "command": ["`+scriptPath+`"],
  "available_tools": ["old_tool"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mcpDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(mcpDir, 0o755)
	})

	result := RunMCPInspectTools(context.Background(), MCPInspectToolsRequest{
		ConfigDir: configDir,
		ServerRef: "demo",
		Save:      true,
	})

	if result.OK {
		t.Fatal("OK = true, want false")
	}
	if len(result.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Results))
	}
	if result.Results[0].Status != InspectStatusError {
		t.Fatalf("status = %q, want %q", result.Results[0].Status, InspectStatusError)
	}
	if !strings.Contains(result.Results[0].Error, "save failed") {
		t.Fatalf("error = %q, want save failure", result.Results[0].Error)
	}
}

func TestRunMCPInspectTools_ProbeMode_DefaultStdioWithoutProfile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell script probe fixture is not portable to Windows")
	}

	configDir := t.TempDir()
	mcpDir := filepath.Join(configDir, "packs", "test-pack", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(t.TempDir(), "mcp-server.sh")
	if err := os.WriteFile(scriptPath, []byte(mcpTestServerScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "demo.json"), []byte(`{
  "name": "demo",
  "command": ["`+scriptPath+`"],
  "available_tools": ["old_tool"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := RunMCPInspectTools(context.Background(), MCPInspectToolsRequest{
		ConfigDir: configDir,
		ServerRef: "demo",
	})

	if !result.OK {
		t.Fatalf("OK = false, want true; results=%+v", result.Results)
	}
	if len(result.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Results))
	}
	if result.Results[0].Transport != domain.TransportStdio {
		t.Fatalf("transport = %q, want %q", result.Results[0].Transport, domain.TransportStdio)
	}
	if result.Results[0].Status != InspectStatusOK {
		t.Fatalf("status = %q, want %q", result.Results[0].Status, InspectStatusOK)
	}
}

func TestRunMCPInspectTools_ListMode_SkipsHiddenBackupDirs(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	visibleMCPDir := filepath.Join(configDir, "packs", "visible-pack", "mcp")
	if err := os.MkdirAll(visibleMCPDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visibleMCPDir, "demo.json"), []byte(`{
  "name": "demo",
  "command": ["echo"],
  "available_tools": ["tool_a"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	hiddenMCPDir := filepath.Join(configDir, "packs", ".visible-pack.bak-20260405T120000Z-deadbeef", "mcp")
	if err := os.MkdirAll(hiddenMCPDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenMCPDir, "demo.json"), []byte(`{
  "name": "demo",
  "command": ["echo"],
  "available_tools": ["tool_b"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := RunMCPInspectTools(context.Background(), MCPInspectToolsRequest{
		ConfigDir: configDir,
	})

	if !result.OK {
		t.Fatalf("OK = false, want true; results=%+v", result.Results)
	}
	if len(result.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(result.Servers))
	}
	if result.Servers[0].PackName != "visible-pack" {
		t.Fatalf("pack = %q, want %q", result.Servers[0].PackName, "visible-pack")
	}
	if result.Servers[0].Transport != domain.TransportStdio {
		t.Fatalf("transport = %q, want %q", result.Servers[0].Transport, domain.TransportStdio)
	}
}

func TestRunMCPInspectTools_ProbeFailureMarksNotOK(t *testing.T) {
	t.Parallel()

	// SSE targets are probed in v0.23; an unreachable endpoint surfaces as
	// InspectStatusError. result.OK must flip to false so the CLI wrapper
	// returns ExitFail.
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := RunMCPInspectTools(ctx, MCPInspectToolsRequest{
		ConfigDir: configDir,
		ServerRef: "demo",
		Timeout:   2 * time.Second,
	})

	if result.OK {
		t.Fatalf("OK = true, want false when target probe errors; results=%+v", result.Results)
	}
	if len(result.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Results))
	}
	if result.Results[0].Status != InspectStatusError {
		t.Fatalf("status = %q, want %q", result.Results[0].Status, InspectStatusError)
	}
}
