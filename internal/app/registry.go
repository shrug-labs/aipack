package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

// RegistryListRequest holds the inputs for listing/searching registry packs.
type RegistryListRequest struct {
	ConfigDir    string
	RegistryPath string // explicit override; empty = resolve from config
}

// RegistrySearchResult describes a pack found in the registry.
type RegistrySearchResult struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	config.RegistryEntry
}

// RegistryValidateResult describes semantic validation of one registry file.
type RegistryValidateResult struct {
	Path   string   `json:"path"`
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// RegistryValidate parses and validates a registry YAML file.
func RegistryValidate(path string) (RegistryValidateResult, error) {
	reg, err := config.LoadRegistry(path)
	if err != nil {
		return RegistryValidateResult{}, err
	}
	errs := config.ValidateRegistry(reg)
	return RegistryValidateResult{
		Path:   path,
		Valid:  len(errs) == 0,
		Errors: errs,
	}, nil
}

// RegistryList returns all packs in the registry, sorted by name.
func RegistryList(req RegistryListRequest) ([]RegistrySearchResult, error) {
	reg, err := loadRegistryForRequest(req)
	if err != nil {
		return nil, err
	}
	results := registryEntriesToResults(reg)
	markInstalled(results, req.ConfigDir)
	return results, nil
}

// RegistrySearch returns packs whose name or description match the query (case-insensitive substring).
func RegistrySearch(req RegistryListRequest, query string) ([]RegistrySearchResult, error) {
	reg, err := loadRegistryForRequest(req)
	if err != nil {
		return nil, err
	}
	all := registryEntriesToResults(reg)
	markInstalled(all, req.ConfigDir)
	q := strings.ToLower(query)
	var matched []RegistrySearchResult
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.Name), q) || strings.Contains(strings.ToLower(r.Description), q) {
			matched = append(matched, r)
		}
	}
	return matched, nil
}

// RegistryLookup returns the registry entry for a pack by name.
func RegistryLookup(req RegistryListRequest, name string) (config.RegistryEntry, error) {
	reg, err := loadRegistryForRequest(req)
	if err != nil {
		return config.RegistryEntry{}, err
	}
	entry, ok := reg.Packs[name]
	if !ok {
		if hint := missingRegistryPackHint(req, name); hint != "" {
			return config.RegistryEntry{}, fmt.Errorf("pack %q not found in registry. %s", name, hint)
		}
		return config.RegistryEntry{}, fmt.Errorf("pack %q not found in registry", name)
	}
	return entry, nil
}

// PackInstallRequestFromRegistryEntry maps registry metadata into the canonical
// install request shape used by CLI, TUI, and lifecycle repair paths.
func PackInstallRequestFromRegistryEntry(configDir, name string, entry config.RegistryEntry) PackInstallRequest {
	req := PackInstallRequest{
		ConfigDir:    configDir,
		Ref:          entry.Ref,
		SubPath:      entry.Path,
		Name:         name,
		Quiet:        boolPtrIf(entry.Quiet),
		ContentPaths: maps.Clone(entry.ContentPaths),
	}
	if entry.Method == config.MethodArchive {
		req.URL = entry.URL
		req.Archive = true
		req.Ref = ""
	} else {
		req.URL = entry.Repo
	}
	return req
}

func missingRegistryPackHint(req RegistryListRequest, name string) string {
	if req.RegistryPath != "" {
		return ""
	}
	switch name {
	case "aipack-core", "essentials":
		return fmt.Sprintf("Run `aipack registry fetch %s` to fetch the default public registry.", config.DefaultRegistryRepo)
	default:
		return ""
	}
}

func loadRegistryForRequest(req RegistryListRequest) (config.Registry, error) {
	if req.RegistryPath != "" {
		// Explicit path override — single file mode.
		return config.LoadRegistry(req.RegistryPath)
	}
	// Default — merged view from local + cached sources.
	return config.LoadMergedRegistry(req.ConfigDir)
}

// RegistrySourceInfo describes a configured registry source for display.
type RegistrySourceInfo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
	Cached bool   `json:"cached"`
}

