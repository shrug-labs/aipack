package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureInit_CreatesAllFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "aipack") // does not exist yet

	created, err := EnsureInit(configDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for fresh directory")
	}

	// sync-config.yaml must exist and be loadable.
	sc, err := LoadSyncConfig(SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if sc.Defaults.Profile != "default" {
		t.Errorf("defaults.profile = %q, want default", sc.Defaults.Profile)
	}
	if sc.Defaults.Scope != "global" {
		t.Errorf("defaults.scope = %q, want global", sc.Defaults.Scope)
	}
	if len(sc.Defaults.Harnesses) != 1 || sc.Defaults.Harnesses[0] != "codex" {
		t.Errorf("defaults.harnesses = %v, want [codex]", sc.Defaults.Harnesses)
	}
	gotSyncConfig, err := os.ReadFile(SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("reading sync-config: %v", err)
	}
	if !strings.Contains(string(gotSyncConfig), "auto_sync: false") {
		t.Errorf("sync-config template missing visible auto_sync default:\n%s", string(gotSyncConfig))
	}
	if !strings.Contains(string(gotSyncConfig), "namespaced: false") {
		t.Errorf("sync-config template missing visible namespaced default:\n%s", string(gotSyncConfig))
	}

	// default profile must exist.
	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	if _, err := os.Stat(profPath); err != nil {
		t.Fatalf("default profile not created: %v", err)
	}

	// .env must exist as the local placeholder for machine-specific env refs.
	envPath := filepath.Join(configDir, ".env")
	gotEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf(".env not created: %v", err)
	}
	if len(gotEnv) != 0 {
		t.Fatalf(".env = %q, want empty placeholder", string(gotEnv))
	}
}

func TestEnsureInit_DirExistsButFileMissing(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir() // directory already exists

	// Simulate partial state: directory exists but no files.
	created, err := EnsureInit(configDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still create missing files even though the directory exists.
	scPath := SyncConfigPath(configDir)
	if _, err := os.Stat(scPath); err != nil {
		t.Errorf("sync-config.yaml not created when dir pre-exists: %v", err)
	}

	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	if _, err := os.Stat(profPath); err != nil {
		t.Errorf("default profile not created when dir pre-exists: %v", err)
	}

	// created should be true since files were written.
	if !created {
		t.Error("expected created=true when files were missing")
	}
}

func TestEnsureInit_IdempotentWhenComplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "aipack")

	// First call creates everything.
	if _, err := EnsureInit(configDir); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Second call should be a no-op and return false.
	created, err := EnsureInit(configDir)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if created {
		t.Error("expected created=false when all files already exist")
	}
}

func TestEnsureInit_PreservesExistingFiles(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Write a custom sync-config before EnsureInit.
	scPath := SyncConfigPath(configDir)
	customContent := []byte("schema_version: 1\ndefaults:\n  profile: custom\n")
	if err := os.MkdirAll(filepath.Dir(scPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scPath, customContent, 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureInit(configDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Existing sync-config must NOT be overwritten.
	got, err := os.ReadFile(scPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(customContent) {
		t.Errorf("sync-config was overwritten:\n  got:  %q\n  want: %q", string(got), string(customContent))
	}

	envPath := filepath.Join(configDir, ".env")
	customEnv := []byte("API_TOKEN=keep\n")
	if err := os.WriteFile(envPath, customEnv, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureInit(configDir); err != nil {
		t.Fatalf("second ensure init: %v", err)
	}
	gotEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotEnv) != string(customEnv) {
		t.Errorf(".env was overwritten:\n  got:  %q\n  want: %q", string(gotEnv), string(customEnv))
	}

	// But the missing default profile should be created.
	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	if _, err := os.Stat(profPath); err != nil {
		t.Errorf("default profile not created alongside existing sync-config: %v", err)
	}

	// created=true because the profile was written.
	if !created {
		t.Error("expected created=true when profile was missing")
	}
}
