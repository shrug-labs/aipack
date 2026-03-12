package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeIntegrity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create some files.
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "team.md"), []byte("# Team"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := computeIntegrity(dir)
	if err != nil {
		t.Fatalf("computeIntegrity: %v", err)
	}

	if len(m.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(m.Files), m.Files)
	}
	if _, ok := m.Files["pack.json"]; !ok {
		t.Error("missing pack.json")
	}
	if _, ok := m.Files["rules/team.md"]; !ok {
		t.Error("missing rules/team.md")
	}
}

func TestSaveAndLoadIntegrity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := saveIntegrity(dir); err != nil {
		t.Fatalf("saveIntegrity: %v", err)
	}

	loaded, err := loadIntegrity(dir)
	if err != nil {
		t.Fatalf("loadIntegrity: %v", err)
	}
	if len(loaded.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(loaded.Files))
	}
	if _, ok := loaded.Files["file.txt"]; !ok {
		t.Error("missing file.txt in loaded integrity")
	}
}

func TestDiffIntegrity(t *testing.T) {
	t.Parallel()

	old := IntegrityManifest{Files: map[string]string{
		"rules/team.md":    "aaa",
		"rules/safety.md":  "bbb",
		"rules/removed.md": "ccc",
	}}
	new := IntegrityManifest{Files: map[string]string{
		"rules/team.md":   "aaa", // unchanged
		"rules/safety.md": "ddd", // modified
		"rules/added.md":  "eee", // added
	}}

	diff := diffIntegrity(old, new)

	if len(diff.Modified) != 1 || diff.Modified[0] != "rules/safety.md" {
		t.Errorf("Modified = %v, want [rules/safety.md]", diff.Modified)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "rules/added.md" {
		t.Errorf("Added = %v, want [rules/added.md]", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "rules/removed.md" {
		t.Errorf("Removed = %v, want [rules/removed.md]", diff.Removed)
	}
}

func TestDiffIntegrity_NoChanges(t *testing.T) {
	t.Parallel()
	m := IntegrityManifest{Files: map[string]string{
		"a.md": "hash1",
		"b.md": "hash2",
	}}
	diff := diffIntegrity(m, m)
	if diff.HasChanges() {
		t.Error("expected no changes")
	}
}

func TestLoadIntegrity_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m, err := loadIntegrity(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Files) != 0 {
		t.Fatalf("expected empty files, got %v", m.Files)
	}
}
