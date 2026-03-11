package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	packs, _, err := ResolveProfile(cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	// include: [] is treated as "no filter" (include all), same as include: null.
	// An empty allowlist is almost always a mistake from clearing the list.
	if len(packs[0].Rules) != 1 {
		t.Fatalf("expected 1 rule (empty include = include all), got %d", len(packs[0].Rules))
	}
}
