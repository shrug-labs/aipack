package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := os.WriteFile(path, []byte("schema_version: 1\ndefaults:\n  profile: ocm\n  harnesses: [cline, opencode]\n  scope: project\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadSyncConfig(path)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if cfg.Defaults.Profile != "ocm" {
		t.Fatalf("expected profile=ocm, got %q", cfg.Defaults.Profile)
	}
	if len(cfg.Defaults.Harnesses) != 2 {
		t.Fatalf("expected 2 harnesses, got %d", len(cfg.Defaults.Harnesses))
	}
	if cfg.Defaults.Scope != "project" {
		t.Fatalf("expected scope=project, got %q", cfg.Defaults.Scope)
	}
}

func TestSaveSyncConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync-config.yaml")

	cfg := SyncConfig{SchemaVersion: SyncConfigSchemaVersion}
	cfg.Defaults.Profile = "test"
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
