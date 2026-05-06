package index

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// InspectedTTL is the default time-to-live for `inspect` rows in the index.
// Rows older than this are dropped opportunistically on the next inspect.
const InspectedTTL = 30 * 24 * time.Hour

// PackInfo holds pack-level metadata for indexing.
type PackInfo struct {
	Name        string
	Version     string
	Description string
	Repo        string
	Ref         string
	Path        string // subdirectory within repo
	Owner       string
	Contact     string
	Installed   bool
	Source      string // "sync", "registry"
}

// Resource holds resource-level metadata extracted from frontmatter.
type Resource struct {
	Kind        string // see ExtractFromPack (rule, skill, workflow, agent, plugin) and the registry/inspect extractors (prompt, mcp)
	Name        string
	Description string
	Owner       string
	LastUpdated string
	Path        string // relative to pack root
	Body        string // markdown body text (for full-text search)
	Category    string // constrained enum: ops, dev, infra, governance, meta
	Tags        []string
	Roles       []string
	Requires    []string // "mcp:atlassian" format
}

// Update replaces all index entries for a pack. Idempotent.
// For installed packs (via sync), this sets installed=1 and replaces all resources.
func (db *DB) Update(pack PackInfo, resources []Resource) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning index update: %w", err)
	}
	defer tx.Rollback()

	// Upsert pack. Update always means installed (it carries resources).
	// Preserve registry-sourced coordinates (repo, ref, path, contact) when
	// the caller doesn't supply them.
	source := pack.Source
	if source == "" {
		source = "sync"
	}
	_, err = tx.Exec(`INSERT INTO packs (name, version, description, repo, ref, path, owner, contact, installed, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,
			description=excluded.description,
			owner=excluded.owner,
			installed=1,
			source=excluded.source,
			repo=CASE WHEN excluded.repo != '' THEN excluded.repo ELSE packs.repo END,
			ref=CASE WHEN excluded.ref != '' THEN excluded.ref ELSE packs.ref END,
			path=CASE WHEN excluded.path != '' THEN excluded.path ELSE packs.path END,
			contact=CASE WHEN excluded.contact != '' THEN excluded.contact ELSE packs.contact END`,
		pack.Name, pack.Version, pack.Description, pack.Repo, pack.Ref, pack.Path,
		pack.Owner, pack.Contact, source)
	if err != nil {
		return fmt.Errorf("upserting pack %s: %w", pack.Name, err)
	}

	var packID int64
	if err := tx.QueryRow("SELECT id FROM packs WHERE name=?", pack.Name).Scan(&packID); err != nil {
		return fmt.Errorf("looking up pack id for %s: %w", pack.Name, err)
	}

	// Delete old resources (CASCADE deletes tags, roles, requires).
	if _, err := tx.Exec("DELETE FROM resources WHERE pack_id=?", packID); err != nil {
		return fmt.Errorf("clearing old resources for %s: %w", pack.Name, err)
	}

	if err := insertResources(tx, packID, resources, source); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateRegistryPacks upserts registry entries as pack-level rows.