// RegistrySources returns all configured registry sources.
func RegistrySources(configDir string) ([]RegistrySourceInfo, error) {
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		return nil, fmt.Errorf("loading sync-config: %w", err)
	}
	result := make([]RegistrySourceInfo, 0, len(sc.RegistrySources))
	for _, src := range sc.RegistrySources {
		cachePath := config.SourceCachePath(configDir, src.Name)
		_, statErr := os.Stat(cachePath)
		result = append(result, RegistrySourceInfo{
			Name:   src.Name,
			URL:    src.URL,
			Ref:    src.Ref,
			Path:   src.Path,
			Cached: statErr == nil,
		})
	}
	return result, nil
}

// RegistryAddSourceRequest holds the inputs for adding a registry source without fetching.
type RegistryAddSourceRequest struct {
	ConfigDir string
	URL       string
	Ref       string
	Path      string
	Name      string
}

// RegistryAddSource adds a registry source to sync-config without fetching it.
// The source can be fetched later with `registry fetch`.
func RegistryAddSource(req RegistryAddSourceRequest, stdout io.Writer) error {
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir))
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}

	isGit := config.IsGitURL(req.URL, req.Ref)
	ref := req.Ref
	filePath := req.Path
	if isGit {
		if filePath == "" {
			filePath = config.DefaultRegistryPath
		}
	}

	name := req.Name
	if name == "" {
		derived := config.DeriveSourceName(req.URL, filePath)
		name = config.UniqueSourceName(derived, req.URL, filePath, sc.RegistrySources)
	}

	upsertRegistrySource(&sc, config.RegistrySourceEntry{
		Name: name,
		URL:  req.URL,
		Ref:  ref,
		Path: filePath,
	})

	if err := config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}

	fmt.Fprintf(stdout, "Added registry source %q (%s)\n", name, req.URL)
	fmt.Fprintln(stdout, "Run 'aipack registry fetch' to fetch pack listings.")
	return nil
}

// RegistryFetchRequest holds the inputs for fetching a remote registry.
type RegistryFetchRequest struct {
	ConfigDir string
	URL       string // explicit URL; empty = fetch all known sources
	Ref       string // git ref (branch/tag); presence implies git-based fetch
	Path      string // file path within repo (git only); default: registry.yaml
	Name      string // explicit source name; empty = derive from URL

	// FetchFn overrides how bytes are fetched from an HTTP URL (for testing).
	FetchFn func(url string) ([]byte, error)

	// GitFetchFn overrides how a file is fetched via git (for testing).
	GitFetchFn func(repo, ref, path string) ([]byte, error)
}

// RegistryFetch fetches remote registries and caches them locally.
// With an explicit URL, fetches that single source and saves it to sync-config.
// Without a URL, fetches all sources in registry_sources (or the compiled-in default).
func RegistryFetch(ctx context.Context, req RegistryFetchRequest, stdout io.Writer) error {
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir))
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}

	if req.URL != "" {
		if _, err := registryFetchOne(ctx, req, &sc, stdout); err != nil {
			return err
		}
		return config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc)
	}

	// Multi-source: fetch all known sources.
	sources := sc.RegistrySources

	// Backward compat: if no sources but defaults.registry_url is set, use it.
	if len(sources) == 0 && sc.Defaults.RegistryURL != "" {
		sources = []config.RegistrySourceEntry{{
			Name: config.DeriveSourceName(sc.Defaults.RegistryURL, ""),
			URL:  sc.Defaults.RegistryURL,
		}}
	}

	// Ensure compiled-in default registries are always present so foundational
	// packs stay resolvable when users add custom registry sources.
	sources = appendMissingDefaultRegistrySources(sources, config.DefaultRegistrySources())

	var totalPacks, succeeded int
	for _, src := range sources {
		oneReq := RegistryFetchRequest{
			ConfigDir:  req.ConfigDir,
			URL:        src.URL,
			Ref:        src.Ref,
			Path:       src.Path,
			Name:       src.Name,
			FetchFn:    req.FetchFn,
			GitFetchFn: req.GitFetchFn,
		}
		n, err := registryFetchOne(ctx, oneReq, &sc, stdout)
		if err != nil {
			printRegistryWarning(stdout, src.Name, err)
			continue
		}
		succeeded++
		totalPacks += n
	}

	if succeeded == 0 {
		return fmt.Errorf("all %d registry source(s) failed to fetch", len(sources))
	}

	sc.RegistrySources = orderRegistrySources(sc.RegistrySources, sources)

	// Save sync-config once after all sources are processed.
	if err := config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}

	if len(sources) > 1 && stdout != nil {
		fmt.Fprintf(stdout, "%d source(s), %d total pack(s)\n", len(sources), totalPacks)
	}
	return nil
}

