package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// errPackRootEscape is returned when a manifest's Root field resolves to a
// path outside the clone/download boundary.
var errPackRootEscape = fmt.Errorf("pack.json root escapes source boundary")

// packContentDirs lists directories that constitute pack content.
// Only these are extracted from a source into the installed pack.
var packContentDirs = []string{
	"rules", "agents", "workflows", "skills",
	"mcp", "configs", "prompts", "profiles",
}

// extractPackContent extracts pack content from srcRoot into a clean staging
// directory with standard layout. Two modes:
//
// Standard pack (contentPaths nil): loads pack.json from srcRoot, copies
// only content directories + pack.json + declared registries. Everything
// else (.git, tests, docs, CI) is excluded.
//
// Content_paths pack (contentPaths non-nil): no pack.json expected in source.
// Maps source directories to standard layout, discovers content, writes
// synthetic pack.json. packName is required.
//
// symlinkBoundary is the root directory for symlink resolution. For subpath
// packs this should be the clone root (not the subpath), so that symlinks
// pointing to sibling directories within the repo are resolved correctly.
// If empty, defaults to srcRoot.
//
// Returns staging directory path, loaded/synthetic manifest, error.
// The caller is responsible for moving staging to its final destination.
func extractPackContent(parentDir, srcRoot string, contentPaths map[domain.PackCategory]string, packName, symlinkBoundary string) (string, config.PackManifest, error) {
	staging, err := os.MkdirTemp(parentDir, "extract-*")
	if err != nil {
		return "", config.PackManifest{}, fmt.Errorf("creating staging dir: %w", err)
	}

	if symlinkBoundary == "" {
		symlinkBoundary = srcRoot
	}

	if contentPaths != nil {
		return extractWithContentPaths(staging, srcRoot, symlinkBoundary, contentPaths, packName)
	}
	return extractStandardPack(staging, srcRoot, symlinkBoundary)
}

func extractStandardPack(staging, srcRoot, symlinkBoundary string) (string, config.PackManifest, error) {
	cleanup := func(e error) (string, config.PackManifest, error) {
		os.RemoveAll(staging)
		return "", config.PackManifest{}, e
	}

	manifestPath := filepath.Join(srcRoot, "pack.json")
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return cleanup(fmt.Errorf("loading pack.json: %w", err))
	}

	packRoot := config.ResolvePackRoot(manifestPath, manifest.Root)
	if packRoot == "" {
		return cleanup(fmt.Errorf("could not resolve pack root"))
	}

	// Guard against a malicious Root field that escapes the source boundary.
	if !util.IsWithinDir(packRoot, symlinkBoundary) {
		return cleanup(fmt.Errorf("%w: root %q resolves outside %q", errPackRootEscape, manifest.Root, symlinkBoundary))
	}

	// Content is flattened into the staging root, so the manifest's Root
	// must be "." regardless of what the source pack declared.
	manifest.Root = "."
	if err := config.SavePackManifest(filepath.Join(staging, "pack.json"), manifest); err != nil {
		return cleanup(fmt.Errorf("writing pack.json: %w", err))
	}

	// Copy content directories that exist. Use CopyDirResolvingSymlinks
	// to handle packs with in-repo symlinks (e.g. shared rules). The
	// packRoot serves as the symlink boundary.
	for _, dir := range packContentDirs {
		src := filepath.Join(packRoot, dir)
		if err := util.CopyDirResolvingSymlinks(src, filepath.Join(staging, dir), symlinkBoundary); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return cleanup(fmt.Errorf("copying %s: %w", dir, err))
		}
	}

	// Copy declared registries (may live outside content dirs).
	for _, rel := range manifest.Registries {
		src := filepath.Join(packRoot, rel)
		dst := filepath.Join(staging, rel)
		// Guard against path traversal in registry declarations.
		if !util.IsWithinDir(src, symlinkBoundary) || !util.IsWithinDir(dst, staging) {
			return cleanup(fmt.Errorf("registry path %q escapes boundary", rel))
		}
		rd, rerr := os.ReadFile(src)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				continue
			}
			return cleanup(fmt.Errorf("reading registry %s: %w", rel, rerr))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return cleanup(err)
		}
		if err := util.WriteFileAtomicWithPerms(dst, rd, 0o700, 0o600); err != nil {
			return cleanup(fmt.Errorf("writing registry %s: %w", rel, err))
		}
	}

	return staging, manifest, nil
}

func extractWithContentPaths(staging, srcRoot, symlinkBoundary string, contentPaths map[domain.PackCategory]string, packName string) (string, config.PackManifest, error) {
	cleanup := func(e error) (string, config.PackManifest, error) {
		os.RemoveAll(staging)
		return "", config.PackManifest{}, e
	}

	for cat, rel := range contentPaths {
		if !cat.IsContentPathTarget() {
			return cleanup(fmt.Errorf("invalid content_paths key %q: valid keys are rules, agents, workflows, skills, prompts", cat))
		}
		src := filepath.Join(srcRoot, rel)
		if _, serr := os.Stat(src); serr != nil {
			return cleanup(fmt.Errorf("content_paths %s: source %q not found: %w", cat, rel, serr))
		}
		dst := filepath.Join(staging, cat.DirName())
		if err := util.CopyDirResolvingSymlinks(src, dst, symlinkBoundary); err != nil {
			return cleanup(fmt.Errorf("copying %s from %q: %w", cat, rel, err))
		}
	}

	// Discover content from standard layout and write synthetic manifest.
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          packName,
		Version:       "0.1.0",
		Root:          ".",
	}
	if err := config.DiscoverContent(&manifest, staging); err != nil {
		return cleanup(fmt.Errorf("discovering extracted content: %w", err))
	}
	if err := config.SavePackManifest(filepath.Join(staging, "pack.json"), manifest); err != nil {
		return cleanup(fmt.Errorf("writing pack.json: %w", err))
	}

	return staging, manifest, nil
}
