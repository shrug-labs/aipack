package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverIDs_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"alpha.md", "beta.md", "README.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := DiscoverIDs(dir, ".md")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "alpha" || ids[1] != "beta" {
		t.Fatalf("got %v, want [alpha beta]", ids)
	}
}

func TestDiscoverIDs_MissingDir(t *testing.T) {
	t.Parallel()
	ids, err := DiscoverIDs("/nonexistent/path", ".md")
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil {
		t.Fatalf("expected nil for missing dir, got %v", ids)
	}
}

func TestDiscoverIDs_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ids, err := DiscoverIDs(dir, ".md")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty slice, got %v", ids)
	}
}

func TestDiscoverSkills_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Valid skill: subdir with SKILL.md
	skillDir := filepath.Join(dir, "oncall")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Invalid: subdir without SKILL.md
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid: file, not directory
	if err := os.WriteFile(filepath.Join(dir, "notadir.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ids, err := DiscoverSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "oncall" {
		t.Fatalf("got %v, want [oncall]", ids)
	}
}

func TestDiscoverSkills_MissingDir(t *testing.T) {
	t.Parallel()
	ids, err := DiscoverSkills("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil {
		t.Fatalf("expected nil for missing dir, got %v", ids)
	}
}

func TestDiscoverContent_NilFieldsPopulated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create content on disk
	for _, dir := range []string{"rules", "agents", "workflows"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "a1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "s1")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := PackManifest{
		SchemaVersion: 1,
		Name:          "test",
		Root:          ".",
		// All content fields are nil (absent from JSON)
	}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}

	if len(m.Rules) != 1 || m.Rules[0] != "r1" {
		t.Fatalf("Rules = %v, want [r1]", m.Rules)
	}
	if len(m.Agents) != 1 || m.Agents[0] != "a1" {
		t.Fatalf("Agents = %v, want [a1]", m.Agents)
	}
	if len(m.Workflows) != 0 {
		t.Fatalf("Workflows = %v, want []", m.Workflows)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "s1" {
		t.Fatalf("Skills = %v, want [s1]", m.Skills)
	}
}

func TestDiscoverContent_ExplicitFieldsPreserved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create two rules on disk
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r2.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := PackManifest{
		SchemaVersion: 1,
		Name:          "test",
		Root:          ".",
		Rules:         []string{"r1"}, // explicit list — only r1
	}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}

	// Explicit field should NOT be overwritten — still just r1
	if len(m.Rules) != 1 || m.Rules[0] != "r1" {
		t.Fatalf("Rules = %v, want [r1] (explicit preserved)", m.Rules)
	}
}

func TestDiscoverContent_EmptySliceMeansNoContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create a rule on disk
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Explicit empty slice means "I explicitly have zero rules"
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "test",
		Root:          ".",
		Rules:         []string{}, // non-nil empty slice
	}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}

	// Should remain empty — not overwritten by discovery
	if len(m.Rules) != 0 {
		t.Fatalf("Rules = %v, want [] (empty slice preserved)", m.Rules)
	}
}
