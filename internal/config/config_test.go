package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveProfile_AllowsEmptyInclude(t *testing.T) {
	root := t.TempDir()
	// Install pack at configDir/packs/test/
	packRoot := filepath.Join(root, "packs", "test")
	if err := os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "rules", "base.md"), []byte("---\nname: base\n---\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	manifest := PackManifest{
		SchemaVersion: 1,
		Name:          "test",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"base"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o600); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	empty := []string{}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "test",
			Enabled: BoolPtr(true),
			Rules:   VectorSelector{Include: &empty},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	r, err := ResolveProfile(cfg, profilePath, root, CollisionError, nil)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(r.Packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(r.Packs))
	}
	// include: [] is treated as "no filter" (include all), same as include: null.
	// An empty allowlist is almost always a mistake from clearing the list.
	if len(r.Packs[0].Rules) != 1 {
		t.Fatalf("expected 1 rule (empty include = include all), got %d", len(r.Packs[0].Rules))
	}
}

// --- Test helper types and functions ---

// testPackSpec describes a pack to install into a test configDir.
type testPackSpec struct {
	name       string
	rules      []string // rule IDs — creates rules/<id>.md
	agents     []string // agent IDs — creates agents/<id>.md
	workflows  []string // workflow IDs — creates workflows/<id>.md
	skills     []string // skill IDs — creates skills/<id>/SKILL.md
	mcpServers []string // MCP server names — creates mcp/<name>.json and adds to manifest
}

// installTestPack creates a pack directory structure under configDir/packs/<name>/
// with the specified content. The manifest leaves authored content fields nil so
// DiscoverContent fills them from the filesystem.
func installTestPack(t *testing.T, configDir string, spec testPackSpec) {
	t.Helper()
	packDir := filepath.Join(configDir, "packs", spec.name)

	for _, id := range spec.rules {
		dir := filepath.Join(packDir, "rules")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir rules: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("---\nname: "+id+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write rule %s: %v", id, err)
		}
	}
	for _, id := range spec.agents {
		dir := filepath.Join(packDir, "agents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir agents: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("---\nname: "+id+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write agent %s: %v", id, err)
		}
	}
	for _, id := range spec.workflows {
		dir := filepath.Join(packDir, "workflows")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir workflows: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("---\nname: "+id+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write workflow %s: %v", id, err)
		}
	}
	for _, id := range spec.skills {
		dir := filepath.Join(packDir, "skills", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill %s: %v", id, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+id+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write skill %s: %v", id, err)
		}
	}

	// Build MCP manifest entries and create server JSON files.
	mcpPack := MCPPack{Servers: map[string]MCPDefaults{}}
	for _, name := range spec.mcpServers {
		mcpPack.Servers[name] = MCPDefaults{}
		dir := filepath.Join(packDir, "mcp")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir mcp: %v", err)
		}
		serverJSON, _ := json.Marshal(map[string]any{
			"name":      name,
			"transport": "stdio",
			"command":   []string{"echo"},
		})
		if err := os.WriteFile(filepath.Join(dir, name+".json"), serverJSON, 0o644); err != nil {
			t.Fatalf("write mcp %s: %v", name, err)
		}
	}

	// Ensure the pack directory itself exists (even if no content dirs were created).
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir packDir: %v", err)
	}

	// Write pack.json. Content fields are left nil (zero value) so DiscoverContent
	// populates them from the filesystem.
	manifest := PackManifest{
		SchemaVersion: 1,
		Name:          spec.name,
		Version:       "1.0.0",
		Root:          ".",
		MCP:           mcpPack,
	}
	if err := SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatalf("save manifest for %s: %v", spec.name, err)
	}
}

func strSlice(items ...string) []string { return items }

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

func TestResolveProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, configDir string)
		cfg     ProfileConfig
		wantErr string
		check   func(t *testing.T, packs []ResolvedPack, settingsPacks []string)
	}{
		{
			name: "single pack all content",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:      "full",
					rules:     strSlice("alpha", "beta"),
					agents:    strSlice("reviewer"),
					workflows: strSlice("deploy"),
					skills:    strSlice("oncall"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs:         []PackEntry{{Name: "full"}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if len(packs) != 1 {
					t.Fatalf("expected 1 pack, got %d", len(packs))
				}
				p := packs[0]
				if got := sortedCopy(p.Rules); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
					t.Errorf("rules = %v, want [alpha beta]", got)
				}
				if got := sortedCopy(p.Agents); len(got) != 1 || got[0] != "reviewer" {
					t.Errorf("agents = %v, want [reviewer]", got)
				}
				if got := sortedCopy(p.Workflows); len(got) != 1 || got[0] != "deploy" {
					t.Errorf("workflows = %v, want [deploy]", got)
				}
				if got := sortedCopy(p.Skills); len(got) != 1 || got[0] != "oncall" {
					t.Errorf("skills = %v, want [oncall]", got)
				}
			},
		},
		{
			name: "include selector filters rules",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "filtered",
					rules: strSlice("alpha", "beta", "gamma"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name:  "filtered",
					Rules: VectorSelector{Include: &[]string{"alpha", "gamma"}},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				got := sortedCopy(packs[0].Rules)
				if len(got) != 2 || got[0] != "alpha" || got[1] != "gamma" {
					t.Errorf("rules = %v, want [alpha gamma]", got)
				}
			},
		},
		{
			name: "exclude selector removes rules",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "excluded",
					rules: strSlice("alpha", "beta", "gamma"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name:  "excluded",
					Rules: VectorSelector{Exclude: &[]string{"beta"}},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				got := sortedCopy(packs[0].Rules)
				if len(got) != 2 || got[0] != "alpha" || got[1] != "gamma" {
					t.Errorf("rules = %v, want [alpha gamma]", got)
				}
			},
		},
		{
			name: "quiet pack includes nothing by default",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "quiet-lib",
					rules: strSlice("r1", "r2"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name:  "quiet-lib",
					Quiet: true,
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if len(packs[0].Rules) != 0 {
					t.Errorf("quiet pack should resolve zero rules, got %v", packs[0].Rules)
				}
			},
		},
		{
			name: "quiet pack with explicit include",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "quiet-selective",
					rules: strSlice("r1", "r2", "r3"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name:  "quiet-selective",
					Quiet: true,
					Rules: VectorSelector{Include: &[]string{"r2"}},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				got := sortedCopy(packs[0].Rules)
				if len(got) != 1 || got[0] != "r2" {
					t.Errorf("rules = %v, want [r2]", got)
				}
			},
		},
		{
			name: "disabled pack excluded",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "active",
					rules: strSlice("kept"),
				})
				installTestPack(t, configDir, testPackSpec{
					name:  "inactive",
					rules: strSlice("dropped"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{
					{Name: "active"},
					{Name: "inactive", Enabled: BoolPtr(false)},
				},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if len(packs) != 1 {
					t.Fatalf("expected 1 enabled pack, got %d", len(packs))
				}
				if packs[0].Name != "active" {
					t.Errorf("expected pack 'active', got %q", packs[0].Name)
				}
			},
		},
		{
			name: "collision without override errors",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "pack-a",
					rules: strSlice("shared"),
				})
				installTestPack(t, configDir, testPackSpec{
					name:  "pack-b",
					rules: strSlice("shared"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{
					{Name: "pack-a"},
					{Name: "pack-b"},
				},
			},
			wantErr: "collision(s)",
		},
		{
			name: "collision with override resolves to winner",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "base-pack",
					rules: strSlice("shared", "base-only"),
				})
				installTestPack(t, configDir, testPackSpec{
					name:  "override-pack",
					rules: strSlice("shared", "override-only"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{
					{Name: "base-pack"},
					{Name: "override-pack", Overrides: Overrides{Rules: []string{"shared"}}},
				},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if len(packs) != 2 {
					t.Fatalf("expected 2 packs, got %d", len(packs))
				}
				// base-pack should have "shared" stripped.
				baseRules := sortedCopy(packs[0].Rules)
				if len(baseRules) != 1 || baseRules[0] != "base-only" {
					t.Errorf("base-pack rules = %v, want [base-only]", baseRules)
				}
				// override-pack should own "shared".
				overrideRules := sortedCopy(packs[1].Rules)
				if len(overrideRules) != 2 || overrideRules[0] != "override-only" || overrideRules[1] != "shared" {
					t.Errorf("override-pack rules = %v, want [override-only shared]", overrideRules)
				}
			},
		},
		{
			name: "MCP servers included by default",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:       "mcp-default",
					mcpServers: strSlice("my-server"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs:         []PackEntry{{Name: "mcp-default"}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if _, ok := packs[0].MCP["my-server"]; !ok {
					t.Errorf("MCP server 'my-server' should be present by default, got %v", packs[0].MCP)
				}
			},
		},
		{
			name: "MCP server disabled",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:       "mcp-off",
					mcpServers: strSlice("disabled-server"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name: "mcp-off",
					MCP: map[string]MCPServerConfig{
						"disabled-server": {Enabled: BoolPtr(false)},
					},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				if _, ok := packs[0].MCP["disabled-server"]; ok {
					t.Errorf("disabled MCP server should be absent, got %v", packs[0].MCP)
				}
			},
		},
		{
			name: "MCP server with custom tools",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:       "mcp-tools",
					mcpServers: strSlice("tooled-server"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name: "mcp-tools",
					MCP: map[string]MCPServerConfig{
						"tooled-server": {
							Enabled:      BoolPtr(true),
							AllowedTools: []string{"tool-a", "tool-b"},
						},
					},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				srv, ok := packs[0].MCP["tooled-server"]
				if !ok {
					t.Fatalf("MCP server 'tooled-server' missing")
				}
				got := sortedCopy(srv.AllowedTools)
				if len(got) != 2 || got[0] != "tool-a" || got[1] != "tool-b" {
					t.Errorf("allowed_tools = %v, want [tool-a tool-b]", got)
				}
			},
		},
		{
			name: "glob include selector",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "glob-pack",
					rules: strSlice("ops-triage", "ops-escalation", "dev-testing"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name:  "glob-pack",
					Rules: VectorSelector{Include: &[]string{"ops-*"}},
				}},
			},
			check: func(t *testing.T, packs []ResolvedPack, _ []string) {
				got := sortedCopy(packs[0].Rules)
				if len(got) != 2 || got[0] != "ops-escalation" || got[1] != "ops-triage" {
					t.Errorf("rules = %v, want [ops-escalation ops-triage]", got)
				}
			},
		},
		{
			name: "both include and exclude errors",
			setup: func(t *testing.T, configDir string) {
				installTestPack(t, configDir, testPackSpec{
					name:  "both-pack",
					rules: strSlice("r1"),
				})
			},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs: []PackEntry{{
					Name: "both-pack",
					Rules: VectorSelector{
						Include: &[]string{"r1"},
						Exclude: &[]string{"r1"},
					},
				}},
			},
			wantErr: "cannot set both include and exclude",
		},
		{
			name:  "empty packs list errors",
			setup: func(t *testing.T, configDir string) {},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs:         []PackEntry{},
			},
			wantErr: "profile packs must be configured",
		},
		{
			name:  "missing pack errors",
			setup: func(t *testing.T, configDir string) {},
			cfg: ProfileConfig{
				SchemaVersion: ProfileSchemaVersion,
				Packs:         []PackEntry{{Name: "nonexistent"}},
			},
			wantErr: "manifest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			tt.setup(t, configDir)

			profilePath := filepath.Join(configDir, "profile.yaml")
			r, err := ResolveProfile(tt.cfg, profilePath, configDir, CollisionError, nil)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, r.Packs, r.SettingsPacks)
		})
	}
}