// Already-installed packs are not downgraded; callers can also mark a
// registry entry as installed when their inventory knows more than index.db.
// Source coordinates (repo, ref, path, contact) are refreshed either way.
func (db *DB) UpdateRegistryPacks(packs []PackInfo) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning registry index update: %w", err)
	}
	defer tx.Rollback()

	names := make([]any, 0, len(packs))
	for _, p := range packs {
		names = append(names, p.Name)
		_, err := tx.Exec(`INSERT INTO packs (name, version, description, repo, ref, path, owner, contact, installed, source, previous_source, last_indexed_at)
			VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, 'registry', '', 0)
			ON CONFLICT(name) DO UPDATE SET
				repo=excluded.repo,
				ref=excluded.ref,
				path=excluded.path,
				contact=excluded.contact,
				installed=CASE WHEN packs.installed = 1 OR excluded.installed = 1 THEN 1 ELSE 0 END,
				description=CASE WHEN packs.installed = 0 THEN excluded.description ELSE packs.description END,
				owner=CASE WHEN packs.installed = 0 THEN excluded.owner ELSE packs.owner END,
				source=CASE
					WHEN packs.installed = 1 THEN packs.source
					WHEN excluded.installed = 1 THEN 'registry'
					WHEN packs.source = 'deep-index' THEN packs.source
					WHEN packs.source = 'inspected' AND packs.previous_source = 'deep-index' THEN 'deep-index'
					ELSE 'registry'
				END,
				previous_source=CASE WHEN packs.installed = 0 THEN '' ELSE packs.previous_source END,
				last_indexed_at=CASE WHEN packs.installed = 0 THEN 0 ELSE packs.last_indexed_at END`,
			p.Name, p.Description, p.Repo, p.Ref, p.Path, p.Owner, p.Contact, boolToInt(p.Installed))
		if err != nil {
			return fmt.Errorf("upserting registry pack %s: %w", p.Name, err)
		}
	}
	if len(names) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
		if _, err := tx.Exec(`DELETE FROM resources WHERE source = 'inspected' AND pack_id IN (
			SELECT id FROM packs WHERE installed = 0 AND name IN (`+placeholders+`)
		)`, names...); err != nil {
			return fmt.Errorf("clearing inspected resources for registry packs: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM resources WHERE (source = '' OR source = 'registry') AND pack_id IN (
			SELECT id FROM packs WHERE installed = 0 AND source = 'registry' AND name IN (`+placeholders+`)
		)`, names...); err != nil {
			return fmt.Errorf("clearing registry-only resources for registry packs: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateDeepIndex upserts resource-level metadata for uninstalled packs.
// Already-installed packs are skipped — sync provides richer data.
// Source is set to "deep-index" so it can be distinguished from sync data.
func (db *DB) UpdateDeepIndex(pack PackInfo, resources []Resource) error {
	return db.updateUninstalledResourceIndex(pack, resources, "deep-index")
}

// UpdateInspectedIndex upserts resource-level metadata for a one-off inspected
// pack. Already-installed packs are skipped — sync provides authoritative data.
// Stale inspected rows (older than InspectedTTL) are dropped opportunistically
// on the same transaction boundary so the inspected slice doesn't accumulate
// indefinitely without explicit cleanup.
func (db *DB) UpdateInspectedIndex(pack PackInfo, resources []Resource) error {
	if _, err := db.DropStaleInspected(time.Now(), InspectedTTL); err != nil {
		return err
	}
	return db.updateUninstalledResourceIndex(pack, resources, "inspected")
}

func (db *DB) updateUninstalledResourceIndex(pack PackInfo, resources []Resource, source string) error {
	// Skip if this pack is already installed — sync data is authoritative.
	var installed int
	var existingSource, existingPreviousSource string
	err := db.QueryRow("SELECT installed, source, previous_source FROM packs WHERE name=?", pack.Name).Scan(&installed, &existingSource, &existingPreviousSource)
	if err == nil && installed == 1 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning deep index update: %w", err)
	}
	defer tx.Rollback()

	// Stamp `last_indexed_at` for inspected rows so the TTL/clear path can
	// reason about row freshness. Other uninstalled sources (registry,
	// deep-index) leave the column at 0; they are refreshed by `registry
	// fetch` on its own cadence.
	var lastIndexedAt int64
	previousSource := ""
	if source == "inspected" {
		lastIndexedAt = time.Now().Unix()
		if existingSource != "" && existingSource != "inspected" {
			previousSource = existingSource
		} else {
			previousSource = existingPreviousSource
		}
	}

	// Upsert pack as uninstalled with the requested discovery source.
	_, err = tx.Exec(`INSERT INTO packs (name, version, description, repo, ref, path, owner, contact, installed, source, previous_source, last_indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,
			description=CASE WHEN excluded.description != '' THEN excluded.description ELSE packs.description END,
			repo=CASE WHEN excluded.repo != '' THEN excluded.repo ELSE packs.repo END,
			ref=CASE WHEN excluded.ref != '' THEN excluded.ref ELSE packs.ref END,
			path=CASE WHEN excluded.path != '' THEN excluded.path ELSE packs.path END,
			owner=CASE WHEN excluded.owner != '' THEN excluded.owner ELSE packs.owner END,
			contact=CASE WHEN excluded.contact != '' THEN excluded.contact ELSE packs.contact END,
			source=excluded.source,
			previous_source=excluded.previous_source,
			last_indexed_at=CASE WHEN excluded.last_indexed_at > 0 THEN excluded.last_indexed_at ELSE packs.last_indexed_at END`,
		pack.Name, pack.Version, pack.Description, pack.Repo, pack.Ref, pack.Path,
		pack.Owner, pack.Contact, source, previousSource, lastIndexedAt)
	if err != nil {
		return fmt.Errorf("upserting %s pack %s: %w", source, pack.Name, err)
	}

	var packID int64
	if err := tx.QueryRow("SELECT id FROM packs WHERE name=?", pack.Name).Scan(&packID); err != nil {
		return fmt.Errorf("looking up pack id for %s: %w", pack.Name, err)
	}

	// Replace old resources from the same discovery source. This lets
	// inspected previews overlay deep-indexed resources without erasing them.
	if _, err := tx.Exec("DELETE FROM resources WHERE pack_id=? AND source=?", packID, source); err != nil {
		return fmt.Errorf("clearing old resources for %s: %w", pack.Name, err)
	}

	if err := insertResources(tx, packID, resources, source); err != nil {
		return err
	}

	return tx.Commit()
}

