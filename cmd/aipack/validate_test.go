package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"schema_version":1,"name":"demo","root":"."}`), 0o644); err != nil {
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
