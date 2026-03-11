package engine

import (
	"fmt"

	"github.com/shrug-labs/aipack/internal/domain"
)

// PlanRequest describes what to sync.
type PlanRequest struct {
	Scope        domain.Scope
	Harnesses    []domain.Harness
	ProjectDir   string
	Home         string // $HOME — threaded explicitly for testability
	SkipSettings bool
}

// Planner is the interface harness adapters implement for plan contribution.
// Each harness converts typed content into a Fragment of writes/copies/settings.
type Planner interface {
	ID() domain.Harness
	Plan(ctx SyncContext) (domain.Fragment, error)
}

// SyncContext provides typed content and config to harness planners.
// Profile carries all resolved content (rules, agents, workflows, skills,
// MCP servers, settings, plugins) — replacing the former 4 separate fields.
type SyncContext struct {
	Scope        domain.Scope
	TargetDir    string         // project dir or $HOME
	Home         string         // $HOME — always set, even in project scope (needed by Cline)
	Profile      domain.Profile // fully-resolved profile with typed content
	SkipSettings bool
}

// PlanSync produces a sync Plan by asking each harness planner to contribute a Fragment.
// The Profile must already be fully resolved (via engine.Resolve).
func PlanSync(profile domain.Profile, req PlanRequest, harnesses []Planner) (domain.Plan, error) {
	if req.Scope == domain.ScopeGlobal && req.Home == "" {
		return domain.Plan{}, fmt.Errorf("HOME is not set (required for global scope)")
	}

	plan := domain.Plan{Desired: map[string]struct{}{}}
	plan.Ledger = ledgerPath(req)

	targetDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		targetDir = req.Home
	}

	// Each harness contributes a Fragment.
	for _, h := range harnesses {
		ctx := SyncContext{
			Scope:        req.Scope,
			TargetDir:    targetDir,
			Home:         req.Home,
			Profile:      profile,
			SkipSettings: req.SkipSettings,
		}
		frag, err := h.Plan(ctx)
		if err != nil {
			return domain.Plan{}, fmt.Errorf("harness %s: %w", h.ID(), err)
		}
		frag.Apply(&plan)
	}

	return plan, nil
}

// ledgerPath computes the ledger file path for a plan request.
// Delegates to LedgerPathForScope to avoid duplicating path logic.
func ledgerPath(req PlanRequest) string {
	names := make([]string, len(req.Harnesses))
	for i, h := range req.Harnesses {
		names[i] = string(h)
	}
	return LedgerPathForScope(req.Scope, req.ProjectDir, req.Home, names)
}
