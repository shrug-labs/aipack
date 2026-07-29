package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

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
	PlanOpHook     PlanOpKind = "hook"
	PlanOpSettings PlanOpKind = "settings"
	PlanOpMCP      PlanOpKind = "mcp"
	PlanOpStale    PlanOpKind = "stale"
)

// PlanOp represents a single classified plan operation.
type PlanOp struct {
	Kind       PlanOpKind
	Dst        string
	DisplayDst string
	Src        string // optional source file fallback when Content is not populated
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
	NumHooks       int
	NumSettings    int
	NumMCP         int
	NumStale       int
	LedgerPath     string
	LedgerFiles    int
	HarnessLedgers []HarnessLedgerInfo
	Warnings       []domain.Warning
}

// NumContent returns the total number of content changes.
func (ps PlanSummary) NumContent() int {
	return ps.NumRules + ps.NumWorkflows + ps.NumAgents + ps.NumSkills + ps.NumHooks
}

// TotalChanges returns the total number of pending changes.
func (ps PlanSummary) TotalChanges() int {
	return ps.NumContent() + ps.NumSettings + ps.NumMCP + ps.NumStale
}

func (ps *PlanSummary) Merge(other PlanSummary) {
	ps.Ops = append(ps.Ops, other.Ops...)
	ps.NumRules += other.NumRules
	ps.NumWorkflows += other.NumWorkflows
	ps.NumAgents += other.NumAgents
	ps.NumSkills += other.NumSkills
	ps.NumHooks += other.NumHooks
	ps.NumSettings += other.NumSettings
	ps.NumMCP += other.NumMCP
	ps.NumStale += other.NumStale
	ps.LedgerFiles += other.LedgerFiles
	ps.HarnessLedgers = append(ps.HarnessLedgers, other.HarnessLedgers...)
	ps.Warnings = append(ps.Warnings, other.Warnings...)
	if other.LedgerPath != "" {
		ps.LedgerPath = other.LedgerPath
	}
}

// PlanWithDiffs plans a sync and classifies each action against on-disk state,
// filtering out identical (already-synced) entries. This is the "dry-run with
// details" entry point used by the TUI and potentially CLI --dry-run.
func PlanWithDiffs(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry) (PlanSummary, error) {
	var summary PlanSummary
	knownPacks := knownPacksFromRoots(resolvePackRoots(profile))
	hid, err := SingleSyncHarness(req.Harnesses)
	if err != nil {
		return PlanSummary{}, err
	}

	planners, err := reg.AsPlanners([]domain.Harness{hid})
	if err != nil {
		return PlanSummary{}, err
	}
	h, err := reg.Lookup(hid)
	if err != nil {
		return PlanSummary{}, err
	}

	planReq := planRequestForHarness(req, hid)
	labelFor := func(path string) string {
		return syncDisplayPath(hid, path)
	}

	plan, err := engine.PlanSync(ctx, profile, planReq, planners)
	if err != nil {
		return PlanSummary{}, err
	}

	captured, err := h.Capture(ctx, captureContextForHarness(req.TargetSpec, hid, knownPacks))
	if err != nil {
		return PlanSummary{}, err
	}
	summary.Warnings = append(summary.Warnings, captured.Warnings...)

	// Load per-harness ledger.
	var lg domain.Ledger
	if plan.Ledger != "" {
		if l, ledgerWarnings, lerr := eng.LoadLedger(plan.Ledger); lerr == nil {
			lg = l
			summary.Warnings = append(summary.Warnings, ledgerWarnings...)
		}
	}

	// Classify writes.
	for _, w := range plan.Writes {
		fd, werr := eng.ClassifyWrite(w, labelFor(w.Dst), lg)
		if werr != nil {
			summary.Warnings = append(summary.Warnings, domain.Warning{
				Path: w.Dst, Message: fmt.Sprintf("classify: %v", werr),
			})
			continue
		}
		if fd.Kind == domain.DiffIdentical {
			continue
		}
		opKind := planOpKindForWrite(w)
		incrContentCount(&summary, opKind)
		summary.Ops = append(summary.Ops, PlanOp{
			Kind:       opKind,
			Dst:        w.Dst,
			DisplayDst: planOpDisplayDst(fd),
			SourcePack: w.SourcePack,
			Size:       len(w.Content),
			Content:    w.Content,
			DiffKind:   fd.Kind,
			Diff:       classifyWriteDiffText(fd, fd.Kind),
		})
	}

	// Classify copies.
	for _, c := range plan.Copies {
		switch c.Kind {
		case domain.CopyKindDir:
			fds, cerr := eng.ClassifyCopyWithOptions(c.Src, c.Dst, c.SourcePack, lg, engine.ClassifyCopyOptions{
				LabelForPath:   labelFor,
				SourceBoundary: c.SourceBoundary,
			})
			if cerr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: c.Src, Message: fmt.Sprintf("classify dir: %v", cerr),
				})
				continue
			}
			for _, fd := range fds {
				appendPlanOpFromFileDiff(&summary, fd, inferContentKind(fd.Dst))
			}
		case domain.CopyKindFile:
			fd, cerr := eng.ClassifyCopyFileWithOptions(c.Src, c.Dst, c.SourcePack, lg, engine.ClassifyCopyOptions{
				LabelForPath:   labelFor,
				SourceBoundary: c.SourceBoundary,
			})
			if cerr != nil {
				summary.Warnings = append(summary.Warnings, domain.Warning{
					Path: c.Dst, Message: fmt.Sprintf("classify: %v", cerr),
				})
				continue
			}
			appendPlanOpFromFileDiff(&summary, fd, inferContentKind(c.Dst))
		}
	}

	// Classify settings and MCP.
	sOps, sCount, sErr := classifySettingsOps(eng, plan.Settings, lg, PlanOpSettings, labelFor)
	if sErr != nil {
		return summary, fmt.Errorf("classify settings: %w", sErr)
	}
	summary.NumSettings += sCount
	summary.Ops = append(summary.Ops, sOps...)

	// MCP config actions (plan.MCP) are file-level MergeMode settings
	// that are never gated by SkipSettings. Process them through the
	// same three-way merge classification as plan.Settings.
	mcpCfgOps, mcpCfgCount, mcpCfgErr := classifySettingsOps(eng, plan.MCP, lg, PlanOpMCP, labelFor)
	if mcpCfgErr != nil {
		return summary, fmt.Errorf("classify mcp config: %w", mcpCfgErr)
	}
	summary.NumMCP += mcpCfgCount
	summary.Ops = append(summary.Ops, mcpCfgOps...)

	// Detect stale files.
	layout := h.Layout(req.Scope, targetDirForHarness(req.TargetSpec, hid), req.Home)
	cleanup := staleCleanupContextForHarness(eng, reg, req.TargetSpec, hid, layout, nil)
	if candidates, perr := eng.StaleCandidatesWithLedger(plan, cleanup.Roots, lg); perr == nil {
		cleanup = staleCleanupContextForHarness(eng, reg, req.TargetSpec, hid, layout, candidates)
		for _, p := range candidates {
			if _, protected := cleanup.Protected[filepath.Clean(p)]; protected {
				continue
			}
			summary.NumStale++
			summary.Ops = append(summary.Ops, PlanOp{
				Kind:       PlanOpStale,
				Dst:        p,
				DisplayDst: labelFor(p),
			})
		}
	}
	for _, candidate := range newInactiveStaleContext(eng, reg, req.TargetSpec, hid, cleanup.StaleRoots).candidates() {
		summary.NumStale++
		summary.Ops = append(summary.Ops, PlanOp{
			Kind:       PlanOpStale,
			Dst:        candidate.Path,
			DisplayDst: syncDisplayPath(candidate.Harness, candidate.Path),
		})
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

	return summary, nil
}

