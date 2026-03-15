package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

func ledgerPathForScope(scope domain.Scope, projectDir, home string, h domain.Harness) string {
	return engine.LedgerPathForScope(scope, projectDir, home, strings.ToLower(string(h)))
}

func saveFileToPack(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return saveContentToPack(b, dst)
}

func saveContentToPack(content []byte, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return util.WriteFileAtomic(dst, content)
}

func saveDirToPack(src, dst string) error {
	// Collect source-relative paths so we can prune stale dst files.
	srcFiles := map[string]struct{}{}
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		srcFiles[rel] = struct{}{}
		return saveFileToPack(p, target)
	})
	if err != nil {
		return err
	}

	// Remove files in dst that no longer exist in src.
	if _, statErr := os.Stat(dst); statErr != nil {
		return nil // dst doesn't exist yet, nothing to prune
	}
	return filepath.WalkDir(dst, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dst, p)
		if err != nil {
			return err
		}
		if _, exists := srcFiles[rel]; !exists {
			return os.Remove(p)
		}
		return nil
	})
}

func desiredBytesForCopy(c domain.CopyAction, res harness.CaptureResult, srcContent []byte) ([]byte, error) {
	if c.Kind != domain.CopyKindFile {
		return nil, nil
	}
	kind, _, ok := domain.MatchPrimaryContentFile(c.Dst)
	if !ok {
		return srcContent, nil
	}
	src := filepath.Clean(c.Src)
	switch kind {
	case domain.CategoryRules:
		for _, rule := range res.Rules {
			if filepath.Clean(rule.SourcePath) == src {
				return engine.RenderRuleBytes(rule)
			}
		}
	case domain.CategoryAgents:
		for _, agent := range res.Agents {
			if filepath.Clean(agent.SourcePath) == src {
				return engine.RenderAgentBytes(agent)
			}
		}
	case domain.CategoryWorkflows:
		for _, workflow := range res.Workflows {
			if filepath.Clean(workflow.SourcePath) == src {
				return engine.RenderWorkflowBytes(workflow)
			}
		}
	}
	return srcContent, nil
}

// checkFileConflict returns true if dst exists with content different from srcContent.
func checkFileConflict(srcContent []byte, dst string) (bool, error) {
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !bytes.Equal(srcContent, dstContent), nil
}

func checkMCPConflict(srcContent []byte, dst string) (bool, error) {
	srcTracked, err := trackedMCPBytesFromFile(srcContent)
	if err != nil {
		return true, nil
	}
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	dstTracked, err := trackedMCPBytesFromFile(dstContent)
	if err != nil {
		return true, nil
	}
	return !bytes.Equal(srcTracked, dstTracked), nil
}

// checkDirConflict returns true if any file in srcDir differs from its counterpart under dstDir.
func checkDirConflict(srcDir, dstDir string) (bool, error) {
	if _, err := os.Stat(dstDir); os.IsNotExist(err) {
		return false, nil
	}
	conflict := false
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		srcContent, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		c, err := checkFileConflict(srcContent, filepath.Join(dstDir, rel))
		if err != nil {
			return err
		}
		if c {
			conflict = true
			return filepath.SkipAll
		}
		return nil
	})
	return conflict, err
}

// ---------------------------------------------------------------------------
// Round-trip save: harness → source packs
// ---------------------------------------------------------------------------

// RoundTripRequest holds parameters for a round-trip save.
type RoundTripRequest struct {
	TargetSpec
	PackRoots map[string]string // pack name → resolved root
	DryRun    bool
	Force     bool
	Stderr    io.Writer
}

// RoundTripResult holds the output of a round-trip save.
type RoundTripResult struct {
	SavedFiles      []SavedFile
	PendingSettings []PendingSettingsChange
	Conflicts       []ConflictFile
	UntrackedFiles  []string
	UnchangedCount  int
	CaptureWarnings []domain.Warning
}

// SavedFile records a file saved from harness to pack.
type SavedFile struct {
	HarnessPath string
	PackName    string
	PackPath    string
}

// ConflictFile records a destination file that would be overwritten with different content.
type ConflictFile struct {
	HarnessPath string
	PackPath    string
	PackName    string
}

// PendingSettingsChange records a settings file that changed.
type PendingSettingsChange struct {
	HarnessPath string
	PackName    string
	PackPath    string
	Stripped    []byte
}

