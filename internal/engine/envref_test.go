package engine

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestExpandParams_Basic(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url":       "https://jira.example.com",
		"confluence_url": "https://wiki.example.com",
	}
	out, err := ExpandParams(params, "Visit {params.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandParams_LegacyParamSyntax(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url": "https://jira.example.com",
	}
	out, err := ExpandParams(params, "Visit {param.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandParams_LegacyGlobalSyntax(t *testing.T) {
	t.Parallel()
	params := map[string]string{
		"jira_url": "https://jira.example.com",
	}
	out, err := ExpandParams(params, "Visit {global.jira_url}/browse/PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Visit https://jira.example.com/browse/PROJ-123" {
		t.Errorf("out = %q", out)
	}
}

func TestExpandParams_Unresolved(t *testing.T) {
	t.Parallel()
	params := map[string]string{}
	_, err := ExpandParams(params, "Has {params.unknown_field}")
	if err == nil {
		t.Error("expected error for unresolved param")
	}
}

func TestTransformEnvRefs_Cline(t *testing.T) {
	t.Parallel()
	input := "use {env:HOME}/path"
	got := transformEnvRefs(input, "cline")
	want := "use ${HOME}/path"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTransformEnvRefs_Shell(t *testing.T) {
	t.Parallel()
	input := "use {env:HOME}/path"
	got := transformEnvRefs(input, "shell")
	want := "use $HOME/path"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTransformEnvRefs_ClineMalformed(t *testing.T) {
	t.Parallel()
	// Malformed input without closing brace should be returned as-is.
	input := "use {env:HOME and no close"
	got := transformEnvRefs(input, "cline")
	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestTransformEnvRefs_ClineMultiple(t *testing.T) {
	t.Parallel()
	input := "{env:HOME}/bin/{env:USER}"
	got := transformEnvRefs(input, "cline")
	want := "${HOME}/bin/${USER}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandMCPForRender_TransformFormat(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:HOME}/bin/server"},
			Env:       map[string]string{"PATH": "{env:PATH}"},
		},
	}
	result, _ := ExpandMCPForRender(servers, false, "cline")
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Command[0] != "${HOME}/bin/server" {
		t.Errorf("Command[0] = %q, want ${HOME}/bin/server", result[0].Command[0])
	}
	if result[0].Env["PATH"] != "${PATH}" {
		t.Errorf("Env[PATH] = %q, want ${PATH}", result[0].Env["PATH"])
	}
}

func TestExpandMCPForRender_ShellFormat(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:HOME}/bin/server"},
		},
	}
	result, _ := ExpandMCPForRender(servers, false, "shell")
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Command[0] != "$HOME/bin/server" {
		t.Errorf("Command[0] = %q, want $HOME/bin/server", result[0].Command[0])
	}
}

func TestExpandMCPForRender_SkipsOnUnresolvedEnv(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:DEFINITELY_NOT_SET_VAR_12345}/bin/server"},
		},
	}
	result, _ := ExpandMCPForRender(servers, true, "")
	if len(result) != 0 {
		t.Errorf("expected 0 servers (skipped), got %d", len(result))
	}
}

func TestExpandMCPForRenderBestEffort_ResolvesAvailableAndPreservesMissing(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	servers := []domain.MCPServer{
		{
			Name:      "test",
			Transport: domain.TransportStdio,
			Command:   []string{"{env:HOME}/bin/server", "{env:MISSING_VAR}/fallback"},
			Env:       map[string]string{"TOKEN": "{env:MISSING_VAR}", "PATH": "{env:HOME}/bin"},
		},
	}

	result, warnings := ExpandMCPForRenderBestEffort(servers)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Command[0] != "/tmp/test-home/bin/server" {
		t.Fatalf("Command[0] = %q, want /tmp/test-home/bin/server", result[0].Command[0])
	}
	if result[0].Command[1] != "{env:MISSING_VAR}/fallback" {
		t.Fatalf("Command[1] = %q, want placeholder preserved", result[0].Command[1])
	}
	if result[0].Env["TOKEN"] != "{env:MISSING_VAR}" {
		t.Fatalf("Env[TOKEN] = %q, want placeholder preserved", result[0].Env["TOKEN"])
	}
	if result[0].Env["PATH"] != "/tmp/test-home/bin" {
		t.Fatalf("Env[PATH] = %q, want /tmp/test-home/bin", result[0].Env["PATH"])
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
