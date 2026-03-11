package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestRenderMCPBytesFromTyped_Basic(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "foo",
			Transport: domain.TransportStdio,
			Command:   []string{"echo", "hi"},
			Env:       map[string]string{"KEY": "val"},
		},
	}

	out, _, err := RenderMCPBytesFromTyped(servers, false)
	if err != nil {
		t.Fatalf("RenderMCPBytesFromTyped: %v", err)
	}

	var got mcpRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entry, ok := got.MCPServers["foo"]
	if !ok {
		t.Fatal("missing server 'foo'")
	}
	if entry.Command != "echo" {
		t.Errorf("command: got %q want %q", entry.Command, "echo")
	}
	if len(entry.Args) != 1 || entry.Args[0] != "hi" {
		t.Errorf("args: got %v want [hi]", entry.Args)
	}
	if entry.Env["KEY"] != "val" {
		t.Errorf("env KEY: got %q want %q", entry.Env["KEY"], "val")
	}
}

func TestRenderMCPBytesFromTyped_OmitsEmptyEnv(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:    "clean",
			Command: []string{"cmd"},
			Env:     map[string]string{},
		},
	}

	out, _, err := RenderMCPBytesFromTyped(servers, false)
	if err != nil {
		t.Fatalf("RenderMCPBytesFromTyped: %v", err)
	}

	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	var serverRaw map[string]json.RawMessage
	if err := json.Unmarshal(root.MCPServers["clean"], &serverRaw); err != nil {
		t.Fatalf("unmarshal server: %v", err)
	}
	if _, ok := serverRaw["env"]; ok {
		t.Error("expected 'env' to be omitted when empty")
	}
}

func TestRenderMCPBytesFromTyped_EnvRefTransform(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:    "bar",
			Command: []string{"{env:MY_CMD}"},
			Env:     map[string]string{},
		},
	}

	out, _, err := RenderMCPBytesFromTyped(servers, false)
	if err != nil {
		t.Fatalf("RenderMCPBytesFromTyped: %v", err)
	}

	var got mcpRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// {env:MY_CMD} → ${MY_CMD} (brace format for Claude Code).
	if got.MCPServers["bar"].Command != "${MY_CMD}" {
		t.Errorf("env transform: got %q want %q", got.MCPServers["bar"].Command, "${MY_CMD}")
	}
}

func TestRenderPermissions_AllowedTools(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "atlassian", AllowedTools: []string{"jira_get_issue", "confluence_search"}},
		{Name: "bitbucket", AllowedTools: []string{"list_repositories"}},
	}

	perms := RenderPermissions(servers)

	want := []string{
		"mcp__atlassian__confluence_search",
		"mcp__atlassian__jira_get_issue",
		"mcp__bitbucket__list_repositories",
	}
	if len(perms) != len(want) {
		t.Fatalf("permissions: got %v want %v", perms, want)
	}
	for i, p := range perms {
		if p != want[i] {
			t.Errorf("permissions[%d]: got %q want %q", i, p, want[i])
		}
	}
}

func TestRenderDenyPermissions_DisabledTools(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "atlassian", DisabledTools: []string{"jira_delete_issue", "confluence_delete_page"}},
		{Name: "bitbucket", DisabledTools: []string{"merge_pull_request"}},
	}

	perms := RenderDenyPermissions(servers)

	want := []string{
		"mcp__atlassian__confluence_delete_page",
		"mcp__atlassian__jira_delete_issue",
		"mcp__bitbucket__merge_pull_request",
	}
	if len(perms) != len(want) {
		t.Fatalf("deny permissions: got %v want %v", perms, want)
	}
	for i, p := range perms {
		if p != want[i] {
			t.Errorf("deny permissions[%d]: got %q want %q", i, p, want[i])
		}
	}
}

func TestRenderDenyPermissions_EmptyDisabledTools(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "foo", AllowedTools: []string{"bar"}},
	}

	perms := RenderDenyPermissions(servers)
	if len(perms) != 0 {
		t.Errorf("expected no deny permissions, got %v", perms)
	}
}

func TestRenderSettingsBytes_AllowOnly(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "svc", AllowedTools: []string{"tool1"}},
	}

	out, err := RenderSettingsBytes(servers)
	if err != nil {
		t.Fatalf("RenderSettingsBytes: %v", err)
	}

	var got settingsRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Permissions.Allow) != 1 || got.Permissions.Allow[0] != "mcp__svc__tool1" {
		t.Errorf("allow: got %v want [mcp__svc__tool1]", got.Permissions.Allow)
	}
}

