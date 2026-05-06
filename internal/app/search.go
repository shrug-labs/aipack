package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/index"
)

// SearchResult mirrors index.SearchResult at the app layer so callers
// (like the TUI) don't need to import internal/index.
type SearchResult = index.SearchResult

// IndexSearchRequest holds parameters for a search against the pack index.
type IndexSearchRequest struct {
	ConfigDir string // override; empty = default
	Home      string
	Terms     string
	Tags      []string
	Role      string
	Kind      string
	Pack      string
	Category  string
	Installed *bool // nil = both, true = installed only, false = available only
	Status    string
}

// IndexedPackDetail is the search-index view of a pack. It includes
// uninstalled registry, deep-index, and inspected packs, which may not have a
// local pack directory yet.
type IndexedPackDetail struct {
	Name           string
	Version        string
	Description    string
	Repo           string
	Ref            string
	Path           string
	Owner          string
	Contact        string
	Installed      bool
	Source         string
	PreviousSource string
	Status         string
	LastIndexedAt  int64
	Resources      []IndexedPackResource
}

// IndexedPackResource is one resource row from the search index.
type IndexedPackResource struct {
	Kind        string
	Name        string
	Description string
	Owner       string
	LastUpdated string
	Path        string
	Category    string
	Source      string
	Body        string
}

// RunIndexSearch opens the index DB and executes a search with optional filters.
func RunIndexSearch(req IndexSearchRequest) ([]SearchResult, error) {
	cfgDir, err := effectiveIndexConfigDir(req.ConfigDir, req.Home)
	if err != nil {
		return nil, err
	}
	db, err := openIndexDB(cfgDir, "")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Installed state is owned by the lockfile and pack directory, not by
	// index.db. Search broadly, hydrate stale index rows, then apply the
	// installed/status filter against the corrected state.
	results, err := db.Search(req.Terms, index.SearchFilters{
		Tags:     req.Tags,
		Role:     req.Role,
		Kind:     req.Kind,
		Pack:     req.Pack,
		Category: req.Category,
	})
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []index.SearchResult{}
	}
	results = hydrateInstalledSearchResults(cfgDir, results)
	results = filterHydratedSearchResults(results, req)
	return results, nil
}

// PackIndexDetails returns pack and resource metadata currently present in the
// search index. It is intentionally index-backed: callers can show inspected or
// deep-indexed pack details before a pack is installed locally.
func PackIndexDetails(configDir string) ([]IndexedPackDetail, error) {
	cfgDir, err := effectiveIndexConfigDir(configDir, "")
	if err != nil {
		return nil, err
	}
	db, err := openIndexDB(cfgDir, "")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, version, description, repo, ref, path, owner, contact, installed, source, previous_source, last_indexed_at
		FROM packs
		ORDER BY lower(name)`)
	if err != nil {
		return nil, fmt.Errorf("querying indexed packs: %w", err)
	}
	defer rows.Close()

	var details []IndexedPackDetail
	byName := map[string]int{}
	for rows.Next() {
		var detail IndexedPackDetail
		var installed int
		if err := rows.Scan(&detail.Name, &detail.Version, &detail.Description, &detail.Repo, &detail.Ref, &detail.Path, &detail.Owner, &detail.Contact, &installed, &detail.Source, &detail.PreviousSource, &detail.LastIndexedAt); err != nil {
			return nil, fmt.Errorf("scanning indexed pack: %w", err)
		}
		detail.Installed = installed != 0
		detail.Status = index.ResultStatus(detail.Installed, detail.Source)
		byName[detail.Name] = len(details)
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating indexed packs: %w", err)
	}

	resRows, err := db.Query(`SELECT p.name, r.kind, r.name, r.description, r.owner, r.last_updated, r.path, r.category, r.source
		FROM resources r
		JOIN packs p ON r.pack_id = p.id
		ORDER BY lower(p.name), r.kind, lower(r.name)`)
	if err != nil {
		return nil, fmt.Errorf("querying indexed resources: %w", err)
	}
	defer resRows.Close()

	for resRows.Next() {
		var packName string
		var resource IndexedPackResource
		if err := resRows.Scan(&packName, &resource.Kind, &resource.Name, &resource.Description, &resource.Owner, &resource.LastUpdated, &resource.Path, &resource.Category, &resource.Source); err != nil {
			return nil, fmt.Errorf("scanning indexed resource: %w", err)
		}
		if idx, ok := byName[packName]; ok {
			details[idx].Resources = append(details[idx].Resources, resource)
		}
	}
	if err := resRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating indexed resources: %w", err)
	}
	return details, nil
}

// RunIndexQuery executes raw SQL against the pack index and returns rows as maps.
func RunIndexQuery(configDir, home, sql string) ([]map[string]any, error) {
	db, err := openIndexDB(configDir, home)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, _, err := db.RawQuery(sql)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

// RunIndexSchema returns the DDL for the pack index database.
func RunIndexSchema(configDir, home string) (string, error) {
	db, err := openIndexDB(configDir, home)
	if err != nil {
		return "", err
	}
	defer db.Close()

	return db.Schema()
}

// SearchResultFilePath returns the primary file path for a search result.
func SearchResultFilePath(r SearchResult) string {
	if r.Path == "" {
		return ""
	}
	if r.Kind == "skill" {
		return filepath.Join(r.Path, "SKILL.md")
	}
	return r.Path
}

func effectiveIndexConfigDir(configDir, home string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}
	cfgDir, err := config.DefaultConfigDir(home)
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return cfgDir, nil
}

func openIndexDB(configDir, home string) (*index.DB, error) {
	cfgDir, err := effectiveIndexConfigDir(configDir, home)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(cfgDir, "index.db")
	db, err := index.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}
	return db, nil
}

func lockfileInstalledPackRoots(configDir string) map[string]string {
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil || len(lf.Packs) == 0 {
		return nil
	}
	packRoots := make(map[string]string, len(lf.Packs))
	for name := range lf.Packs {
		root := filepath.Join(PacksDir(configDir), name)
		if _, err := os.Lstat(root); err == nil {
			packRoots[name] = root
		}
	}
	return packRoots
}

func hydrateInstalledSearchResults(configDir string, results []SearchResult) []SearchResult {
	packRoots := lockfileInstalledPackRoots(configDir)
	if len(packRoots) == 0 {
		return results
	}

	out := append([]SearchResult(nil), results...)
	for i := range out {
		root, ok := packRoots[out[i].Pack]
		if !ok {
			continue
		}
		out[i].Installed = true
		out[i].Status = "installed"
		if out[i].Path != "" && !filepath.IsAbs(out[i].Path) {
			out[i].Path = filepath.Join(root, filepath.FromSlash(out[i].Path))
		}
	}
	return out
}

func filterHydratedSearchResults(results []SearchResult, req IndexSearchRequest) []SearchResult {
	keep := func(r SearchResult) bool {
		if req.Installed != nil && r.Installed != *req.Installed {
			return false
		}
		if req.Status != "" && r.Status != req.Status {
			return false
		}
		return true
	}
	if req.Installed == nil && req.Status == "" {
		return results
	}
	out := results[:0]
	for _, r := range results {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}
