package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite index database.
type DB struct {
	*sql.DB
}

// schemaVersion is bumped when the DDL changes. On mismatch the index is
// dropped and recreated — it is a rebuildable cache, not durable state.
const schemaVersion = 3

// Open opens (or creates) the index database at path, applying schema migrations.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating index dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening index db: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("setting journal mode: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate() error {
	var version int
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)

	if version < schemaVersion {
		// Schema changed — drop and recreate. The index is a rebuildable cache:
		// pack content is re-indexed on next sync, registry entries on next fetch.
		drops := []string{
			"DROP TRIGGER IF EXISTS resources_au",
			"DROP TRIGGER IF EXISTS resources_ad",
			"DROP TRIGGER IF EXISTS resources_ai",
			"DROP TRIGGER IF EXISTS packs_au",
			"DROP TRIGGER IF EXISTS packs_ad",
			"DROP TRIGGER IF EXISTS packs_ai",
			"DROP TABLE IF EXISTS resources_fts",
			"DROP TABLE IF EXISTS packs_fts",
			"DROP TABLE IF EXISTS requires",
			"DROP TABLE IF EXISTS roles",
			"DROP TABLE IF EXISTS tags",
			"DROP TABLE IF EXISTS resources",
			"DROP TABLE IF EXISTS packs",
		}
		for _, stmt := range drops {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("dropping old schema: %w", err)
			}
		}
	}

	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("setting schema version: %w", err)
	}
	return nil
}

const schemaDDL = `
CREATE TABLE IF NOT EXISTS packs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT UNIQUE NOT NULL,
	version TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	repo TEXT NOT NULL DEFAULT '',
	ref TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	owner TEXT NOT NULL DEFAULT '',
	contact TEXT NOT NULL DEFAULT '',
	installed INTEGER NOT NULL DEFAULT 1,
	source TEXT NOT NULL DEFAULT 'sync'
);

CREATE TABLE IF NOT EXISTS resources (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pack_id INTEGER NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	owner TEXT NOT NULL DEFAULT '',
	last_updated TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS tags (
	resource_id INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
	tag TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS roles (
	resource_id INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
	role TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requires (
	resource_id INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	target TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resources_pack_id ON resources(pack_id);
CREATE INDEX IF NOT EXISTS idx_tags_resource_id ON tags(resource_id);
CREATE INDEX IF NOT EXISTS idx_roles_resource_id ON roles(resource_id);
CREATE INDEX IF NOT EXISTS idx_requires_resource_id ON requires(resource_id);

CREATE VIRTUAL TABLE IF NOT EXISTS resources_fts USING fts5(
	name, description, body, content=resources, content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS resources_ai AFTER INSERT ON resources BEGIN
	INSERT INTO resources_fts(rowid, name, description, body) VALUES (new.id, new.name, new.description, new.body);
END;

CREATE TRIGGER IF NOT EXISTS resources_ad AFTER DELETE ON resources BEGIN
	INSERT INTO resources_fts(resources_fts, rowid, name, description, body) VALUES ('delete', old.id, old.name, old.description, old.body);
END;

CREATE TRIGGER IF NOT EXISTS resources_au AFTER UPDATE ON resources BEGIN
	INSERT INTO resources_fts(resources_fts, rowid, name, description, body) VALUES ('delete', old.id, old.name, old.description, old.body);
	INSERT INTO resources_fts(rowid, name, description, body) VALUES (new.id, new.name, new.description, new.body);
END;

CREATE VIRTUAL TABLE IF NOT EXISTS packs_fts USING fts5(
	name, description, content=packs, content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS packs_ai AFTER INSERT ON packs BEGIN
	INSERT INTO packs_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;

CREATE TRIGGER IF NOT EXISTS packs_ad AFTER DELETE ON packs BEGIN
	INSERT INTO packs_fts(packs_fts, rowid, name, description) VALUES ('delete', old.id, old.name, old.description);
END;

CREATE TRIGGER IF NOT EXISTS packs_au AFTER UPDATE ON packs BEGIN
	INSERT INTO packs_fts(packs_fts, rowid, name, description) VALUES ('delete', old.id, old.name, old.description);
	INSERT INTO packs_fts(rowid, name, description) VALUES (new.id, new.name, new.description);
END;
`
