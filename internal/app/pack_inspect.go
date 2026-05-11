package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

// PackInspectRequest holds inputs for read-only pack inspection.
type PackInspectRequest struct {
	ConfigDir    string
	Input        string
	URL          string
	Archive      bool
	RegistryPath string
	Name         string
	Ref          string
	SubPath      string
	ContentPaths map[domain.PackCategory]string
	RunGitFn     func(ctx context.Context, args ...string) error
}

// PackInspectResult describes a pack source without installing it.
type PackInspectResult struct {
	Name       string               `json:"name"`
	Version    string               `json:"version,omitempty"`
	Path       string               `json:"path,omitempty"`
	Source     string               `json:"source"`
	SourceType string               `json:"source_type"`
	Status     string               `json:"status"`
	Method     string               `json:"method,omitempty"`
	Ref        string               `json:"ref,omitempty"`
	SubPath    string               `json:"path_in_source,omitempty"`
	Counts     ContentCounts        `json:"counts"`
	Rules      []string             `json:"rules"`
	Agents     []string             `json:"agents"`
	Workflows  []string             `json:"workflows"`
	Skills     []string             `json:"skills"`
	Plugins    []string             `json:"plugins"`
	Prompts    []string             `json:"prompts"`
	MCPServers []string             `json:"mcp_servers"`
	Profiles   []string             `json:"profiles,omitempty"`
	Registries []string             `json:"registries,omitempty"`
	Extras     []string             `json:"extras,omitempty"`
	Warnings   []string             `json:"warnings,omitempty"`
	Registry   *PackInspectRegistry `json:"registry,omitempty"`
}

type PackInspectRegistry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Contact     string `json:"contact,omitempty"`
	Quiet       bool   `json:"quiet,omitempty"`
}

// PackInspect reads a local, registry, clone, or archive pack source without
// installing it. The discovered resources are written to the search index with
// status=inspected so users can search a previewed pack before trusting it.
func PackInspect(ctx context.Context, req PackInspectRequest) (PackInspectResult, error) {
	if req.ConfigDir == "" {
		return PackInspectResult{}, fmt.Errorf("config dir is required")
	}
	if req.URL != "" {
		if !req.Archive && source.IsHTTPArchiveURL(req.URL) {
			req.Archive = true
		}
		return inspectURL(ctx, req, "url", nil)
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		return PackInspectResult{}, fmt.Errorf("pack name, path, or URL is required")
	}
	if pathLooksLocal(input) {
		if req.Archive || localPathLooksLikeArchiveFile(input) {
			req.URL = input
			req.Archive = true
			return inspectArchive(ctx, req, "path", nil)
		}
		return inspectPath(req, input, "path", nil)
	}
	if source.LooksLikeURL(input) {
		req.URL = input
		if !req.Archive && source.IsHTTPArchiveURL(input) {
			req.Archive = true
		}
		return inspectURL(ctx, req, "url", nil)
	}

	entry, err := RegistryLookup(RegistryListRequest{ConfigDir: req.ConfigDir, RegistryPath: req.RegistryPath}, input)
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("registry lookup for %q: %w", input, err)
	}
	registryInfo := &PackInspectRegistry{
		Name:        input,
		Description: entry.Description,
		Owner:       entry.Owner,
		Contact:     entry.Contact,
		Quiet:       entry.Quiet,
	}
	if req.Name == "" {
		req.Name = input
	}
	if req.Ref == "" {
		req.Ref = entry.Ref
	}
	if req.SubPath == "" {
		req.SubPath = entry.Path
	}
	if len(req.ContentPaths) == 0 {
		req.ContentPaths = entry.ContentPaths
	}
	if entry.Method == config.MethodArchive {
		req.URL = entry.URL
		req.Archive = true
		return inspectURL(ctx, req, "registry", registryInfo)
	}
	if pathLooksLocal(entry.Repo) {
		return inspectPath(req, registryLocalPath(entry.Repo, entry.Path), "registry", registryInfo)
	}
	req.URL = entry.Repo
	return inspectURL(ctx, req, "registry", registryInfo)
}