// PlanWithDiffsEach classifies the sync plan for each resolved harness and
// merges the results into one summary. Counts and operations sum across
// harnesses — every harness writes its own copy of the profile's content, so a
// rule synced to two harnesses is two pending file changes. Used by the TUI to
// preview a default sync that targets all configured harnesses independently.
func PlanWithDiffsEach(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry) (PlanSummary, error) {
	if err := ValidateSyncHarnesses(req.Harnesses); err != nil {
		return PlanSummary{}, err
	}
	activeHarnesses := append([]domain.Harness{}, req.Harnesses...)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	summaries := make([]PlanSummary, len(req.Harnesses))
	errs := make([]error, len(req.Harnesses))
	var wg sync.WaitGroup
	for i, hid := range req.Harnesses {
		wg.Add(1)
		go func(i int, hid domain.Harness) {
			defer wg.Done()
			single := req
			single.ActiveHarnesses = activeHarnesses
			single.Harnesses = []domain.Harness{hid}
			summaries[i], errs[i] = PlanWithDiffs(ctx, eng, profile, single, reg)
			if errs[i] != nil {
				cancel()
			}
		}(i, hid)
	}
	wg.Wait()

	var merged PlanSummary
	for i, err := range errs {
		if err != nil {
			return PlanSummary{}, err
		}
		merged.Merge(summaries[i])
	}
	return merged, nil
}

func appendPlanOpFromFileDiff(summary *PlanSummary, fd engine.FileDiff, kind PlanOpKind) {
	if fd.Kind == domain.DiffIdentical {
		return
	}
	incrContentCount(summary, kind)
	summary.Ops = append(summary.Ops, PlanOp{
		Kind:       kind,
		Dst:        fd.Dst,
		DisplayDst: planOpDisplayDst(fd),
		SourcePack: fd.SourcePack,
		Size:       len(fd.Desired),
		Content:    fd.Desired,
		DiffKind:   fd.Kind,
		Diff:       fd.Diff,
		MergeOps:   fd.MergeOps,
	})
}

func planOpDisplayDst(fd engine.FileDiff) string {
	if fd.Label != "" {
		return fd.Label
	}
	return fd.Dst
}

