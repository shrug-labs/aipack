package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestTrace_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("trace --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestTrace_InvalidScope_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "rule", "test-rule", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("trace --scope badscope should fail, got exit=%d", code)
	}
}

func TestTrace_GlobalWithProjectDir_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "rule", "test-rule", "--scope", "global", "--project-dir", "/tmp")
	if code == cmdutil.ExitOK {
		t.Fatal("trace --scope global --project-dir should fail")
	}
}

func TestTrace_WithoutHarnessDefaults_UsesAllHarnesses(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "sample", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, harness := range []string{"claudecode:", "cline:", "codex:", "opencode:"} {
		if !strings.Contains(stdout, harness) {
			t.Fatalf("expected trace output to include %s, got: %s", harness, stdout)
		}
	}
}

func TestTrace_DefaultGlobalScopeRejectsProjectDir(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: global\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "trace", "rule", "sample", "--config-dir", configDir, "--project-dir", filepath.Join(home, "project"))
	if code == cmdutil.ExitOK {
		t.Fatal("trace should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}
