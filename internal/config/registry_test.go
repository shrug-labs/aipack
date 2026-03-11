package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistry_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/repo
    path: subdir
    description: A test pack
    ref: v1.0.0
    owner: Team A
    contact: "#team-a"
  root-pack:
    repo: https://github.com/org/other
    description: Pack at repo root
`), 0o600)

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(reg.Packs))
	}
	entry := reg.Packs["my-pack"]
	if entry.Repo != "https://github.com/org/repo" {
		t.Errorf("Repo = %q, want %q", entry.Repo, "https://github.com/org/repo")
	}
	if entry.Path != "subdir" {
		t.Errorf("Path = %q, want %q", entry.Path, "subdir")
	}
	if entry.Ref != "v1.0.0" {
		t.Errorf("Ref = %q, want %q", entry.Ref, "v1.0.0")
	}
	if entry.Owner != "Team A" {
		t.Errorf("Owner = %q, want %q", entry.Owner, "Team A")
	}

	root := reg.Packs["root-pack"]
	if root.Path != "" {
		t.Errorf("root-pack Path = %q, want empty", root.Path)
	}
}

func TestLoadRegistry_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadRegistry("/nonexistent/registry.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadRegistry_BadSchemaVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 99
packs: {}
`), 0o600)

	_, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("expected error for bad schema version")
	}
}

func TestLoadRegistry_EmptyPacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 1
`), 0o600)

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Packs == nil {
		t.Fatal("Packs map should be initialized, not nil")
	}
	if len(reg.Packs) != 0 {
		t.Errorf("expected 0 packs, got %d", len(reg.Packs))
	}
}

func TestResolveRegistryPath_FlagOverride(t *testing.T) {
	t.Parallel()
	got := ResolveRegistryPath("/explicit/path.yaml", "", "/config")
	if got != "/explicit/path.yaml" {
		t.Errorf("got %q, want /explicit/path.yaml", got)
	}
}

func TestResolveRegistryPath_SyncConfigDefault(t *testing.T) {
	t.Parallel()
	got := ResolveRegistryPath("", "/from/sync-config.yaml", "/config")
	if got != "/from/sync-config.yaml" {
		t.Errorf("got %q, want /from/sync-config.yaml", got)
	}
}

func TestResolveRegistryPath_DefaultFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := ResolveRegistryPath("", "", dir)
	want := filepath.Join(dir, "registry.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
