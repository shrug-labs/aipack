package engine

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/shrug-labs/aipack/internal/domain"
)

// reconcileStaleEntries removes ledger entries (and their on-disk files) that
// are no longer in the plan's desired set. User-modified files require
// interactive confirmation unless yes is true.
func (e *Engine) reconcileStaleEntries(ctx context.Context, plan domain.Plan, lg *domain.Ledger, managedRoots []string, recorded map[string]struct{}, ar ApplyRequest) []domain.Warning {
	var warnings []domain.Warning
	desired := plan.Desired
	staleRoots := make([]string, len(managedRoots)+1)
	copy(staleRoots, managedRoots)
	staleRoots[len(managedRoots)] = filepath.Dir(plan.Ledger)

	keys := slices.Sorted(maps.Keys(lg.Managed))

	stripLedger := domain.Ledger{Managed: maps.Clone(lg.Managed), UpdatedAt: lg.UpdatedAt}

	var nonInteractiveSkips int
	cleanup := newEmptyDirCleanup(staleRoots, e.FS)
	for _, k := range keys {
		cleanKey := filepath.Clean(k)
		if domain.IsMCPLedgerKey(k) {
			if _, ok := recorded[cleanKey]; !ok && !ar.DryRun {
				lg.Delete(k)
			}
			continue
		}
		if !domain.IsUnderAny(cleanKey, staleRoots) {
			continue
		}
		if _, ok := desired[cleanKey]; ok {
			continue
		}
		// Skip entries recorded in this apply cycle — they're current
		// even if not explicitly in Desired (e.g., copy-dir children).
		if _, ok := recorded[cleanKey]; ok {
			continue
		}

		if _, ok := ar.PruneOnlyStalePaths[cleanKey]; ok {
			if !ar.DryRun {
				lg.Delete(cleanKey)
			}
			continue
		}

		prune := e.pruneStalePath(ctx, stalePruneAction{
			Path:                     k,
			DisplayLabel:             ar.displayLabel(k),
			DisplayLabelIncludesPath: ar.LabelForPath != nil,
			Yes:                      ar.Yes,
			PrevDigest:               lg.PrevDigest(k),
			DryRun:                   ar.DryRun,
			Apply: func() []domain.Warning {
				if stripFn, isOwned := ar.StripFuncs[filepath.Clean(k)]; isOwned {
					if err := e.stripOwnedFile(k, stripLedger, stripFn); err != nil {
						return []domain.Warning{staleWarning(k, "strip managed keys: %v", err)}
					}
				} else {
					_ = e.FS.Remove(k)
					cleanup.MaybeCleanupParents(filepath.Dir(k))
				}
				return nil
			},
		})
		warnings = append(warnings, prune.Warnings...)
		if prune.NonInteractiveSkip {
			nonInteractiveSkips++
		}
		if !prune.DeleteLedger {
			continue
		}
		lg.Delete(k)
	}
	cleanup.Flush()

	if nonInteractiveSkips > 0 {
		warnings = append(warnings, domain.Warning{
			Field:   "stale",
			Message: fmt.Sprintf("%d user-modified stale file(s) skipped (use --yes to auto-confirm or run interactively)", nonInteractiveSkips),
		})
	}
	return warnings
}

// PruneLedgerManagedRequest controls cleanup of ledger-managed file paths
// under explicit roots. Unlike ApplyPlan stale reconciliation, this ignores MCP
// ledger keys because callers use it for narrow filesystem-root retirement.
type PruneLedgerManagedRequest struct {
	Yes          bool
	DryRun       bool
	LabelForPath func(string) string
}

func (r PruneLedgerManagedRequest) displayLabel(path string) string {
	if r.LabelForPath != nil {
		return r.LabelForPath(path)
	}
	return domain.DisplayPath(path)
}