// DropStaleInspected removes inspected packs whose last_indexed_at is older
// than now-ttl. Returns the number of packs removed. Other discovery sources
// (sync, registry, deep-index) are untouched. A non-positive ttl is treated as
// "drop everything inspected" — equivalent to ClearInspected.
func (db *DB) DropStaleInspected(now time.Time, ttl time.Duration) (int64, error) {
	cutoff := now.Add(-ttl).Unix()
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning stale-inspected drop: %w", err)
	}
	defer tx.Rollback()

	// Delete resources first so CASCADE cleanup of tags/roles/requires is
	// covered by the existing foreign-key constraints; then delete the packs.
	target := `installed = 0 AND source = 'inspected' AND last_indexed_at > 0 AND last_indexed_at < ?`
	if _, err := tx.Exec(`DELETE FROM resources WHERE source = 'inspected' AND pack_id IN (
		SELECT id FROM packs WHERE `+target+`
	)`, cutoff); err != nil {
		return 0, fmt.Errorf("dropping stale inspected resources: %w", err)
	}
	restored, err := tx.Exec(`UPDATE packs SET source = previous_source, previous_source = '', last_indexed_at = 0 WHERE `+target+` AND previous_source != ''`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("restoring stale inspected packs: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM packs WHERE `+target+` AND previous_source = ''`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("dropping stale inspected packs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing stale-inspected drop: %w", err)
	}
	deletedRows, _ := res.RowsAffected()
	restoredRows, _ := restored.RowsAffected()
	return deletedRows + restoredRows, nil
}

// ClearInspected removes every inspected pack regardless of age. Used by
// `aipack pack inspect --clear` to wipe the inspected slice of the index.
func (db *DB) ClearInspected() (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning inspected clear: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM resources WHERE source = 'inspected'`); err != nil {
		return 0, fmt.Errorf("clearing inspected resources: %w", err)
	}
	restored, err := tx.Exec(`UPDATE packs SET source = previous_source, previous_source = '', last_indexed_at = 0 WHERE installed = 0 AND source = 'inspected' AND previous_source != ''`)
	if err != nil {
		return 0, fmt.Errorf("restoring inspected packs: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM packs WHERE installed = 0 AND source = 'inspected' AND previous_source = ''`)
	if err != nil {
		return 0, fmt.Errorf("clearing inspected packs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing inspected clear: %w", err)
	}
	deletedRows, _ := res.RowsAffected()
	restoredRows, _ := restored.RowsAffected()
	return deletedRows + restoredRows, nil
}

// DeletePack removes a pack and all its resources from the index.
// CASCADE on resources handles tags, roles, and requires cleanup.
func (db *DB) DeletePack(name string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning pack delete: %w", err)
	}
	defer tx.Rollback()

	// Delete resources first (CASCADE handles tags/roles/requires).
	_, err = tx.Exec("DELETE FROM resources WHERE pack_id IN (SELECT id FROM packs WHERE name=?)", name)
	if err != nil {
		return fmt.Errorf("deleting resources for %s: %w", name, err)
	}

	_, err = tx.Exec("DELETE FROM packs WHERE name=?", name)
	if err != nil {
		return fmt.Errorf("deleting pack %s: %w", name, err)
	}

	return tx.Commit()
}

// insertResources inserts resource rows and their associated tags, roles,
// and requires entries within a transaction.
func insertResources(tx *sql.Tx, packID int64, resources []Resource, source string) error {
	for _, r := range resources {
		rRes, err := tx.Exec(`INSERT INTO resources (pack_id, kind, name, description, owner, last_updated, path, body, category, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			packID, r.Kind, r.Name, r.Description, r.Owner, r.LastUpdated, r.Path, r.Body, r.Category, source)
		if err != nil {
			return fmt.Errorf("inserting resource %s/%s: %w", r.Kind, r.Name, err)
		}
		resID, _ := rRes.LastInsertId()
		for _, tag := range r.Tags {
			if _, err := tx.Exec("INSERT INTO tags (resource_id, tag) VALUES (?, ?)", resID, tag); err != nil {
				return fmt.Errorf("inserting tag %s for %s: %w", tag, r.Name, err)
			}
		}
		for _, role := range r.Roles {
			if _, err := tx.Exec("INSERT INTO roles (resource_id, role) VALUES (?, ?)", resID, role); err != nil {
				return fmt.Errorf("inserting role %s for %s: %w", role, r.Name, err)
			}
		}
		for _, req := range r.Requires {
			kind, target := parseRequires(req)
			if _, err := tx.Exec("INSERT INTO requires (resource_id, kind, target) VALUES (?, ?, ?)", resID, kind, target); err != nil {
				return fmt.Errorf("inserting requires %s for %s: %w", req, r.Name, err)
			}
		}
	}
	return nil
}

// parseRequires splits "mcp:atlassian" into ("mcp", "atlassian").
// If no colon, kind defaults to "pack".
func parseRequires(s string) (kind, target string) {
	for i, c := range s {
		if c == ':' {
			return s[:i], s[i+1:]
		}
	}
	return "pack", s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
