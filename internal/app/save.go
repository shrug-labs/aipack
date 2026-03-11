package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// SnapshotRequest holds parameters for a snapshot save operation.
type SnapshotRequest struct {
	TargetSpec

	Now        func() time.Time
	RandUint32 func() uint32
}

// SnapshotResult holds the output of a snapshot operation.
type SnapshotResult struct {
	BaseDir         string
	SnapshotID      string
	SecretFindings  []string
	CaptureWarnings []domain.Warning
}

// RunSnapshot captures harness content into a timestamped snapshot directory.
func RunSnapshot(req SnapshotRequest, reg *harness.Registry) (SnapshotResult, error) {
	now := req.Now
	if now == nil {
		now = time.Now
	}
	randU32 := req.RandUint32
	if randU32 == nil {
		randU32 = func() uint32 { return rand.Uint32() }
	}
	home := req.Home
	if strings.TrimSpace(home) == "" {
		return SnapshotResult{}, fmt.Errorf("HOME is not set")
	}
	if len(req.Harnesses) == 0 {
		return SnapshotResult{}, fmt.Errorf("no harnesses selected")
	}

	timestamp := now().UTC().Format("20060102-150405") + fmt.Sprintf("-%08x", randU32())
	base := filepath.Join(home, ".config", "aipack", "saved", timestamp)
	packDir := filepath.Join(base, "pack")
	if _, err := os.Stat(base); err == nil {
		return SnapshotResult{}, fmt.Errorf("save destination already exists: %s", base)
	}

	if err := os.MkdirAll(packDir, 0o700); err != nil {
		return SnapshotResult{}, err
	}

	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}
	merged, err := captureAndMerge(ctx, req.Harnesses, reg)
	if err != nil {
		return SnapshotResult{}, err
	}

	for _, c := range merged.Copies {
		dst := filepath.Join(packDir, filepath.FromSlash(c.Dst))
		switch c.Kind {
		case domain.CopyKindDir:
			if err := util.CopyDir(c.Src, dst); err != nil {
				return SnapshotResult{}, err
			}
		case domain.CopyKindFile:
			b, err := os.ReadFile(c.Src)
			if err != nil {
				return SnapshotResult{}, err
			}
			b, err = desiredBytesForCopy(c, merged, b)
			if err != nil {
				return SnapshotResult{}, err
			}
			if err := util.WriteFileAtomicWithPerms(dst, b, 0o700, 0o600); err != nil {
				return SnapshotResult{}, err
			}
		}
	}
	for _, w := range merged.Writes {
		dst := filepath.Join(packDir, filepath.FromSlash(w.Dst))
		if err := util.WriteFileAtomicWithPerms(dst, w.Content, 0o700, 0o600); err != nil {
			return SnapshotResult{}, err
		}
	}

	if len(merged.MCPServers) > 0 {
		mcpDir := filepath.Join(packDir, "mcp")
		if err := os.MkdirAll(mcpDir, 0o700); err != nil {
			return SnapshotResult{}, err
		}
		keys := sortedMapKeys(merged.MCPServers)
		for _, k := range keys {
			s := merged.MCPServers[k]
			s.Name = k
			if tools := merged.AllowedTools[k]; len(tools) > 0 {
				s.AvailableTools = append([]string{}, tools...)
			}
			b, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return SnapshotResult{}, err
			}
			b = append(b, '\n')
			if err := util.WriteFileAtomicWithPerms(filepath.Join(mcpDir, k+".json"), b, 0o700, 0o600); err != nil {
				return SnapshotResult{}, err
			}
		}
	}

	manifest, err := buildPackManifest(packDir, timestamp, merged.MCPServers, merged.AllowedTools)
	if err != nil {
		return SnapshotResult{}, err
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SnapshotResult{}, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := util.WriteFileAtomicWithPerms(filepath.Join(packDir, "pack.json"), manifestBytes, 0o700, 0o600); err != nil {
		return SnapshotResult{}, err
	}

	// Install the snapshot pack at the standard installed-packs location.
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "snapshot-" + timestamp
	installedPackDir := filepath.Join(configDir, "packs", packName)
	if err := util.CopyDir(packDir, installedPackDir); err != nil {
		return SnapshotResult{}, fmt.Errorf("installing snapshot pack: %w", err)
	}

	// Register in sync-config.
	syncCfgPath := config.SyncConfigPath(configDir)
	syncCfg, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("loading sync-config: %w", err)
	}
	if syncCfg.InstalledPacks == nil {
		syncCfg.InstalledPacks = map[string]config.InstalledPackMeta{}
	}
	syncCfg.InstalledPacks[packName] = config.InstalledPackMeta{
		Origin: base,
		Method: "snapshot",
	}
	if err := config.SaveSyncConfig(syncCfgPath, syncCfg); err != nil {
		return SnapshotResult{}, fmt.Errorf("saving sync-config: %w", err)
	}

	findings := scanSnapshotForSecrets(packDir)
	return SnapshotResult{
		BaseDir:         base,
		SnapshotID:      timestamp,
		SecretFindings:  findings,
		CaptureWarnings: merged.Warnings,
	}, nil
}

