package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
)

// PackCreateRequest describes a pack scaffolding request.
type PackCreateRequest struct {
	Dir  string // directory to create
	Name string // pack name (defaults to dir basename)
}

// PackCreate scaffolds a new pack directory with all capability vector subdirs.
func PackCreate(req PackCreateRequest) error {
	dir := req.Dir
	if dir == "" {
		return fmt.Errorf("pack directory is required")
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(dir)
	}

	// Error if pack.json already exists.
	manifestPath := filepath.Join(dir, "pack.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return fmt.Errorf("pack.json already exists: %s", manifestPath)
	}

	// Create all capability vector dirs.
	dirs := []string{
		filepath.Join(dir, "rules"),
		filepath.Join(dir, "agents"),
		filepath.Join(dir, "workflows"),
		filepath.Join(dir, "skills"),
		filepath.Join(dir, "prompts"),
		filepath.Join(dir, "mcp"),
		filepath.Join(dir, "configs"),
		filepath.Join(dir, "profiles"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
		// Create .gitkeep so empty dirs are tracked.
		gitkeep := filepath.Join(d, ".gitkeep")
		if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
			if err := os.WriteFile(gitkeep, nil, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", gitkeep, err)
			}
		}
	}

	// Write a seed profile that references this pack by name.
	profileRel := filepath.Join("profiles", name+".yaml")
	profilePath := filepath.Join(dir, profileRel)
	profileContent := []byte("schema_version: 2\npacks:\n  - name: " + name + "\n")
	if err := os.WriteFile(profilePath, profileContent, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", profilePath, err)
	}

	// Write a seed registry so authors can list related packs for discovery.
	registryRel := "registry.yaml"
	registryPath := filepath.Join(dir, registryRel)
	registryContent := []byte("schema_version: 1\npacks: {}\n")
	if err := os.WriteFile(registryPath, registryContent, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", registryPath, err)
	}

	// Content vector fields are intentionally nil so that DiscoverContent
	// auto-discovers them from the directory structure at sync time.
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          name,
		Version:       "0.1.0",
		Root:          ".",
		Profiles:      []string{profileRel},
		Registries:    []string{registryRel},
	}
	if err := config.SavePackManifest(manifestPath, manifest); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}
