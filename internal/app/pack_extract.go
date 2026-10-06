package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// errPackRootEscape is returned when a manifest's Root field resolves to a
// path outside the clone/download boundary.
var errPackRootEscape = fmt.Errorf("pack.json root escapes source boundary")

// packContentDirs lists directories that constitute pack content.
// Only these are extracted from a source into the installed pack.
var packContentDirs = []string{
	"rules", "agents", "workflows", "skills", "hooks",
	"mcp", "configs", "prompts", "profiles", "registries",
}

// Acquisition feeds the same materializer for install, inspect, and update.
func extractPackSource(parentDir, srcRoot string, contentPaths map[domain.PackCategory]string, packName, boundary string, native *domain.PluginSource) (string, config.PackManifest, error) {
	if native == nil {
		return extractPackContent(parentDir, srcRoot, contentPaths, packName, boundary)
	}
	if contentPaths != nil {
		return "", config.PackManifest{}, fmt.Errorf("plugin sources cannot declare content_paths")
	}
	if native.Format != "" && native.Format != plugin.CodexLegacy && native.Format != plugin.AgentPlugins && native.Format != plugin.Claude {
		return "", config.PackManifest{}, fmt.Errorf("unsupported plugin format %q", native.Format)
	}
	if boundary == "" {
		boundary = srcRoot
	}
	resolved, err := filepath.EvalSymlinks(srcRoot)
	if err != nil || !util.IsWithinDir(resolved, canonicalPath(boundary)) {
		return "", config.PackManifest{}, fmt.Errorf("plugin root escapes source boundary: %s", srcRoot)
	}
	format := native.Format
	if format == "" {
		if _, err := os.Stat(filepath.Join(srcRoot, ".claude-plugin/plugin.json")); err == nil {
			if util.PathExists(filepath.Join(srcRoot, ".codex-plugin/plugin.json")) || util.PathExists(filepath.Join(srcRoot, "plugin.json")) {
				return "", config.PackManifest{}, fmt.Errorf("plugin declares both Claude and Codex manifests; its source must specify a format")
			}
			format = plugin.Claude
		}
	}
	if native.NPM != nil && format == plugin.Claude && native.Format != plugin.Claude {
		return "", config.PackManifest{}, fmt.Errorf("npm imports require a Codex plugin format unless the source explicitly declares Claude")
	}
	if format != plugin.Claude {
		if err := config.ValidateCodexMarketplacePolicy(native.Entry); err != nil {
			return "", config.PackManifest{}, err
		}
	}
	staging, err := os.MkdirTemp(parentDir, "extract-*")
	if err != nil {
		return "", config.PackManifest{}, err
	}
	var m config.PackManifest
	if format == plugin.Claude {
		m, err = plugin.MaterializeClaude(srcRoot, staging, packName, *native)
	} else {
		m, err = plugin.MaterializeCodex(srcRoot, staging, packName, native.Marketplace)
	}
	if err == nil {
		m.NativePlugin.Name = native.Name
		m.NativePlugin.MarketplaceEntry = native.Entry
		m.NativePlugin.MarketplaceMetadata = native.MarketplaceMetadata
		err = config.SavePackManifest(filepath.Join(staging, "pack.json"), m)
	}
	if err == nil {
		for _, finding := range config.ValidatePackRoot(staging) {
			if finding.Severity == config.FindingSeverityError {
				err = fmt.Errorf("invalid converted plugin: %s", finding.String())
				break
			}
		}
	}
	if err != nil {
		util.RemoveOwnedTree(staging)
		return "", config.PackManifest{}, err
	}
	return staging, m, nil
}

