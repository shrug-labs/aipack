package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

// ApplyRequest controls how the plan is applied.
type ApplyRequest struct {
	Force  bool // override conflicts for ALL file types
	Yes    bool // auto-confirm stale file deletions
	DryRun bool
	Quiet  bool      // suppress diagnostic output (for TUI and --json)
	Stdout io.Writer // progress diagnostics (create/update/conflict/diff/merge); nil suppresses output
	Req    PlanRequest

	// LabelForPath returns the human-readable label for diagnostics.
	// When nil, paths are displayed as stable slash-separated absolute paths.
	LabelForPath func(string) string

	// StaleRoots are legacy cleanup-only roots in addition to managedRoots.
	// Plan destinations are not allowed here; only ledger-managed stale entries
	// may be removed from these roots.
	StaleRoots []string

	// PruneOnlyStalePaths are stale ledger entries whose files are currently
	// owned by another harness. The current harness claim is removed, but the
	// on-disk file is preserved.
	PruneOnlyStalePaths map[string]struct{}

	// StripFuncs maps cleaned file paths to functions that strip only the
	// managed keys from shared config files. When a stale ledger entry
	// matches a path in this map, the strip function is called instead of
	// deleting the entire file. The ledger argument is a pre-reconciliation
	// snapshot so strip functions can derive any ownership context they need.
	StripFuncs map[string]func(content []byte, ledger domain.Ledger) ([]byte, error)
}

// ApplyPlan applies a sync plan to disk.
//
// Error policy: loading/reading failures that degrade gracefully are returned
// as warnings (e.g. stale ledger, failed stale-file removal). Writing
// failures that leave inconsistent state are fatal errors.
func (e *Engine) ApplyPlan(ctx context.Context, plan domain.Plan, ar ApplyRequest, managedRoots []string) ([]domain.Warning, error) {
	warnings := make([]domain.Warning, 0, 4)

	if err := validateDestinationsForApply(plan, managedRoots); err != nil {
		return nil, wrapFatalApply("validate plan destinations", err)
	}

	lg, ledgerWarnings, err := e.LoadLedger(plan.Ledger)
	if err != nil {
		return nil, wrapFatalApply("load ledger", err)
	}
	warnings = append(warnings, ledgerWarnings...)

	snapshotWarnings, err := e.snapshotSettingsForApply(plan, ar)
	if err != nil {
		return warnings, wrapFatalApply("snapshot settings", err)
	}
	warnings = append(warnings, snapshotWarnings...)

	diffs, err := e.buildDiffsForApply(plan, ar, lg)
	if err != nil {
		return warnings, wrapFatalApply("classify plan content", err)
	}

	recorded, err := e.applyDiffsAndRecord(plan, ar, &lg, diffs)
	if err != nil {
		return warnings, wrapFatalApply("apply file diffs", err)
	}

	staleRoots := append([]string{}, managedRoots...)
	staleRoots = append(staleRoots, ar.StaleRoots...)
	warnings = append(warnings, e.reconcileStaleEntries(ctx, plan, &lg, staleRoots, recorded, ar)...)

	if err := e.SaveLedger(plan.Ledger, lg, ar.DryRun); err != nil {
		return warnings, wrapFatalApply("save ledger", err)
	}
	return warnings, nil
}

func validateDestinationsForApply(plan domain.Plan, managedRoots []string) error {
	allowed := make([]string, len(managedRoots)+1)
	copy(allowed, managedRoots)
	allowed[len(managedRoots)] = filepath.Dir(plan.Ledger)
	return validatePlanDestinations(plan, allowed)
}

func (e *Engine) snapshotSettingsForApply(plan domain.Plan, ar ApplyRequest) ([]domain.Warning, error) {
	var allSettings []domain.SettingsAction
	if !ar.Req.SkipSettings {
		allSettings = append(allSettings, plan.Settings...)
	}
	allSettings = append(allSettings, plan.MCP...)
	return e.SnapshotSettingsFiles(allSettings, plan.Ledger, ar.DryRun)
}

