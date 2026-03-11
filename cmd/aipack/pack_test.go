package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestPackList_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "list", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackList_JSON_Empty(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []interface{}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(entries))
	}
}

func TestPackList_JSON_WithPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "test-pack")

	var addOut [0]byte
	_ = app.PackAdd(app.PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, os.NewFile(0, os.DevNull))
	_ = addOut

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["name"] != "test-pack" {
		t.Fatalf("name = %v, want test-pack", entries[0]["name"])
	}
}

func TestPackUpdate_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack update --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackUpdate_MutualExclusion(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--all", "my-pack")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update --all+name should fail, got exit=%d", code)
	}
}

func TestPackUpdate_NeitherNameNorAll(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update (no args) should fail, got exit=%d", code)
	}
}

func TestPackShow_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack show --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackShow_MissingName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack show (no args) should fail, got exit=%d", code)
	}
}

func TestPackShow_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "pack", "show", "nonexistent", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("pack show nonexistent exit=%d, want %d", code, cmdutil.ExitFail)
	}
}

func writePackManifestCmd(t *testing.T, dir string, name string) {
	t.Helper()
	m := map[string]interface{}{
		"schema_version": 1,
		"name":           name,
		"version":        "1.0.0",
		"root":           ".",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
