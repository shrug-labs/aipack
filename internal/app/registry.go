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
	scDefault := ""
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir))
	if err == nil {
		scDefault = sc.Defaults.Registry
	}
	path := config.ResolveRegistryPath(req.RegistryPath, scDefault, req.ConfigDir)
	return config.LoadRegistry(path)
}

// RegistryFetchRequest holds the inputs for fetching and merging a remote registry.
type RegistryFetchRequest struct {
	ConfigDir    string
	RegistryPath string // explicit override for local registry path
	URL          string // explicit URL override; empty = resolve from sync-config
	Prune        bool   // remove local entries not present in the fetched registry

	// FetchFn overrides how bytes are fetched from a URL. If nil, uses HTTP GET.
	// Only used when fetching via URL (explicit or sync-config). Git-based
	// fetch (the compiled-in default) does not use this.
	FetchFn func(url string) ([]byte, error)

	// GitFetchFn overrides how a file is fetched via git (for testing).
	// Signature: (repo, ref, path) → bytes. If nil, uses real git clone.
	GitFetchFn func(repo, ref, path string) ([]byte, error)
}

// RegistryFetch fetches a remote registry and merges it into the local registry.
func RegistryFetch(req RegistryFetchRequest, stdout io.Writer) error {
	// 1. Load sync-config once for URL and registry path resolution.
	var sc config.SyncConfig
	if loaded, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir)); err == nil {
		sc = loaded
	}

	// 2. Resolve the source: explicit URL > sync-config registry_url > compiled-in git default
	url := req.URL
	if url == "" {
		url = sc.Defaults.RegistryURL
	}

	// 3. Fetch the remote registry
	var data []byte
	var source string
	var err error

	if url != "" {
		// URL-based fetch (explicit or from sync-config).
		source = url
		fetchFn := req.FetchFn
		if fetchFn == nil {
			fetchFn = config.FetchRegistryFromURL
		}
		data, err = fetchFn(url)
	} else {
		// Git-based fetch using compiled-in default coordinates.
		source = config.DefaultRegistryRepo
		gitFetchFn := req.GitFetchFn
		if gitFetchFn == nil {
			gitFetchFn = config.FetchFileViaGit
		}
		data, err = gitFetchFn(config.DefaultRegistryRepo, config.DefaultRegistryRef, config.DefaultRegistryPath)
	}
	if err != nil {
		return fmt.Errorf("fetching registry from %s: %w", source, err)
	}

	// 4. Parse and validate
	remote, err := config.ParseRegistry(data)
	if err != nil {
		return fmt.Errorf("parsing remote registry: %w", err)
	}

	// 5. Resolve local registry path
	localPath := config.ResolveRegistryPath(req.RegistryPath, sc.Defaults.Registry, req.ConfigDir)

	// 6. Load or create local registry, merge
	local, err := config.LoadRegistry(localPath)
	if err != nil {
		// Local doesn't exist or is invalid — start fresh
		local = config.Registry{
			SchemaVersion: config.RegistrySchemaVersion,
			Packs:         make(map[string]config.RegistryEntry),
		}
	}

	added := 0
	for name, entry := range remote.Packs {
		if _, exists := local.Packs[name]; !exists {
			local.Packs[name] = entry
			added++
		}
	}

	pruned := 0
	var prunedNames []string
	if req.Prune {
		for name := range local.Packs {
			if _, exists := remote.Packs[name]; !exists {
				delete(local.Packs, name)
				prunedNames = append(prunedNames, name)
				pruned++
			}
		}
	}

	// 7. Write the merged registry
	out, err := yaml.Marshal(&local)
	if err != nil {
		return fmt.Errorf("marshalling registry: %w", err)
	}
	if err := util.WriteFileAtomicWithPerms(localPath, out, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing registry: %w", err)
	}

	// 8. Update the search index with registry entries (best-effort).
	if err := indexRegistryEntries(local, req.ConfigDir); err != nil {
		fmt.Fprintf(stdout, "warning: index update failed: %v\n", err)
	}

	// 8b. Remove pruned packs from the search index (best-effort).
	if len(prunedNames) > 0 {
		if err := pruneIndexEntries(prunedNames, req.ConfigDir); err != nil {
			fmt.Fprintf(stdout, "warning: index prune failed: %v\n", err)
		}
	}

	// 9. Summary
	if pruned > 0 {
		fmt.Fprintf(stdout, "Fetched registry from %s: %d new, %d pruned, %d total\n", source, added, pruned, len(local.Packs))
	} else {
		fmt.Fprintf(stdout, "Fetched registry from %s: %d new pack(s), %d total\n", source, added, len(local.Packs))
	}
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

// pruneIndexEntries removes pruned packs from the search index.
func pruneIndexEntries(names []string, configDir string) error {
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	for _, name := range names {
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
