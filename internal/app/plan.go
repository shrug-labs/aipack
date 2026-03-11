package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// PlanOpKind identifies the type of a plan operation.
type PlanOpKind string

const (
	PlanOpWrite    PlanOpKind = "write"
	PlanOpCopy     PlanOpKind = "copy"
	PlanOpSettings PlanOpKind = "settings"
	PlanOpPlugin   PlanOpKind = "plugin"
	PlanOpPrune    PlanOpKind = "prune"
)

// PlanOp represents a single classified plan operation.
type PlanOp struct {
	Kind       PlanOpKind
	Dst        string
	Src        string // copies only
	SourcePack string
	Size       int
	Content    []byte
	DiffKind   domain.DiffKind // create, managed, conflict
	Diff       string          // unified diff text (empty for new files)
}

// PlanSummary holds a classified sync plan with per-file diff information.
// Identical (already-synced) entries are filtered out.
type PlanSummary struct {
	Ops         []PlanOp
	NumWrites   int
	NumCopies   int
	NumSettings int
	NumPlugins  int
	NumPrunes   int
	LedgerPath  string
	LedgerFiles int
	Warnings    []domain.Warning
}

// TotalChanges returns the total number of pending changes.
func (ps PlanSummary) TotalChanges() int {
	return ps.NumWrites + ps.NumCopies + ps.NumSettings + ps.NumPlugins + ps.NumPrunes
}

// PlanWithDiffs plans a sync and classifies each action against on-disk state,
// filtering out identical (already-synced) entries. This is the "dry-run with
// details" entry point used by the TUI and potentially CLI --dry-run.
func PlanWithDiffs(profile domain.Profile, req SyncRequest, reg *harness.Registry) (PlanSummary, error) {
	planners, err := reg.AsPlanners(req.Harnesses)
	if err != nil {
		return PlanSummary{}, err
	}

	planReq := req.toPlanRequest()

	plan, err := engine.PlanSync(profile, planReq, planners)
	if err != nil {
		return PlanSummary{}, err
	}

	// Load ledger for accurate diff computation.
	var lg domain.Ledger
	var summary PlanSummary
	if plan.Ledger != "" {
		if l, ledgerWarn, lerr := engine.LoadLedger(plan.Ledger); lerr == nil {
			lg = l
			if ledgerWarn != "" {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Field:   "ledger",
					Message: ledgerWarn,
				})
			}
		}
	}

	// Classify writes.
	for _, w := range plan.Writes {
		fd, werr := engine.ClassifyFile(w.Dst, w.Content, filepath.Base(w.Dst), w.SourcePack, lg)
		if werr != nil {
			summary.Warnings = append(summary.Warnings, domain.Warning{
				Path: w.Dst, Message: fmt.Sprintf("classify: %v", werr),
			})
			continue
		}
		if fd.Kind == domain.DiffIdentical {
			continue
		}
		summary.NumWrites++
		summary.Ops = append(summary.Ops, PlanOp{
			Kind:       PlanOpWrite,
			Dst:        w.Dst,
			SourcePack: w.SourcePack,
			Size:       len(w.Content),
			Content:    w.Content,
			DiffKind:   fd.Kind,
			Diff:       fd.Diff,
		})
	}

	// Classify copies.
	for _, c := range plan.Copies {
		switch c.Kind {
		case domain.CopyKindDir:
			fds, cerr := engine.ClassifyCopy(c.Src, c.Dst, c.SourcePack, lg)
			if cerr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: c.Src, Message: fmt.Sprintf("classify dir: %v", cerr),
				})
				continue
			}
			changed := false
			for _, fd := range fds {
				if fd.Kind != domain.DiffIdentical {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
		case domain.CopyKindFile:
			content, cerr := os.ReadFile(c.Src)
			if cerr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: c.Src, Message: fmt.Sprintf("read: %v", cerr),
				})
				continue
			}
			fd, cerr := engine.ClassifyFile(c.Dst, content, filepath.Base(c.Dst), c.SourcePack, lg)
			if cerr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: c.Dst, Message: fmt.Sprintf("classify: %v", cerr),
				})
				continue
			}
			if fd.Kind == domain.DiffIdentical {
				continue
			}
		}
		summary.NumCopies++
		summary.Ops = append(summary.Ops, PlanOp{
			Kind:       PlanOpCopy,
			Dst:        c.Dst,
			Src:        c.Src,
			SourcePack: c.SourcePack,
		})
	}

	// Classify settings and plugins.
	sOps, sCount := classifySettingsOps(plan.Settings, lg, PlanOpSettings)
	summary.NumSettings = sCount
	summary.Ops = append(summary.Ops, sOps...)

	pOps, pCount := classifySettingsOps(plan.Plugins, lg, PlanOpPlugin)
	summary.NumPlugins = pCount
	summary.Ops = append(summary.Ops, pOps...)

	// Detect prune candidates.
	managedRoots := computeManagedRoots(reg, req)
	if candidates, perr := engine.PruneCandidatesWithLedger(plan, managedRoots, lg); perr == nil {
		for _, p := range candidates {
			summary.NumPrunes++
			summary.Ops = append(summary.Ops, PlanOp{
				Kind: PlanOpPrune,
				Dst:  p,
			})
		}
	}

	// Ledger summary.
	if plan.Ledger != "" {
		summary.LedgerPath = plan.Ledger
		summary.LedgerFiles = len(lg.Managed)
	}

	return summary, nil
}

// classifySettingsOps computes diffs for settings/plugin actions and returns plan ops.
func classifySettingsOps(actions []domain.SettingsAction, lg domain.Ledger, kind PlanOpKind) ([]PlanOp, int) {
	if len(actions) == 0 {
		return nil, 0
	}
	diffs, err := engine.ComputeSettingsDiffs(actions, lg)
	if err != nil {
		return nil, 0
	}
	var ops []PlanOp
	count := 0
	for _, fd := range diffs {
		if fd.Kind == domain.DiffIdentical {
			continue
		}
		count++
		ops = append(ops, PlanOp{
			Kind:       kind,
			Dst:        fd.Dst,
			SourcePack: fd.SourcePack,
			Size:       len(fd.Desired),
			Content:    fd.Desired,
			DiffKind:   fd.Kind,
			Diff:       fd.Diff,
		})
	}
	return ops, count
}
