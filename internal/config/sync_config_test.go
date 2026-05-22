package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestLoadSyncConfig_Missing_IsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync-config.yaml")
	cfg, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if cfg.SchemaVersion == 0 {
		t.Fatalf("expected schema_version set")
	}
}

func TestLoadSyncConfig_ParsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\ndefaults:\n  profile: ops\n  harnesses: [cline, opencode]\n  scope: project\n  auto_sync: true\n  namespaced: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if cfg.Defaults.Profile != "ops" {
		t.Fatalf("expected profile=ops, got %q", cfg.Defaults.Profile)
	}
	if len(cfg.Defaults.Harnesses) != 2 {
		t.Fatalf("expected 2 harnesses, got %d", len(cfg.Defaults.Harnesses))
	}
	if cfg.Defaults.Scope != "project" {
		t.Fatalf("expected scope=project, got %q", cfg.Defaults.Scope)
	}
	if !cfg.Defaults.AutoSync {
		t.Fatal("expected auto_sync=true")
	}
	if !cfg.Defaults.Namespaced {
		t.Fatal("expected namespaced=true")
	}
}

func TestLoadSyncConfig_DefaultsBooleansFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync-config.yaml")
	cfg, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if cfg.Defaults.AutoSync {
		t.Fatal("missing auto_sync should default to false")
	}
	if cfg.Defaults.Namespaced {
		t.Fatal("missing namespaced should default to false")
	}
}

func TestSaveSyncConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")

	cfg := SyncConfig{SchemaVersion: SyncConfigSchemaVersion}
	cfg.Defaults.Profile = "test"
	cfg.Defaults.AutoSync = true
	cfg.Defaults.Namespaced = true
	cfg.InstalledPacks = map[string]InstalledPackMeta{
		"my-pack": {
			Origin:      "https://github.com/example/my-pack",
			Method:      MethodClone,
			InstalledAt: "2025-06-15T12:00:00Z",
			Ref:         "main",
		},
	}

	if err := SaveSyncConfig(path, cfg); err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}

	loaded, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if loaded.Defaults.Profile != "test" {
		t.Fatalf("profile = %q, want %q", loaded.Defaults.Profile, "test")
	}
	if !loaded.Defaults.AutoSync {
		t.Fatal("auto_sync did not round-trip")
	}
	if !loaded.Defaults.Namespaced {
		t.Fatal("namespaced did not round-trip")
	}
	meta, ok := loaded.InstalledPacks["my-pack"]
	if !ok {
		t.Fatal("expected my-pack in InstalledPacks")
	}
	if meta.Origin != "https://github.com/example/my-pack" {
		t.Fatalf("origin = %q", meta.Origin)
	}
	if meta.Method != MethodClone {
		t.Fatalf("method = %q", meta.Method)
	}
	if meta.Ref != "main" {
		t.Fatalf("ref = %q", meta.Ref)
	}
	if meta.InstalledAt != "2025-06-15T12:00:00Z" {
		t.Fatalf("installed_at = %q", meta.InstalledAt)
	}
}

func TestSaveSyncConfig_ContentPathsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")

	cfg := SyncConfig{SchemaVersion: SyncConfigSchemaVersion}
	cfg.InstalledPacks = map[string]InstalledPackMeta{
		"ext-skills": {
			Origin: "ssh://git@example.com:7999/team/repo.git",
			Method: MethodClone,
			ContentPaths: map[domain.PackCategory]string{
				domain.CategorySkills: "src/agent/skills",
				domain.CategoryRules:  "config/rules",
			},
		},
	}
	if err := SaveSyncConfig(path, cfg); err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}

	loaded, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta := loaded.InstalledPacks["ext-skills"]
	if len(meta.ContentPaths) != 2 {
		t.Fatalf("content_paths count = %d, want 2", len(meta.ContentPaths))
	}
	if meta.ContentPaths[domain.CategorySkills] != "src/agent/skills" {
		t.Fatalf("skills path = %q", meta.ContentPaths[domain.CategorySkills])
	}
	if meta.ContentPaths[domain.CategoryRules] != "config/rules" {
		t.Fatalf("rules path = %q", meta.ContentPaths[domain.CategoryRules])
	}
}

func TestSaveSyncConfig_PrefsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")

	cfg := SyncConfig{SchemaVersion: SyncConfigSchemaVersion}
	cfg.InstalledPacks = map[string]InstalledPackMeta{
		"prefs-pack": {
			Origin:   "https://example.com/repo.git",
			Method:   MethodClone,
			Approved: []domain.BundledCategory{"rules", "skills", "profiles"},
			Declined: []domain.BundledCategory{"registries", "extras"},
		},
		"no-prefs": {
			Origin: "/local/path",
			Method: MethodCopy,
			// Approved and Declined intentionally nil (pre-preference pack).
		},
	}
	if err := SaveSyncConfig(path, cfg); err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}

	loaded, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}

	// Pack with preferences.
	m := loaded.InstalledPacks["prefs-pack"]
	if len(m.Approved) != 3 {
		t.Fatalf("approved count = %d, want 3; got %v", len(m.Approved), m.Approved)
	}
	if m.Approved[0] != "rules" || m.Approved[1] != "skills" || m.Approved[2] != "profiles" {
		t.Fatalf("approved = %v", m.Approved)
	}
	if len(m.Declined) != 2 {
		t.Fatalf("declined count = %d, want 2; got %v", len(m.Declined), m.Declined)
	}
	if m.Declined[0] != "registries" || m.Declined[1] != "extras" {
		t.Fatalf("declined = %v", m.Declined)
	}

	// Pre-preference pack (no approved/declined).
	m2 := loaded.InstalledPacks["no-prefs"]
	if m2.Approved != nil {
		t.Fatalf("expected nil Approved for pre-preference pack, got %v", m2.Approved)
	}
	if m2.Declined != nil {
		t.Fatalf("expected nil Declined for pre-preference pack, got %v", m2.Declined)
	}
}

func TestLoadSyncConfig_NoInstalledPacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\ndefaults:\n  profile: default\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if cfg.InstalledPacks != nil {
		t.Fatalf("expected nil InstalledPacks for old format, got %v", cfg.InstalledPacks)
	}
}
