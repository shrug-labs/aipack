package main

import (
	"os"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestInstall_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "install", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("install --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestInstall_LocalPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "test-install")

	// Install via top-level alias.
	_, stderr, code := runApp(t, "install", packDir, "--config-dir", configDir, "--no-register")
	if code != cmdutil.ExitOK {
		t.Fatalf("install exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	// Verify the pack is listed.
	entries, err := app.PackList(configDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "test-install" {
			found = true
		}
	}
	if !found {
		t.Fatal("pack 'test-install' not found after install")
	}
}

func TestInstall_ValidationMutualExclusion(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "install", "./some-path", "--url", "https://example.com/repo", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("install with both path and --url should fail")
	}
}

func TestInstall_CopyWithURL(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "install", "--url", "https://example.com/repo", "--copy", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("install --copy --url should fail")
	}
}

func TestInstall_ProfileReconciliation_EmptyProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Create a minimal profile so reconciliation doesn't error on missing file.
	profDir := configDir + "/profiles"
	if err := os.MkdirAll(profDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profDir+"/default.yaml", []byte("packs: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "install", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("install (no args) exit=%d, want %d; stderr=%s stdout=%s", code, cmdutil.ExitOK, stderr, stdout)
	}
}
