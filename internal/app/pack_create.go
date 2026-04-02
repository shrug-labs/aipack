package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// PackCreateRequest describes a pack scaffolding request.
type PackCreateRequest struct {
	Name           string                         // pack name (required)
	ConfigDir      string                         // aipack config directory (required)
	Local          bool                           // when true, create inside packs dir; otherwise create in CWD and symlink
	WorkDir        string                         // working directory for non-local creates (defaults to os.Getwd if empty)
	ContentSources map[domain.PackCategory]string // local absolute paths; replaces scaffold dirs with symlinks
}

// PackCreate scaffolds a new pack directory, installs it into the packs
// directory, and registers it in sync-config. By default the pack is created
// in the current working directory and symlinked into packs/ (--link).
// With Local=true, it is created directly inside packs/.
func PackCreate(req PackCreateRequest) error {
	if req.Name == "" {
		return fmt.Errorf("pack name is required")
	}
	if err := validatePackName(req.Name); err != nil {
		return err
	}
	if req.ConfigDir == "" {
		return fmt.Errorf("config dir is required")
	}

	packsDir := PacksDir(req.ConfigDir)
	destDir := filepath.Join(packsDir, req.Name)

	// Determine where the pack content lives on disk.
	var contentDir string
	var method string
	if req.Local {
		contentDir = destDir
		method = config.MethodLocal
	} else {
		workDir := req.WorkDir
		if workDir == "" {
			var err error
			workDir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
		}
		contentDir = filepath.Join(workDir, req.Name)
		method = config.MethodLink
	}

	// Error if pack.json already exists at the content location.
	manifestPath := filepath.Join(contentDir, "pack.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return fmt.Errorf("pack.json already exists: %s", manifestPath)
	}

	// Error if the destination already exists in the packs directory.
	if !req.Local {
		if _, err := os.Stat(destDir); err == nil {
			return fmt.Errorf("pack %q already exists in %s", req.Name, packsDir)
		}
	}

	// Scaffold the pack directory.
	dirs := []string{
		filepath.Join(contentDir, "rules"),
		filepath.Join(contentDir, "agents"),
		filepath.Join(contentDir, "workflows"),
		filepath.Join(contentDir, "skills"),
		filepath.Join(contentDir, "prompts"),
		filepath.Join(contentDir, "mcp"),
		filepath.Join(contentDir, "configs"),
		filepath.Join(contentDir, "profiles"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
		gitkeep := filepath.Join(d, ".gitkeep")
		if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
			if err := os.WriteFile(gitkeep, nil, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", gitkeep, err)
			}
		}
	}

	// Replace scaffolded dirs with symlinks to content sources.
	for cat, srcPath := range req.ContentSources {
		if _, err := os.Stat(srcPath); err != nil {
			return fmt.Errorf("--%s source %q not found: %w", cat.DirName(), srcPath, err)
		}
		targetDir := filepath.Join(contentDir, cat.DirName())
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("removing scaffold dir %s: %w", cat.DirName(), err)
		}
		if err := os.Symlink(srcPath, targetDir); err != nil {
			return fmt.Errorf("symlinking %s to %s: %w", cat.DirName(), srcPath, err)
		}
	}

	// Write a seed profile that references this pack by name.
	profileRel := "profiles/" + req.Name + ".yaml"
	profilePath := filepath.Join(contentDir, "profiles", req.Name+".yaml")
	profileContent := []byte("schema_version: 2\npacks:\n  - name: " + req.Name + "\n")
	if err := os.WriteFile(profilePath, profileContent, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", profilePath, err)
	}

	// Write a seed registry.
	registryRel := "registry.yaml"
	registryPath := filepath.Join(contentDir, registryRel)
	registryContent := []byte("schema_version: 1\npacks: {}\n")
	if err := os.WriteFile(registryPath, registryContent, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", registryPath, err)
	}

	// Content vector fields are intentionally nil so that DiscoverContent
	// auto-discovers them from the directory structure at sync time.
	// When content sources are provided, discover now so the manifest
	// reflects what's actually linked.
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          req.Name,
		Version:       "0.1.0",
		Root:          ".",
		Profiles:      []string{profileRel},
		Registries:    []string{registryRel},
	}
	if len(req.ContentSources) > 0 {
		if err := config.DiscoverContent(&manifest, contentDir); err != nil {
			return fmt.Errorf("discovering content: %w", err)
		}
	}
	if err := config.SavePackManifest(manifestPath, manifest); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}

	// Link into the packs directory when content lives elsewhere.
	if !req.Local {
		if err := os.MkdirAll(packsDir, 0o700); err != nil {
			return fmt.Errorf("creating packs directory: %w", err)
		}
		if err := createLink(contentDir, destDir); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
	}

	// Register in sync-config.
	if err := packRecordOrigin(req.ConfigDir, req.Name, config.InstalledPackMeta{
		Origin:      contentDir,
		Method:      method,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return fmt.Errorf("registering pack: %w", err)
	}

	return nil
}