func appendMissingDefaultRegistrySources(sources, defaults []config.RegistrySourceEntry) []config.RegistrySourceEntry {
	result := slices.Clone(sources)
	for i, def := range defaults {
		if registrySourceIndex(result, def) >= 0 {
			continue
		}

		sourceToAdd := def
		sourceToAdd.Name = config.UniqueSourceName(sourceToAdd.Name, sourceToAdd.URL, sourceToAdd.Path, result)

		insertAt := len(result)
		for _, laterDefault := range defaults[i+1:] {
			if idx := registrySourceIndex(result, laterDefault); idx >= 0 && idx < insertAt {
				insertAt = idx
			}
		}
		result = slices.Insert(result, insertAt, sourceToAdd)
	}
	return result
}

func registrySourceIndex(sources []config.RegistrySourceEntry, needle config.RegistrySourceEntry) int {
	return slices.IndexFunc(sources, func(src config.RegistrySourceEntry) bool {
		return sameRegistryCoordinates(src, needle)
	})
}

func orderRegistrySources(current, planned []config.RegistrySourceEntry) []config.RegistrySourceEntry {
	result := make([]config.RegistrySourceEntry, 0, len(current))
	used := make([]bool, len(current))
	for _, want := range planned {
		for i, src := range current {
			if used[i] || !sameRegistryIdentity(src, want) {
				continue
			}
			result = append(result, src)
			used[i] = true
			break
		}
	}
	for i, src := range current {
		if !used[i] {
			result = append(result, src)
		}
	}
	return result
}

func sameRegistryCoordinates(a, b config.RegistrySourceEntry) bool {
	return a.URL == b.URL && a.Path == b.Path
}

func sameRegistryIdentity(a, b config.RegistrySourceEntry) bool {
	return a.Name == b.Name || sameRegistryCoordinates(a, b)
}

// registryFetchOne fetches a single registry source, caches it, and upserts it
// into the in-memory sync-config. The caller is responsible for saving sync-config.
// Returns the number of packs in the fetched registry.
func registryFetchOne(ctx context.Context, req RegistryFetchRequest, sc *config.SyncConfig, stdout io.Writer) (int, error) {
	url := req.URL
	ref := req.Ref
	filePath := req.Path

	// Apply defaults for git URLs. For HTTP URLs, ref and filePath stay empty —
	// the upsert writes them with omitempty, and re-fetch identifies the source
	// as HTTP because IsGitURL(url, "") returns false.
	isGit := config.IsGitURL(url, ref)
	if isGit {
		if filePath == "" {
			filePath = config.DefaultRegistryPath
		}
	}

	// Resolve source name.
	name := req.Name
	if name == "" {
		derived := config.DeriveSourceName(url, filePath)
		name = config.UniqueSourceName(derived, url, filePath, sc.RegistrySources)
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "Fetching %s from %s\n", name, url)
	}

	// Fetch remote registry bytes.
	var data []byte
	var err error

	if isGit {
		gitFetchFn := req.GitFetchFn
		if gitFetchFn == nil {
			if source.SelectFetchStrategy(url) == source.StrategyHTTPTarball {
				gitFetchFn = func(repo, gitRef, path string) ([]byte, error) {
					tarballURL, urlErr := source.GitHubTarballURL(repo, gitRef)
					if urlErr != nil {
						return nil, urlErr
					}
					return source.FetchFileHTTPTarball(ctx, tarballURL, path)
				}
			} else {
				gitFetchFn = func(repo, gitRef, path string) ([]byte, error) {
					return config.FetchFileViaGit(ctx, repo, gitRef, path)
				}
			}
		}
		data, err = gitFetchFn(url, ref, filePath)
	} else {
		fetchFn := req.FetchFn
		if fetchFn == nil {
			fetchFn = func(u string) ([]byte, error) {
				return config.FetchRegistryFromURL(ctx, u)
			}
		}
		data, err = fetchFn(url)
	}
	if err != nil {
		return 0, fmt.Errorf("fetching registry from %s: %w", url, err)
	}

	// Parse and validate.
	remote, err := config.ParseRegistry(data)
	if err != nil {
		return 0, fmt.Errorf("parsing remote registry from %s: %w", url, err)
	}

	// Write cache file.
	cacheDir := config.RegistriesCacheDir(req.ConfigDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return 0, fmt.Errorf("creating registries cache dir: %w", err)
	}
	cachePath := config.SourceCachePath(req.ConfigDir, name)
	out, err := yaml.Marshal(&remote)
	if err != nil {
		return 0, fmt.Errorf("marshalling registry: %w", err)
	}
	if err := util.WriteFileAtomicWithPerms(cachePath, out, 0o700, 0o600); err != nil {
		return 0, fmt.Errorf("writing cached registry: %w", err)
	}

	// Upsert source in sync-config (caller saves).
	upsertRegistrySource(sc, config.RegistrySourceEntry{
		Name: name,
		URL:  url,
		Ref:  ref,
		Path: filePath,
	})

	// Update search index (best-effort).
	if err := indexRegistryEntries(remote, req.ConfigDir); err != nil {
		printRegistryWarning(stdout, "index update failed", err)
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "%s: %d pack(s) (from %s)\n", name, len(remote.Packs), url)
	}
	return len(remote.Packs), nil
}