func (e *Engine) buildDiffsForApply(plan domain.Plan, ar ApplyRequest, lg domain.Ledger) ([]FileDiff, error) {
	var diffs []FileDiff

	for _, w := range plan.Writes {
		fd, err := e.ClassifyWrite(w, ar.displayLabel(w.Dst), lg)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, fd)
	}

	for _, c := range plan.Copies {
		switch c.Kind {
		case domain.CopyKindDir:
			fds, err := e.ClassifyCopyWithOptions(c.Src, c.Dst, c.SourcePack, lg, ClassifyCopyOptions{
				LabelForPath: ar.displayLabel,
			})
			if err != nil {
				return nil, err
			}
			diffs = append(diffs, fds...)
		case domain.CopyKindFile:
			content, err := e.FS.ReadFile(c.Src)
			if err != nil {
				return nil, err
			}
			desiredMode, err := e.copyDesiredMode(c.Src)
			if err != nil {
				return nil, err
			}
			fd, err := e.classifyCopyFileWithMode(c.Dst, content, desiredMode, ar.displayLabel(c.Dst), c.SourcePack, lg)
			if err != nil {
				return nil, err
			}
			diffs = append(diffs, fd)
		default:
			return nil, fmt.Errorf("unknown copy kind: %s", c.Kind)
		}
	}

	if !ar.Req.SkipSettings {
		settingsDiffs, err := e.ComputeSettingsDiffs(LabelSettingsActions(plan.Settings, ar.displayLabel), lg)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, settingsDiffs...)
	}

	// MCP configs are NEVER gated by SkipSettings.
	mcpDiffs, err := e.ComputeSettingsDiffs(LabelSettingsActions(plan.MCP, ar.displayLabel), lg)
	if err != nil {
		return nil, err
	}
	diffs = append(diffs, mcpDiffs...)
	return diffs, nil
}

func (ar ApplyRequest) displayLabel(path string) string {
	if ar.LabelForPath != nil {
		return ar.LabelForPath(path)
	}
	return domain.DisplayPath(path)
}

func (e *Engine) applyDiffsAndRecord(plan domain.Plan, ar ApplyRequest, lg *domain.Ledger, diffs []FileDiff) (map[string]struct{}, error) {
	now := time.Now()
	recorded := map[string]struct{}{}
	for _, d := range diffs {
		applied, err := e.applyFileDiff(d, ar)
		if err != nil {
			return nil, err
		}
		if !ar.DryRun && (applied || d.Kind == domain.DiffIdentical) {
			p := filepath.Clean(d.Dst)
			lg.Record(p, d.Desired, d.SourcePack, d.ManagedOverlay, now)
			recorded[p] = struct{}{}
		}
	}
	for _, m := range plan.MCPServers {
		if ar.DryRun {
			continue
		}
		key := m.LedgerKey()
		lg.Record(key, m.Content, m.SourcePack, nil, now)
		recorded[key] = struct{}{}
	}
	return recorded, nil
}

// staleCandidates returns ledger-tracked file paths that are not in the
// current plan's desired set and will be deleted during sync.
func (e *Engine) staleCandidates(plan domain.Plan, managedRoots []string) ([]string, error) {
	if plan.Ledger == "" {
		return nil, nil
	}
	lg, _, err := e.LoadLedger(plan.Ledger)
	if err != nil {
		return nil, fmt.Errorf("loading ledger for stale check: %w", err)
	}
	return e.StaleCandidatesWithLedger(plan, managedRoots, lg)
}

// StaleCandidatesWithLedger is like staleCandidates but accepts a pre-loaded
// ledger, avoiding a redundant disk read when the caller already has one.
func (e *Engine) StaleCandidatesWithLedger(plan domain.Plan, managedRoots []string, lg domain.Ledger) ([]string, error) {
	return e.StaleCandidatesWithLedgerExcluding(plan, managedRoots, lg, nil)
}

