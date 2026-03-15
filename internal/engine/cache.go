package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

const cacheSubdirName = "presync"

// settingsCacheKey derives a deterministic, readable cache key from harness and dst path.
// Uses dir basename + filename to disambiguate files with the same name in
// different directories (e.g. .claude/settings.local.json vs .config/claude/settings.local.json).
func settingsCacheKey(harness domain.Harness, dst string) string {
	dir := filepath.Base(filepath.Dir(dst))
	base := filepath.Base(dst)
	return string(harness) + "--" + dir + "--" + base
}

// presyncDir returns the presync cache directory alongside the ledger.
func presyncDir(ledgerPath string) string {
	base := strings.TrimSuffix(filepath.Base(ledgerPath), filepath.Ext(ledgerPath))
	if base == "" {
		base = "ledger"
	}
	return filepath.Join(filepath.Dir(ledgerPath), base+"-"+cacheSubdirName)
}

func legacyPresyncDir(ledgerPath string) string {
	return filepath.Join(filepath.Dir(ledgerPath), cacheSubdirName)
}

// presyncIndex maps cache keys to their original file paths.
type presyncIndex map[string]string

// loadPresyncIndex loads the presync cache index. If the index is corrupt,
// it returns an empty index and a warning string (non-fatal).
func loadPresyncIndex(dir string) (presyncIndex, string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return presyncIndex{}, "", nil
		}
		return nil, "", err
	}
	var idx presyncIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return presyncIndex{}, fmt.Sprintf("warning: presync index corrupt, starting fresh: %v", err), nil
	}
	return idx, "", nil
}

func savePresyncIndex(dir string, idx presyncIndex) error {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return util.WriteFileAtomicWithPerms(filepath.Join(dir, "index.json"), b, 0o700, 0o600)
}

// SnapshotSettingsFiles copies current on-disk settings files into the
// presync cache before sync overwrites them. Stale entries from previous
// syncs (e.g. for removed harnesses) are purged. Only one level of undo
// is available — each sync replaces the previous cache.
func SnapshotSettingsFiles(settings []domain.SettingsAction, ledgerPath string, dryRun bool) (string, error) {
	if dryRun || len(settings) == 0 {
		return "", nil
	}

	dir := presyncDir(ledgerPath)
	idx, warn, err := loadPresyncIndex(dir)
	if err != nil {
		return "", fmt.Errorf("loading presync index: %w", err)
	}

	// Build set of keys we expect this sync to produce.
	currentKeys := map[string]struct{}{}
	for _, s := range settings {
		currentKeys[settingsCacheKey(s.Harness, filepath.Clean(s.Dst))] = struct{}{}
	}
	// Purge stale entries from previous syncs that targeted different harnesses/files.
	purged := false
	var purgeWarnings []string
	for key := range idx {
		if _, ok := currentKeys[key]; !ok {
			if err := os.Remove(filepath.Join(dir, key)); err != nil && !os.IsNotExist(err) {
				// File could not be removed (permissions, etc.) — keep the index
				// entry so we don't lose track of it.
				purgeWarnings = append(purgeWarnings, fmt.Sprintf("could not purge stale cache entry %s: %v", key, err))
				continue
			}
			delete(idx, key)
			purged = true
		}
	}
	if len(purgeWarnings) > 0 && warn == "" {
		warn = "warning: " + strings.Join(purgeWarnings, "; ")
	}

	dirCreated := false
	dirty := purged
	for _, s := range settings {
		dst := filepath.Clean(s.Dst)
		content, err := os.ReadFile(dst)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}

		if !dirCreated {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
			dirCreated = true
		}

		key := settingsCacheKey(s.Harness, dst)
		if err := util.WriteFileAtomicWithPerms(filepath.Join(dir, key), content, 0o700, 0o600); err != nil {
			return "", fmt.Errorf("caching %s: %w", key, err)
		}
		idx[key] = dst
		dirty = true
	}

	if dirty {
		return warn, savePresyncIndex(dir, idx)
	}
	return warn, nil
}

// RestoredFile records a file restored from cache.
type RestoredFile struct {
	CacheKey     string
	OriginalPath string
}

// RestoreFromCache restores settings files from the presync cache.
// When filterHarness is non-empty, only files for that harness are restored.
// After a successful non-dry-run restore, the restored entries are removed
// from the cache so that a second restore is a no-op.
func RestoreFromCache(ledgerPath, filterHarness string, dryRun bool) ([]RestoredFile, error) {
	var restored []RestoredFile
	dirs := []string{presyncDir(ledgerPath)}
	if legacy := legacyPresyncDir(ledgerPath); legacy != dirs[0] {
		dirs = append(dirs, legacy)
	}

	for _, dir := range dirs {
		idx, _, err := loadPresyncIndex(dir)
		if err != nil {
			return nil, fmt.Errorf("loading presync index: %w", err)
		}
		if len(idx) == 0 {
			continue
		}

		keys := make([]string, 0, len(idx))
		for k := range idx {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var dirRestored []RestoredFile
		for _, key := range keys {
			origPath := idx[key]
			if filterHarness != "" && !strings.HasPrefix(key, filterHarness+"--") {
				continue
			}

			cached, err := os.ReadFile(filepath.Join(dir, key))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			if !dryRun {
				if err := os.MkdirAll(filepath.Dir(origPath), 0o700); err != nil {
					return nil, err
				}
				if err := util.WriteFileAtomic(origPath, cached); err != nil {
					return nil, err
				}
			}
			dirRestored = append(dirRestored, RestoredFile{CacheKey: key, OriginalPath: origPath})
		}

		if !dryRun && len(dirRestored) > 0 {
			for _, r := range dirRestored {
				_ = os.Remove(filepath.Join(dir, r.CacheKey))
				delete(idx, r.CacheKey)
			}
			if len(idx) == 0 {
				_ = os.Remove(filepath.Join(dir, "index.json"))
				_ = os.Remove(dir)
			} else if err := savePresyncIndex(dir, idx); err != nil {
				return append(restored, dirRestored...), fmt.Errorf("updating presync index after restore: %w", err)
			}
		}

		restored = append(restored, dirRestored...)
	}

	return restored, nil
}