func printRegistryWarning(w io.Writer, label string, err error) {
	if w == nil {
		return
	}
	if err != nil {
		fmt.Fprintf(w, "warning: %s: %v\n", label, err)
		return
	}
	fmt.Fprintf(w, "warning: %s\n", label)
}

// upsertRegistrySource adds or updates a source in the sync-config sources list.
// A source is considered the same if the name matches, or if BOTH the URL and
// path match. Two different files from the same repo are distinct sources.
func upsertRegistrySource(sc *config.SyncConfig, src config.RegistrySourceEntry) {
	for i, existing := range sc.RegistrySources {
		if existing.Name == src.Name || (existing.URL == src.URL && existing.Path == src.Path) {
			sc.RegistrySources[i] = src
			return
		}
	}
	sc.RegistrySources = append(sc.RegistrySources, src)
}

// RegistryDeleteRequest holds the inputs for deleting a registry source.
type RegistryDeleteRequest struct {
	ConfigDir string
	Name      string // source name to delete
}

// RegistryDelete deletes a registry source from sync-config and removes its cache file.
func RegistryDelete(req RegistryDeleteRequest, stdout io.Writer) error {
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir))
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}

	found := false
	filtered := make([]config.RegistrySourceEntry, 0, len(sc.RegistrySources))
	for _, src := range sc.RegistrySources {
		if src.Name == req.Name {
			found = true
			continue
		}
		filtered = append(filtered, src)
	}
	if !found {
		return fmt.Errorf("registry source %q not found", req.Name)
	}

	sc.RegistrySources = filtered
	if err := config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}

	cachePath := config.SourceCachePath(req.ConfigDir, req.Name)

	// Remove packs from the search index before deleting the cache file (best-effort).
	if cached, loadErr := config.LoadRegistry(cachePath); loadErr == nil {
		if pruneErr := pruneIndexEntries(cached, req.ConfigDir); pruneErr != nil {
			fmt.Fprintf(stdout, "warning: index cleanup failed: %v\n", pruneErr)
		}
	}

	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stdout, "warning: could not remove cache file: %v\n", err)
	}

	fmt.Fprintf(stdout, "Deleted registry source %q\n", req.Name)
	return nil
}

