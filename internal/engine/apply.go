package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// ApplyRequest controls how the plan is applied.
type ApplyRequest struct {
	Force  bool // override conflicts for ALL file types
	Prune  bool
	Yes    bool // auto-confirm prune deletions
	DryRun bool
	Quiet  bool      // suppress stderr diagnostic output (for TUI)
	Stderr io.Writer // warning/diagnostic output (defaults to os.Stderr)
	Req    PlanRequest
}

// ApplyPlan applies a sync plan to disk.
// Key improvements over v1:
//   - No backfill loop: UpdateMetadata handles DiffIdentical files explicitly
//   - Record() takes in-memory content (no disk re-read for digest)
//   - Managed roots computed once for prune (not per-file)
func (ar ApplyRequest) stderr() io.Writer {
	if ar.Stderr != nil {
		return ar.Stderr
	}
	return os.Stderr
}

func ApplyPlan(plan domain.Plan, ar ApplyRequest, managedRoots []string) error {
	allowed := make([]string, len(managedRoots)+1)
	copy(allowed, managedRoots)
	allowed[len(managedRoots)] = filepath.Dir(plan.Ledger)
	if err := validatePlanDestinations(plan, allowed); err != nil {
		return err
	}

	lg, ledgerWarn, err := LoadLedger(plan.Ledger)
	if err != nil {
		return err
	}
	if ledgerWarn != "" && !ar.Quiet {
		fmt.Fprintln(ar.stderr(), "WARNING: "+ledgerWarn)
	}

	// Snapshot settings files before computing diffs (for restore).
	// Only snapshot files that will actually be synced — snapshotting skipped
	// files would produce confusing restore candidates.
	var allSettings []domain.SettingsAction
	if !ar.Req.SkipSettings {
		allSettings = append(allSettings, plan.Settings...)
	}
	allSettings = append(allSettings, plan.MCP...)
	cacheWarn, err := SnapshotSettingsFiles(allSettings, plan.Ledger, ar.DryRun)
	if err != nil {
		return err
	}
	if cacheWarn != "" && !ar.Quiet {
		fmt.Fprintln(ar.stderr(), "WARNING: "+cacheWarn)
	}

	// Classify ALL files into a unified []FileDiff.
	var diffs []FileDiff

	for _, w := range plan.Writes {
		fd, err := ClassifyFile(w.Dst, w.Content, filepath.Base(w.Dst), w.SourcePack, lg)
		if err != nil {
			return err
		}
		diffs = append(diffs, fd)
	}

	for _, c := range plan.Copies {
		switch c.Kind {
		case domain.CopyKindDir:
			fds, err := ClassifyCopy(c.Src, c.Dst, c.SourcePack, lg)
			if err != nil {
				return err
			}
			diffs = append(diffs, fds...)
		case domain.CopyKindFile:
			content, err := os.ReadFile(c.Src)
			if err != nil {
				return err
			}
			fd, err := ClassifyFile(c.Dst, content, filepath.Base(c.Dst), c.SourcePack, lg)
			if err != nil {
				return err
			}
			diffs = append(diffs, fd)
		default:
			return fmt.Errorf("unknown copy kind: %s", c.Kind)
		}
	}

	if !ar.Req.SkipSettings {
		settingsDiffs, err := ComputeSettingsDiffs(plan.Settings, lg)
		if err != nil {
			return err
		}
		diffs = append(diffs, settingsDiffs...)
	}

	// MCP configs are NEVER gated by SkipSettings.
	mcpDiffs, err := ComputeSettingsDiffs(plan.MCP, lg)
	if err != nil {
		return err
	}
	diffs = append(diffs, mcpDiffs...)

	// Apply each diff and update ledger.
	// DiffIdentical files are also recorded so that files present on disk but
	// missing from the ledger (e.g., after adding a harness) get their digest
	// stored. For already-tracked files the digest is unchanged.
	// Track which paths were recorded this cycle so we don't prune them below
	// (Desired may be incomplete for copy-dir expansions).
	now := time.Now()
	recorded := map[string]struct{}{}
	for _, d := range diffs {
		applied, err := applyFileDiff(d, ar)
		if err != nil {
			return err
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

	// Reconcile stale ledger entries: entries under managed roots that are
	// no longer in the plan's desired set. This happens when a harness
	// changes where it writes files (e.g., agents promoted to skills).
	//
	// Always remove stale entries from the ledger so that the dirty-status
	// check (PruneCandidatesWithLedger) doesn't permanently flag them.
	// Only delete actual files when Prune is explicitly requested.
	{
		desired := plan.Desired
		pruneRoots := make([]string, len(managedRoots)+1)
		copy(pruneRoots, managedRoots)
		pruneRoots[len(managedRoots)] = filepath.Dir(plan.Ledger)
		keys := make([]string, 0, len(lg.Managed))
		for k := range lg.Managed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var cleanup *emptyDirCleanup
		if ar.Prune {
			cleanup = newEmptyDirCleanup(pruneRoots)
		}
		for _, k := range keys {
			if domain.IsMCPLedgerKey(k) {
				if _, ok := recorded[filepath.Clean(k)]; !ok && !ar.DryRun {
					lg.Delete(k)
				}
				continue
			}
			if !isUnder(k, pruneRoots) {
				continue
			}
			if _, ok := desired[filepath.Clean(k)]; ok {
				continue
			}
			// Skip entries recorded in this apply cycle — they're current
			// even if not explicitly in Desired (e.g., copy-dir children).
			if _, ok := recorded[filepath.Clean(k)]; ok {
				continue
			}

			// If the path is already gone, prune the ledger entry without prompting.
			if _, err := os.Stat(k); err != nil {
				if os.IsNotExist(err) {
					if !ar.DryRun {
						lg.Delete(k)
					}
					continue
				}
				return err
			}

			if !ar.Prune {
				// File exists but is no longer desired — remove from ledger
				// so it stops showing as a prune candidate, but leave the
				// file on disk.
				if !ar.DryRun {
					lg.Delete(k)
				}
				continue
			}

			ok, err := shouldDelete(k, ar.Yes, lg.PrevDigest(k), ar.DryRun)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if ar.DryRun {
				continue
			}

			if err := os.Remove(k); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(ar.stderr(), "warning: could not remove %s: %v\n", k, err)
				continue // do NOT delete from ledger
			}
			lg.Delete(k)
			cleanup.MaybeCleanupParents(filepath.Dir(k))
		}
		if cleanup != nil {
			cleanup.Flush()
		}
	}

	return SaveLedger(plan.Ledger, lg, ar.DryRun)
}

// PruneCandidates returns ledger-tracked file paths that are not in the
// current plan's desired set and would be deleted by a prune operation.
func PruneCandidates(plan domain.Plan, managedRoots []string) ([]string, error) {
	if plan.Ledger == "" {
		return nil, nil
	}
	lg, _, err := LoadLedger(plan.Ledger)
	if err != nil {
		return nil, fmt.Errorf("loading ledger for prune: %w", err)
	}
	return PruneCandidatesWithLedger(plan, managedRoots, lg)
}

// PruneCandidatesWithLedger is like PruneCandidates but accepts a pre-loaded
// ledger, avoiding a redundant disk read when the caller already has one.
func PruneCandidatesWithLedger(plan domain.Plan, managedRoots []string, lg domain.Ledger) ([]string, error) {
	if plan.Ledger == "" {
		return nil, nil
	}
	desired := plan.Desired
	pruneRoots := make([]string, len(managedRoots)+1)
	copy(pruneRoots, managedRoots)
	pruneRoots[len(managedRoots)] = filepath.Dir(plan.Ledger)
	var candidates []string
	for k := range lg.Managed {
		if domain.IsMCPLedgerKey(k) {
			continue
		}
		if !isUnder(k, pruneRoots) {
			continue
		}
		if _, ok := desired[filepath.Clean(k)]; ok {
			continue
		}
		if _, err := os.Stat(k); err != nil {
			continue // gone or inaccessible — not a prune candidate
		}
		candidates = append(candidates, k)
	}
	sort.Strings(candidates)
	return candidates, nil
}

// applyFileDiff applies a single file diff according to policy.
func applyFileDiff(d FileDiff, ar ApplyRequest) (bool, error) {
	w := ar.stderr()
	switch d.Kind {
	case domain.DiffIdentical:
		return false, nil

	case domain.DiffCreate:
		if !ar.Quiet {
			fmt.Fprintf(w, "  create: %s\n", d.Label)
			printMergeOpsSummary(w, d.MergeOps)
		}
		if ar.DryRun {
			return false, nil
		}
		if err := os.MkdirAll(filepath.Dir(d.Dst), 0o755); err != nil {
			return false, err
		}
		return true, util.WriteFileAtomic(d.Dst, d.Desired)

	case domain.DiffManaged:
		if !ar.Quiet {
			fmt.Fprintf(w, "  update: %s\n", d.Label)
			printMergeOpsSummary(w, d.MergeOps)
		}
		if ar.DryRun {
			return false, nil
		}
		return true, util.WriteFileAtomic(d.Dst, d.Desired)

	case domain.DiffConflict:
		if !ar.Quiet {
			showFileDiff(w, d)
		}
		if ar.DryRun {
			if !ar.Quiet {
				fmt.Fprintf(w, "  conflict: %s (dry-run, would need --force)\n", d.Label)
			}
			return false, nil
		}
		if ar.Force {
			if !ar.Quiet {
				fmt.Fprintf(w, "  force-apply: %s\n", d.Label)
			}
			return true, util.WriteFileAtomic(d.Dst, d.Desired)
		}
		if !ar.Quiet {
			fmt.Fprintf(w, "  skip (conflict, use --force to apply): %s\n", d.Label)
		}
		return false, nil
	}
	return false, fmt.Errorf("unhandled diff kind %q for %s", d.Kind, d.Label)
}

func showFileDiff(w io.Writer, d FileDiff) {
	if d.Diff == "" {
		return
	}
	fmt.Fprintf(w, "\n--- Diff: %s ---\n", d.Label)
	fmt.Fprintln(w, d.Diff)
}

func shouldDelete(path string, yes bool, prevDigest string, dryRun bool) (bool, error) {
	if prevDigest != "" {
		if d, err := pathDigest(path); err == nil && d == prevDigest {
			return true, nil
		}
	}
	if dryRun {
		return true, nil
	}
	if yes {
		return true, nil
	}
	if !isTerminal() {
		return false, fmt.Errorf("refusing to delete %s without --yes (non-interactive)", path)
	}
	ans, err := prompt(fmt.Sprintf("Delete path? %s [y/N]: ", path))
	if err != nil {
		return false, err
	}
	return ans == "y" || ans == "yes", nil
}

func isTerminal() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func prompt(msg string) (string, error) {
	_, err := fmt.Fprint(os.Stderr, msg)
	if err != nil {
		return "", err
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(line)), nil
}

// isUnder delegates to domain.IsUnderAny.
func isUnder(path string, prefixes []string) bool {
	return domain.IsUnderAny(path, prefixes)
}

func validatePlanDestinations(plan domain.Plan, allowed []string) error {
	for _, w := range plan.Writes {
		dst := filepath.Clean(w.Dst)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid write destination: %q", w.Dst)
		}
		if !isUnder(dst, allowed) {
			return fmt.Errorf("refusing to write outside managed roots: %s", dst)
		}
	}
	for _, c := range plan.Copies {
		dst := filepath.Clean(c.Dst)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid copy destination: %q", c.Dst)
		}
		if !isUnder(dst, allowed) {
			return fmt.Errorf("refusing to copy outside managed roots: %s", dst)
		}
	}
	for _, s := range plan.Settings {
		dst := filepath.Clean(s.Dst)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid settings destination: %q", s.Dst)
		}
		if !isUnder(dst, allowed) {
			return fmt.Errorf("refusing to write settings outside managed roots: %s", dst)
		}
	}
	for _, m := range plan.MCP {
		dst := filepath.Clean(m.Dst)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid MCP destination: %q", m.Dst)
		}
		if !isUnder(dst, allowed) {
			return fmt.Errorf("refusing to write MCP config outside managed roots: %s", dst)
		}
	}
	for _, m := range plan.MCPServers {
		dst := filepath.Clean(m.ConfigPath)
		if dst == "" || dst == "." {
			return fmt.Errorf("invalid MCP config path: %q", m.ConfigPath)
		}
		if !isUnder(dst, allowed) {
			return fmt.Errorf("refusing to track MCP outside managed roots: %s", dst)
		}
	}
	if plan.Ledger != "" {
		lp := filepath.Clean(plan.Ledger)
		if lp == "" || lp == "." {
			return fmt.Errorf("invalid ledger destination: %q", plan.Ledger)
		}
		if !isUnder(lp, allowed) {
			return fmt.Errorf("refusing to write ledger outside managed roots: %s", lp)
		}
	}
	return nil
}