// StaleCandidatesWithLedgerExcluding is like StaleCandidatesWithLedger, but
// omits entries that should be pruned from the current ledger without deleting
// the on-disk file.
func (e *Engine) StaleCandidatesWithLedgerExcluding(plan domain.Plan, managedRoots []string, lg domain.Ledger, exclude map[string]struct{}) ([]string, error) {
	if plan.Ledger == "" {
		return nil, nil
	}
	desired := plan.Desired
	staleRoots := make([]string, len(managedRoots)+1)
	copy(staleRoots, managedRoots)
	staleRoots[len(managedRoots)] = filepath.Dir(plan.Ledger)
	var candidates []string
	for k := range lg.Managed {
		if domain.IsMCPLedgerKey(k) {
			continue
		}
		clean := filepath.Clean(k)
		if !domain.IsUnderAny(clean, staleRoots) {
			continue
		}
		if _, ok := desired[clean]; ok {
			continue
		}
		if _, ok := exclude[clean]; ok {
			continue
		}
		if _, err := e.FS.Stat(k); err != nil {
			continue // gone or inaccessible — not a stale candidate
		}
		candidates = append(candidates, k)
	}
	slices.Sort(candidates)
	return candidates, nil
}

// applyFileDiff applies a single file diff according to policy.
func (e *Engine) applyFileDiff(d FileDiff, ar ApplyRequest) (bool, error) {
	switch d.Kind {
	case domain.DiffIdentical:
		// MergeConflict ops can be attached to an otherwise-identical settings
		// file (first-sync scalar collision: the pack tried to set a key the
		// user already has). The disk content is unchanged, but the conflict
		// still needs to be surfaced so pack authors and users see that the
		// managed value was rejected rather than silently applied.
		if !ar.Quiet && ar.Stdout != nil && hasMergeConflict(d.MergeOps) {
			fmt.Fprintf(ar.Stdout, "  unchanged: %s\n", d.Label)
			emitMergeOpsSummary(ar.Stdout, d.MergeOps)
		}
		return false, nil

	case domain.DiffCreate:
		if !ar.Quiet && ar.Stdout != nil {
			fmt.Fprintf(ar.Stdout, "  create: %s\n", d.Label)
			emitMergeOpsSummary(ar.Stdout, d.MergeOps)
		}
		if ar.DryRun {
			return false, nil
		}
		if err := e.FS.MkdirAll(filepath.Dir(d.Dst), 0o755); err != nil {
			return false, err
		}
		return true, e.FS.WriteFile(d.Dst, d.Desired, fileDiffMode(d))

	case domain.DiffManaged:
		if !ar.Quiet && ar.Stdout != nil {
			fmt.Fprintf(ar.Stdout, "  update: %s\n", d.Label)
			emitMergeOpsSummary(ar.Stdout, d.MergeOps)
			emitManagedDiffSummary(ar.Stdout, d)
		}
		if ar.DryRun {
			return false, nil
		}
		if err := e.FS.MkdirAll(filepath.Dir(d.Dst), 0o755); err != nil {
			return false, err
		}
		return true, e.FS.WriteFile(d.Dst, d.Desired, fileDiffMode(d))

	case domain.DiffConflict:
		if !ar.Quiet && ar.Stdout != nil {
			emitFileDiff(ar.Stdout, d)
		}
		if ar.DryRun {
			if !ar.Quiet && ar.Stdout != nil {
				fmt.Fprintf(ar.Stdout, "  conflict: %s (dry-run, would need --force)\n", d.Label)
			}
			return false, nil
		}
		if ar.Force {
			if !ar.Quiet && ar.Stdout != nil {
				fmt.Fprintf(ar.Stdout, "  force-apply: %s\n", d.Label)
			}
			if err := e.FS.MkdirAll(filepath.Dir(d.Dst), 0o755); err != nil {
				return false, err
			}
			return true, e.FS.WriteFile(d.Dst, d.Desired, fileDiffMode(d))
		}
		if !ar.Quiet && ar.Stdout != nil {
			fmt.Fprintf(ar.Stdout, "  skip (conflict, use --force to apply): %s\n", d.Label)
		}
		return false, nil
	}
	return false, fmt.Errorf("unhandled diff kind %q for %s", d.Kind, d.Label)
}

