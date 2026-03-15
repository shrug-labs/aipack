package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestInit_HappyPath_WritesFiles(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()

	_, stderr, code := runApp(t, "init", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("init exit=%d, stderr=%q", code, stderr)
	}

	// sync-config.yaml: verify it is loadable and has expected defaults.
	// Exact byte comparison is not used because init now auto-fetches the
	// default registry, which appends registry_sources to the file.
	syncCfgPath := filepath.Join(configDir, "sync-config.yaml")
	sc, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		t.Fatalf("load sync-config: %v", err)
	}
	if sc.Defaults.Profile != "default" {
		t.Errorf("sync-config defaults.profile = %q, want default", sc.Defaults.Profile)
	}

	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	gotProf, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if string(gotProf) != string(config.InitProfileBytes) {
		t.Fatalf("profile contents mismatch\n--- got\n%s\n--- want\n%s", string(gotProf), string(config.InitProfileBytes))
	}

	// registry.yaml should not be created (registries are managed via registry fetch).
	regPath := filepath.Join(configDir, "registry.yaml")
	if _, err := os.Stat(regPath); err == nil {
		t.Fatal("registry.yaml should not be created by init")
	}
}

func TestInit_SkipWhenPresent_NoForce(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()

	syncCfgPath := filepath.Join(configDir, "sync-config.yaml")
	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, syncCfgPath, []byte("old sync\n"))
	writeFile(t, profPath, []byte("old prof\n"))

	_, stderr, code := runApp(t, "init", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("init exit=%d, stderr=%q", code, stderr)
	}

	gotSync, err := os.ReadFile(syncCfgPath)
	if err != nil {
		t.Fatalf("read sync-config: %v", err)
	}
	if string(gotSync) != "old sync\n" {
		t.Fatalf("sync-config was overwritten unexpectedly: %q", string(gotSync))
	}

	gotProf, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if string(gotProf) != "old prof\n" {
		t.Fatalf("profile was overwritten unexpectedly: %q", string(gotProf))
	}
}

func TestInit_ForceOverwrites(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()

	syncCfgPath := filepath.Join(configDir, "sync-config.yaml")
	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, syncCfgPath, []byte("old sync\n"))
	writeFile(t, profPath, []byte("old prof\n"))

	_, stderr, code := runApp(t, "init", "--config-dir", configDir, "--force")
	if code != cmdutil.ExitOK {
		t.Fatalf("init exit=%d, stderr=%q", code, stderr)
	}

	// sync-config.yaml: verify it is loadable and has expected defaults.
	sc, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		t.Fatalf("load sync-config: %v", err)
	}
	if sc.Defaults.Profile != "default" {
		t.Errorf("sync-config defaults.profile = %q, want default", sc.Defaults.Profile)
	}

	gotProf, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if string(gotProf) != string(config.InitProfileBytes) {
		t.Fatalf("profile contents mismatch\n--- got\n%s\n--- want\n%s", string(gotProf), string(config.InitProfileBytes))
	}
}

func TestInit_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "init", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("init --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
