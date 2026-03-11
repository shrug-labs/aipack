package index

import (
	"fmt"
	"strings"
)

// SearchResult is a resource or pack matching a search query.
type SearchResult struct {
	Pack        string `json:"pack"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner       string `json:"owner"`
	LastUpdated string `json:"last_updated,omitempty"`
	Path        string `json:"path,omitempty"`
	Category    string `json:"category,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	Installed   bool   `json:"installed"`
}

// SearchFilters holds optional structured filters for Search.
type SearchFilters struct {
	Tags      []string
	Role      string
	Kind      string
	Pack      string
	Category  string
	Installed *bool // nil = both, true = installed only, false = available only
}

// Search performs an FTS5 search with optional structured filters.
// If terms is empty, returns all resources matching the filters.
// Uninstalled packs from the registry are included as pack-level results
// unless filters exclude them (e.g. Kind="skill").
func (db *DB) Search(terms string, filters SearchFilters) ([]SearchResult, error) {
	results, err := db.searchResources(terms, filters)
	if err != nil {
		return nil, err
	}

	// Include pack-level results for uninstalled packs unless the caller
	// is filtering to a specific resource kind or only wants installed results.
	includePacks := true
	if filters.Kind != "" && filters.Kind != "pack" {
		includePacks = false
	}
	if filters.Installed != nil && *filters.Installed {
		includePacks = false
	}
	// Tag/role/category filters don't apply to pack-level entries (no resources).
	if len(filters.Tags) > 0 || filters.Role != "" || filters.Category != "" {
		includePacks = false
	}

	if includePacks {
		packResults, err := db.searchPacks(terms, filters)
		if err != nil {
			return nil, err
		}
		results = append(results, packResults...)
	}

	return results, nil
}

// searchResources queries the resources table with FTS + structured filters.
// When FTS terms are provided, results are ranked with BM25 (name:10, description:5, body:1)
// and include a snippet from the body column for context.
func (db *DB) searchResources(terms string, filters SearchFilters) ([]SearchResult, error) {
	var where []string
	var args []any

	from := "resources r JOIN packs p ON r.pack_id = p.id"

	// snippet and orderBy depend on whether FTS is active.
	snippetExpr := "''" // no snippet without FTS
	orderBy := "r.kind, r.name"

	if terms != "" {
		from += " JOIN resources_fts fts ON fts.rowid = r.id"
		where = append(where, "resources_fts MATCH ?")
		args = append(args, terms)
		// BM25 weights: name=10, description=5, body=1.
		orderBy = "bm25(resources_fts, 10.0, 5.0, 1.0)"
		// Snippet from body column (index 2). Show 16 tokens of context.
		snippetExpr = "snippet(resources_fts, 2, '«', '»', '…', 16)"
	}
	if len(filters.Tags) > 0 {
		placeholders := make([]string, len(filters.Tags))
		for i, t := range filters.Tags {
			placeholders[i] = "?"
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf("r.id IN (SELECT resource_id FROM tags WHERE tag IN (%s))", strings.Join(placeholders, ",")))
	}
	if filters.Role != "" {
		where = append(where, "r.id IN (SELECT resource_id FROM roles WHERE role = ?)")
		args = append(args, filters.Role)
	}
	if filters.Kind != "" && filters.Kind != "pack" {
		where = append(where, "r.kind = ?")
		args = append(args, filters.Kind)
	}
	if filters.Pack != "" {
		where = append(where, "p.name = ?")
		args = append(args, filters.Pack)
	}
	if filters.Category != "" {
		where = append(where, "r.category = ?")
		args = append(args, filters.Category)
	}
	if filters.Installed != nil {
		where = append(where, "p.installed = ?")
		args = append(args, boolToInt(*filters.Installed))
	}

	query := fmt.Sprintf("SELECT p.name, r.kind, r.name, r.description, r.owner, r.last_updated, r.path, r.category, %s, p.installed FROM %s", snippetExpr, from)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY " + orderBy

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching index: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var sr SearchResult
		var installed int
		if err := rows.Scan(&sr.Pack, &sr.Kind, &sr.Name, &sr.Description, &sr.Owner, &sr.LastUpdated, &sr.Path, &sr.Category, &sr.Snippet, &installed); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}
		sr.Installed = installed != 0
		results = append(results, sr)
	}
	return results, rows.Err()
}

// searchPacks queries uninstalled packs that match the search terms.
func (db *DB) searchPacks(terms string, filters SearchFilters) ([]SearchResult, error) {
	var where []string
	var args []any

	from := "packs p"

	where = append(where, "p.installed = 0")

	if terms != "" {
		from += " JOIN packs_fts pfts ON pfts.rowid = p.id"
		where = append(where, "packs_fts MATCH ?")
		args = append(args, terms)
	}
	if filters.Pack != "" {
		where = append(where, "p.name = ?")
		args = append(args, filters.Pack)
	}

	query := "SELECT p.name, 'pack', p.name, p.description, p.owner, '', '' FROM " + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY p.name"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching packs: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var sr SearchResult
		if err := rows.Scan(&sr.Pack, &sr.Kind, &sr.Name, &sr.Description, &sr.Owner, &sr.LastUpdated, &sr.Path); err != nil {
			return nil, fmt.Errorf("scanning pack result: %w", err)
		}
		results = append(results, sr)
	}
	return results, rows.Err()
}

// RawQuery executes arbitrary read-only SQL and returns rows as maps.
func (db *DB) RawQuery(sql string) ([]map[string]any, []string, error) {
	rows, err := db.Query(sql)
	if err != nil {
		return nil, nil, fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("getting columns: %w", err)
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var result []map[string]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = vals[i]
		}
		result = append(result, row)
	}
	return result, cols, rows.Err()
}

// Schema returns the DDL for all tables in the index DB.
func (db *DB) Schema() (string, error) {
	rows, err := db.Query("SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		return "", fmt.Errorf("reading schema: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var ddl string
		if err := rows.Scan(&ddl); err != nil {
			return "", fmt.Errorf("scanning schema: %w", err)
		}
		parts = append(parts, ddl)
	}
	return strings.Join(parts, ";\n\n") + ";", rows.Err()
}
