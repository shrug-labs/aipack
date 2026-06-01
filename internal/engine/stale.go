package engine

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
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
		if domain.IsMCPLedgerKey(k) {
			if _, ok := recorded[filepath.Clean(k)]; !ok && !ar.DryRun {
				lg.Delete(k)
			}
			continue
		}
		if !domain.IsUnderAny(k, staleRoots) {
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

		// If the path is already gone, remove the ledger entry without prompting.
		if _, err := e.FS.Stat(k); os.IsNotExist(err) {
			if !ar.DryRun {
				lg.Delete(k)
			}
			continue
		}

		dec, err := e.shouldDelete(ctx, k, ar.Yes, lg.PrevDigest(k), ar.DryRun)
		if err != nil {
			warnings = append(warnings, staleWarning(k, "prompt error: %v", err))
			continue
		}
		switch dec {
		case DeleteNo:
			continue
		case DeleteSkippedNonInteractive:
			nonInteractiveSkips++
			warnings = append(warnings, staleWarning(k, "user-modified file skipped (non-interactive)"))
			continue
		}
		if ar.DryRun {
			continue
		}

		if stripFn, isOwned := ar.StripFuncs[filepath.Clean(k)]; isOwned {
			if err := e.stripOwnedFile(k, stripLedger, stripFn); err != nil {
				warnings = append(warnings, staleWarning(k, "strip managed keys: %v", err))
			}
		} else {
			_ = e.FS.Remove(k)
			cleanup.MaybeCleanupParents(filepath.Dir(k))
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

// shouldDelete decides whether a stale file should be removed.
// If the file's on-disk digest matches prevDigest, it's safe (managed, unmodified).
// Otherwise, interactive confirmation is required unless yes or dryRun is set.
func (e *Engine) shouldDelete(ctx context.Context, path string, yes bool, prevDigest string, dryRun bool) (DeleteDecision, error) {
	if prevDigest != "" {
		if d, err := e.pathDigest(path); err == nil && d == prevDigest {
			return DeleteYes, nil
		}
	}
	if dryRun {
		return DeleteYes, nil
	}
	if yes {
		return DeleteYes, nil
	}
	if e.Interact == nil || !e.Interact.IsTerminal() {
		return DeleteSkippedNonInteractive, nil
	}
	ans, err := e.Interact.Prompt(ctx, fmt.Sprintf("Delete path? %s [y/N]: ", path))
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