// indexRegistryEntries upserts registry entries into the search index, making
// them discoverable via `aipack search` without letting registry metadata
// override the installed-pack inventory.
func indexRegistryEntries(reg config.Registry, configDir string) error {
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	installed := lockfileInstalledPackRoots(configDir)
	packs := make([]index.PackInfo, 0, len(reg.Packs))
	for name, entry := range reg.Packs {
		repo := entry.Repo
		if entry.Method == config.MethodArchive {
			repo = entry.URL
		}
		packs = append(packs, index.PackInfo{
			Name:        name,
			Description: entry.Description,
			Repo:        repo,
			Ref:         entry.Ref,
			Path:        entry.Path,
			Owner:       entry.Owner,
			Contact:     entry.Contact,
			Installed:   installed[name] != "",
		})
	}
	return db.UpdateRegistryPacks(packs)
}

// pruneIndexEntries removes packs from the search index that belong to a registry.
func pruneIndexEntries(reg config.Registry, configDir string) error {
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	for name := range reg.Packs {
		if err := db.DeletePack(name); err != nil {
			return err
		}
	}
	return nil
}

// RegistryDeepIndexRequest holds the inputs for deep-indexing registry packs.
type RegistryDeepIndexRequest struct {
	ConfigDir    string
	RegistryPath string

	// Test injection points.
	GitCloneFn func(repoURL, dir, ref string) error // nil = source.EnsureClone
}

// RegistryDeepIndex clones each uninstalled registry pack and indexes its
// resource-level frontmatter into the search index. Already-installed packs
// are skipped because sync provides richer data.
func RegistryDeepIndex(ctx context.Context, req RegistryDeepIndexRequest, stdout io.Writer) error {
	reg, err := loadRegistryForRequest(RegistryListRequest{
		ConfigDir:    req.ConfigDir,
		RegistryPath: req.RegistryPath,
	})
	if err != nil {
		return err
	}

	db, err := openIndexDB(req.ConfigDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	cloneFn := req.GitCloneFn
	if cloneFn == nil {
		cloneFn = func(repoURL, dir, ref string) error {
			return source.EnsureClone(ctx, repoURL, dir, ref)
		}
	}

	indexed := 0
	installed := lockfileInstalledPackRoots(req.ConfigDir)
	for name, entry := range reg.Packs {
		if installed[name] != "" {
			continue
		}
		sourceValue := entry.Repo
		if entry.Method == config.MethodArchive {
			sourceValue = entry.URL
		}
		if sourceValue == "" {
			continue
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "Deep-indexing %s from %s\n", name, sourceValue)
		}
		resources, err := deepIndexOnePack(ctx, req.ConfigDir, name, entry, cloneFn)
		if err != nil {
			printDeepIndexWarning(stdout, name, err)
			continue
		}

		packInfo := index.PackInfo{
			Name:        name,
			Description: entry.Description,
			Repo:        sourceValue,
			Ref:         entry.Ref,
			Path:        entry.Path,
			Owner:       entry.Owner,
			Contact:     entry.Contact,
			Source:      "deep-index",
		}
		if err := db.UpdateDeepIndex(packInfo, resources); err != nil {
			printDeepIndexWarning(stdout, name, err)
			continue
		}
		indexed++
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "Deep-indexed %d pack(s) from registry (%d total)\n", indexed, len(reg.Packs))
	}
	return nil
}

func printDeepIndexWarning(w io.Writer, pack string, err error) {
	if w == nil {
		return
	}
	if err != nil {
		fmt.Fprintf(w, "warning: deep-index %s: %v\n", pack, err)
		return
	}
	fmt.Fprintf(w, "warning: deep-index %s\n", pack)
}

