package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

// TestArchitecture_ServicePackagesDoNotImportCmd verifies the one-way
// dependency direction: cmd → app/internal, never the reverse.
func TestArchitecture_ServicePackagesDoNotImportCmd(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Imports}}", "github.com/shrug-labs/aipack/internal/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	forbidden := "github.com/shrug-labs/aipack/cmd/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		pkg := parts[0]
		imports := parts[1]
		if strings.Contains(imports, forbidden) {
			t.Errorf("package %s imports from cmd layer: %s", pkg, imports)
		}
	}
}

// TestArchitecture_NoDeletedPackages verifies no package imports deleted v1
// packages or uses the obsolete v2/ prefix.
func TestArchitecture_NoDeletedPackages(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Imports}}", "github.com/shrug-labs/aipack/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	forbidden := []string{
		"github.com/shrug-labs/aipack/internal/sync",
		"github.com/shrug-labs/aipack/internal/syncmodel",
		"github.com/shrug-labs/aipack/internal/harnesses",
		"github.com/shrug-labs/aipack/internal/ledger",
		"github.com/shrug-labs/aipack/internal/mcpinventory",
		"github.com/shrug-labs/aipack/internal/v2/",
		"github.com/shrug-labs/aipack/internal/app/pack",
		"github.com/shrug-labs/aipack/internal/app/save",
		"github.com/shrug-labs/aipack/internal/app/clean",
		"github.com/shrug-labs/aipack/internal/app/doctor",
		"github.com/shrug-labs/aipack/internal/app/seed",
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		pkg := parts[0]
		imports := parts[1]
		for _, f := range forbidden {
			if strings.Contains(imports, f) {
				t.Errorf("package %s imports deleted/obsolete package %s", pkg, f)
			}
		}
	}
}

// TestArchitecture_HarnessAndRenderDoNotImportConfig verifies the layer rule:
// harness/ and render/ depend on domain/ and engine/, never config/.
func TestArchitecture_HarnessAndRenderDoNotImportConfig(t *testing.T) {
	t.Parallel()

	packages := []string{
		"github.com/shrug-labs/aipack/internal/harness/...",
		"github.com/shrug-labs/aipack/internal/render",
	}
	forbidden := "github.com/shrug-labs/aipack/internal/config"

	for _, pkg := range packages {
		cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Imports}}", pkg)
		out, err := cmd.CombinedOutput()
		if err != nil {
			continue // package may have no Go files
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) < 2 {
				continue
			}
			if strings.Contains(parts[1], forbidden) {
				t.Errorf("package %s imports config/ — violates layer rule", parts[0])
			}
		}
	}
}

// TestCLIExitCodes_HelpReturnsZero exercises the --help flag for each
// subcommand and verifies it returns exit code 0 (not usage error).
func TestCLIExitCodes_HelpReturnsZero(t *testing.T) {
	t.Parallel()

	commands := []string{"doctor", "validate", "save", "init", "clean", "sync", "render", "version", "manage", "pack", "profile", "search", "query"}
	for _, cmd := range commands {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			_, _, code := runApp(t, cmd, "--help")
			if code != cmdutil.ExitOK {
				t.Errorf("%s --help returned %d, want %d", cmd, code, cmdutil.ExitOK)
			}
		})
	}
}

// TestCLIExitCodes_InvalidSubcommandFails verifies that unknown subcommands
// produce a non-zero exit code.
func TestCLIExitCodes_InvalidSubcommandFails(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "nonexistent")
	if code == cmdutil.ExitOK {
		t.Fatalf("expected non-zero exit for unknown subcommand, got %d", code)
	}
}