func planOpKindForWrite(w domain.WriteAction) PlanOpKind {
	if kind, ok := planOpKindForCategory(w.Category); ok {
		return kind
	}
	return inferContentKind(w.Dst)
}

func planOpKindForCategory(cat domain.PackCategory) (PlanOpKind, bool) {
	switch cat {
	case domain.CategoryRules:
		return PlanOpRule, true
	case domain.CategoryWorkflows:
		return PlanOpWorkflow, true
	case domain.CategoryAgents:
		return PlanOpAgent, true
	case domain.CategorySkills:
		return PlanOpSkill, true
	case domain.CategoryHooks:
		return PlanOpHook, true
	case domain.CategorySettings:
		return PlanOpSettings, true
	case domain.CategoryMCP:
		return PlanOpMCP, true
	default:
		return "", false
	}
}

// inferContentKind determines the content type from a destination path by
// checking parent directory names. Harnesses use varying names for the same
// concept (e.g. "commands" vs "workflows"), so we check for all known variants.
func inferContentKind(dst string) PlanOpKind {
	if strings.EqualFold(filepath.Base(dst), "hooks.json") {
		return PlanOpHook
	}
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
		case "hooks":
			return PlanOpHook
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
	case PlanOpHook:
		s.NumHooks++
	}
}

func classifyWriteKind(eng *engine.Engine, w domain.WriteAction, lg domain.Ledger) (domain.DiffKind, error) {
	return eng.ClassifyWriteKind(w, lg)
}

func classifyWriteDiffText(fd engine.FileDiff, diffKind domain.DiffKind) string {
	if diffKind == domain.DiffCreate || diffKind == domain.DiffIdentical {
		return ""
	}
	return fd.Diff
}

// ContentCounts holds counts of content items by type for display.
type ContentCounts struct {
	Rules     int `json:"rules"`
	Skills    int `json:"skills"`
	Workflows int `json:"workflows"`
	Agents    int `json:"agents"`
	Hooks     int `json:"hooks"`
	Plugins   int `json:"plugins"`
	Prompts   int `json:"prompts"`
	MCP       int `json:"mcp"`
}

func (c *ContentCounts) Add(category domain.PackCategory) {
	switch category {
	case domain.CategoryRules:
		c.Rules++
	case domain.CategorySkills:
		c.Skills++
	case domain.CategoryWorkflows:
		c.Workflows++
	case domain.CategoryAgents:
		c.Agents++
	case domain.CategoryHooks:
		c.Hooks++
	case domain.CategoryPlugins:
		c.Plugins++
	case domain.CategoryPrompts:
		c.Prompts++
	case domain.CategoryMCP:
		c.MCP++
	}
}

// String returns a compact summary like "3 rules, 2 skills, 1 agent".
func (c ContentCounts) String() string {
	var parts []string
	for _, pair := range c.pairs() {
		if pair.n > 0 {
			if pair.n == 1 {
				parts = append(parts, fmt.Sprintf("1 %s", pair.label))
			} else {
				parts = append(parts, fmt.Sprintf("%d %ss", pair.n, pair.label))
			}
		}
	}
	if len(parts) == 0 {
		return "0 content"
	}
	return strings.Join(parts, ", ")
}

// IsZero returns true when all counts are zero.
func (c ContentCounts) IsZero() bool {
	return c.Total() == 0
}

func (c ContentCounts) Total() int {
	return c.Rules + c.Skills + c.Workflows + c.Agents + c.Hooks + c.Plugins + c.Prompts + c.MCP
}

func (c ContentCounts) pairs() [8]struct {
	n     int
	label string
} {
	return [8]struct {
		n     int
		label string
	}{
		{c.Rules, "rule"},
		{c.Skills, "skill"},
		{c.Workflows, "workflow"},
		{c.Agents, "agent"},
		{c.Hooks, "hook"},
		{c.Plugins, "plugin"},
		{c.Prompts, "prompt"},
		{c.MCP, "mcp-server"},
	}
}

// CountProfileContent tallies content items from the profile's typed
// collections. This counts source resources, not plan-level file writes,
// so counts are stable regardless of how many harnesses are targeted.
func CountProfileContent(p domain.Profile) ContentCounts {
	return ContentCounts{
		Rules:     len(p.AllRules()),
		Workflows: len(p.AllWorkflows()),
		Agents:    len(p.AllAgents()),
		Skills:    len(p.AllSkills()),
		Hooks:     len(p.AllHooks()),
		Plugins:   len(p.AllPlugins()),
		MCP:       len(p.MCPServers),
	}
}

// classifySettingsOps computes diffs for settings/plugin actions and returns plan ops.
func classifySettingsOps(eng *engine.Engine, actions []domain.SettingsAction, lg domain.Ledger, kind PlanOpKind, labelFor func(string) string) ([]PlanOp, int, error) {
	if len(actions) == 0 {
		return nil, 0, nil
	}
	diffs, err := eng.ComputeSettingsDiffs(engine.LabelSettingsActions(actions, labelFor), lg)
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
			DisplayDst: planOpDisplayDst(fd),
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
