package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
)

func TestPackInspectLocalPathIndexesAsInspectedWithoutInstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifest(t, packDir, "preview-pack")
	writeFile(t, filepath.Join(packDir, "rules", "safety.md"), "---\nname: safety\ndescription: Safety checks\n---\n\nInspect before installing.\n")

	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     packDir,
	})
	if err != nil {
		t.Fatalf("PackInspect: %v", err)
	}
	if result.Name != "preview-pack" {
		t.Fatalf("Name = %q, want preview-pack", result.Name)
	}
	if result.SourceType != "path" {
		t.Fatalf("SourceType = %q, want path", result.SourceType)
	}
	if result.Counts.Rules != 1 {
		t.Fatalf("rules count = %d, want 1", result.Counts.Rules)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "preview-pack")); !os.IsNotExist(err) {
		t.Fatalf("inspect must not install pack, stat err=%v", err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("installing", index.SearchFilters{Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "preview-pack" || results[0].Status != "inspected" {
		t.Fatalf("search inspected results = %+v", results)
	}
}

func TestPackInspectLocalDirectoryWithArchiveSuffixInspectsAsPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "preview.zip")
	writePackManifest(t, packDir, "suffix-preview")

	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     packDir,
	})
	if err != nil {
		t.Fatalf("PackInspect directory with archive suffix: %v", err)
	}
	if result.SourceType != "path" {
		t.Fatalf("SourceType = %q, want path", result.SourceType)
	}
	if result.Method != config.MethodLocal {
		t.Fatalf("Method = %q, want %q", result.Method, config.MethodLocal)
	}
}

func TestPackInspectRegistryNameUsesRegistrySource(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	sourceDir := t.TempDir()
	writePackManifest(t, sourceDir, "registry-pack")
	writeFile(t, filepath.Join(sourceDir, "skills", "assist", "SKILL.md"), "---\nname: assist\ndescription: Assist skill\n---\n\nAssist body.\n")
	writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
		"registry-pack": {Repo: sourceDir},
	})

	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     "registry-pack",
	})
	if err != nil {
		t.Fatalf("PackInspect registry: %v", err)
	}
	if result.SourceType != "registry" {
		t.Fatalf("SourceType = %q, want registry", result.SourceType)
	}
	if result.Counts.Skills != 1 {
		t.Fatalf("skills count = %d, want 1", result.Counts.Skills)
	}
	if result.Registry == nil || result.Registry.Name != "registry-pack" {
		t.Fatalf("registry metadata = %+v", result.Registry)
	}
}

func TestPackInspectWarnsForMCPServers(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifest(t, packDir, "tool-pack")
	writeFile(t, filepath.Join(packDir, "mcp", "tools.json"), `{
  "name": "tools",
  "transport": "stdio",
  "command": ["tool"],
  "available_tools": ["search"]
}`)

	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     packDir,
	})
	if err != nil {
		t.Fatalf("PackInspect: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one warning", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "MCP servers") || !strings.Contains(result.Warnings[0], "tools") {
		t.Fatalf("warning should mention MCP server name, got %q", result.Warnings[0])
	}
}

func TestPackInspectArchiveURLIndexesWithoutInstall(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/pack.json":      `{"schema_version":2,"name":"archive-preview","version":"1.0.0","root":"."}`,
		"repo-main/prompts/run.md": "Archive prompt body.\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	configDir := t.TempDir()
	archiveURL := srv.URL + "/pack.zip"
	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     archiveURL,
		Archive:   true,
	})
	if err != nil {
		t.Fatalf("PackInspect archive: %v", err)
	}
	if result.Name != "archive-preview" {
		t.Fatalf("Name = %q, want archive-preview", result.Name)
	}
	if result.Method != config.MethodArchive {
		t.Fatalf("Method = %q, want %q", result.Method, config.MethodArchive)
	}
	if result.Path != archiveURL {
		t.Fatalf("Path = %q, want source URL", result.Path)
	}
	if result.Counts.Prompts != 1 {
		t.Fatalf("prompts count = %d, want 1", result.Counts.Prompts)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "archive-preview")); !os.IsNotExist(err) {
		t.Fatalf("inspect must not install archive pack, stat err=%v", err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("Archive", index.SearchFilters{Kind: "prompt", Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Kind != "prompt" || results[0].Status != "inspected" {
		t.Fatalf("search inspected archive results = %+v", results)
	}
}

func TestPackInspectArchiveURLContentPathsWithoutPackJSON(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/material/prompts/run.md": "Archive prompt body.\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	configDir := t.TempDir()
	archiveURL := srv.URL + "/content.zip"
	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     archiveURL,
		Archive:   true,
		Name:      "archive-preview",
		ContentPaths: map[domain.PackCategory]string{
			domain.CategoryPrompts: "material/prompts",
		},
	})
	if err != nil {
		t.Fatalf("PackInspect archive content_paths: %v", err)
	}
	if result.Name != "archive-preview" {
		t.Fatalf("Name = %q, want archive-preview", result.Name)
	}
	if result.Counts.Prompts != 1 {
		t.Fatalf("prompts count = %d, want 1", result.Counts.Prompts)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("Archive", index.SearchFilters{Kind: "prompt", Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "archive-preview" || results[0].Status != "inspected" {
		t.Fatalf("search inspected archive content_path results = %+v", results)
	}
}

func TestPackInspectIndexesNestedContentResources(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifest(t, packDir, "nested-preview")
	writeFile(t, filepath.Join(packDir, "rules", "team", "safety.md"),
		"---\ndescription: Nested safety rule\n---\nNested rule body.\n")

	result, err := PackInspect(context.Background(), PackInspectRequest{
		ConfigDir: configDir,
		Input:     packDir,
	})
	if err != nil {
		t.Fatalf("PackInspect: %v", err)
	}
	if result.Counts.Rules != 1 {
		t.Fatalf("rules count = %d, want 1", result.Counts.Rules)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("Nested", index.SearchFilters{Kind: "rule", Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "nested-preview" || results[0].Name != "team/safety" {
		t.Fatalf("nested inspected resource missing or wrong name: %+v", results)
	}
}