func inspectPath(req PackInspectRequest, rawPath, sourceType string, registry *PackInspectRegistry) (PackInspectResult, error) {
	packPath, err := filepath.Abs(rawPath)
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("resolving pack path: %w", err)
	}
	packDir := packPath
	if st, err := os.Stat(packPath); err == nil && !st.IsDir() {
		if filepath.Base(packPath) != "pack.json" {
			return PackInspectResult{}, fmt.Errorf("pack path is a file but not pack.json: %s", packPath)
		}
		packDir = filepath.Dir(packPath)
	}
	boundary := util.FindRepoRoot(packDir)
	if boundary == "" {
		boundary = packDir
	}
	if err := os.MkdirAll(packStagingDir(req.ConfigDir), 0o700); err != nil {
		return PackInspectResult{}, fmt.Errorf("creating staging dir: %w", err)
	}
	staging, manifest, err := extractPackContent(packStagingDir(req.ConfigDir), packDir, req.ContentPaths, req.Name, boundary)
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("extracting pack: %w", err)
	}
	defer os.RemoveAll(staging)
	return finishPackInspect(req, staging, manifest, sourceType, packPath, config.MethodLocal, registry)
}

func inspectURL(ctx context.Context, req PackInspectRequest, sourceType string, registry *PackInspectRegistry) (PackInspectResult, error) {
	if req.Archive {
		return inspectArchive(ctx, req, sourceType, registry)
	}
	return inspectClone(ctx, req, sourceType, registry)
}

func inspectArchive(ctx context.Context, req PackInspectRequest, sourceType string, registry *PackInspectRegistry) (PackInspectResult, error) {
	archiveDir, err := makePackTempDir(req.ConfigDir, "inspect-archive-*")
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(archiveDir)
	if err := source.FetchArchive(ctx, req.URL, archiveDir, source.HTTPArchiveOptions{}); err != nil {
		return PackInspectResult{}, fmt.Errorf("fetching archive %s: %w", req.URL, err)
	}
	var packRoot string
	if req.ContentPaths != nil {
		packRoot, err = resolveArchiveContentRoot(archiveDir, req.SubPath)
	} else {
		packRoot, err = resolveArchivePackRoot(archiveDir, req.SubPath)
	}
	if err != nil {
		return PackInspectResult{}, err
	}
	staging, manifest, err := extractPackContent(packStagingDir(req.ConfigDir), packRoot, req.ContentPaths, req.Name, archiveDir)
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("extracting pack: %w", err)
	}
	defer os.RemoveAll(staging)
	return finishPackInspect(req, staging, manifest, sourceType, req.URL, config.MethodArchive, registry)
}

func inspectClone(ctx context.Context, req PackInspectRequest, sourceType string, registry *PackInspectRegistry) (PackInspectResult, error) {
	info := source.PackURLInfo{RepoURL: req.URL, Ref: req.Ref, SubPath: req.SubPath}
	if req.SubPath == "" && req.Ref == "" && !config.IsGitURL(req.URL, "") {
		probed, err := source.ProbePackURL(req.URL)
		if err != nil {
			return PackInspectResult{}, fmt.Errorf("probing URL: %w", err)
		}
		info = probed
		if req.Ref != "" {
			info.Ref = req.Ref
		}
	}
	cloneDir, err := makePackTempDir(req.ConfigDir, "inspect-clone-*")
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(cloneDir)
	gitFn := req.RunGitFn
	if gitFn == nil {
		gitFn = source.RunGit
	}
	if err := source.EnsureCloneWithRef(ctx, info.RepoURL, cloneDir, info.Ref, source.CacheRefDir(req.ConfigDir, info.RepoURL), gitFn); err != nil {
		return PackInspectResult{}, fmt.Errorf("cloning %s: %w", info.RepoURL, err)
	}
	packRoot := cloneDir
	if info.SubPath != "" {
		packRoot = filepath.Join(cloneDir, info.SubPath)
	}
	staging, manifest, err := extractPackContent(packStagingDir(req.ConfigDir), packRoot, req.ContentPaths, req.Name, cloneDir)
	if err != nil {
		return PackInspectResult{}, fmt.Errorf("extracting pack: %w", err)
	}
	defer os.RemoveAll(staging)
	req.Ref = info.Ref
	req.SubPath = info.SubPath
	return finishPackInspect(req, staging, manifest, sourceType, info.RepoURL, config.MethodClone, registry)
}

