package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestRestore_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "restore", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("restore --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestRestore_InvalidScope_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "restore", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("restore --scope badscope should fail, got exit=%d", code)
	}
}

func TestRestore_DefaultGlobalScopeRejectsProjectDir(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  scope: global\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "restore", "--config-dir", configDir, "--project-dir", projectDir, "--yes")
	if code == cmdutil.ExitOK {
		t.Fatal("restore should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}
