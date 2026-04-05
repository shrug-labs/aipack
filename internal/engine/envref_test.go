package engine

import (
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestExpandRefs_Params(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url":       "https://jira.example.com",
		"confluence_url": "https://wiki.example.com",
	}
	out, err := ExpandRefs(params, "Visit {params.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandRefs_LegacyParamSyntax(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url": "https://jira.example.com",
	}
	out, err := ExpandRefs(params, "Visit {param.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandRefs_LegacyGlobalSyntax(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url": "https://jira.example.com",
	}
	out, err := ExpandRefs(params, "Visit {global.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandRefs_UnresolvedParam(t *testing.T) {
	t.Parallel()
	_, err := ExpandRefs(nil, "Has {params.unknown_field}")
	if err == nil {
		t.Error("expected error for unresolved param")
	}
}

func TestExpandRefs_EnvVar(t *testing.T) {
	t.Setenv("TEST_EXPAND_VAR", "/resolved/path")
	out, err := ExpandRefs(nil, "{env:TEST_EXPAND_VAR}/bin")
	if err != nil {
		t.Fatal(err)
	}
	if out != "/resolved/path/bin" {
		t.Errorf("out = %q, want /resolved/path/bin", out)
	}
}

func TestExpandRefs_UnresolvedEnvVar(t *testing.T) {
	t.Parallel()
	_, err := ExpandRefs(nil, "{env:DEFINITELY_NOT_SET_VAR_12345}")
	if err == nil {
		t.Error("expected error for unresolved env var")
	}
}

func TestExpandRefs_ParamsAndEnv(t *testing.T) {
	t.Setenv("TEST_TOKEN", "secret123")
	params := map[string]string{"base_url": "https://api.example.com"}
	out, err := ExpandRefs(params, "{params.base_url}?token={env:TEST_TOKEN}")
	if err != nil {
		t.Fatal(err)
	}
	if out != "https://api.example.com?token=secret123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandMCPServers_SkipsOnUnresolvedEnv(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:DEFINITELY_NOT_SET_VAR_12345}/bin/server"},
		},
	}
	result, _ := ExpandMCPServers(servers)
	if len(result) != 0 {
		t.Errorf("expected 0 servers (skipped), got %d", len(result))
	}
}

func TestExpandMCPServers_ResolvesEnvVars(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:HOME}/bin/server"},
			Env:       map[string]string{"PATH": "{env:HOME}/bin"},
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Command[0] != "/tmp/test-home/bin/server" {
		t.Fatalf("Command[0] = %q, want /tmp/test-home/bin/server", result[0].Command[0])
	}
	if result[0].Env["PATH"] != "/tmp/test-home/bin" {
		t.Fatalf("Env[PATH] = %q, want /tmp/test-home/bin", result[0].Env["PATH"])
	}
}

