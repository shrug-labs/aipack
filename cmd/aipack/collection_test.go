package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func writeCollectionTestRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(`schema_version: 1
packs:
  essentials:
    repo: https://github.com/shrug-labs/packs.git
    path: essentials
  aipack-core:
    repo: https://github.com/shrug-labs/packs.git
    path: aipack-core
collections:
  team-dev:
    description: Team developer starter set
    packs:
      - essentials
      - name: aipack-core
        ref: aipack-core/v1.2.3
        with: all
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectionList(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	registryPath := writeCollectionTestRegistry(t)

	stdout, stderr, code := runApp(t, "--config-dir", configDir, "collection", "list", "--registry", registryPath)
	if code != cmdutil.ExitOK {
		t.Fatalf("collection list exit=%d, stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "team-dev") || !strings.Contains(stdout, "essentials, aipack-core@aipack-core/v1.2.3") {
		t.Fatalf("unexpected stdout:\n%s", stdout)
	}
}

func TestCollectionShowJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	registryPath := writeCollectionTestRegistry(t)

	stdout, stderr, code := runApp(t, "--config-dir", configDir, "collection", "show", "team-dev", "--registry", registryPath, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("collection show exit=%d, stderr=%s stdout=%s", code, stderr, stdout)
	}
	var got struct {
		Name  string `json:"name"`
		Packs []struct {
			Name string   `json:"name"`
			Ref  string   `json:"ref"`
			With []string `json:"with"`
		} `json:"packs"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got.Name != "team-dev" || len(got.Packs) != 2 {
		t.Fatalf("got %+v, want team-dev with 2 packs", got)
	}
	if got.Packs[1].With[0] != "all" {
		t.Fatalf("second pack with = %v, want [all]", got.Packs[1].With)
	}
}