func TestRenderSettingsBytes_AllowAndDeny(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:          "foo",
			AllowedTools:  []string{"bar"},
			DisabledTools: []string{"baz"},
		},
	}

	out, err := RenderSettingsBytes(servers)
	if err != nil {
		t.Fatalf("RenderSettingsBytes: %v", err)
	}

	var got settingsRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Permissions.Allow) != 1 || got.Permissions.Allow[0] != "mcp__foo__bar" {
		t.Errorf("allow: got %v", got.Permissions.Allow)
	}
	if len(got.Permissions.Deny) != 1 || got.Permissions.Deny[0] != "mcp__foo__baz" {
		t.Errorf("deny: got %v", got.Permissions.Deny)
	}
}

func TestRenderSettingsBytes_NoDenyOmitsDenyKey(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "svc", AllowedTools: []string{"tool1"}},
	}

	out, err := RenderSettingsBytes(servers)
	if err != nil {
		t.Fatalf("RenderSettingsBytes: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	var permsRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["permissions"], &permsRaw); err != nil {
		t.Fatalf("unmarshal permissions: %v", err)
	}
	if _, ok := permsRaw["deny"]; ok {
		t.Error("expected 'deny' key to be omitted when no deny entries")
	}
}

func TestStripManagedPermissions_AllowOnly(t *testing.T) {
	t.Parallel()
	input := []byte(`{
  "permissions": {
    "allow": [
      "Bash(go test:*)",
      "mcp__foo__bar",
      "WebSearch",
      "mcp__baz__qux"
    ]
  }
}`)

	out, err := StripManagedPermissions(input)
	if err != nil {
		t.Fatalf("StripManagedPermissions: %v", err)
	}

	var got settingsRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := []string{"Bash(go test:*)", "WebSearch"}
	if len(got.Permissions.Allow) != len(want) {
		t.Fatalf("allow: got %v want %v", got.Permissions.Allow, want)
	}
	for i, p := range got.Permissions.Allow {
		if p != want[i] {
			t.Errorf("allow[%d]: got %q want %q", i, p, want[i])
		}
	}
}

func TestStripManagedPermissions_StripsDenyEntries(t *testing.T) {
	t.Parallel()
	input := []byte(`{
  "permissions": {
    "allow": [
      "Bash(go test:*)",
      "mcp__foo__bar"
    ],
    "deny": [
      "Bash(rm -rf:*)",
      "mcp__foo__baz",
      "mcp__other__qux"
    ]
  }
}`)

	out, err := StripManagedPermissions(input)
	if err != nil {
		t.Fatalf("StripManagedPermissions: %v", err)
	}

	var got settingsRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Permissions.Allow) != 1 || got.Permissions.Allow[0] != "Bash(go test:*)" {
		t.Fatalf("allow: got %v want [Bash(go test:*)]", got.Permissions.Allow)
	}
	if len(got.Permissions.Deny) != 1 || got.Permissions.Deny[0] != "Bash(rm -rf:*)" {
		t.Fatalf("deny: got %v want [Bash(rm -rf:*)]", got.Permissions.Deny)
	}
}

func TestRenderMCPBytesFromTyped_SSEServer(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:      "remote",
			Transport: domain.TransportSSE,
			URL:       "https://example.com/sse",
			Headers:   map[string]string{"Authorization": "Bearer tok"},
		},
	}

	out, _, err := RenderMCPBytesFromTyped(servers, false)
	if err != nil {
		t.Fatalf("RenderMCPBytesFromTyped: %v", err)
	}

	var got mcpRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entry, ok := got.MCPServers["remote"]
	if !ok {
		t.Fatal("missing server 'remote'")
	}
	if entry.Type != domain.TransportSSE {
		t.Errorf("type: got %q want %q", entry.Type, domain.TransportSSE)
	}
	if entry.URL != "https://example.com/sse" {
		t.Errorf("url: got %q", entry.URL)
	}
	if entry.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("headers: got %v", entry.Headers)
	}
	if entry.Command != "" {
		t.Errorf("command should be empty for SSE: got %q", entry.Command)
	}
}

func TestRenderMCPBytesFromTyped_MixedTransports(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "local", Command: []string{"node", "srv.js"}, Env: map[string]string{}},
		{Name: "remote", Transport: domain.TransportStreamableHTTP, URL: "https://example.com/mcp"},
	}

	out, _, err := RenderMCPBytesFromTyped(servers, false)
	if err != nil {
		t.Fatalf("RenderMCPBytesFromTyped: %v", err)
	}

	var got mcpRoot
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(got.MCPServers))
	}
	if got.MCPServers["local"].Command != "node" {
		t.Errorf("stdio server command: got %q", got.MCPServers["local"].Command)
	}
	if got.MCPServers["remote"].URL != "https://example.com/mcp" {
		t.Errorf("streamable-http server url: got %q", got.MCPServers["remote"].URL)
	}
}
