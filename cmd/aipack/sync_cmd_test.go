package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestSync_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestSync_InvalidScope_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --scope badscope should fail, got exit=%d", code)
	}
}

func TestSync_UnknownFlag_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--mode", "merge")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --mode (unknown flag) should fail, got exit=%d", code)
	}
}

func TestSync_ProjectDir_InvalidWithGlobalScope(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--scope", "global", "--project-dir", "/tmp/proj")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --scope global --project-dir should fail, got exit=%d", code)
	}
}

func TestSync_DefaultGlobalScopeRejectsProjectDir(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: global\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--project-dir", filepath.Join(home, "project"))
	if code == cmdutil.ExitOK {
		t.Fatal("sync should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}
