package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// PlanOpKind identifies the type of a plan operation.
type PlanOpKind string

const (
	PlanOpRule     PlanOpKind = "rule"
	PlanOpWorkflow PlanOpKind = "workflow"
	PlanOpAgent    PlanOpKind = "agent"
	PlanOpSkill    PlanOpKind = "skill"
	PlanOpSettings PlanOpKind = "settings"
	PlanOpMCP      PlanOpKind = "mcp"
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
	DiffKind   domain.DiffKind  // create, managed, conflict
	Diff       string           // unified diff text (empty for new files)
	MergeOps   []engine.MergeOp // merge operations for settings (nil for non-settings)
}

// PlanSummary holds a classified sync plan with per-file diff information.
// Identical (already-synced) entries are filtered out.
// HarnessLedgerInfo holds per-harness ledger metadata for display.
type HarnessLedgerInfo struct {
	Harness   string
	Path      string
	Files     int
	UpdatedAt int64 // epoch seconds; 0 if unknown
}

type PlanSummary struct {
	Ops            []PlanOp
	NumRules       int
	NumWorkflows   int
	NumAgents      int
	NumSkills      int
	NumSettings    int
	NumMCP         int
	NumPrunes      int
	LedgerPath     string
	LedgerFiles    int
	HarnessLedgers []HarnessLedgerInfo
	Warnings       []domain.Warning
}

// NumContent returns the total number of content changes (rules + workflows + agents + skills).
func (ps PlanSummary) NumContent() int {
	return ps.NumRules + ps.NumWorkflows + ps.NumAgents + ps.NumSkills
}

// TotalChanges returns the total number of pending changes.
func (ps PlanSummary) TotalChanges() int {
	return ps.NumContent() + ps.NumSettings + ps.NumMCP + ps.NumPrunes
}

// PlanWithDiffs plans a sync and classifies each action against on-disk state,
// filtering out identical (already-synced) entries. This is the "dry-run with
// details" entry point used by the TUI and potentially CLI --dry-run.
func PlanWithDiffs(profile domain.Profile, req SyncRequest, reg *harness.Registry) (PlanSummary, error) {
	var summary PlanSummary

	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}

	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			return PlanSummary{}, err
		}
		h, err := reg.Lookup(hid)
		if err != nil {
			return PlanSummary{}, err
		}

		planReq := engine.PlanRequest{
			Scope:        req.Scope,
			Harnesses:    []domain.Harness{hid},
			ProjectDir:   req.ProjectDir,
			Home:         req.Home,
			SkipSettings: req.SkipSettings,
		}

		plan, err := engine.PlanSync(profile, planReq, planners)
		if err != nil {
			return PlanSummary{}, err
		}

		captured, err := h.Capture(harness.CaptureContext{
			Scope:      req.Scope,
			ProjectDir: req.ProjectDir,
			Home:       req.Home,
		})
		if err != nil {
			return PlanSummary{}, err
		}
		summary.Warnings = append(summary.Warnings, captured.Warnings...)
		currentMCP, err := capturedMCPDigests(captured)
		if err != nil {
			return PlanSummary{}, err
		}

		// Load per-harness ledger.
		var lg domain.Ledger
		if plan.Ledger != "" {
			if l, ledgerWarn, lerr := engine.LoadLedger(plan.Ledger); lerr == nil {
				lg = l
				if ledgerWarn != "" {
					summary.Warnings = append(summary.Warnings, domain.Warning{
						Field: "ledger", Message: ledgerWarn,
					})
				}
			}
		}

		// Classify writes.
		for _, w := range plan.Writes {
			diffKind, werr := classifyWriteKind(w, lg)
			if werr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: w.Dst, Message: fmt.Sprintf("classify: %v", werr),
				})
				continue
			}
			if diffKind == domain.DiffIdentical {
				continue
			}
			fd, werr := engine.ClassifyFile(w.Dst, w.Content, filepath.Base(w.Dst), w.SourcePack, lg)
			if werr != nil && diffKind != domain.DiffCreate {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: w.Dst, Message: fmt.Sprintf("classify diff: %v", werr),
				})
			}
			opKind := inferContentKind(w.Dst)
			incrContentCount(&summary, opKind)
			summary.Ops = append(summary.Ops, PlanOp{
				Kind:       opKind,
				Dst:        w.Dst,
				SourcePack: w.SourcePack,
				Size:       len(w.Content),
				Content:    w.Content,
				DiffKind:   diffKind,
				Diff:       classifyWriteDiffText(fd, diffKind),
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
			kind := inferContentKind(c.Dst)
			incrContentCount(&summary, kind)
			summary.Ops = append(summary.Ops, PlanOp{
				Kind:       kind,
				Dst:        c.Dst,
				Src:        c.Src,
				SourcePack: c.SourcePack,
			})
		}

		// Classify settings and MCP.
		sOps, sCount, sErr := classifySettingsOps(plan.Settings, lg, PlanOpSettings)
		if sErr != nil {
			return summary, fmt.Errorf("classify settings: %w", sErr)
		}
		summary.NumSettings += sCount
		summary.Ops = append(summary.Ops, sOps...)

		mOps, mCount, mErr := classifyMCPOps(plan.MCPServers, currentMCP, lg)
		if mErr != nil {
			return summary, fmt.Errorf("classify mcp: %w", mErr)
		}
		summary.NumMCP += mCount
		summary.Ops = append(summary.Ops, mOps...)

		// Detect prune candidates per harness.
		managedRoots := h.ManagedRoots(req.Scope, baseDir, req.Home)
		if candidates, perr := engine.PruneCandidatesWithLedger(plan, managedRoots, lg); perr == nil {
			for _, p := range candidates {
				summary.NumPrunes++
				summary.Ops = append(summary.Ops, PlanOp{
					Kind: PlanOpPrune,
					Dst:  p,
				})
			}
		}

		if plan.Ledger != "" {
			summary.LedgerPath = plan.Ledger
			summary.LedgerFiles += len(lg.Managed)
			summary.HarnessLedgers = append(summary.HarnessLedgers, HarnessLedgerInfo{
				Harness:   string(hid),
				Path:      plan.Ledger,
				Files:     len(lg.Managed),
				UpdatedAt: lg.UpdatedAt,
			})
		}
	}

	return summary, nil
}