type emptyDirCleanup struct {
	prefixes []string
	queue    []string
}

func newEmptyDirCleanup(prefixes []string) *emptyDirCleanup {
	return &emptyDirCleanup{prefixes: prefixes}
}

func (c *emptyDirCleanup) MaybeCleanupParents(dir string) {
	if dir == "" || dir == "." {
		return
	}
	if !isUnder(dir, c.prefixes) {
		return
	}
	c.queue = append(c.queue, dir)
}

func (c *emptyDirCleanup) Flush() {
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(c.queue))
	for _, d := range c.queue {
		dc := filepath.Clean(d)
		if _, ok := seen[dc]; ok {
			continue
		}
		seen[dc] = struct{}{}
		uniq = append(uniq, dc)
	}
	sort.Slice(uniq, func(i, j int) bool { return len(uniq[i]) > len(uniq[j]) })
	for _, d := range uniq {
		c.cleanupUp(d)
	}
}

func (c *emptyDirCleanup) cleanupUp(dir string) {
	cur := filepath.Clean(dir)
	for cur != "." && cur != string(filepath.Separator) {
		if !isUnder(cur, c.prefixes) {
			return
		}
		if err := os.Remove(cur); err != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}

// printMergeOpsSummary prints a one-line merge summary when ops are present.
func printMergeOpsSummary(w io.Writer, ops []MergeOp) {
	if len(ops) == 0 {
		return
	}
	var adds, updates, removes int
	for _, op := range ops {
		switch op.Action {
		case MergeAdd:
			adds++
		case MergeUpdate:
			updates++
		case MergeRemove:
			removes++
		}
	}
	var parts []string
	if adds > 0 {
		parts = append(parts, fmt.Sprintf("%d added", adds))
	}
	if updates > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updates))
	}
	if removes > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", removes))
	}
	fmt.Fprintf(w, "    merge: %s\n", strings.Join(parts, ", "))
}