// extractPackContent extracts pack content from srcRoot into a clean staging
// directory with standard layout. Two modes:
//
// Standard pack (contentPaths nil): loads pack.json from srcRoot, copies
// only content directories + pack.json + declared extras. Everything
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
	if manifest.NativePlugin != nil {
		files, err := plugin.ReadFiles(filepath.Join(packRoot, "upstream"))
		if err != nil {
			return cleanup(err)
		}
		if err := plugin.WriteFiles(filepath.Join(staging, "upstream"), files); err != nil {
			return cleanup(err)
		}
		if err := config.SavePackManifest(filepath.Join(staging, "pack.json"), manifest); err != nil {
			return cleanup(err)
		}
		return staging, manifest, nil
	}
	// Validate the source before extraction can omit unsupported directories.
	if err := config.DiscoverContent(&manifest, packRoot); err != nil {
		return cleanup(fmt.Errorf("discovering source content: %w", err))
	}
	origExtras := manifest.Extras

	// Validate extras entries and compute staging-relative names (leading
	// ".." segments stripped) before saving the manifest. Validation runs
	// first so structural errors fail early.
	if len(origExtras) > config.MaxExtras {
		return cleanup(fmt.Errorf("extras declares %d entries (max %d)", len(origExtras), config.MaxExtras))
	}
	ev := config.NewExtrasValidator()
	rewritten := make([]string, 0, len(origExtras))
	for _, ext := range origExtras {
		stagingName, err := ev.ValidateEntry(ext)
		if err != nil {
			return cleanup(err)
		}
		rewritten = append(rewritten, stagingName)
	}
	manifest.Extras = rewritten

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

	// Copy extras (files or directories) declared in the manifest.
	// Structural validation was done above; filesystem boundary and symlink
	// checks are extraction-specific.
	type resolvedEntry struct {
		realPath string
		extras   string
	}
	var resolvedSources []resolvedEntry
	for i, ext := range origExtras {
		src := filepath.Join(packRoot, ext)
		dst := filepath.Join(staging, rewritten[i])
		if !util.IsWithinDir(src, symlinkBoundary) {
			return cleanup(fmt.Errorf("extras path %q escapes repo boundary", ext))
		}
		if !util.IsWithinDir(dst, staging) {
			return cleanup(fmt.Errorf("extras path %q escapes staging directory", ext))
		}

		// Resolve symlinks to detect real-path overlaps.
		realSrc, err := filepath.EvalSymlinks(src)
		if err != nil {
			return cleanup(fmt.Errorf("resolving extras path %s: %w", ext, err))
		}
		for _, prev := range resolvedSources {
			if realSrc == prev.realPath || util.IsWithinDir(realSrc, prev.realPath) || util.IsWithinDir(prev.realPath, realSrc) {
				return cleanup(fmt.Errorf("extras path %q overlaps with %q (both resolve under %s)", ext, prev.extras, prev.realPath))
			}
		}
		resolvedSources = append(resolvedSources, resolvedEntry{realPath: realSrc, extras: ext})

		info, err := os.Lstat(realSrc)
		if err != nil {
			return cleanup(fmt.Errorf("stat extras %s: %w", ext, err))
		}
		copyAsDir := info.IsDir()
		if copyAsDir {
			if err := util.CopyDirResolvingSymlinks(src, dst, symlinkBoundary); err != nil {
				return cleanup(fmt.Errorf("copying extras dir %s: %w", ext, err))
			}
		} else {
			if err := util.CopyFileResolvingSymlink(src, dst, symlinkBoundary); err != nil {
				return cleanup(fmt.Errorf("copying extras file %s: %w", ext, err))
			}
		}
	}

	return staging, manifest, nil
}

// applyWithFilter removes bundled content (profiles, registries, extras)
// from a staging directory when not approved via --with. Core content
// (rules, agents, workflows, skills, hooks, prompts, mcp, configs) is always
// kept. Updates the manifest in-place, deletes unapproved files from
// disk, and re-saves pack.json. No-op when with is nil or when all
// bundled categories are approved.
func applyWithFilter(staging string, manifest *config.PackManifest, with domain.BundledSet) error {
	if with == nil {
		return nil
	}
	// Short-circuit when all bundled categories are approved.
	allApproved := true
	for _, cat := range domain.AllBundledCategories {
		if !with.Has(cat) {
			allApproved = false
			break
		}
	}
	if allApproved {
		return nil
	}

	// Profiles: directory-based bundled content.
	if !with.Has(domain.BundledProfiles) {
		os.RemoveAll(filepath.Join(staging, "profiles"))
		manifest.Profiles = nil
	}

	// Extras: remove individual paths listed in the manifest.
	if !with.Has(domain.BundledExtras) {
		for _, ext := range manifest.Extras {
			os.RemoveAll(filepath.Join(staging, ext))
		}
		manifest.Extras = nil
	}

	// Registries: directory-based bundled content.
	if !with.Has(domain.BundledRegistries) {
		os.RemoveAll(filepath.Join(staging, "registries"))
		manifest.Registries = nil
	}

	return config.SavePackManifest(filepath.Join(staging, "pack.json"), *manifest)
}

func extractWithContentPaths(staging, srcRoot, symlinkBoundary string, contentPaths map[domain.PackCategory]string, packName string) (string, config.PackManifest, error) {
	cleanup := func(e error) (string, config.PackManifest, error) {
		os.RemoveAll(staging)
		return "", config.PackManifest{}, e
	}

	for cat, rel := range contentPaths {
		if !cat.IsContentPathTarget() {
			return cleanup(fmt.Errorf("invalid content_paths key %q: valid keys are rules, agents, workflows, skills, hooks, prompts", cat))
		}
		src, err := resolveContentPathSource(srcRoot, rel)
		if err != nil {
			return cleanup(fmt.Errorf("content_paths %s: %w", cat, err))
		}
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

func resolveContentPathSource(srcRoot, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("source path is required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("source path %q must be relative", rel)
	}
	cleanRel := filepath.Clean(rel)
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source path %q escapes source boundary", rel)
	}
	src := filepath.Clean(filepath.Join(srcRoot, cleanRel))
	if !util.IsWithinDir(src, srcRoot) {
		return "", fmt.Errorf("source path %q escapes source boundary", rel)
	}
	return src, nil
}
