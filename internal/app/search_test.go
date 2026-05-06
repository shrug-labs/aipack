package app

import (
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/index"
)

func TestRunIndexSearchHydratesInstalledStateFromLockfile(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	packRoot := filepath.Join(PacksDir(configDir), "dev-pack")
	mcpPath := filepath.Join(packRoot, "mcp", "demoserver.json")
	if err := writeTestFile(mcpPath, `{}`); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{
		Packs: map[string]config.InstalledPackMeta{
			"dev-pack": {Method: config.MethodLink, Origin: packRoot},
		},
	}); err != nil {
		t.Fatal(err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpdateDeepIndex(index.PackInfo{
		Name:   "dev-pack",
		Source: "deep-index",
	}, []index.Resource{
		{Kind: "mcp", Name: "demoserver", Path: "mcp/demoserver.json"},
	}); err != nil {
		t.Fatal(err)
	}

	results, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "demoserver",
		Status:    "installed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one installed result", results)
	}
	if !results[0].Installed || results[0].Status != "installed" {
		t.Fatalf("result was not hydrated as installed: %+v", results[0])
	}
	if results[0].Path != mcpPath {
		t.Fatalf("path = %q, want %q", results[0].Path, mcpPath)
	}
}

func TestRunIndexSearchAvailableFilterExcludesHydratedInstalledRows(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	packRoot := filepath.Join(PacksDir(configDir), "dev-pack")
	if err := writeTestFile(filepath.Join(packRoot, "agents", "demoagent.md"), "---\n---\n"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{
		Packs: map[string]config.InstalledPackMeta{
			"dev-pack": {Method: config.MethodLink, Origin: packRoot},
		},
	}); err != nil {
		t.Fatal(err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpdateDeepIndex(index.PackInfo{
		Name:   "dev-pack",
		Source: "deep-index",
	}, []index.Resource{
		{Kind: "agent", Name: "demoagent", Path: "agents/demoagent.md"},
	}); err != nil {
		t.Fatal(err)
	}

	available := false
	results, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "demoagent",
		Installed: &available,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("available results leaked installed row: %+v", results)
	}
}

func TestPackIndexDetailsIncludesInspectedResources(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpdateInspectedIndex(index.PackInfo{
		Name:        "preview-pack",
		Version:     "1.2.3",
		Description: "Previewable pack",
		Repo:        "https://example.com/preview-pack.git",
		Ref:         "main",
		Path:        "packs/preview",
		Owner:       "platform",
		Contact:     "#platform",
	}, []index.Resource{
		{
			Kind:        "workflow",
			Name:        "release-check",
			Description: "Release checklist",
			Path:        "workflows/release-check.md",
			Category:    "ops",
			Body:        "Check release state before shipping.",
		},
	}); err != nil {
		t.Fatal(err)
	}

	details, err := PackIndexDetails(configDir)
	if err != nil {
		t.Fatalf("PackIndexDetails: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("details = %+v, want one pack", details)
	}
	got := details[0]
	if got.Name != "preview-pack" || got.Status != "inspected" || got.Version != "1.2.3" {
		t.Fatalf("unexpected pack detail: %+v", got)
	}
	if got.Repo != "https://example.com/preview-pack.git" || got.Ref != "main" || got.Path != "packs/preview" {
		t.Fatalf("source coordinates not preserved: %+v", got)
	}
	if len(got.Resources) != 1 || got.Resources[0].Name != "release-check" {
		t.Fatalf("indexed resources missing: %+v", got.Resources)
	}
}
