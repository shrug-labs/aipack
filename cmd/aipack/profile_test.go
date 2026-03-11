package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestProfileList_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "profile", "list", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestProfileList_NoProfiles(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "profile", "list", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "No profiles found") {
		t.Fatalf("expected 'No profiles found', got: %s", stdout)
	}
}

func TestProfileList_WithDefault(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "default.yaml"), []byte("schema_version: 6\npacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "dev.yaml"), []byte("schema_version: 6\npacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "profile", "list", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	output := stdout
	if !strings.Contains(output, "default *") {
		t.Fatalf("expected 'default *' in output, got: %s", output)
	}
	if !strings.Contains(output, "dev") {
		t.Fatalf("expected 'dev' in output, got: %s", output)
	}
}

func TestProfileShow_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "profile", "show", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("profile show --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestProfileShow_MissingProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "profile", "show", "nonexistent", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("profile show nonexistent exit=%d, want %d", code, cmdutil.ExitFail)
	}
}
