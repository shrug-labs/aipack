package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestClean_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "clean", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("clean --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestClean_InvalidScope_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "clean", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("clean --scope badscope should fail, got exit=%d", code)
	}
}

func TestClean_DryRunWithoutHarnessDefaults_UsesAllHarnesses(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Chdir(projectDir)

	_, stderr, code := runApp(t, "clean", "--dry-run")
	if code != cmdutil.ExitOK {
		t.Fatalf("clean --dry-run exit=%d stderr=%s", code, stderr)
	}
	for _, frag := range []string{".claude", ".clinerules", ".agents", ".opencode"} {
		if !strings.Contains(stderr, frag) {
			t.Fatalf("expected clean dry-run output to mention %s, got: %s", frag, stderr)
		}
	}
}

func TestClean_DefaultGlobalScopeRejectsProjectDir(t *testing.T) {
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

	_, stderr, code := runApp(t, "clean", "--config-dir", configDir, "--project-dir", projectDir)
	if code == cmdutil.ExitOK {
		t.Fatal("clean should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}
