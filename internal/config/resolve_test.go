package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProfile_InstalledPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Set up installed pack at configDir/packs/snap/pack.json
	packRoot := filepath.Join(root, "packs", "snap")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatalf("mkdir pack root: %v", err)
	}
	manifest := PackManifest{
		SchemaVersion: 1,
		Name:          "snap",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o600); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	profilePath := filepath.Join(root, "profiles", "test.yaml")
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "snap", Enabled: BoolPtr(true)}},
	}

	packs, _, err := ResolveProfile(cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if packs[0].Root != packRoot {
		t.Fatalf("resolved pack root = %q, want %q", packs[0].Root, packRoot)
	}
}

func TestResolveProfile_PackNotInstalled_Error(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "nonexistent"}},
	}
	_, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
	if err == nil {
		t.Fatal("expected error for missing pack, got nil")
	}
}

func TestResolveProfile_VectorSelectorErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 1,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/alpha.md": "---\nname: alpha\n---\nbody\n"})

	include := []string{"alpha"}
	exclude := []string{"alpha"}
	_, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Include: &include, Exclude: &exclude},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules cannot set both include and exclude`) {
		t.Fatalf("expected include/exclude error, got %v", err)
	}

	unknown := []string{"missing"}
	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Include: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules include references unknown id "missing"`) {
		t.Fatalf("expected unknown include error, got %v", err)
	}

	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Exclude: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules exclude references unknown id "missing"`) {
		t.Fatalf("expected unknown exclude error, got %v", err)
	}
}

func TestResolveProfile_OverrideAndDuplicateErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 1,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"jira": {}}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 1,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"jira": {}}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})

	_, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second"}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `rules id "shared" appears in both "first" and "second"`) {
		t.Fatalf("expected duplicate rules error, got %v", err)
	}

	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second", Overrides: Overrides{Rules: []string{"missing"}}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "second" overrides.rules references unknown id "missing"`) {
		t.Fatalf("expected unknown override error, got %v", err)
	}
}

func TestResolveProfile_AllowsDeclaredOverrideAndSettingsPackValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 1,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 1,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})

	packs, settingsPack, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second", Overrides: Overrides{Rules: []string{"shared"}}, Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("packs = %d, want 2", len(packs))
	}
	if settingsPack != "second" {
		t.Fatalf("settingsPack = %q, want %q", settingsPack, "second")
	}

	installPackForResolveTest(t, root, "third", PackManifest{
		SchemaVersion: 1,
		Name:          "third",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, nil)
	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}, {Name: "third", Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `multiple packs have settings.enabled`) {
		t.Fatalf("expected multiple settings pack error, got %v", err)
	}
}

func TestResolveProfile_MCPSelectionErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 1,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		MCP: MCPPack{Servers: map[string]MCPDefaults{
			"jira": {DefaultAllowedTools: []string{"get_issue"}},
		}},
	}, map[string]string{"mcp/jira.json": `{"name":"jira"}`})

	_, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name: "base",
			MCP:  map[string]MCPServerConfig{"missing": {Enabled: BoolPtr(true)}},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" references unknown mcp server "missing"`) {
		t.Fatalf("expected unknown mcp server error, got %v", err)
	}
}

func installPackForResolveTest(t *testing.T, configDir string, packName string, manifest PackManifest, files map[string]string) {
	t.Helper()
	packRoot := filepath.Join(configDir, "packs", packName)
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		path := filepath.Join(packRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
