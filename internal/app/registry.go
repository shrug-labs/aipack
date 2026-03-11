package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
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
	Name string `json:"name"`
	config.RegistryEntry
}

// RegistryList returns all packs in the registry, sorted by name.
func RegistryList(req RegistryListRequest) ([]RegistrySearchResult, error) {
	reg, err := loadRegistryForRequest(req)
	if err != nil {
		return nil, err
	}
	return registryEntriesToResults(reg), nil
}

// RegistrySearch returns packs whose name or description match the query (case-insensitive substring).
func RegistrySearch(req RegistryListRequest, query string) ([]RegistrySearchResult, error) {
	reg, err := loadRegistryForRequest(req)
	if err != nil {
		return nil, err
	}
	all := registryEntriesToResults(reg)
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
		return config.RegistryEntry{}, fmt.Errorf("pack %q not found in registry", name)
	}
	return entry, nil
}

func loadRegistryForRequest(req RegistryListRequest) (config.Registry, error) {
	if req.RegistryPath != "" {
		// Explicit path override — single file mode.
		return config.LoadRegistry(req.RegistryPath)
	}
	// Default — merged view from local + cached sources.
	return config.LoadMergedRegistry(req.ConfigDir)
}

// RegistryFetchRequest holds the inputs for fetching a remote registry.
type RegistryFetchRequest struct {
	ConfigDir string
	URL       string // explicit URL; empty = fetch all known sources
	Ref       string // git ref (branch/tag); presence implies git-based fetch
	Path      string // file path within repo (git only); default: registry.yaml
	Name      string // explicit source name; empty = derive from URL
	Prune     bool   // deprecated: cached registries are always overwritten

	// FetchFn overrides how bytes are fetched from an HTTP URL (for testing).
	FetchFn func(url string) ([]byte, error)

	// GitFetchFn overrides how a file is fetched via git (for testing).
	GitFetchFn func(repo, ref, path string) ([]byte, error)
}

// RegistryFetch fetches remote registries and caches them locally.
// With an explicit URL, fetches that single source and saves it to sync-config.
// Without a URL, fetches all sources in registry_sources (or the compiled-in default).
func RegistryFetch(req RegistryFetchRequest, stdout io.Writer) error {
	if req.Prune {
		fmt.Fprintln(stdout, "note: --prune is no longer needed; cached registries are kept in sync automatically")
	}

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir))
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}

	if req.URL != "" {
		if _, err := registryFetchOne(req, &sc, stdout); err != nil {
			return err
		}
		return config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc)
	}

	// Multi-source: fetch all known sources.
	sources := sc.RegistrySources

	// Backward compat: if no sources but defaults.registry_url is set, use it.
	if len(sources) == 0 && sc.Defaults.RegistryURL != "" {
		sources = []config.RegistrySourceEntry{{
			Name: config.DeriveSourceName(sc.Defaults.RegistryURL),
			URL:  sc.Defaults.RegistryURL,
		}}
	}

	// No sources at all: use compiled-in default.
	if len(sources) == 0 {
		sources = []config.RegistrySourceEntry{{
			Name: config.DeriveSourceName(config.DefaultRegistryRepo),
			URL:  config.DefaultRegistryRepo,
			Ref:  config.DefaultRegistryRef,
			Path: config.DefaultRegistryPath,
		}}
	}

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
		n, err := registryFetchOne(oneReq, &sc, stdout)
		if err != nil {
			fmt.Fprintf(stdout, "warning: %s: %v\n", src.Name, err)
			continue
		}
		succeeded++
		totalPacks += n
	}

	if succeeded == 0 {
		return fmt.Errorf("all %d registry source(s) failed to fetch", len(sources))
	}

	// Save sync-config once after all sources are processed.
	if err := config.SaveSyncConfig(config.SyncConfigPath(req.ConfigDir), sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}

	if len(sources) > 1 {
		fmt.Fprintf(stdout, "%d source(s), %d total pack(s)\n", len(sources), totalPacks)
	}
	return nil
}

