package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePackInventory_AllowsEmptyVectorsWithoutContentDirs(t *testing.T) {
	t.Parallel()
	packRoot := t.TempDir()
	manifest := PackManifest{
		SchemaVersion: 1,
		Name:          "demo",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}

	if err := validatePackInventory("demo", packRoot, manifest); err != nil {
		t.Fatalf("validatePackInventory: %v", err)
	}
}

func TestValidatePackInventory_RequiresManifestReferencedFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		manifest PackManifest
		setup    func(t *testing.T, root string)
		wantErr  string
	}{
		{
			name: "missing rule file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				Rules:         []string{"triage"},
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
			},
			wantErr: `pack "demo" rules "triage" missing`,
		},
		{
			name: "missing skill entry file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				Skills:        []string{"triage"},
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
			},
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "skills", "triage"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: `pack "demo" skills "triage" missing`,
		},
		{
			name: "missing prompt file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				Prompts:       []string{"review"},
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
			},
			wantErr: `pack "demo" prompts "review" missing`,
		},
		{
			name: "missing mcp file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP: MCPPack{Servers: map[string]MCPDefaults{
					"atlassian": {},
				}},
			},
			wantErr: `pack "demo" mcp server "atlassian" missing`,
		},
		{
			name: "missing harness settings file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
				Configs: PackConfigs{HarnessSettings: map[string][]string{
					"claudecode": {"settings.local.json"},
				}},
			},
			wantErr: `pack "demo" configs.harness_settings[claudecode] missing "settings.local.json"`,
		},
		{
			name: "missing harness plugins file",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
				Configs: PackConfigs{HarnessPlugins: map[string][]string{
					"opencode": {"plugin.json"},
				}},
			},
			wantErr: `pack "demo" configs.harness_plugins[opencode] missing "plugin.json"`,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			packRoot := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, packRoot)
			}
			err := validatePackInventory("demo", packRoot, tc.manifest)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidatePackInventory_RejectsBadManifestEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		manifest PackManifest
		wantErr  string
	}{
		{
			name: "duplicate rules id",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				Rules:         []string{"triage", "triage"},
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
			},
			wantErr: `pack "demo" rules contains duplicate id "triage"`,
		},
		{
			name: "empty workflow id",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				Workflows:     []string{"  "},
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
			},
			wantErr: `pack "demo" workflows contains empty id`,
		},
		{
			name: "empty harness key",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
				Configs: PackConfigs{HarnessSettings: map[string][]string{
					"   ": {"settings.local.json"},
				}},
			},
			wantErr: `pack "demo" configs.harness_settings contains empty harness key`,
		},
		{
			name: "empty config filename",
			manifest: PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
				Configs: PackConfigs{HarnessPlugins: map[string][]string{
					"opencode": {"   "},
				}},
			},
			wantErr: `pack "demo" configs.harness_plugins[opencode] contains empty filename`,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validatePackInventory("demo", t.TempDir(), tc.manifest)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidatePackInventory_MCPServerNameMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		key      string // manifest key in mcp.servers
		jsonName string // "name" field inside the .json file
		wantErr  string
	}{
		{
			name:     "name mismatch",
			key:      "oci-mcp",
			jsonName: "oci_mcp",
			wantErr:  `name field is "oci_mcp" in oci-mcp.json (must match manifest key)`,
		},
		{
			name:     "empty name field",
			key:      "atlassian",
			jsonName: "",
			wantErr:  `missing "name" field in atlassian.json`,
		},
		{
			name:     "matching name passes",
			key:      "dope",
			jsonName: "dope",
			wantErr:  "",
		},
		{
			name:     "case-insensitive match passes",
			key:      "Dope",
			jsonName: "dope",
			wantErr:  "",
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			packRoot := t.TempDir()
			mcpDir := filepath.Join(packRoot, "mcp")
			if err := os.MkdirAll(mcpDir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := []byte(`{"name":"` + tc.jsonName + `","command":["echo"]}`)
			if err := os.WriteFile(filepath.Join(mcpDir, tc.key+".json"), content, 0o600); err != nil {
				t.Fatal(err)
			}

			manifest := PackManifest{
				SchemaVersion: 1,
				Name:          "demo",
				Root:          ".",
				MCP: MCPPack{Servers: map[string]MCPDefaults{
					tc.key: {},
				}},
			}
			err := validatePackInventory("demo", packRoot, manifest)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestResolvePackRoot(t *testing.T) {
	t.Parallel()
	manifestPath := filepath.Join(string(filepath.Separator), "tmp", "packs", "demo", "pack.json")
	abs := filepath.Join(string(filepath.Separator), "opt", "packs", "demo")

	if got := ResolvePackRoot(manifestPath, ""); got != "" {
		t.Fatalf("ResolvePackRoot(empty) = %q, want empty", got)
	}
	if got := ResolvePackRoot(manifestPath, abs); got != abs {
		t.Fatalf("ResolvePackRoot(abs) = %q, want %q", got, abs)
	}
	wantRel := filepath.Join(string(filepath.Separator), "tmp", "packs", "demo", "content")
	if got := ResolvePackRoot(manifestPath, "content"); got != wantRel {
		t.Fatalf("ResolvePackRoot(rel) = %q, want %q", got, wantRel)
	}
}
