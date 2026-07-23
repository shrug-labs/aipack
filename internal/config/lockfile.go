package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/util"
	"gopkg.in/yaml.v3"
)

const LockfileVersion = 1

// Lockfile records the resolved state of all installed packs.
// Machine-generated. LockVersion is bumped only for incompatible format
// changes; additive fields are tolerated by the YAML decoder.
type Lockfile struct {
	LockVersion int                          `yaml:"lock_version"`
	Packs       map[string]InstalledPackMeta `yaml:"packs,omitempty"`
}

// LockfilePath returns the path to the lockfile in configDir.
func LockfilePath(configDir string) string {
	return filepath.Join(configDir, "aipack.lock")
}

// EnsureLockfileMigrated runs MigratePackStateToLockfile and then loads the
// lockfile. Production entry point — guarantees legacy sync-config
// installed_packs are merged before the read. Tests wanting a pure read
// call LoadLockfile directly.
func EnsureLockfileMigrated(configDir string) (Lockfile, error) {
	if err := MigratePackStateToLockfile(configDir); err != nil {
		return Lockfile{}, err
	}
	return LoadLockfile(LockfilePath(configDir))
}

// LoadLockfileReadOnlyMerged returns the same effective installed-pack state
// as migration without writing either aipack.lock or sync-config.yaml.
// Lockfile entries win over vestigial sync-config installed_packs entries.
func LoadLockfileReadOnlyMerged(configDir string) (Lockfile, error) {
	lf, err := LoadLockfile(LockfilePath(configDir))
	if err != nil {
		return Lockfile{}, err
	}
	sc, err := LoadSyncConfig(SyncConfigPath(configDir))
	if err != nil {
		return Lockfile{}, err
	}
	mergeInstalledPackState(&lf, sc.InstalledPacks)
	return lf, nil
}

func mergeInstalledPackState(lf *Lockfile, legacy map[string]InstalledPackMeta) bool {
	if lf.Packs == nil {
		lf.Packs = make(map[string]InstalledPackMeta)
	}
	merged := false
	for name, meta := range legacy {
		if _, exists := lf.Packs[name]; exists {
			continue
		}
		lf.Packs[name] = meta
		merged = true
	}
	return merged
}

// LoadLockfile reads and parses the lockfile. Returns a zero-value Lockfile
// when the file does not exist. Pure read — does not run migration.
func LoadLockfile(path string) (Lockfile, error) {
	lf := Lockfile{LockVersion: LockfileVersion, Packs: make(map[string]InstalledPackMeta)}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lf, nil
		}
		return Lockfile{}, fmt.Errorf("reading lockfile: %w", err)
	}
	if err := yaml.Unmarshal(b, &lf); err != nil {
		return Lockfile{}, fmt.Errorf("parsing lockfile: %w", err)
	}
	// Refuse to silently downgrade a future-version lockfile. SaveLockfile
	// rewrites LockVersion to the current constant, so a v1 binary reading a
	// v2 file would round-trip it back to v1 on the next save and may drop
	// fields it doesn't recognize. lock_version=0 is treated as "unset"
	// (legacy file with no version stamp) and accepted.
	if lf.LockVersion != 0 && lf.LockVersion != LockfileVersion {
		return Lockfile{}, fmt.Errorf("unsupported lockfile lock_version %d (expected %d): upgrade aipack to read this file", lf.LockVersion, LockfileVersion)
	}
	if lf.Packs == nil {
		lf.Packs = make(map[string]InstalledPackMeta)
	}
	return lf, nil
}

// SaveLockfile atomically writes the lockfile. Skips the write when the
// marshaled bytes match the existing file, so no-op mutations don't churn
// mtime in user-tracked config directories.
func SaveLockfile(path string, lf Lockfile) error {
	lf.LockVersion = LockfileVersion
	out, err := yaml.Marshal(&lf)
	if err != nil {
		return fmt.Errorf("marshalling lockfile: %w", err)
	}
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, out) {
		return nil
	}
	return util.WriteFileAtomicWithPerms(path, out, 0o700, 0o600)
}

// MigratePackStateToLockfile merges installed pack metadata from sync-config
// into the lockfile. Idempotent. Lockfile entries win on conflict so user
// updates made post-migration aren't clobbered by a stale sync-config copy.
func MigratePackStateToLockfile(configDir string) error {
	scPath := SyncConfigPath(configDir)
	sc, err := LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if len(sc.InstalledPacks) == 0 {
		return nil
	}

	lfPath := LockfilePath(configDir)
	lf, err := LoadLockfile(lfPath)
	if err != nil {
		return err
	}
	merged := mergeInstalledPackState(&lf, sc.InstalledPacks)
	if merged {
		if err := SaveLockfile(lfPath, lf); err != nil {
			return fmt.Errorf("writing lockfile during migration: %w", err)
		}
	}
	sc.InstalledPacks = nil
	if err := SaveSyncConfig(scPath, sc); err != nil {
		return fmt.Errorf("clearing legacy installed_packs from sync-config: %w", err)
	}
	return nil
}
