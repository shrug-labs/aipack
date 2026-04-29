package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/util"
)

const integrityFileName = ".aipack-integrity.json"

// IntegrityManifest records SHA256 hashes of all files in a pack at install time.
type IntegrityManifest struct {
	Files map[string]string `json:"files"` // relative path -> hex SHA256
}

// computeIntegrity walks packDir and computes SHA256 for every regular file,
// skipping the integrity file itself and ignored names.
func computeIntegrity(packDir string) (IntegrityManifest, error) {
	m := IntegrityManifest{Files: make(map[string]string)}
	err := filepath.WalkDir(packDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == integrityFileName {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed in pack content: %s", path)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(packDir, path)
		if err != nil {
			return err
		}
		hash, err := util.FileDigest(path)
		if err != nil {
			return err
		}
		m.Files[filepath.ToSlash(rel)] = hash
		return nil
	})
	return m, err
}

// saveIntegrity computes and writes the integrity manifest to packDir,
// returning the computed manifest so callers can diff without re-reading.
func saveIntegrity(packDir string) (IntegrityManifest, error) {
	m, err := computeIntegrity(packDir)
	if err != nil {
		return IntegrityManifest{}, fmt.Errorf("computing integrity: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return IntegrityManifest{}, err
	}
	data = append(data, '\n')
	if err := util.WriteFileAtomicWithPerms(filepath.Join(packDir, integrityFileName), data, 0o700, 0o600); err != nil {
		return IntegrityManifest{}, err
	}
	return m, nil
}

// loadIntegrity reads the integrity manifest from packDir.
// Returns an empty manifest (not an error) if the file doesn't exist.
func loadIntegrity(packDir string) (IntegrityManifest, error) {
	path := filepath.Join(packDir, integrityFileName)
	data, exists, err := util.ReadFileIfExists(path)
	if err != nil {
		return IntegrityManifest{}, err
	}
	if !exists {
		return IntegrityManifest{Files: make(map[string]string)}, nil
	}
	var m IntegrityManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return IntegrityManifest{}, err
	}
	if m.Files == nil {
		m.Files = make(map[string]string)
	}
	return m, nil
}

// IntegrityCheckResult describes the outcome of verifying a pack's integrity.
type IntegrityCheckResult struct {
	Modified []string `json:"modified,omitempty"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
}

// HasChanges reports whether any files were modified, added, or removed.
func (r IntegrityCheckResult) HasChanges() bool {
	return len(r.Modified) > 0 || len(r.Added) > 0 || len(r.Removed) > 0
}

// diffIntegrity compares two integrity manifests and returns the differences.
func diffIntegrity(old, new IntegrityManifest) IntegrityCheckResult {
	var r IntegrityCheckResult

	for path, oldHash := range old.Files {
		newHash, exists := new.Files[path]
		if !exists {
			r.Removed = append(r.Removed, path)
		} else if oldHash != newHash {
			r.Modified = append(r.Modified, path)
		}
	}
	for path := range new.Files {
		if _, exists := old.Files[path]; !exists {
			r.Added = append(r.Added, path)
		}
	}

	slices.Sort(r.Modified)
	slices.Sort(r.Added)
	slices.Sort(r.Removed)
	return r
}

// formatIntegrityDiff returns the multi-line per-file diff body (sans the
// "Changes:\n" header). Each line is one of "  + path", "  ~ path", "  - path"
// terminated with "\n". Returns "" when the diff has no changes.
func formatIntegrityDiff(diff IntegrityCheckResult) string {
	if !diff.HasChanges() {
		return ""
	}
	var sb strings.Builder
	for _, f := range diff.Added {
		fmt.Fprintf(&sb, "  + %s\n", f)
	}
	for _, f := range diff.Modified {
		fmt.Fprintf(&sb, "  ~ %s\n", f)
	}
	for _, f := range diff.Removed {
		fmt.Fprintf(&sb, "  - %s\n", f)
	}
	return sb.String()
}

// saveAndDiffIntegrity saves the integrity manifest for packDir and, if
// oldIntegrity is non-empty, emits an integrity.changed event with the full
// diff body. Returns the new manifest, whether any content changed, and
// whether a save error occurred. When saveErr is true, callers should not
// trust the changed flag (it is conservatively set to true so updates are
// not skipped).
func saveAndDiffIntegrity(packDir string, oldIntegrity IntegrityManifest, stdout io.Writer) (newManifest IntegrityManifest, changed bool, saveErr bool) {
	newIntegrity, err := saveIntegrity(packDir)
	if err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to record integrity: %v\n", err)
		}
		// Return changed=true so callers don't skip an update due to our failure.
		return newIntegrity, true, true
	}
	if len(oldIntegrity.Files) == 0 {
		return newIntegrity, false, false
	}
	diff := diffIntegrity(oldIntegrity, newIntegrity)
	if diff.HasChanges() {
		if stdout != nil {
			fmt.Fprintf(stdout, "Changes:\n%s", formatIntegrityDiff(diff))
		}
	}
	return newIntegrity, diff.HasChanges(), false
}
