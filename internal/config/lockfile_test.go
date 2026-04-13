package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestLockfile_LoadSave_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aipack.lock")

	original := Lockfile{
		LockVersion: LockfileVersion,
		Packs: map[string]InstalledPackMeta{
			"essentials": {
				Origin:      "https://github.com/shrug-labs/packs",
				Method:      MethodClone,
				SubPath:     "packs/essentials",
				CommitHash:  "abc1234",
				Ref:         "v1.2.3",
				InstalledAt: "2026-04-12T00:00:00Z",
				Approved:    []domain.BundledCategory{"profiles"},
			},
			"team-pack": {
				Origin:      "git@example.com:team/pack.git",
				Method:      MethodClone,
				Ref:         "main",
				CommitHash:  "def5678",
				InstalledAt: "2026-04-11T00:00:00Z",
			},
		},
	}

	if err := SaveLockfile(path, original); err != nil {
		t.Fatalf("SaveLockfile: %v", err)
	}
	loaded, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}

	if len(loaded.Packs) != 2 {
		t.Fatalf("got %d packs, want 2", len(loaded.Packs))
	}
	ess := loaded.Packs["essentials"]
	if ess.Ref != "v1.2.3" || ess.CommitHash != "abc1234" || ess.Method != MethodClone {
		t.Errorf("essentials = %+v", ess)
	}
	if ess.SubPath != "packs/essentials" {
		t.Errorf("essentials SubPath = %q", ess.SubPath)
	}
	team := loaded.Packs["team-pack"]
	if team.Ref != "main" {
		t.Errorf("team-pack Ref = %q, want \"main\" (tracking HEAD)", team.Ref)
	}
	if team.CommitHash != "def5678" {
		t.Errorf("team-pack CommitHash = %q", team.CommitHash)
	}
}

func TestLockfile_RejectsFutureLockVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aipack.lock")
	// Hand-write a lockfile with a higher lock_version than this binary
	// supports. A v1 binary must refuse rather than silently round-trip
	// the file through SaveLockfile (which would rewrite lock_version
	// back to 1 and drop unknown fields).
	if err := os.WriteFile(path, []byte("lock_version: 999\npacks: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLockfile(path); err == nil {
		t.Fatal("expected error loading future-version lockfile")
	}
}

func TestLockfile_AcceptsUnstampedLockVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aipack.lock")
	// Files written without an explicit lock_version (e.g. hand-edited or
	// produced by a pre-versioning prototype) should still load — only an
	// explicit, mismatched version is rejected.
	if err := os.WriteFile(path, []byte("packs:\n  my-pack:\n    origin: x\n    method: clone\n    installed_at: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lf, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lf.Packs["my-pack"]; !ok {
		t.Error("expected my-pack to load from unstamped lockfile")
	}
}

func TestLockfile_LoadMissing(t *testing.T) {
	t.Parallel()
	lf, err := LoadLockfile(filepath.Join(t.TempDir(), "nonexistent.lock"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lf.LockVersion != LockfileVersion {
		t.Errorf("LockVersion = %d, want %d", lf.LockVersion, LockfileVersion)
	}
	if len(lf.Packs) != 0 {
		t.Errorf("expected empty packs, got %d", len(lf.Packs))
	}
}

func TestMigratePackStateToLockfile_FreshConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No lockfile, no sync-config — nothing to migrate.
	if err := MigratePackStateToLockfile(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Lockfile should not be created when there's nothing to migrate.
	if _, err := os.Stat(LockfilePath(dir)); !os.IsNotExist(err) {
		t.Error("lockfile should not exist after no-op migration")
	}
}

func TestMigratePackStateToLockfile_MigratesFromSyncConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write sync-config with installed_packs, no lockfile.
	sc := SyncConfig{
		SchemaVersion: SyncConfigSchemaVersion,
		InstalledPacks: map[string]InstalledPackMeta{
			"migrated": {
				Origin:     "https://example.com/pack.git",
				Method:     MethodClone,
				CommitHash: "abc123",
			},
		},
	}
	if err := SaveSyncConfig(SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}

	if err := MigratePackStateToLockfile(dir); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify lockfile was created with correct data.
	lf, err := LoadLockfile(LockfilePath(dir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	if len(lf.Packs) != 1 {
		t.Fatalf("got %d packs, want 1", len(lf.Packs))
	}
	if lf.Packs["migrated"].Ref != "" {
		t.Errorf("ref = %q, want empty (no version intent)", lf.Packs["migrated"].Ref)
	}
	if lf.Packs["migrated"].CommitHash != "abc123" {
		t.Errorf("commit_hash = %q", lf.Packs["migrated"].CommitHash)
	}

	// Verify sync-config no longer has installed_packs.
	reloaded, err := LoadSyncConfig(SyncConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.InstalledPacks) != 0 {
		t.Errorf("sync-config still has %d installed_packs after migration", len(reloaded.InstalledPacks))
	}
}

func TestMigratePackStateToLockfile_MergesWithExistingLockfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Lockfile already has an entry (e.g. created by a fresh `pack install`
	// running before doctor on an upgraded config).
	lf := Lockfile{
		LockVersion: LockfileVersion,
		Packs: map[string]InstalledPackMeta{
			"from-lock": {Origin: "lock", Method: MethodClone},
			"shared":    {Origin: "lockfile-wins", Method: MethodClone, CommitHash: "new"},
		},
	}
	if err := SaveLockfile(LockfilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	// Sync-config still has legacy installed_packs from before the upgrade.
	sc := SyncConfig{
		SchemaVersion: SyncConfigSchemaVersion,
		InstalledPacks: map[string]InstalledPackMeta{
			"from-sc": {Origin: "sc", Method: MethodClone},
			"shared":  {Origin: "stale-sc-version", Method: MethodClone, CommitHash: "old"},
		},
	}
	if err := SaveSyncConfig(SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}

	if err := MigratePackStateToLockfile(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded, err := LoadLockfile(LockfilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Both unique packs survive.
	if _, ok := loaded.Packs["from-lock"]; !ok {
		t.Error("lockfile-only pack should be preserved")
	}
	if _, ok := loaded.Packs["from-sc"]; !ok {
		t.Error("sync-config pack should be merged into the lockfile")
	}
	// Conflict resolution: lockfile entry wins so post-migration updates
	// aren't clobbered by stale sync-config copies.
	if loaded.Packs["shared"].CommitHash != "new" {
		t.Errorf("shared pack commit_hash = %q, want lockfile value 'new'", loaded.Packs["shared"].CommitHash)
	}

	// Sync-config should be cleared.
	reloadedSC, err := LoadSyncConfig(SyncConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedSC.InstalledPacks) != 0 {
		t.Errorf("sync-config still has %d installed_packs after merge", len(reloadedSC.InstalledPacks))
	}
}

func TestMigratePackStateToLockfile_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write sync-config with installed_packs.
	sc := SyncConfig{
		SchemaVersion: SyncConfigSchemaVersion,
		InstalledPacks: map[string]InstalledPackMeta{
			"my-pack": {Origin: "https://example.com/pack.git", Method: MethodClone},
		},
	}
	if err := SaveSyncConfig(SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}

	// First migration.
	if err := MigratePackStateToLockfile(dir); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	// Second migration should be a no-op — sync-config installed_packs was
	// cleared by the first call.
	if err := MigratePackStateToLockfile(dir); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	// Still one pack in lockfile.
	lf, err := LoadLockfile(LockfilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Packs) != 1 {
		t.Fatalf("got %d packs after double migration, want 1", len(lf.Packs))
	}
}
