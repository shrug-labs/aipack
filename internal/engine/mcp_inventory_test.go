package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadMCPInventoryDir_IgnoresMarkdownFiles(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "foo.json"), `{
		"name": "foo",
		"transport": "stdio",
		"timeout": 123,
		"command": ["foo", "--bar"],
		"env": {"A": "B"},
		"available_tools": ["x"],
		"links": ["https://example.invalid"],
		"auth": "token",
		"notes": "doc only"
	}`)
	writeTestFile(t, filepath.Join(dir, "foo.md"), "# doc\ninitial\n")

	inv1, err := loadMCPInventoryDir(dir)
	if err != nil {
		t.Fatalf("loadMCPInventoryDir: %v", err)
	}
	if len(inv1) != 1 {
		t.Fatalf("expected 1 server, got %d", len(inv1))
	}
	if _, ok := inv1["foo"]; !ok {
		t.Fatalf("expected normalized key foo, got keys: %v", reflect.ValueOf(inv1).MapKeys())
	}

	writeTestFile(t, filepath.Join(dir, "foo.md"), "# doc\nchanged\n")
	inv2, err := loadMCPInventoryDir(dir)
	if err != nil {
		t.Fatalf("loadMCPInventoryDir after md change: %v", err)
	}
	if !reflect.DeepEqual(inv1, inv2) {
		t.Fatalf("expected inventory unchanged by markdown edits\ninv1=%#v\ninv2=%#v", inv1, inv2)
	}
}

func TestLoadMCPInventoryDir_AcceptsDocMetadataAndUnknownFields(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "foo.json"), `{
		"name": "foo",
		"transport": "stdio",
		"command": ["foo"],
		"available_tools": [],
		"links": ["https://a.invalid", "https://b.invalid"],
		"auth": "oauth",
		"notes": "hello",
		"unknown_extra_field": {"k": "v"}
	}`)

	inv, err := loadMCPInventoryDir(dir)
	if err != nil {
		t.Fatalf("loadMCPInventoryDir: %v", err)
	}
	s := inv["foo"]
	if got, want := s.Auth, "oauth"; got != want {
		t.Fatalf("auth: got %q want %q", got, want)
	}
	if got, want := s.Notes, "hello"; got != want {
		t.Fatalf("notes: got %q want %q", got, want)
	}
	if got, want := len(s.Links), 2; got != want {
		t.Fatalf("links length: got %d want %d", got, want)
	}
}

func TestLoadMCPInventoryForPacks_FiltersToPackMCPMap(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mcp"), 0o755); err != nil {
		t.Fatalf("mkdir mcp: %v", err)
	}

	writeTestFile(t, filepath.Join(root, "mcp", "foo.json"), `{
		"name": "foo",
		"transport": "stdio",
		"command": ["foo"],
		"available_tools": []
	}`)
	writeTestFile(t, filepath.Join(root, "mcp", "bar.json"), `{
		"name": "bar",
		"transport": "stdio",
		"command": ["bar"],
		"available_tools": []
	}`)

	packs := []config.ResolvedPack{
		{
			Name: "p1",
			Root: root,
			MCP: map[string]config.ResolvedMCPServer{
				"foo": {},
			},
		},
	}

	inv, err := LoadMCPInventoryForPacks(packs)
	if err != nil {
		t.Fatalf("LoadMCPInventoryForPacks: %v", err)
	}
	if len(inv) != 1 {
		t.Fatalf("expected 1 server, got %d", len(inv))
	}
	if _, ok := inv["foo"]; !ok {
		t.Fatalf("expected foo, got keys: %v", reflect.ValueOf(inv).MapKeys())
	}
	if _, ok := inv["bar"]; ok {
		t.Fatalf("expected bar to be filtered out")
	}
}

func TestLoadMCPInventoryForPacks_DetectsDuplicateInventoryAcrossPacks(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root1, "mcp"), 0o755); err != nil {
		t.Fatalf("mkdir mcp root1: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "mcp"), 0o755); err != nil {
		t.Fatalf("mkdir mcp root2: %v", err)
	}

	writeTestFile(t, filepath.Join(root1, "mcp", "foo.json"), `{
		"name": "foo",
		"transport": "stdio",
		"command": ["foo"],
		"available_tools": []
	}`)
	writeTestFile(t, filepath.Join(root2, "mcp", "foo.json"), `{
		"name": "foo",
		"transport": "stdio",
		"command": ["foo"],
		"available_tools": []
	}`)

	packs := []config.ResolvedPack{
		{
			Name: "p1",
			Root: root1,
			MCP: map[string]config.ResolvedMCPServer{
				"foo": {},
			},
		},
		{
			Name: "p2",
			Root: root2,
			MCP: map[string]config.ResolvedMCPServer{
				"foo": {},
			},
		},
	}

	_, err := LoadMCPInventoryForPacks(packs)
	if err == nil {
		t.Fatalf("expected error")
	}
	if got, want := err.Error(), "duplicate MCP server inventory for foo"; got != want {
		t.Fatalf("error: got %q want %q", got, want)
	}
}

func TestBuildMCPServers_WarnsOnMissingInventory(t *testing.T) {
	t.Parallel()
	packs := []config.ResolvedPack{
		{
			Name: "p1",
			MCP: map[string]config.ResolvedMCPServer{
				"missing-server": {},
			},
		},
	}
	inventory := map[string]domain.MCPServer{} // empty inventory

	servers, warnings := buildMCPServers(nil, packs, inventory)
	if len(servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(servers))
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for missing inventory, got none")
	}
	found := false
	for _, w := range warnings {
		if w.Field == "mcp.missing-server" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning with field mcp.missing-server, got %v", warnings)
	}
}

func TestBuildMCPServers_ExpandsParams(t *testing.T) {
	t.Parallel()
	params := map[string]string{"base_url": "https://example.com"}
	packs := []config.ResolvedPack{
		{
			Name: "p1",
			MCP: map[string]config.ResolvedMCPServer{
				"my-server": {},
			},
		},
	}
	inventory := map[string]domain.MCPServer{
		"my-server": {
			Name:      "my-server",
			Transport: domain.TransportSSE,
			URL:       "{params.base_url}/api",
		},
	}

	servers, warnings := buildMCPServers(params, packs, inventory)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].URL != "https://example.com/api" {
		t.Errorf("URL = %q, want https://example.com/api", servers[0].URL)
	}
}

func TestLoadMCPInventoryDir_LoadsSSEServer(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "remote.json"), `{
		"name": "remote",
		"transport": "sse",
		"url": "https://example.com/sse",
		"headers": {"Authorization": "Bearer {env:TOKEN}"},
		"available_tools": ["search"]
	}`)

	inv, err := loadMCPInventoryDir(dir)
	if err != nil {
		t.Fatalf("loadMCPInventoryDir: %v", err)
	}
	if len(inv) != 1 {
		t.Fatalf("expected 1 server, got %d", len(inv))
	}
	s := inv["remote"]
	if s.Transport != domain.TransportSSE {
		t.Fatalf("transport: got %q want %q", s.Transport, domain.TransportSSE)
	}
	if s.URL != "https://example.com/sse" {
		t.Fatalf("url: got %q", s.URL)
	}
	if s.Headers["Authorization"] != "Bearer {env:TOKEN}" {
		t.Fatalf("headers: got %v", s.Headers)
	}
}