// ToPackRequest holds parameters for saving harness content back to a source pack.
type ToPackRequest struct {
	TargetSpec
	PackName  string
	ConfigDir string
	DryRun    bool
	Force     bool
	Stderr    io.Writer
}

// ToPackResult holds the output of a to-pack save operation.
type ToPackResult struct {
	SavedFiles []SavedFile
	Skipped    int
	Conflicts  []ConflictFile
	Warnings   []domain.Warning
}

// RunToPack captures current harness content and writes it back to an
// installed pack's directory. If the pack doesn't exist, it scaffolds a new
// one. Files attributed to OTHER packs via the ledger are excluded.
func RunToPack(req ToPackRequest, reg *harness.Registry) (ToPackResult, error) {
	packRoot := filepath.Join(req.ConfigDir, "packs", req.PackName)
	manifestPath := filepath.Join(packRoot, "pack.json")
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return runToPackCreate(req, packRoot, reg)
		}
		return ToPackResult{}, err
	}
	return runToPackExisting(req, packRoot, manifestPath, reg)
}

func runToPackExisting(req ToPackRequest, packRoot, manifestPath string, reg *harness.Registry) (ToPackResult, error) {
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	home := req.Home
	var result ToPackResult

	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return result, fmt.Errorf("loading pack manifest: %w", err)
	}
	resolvedRoot := ResolvePackRootWithFallback(manifestPath, manifest, packRoot)

	ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, home, req.Harnesses)
	lg, ledgerWarn, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		return result, fmt.Errorf("loading ledger: %w", err)
	}
	if ledgerWarn != "" {
		fmt.Fprintln(stderr, "WARNING: "+ledgerWarn)
	}

	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}

	for _, hid := range req.Harnesses {
		h, err := reg.Lookup(hid)
		if err != nil {
			return result, err
		}
		res, err := h.Capture(ctx)
		if err != nil {
			return result, err
		}
		result.Warnings = append(result.Warnings, res.Warnings...)

		for _, c := range res.Copies {
			if shouldSkipForPack(c.Src, req.PackName, lg) {
				result.Skipped++
				continue
			}
			dst := filepath.Join(resolvedRoot, filepath.FromSlash(c.Dst))

			// For files, read content once and reuse for conflict check, save, and digest.
			var srcContent []byte
			var desiredContent []byte
			if c.Kind == domain.CopyKindFile {
				srcContent, err = os.ReadFile(c.Src)
				if err != nil {
					return result, err
				}
				desiredContent, err = desiredBytesForCopy(c, res, srcContent)
				if err != nil {
					return result, err
				}
			}

			// Conflict check.
			var conflict bool
			if c.Kind == domain.CopyKindDir {
				conflict, err = checkDirConflict(c.Src, dst)
			} else {
				conflict, err = checkFileConflict(desiredContent, dst)
			}
			if err != nil {
				return result, err
			}

			if conflict {
				result.Conflicts = append(result.Conflicts, ConflictFile{
					HarnessPath: c.Src, PackPath: dst, PackName: req.PackName,
				})
				if !req.Force && !req.DryRun {
					return result, fmt.Errorf("conflict: %s differs from %s (use --force to overwrite)", c.Src, dst)
				}
			}

			if !req.DryRun {
				if c.Kind == domain.CopyKindDir {
					if err := saveDirToPack(c.Src, dst); err != nil {
						return result, err
					}
				} else {
					if err := saveContentToPack(desiredContent, dst); err != nil {
						return result, err
					}
				}

				// Update ledger so round-trip can route this file next time.
				var digest string
				if c.Kind == domain.CopyKindDir {
					digest, err = dirDigest(c.Src)
				} else {
					digest = domain.SingleFileDigest(srcContent)
				}
				if err != nil {
					return result, err
				}
				lg.Managed[filepath.Clean(c.Src)] = domain.Entry{
					SourcePack: req.PackName,
					Digest:     digest,
				}
			}
			result.SavedFiles = append(result.SavedFiles, SavedFile{
				HarnessPath: c.Src, PackName: req.PackName, PackPath: dst,
			})
		}

		for _, w := range res.Writes {
			dst := filepath.Join(resolvedRoot, filepath.FromSlash(w.Dst))

			conflict, err := checkFileConflict(w.Content, dst)
			if err != nil {
				return result, err
			}
			if conflict {
				result.Conflicts = append(result.Conflicts, ConflictFile{
					HarnessPath: "(generated)", PackPath: dst, PackName: req.PackName,
				})
				if !req.Force && !req.DryRun {
					return result, fmt.Errorf("conflict: generated content differs from %s (use --force to overwrite)", dst)
				}
			}

			if !req.DryRun {
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return result, err
				}
				if err := util.WriteFileAtomic(dst, w.Content); err != nil {
					return result, err
				}

				if w.Src != "" {
					lg.Managed[filepath.Clean(w.Src)] = domain.Entry{
						SourcePack: req.PackName,
						Digest:     domain.SingleFileDigest(w.Content),
					}
				}
			}
			result.SavedFiles = append(result.SavedFiles, SavedFile{
				HarnessPath: "(generated)", PackName: req.PackName, PackPath: dst,
			})
		}
	}

	// Persist updated ledger.
	if !req.DryRun && len(result.SavedFiles) > 0 {
		if err := engine.SaveLedger(ledgerPath, lg, false); err != nil {
			return result, fmt.Errorf("saving ledger: %w", err)
		}
	}

	return result, nil
}

