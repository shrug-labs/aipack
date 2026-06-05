package app

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

func cleanupRootsForLayout(layout harness.Layout) []string {
	roots := append([]string{}, layout.ValidationRoots...)
	roots = append(roots, cloneStaleRoots(layout)...)
	return roots
}

func cloneStaleRoots(layout harness.Layout) []string {
	if len(layout.StaleRoots) == 0 {
		return nil
	}
	return append([]string{}, layout.StaleRoots...)
}

type staleCleanupContext struct {
	StaleRoots []string
	Roots      []string
	Protected  map[string]struct{}
}

func staleCleanupContextForHarness(eng *engine.Engine, reg *harness.Registry, spec TargetSpec, hid domain.Harness, layout harness.Layout, currentCandidates []string) staleCleanupContext {
	staleRoots := cloneStaleRoots(layout)
	return staleCleanupContext{
		StaleRoots: staleRoots,
		Roots:      cleanupRootsForLayout(layout),
		Protected:  protectedStalePathsForHarness(eng, reg, spec, hid, staleRoots, currentCandidates),
	}
}

func protectedStalePathsForHarness(eng *engine.Engine, reg *harness.Registry, spec TargetSpec, hid domain.Harness, staleRoots []string, currentCandidates []string) map[string]struct{} {
	if len(staleRoots) == 0 {
		return nil
	}
	current := currentStaleCandidateSet(staleRoots, currentCandidates)
	if len(current) == 0 {
		return nil
	}
	configDir := config.FallbackConfigDir(spec.ConfigDir, spec.Home)
	active := activeHarnessSet(spec, registryHarnessIDs(reg))
	protected := map[string]struct{}{}
	for _, h := range reg.All() {
		otherID := h.ID()
		if otherID == hid {
			continue
		}
		if _, ok := active[otherID]; !ok {
			continue
		}
		layout := h.Layout(spec.Scope, targetDirForHarness(spec, otherID), spec.Home)
		if !rootsOverlapAny(staleRoots, layout.ValidationRoots) {
			continue
		}
		ledgerPath := engine.LedgerPath(configDir, spec.Scope, spec.ProjectDir, otherID)
		lg, _, err := eng.LoadLedger(ledgerPath)
		if err != nil {
			continue
		}
		for path := range lg.Managed {
			clean := filepath.Clean(path)
			if domain.IsMCPLedgerKey(clean) {
				continue
			}
			if _, candidate := current[clean]; candidate && domain.IsUnderAny(clean, layout.ValidationRoots) {
				protected[clean] = struct{}{}
			}
		}
	}
	if len(protected) == 0 {
		return nil
	}
	return protected
}

func currentStaleCandidateSet(staleRoots []string, currentCandidates []string) map[string]struct{} {
	if len(currentCandidates) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, path := range currentCandidates {
		clean := filepath.Clean(path)
		if domain.IsMCPLedgerKey(clean) || !domain.IsUnderAny(clean, staleRoots) {
			continue
		}
		out[clean] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type inactiveStaleCandidate struct {
	Harness domain.Harness
	Path    string
}

type inactiveStaleContext struct {
	eng        *engine.Engine
	reg        *harness.Registry
	spec       TargetSpec
	hid        domain.Harness
	staleRoots []string
	active     map[domain.Harness]struct{}
	configDir  string
}

func newInactiveStaleContext(eng *engine.Engine, reg *harness.Registry, spec TargetSpec, hid domain.Harness, staleRoots []string) inactiveStaleContext {
	return inactiveStaleContext{
		eng:        eng,
		reg:        reg,
		spec:       spec,
		hid:        hid,
		staleRoots: staleRoots,
		active:     activeHarnessSet(spec, registryHarnessIDs(reg)),
		configDir:  config.FallbackConfigDir(spec.ConfigDir, spec.Home),
	}
}

func (ctx inactiveStaleContext) candidates() []inactiveStaleCandidate {
	if len(ctx.staleRoots) == 0 {
		return nil
	}
	var out []inactiveStaleCandidate
	for _, owner := range ctx.owners() {
		lg, _, err := ctx.eng.LoadLedger(owner.ledgerPath)
		if err != nil {
			continue
		}
		plan := domain.Plan{Ledger: owner.ledgerPath}
		paths, err := ctx.eng.StaleCandidatesWithLedger(plan, ctx.staleRoots, lg)
		if err != nil {
			continue
		}
		for _, path := range paths {
			out = append(out, inactiveStaleCandidate{Harness: owner.harness, Path: path})
		}
	}
	slices.SortFunc(out, func(a, b inactiveStaleCandidate) int {
		if a.Harness != b.Harness {
			return cmp.Compare(string(a.Harness), string(b.Harness))
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

func (ctx inactiveStaleContext) prune(ctxGo context.Context, req engine.PruneLedgerManagedRequest) []domain.Warning {
	if len(ctx.staleRoots) == 0 {
		return nil
	}
	var warnings []domain.Warning
	for _, owner := range ctx.owners() {
		ownerReq := req
		ownerReq.LabelForPath = func(path string) string {
			return syncDisplayPath(owner.harness, path)
		}
		pruneWarnings, err := ctx.eng.PruneLedgerManagedPaths(ctxGo, owner.ledgerPath, ctx.staleRoots, ownerReq)
		warnings = append(warnings, pruneWarnings...)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Field:   "stale",
				Message: fmt.Sprintf("inactive %s stale cleanup failed: %v", owner.harness, err),
			})
		}
	}
	return warnings
}

type inactiveStaleLedgerOwner struct {
	harness    domain.Harness
	ledgerPath string
}

func (ctx inactiveStaleContext) owners() []inactiveStaleLedgerOwner {
	var owners []inactiveStaleLedgerOwner
	for _, h := range ctx.reg.All() {
		otherID := h.ID()
		if otherID == ctx.hid {
			continue
		}
		if _, ok := ctx.active[otherID]; ok {
			continue
		}
		layout := h.Layout(ctx.spec.Scope, targetDirForHarness(ctx.spec, otherID), ctx.spec.Home)
		if !rootsOverlapAny(ctx.staleRoots, layout.ValidationRoots) {
			continue
		}
		owners = append(owners, inactiveStaleLedgerOwner{
			harness:    otherID,
			ledgerPath: engine.LedgerPath(ctx.configDir, ctx.spec.Scope, ctx.spec.ProjectDir, otherID),
		})
	}
	return owners
}

func activeHarnessSet(spec TargetSpec, fallback []domain.Harness) map[domain.Harness]struct{} {
	active := spec.ActiveHarnesses
	if len(active) == 0 {
		active = fallback
	}
	if len(active) == 0 {
		active = spec.Harnesses
	}
	out := make(map[domain.Harness]struct{}, len(active))
	for _, h := range active {
		out[h] = struct{}{}
	}
	return out
}

func registryHarnessIDs(reg *harness.Registry) []domain.Harness {
	if reg == nil {
		return nil
	}
	hs := reg.All()
	out := make([]domain.Harness, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.ID())
	}
	return out
}

func rootsOverlapAny(a, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if rootsOverlap(left, right) {
				return true
			}
		}
	}
	return false
}

func rootsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return util.IsWithinDir(a, b) || util.IsWithinDir(b, a)
}
