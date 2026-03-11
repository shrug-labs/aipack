package app

import (
	"fmt"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/index"
)

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
}

// RunIndexSearch opens the index DB and executes a search with optional filters.
func RunIndexSearch(req IndexSearchRequest) ([]index.SearchResult, error) {
	db, err := openIndexDB(req.ConfigDir, req.Home)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	results, err := db.Search(req.Terms, index.SearchFilters{
		Tags:      req.Tags,
		Role:      req.Role,
		Kind:      req.Kind,
		Pack:      req.Pack,
		Category:  req.Category,
		Installed: req.Installed,
	})
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []index.SearchResult{}
	}
	return results, nil
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

func openIndexDB(configDir, home string) (*index.DB, error) {
	cfgDir := configDir
	if cfgDir == "" {
		var err error
		cfgDir, err = config.DefaultConfigDir(home)
		if err != nil {
			return nil, fmt.Errorf("resolving config dir: %w", err)
		}
	}
	dbPath := filepath.Join(cfgDir, "index.db")
	db, err := index.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}
	return db, nil
}