// RunRoundTrip captures current harness content and saves changed files
// back to their source packs using ledger provenance.
// Each harness is processed independently with its own per-harness ledger.
func RunRoundTrip(req RoundTripRequest, reg *harness.Registry) (RoundTripResult, error) {
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	home := req.Home
	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}
	var result RoundTripResult

	for _, hid := range req.Harnesses {
		ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, home, hid)
		lg, ledgerWarn, err := engine.LoadLedger(ledgerPath)
		if err != nil {
			return RoundTripResult{}, fmt.Errorf("loading ledger for %s: %w", hid, err)
		}
		if ledgerWarn != "" {
			fmt.Fprintln(stderr, "WARNING: "+ledgerWarn)
		}
		if len(lg.Managed) == 0 {
			result.CaptureWarnings = append(result.CaptureWarnings, domain.Warning{
				Field:   "ledger",
				Message: fmt.Sprintf("no ledger found for %s at %s — run 'aipack sync' first", hid, ledgerPath),
			})
			continue
		}

		h, err := reg.Lookup(hid)
		if err != nil {
			return RoundTripResult{}, err
		}
		res, err := h.Capture(ctx)
		if err != nil {
			return RoundTripResult{}, err
		}
		result.CaptureWarnings = append(result.CaptureWarnings, res.Warnings...)

		savedCount := 0

		// Content files (rules, agents, workflows, skills).
		for _, c := range res.Copies {
			src := filepath.Clean(c.Src)
			entry, tracked := lg.Managed[src]
			if !tracked && c.Kind == domain.CopyKindDir {
				entry, tracked = findChildEntry(src, lg)
			}
			if !tracked {
				result.UntrackedFiles = append(result.UntrackedFiles, src)
				continue
			}

			packName := entry.SourcePack
			if packName == "" {
				if len(req.PackRoots) == 1 {
					for k := range req.PackRoots {
						packName = k
					}
				} else {
					return RoundTripResult{}, fmt.Errorf("empty source_pack for %s and multiple packs configured — run 'aipack sync' first to populate provenance", src)
				}
			}
			packRoot, ok := req.PackRoots[packName]
			if !ok {
				result.UntrackedFiles = append(result.UntrackedFiles, src)
				continue
			}

			// For files, read content once and reuse for change check, save, and digest.
			var srcContent []byte
			var desiredContent []byte
			if c.Kind == domain.CopyKindFile {
				srcContent, err = os.ReadFile(src)
				if err != nil {
					return RoundTripResult{}, err
				}
				desiredContent, err = desiredBytesForCopy(c, res, srcContent)
				if err != nil {
					return RoundTripResult{}, err
				}
			}

			// For directories, the ledger may track individual child files
			// (from sync) or a directory-level entry (from save). Use
			// per-child comparison when the direct lookup returned a child entry.
			var changed bool
			if c.Kind == domain.CopyKindDir && lg.Managed[src].Digest == "" {
				changed = !dirChildrenClean(src, lg)
			} else {
				changed, err = contentChangedSinceLedger(srcContent, src, entry.Digest, c.Kind)
				if err != nil {
					return RoundTripResult{}, err
				}
			}
			if !changed {
				result.UnchangedCount++
				continue
			}

			dst := filepath.Join(packRoot, filepath.FromSlash(c.Dst))

			// Pack-side conflict: check if pack file also diverged from ledger.
			// For directories with per-file ledger entries (from sync), compare
			// each child file individually rather than using a dir-level digest.
			usesChildEntries := c.Kind == domain.CopyKindDir && lg.Managed[src].Digest == ""
			if _, statErr := os.Stat(dst); statErr == nil {
				packChanged := true
				if c.Kind == domain.CopyKindFile {
					matchesDesired, packErr := checkFileConflict(desiredContent, dst)
					if packErr != nil {
						return RoundTripResult{}, packErr
					}
					packChanged = matchesDesired
				}
				if packChanged {
					if usesChildEntries {
						// Per-file comparison using mapped ledger entries:
						// the ledger tracks harness paths, so remap to check
						// pack children against their harness-side digests.
						packChanged = !packDirMatchesLedger(src, dst, lg)
					} else {
						var packErr error
						packChanged, packErr = fileChangedSinceLedger(dst, entry.Digest, c.Kind)
						if packErr != nil {
							return RoundTripResult{}, packErr
						}
					}
				}
				if packChanged {
					result.Conflicts = append(result.Conflicts, ConflictFile{
						HarnessPath: src, PackPath: dst, PackName: packName,
					})
					if !req.Force && !req.DryRun {
						return result, fmt.Errorf(
							"conflict: both harness (%s) and pack (%s) changed since last sync (use --force to overwrite pack)",
							src, dst,
						)
					}
					if req.DryRun {
						continue
					}
				}
			}

			if !req.DryRun {
				if c.Kind == domain.CopyKindDir {
					if err := saveDirToPack(src, dst); err != nil {
						return RoundTripResult{}, err
					}
				} else {
					if err := saveContentToPack(desiredContent, dst); err != nil {
						return RoundTripResult{}, err
					}
				}

				// Update ledger digest to reflect the saved content.
				var digest string
				if c.Kind == domain.CopyKindDir {
					digest, err = dirDigest(src)
				} else {
					digest = domain.SingleFileDigest(srcContent)
				}
				if err != nil {
					return RoundTripResult{}, err
				}
				lg.Managed[src] = domain.Entry{
					SourcePack: packName,
					Digest:     digest,
				}
			}
			result.SavedFiles = append(result.SavedFiles, SavedFile{HarnessPath: src, PackName: packName, PackPath: dst})
			savedCount++
		}

		// Write actions: content writes (promoted agents/workflows) and
		// settings writes are handled differently.
		for _, w := range res.Writes {
			if w.Src == "" {
				continue
			}
			src := filepath.Clean(w.Src)
			entry, tracked := lg.Managed[src]
			if !tracked {
				continue
			}

			curDigest := w.EffectiveDigest()
			if curDigest == entry.Digest {
				result.UnchangedCount++
				continue
			}

			packName := entry.SourcePack
			if packName == "" {
				if len(req.PackRoots) == 1 {
					for k := range req.PackRoots {
						packName = k
					}
				} else {
					return RoundTripResult{}, fmt.Errorf("empty source_pack for %s and multiple packs configured — run 'aipack sync' first to populate provenance", src)
				}
			}
			packRoot, ok := req.PackRoots[packName]
			if !ok {
				continue
			}

			dst := filepath.Join(packRoot, filepath.FromSlash(w.Dst))

			if w.IsContent {
				// Content write — save re-rendered content directly.
				conflict, err := checkFileConflict(w.Content, dst)
				if err != nil {
					return RoundTripResult{}, err
				}
				if conflict {
					result.Conflicts = append(result.Conflicts, ConflictFile{
						HarnessPath: src, PackPath: dst, PackName: packName,
					})
					if !req.Force && !req.DryRun {
						return result, fmt.Errorf(
							"conflict: both harness (%s) and pack (%s) changed since last sync (use --force to overwrite pack)",
							src, dst,
						)
					}
					if req.DryRun {
						continue
					}
				}

				if !req.DryRun {
					if err := saveContentToPack(w.Content, dst); err != nil {
						return RoundTripResult{}, err
					}
					lg.Managed[src] = domain.Entry{
						SourcePack: packName,
						Digest:     curDigest,
					}
				}
				result.SavedFiles = append(result.SavedFiles, SavedFile{
					HarnessPath: src, PackName: packName, PackPath: dst,
				})
				savedCount++
			} else {
				// Settings write — either save immediately when forced, or emit
				// as pending so the caller can decide whether to persist it.
				stripped, err := h.StripManagedSettings(w.Content, filepath.Base(w.Dst))
				if err != nil {
					return RoundTripResult{}, fmt.Errorf("stripping managed settings from %s: %w", src, err)
				}
				if req.Force && !req.DryRun {
					if err := saveContentToPack(stripped, dst); err != nil {
						return RoundTripResult{}, err
					}
					lg.Managed[src] = domain.Entry{
						SourcePack: packName,
						Digest:     curDigest,
					}
					result.SavedFiles = append(result.SavedFiles, SavedFile{
						HarnessPath: src, PackName: packName, PackPath: dst,
					})
					savedCount++
				} else {
					result.PendingSettings = append(result.PendingSettings, PendingSettingsChange{
						HarnessPath: src,
						PackName:    packName,
						PackPath:    dst,
						Stripped:    stripped,
					})
				}
			}
		}

		// Persist updated ledger digests for this harness.
		if !req.DryRun && savedCount > 0 {
			if err := engine.SaveLedger(ledgerPath, lg, false); err != nil {
				return result, fmt.Errorf("saving ledger for %s: %w", hid, err)
			}
		}
	}

	return result, nil
}