// deepIndexOnePack fetches a registry pack into a temp dir, extracts it through
// the same staging path used by install/inspect, and indexes discovered content.
func deepIndexOnePack(ctx context.Context, configDir, packName string, entry config.RegistryEntry, cloneFn func(string, string, string) error) ([]index.Resource, error) {
	tmp, err := makePackTempDir(configDir, "deep-index-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	packRoot := tmp
	boundary := tmp
	switch entry.Method {
	case config.MethodArchive:
		if entry.URL == "" {
			return nil, fmt.Errorf("archive registry entry missing url")
		}
		if err := source.FetchHTTPArchive(ctx, entry.URL, tmp, source.HTTPArchiveOptions{}); err != nil {
			return nil, fmt.Errorf("fetching archive %s: %w", entry.URL, err)
		}
		var err error
		if len(entry.ContentPaths) > 0 {
			packRoot, err = resolveArchiveContentRoot(tmp, entry.Path)
		} else {
			packRoot, err = resolveArchivePackRoot(tmp, entry.Path)
		}
		if err != nil {
			return nil, err
		}
	default:
		if entry.Repo == "" {
			return nil, fmt.Errorf("registry entry missing repo")
		}
		if err := cloneFn(entry.Repo, tmp, entry.Ref); err != nil {
			return nil, fmt.Errorf("cloning %s: %w", entry.Repo, err)
		}
		if entry.Path != "" {
			packRoot = filepath.Join(tmp, entry.Path)
		}
	}

	staging, manifest, err := extractPackContent(packStagingDir(configDir), packRoot, entry.ContentPaths, packName, boundary)
	if err != nil {
		return nil, fmt.Errorf("extracting pack: %w", err)
	}
	defer os.RemoveAll(staging)

	if err := config.DiscoverContent(&manifest, staging); err != nil {
		return nil, fmt.Errorf("discovering content: %w", err)
	}
	return resourcesFromManifestRoot(packName, staging, manifest), nil
}

// extractPluginFromFile parses a plugin descriptor JSON and produces a
// Resource with Kind="plugin", matching what ExtractFromPack does for installed
// packs.
func extractPluginFromFile(path string) (index.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return index.Resource{}, err
	}
	var plugin domain.Plugin
	if err := json.Unmarshal(data, &plugin); err != nil {
		return index.Resource{}, err
	}
	name := strings.TrimSuffix(filepath.Base(path), ".json")
	body := plugin.Source
	if plugin.Marketplace != "" {
		body += "\n" + plugin.Marketplace
	}
	return index.Resource{
		Kind:        "plugin",
		Name:        name,
		Description: plugin.Source,
		Path:        filepath.Join("plugins", filepath.Base(path)),
		Body:        body,
	}, nil
}

// extractMCPServerFromFile parses an MCP server inventory JSON and produces a
// searchable Resource with Kind="mcp". The body string concatenates Notes,
// Auth, transport summary, and the available_tools list so FTS5 can match on
// any of them.
func extractMCPServerFromFile(path string) (index.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return index.Resource{}, err
	}
	var server domain.MCPServer
	if err := json.Unmarshal(data, &server); err != nil {
		return index.Resource{}, err
	}
	name := server.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".json")
	}

	// Build a search-friendly body: transport summary + notes + auth + tools.
	var bodyParts []string
	if t := server.Transport; t != "" {
		bodyParts = append(bodyParts, "transport: "+t)
	}
	if len(server.Command) > 0 {
		bodyParts = append(bodyParts, "command: "+strings.Join(server.Command, " "))
	}
	if server.URL != "" {
		bodyParts = append(bodyParts, "url: "+server.URL)
	}
	if server.Auth != "" {
		bodyParts = append(bodyParts, "auth: "+server.Auth)
	}
	if server.Notes != "" {
		bodyParts = append(bodyParts, server.Notes)
	}
	if len(server.AvailableTools) > 0 {
		bodyParts = append(bodyParts, "tools: "+strings.Join(server.AvailableTools, ", "))
	}

	desc := server.Notes
	if desc == "" {
		desc = "MCP server"
	}

	return index.Resource{
		Kind:        "mcp",
		Name:        name,
		Description: desc,
		Path:        filepath.Join("mcp", filepath.Base(path)),
		Body:        strings.Join(bodyParts, "\n"),
	}, nil
}

func registryEntriesToResults(reg config.Registry) []RegistrySearchResult {
	results := make([]RegistrySearchResult, 0, len(reg.Packs))
	for name, entry := range reg.Packs {
		results = append(results, RegistrySearchResult{
			Name:          name,
			RegistryEntry: entry,
		})
	}
	slices.SortFunc(results, func(a, b RegistrySearchResult) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return results
}

// markInstalled sets the Installed flag for packs that exist in the packs directory.
func markInstalled(results []RegistrySearchResult, configDir string) {
	if configDir == "" {
		return
	}
	packsDir := PacksDir(configDir)
	for i := range results {
		_, err := os.Stat(filepath.Join(packsDir, results[i].Name))
		results[i].Installed = err == nil
	}
}