func runToPackCreate(req ToPackRequest, packRoot string, reg *harness.Registry) (ToPackResult, error) {
	home := req.Home
	var result ToPackResult

	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}
	merged, err := captureAndMerge(ctx, req.Harnesses, reg)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, merged.Warnings...)

	for _, c := range merged.Copies {
		dst := filepath.Join(packRoot, filepath.FromSlash(c.Dst))
		if c.Kind == domain.CopyKindDir {
			if err := saveDirToPack(c.Src, dst); err != nil {
				return result, err
			}
		} else {
			srcContent, err := os.ReadFile(c.Src)
			if err != nil {
				return result, err
			}
			desiredContent, err := desiredBytesForCopy(c, merged, srcContent)
			if err != nil {
				return result, err
			}
			if err := saveContentToPack(desiredContent, dst); err != nil {
				return result, err
			}
		}
		result.SavedFiles = append(result.SavedFiles, SavedFile{
			HarnessPath: c.Src, PackName: req.PackName, PackPath: dst,
		})
	}
	for _, w := range merged.Writes {
		dst := filepath.Join(packRoot, filepath.FromSlash(w.Dst))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return result, err
		}
		if err := util.WriteFileAtomic(dst, w.Content); err != nil {
			return result, err
		}
		result.SavedFiles = append(result.SavedFiles, SavedFile{
			HarnessPath: "(generated)", PackName: req.PackName, PackPath: dst,
		})
	}

	if len(merged.MCPServers) > 0 {
		mcpDir := filepath.Join(packRoot, "mcp")
		if err := os.MkdirAll(mcpDir, 0o755); err != nil {
			return result, err
		}
		keys := sortedMapKeys(merged.MCPServers)
		for _, k := range keys {
			s := merged.MCPServers[k]
			s.Name = k
			if tools := merged.AllowedTools[k]; len(tools) > 0 {
				s.AvailableTools = append([]string{}, tools...)
			}
			b, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return result, err
			}
			b = append(b, '\n')
			if err := util.WriteFileAtomic(filepath.Join(mcpDir, k+".json"), b); err != nil {
				return result, err
			}
		}
	}

	manifest, err := buildPackManifest(packRoot, "0.1.0", merged.MCPServers, merged.AllowedTools)
	if err != nil {
		return result, err
	}
	manifest.Name = req.PackName
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := util.WriteFileAtomic(filepath.Join(packRoot, "pack.json"), manifestBytes); err != nil {
		return result, err
	}

	syncCfgPath := config.SyncConfigPath(req.ConfigDir)
	syncCfg, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		return result, fmt.Errorf("loading sync-config: %w", err)
	}
	if syncCfg.InstalledPacks == nil {
		syncCfg.InstalledPacks = map[string]config.InstalledPackMeta{}
	}
	syncCfg.InstalledPacks[req.PackName] = config.InstalledPackMeta{
		Origin: packRoot,
		Method: "created",
	}
	if err := config.SaveSyncConfig(syncCfgPath, syncCfg); err != nil {
		return result, fmt.Errorf("saving sync-config: %w", err)
	}

	return result, nil
}