// PruneLedgerManagedPaths removes ledger-tracked files under roots when their
// on-disk digest still matches the ledger. User-modified files require
// confirmation unless Yes is set.
func (e *Engine) PruneLedgerManagedPaths(ctx context.Context, ledgerPath string, roots []string, req PruneLedgerManagedRequest) ([]domain.Warning, error) {
	if ledgerPath == "" || len(roots) == 0 {
		return nil, nil
	}
	lg, ledgerWarnings, err := e.LoadLedger(ledgerPath)
	if err != nil {
		return ledgerWarnings, err
	}
	warnings := append([]domain.Warning{}, ledgerWarnings...)
	keys := slices.Sorted(maps.Keys(lg.Managed))
	cleanup := newEmptyDirCleanup(roots, e.FS)
	dirty := false
	var nonInteractiveSkips int
	for _, k := range keys {
		cleanKey := filepath.Clean(k)
		if domain.IsMCPLedgerKey(cleanKey) || !domain.IsUnderAny(cleanKey, roots) {
			continue
		}
		prune := e.pruneStalePath(ctx, stalePruneAction{
			Path:                     k,
			DisplayLabel:             req.displayLabel(k),
			DisplayLabelIncludesPath: req.LabelForPath != nil,
			Yes:                      req.Yes,
			PrevDigest:               lg.PrevDigest(k),
			DryRun:                   req.DryRun,
			Apply: func() []domain.Warning {
				_ = e.FS.Remove(k)
				cleanup.MaybeCleanupParents(filepath.Dir(k))
				return nil
			},
		})
		warnings = append(warnings, prune.Warnings...)
		if prune.NonInteractiveSkip {
			nonInteractiveSkips++
		}
		if !prune.DeleteLedger {
			continue
		}
		lg.Delete(k)
		dirty = true
	}
	cleanup.Flush()
	if nonInteractiveSkips > 0 {
		warnings = append(warnings, domain.Warning{
			Field:   "stale",
			Message: fmt.Sprintf("%d user-modified stale file(s) skipped (use --yes to auto-confirm or run interactively)", nonInteractiveSkips),
		})
	}
	if dirty {
		if err := e.SaveLedger(ledgerPath, lg, req.DryRun); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

type stalePruneAction struct {
	Path                     string
	DisplayLabel             string
	DisplayLabelIncludesPath bool
	Yes                      bool
	PrevDigest               string
	DryRun                   bool
	Apply                    func() []domain.Warning
}

type stalePruneResult struct {
	DeleteLedger       bool
	NonInteractiveSkip bool
	Warnings           []domain.Warning
}

func (e *Engine) pruneStalePath(ctx context.Context, action stalePruneAction) stalePruneResult {
	dec, err := e.shouldDelete(ctx, deleteRequest{
		Path:                     action.Path,
		DisplayLabel:             action.DisplayLabel,
		DisplayLabelIncludesPath: action.DisplayLabelIncludesPath,
		Yes:                      action.Yes,
		PrevDigest:               action.PrevDigest,
		DryRun:                   action.DryRun,
	})
	if err != nil {
		return stalePruneResult{Warnings: []domain.Warning{staleWarning(action.Path, "prompt error: %v", err)}}
	}
	switch dec {
	case DeleteNo:
		return stalePruneResult{}
	case DeleteSkippedNonInteractive:
		return stalePruneResult{
			NonInteractiveSkip: true,
			Warnings:           []domain.Warning{staleWarning(action.Path, "user-modified file skipped (non-interactive)")},
		}
	}
	if action.DryRun {
		return stalePruneResult{}
	}
	var warnings []domain.Warning
	if action.Apply != nil {
		warnings = append(warnings, action.Apply()...)
	}
	return stalePruneResult{DeleteLedger: true, Warnings: warnings}
}

// stripOwnedFile reads the file at path, applies stripFn to remove only
// the managed keys, and writes the result back. Used for shared config
// files (e.g. .claude.json) where aipack manages specific keys but must
// not delete the entire file.
func (e *Engine) stripOwnedFile(path string, ledger domain.Ledger, stripFn func([]byte, domain.Ledger) ([]byte, error)) error {
	content, err := e.FS.ReadFile(path)
	if err != nil {
		return err
	}
	stripped, err := stripFn(content, ledger)
	if err != nil {
		return err
	}
	return e.FS.WriteFile(path, stripped, 0o644)
}

func staleWarning(path, msg string, args ...any) domain.Warning {
	return domain.Warning{
		Path:    path,
		Field:   "stale",
		Message: fmt.Sprintf(msg, args...),
	}
}

// DeleteDecision classifies why a stale file was or was not deleted,
// allowing reconcileStaleEntries to produce distinct warnings per category.
type DeleteDecision int

const (
	// DeleteYes means the file should be removed.
	DeleteYes DeleteDecision = iota
	// DeleteNo means the user explicitly declined deletion.
	DeleteNo
	// DeleteSkippedNonInteractive means the file needs interactive confirmation
	// but no terminal is available and --yes was not set.
	DeleteSkippedNonInteractive
)

type deleteRequest struct {
	Path                     string
	DisplayLabel             string
	DisplayLabelIncludesPath bool
	Yes                      bool
	PrevDigest               string
	DryRun                   bool
}

// shouldDelete decides whether a stale file should be removed.
// If the file's on-disk digest matches prevDigest, it's safe (managed, unmodified).
// Otherwise, interactive confirmation is required unless yes or dryRun is set.
func (e *Engine) shouldDelete(ctx context.Context, req deleteRequest) (DeleteDecision, error) {
	if req.PrevDigest != "" {
		d, missing, err := e.pathDigestStatus(req.Path)
		if missing || (err == nil && d == req.PrevDigest) {
			return DeleteYes, nil
		}
	} else if _, missing, err := e.pathDigestStatus(req.Path); missing && err == nil {
		return DeleteYes, nil
	}
	if req.DryRun {
		return DeleteYes, nil
	}
	if req.Yes {
		return DeleteYes, nil
	}
	if e.Interact == nil || !e.Interact.IsTerminal() {
		return DeleteSkippedNonInteractive, nil
	}
	promptPath := req.DisplayLabel
	clean := domain.DisplayPath(req.Path)
	if promptPath == "" {
		promptPath = clean
	} else if promptPath != clean && !req.DisplayLabelIncludesPath {
		promptPath = fmt.Sprintf("%s (%s)", promptPath, clean)
	}
	ans, err := e.Interact.Prompt(ctx, fmt.Sprintf("Delete stale path? %s [y/N]: ", promptPath))
	if err != nil {
		return DeleteNo, err
	}
	if ans == "y" || ans == "yes" {
		return DeleteYes, nil
	}
	return DeleteNo, nil
}

// emptyDirCleanup removes empty parent directories left behind after stale
// file deletion, walking upward but never beyond the managed roots.
type emptyDirCleanup struct {
	prefixes []string
	queue    []string
	fs       FS
}

func newEmptyDirCleanup(prefixes []string, fs FS) *emptyDirCleanup {
	return &emptyDirCleanup{prefixes: prefixes, fs: fs}
}

func (c *emptyDirCleanup) MaybeCleanupParents(dir string) {
	if dir == "" || dir == "." {
		return
	}
	if !domain.IsUnderAny(dir, c.prefixes) {
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
	slices.SortFunc(uniq, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, d := range uniq {
		c.cleanupUp(d)
	}
}

func (c *emptyDirCleanup) cleanupUp(dir string) {
	cur := filepath.Clean(dir)
	for cur != "." && cur != string(filepath.Separator) {
		if !domain.IsUnderAny(cur, c.prefixes) {
			return
		}
		if err := c.fs.Remove(cur); err != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}