func TestExpandMCPServers_SSETransport(t *testing.T) {
	t.Setenv("TEST_SSE_TOKEN", "tok123")
	servers := []domain.MCPServer{
		{
			Name:      "sse-server",
			Transport: domain.TransportSSE,
			URL:       "https://example.com/sse",
			Headers:   map[string]string{"Authorization": "Bearer {env:TEST_SSE_TOKEN}"},
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].URL != "https://example.com/sse" {
		t.Errorf("URL = %q, want https://example.com/sse", result[0].URL)
	}
	if result[0].Headers["Authorization"] != "Bearer tok123" {
		t.Errorf("Authorization header = %q, want %q", result[0].Headers["Authorization"], "Bearer tok123")
	}
}

func TestExpandMCPServers_SSETransport_SkipsOnUnresolvedHeader(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "sse-missing",
			Transport: domain.TransportSSE,
			URL:       "https://example.com/sse",
			Headers:   map[string]string{"Authorization": "Bearer {env:DEFINITELY_NOT_SET_SSE_12345}"},
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(result) != 0 {
		t.Errorf("expected 0 servers (skipped), got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}

func TestExpandMCPServers_SkipsOnUnresolvedParamRef(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "param-missing",
			Transport: domain.TransportStdio,
			Command:   []string{"run", "{params.unknown}"},
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(result) != 0 {
		t.Fatalf("expected server to be skipped, got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Field != "mcp.param-missing" {
		t.Fatalf("warning field = %q", warnings[0].Field)
	}
}

func TestExpandMCPServers_ResolvesPackRoot(t *testing.T) {
	t.Parallel()
	packRoot := filepath.Join(t.TempDir(), "packs", "my-pack")
	servers := []domain.MCPServer{
		{
			Name:      "wrapped",
			Transport: domain.TransportStdio,
			PackRoot:  packRoot,
			Command:   []string{"python3", "{pack:root}/wrappers/proxy.py", "--", "uvx", "some-mcp"},
			Env:       map[string]string{"BOOTSTRAP": "{pack:root}/wrappers/auth.sh"},
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	wantCmd := filepath.Join(packRoot, "wrappers", "proxy.py")
	if result[0].Command[1] != wantCmd {
		t.Errorf("Command[1] = %q, want %q", result[0].Command[1], wantCmd)
	}
	wantEnv := filepath.Join(packRoot, "wrappers", "auth.sh")
	if result[0].Env["BOOTSTRAP"] != wantEnv {
		t.Errorf("Env[BOOTSTRAP] = %q, want %q", result[0].Env["BOOTSTRAP"], wantEnv)
	}
}

func TestExpandMCPServers_SkipsOnUnresolvedPackRoot(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "no-pack-root",
			Transport: domain.TransportStdio,
			Command:   []string{"python3", "{pack:root}/wrappers/proxy.py"},
			// PackRoot intentionally empty
		},
	}

	result, warnings := ExpandMCPServers(servers)
	if len(result) != 0 {
		t.Fatalf("expected server to be skipped, got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}

func TestExpandMCPServers_PackRootWithParamsAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	packRoot := filepath.Join(home, ".config", "aipack", "packs", "test")
	params := map[string]string{"pypi_url": "https://pypi.example.com/simple"}
	servers := []domain.MCPServer{
		{
			Name:      "combo",
			Transport: domain.TransportStdio,
			PackRoot:  packRoot,
			Command: []string{
				"python3",
				"{pack:root}/wrappers/proxy.py",
				"--",
				"uv", "run", "--default-index", "{params.pypi_url}",
				"python", "{pack:root}/mcp-servers/api.py",
			},
			Env: map[string]string{
				"SCRIPT": "{pack:root}/wrappers/auth.sh",
				"HOME":   "{env:HOME}",
			},
		},
	}

	packs := []config.ResolvedPack{{
		Name: "test",
		MCP:  map[string]config.ResolvedMCPServer{"combo": {}},
	}}
	result, warnings := buildMCPServers(params, packs, map[string]domain.MCPServer{"combo": servers[0]})
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Command[1] != filepath.Join(packRoot, "wrappers", "proxy.py") {
		t.Errorf("Command[1] = %q", result[0].Command[1])
	}
	if result[0].Command[6] != "https://pypi.example.com/simple" {
		t.Errorf("Command[6] (params) = %q", result[0].Command[6])
	}
	if result[0].Command[8] != filepath.Join(packRoot, "mcp-servers", "api.py") {
		t.Errorf("Command[8] = %q", result[0].Command[8])
	}
	if result[0].Env["SCRIPT"] != filepath.Join(packRoot, "wrappers", "auth.sh") {
		t.Errorf("Env[SCRIPT] = %q", result[0].Env["SCRIPT"])
	}
	if result[0].Env["HOME"] != home {
		t.Errorf("Env[HOME] = %q", result[0].Env["HOME"])
	}
}

func TestNormalizeServerName(t *testing.T) {
	t.Parallel()
	tests := []struct{ input, want string }{
		{"  MyServer  ", "myserver"},
		{"already", "already"},
		{"", ""},
	}
	for _, tt := range tests {
		got := NormalizeServerName(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeServerName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
