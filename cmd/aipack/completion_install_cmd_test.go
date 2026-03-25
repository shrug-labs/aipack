package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestInstallCompletions_Print(t *testing.T) {
	t.Parallel()
	stdout, _, code := runApp(t, "install-completions", "--print")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	// Bash/zsh snippets contain "complete"; PowerShell uses "Completer"/"Completion".
	if !strings.Contains(strings.ToLower(stdout), "complet") {
		t.Fatalf("expected completion snippet in stdout, got: %s", stdout)
	}
}

func TestInstallCompletions_InstallWithYes(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	stdout, stderr, code := runApp(t, "install-completions", "--rc-file", rc, "--yes")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Installed") {
		t.Fatalf("expected 'Installed' message, got: %s", stdout)
	}

	content, _ := os.ReadFile(rc)
	if !strings.Contains(string(content), markerBegin) {
		t.Fatalf("rc file should contain marker, got:\n%s", string(content))
	}
}

func TestInstallCompletions_InstallConfirmYes(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	stdout, stderr, code := runAppWithInput(t, "y\n", true, "install-completions", "--rc-file", rc)
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stderr, "Append to") {
		t.Fatalf("expected preview on stderr, got: %s", stderr)
	}
	if !strings.Contains(stdout, "Installed") {
		t.Fatalf("expected 'Installed' message, got: %s", stdout)
	}

	content, _ := os.ReadFile(rc)
	if !strings.Contains(string(content), markerBegin) {
		t.Fatalf("rc file should contain marker, got:\n%s", string(content))
	}
}

func TestInstallCompletions_InstallConfirmNo(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	stdout, _, code := runAppWithInput(t, "n\n", true, "install-completions", "--rc-file", rc)
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	if !strings.Contains(stdout, "Aborted") {
		t.Fatalf("expected 'Aborted' message, got: %s", stdout)
	}

	_, err := os.Stat(rc)
	if err == nil {
		content, _ := os.ReadFile(rc)
		if strings.Contains(string(content), markerBegin) {
			t.Fatal("rc file should not contain completions after declining")
		}
	}
}

func TestInstallCompletions_InstallNonInteractive(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	// stdinTTY=false, no --yes → should skip
	stdout, _, code := runApp(t, "install-completions", "--rc-file", rc)
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	if !strings.Contains(stdout, "use --yes") {
		t.Fatalf("expected non-interactive skip message, got: %s", stdout)
	}
}

func TestInstallCompletions_AlreadyInstalled(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	runApp(t, "install-completions", "--rc-file", rc, "--yes")

	stdout, _, code := runApp(t, "install-completions", "--rc-file", rc, "--yes")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	if !strings.Contains(stdout, "already installed") {
		t.Fatalf("expected 'already installed' message, got: %s", stdout)
	}
}

func TestInstallCompletions_UninstallWithYes(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")

	runApp(t, "install-completions", "--rc-file", rc, "--yes")

	stdout, _, code := runApp(t, "install-completions", "--uninstall", "--rc-file", rc, "--yes")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	if !strings.Contains(stdout, "Removed") {
		t.Fatalf("expected 'Removed' message, got: %s", stdout)
	}

	content, _ := os.ReadFile(rc)
	if strings.Contains(string(content), "aipack") {
		t.Fatalf("rc file should not contain aipack after uninstall, got:\n%s", string(content))
	}
}

func TestInstallCompletions_UninstallNotPresent(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".testrc")
	os.WriteFile(rc, []byte("# empty\n"), 0o644)

	stdout, _, code := runApp(t, "install-completions", "--uninstall", "--rc-file", rc, "--yes")
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d, want %d", code, cmdutil.ExitOK)
	}
	if !strings.Contains(stdout, "not installed") {
		t.Fatalf("expected 'not installed' message, got: %s", stdout)
	}
}