// contentChangedSinceLedger checks if pre-read content (for files) or on-disk
// content (for dirs) differs from the ledger-recorded digest. For CopyKindFile,
// content must be non-nil; for CopyKindDir, src path is read from disk.
func contentChangedSinceLedger(content []byte, src, ledgerDigest string, kind domain.CopyKind) (bool, error) {
	if ledgerDigest == "" {
		return true, nil
	}
	if kind == domain.CopyKindDir {
		curDigest, err := dirDigest(src)
		if err != nil {
			return false, err
		}
		return curDigest != ledgerDigest, nil
	}
	return domain.SingleFileDigest(content) != ledgerDigest, nil
}

// fileChangedSinceLedger checks if the content at src differs from the
// ledger-recorded digest. Reads the file from disk.
func fileChangedSinceLedger(src, ledgerDigest string, kind domain.CopyKind) (bool, error) {
	if ledgerDigest == "" {
		return true, nil
	}
	if kind == domain.CopyKindDir {
		curDigest, err := dirDigest(src)
		if err != nil {
			return false, err
		}
		return curDigest != ledgerDigest, nil
	}
	// Use SingleFileDigest to match ledger format (pathDigest-compatible).
	content, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	return domain.SingleFileDigest(content) != ledgerDigest, nil
}

func mcpFileChangedSinceLedger(src, ledgerDigest string) (bool, error) {
	if ledgerDigest == "" {
		return true, nil
	}
	content, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	tracked, err := trackedMCPBytesFromFile(content)
	if err != nil {
		return true, nil
	}
	return domain.SingleFileDigest(tracked) != ledgerDigest, nil
}

