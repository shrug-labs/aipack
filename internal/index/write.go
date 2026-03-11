package index

import (
	"database/sql"
	"fmt"
)

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
	Kind        string // rule, skill, workflow, agent
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

	if err := insertResources(tx, packID, resources); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateRegistryPacks upserts registry entries as uninstalled packs.
// Already-installed packs are not downgraded; only source coordinates
// (repo, ref, path, contact) are refreshed.
func (db *DB) UpdateRegistryPacks(packs []PackInfo) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning registry index update: %w", err)
	}
	defer tx.Rollback()

	for _, p := range packs {
		_, err := tx.Exec(`INSERT INTO packs (name, version, description, repo, ref, path, owner, contact, installed, source)
			VALUES (?, '', ?, ?, ?, ?, ?, ?, 0, 'registry')
			ON CONFLICT(name) DO UPDATE SET
				repo=excluded.repo,
				ref=excluded.ref,
				path=excluded.path,
				contact=excluded.contact,
				description=CASE WHEN packs.installed = 0 THEN excluded.description ELSE packs.description END,
				owner=CASE WHEN packs.installed = 0 THEN excluded.owner ELSE packs.owner END`,
			p.Name, p.Description, p.Repo, p.Ref, p.Path, p.Owner, p.Contact)
		if err != nil {
			return fmt.Errorf("upserting registry pack %s: %w", p.Name, err)
		}
	}

	return tx.Commit()
}

// UpdateDeepIndex upserts resource-level metadata for uninstalled packs.
// Already-installed packs are skipped — sync provides richer data.
// Source is set to "deep-index" so it can be distinguished from sync data.
func (db *DB) UpdateDeepIndex(pack PackInfo, resources []Resource) error {
	// Skip if this pack is already installed — sync data is authoritative.
	var installed int
	err := db.QueryRow("SELECT installed FROM packs WHERE name=?", pack.Name).Scan(&installed)
	if err == nil && installed == 1 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning deep index update: %w", err)
	}
	defer tx.Rollback()

	// Upsert pack as uninstalled with deep-index source.
	_, err = tx.Exec(`INSERT INTO packs (name, version, description, repo, ref, path, owner, contact, installed, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 'deep-index')
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,
			description=CASE WHEN excluded.description != '' THEN excluded.description ELSE packs.description END,
			repo=CASE WHEN excluded.repo != '' THEN excluded.repo ELSE packs.repo END,
			ref=CASE WHEN excluded.ref != '' THEN excluded.ref ELSE packs.ref END,
			path=CASE WHEN excluded.path != '' THEN excluded.path ELSE packs.path END,
			owner=CASE WHEN excluded.owner != '' THEN excluded.owner ELSE packs.owner END,
			contact=CASE WHEN excluded.contact != '' THEN excluded.contact ELSE packs.contact END,
			source='deep-index'`,
		pack.Name, pack.Version, pack.Description, pack.Repo, pack.Ref, pack.Path,
		pack.Owner, pack.Contact)
	if err != nil {
		return fmt.Errorf("upserting deep-index pack %s: %w", pack.Name, err)
	}

	var packID int64
	if err := tx.QueryRow("SELECT id FROM packs WHERE name=?", pack.Name).Scan(&packID); err != nil {
		return fmt.Errorf("looking up pack id for %s: %w", pack.Name, err)
	}

	// Replace old resources for this pack.
	if _, err := tx.Exec("DELETE FROM resources WHERE pack_id=?", packID); err != nil {
		return fmt.Errorf("clearing old resources for %s: %w", pack.Name, err)
	}

	if err := insertResources(tx, packID, resources); err != nil {
		return err
	}

	return tx.Commit()
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
func insertResources(tx *sql.Tx, packID int64, resources []Resource) error {
	for _, r := range resources {
		rRes, err := tx.Exec(`INSERT INTO resources (pack_id, kind, name, description, owner, last_updated, path, body, category)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			packID, r.Kind, r.Name, r.Description, r.Owner, r.LastUpdated, r.Path, r.Body, r.Category)
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
