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
	_, _, code := runApp(t, "pack", "validate", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack validate --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestValidateCmd_MissingPackRootFails(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "validate")
	if code == cmdutil.ExitOK {
		t.Fatal("expected non-zero exit for missing pack root")
	}
}

func TestValidateCmd_JSONReportsFindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"schema_version":2,"name":"demo","root":"."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "missing-frontmatter.md"), []byte("no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "pack", "validate", dir, "--json")
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

func TestValidateCmd_ExtrasValid(t *testing.T) {
	// Proves: extras validation is wired through the CLI for both files and dirs.
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pack.json"), []byte(
		`{"schema_version":2,"name":"inc","root":".","extras":["wrappers","proxy.py"]}`), 0o644)
	os.MkdirAll(filepath.Join(dir, "wrappers"), 0o755)
	os.WriteFile(filepath.Join(dir, "proxy.py"), []byte("#!/usr/bin/env python3\n"), 0o644)

	_, _, code := runApp(t, "pack", "validate", dir)
	if code != cmdutil.ExitOK {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestValidateCmd_ExtrasInvalidJSON(t *testing.T) {
	// Proves: extras errors surface in --json output with correct structure.
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pack.json"), []byte(
		`{"schema_version":2,"name":"inc","root":".","extras":["missing","/absolute"]}`), 0o644)

	stdout, _, code := runApp(t, "pack", "validate", dir, "--json")
	if code == cmdutil.ExitOK {
		t.Fatal("expected non-zero exit")
	}
	var rep struct {
		OK       bool `json:"ok"`
		Findings []struct {
			Path     string `json:"path"`
			Message  string `json:"message"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal: %v\njson=%s", err, stdout)
	}
	if rep.OK {
		t.Fatal("expected ok=false")
	}
	// Should have findings for both the missing path and the absolute path.
	var gotMissing, gotAbsolute bool
	for _, f := range rep.Findings {
		if f.Severity == "error" && strings.Contains(f.Message, "not found") {
			gotMissing = true
		}
		if strings.Contains(f.Message, "must be relative") {
			gotAbsolute = true
		}
	}
	if !gotMissing {
		t.Errorf("expected a 'not found' finding for missing extras path")
	}
	if !gotAbsolute {
		t.Errorf("expected a 'must be relative' finding for /absolute")
	}
}
