package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
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
// it returns an empty index and a warning (non-fatal).
func (e *Engine) loadPresyncIndex(dir string) (presyncIndex, []domain.Warning, error) {
	b, err := e.FS.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return presyncIndex{}, nil, nil
		}
		return nil, nil, err
	}
	var idx presyncIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return presyncIndex{}, []domain.Warning{{
			Field:   "settings-cache",
			Message: fmt.Sprintf("presync index corrupt, starting fresh: %v", err),
		}}, nil
	}
	return idx, nil, nil
}

func (e *Engine) savePresyncIndex(dir string, idx presyncIndex) error {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(dir, "index.json")
	if err := e.FS.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return e.FS.WriteFile(path, b, 0o600)
}

// SnapshotSettingsFiles copies current on-disk settings files into the
// presync cache before sync overwrites them. Stale entries from previous
// syncs (e.g. for removed harnesses) are purged. Only one level of undo
// is available — each sync replaces the previous cache.
func (e *Engine) SnapshotSettingsFiles(settings []domain.SettingsAction, ledgerPath string, dryRun bool) ([]domain.Warning, error) {
	if dryRun || len(settings) == 0 {
		return nil, nil
	}

	dir := presyncDir(ledgerPath)
	idx, warnings, err := e.loadPresyncIndex(dir)
	if err != nil {
		return nil, fmt.Errorf("loading presync index: %w", err)
	}

	// Build set of keys we expect this sync to produce.
	currentKeys := map[string]struct{}{}
	for _, s := range settings {
		currentKeys[settingsCacheKey(s.Harness, filepath.Clean(s.Dst))] = struct{}{}
	}
	// Purge stale entries from previous syncs that targeted different harnesses/files.
	purged := false
	for key := range idx {
		if _, ok := currentKeys[key]; !ok {
			if err := e.FS.Remove(filepath.Join(dir, key)); err != nil && !os.IsNotExist(err) {
				warnings = append(warnings, domain.Warning{
					Field:   "settings-cache",
					Message: fmt.Sprintf("could not purge stale cache entry %s: %v", key, err),
				})
				continue
			}
			delete(idx, key)
			purged = true
		}
	}

	dirCreated := false
	dirty := purged
	for _, s := range settings {
		dst := filepath.Clean(s.Dst)
		content, err := e.FS.ReadFile(dst)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		if !dirCreated {
			if err := e.FS.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			dirCreated = true
		}

		key := settingsCacheKey(s.Harness, dst)
		if err := e.FS.WriteFile(filepath.Join(dir, key), content, 0o600); err != nil {
			return nil, fmt.Errorf("caching %s: %w", key, err)
		}
		idx[key] = dst
		dirty = true
	}

	if dirty {
		return warnings, e.savePresyncIndex(dir, idx)
	}
	return warnings, nil
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
func (e *Engine) RestoreFromCache(ledgerPath, filterHarness string, dryRun bool) ([]RestoredFile, error) {
	var restored []RestoredFile
	dirs := []string{presyncDir(ledgerPath)}
	if legacy := legacyPresyncDir(ledgerPath); legacy != dirs[0] {
		dirs = append(dirs, legacy)
	}

	for _, dir := range dirs {
		idx, _, err := e.loadPresyncIndex(dir)
		if err != nil {
			return nil, fmt.Errorf("loading presync index: %w", err)
		}
		if len(idx) == 0 {
			continue
		}

		keys := slices.Sorted(maps.Keys(idx))

		var dirRestored []RestoredFile
		for _, key := range keys {
			origPath := idx[key]
			if filterHarness != "" && !strings.HasPrefix(key, filterHarness+"--") {
				continue
			}

			cached, err := e.FS.ReadFile(filepath.Join(dir, key))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			if !dryRun {
				if err := e.FS.MkdirAll(filepath.Dir(origPath), 0o700); err != nil {
					return nil, err
				}
				if err := e.FS.WriteFile(origPath, cached, 0o644); err != nil {
					return nil, err
				}
			}
			dirRestored = append(dirRestored, RestoredFile{CacheKey: key, OriginalPath: origPath})
		}

		if !dryRun && len(dirRestored) > 0 {
			for _, r := range dirRestored {
				_ = e.FS.Remove(filepath.Join(dir, r.CacheKey))
				delete(idx, r.CacheKey)
			}
			if len(idx) == 0 {
				_ = e.FS.Remove(filepath.Join(dir, "index.json"))
				_ = e.FS.Remove(dir)
			} else if err := e.savePresyncIndex(dir, idx); err != nil {
				return append(restored, dirRestored...), fmt.Errorf("updating presync index after restore: %w", err)
			}
		}

		restored = append(restored, dirRestored...)
	}

	return restored, nil
}
