package main

import (
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestInstall_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "install", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("install --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestInstall_HiddenFromTopLevelHelp(t *testing.T) {
	t.Parallel()
	stdout, _, code := runApp(t, "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("--help exit=%d, want %d", code, cmdutil.ExitOK)
	}
	// "install" should not appear as a visible top-level command.
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "install") {
			t.Fatalf("install should be hidden from top-level help, but found: %q", line)
		}
	}
}

func TestInstall_DelegatesToPackInstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "test-install")

	_, stderr, code := runApp(t, "install", packDir, "--config-dir", configDir, "--no-register")
	if code != cmdutil.ExitOK {
		t.Fatalf("install exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
}

func TestInstall_ValidationPassesThrough(t *testing.T) {
	t.Parallel()
	// --copy with --url is invalid; should fail the same as "pack install".
	_, _, code := runApp(t, "install", "--url", "https://example.com/repo", "--copy", "--config-dir", t.TempDir())
	if code == cmdutil.ExitOK {
		t.Fatal("install --copy --url should fail")
	}
}
