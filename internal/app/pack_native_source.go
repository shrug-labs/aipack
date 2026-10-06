package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

func nativeConverterVersion(manifest config.PackManifest) int {
	if manifest.NativePlugin == nil {
		return 0
	}
	return manifest.NativePlugin.ConverterVersion
}

func relativePluginGitSource(plugin *domain.PluginSource, repo string) bool {
	return plugin != nil && plugin.Format != "claude" && strings.HasPrefix(repo, "./")
}

// Keep temporary marketplace paths out of registry entries and installed origins.
func withPluginGitRepository(ctx context.Context, configDir string, plugin *domain.PluginSource, repo string, runGit func(context.Context, ...string) error, use func(string) error) error {
	if !relativePluginGitSource(plugin, repo) {
		return use(repo)
	}
	if !config.RegistrySourceUsesGit(config.RegistrySourceEntry{URL: plugin.MarketplaceURL, Ref: plugin.MarketplaceRef, Path: plugin.MarketplacePath}) {
		return fmt.Errorf("relative git source requires recorded Git marketplace coordinates")
	}
	path := strings.TrimPrefix(repo, "./")
	if path != "" && (!filepath.IsLocal(path) || strings.Contains(path, "\\") || strings.Contains("/"+path+"/", "/../")) {
		return fmt.Errorf("relative git source escapes the marketplace root")
	}
	root, err := makePackTempDir(configDir, "plugin-marketplace-*")
	if err != nil {
		return err
	}
	defer util.RemoveOwnedTree(root)
	if runGit == nil {
		runGit = source.RunGit
	}
	// ponytail: acquire the parent per operation; cache it if clone cost becomes material.
	if err := source.EnsureCloneWithRef(ctx, plugin.MarketplaceURL, root, plugin.MarketplaceRef, source.CacheRefDir(configDir, plugin.MarketplaceURL), runGit); err != nil {
		return fmt.Errorf("acquiring relative git source marketplace: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	boundary, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("resolving marketplace root: %w", err)
	}
	abs, err = filepath.EvalSymlinks(filepath.Join(boundary, path))
	if err != nil {
		return fmt.Errorf("resolving relative git source: %w", err)
	}
	if !util.IsWithinDir(abs, boundary) {
		return fmt.Errorf("relative git source escapes the marketplace root")
	}
	return use(source.FileURL(abs))
}

func clonePluginGitRepository(ctx context.Context, configDir string, plugin *domain.PluginSource, repo, dst, ref string, runGit func(context.Context, ...string) error) error {
	if runGit == nil {
		runGit = source.RunGit
	}
	return withPluginGitRepository(ctx, configDir, plugin, repo, runGit, func(actual string) error {
		return source.EnsureCloneWithRef(ctx, actual, dst, ref, source.CacheRefDir(configDir, actual), runGit)
	})
}

func pluginGitTags(ctx context.Context, configDir string, plugin *domain.PluginSource, repo string, runGit func(context.Context, ...string) error, list func(context.Context, string) ([]string, error)) ([]string, error) {
	var tags []string
	err := withPluginGitRepository(ctx, configDir, plugin, repo, runGit, func(actual string) error {
		var err error
		tags, err = list(ctx, actual)
		return err
	})
	return tags, err
}

func pluginGitHead(ctx context.Context, configDir string, plugin *domain.PluginSource, repo, ref string, runGit func(context.Context, ...string) error, read func(context.Context, string, string) (string, error)) (string, error) {
	var hash string
	err := withPluginGitRepository(ctx, configDir, plugin, repo, runGit, func(actual string) error {
		var err error
		hash, err = read(ctx, actual, ref)
		return err
	})
	return hash, err
}

func pluginSourceAtRevision(source *domain.PluginSource, revision string) *domain.PluginSource {
	if source == nil {
		return nil
	}
	resolved := *source
	resolved.SourceRevision = revision
	return &resolved
}

func installedPluginSource(source *domain.PluginSource, manifest config.PackManifest) *domain.PluginSource {
	if source == nil || manifest.NativePlugin == nil {
		return source
	}
	resolved := *source
	resolved.Format = manifest.NativePlugin.Format
	resolved.Entry = manifest.NativePlugin.MarketplaceEntry
	resolved.MarketplaceMetadata = manifest.NativePlugin.MarketplaceMetadata
	resolved.CatalogCommandOrder = slices.Clone(manifest.NativePlugin.CatalogCommandOrder)
	return &resolved
}

// Colocated catalogs follow the payload revision. Separate catalogs retain their
// own source/ref; an immutable payload pin freezes its recorded catalog entry.
func refreshPluginCatalog(ctx context.Context, meta config.InstalledPackMeta, root, ref string, uctx packUpdateContext) (config.InstalledPackMeta, error) {
	old := meta.Plugin
	if old == nil || old.MarketplaceURL == "" {
		return meta, nil
	}
	separate := meta.Method != config.MethodClone || old.MarketplaceURL != meta.Origin
	if separate {
		if isPinned(meta) && ref == meta.Ref {
			return meta, nil
		}
		ref = old.MarketplaceRef
		root = old.MarketplaceURL
	}
	var data []byte
	var err error
	if separate && old.MarketplacePath == "" && source.IsHTTPURL(root) {
		data, err = config.FetchRegistryFromURL(ctx, root)
	} else {
		if old.MarketplacePath == "" {
			return meta, nil
		}
		if !filepath.IsLocal(old.MarketplacePath) {
			return meta, fmt.Errorf("plugin catalog path escapes marketplace root")
		}
		if separate {
			if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
				if !config.RegistrySourceUsesGit(config.RegistrySourceEntry{URL: root, Ref: ref, Path: old.MarketplacePath}) {
					return meta, fmt.Errorf("recorded separate plugin catalog is not a Git repository or local marketplace root")
				}
				tmp, tmpErr := makePackTempDir(uctx.configDir, "catalog-*")
				if tmpErr != nil {
					return meta, tmpErr
				}
				defer util.RemoveOwnedTree(tmp)
				if err := source.EnsureCloneWithRef(ctx, root, tmp, ref, source.CacheRefDir(uctx.configDir, root), uctx.runGitFn); err != nil {
					return meta, fmt.Errorf("acquiring recorded plugin catalog: %w", err)
				}
				root = tmp
			}
		}
		data, err = config.ReadRepositoryFile(root, old.MarketplacePath)
		if err != nil {
			return meta, fmt.Errorf("reading recorded plugin catalog: %w", err)
		}
	}
	if err != nil {
		return meta, err
	}
	reg, err := config.ParseMarketplace(data, config.RegistrySourceEntry{URL: old.MarketplaceURL, Path: old.MarketplacePath, Ref: ref, Format: old.Format})
	if err != nil {
		return meta, err
	}
	entry, ok := reg.Packs[old.Name]
	if !ok || entry.Plugin.Marketplace != old.Marketplace {
		return meta, fmt.Errorf("recorded plugin %s@%s is absent from its catalog", old.Name, old.Marketplace)
	}
	sameSource := entry.Repo == meta.Origin && filepath.Clean(entry.Path) == filepath.Clean(meta.SubPath)
	if meta.Method == config.MethodCopy {
		sameSource = entry.Method == config.MethodCopy && canonicalPath(filepath.Join(entry.Repo, entry.Path)) == canonicalPath(meta.Origin)
	}
	if meta.Method == config.MethodNPM {
		sameSource = entry.Method == config.MethodNPM && sameNPMOrigin(old.NPM, entry.Plugin.NPM)
		entry.Plugin.NPM = old.NPM
	}
	if entry.Unsupported != "" {
		return meta, fmt.Errorf("plugin %s@%s: %s", old.Name, old.Marketplace, entry.Unsupported)
	}
	if !sameSource {
		return meta, fmt.Errorf("plugin catalog changed acquisition coordinates; reinstall to adopt the new source")
	}
	entry.Plugin.Format = old.Format
	meta.Plugin = entry.Plugin
	return meta, nil
}

func packInstallNativePath(req PackInstallRequest, source string, stdout io.Writer) error {
	if err := os.MkdirAll(packStagingDir(req.ConfigDir), 0o700); err != nil {
		return err
	}
	staging, manifest, err := extractPackSource(packStagingDir(req.ConfigDir), source, req.ContentPaths, req.Name, source, req.Plugin)
	if err != nil {
		return err
	}
	defer util.RemoveOwnedTree(staging)
	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return err
	}
	dest := filepath.Join(PacksDir(req.ConfigDir), name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	_, err = beginPackReplacement(req.ConfigDir, name, staging)
	if err != nil {
		return err
	}
	with := req.With
	if with == nil {
		with = domain.BundledAll()
	}
	meta := config.InstalledPackMeta{
		Origin: source, Method: config.MethodCopy,
		Plugin: installedPluginSource(req.Plugin, manifest), ConverterVersion: nativeConverterVersion(manifest),
	}
	if err := recordLocalPackInstall(req, name, meta, with, stdout); err != nil {
		return err
	}
	_ = indexInstalledPack(req.ConfigDir, name, dest)
	if req.Add {
		return PackAdd(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout)
	}
	return nil
}