// shouldSkipForPack returns true if the file at path is tracked in the ledger
// and attributed to a different pack than the target.
func shouldSkipForPack(path, packName string, lg domain.Ledger) bool {
	entry, tracked := lg.Managed[filepath.Clean(path)]
	if !tracked {
		return false
	}
	if entry.SourcePack == "" {
		return false
	}
	return entry.SourcePack != packName
}

func ledgerPathForScope(scope domain.Scope, projectDir, home string, harnesses []domain.Harness) string {
	keys := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		keys = append(keys, strings.ToLower(string(h)))
	}
	return engine.LedgerPathForScope(scope, projectDir, home, keys)
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
	case domain.ContentRules:
		for _, rule := range res.Rules {
			if filepath.Clean(rule.SourcePath) == src {
				return engine.RenderRuleBytes(rule)
			}
		}
	case domain.ContentAgents:
		for _, agent := range res.Agents {
			if filepath.Clean(agent.SourcePath) == src {
				return engine.RenderAgentBytes(agent)
			}
		}
	case domain.ContentWorkflows:
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
func RunRoundTrip(req RoundTripRequest, reg *harness.Registry) (RoundTripResult, error) {
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	home := req.Home

	ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, home, req.Harnesses)
	lg, ledgerWarn, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		return RoundTripResult{}, fmt.Errorf("loading ledger: %w", err)
	}
	if ledgerWarn != "" {
		fmt.Fprintln(stderr, "WARNING: "+ledgerWarn)
	}
	if len(lg.Managed) == 0 {
		return RoundTripResult{}, fmt.Errorf("no ledger found at %s — run 'aipack sync' first", ledgerPath)
	}

	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}
	var result RoundTripResult
	settingsLedgerDirty := false

	for _, hid := range req.Harnesses {
		h, err := reg.Lookup(hid)
		if err != nil {
			return RoundTripResult{}, err
		}
		res, err := h.Capture(ctx)
		if err != nil {
			return RoundTripResult{}, err
		}
		result.CaptureWarnings = append(result.CaptureWarnings, res.Warnings...)

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
		}

		// Settings files.
		for _, w := range res.Writes {
			if w.Src == "" {
				continue
			}
			src := filepath.Clean(w.Src)
			entry, tracked := lg.Managed[src]
			if !tracked {
				continue
			}

			curDigest := domain.SingleFileDigest(w.Content)
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
					return RoundTripResult{}, fmt.Errorf("empty source_pack for settings %s and multiple packs configured — run 'aipack sync' first to populate provenance", src)
				}
			}
			packRoot, ok := req.PackRoots[packName]
			if !ok {
				continue
			}

			stripped, err := h.StripManagedSettings(w.Content, filepath.Base(w.Dst))
			if err != nil {
				return RoundTripResult{}, fmt.Errorf("stripping managed settings from %s: %w", src, err)
			}

			dst := filepath.Join(packRoot, filepath.FromSlash(w.Dst))
			result.PendingSettings = append(result.PendingSettings, PendingSettingsChange{
				HarnessPath: src,
				PackName:    packName,
				PackPath:    dst,
				Stripped:    stripped,
			})

			// Advance ledger digest so next round-trip sees this as unchanged.
			if !req.DryRun {
				lg.Managed[src] = domain.Entry{
					SourcePack: packName,
					Digest:     curDigest,
				}
				settingsLedgerDirty = true
			}
		}
	}

	// Persist updated ledger digests.
	if !req.DryRun && (len(result.SavedFiles) > 0 || settingsLedgerDirty) {
		if err := engine.SaveLedger(ledgerPath, lg, false); err != nil {
			return result, fmt.Errorf("saving ledger: %w", err)
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

// captureAndMerge runs Capture on each harness and merges the results.
func captureAndMerge(ctx harness.CaptureContext, ids []domain.Harness, reg *harness.Registry) (harness.CaptureResult, error) {
	var results []harness.CaptureResult
	for _, hid := range ids {
		h, err := reg.Lookup(hid)
		if err != nil {
			return harness.CaptureResult{}, err
		}
		res, err := h.Capture(ctx)
		if err != nil {
			return harness.CaptureResult{}, err
		}
		results = append(results, res)
	}
	return harness.MergeCaptureResults(results...)
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildPackManifest(packDir string, version string, servers map[string]domain.MCPServer, allowedTools map[string][]string) (config.PackManifest, error) {
	agents, err := listIDsWithSuffix(filepath.Join(packDir, "agents"), ".md")
	if err != nil {
		return config.PackManifest{}, err
	}
	rules, err := listIDsWithSuffix(filepath.Join(packDir, "rules"), ".md")
	if err != nil {
		return config.PackManifest{}, err
	}
	workflows, err := listIDsWithSuffix(filepath.Join(packDir, "workflows"), ".md")
	if err != nil {
		return config.PackManifest{}, err
	}
	skills, err := listSkillIDs(filepath.Join(packDir, "skills"))
	if err != nil {
		return config.PackManifest{}, err
	}
	mcpIDs, err := listIDsWithSuffix(filepath.Join(packDir, "mcp"), ".json")
	if err != nil {
		return config.PackManifest{}, err
	}

	serverDefaults := map[string]config.MCPDefaults{}
	for _, name := range mcpIDs {
		tools := append([]string{}, allowedTools[name]...)
		sort.Strings(tools)
		serverDefaults[name] = config.MCPDefaults{DefaultAllowedTools: tools}
	}

	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "snapshot",
		Version:       version,
		Root:          ".",
		Rules:         rules,
		Agents:        agents,
		Workflows:     workflows,
		Skills:        skills,
		MCP:           config.MCPPack{Servers: serverDefaults},
		Configs: config.PackConfigs{
			HarnessSettings: detectConfigHarnessSettings(packDir),
			HarnessPlugins:  detectConfigHarnessPlugins(packDir),
		},
	}
	return manifest, nil
}

func detectConfigHarnessSettings(packDir string) map[string][]string {
	out := map[string][]string{}
	if util.ExistsFile(filepath.Join(packDir, "configs", string(domain.HarnessClaudeCode), "settings.local.json")) {
		out[string(domain.HarnessClaudeCode)] = []string{"settings.local.json"}
	}
	if util.ExistsFile(filepath.Join(packDir, "configs", string(domain.HarnessCodex), "config.toml")) {
		out[string(domain.HarnessCodex)] = []string{"config.toml"}
	}
	if util.ExistsFile(filepath.Join(packDir, "configs", string(domain.HarnessOpenCode), "opencode.json")) {
		out[string(domain.HarnessOpenCode)] = []string{"opencode.json"}
	}
	return out
}

func detectConfigHarnessPlugins(packDir string) map[string][]string {
	out := map[string][]string{}
	if util.ExistsFile(filepath.Join(packDir, "configs", string(domain.HarnessOpenCode), "oh-my-opencode.json")) {
		out[string(domain.HarnessOpenCode)] = []string{"oh-my-opencode.json"}
	}
	return out
}

func listIDsWithSuffix(dir string, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lowerSuffix := strings.ToLower(suffix)
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lowerName := strings.ToLower(name)
		if !strings.HasSuffix(lowerName, lowerSuffix) {
			continue
		}
		base := name[:len(name)-len(suffix)]
		if base != "" {
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out, nil
}

func listSkillIDs(skillsDir string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func scanSnapshotForSecrets(packDir string) []string {
	findings := []string{}
	_ = filepath.WalkDir(packDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		low := strings.ToLower(d.Name())
		if strings.HasSuffix(low, ".md") {
			return nil
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.Size() > 512*1024 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		msgs := scanBytesForSecrets(b)
		if len(msgs) == 0 {
			return nil
		}
		rel, err := filepath.Rel(packDir, path)
		if err != nil {
			rel = path
		}
		for _, m := range msgs {
			findings = append(findings, filepath.ToSlash(rel)+": "+m)
		}
		return nil
	})
	sort.Strings(findings)
	return findings
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
