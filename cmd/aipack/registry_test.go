package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestRegistryValidate_JSONValid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(`schema_version: 1
packs:
  demo:
    repo: https://github.com/example/demo.git
    description: Demo pack
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "registry", "validate", path, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("registry validate exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["valid"] != true {
		t.Fatalf("valid = %v, want true; output=%s", got["valid"], stdout)
	}
}

func TestRegistryValidate_InvalidReportsAllErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(`schema_version: 1
packs:
  missing:
    description: Missing source
  archive:
    method: archive
    repo: https://github.com/example/not-used.git
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "registry", "validate", path)
	if code != cmdutil.ExitFail {
		t.Fatalf("registry validate exit=%d, want %d; stderr=%s stdout=%s", code, cmdutil.ExitFail, stderr, stdout)
	}
	if !strings.Contains(stdout, `pack "missing": missing required field repo`) {
		t.Fatalf("stdout missing missing-source error:\n%s", stdout)
	}
	if !strings.Contains(stdout, `pack "archive": missing required field url`) {
		t.Fatalf("stdout missing archive url error:\n%s", stdout)
	}
}
