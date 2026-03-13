package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestValidateCmd_Help(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "validate", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("validate --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestValidateCmd_MissingPackRootFails(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "validate")
	if code == cmdutil.ExitOK {
		t.Fatal("expected non-zero exit for missing pack root")
	}
}

func TestValidateCmd_JSONReportsFindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"schema_version":1,"name":"demo","root":"."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "missing-frontmatter.md"), []byte("no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "validate", dir, "--json")
	if code == cmdutil.ExitOK {
		t.Fatal("expected non-zero exit for invalid pack")
	}
	if stdout == "" {
		t.Fatal("expected validator output")
	}
	var rep struct {
		OK       bool `json:"ok"`
		Findings []struct {
			Path     string `json:"path"`
			Category string `json:"category"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal validate json: %v\njson=%s", err, stdout)
	}
	if rep.OK {
		t.Fatal("expected ok=false in json report")
	}
	if len(rep.Findings) == 0 {
		t.Fatal("expected at least one finding in json report")
	}
	// Verify structured fields are populated.
	f := rep.Findings[0]
	if f.Category == "" {
		t.Fatal("expected category to be set")
	}
	if f.Severity == "" {
		t.Fatal("expected severity to be set")
	}
}

func TestValidateCmd_WarningsOnlyExitOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Pack with a rule missing its description — produces a warning but no error.
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"schema_version":1,"name":"demo","root":".","rules":["no-desc"],"agents":[],"workflows":[],"skills":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "no-desc.md"), []byte("---\nname: no-desc\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "validate", dir)
	if code != cmdutil.ExitOK {
		t.Fatalf("expected exit 0 for warnings-only pack, got %d\nstderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "with warnings") {
		t.Fatalf("expected 'with warnings' in stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "[warning]") {
		t.Fatalf("expected warnings printed to stderr, got %q", stderr)
	}
}