func fileDiffMode(d FileDiff) os.FileMode {
	if d.DesiredMode != 0 {
		return d.DesiredMode.Perm()
	}
	return defaultWriteMode
}

func emitFileDiff(w io.Writer, d FileDiff) {
	if w == nil || d.Diff == "" {
		return
	}
	fmt.Fprintf(w, "\n--- Diff: %s ---\n%s\n", d.Label, d.Diff)
}

func hasMergeConflict(ops []MergeOp) bool {
	for _, op := range ops {
		if op.Action == MergeConflict {
			return true
		}
	}
	return false
}

// emitManagedDiffSummary prints a one-line reason for a managed update when
// the FileDiff lacks MergeOps but carries a short Diff explanation (e.g. the
// mode-only-drift path in classifyWrite). For full unified-diff content the
// caller can already use emitFileDiff in a verbose/dry-run flow.
func emitManagedDiffSummary(w io.Writer, d FileDiff) {
	if w == nil || d.Diff == "" || len(d.MergeOps) > 0 {
		return
	}
	// Short single-line diff (e.g. "mode: 0o644 → 0o755 ..."): print inline.
	if !strings.Contains(strings.TrimSuffix(d.Diff, "\n"), "\n") {
		fmt.Fprintf(w, "    %s", d.Diff)
		if !strings.HasSuffix(d.Diff, "\n") {
			fmt.Fprintln(w)
		}
	}
}

func validatePlanDestinations(plan domain.Plan, allowed []string) error {
	check := func(raw, label string) error {
		dst := filepath.Clean(raw)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid %s destination: %q", label, raw)
		}
		if !domain.IsUnderAny(dst, allowed) {
			return fmt.Errorf("refusing to %s outside managed roots: %s", label, dst)
		}
		return nil
	}
	for _, w := range plan.Writes {
		if err := check(w.Dst, "write"); err != nil {
			return err
		}
	}
	for _, c := range plan.Copies {
		if err := check(c.Dst, "copy"); err != nil {
			return err
		}
	}
	for _, s := range plan.Settings {
		if err := check(s.Dst, "write settings"); err != nil {
			return err
		}
	}
	for _, m := range plan.MCP {
		if err := check(m.Dst, "write MCP config"); err != nil {
			return err
		}
	}
	for _, m := range plan.MCPServers {
		if err := check(m.ConfigPath, "track MCP"); err != nil {
			return err
		}
	}
	if plan.Ledger != "" {
		if err := check(plan.Ledger, "write ledger"); err != nil {
			return err
		}
	}
	return nil
}

func emitMergeOpsSummary(w io.Writer, ops []MergeOp) {
	if w == nil || len(ops) == 0 {
		return
	}
	var adds, updates, removes, resets int
	var conflicts []string
	for _, op := range ops {
		switch op.Action {
		case MergeAdd:
			adds++
		case MergeUpdate:
			updates++
		case MergeRemove:
			removes++
		case MergeReset:
			resets++
		case MergeConflict:
			conflicts = append(conflicts, op.Key)
		}
	}
	var parts []string
	if resets > 0 {
		parts = append(parts, "on-disk file was corrupted, replaced with managed state")
	}
	if adds > 0 {
		parts = append(parts, fmt.Sprintf("%d added", adds))
	}
	if updates > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updates))
	}
	if removes > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", removes))
	}
	if len(conflicts) > 0 {
		parts = append(parts, fmt.Sprintf("%d kept user value", len(conflicts)))
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "    merge: %s\n", strings.Join(parts, ", "))
	}
	for _, key := range conflicts {
		fmt.Fprintf(w, "      conflict: %s (pack value rejected; on-disk user value preserved)\n", key)
	}
}