// inferContentKind determines the content type from a destination path by
// checking parent directory names. Harnesses use varying names for the same
// concept (e.g. "commands" vs "workflows"), so we check for all known variants.
func inferContentKind(dst string) PlanOpKind {
	// Walk up path components looking for a known directory name.
	dir := filepath.Dir(dst)
	for dir != "." && dir != string(filepath.Separator) {
		base := strings.ToLower(filepath.Base(dir))
		switch base {
		case "rules", ".clinerules":
			return PlanOpRule
		case "commands", "workflows":
			return PlanOpWorkflow
		case "agents":
			return PlanOpAgent
		case "skills":
			return PlanOpSkill
		}
		dir = filepath.Dir(dir)
	}
	return PlanOpRule // fallback; content writes are predominantly rules
}

func incrContentCount(s *PlanSummary, kind PlanOpKind) {
	switch kind {
	case PlanOpRule:
		s.NumRules++
	case PlanOpWorkflow:
		s.NumWorkflows++
	case PlanOpAgent:
		s.NumAgents++
	case PlanOpSkill:
		s.NumSkills++
	}
}

func classifyWriteKind(w domain.WriteAction, lg domain.Ledger) (domain.DiffKind, error) {
	dst := filepath.Clean(w.Dst)
	onDisk, err := os.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.DiffCreate, nil
		}
		return "", err
	}
	diskDigest := domain.SingleFileDigest(onDisk)
	if diskDigest == w.EffectiveDigest() {
		return domain.DiffIdentical, nil
	}
	prev := lg.PrevDigest(dst)
	if prev != "" && diskDigest == prev {
		return domain.DiffManaged, nil
	}
	return domain.DiffConflict, nil
}

func classifyWriteDiffText(fd engine.FileDiff, diffKind domain.DiffKind) string {
	if diffKind == domain.DiffCreate || diffKind == domain.DiffIdentical {
		return ""
	}
	return fd.Diff
}

// ContentCounts holds counts of content items by type for display.
type ContentCounts struct {
	Rules, Workflows, Agents, Skills int
}

// String returns a compact summary like "3 rules, 2 skills".
// Zero-count types are omitted.
func (c ContentCounts) String() string {
	var parts []string
	if c.Rules > 0 {
		parts = append(parts, fmt.Sprintf("%d rules", c.Rules))
	}
	if c.Workflows > 0 {
		parts = append(parts, fmt.Sprintf("%d workflows", c.Workflows))
	}
	if c.Agents > 0 {
		parts = append(parts, fmt.Sprintf("%d agents", c.Agents))
	}
	if c.Skills > 0 {
		parts = append(parts, fmt.Sprintf("%d skills", c.Skills))
	}
	if len(parts) == 0 {
		return "0 content"
	}
	return strings.Join(parts, ", ")
}

// Total returns the sum of all content counts.
func (c ContentCounts) Total() int {
	return c.Rules + c.Workflows + c.Agents + c.Skills
}

// CountContentTypes tallies content items from a raw Plan by inferring
// content type from destination paths.
func CountContentTypes(plan domain.Plan) ContentCounts {
	var s PlanSummary
	for _, w := range plan.Writes {
		incrContentCount(&s, inferContentKind(w.Dst))
	}
	for _, cp := range plan.Copies {
		incrContentCount(&s, inferContentKind(cp.Dst))
	}
	return ContentCounts{
		Rules:     s.NumRules,
		Workflows: s.NumWorkflows,
		Agents:    s.NumAgents,
		Skills:    s.NumSkills,
	}
}

// classifySettingsOps computes diffs for settings/plugin actions and returns plan ops.
func classifySettingsOps(actions []domain.SettingsAction, lg domain.Ledger, kind PlanOpKind) ([]PlanOp, int, error) {
	if len(actions) == 0 {
		return nil, 0, nil
	}
	diffs, err := engine.ComputeSettingsDiffs(actions, lg)
	if err != nil {
		return nil, 0, err
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
			MergeOps:   fd.MergeOps,
		})
	}
	return ops, count, nil
}

func classifyMCPOps(actions []domain.MCPAction, current map[string]string, lg domain.Ledger) ([]PlanOp, int, error) {
	ops := make([]PlanOp, 0, len(actions))
	for _, action := range actions {
		diffKind, err := classifyMCPDiffKind(action, current, lg)
		if err != nil {
			return nil, 0, err
		}
		if diffKind == domain.DiffIdentical {
			continue
		}
		ops = append(ops, PlanOp{
			Kind:       PlanOpMCP,
			Dst:        action.ConfigPath,
			SourcePack: action.SourcePack,
			Size:       len(action.Content),
			Content:    action.Content,
			DiffKind:   diffKind,
		})
	}
	return ops, len(ops), nil
}

func classifyMCPDiffKind(action domain.MCPAction, current map[string]string, lg domain.Ledger) (domain.DiffKind, error) {
	return classifyMCPAction(action, current, lg)
}