// registryFetchOne fetches a single registry source, caches it, and upserts it
// into the in-memory sync-config. The caller is responsible for saving sync-config.
// Returns the number of packs in the fetched registry.
func registryFetchOne(req RegistryFetchRequest, sc *config.SyncConfig, stdout io.Writer) (int, error) {
	url := req.URL
	ref := req.Ref
	filePath := req.Path

	// Apply defaults for git URLs. For HTTP URLs, ref and filePath stay empty —
	// the upsert writes them with omitempty, and re-fetch identifies the source
	// as HTTP because IsGitURL(url, "") returns false.
	isGit := config.IsGitURL(url, ref)
	if isGit {
		if ref == "" {
			ref = config.DefaultRegistryRef
		}
		if filePath == "" {
			filePath = config.DefaultRegistryPath
		}
	}

	// Resolve source name.
	name := req.Name
	if name == "" {
		derived := config.DeriveSourceName(url)
		name = config.UniqueSourceName(derived, url, sc.RegistrySources)
	}

	// Fetch remote registry bytes.
	var data []byte
	var err error

	if isGit {
		gitFetchFn := req.GitFetchFn
		if gitFetchFn == nil {
			gitFetchFn = config.FetchFileViaGit
		}
		data, err = gitFetchFn(url, ref, filePath)
	} else {
		fetchFn := req.FetchFn
		if fetchFn == nil {
			fetchFn = config.FetchRegistryFromURL
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
		fmt.Fprintf(stdout, "warning: index update failed: %v\n", err)
	}

	fmt.Fprintf(stdout, "%s: %d pack(s) (from %s)\n", name, len(remote.Packs), url)
	return len(remote.Packs), nil
}

// upsertRegistrySource adds or updates a source in the sync-config sources list.
func upsertRegistrySource(sc *config.SyncConfig, src config.RegistrySourceEntry) {
	for i, existing := range sc.RegistrySources {
		if existing.Name == src.Name || existing.URL == src.URL {
			sc.RegistrySources[i] = src
			return
		}
	}
	sc.RegistrySources = append(sc.RegistrySources, src)
}

// RegistryRemoveRequest holds the inputs for removing a registry source.
type RegistryRemoveRequest struct {
	ConfigDir string
	Name      string // source name to remove
}

// RegistryRemove removes a registry source from sync-config and deletes its cache file.
func RegistryRemove(req RegistryRemoveRequest, stdout io.Writer) error {
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

	fmt.Fprintf(stdout, "Removed registry source %q\n", req.Name)
	return nil
}

// indexRegistryEntries upserts registry entries into the search index as
// uninstalled packs, making them discoverable via `aipack search`.
func indexRegistryEntries(reg config.Registry, configDir string) error {
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	packs := make([]index.PackInfo, 0, len(reg.Packs))
	for name, entry := range reg.Packs {
		packs = append(packs, index.PackInfo{
			Name:        name,
			Description: entry.Description,
			Repo:        entry.Repo,
			Ref:         entry.Ref,
			Path:        entry.Path,
			Owner:       entry.Owner,
			Contact:     entry.Contact,
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
	GitCloneFn func(repoURL, dir, ref string) error // nil = config.EnsureClone
}

// RegistryDeepIndex clones each uninstalled registry pack and indexes its
// resource-level frontmatter into the search index. Already-installed packs
// are skipped because sync provides richer data.
func RegistryDeepIndex(req RegistryDeepIndexRequest, stdout io.Writer) error {
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
		cloneFn = config.EnsureClone
	}

	indexed := 0
	for name, entry := range reg.Packs {
		if entry.Repo == "" {
			continue
		}
		resources, err := deepIndexOnePack(entry, cloneFn)
		if err != nil {
			fmt.Fprintf(stdout, "warning: deep-index %s: %v\n", name, err)
			continue
		}

		packInfo := index.PackInfo{
			Name:        name,
			Description: entry.Description,
			Repo:        entry.Repo,
			Ref:         entry.Ref,
			Path:        entry.Path,
			Owner:       entry.Owner,
			Contact:     entry.Contact,
			Source:      "deep-index",
		}
		if err := db.UpdateDeepIndex(packInfo, resources); err != nil {
			fmt.Fprintf(stdout, "warning: deep-index %s: %v\n", name, err)
			continue
		}
		indexed++
	}

	fmt.Fprintf(stdout, "Deep-indexed %d pack(s) from registry (%d total)\n", indexed, len(reg.Packs))
	return nil
}

// deepIndexOnePack clones a registry pack into a temp dir and extracts
// resource metadata from frontmatter in each content directory.
func deepIndexOnePack(entry config.RegistryEntry, cloneFn func(string, string, string) error) ([]index.Resource, error) {
	tmp, err := os.MkdirTemp("", "aipack-deep-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := cloneFn(entry.Repo, tmp, entry.Ref); err != nil {
		return nil, fmt.Errorf("cloning %s: %w", entry.Repo, err)
	}

	packRoot := tmp
	if entry.Path != "" {
		packRoot = filepath.Join(tmp, entry.Path)
	}

	// Respect pack.json root field if present.
	manifestPath := filepath.Join(packRoot, "pack.json")
	if manifest, merr := config.LoadPackManifest(manifestPath); merr == nil {
		if manifest.Root != "" && manifest.Root != "." {
			packRoot = filepath.Join(packRoot, manifest.Root)
		}
	}

	var resources []index.Resource

	// Scan content directories: rules, agents, workflows, skills.
	contentDirs := []struct {
		dir  string
		kind string
	}{
		{"rules", "rule"},
		{"agents", "agent"},
		{"workflows", "workflow"},
		{"skills", "skill"},
	}

	for _, cd := range contentDirs {
		dirPath := filepath.Join(packRoot, cd.dir)
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue // directory doesn't exist — skip
		}
		for _, e := range entries {
			if cd.kind == "skill" && e.IsDir() {
				r, err := extractSkillFromDir(filepath.Join(dirPath, e.Name()), e.Name())
				if err == nil {
					resources = append(resources, r)
				}
			} else if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				r, err := extractResourceFromFile(filepath.Join(dirPath, e.Name()), cd.kind)
				if err == nil {
					resources = append(resources, r)
				}
			}
		}
	}

	return resources, nil
}

// extractResourceFromFile reads a markdown file, splits frontmatter, and
// builds a Resource from the metadata.
func extractResourceFromFile(path, kind string) (index.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return index.Resource{}, err
	}

	fm, body, err := domain.SplitFrontmatter(data)
	if err != nil {
		return index.Resource{}, err
	}

	meta := parseFrontmatterMeta(fm)
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	desc := index.MetaString(meta, "description")
	return index.ResourceFromMetadata(kind, name, desc, filepath.Base(path), meta, string(body)), nil
}

// extractSkillFromDir reads SKILL.md from a skill directory and builds a Resource.
func extractSkillFromDir(dirPath, name string) (index.Resource, error) {
	skillFile := filepath.Join(dirPath, "SKILL.md")
	r, err := extractResourceFromFile(skillFile, "skill")
	if err != nil {
		return r, err
	}
	r.Name = name
	r.Path = name
	return r, nil
}

// parseFrontmatterMeta parses YAML frontmatter bytes into a metadata map.
func parseFrontmatterMeta(fm []byte) map[string]any {
	if len(fm) == 0 {
		return nil
	}
	var meta map[string]any
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return nil
	}
	return meta
}

func registryEntriesToResults(reg config.Registry) []RegistrySearchResult {
	results := make([]RegistrySearchResult, 0, len(reg.Packs))
	for name, entry := range reg.Packs {
		results = append(results, RegistrySearchResult{
			Name:          name,
			RegistryEntry: entry,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}