func trackedMCPBytesFromFile(content []byte) ([]byte, error) {
	var server domain.MCPServer
	if err := json.Unmarshal(content, &server); err != nil {
		return nil, err
	}
	return domain.MCPTrackedBytes(server)
}

// dirDigest computes a combined digest of all files in a directory tree.
// Format: sorted "rel:sha256\n" entries hashed with ContentDigest.
// This is NOT interchangeable with engine.pathDigest, which uses "key\0sha256\n"
// with a streaming SHA-256. The two never cross-compare: dirDigest is used only
// in save's hasSourceChanged to compare a source directory against its own prior
// digest, while pathDigest is used by the sync engine for ledger entries.
func dirDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, rel := range paths {
		d, err := util.FileDigest(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		b.WriteString(rel)
		b.WriteByte(':')
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return util.ContentDigest([]byte(b.String())), nil
}

// packDirMatchesLedger checks whether each file in the pack directory (dst)
// has the same digest as the corresponding harness file's ledger entry (keyed
// under srcDir). Returns true when all pack files match the ledger, meaning
// the pack side has not been independently modified.
func packDirMatchesLedger(srcDir, dstDir string, lg domain.Ledger) bool {
	clean := true
	_ = filepath.WalkDir(dstDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dstDir, p)
		if err != nil {
			clean = false
			return nil
		}
		// Map pack-relative path back to harness-relative path for ledger lookup.
		harnessSibling := filepath.Clean(filepath.Join(srcDir, rel))
		entry, ok := lg.Managed[harnessSibling]
		if !ok {
			// File exists in pack but not tracked — conservative: not clean.
			clean = false
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			clean = false
			return nil
		}
		if domain.SingleFileDigest(content) != entry.Digest {
			clean = false
		}
		return nil
	})
	return clean
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func scanBytesForSecrets(b []byte) []string {
	text := string(b)
	var results []string

	// SSH private keys.
	if strings.Contains(text, "BEGIN RSA PRIVATE KEY") ||
		strings.Contains(text, "BEGIN OPENSSH PRIVATE KEY") ||
		strings.Contains(text, "ssh-rsa ") ||
		strings.Contains(text, "ssh-rsa\t") {
		results = append(results, "matches forbidden secret pattern: SSH key material")
	}

	// AWS access key IDs (AKIA followed by 16 uppercase alphanumeric chars).
	if strings.Contains(text, "AKIA") {
		for i := 0; i <= len(text)-20; i++ {
			if text[i:i+4] == "AKIA" {
				candidate := text[i : i+20]
				allUpper := true
				for _, c := range candidate[4:] {
					if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
						allUpper = false
						break
					}
				}
				if allUpper {
					results = append(results, "matches forbidden secret pattern: AKIA[0-9A-Z]{16}")
					break
				}
			}
		}
	}

	// OCI resource identifiers.
	if strings.Contains(text, "ocid1.") {
		results = append(results, "matches forbidden secret pattern: ocid1.*")
	}

	return results
}

// SaveRoundTrip executes a round-trip save (harness → source packs) using
// the active profile from sync-config defaults.
func SaveRoundTrip(configDir string, force bool, reg *harness.Registry) (RoundTripResult, []domain.Warning, error) {
	return saveRoundTripActive(configDir, false, force, reg)
}

// SaveRoundTripPlan runs a dry-run round-trip save using the active profile,
// returning what would change without writing.
func SaveRoundTripPlan(configDir string, reg *harness.Registry) (RoundTripResult, []domain.Warning, error) {
	return saveRoundTripActive(configDir, true, false, reg)
}

func saveRoundTripActive(configDir string, dryRun, force bool, reg *harness.Registry) (RoundTripResult, []domain.Warning, error) {
	res, warnings, err := ResolveActiveProfile(configDir)
	if err != nil {
		return RoundTripResult{}, warnings, err
	}
	packRoots := resolvePackRoots(res.Profile)
	result, err := RunRoundTrip(RoundTripRequest{
		TargetSpec: res.TargetSpec,
		PackRoots:  packRoots,
		DryRun:     dryRun,
		Force:      force,
	}, reg)
	if err != nil {
		return result, warnings, err
	}
	warnings = append(warnings, result.CaptureWarnings...)
	return result, warnings, nil
}