func finishPackInspect(req PackInspectRequest, staging string, manifest config.PackManifest, sourceType, sourceValue, method string, registry *PackInspectRegistry) (PackInspectResult, error) {
	if err := config.DiscoverContent(&manifest, staging); err != nil {
		return PackInspectResult{}, fmt.Errorf("discovering content: %w", err)
	}
	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return PackInspectResult{}, err
	}
	resources := resourcesFromManifestRoot(name, staging, manifest)
	info := index.PackInfo{
		Name:        name,
		Version:     manifest.Version,
		Description: registryDescription(registry),
		Repo:        sourceValue,
		Ref:         req.Ref,
		Path:        req.SubPath,
		Owner:       registryOwner(registry),
		Contact:     registryContact(registry),
	}
	if info.Description == "" {
		info.Description = name
	}
	if err := indexInspectedPack(req.ConfigDir, info, resources); err != nil {
		return PackInspectResult{}, err
	}
	return PackInspectResult{
		Name:       name,
		Version:    manifest.Version,
		Path:       sourceValue,
		Source:     sourceValue,
		SourceType: sourceType,
		Status:     "inspected",
		Method:     method,
		Ref:        req.Ref,
		SubPath:    req.SubPath,
		Counts: ContentCounts{
			Rules:     len(manifest.Rules),
			Skills:    len(manifest.Skills),
			Workflows: len(manifest.Workflows),
			Agents:    len(manifest.Agents),
			Plugins:   len(manifest.Plugins),
			Prompts:   len(manifest.Prompts),
			MCP:       len(manifest.MCP),
		},
		Rules:      nonNilStrings(manifest.Rules),
		Agents:     nonNilStrings(manifest.Agents),
		Workflows:  nonNilStrings(manifest.Workflows),
		Skills:     nonNilStrings(manifest.Skills),
		Plugins:    nonNilStrings(manifest.Plugins),
		Prompts:    nonNilStrings(manifest.Prompts),
		MCPServers: nonNilStrings(manifest.MCP),
		Profiles:   manifest.Profiles,
		Registries: manifest.Registries,
		Extras:     manifest.Extras,
		Warnings:   inspectWarnings(manifest),
		Registry:   registry,
	}, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func inspectWarnings(manifest config.PackManifest) []string {
	if len(manifest.MCP) == 0 {
		return nil
	}
	servers := slices.Clone(manifest.MCP)
	slices.Sort(servers)
	return []string{
		fmt.Sprintf("defines MCP servers (external tool access): %s", strings.Join(servers, ", ")),
	}
}

func indexInspectedPack(configDir string, info index.PackInfo, resources []index.Resource) error {
	db, err := openIndexDB(configDir, config.HomeDir())
	if err != nil {
		return err
	}
	defer db.Close()
	return db.UpdateInspectedIndex(info, resources)
}

// PackInspectClearRequest holds inputs for the inspected-rows clear path.
type PackInspectClearRequest struct {
	ConfigDir string
}

// PackInspectClearResult reports how many inspected packs were removed.
type PackInspectClearResult struct {
	Removed int64 `json:"removed"`
}

// PackInspectClear wipes every inspected pack from the search index. Other
// discovery sources (sync, registry, deep-index) are untouched.
func PackInspectClear(req PackInspectClearRequest) (PackInspectClearResult, error) {
	if req.ConfigDir == "" {
		return PackInspectClearResult{}, fmt.Errorf("config dir is required")
	}
	db, err := openIndexDB(req.ConfigDir, config.HomeDir())
	if err != nil {
		return PackInspectClearResult{}, err
	}
	defer db.Close()
	removed, err := db.ClearInspected()
	if err != nil {
		return PackInspectClearResult{}, err
	}
	return PackInspectClearResult{Removed: removed}, nil
}

func resourcesFromManifestRoot(packName, root string, manifest config.PackManifest) []index.Resource {
	resources := indexManifestContent(packName, manifest, root, nil)
	for _, jd := range []struct {
		cat     domain.PackCategory
		extract func(string) (index.Resource, error)
	}{
		{domain.CategoryPlugins, extractPluginFromFile},
		{domain.CategoryMCP, extractMCPServerFromFile},
	} {
		for _, id := range manifest.ContentIDs(jd.cat) {
			rel := filepath.ToSlash(manifest.RelPath(jd.cat, id))
			if r, err := jd.extract(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				r.Name = id
				r.Path = rel
				resources = append(resources, r)
			}
		}
	}
	return resources
}

func pathLooksLocal(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func registryLocalPath(repo, subPath string) string {
	if subPath == "" {
		return repo
	}
	return filepath.Join(repo, filepath.FromSlash(subPath))
}

func registryDescription(reg *PackInspectRegistry) string {
	if reg == nil {
		return ""
	}
	return reg.Description
}

func registryOwner(reg *PackInspectRegistry) string {
	if reg == nil {
		return ""
	}
	return reg.Owner
}

func registryContact(reg *PackInspectRegistry) string {
	if reg == nil {
		return ""
	}
	return reg.Contact
}
