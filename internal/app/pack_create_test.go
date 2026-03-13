package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

func TestPackCreate_ScaffoldsValidPack(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "my-pack")

	if err := PackCreate(PackCreateRequest{Dir: dir, Name: "my-pack"}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	// Verify pack.json round-trips through LoadPackManifest.
	m, err := config.LoadPackManifest(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if m.Name != "my-pack" {
		t.Fatalf("name = %q, want %q", m.Name, "my-pack")
	}
	if m.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", m.SchemaVersion)
	}
	if m.Root != "." {
		t.Fatalf("root = %q, want %q", m.Root, ".")
	}

	// Content vector fields must be nil so DiscoverContent auto-discovers.
	if m.Rules != nil {
		t.Fatalf("Rules = %v, want nil (auto-discovery friendly)", m.Rules)
	}
	if m.Agents != nil {
		t.Fatalf("Agents = %v, want nil (auto-discovery friendly)", m.Agents)
	}
	if m.Workflows != nil {
		t.Fatalf("Workflows = %v, want nil (auto-discovery friendly)", m.Workflows)
	}
	if m.Skills != nil {
		t.Fatalf("Skills = %v, want nil (auto-discovery friendly)", m.Skills)
	}

	// Verify all vector dirs exist.
	for _, sub := range []string{"rules", "agents", "workflows", "skills", "mcp", "configs"} {
		d := filepath.Join(dir, sub)
		st, err := os.Stat(d)
		if err != nil {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
	}
}

func TestPackCreate_AutoDiscoveryWorksWithScaffold(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "disco-pack")

	if err := PackCreate(PackCreateRequest{Dir: dir, Name: "disco-pack"}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	// Add content files to the scaffolded directories.
	if err := os.WriteFile(filepath.Join(dir, "rules", "my-rule.md"), []byte("# Rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load manifest and run auto-discovery — should find the new content.
	m, err := config.LoadPackManifest(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if err := config.DiscoverContent(&m, dir); err != nil {
		t.Fatalf("DiscoverContent: %v", err)
	}

	if len(m.Rules) != 1 || m.Rules[0] != "my-rule" {
		t.Fatalf("Rules = %v, want [my-rule]", m.Rules)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "my-skill" {
		t.Fatalf("Skills = %v, want [my-skill]", m.Skills)
	}
}

func TestPackCreate_DefaultsNameToBasename(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cool-pack")

	if err := PackCreate(PackCreateRequest{Dir: dir}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	m, err := config.LoadPackManifest(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if m.Name != "cool-pack" {
		t.Fatalf("name = %q, want %q", m.Name, "cool-pack")
	}
}

func TestPackCreate_ErrorOnExistingPackJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := PackCreate(PackCreateRequest{Dir: dir, Name: "test"})
	if err == nil {
		t.Fatal("expected error on existing pack.json")
	}
}

func TestPackCreate_ErrorOnEmptyDir(t *testing.T) {
	t.Parallel()
	err := PackCreate(PackCreateRequest{Dir: ""})
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}
