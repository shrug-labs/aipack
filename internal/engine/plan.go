package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
)

// PlanRequest describes what to sync.
type PlanRequest struct {
	ConfigDir       string
	Scope           domain.Scope
	Harnesses       []domain.Harness
	ProjectDir      string
	Home            string // $HOME — threaded explicitly for testability
	TargetDir       string // optional resolved target dir; defaults to project dir or $HOME
	TargetConfigDir bool
	NativeConfigDir string // optional harness-native user config home (also needed by project plugin installs)
	SkipSettings    bool
	Namespaced      bool
}

// Planner is the interface harness adapters implement for plan contribution.
// Each harness converts typed content into a Fragment of writes/copies/settings.
type Planner interface {
	ID() domain.Harness
	Plan(ctx context.Context, sctx SyncContext) (domain.Fragment, error)
}

// SyncContext provides typed content and config to harness planners.
// Profile carries all resolved content (rules, agents, workflows, skills,
// MCP servers, settings, plugins) — replacing the former 4 separate fields.
type SyncContext struct {
	ConfigDir       string
	Scope           domain.Scope
	TargetDir       string // project dir or $HOME
	TargetConfigDir bool   // global TargetDir is the harness-native config directory
	NativeConfigDir string
	Home            string         // $HOME — always set, even in project scope (needed by Cline)
	Profile         domain.Profile // fully-resolved profile with typed content
	SkipSettings    bool
	Namespaced      bool
}

// PlanSync produces a sync Plan by asking each harness planner to contribute a Fragment.
// The Profile must already be fully resolved (via engine.Resolve).
func PlanSync(ctx context.Context, profile domain.Profile, req PlanRequest, harnesses []Planner) (domain.Plan, error) {
	if req.Scope == domain.ScopeGlobal && req.Home == "" {
		return domain.Plan{}, fmt.Errorf("HOME is not set (required for global scope)")
	}

	plan := domain.Plan{Desired: map[string]struct{}{}}
	plan.Ledger = ledgerPath(req)

	targetDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		targetDir = req.Home
	}
	if req.TargetDir != "" {
		targetDir = req.TargetDir
	}
	targets := make([]domain.Harness, 0, len(harnesses))
	for _, h := range harnesses {
		targets = append(targets, h.ID())
	}
	if err := ValidatePluginTargets(profile, targets, req.Namespaced); err != nil {
		return domain.Plan{}, err
	}

	// Each harness contributes a Fragment.
	for _, h := range harnesses {
		targetProfile, targetWarnings, err := ProfileForHarness(profile, h.ID(), req)
		if err != nil {
			return domain.Plan{}, fmt.Errorf("harness %s: %w", h.ID(), err)
		}
		sctx := SyncContext{
			ConfigDir:       req.ConfigDir,
			Scope:           req.Scope,
			TargetDir:       targetDir,
			TargetConfigDir: req.TargetConfigDir,
			NativeConfigDir: req.NativeConfigDir,
			Home:            req.Home,
			Profile:         targetProfile,
			SkipSettings:    req.SkipSettings,
			Namespaced:      req.Namespaced,
		}
		frag, err := h.Plan(ctx, sctx)
		if err != nil {
			return domain.Plan{}, fmt.Errorf("harness %s: %w", h.ID(), err)
		}
		frag.Warnings = append(frag.Warnings, targetWarnings...)
		for _, pack := range targetProfile.Packs {
			if pack.NativePlugin != nil && !slices.ContainsFunc(frag.NativePlugins, func(action domain.NativePluginAction) bool {
				return action.SourcePack == pack.Name && action.Package.Binding() == pack.NativePlugin.Package.Binding()
			}) && !slices.ContainsFunc(frag.Writes, func(action domain.WriteAction) bool {
				return h.ID() == domain.HarnessOpenCode && action.PackageFiles != nil && action.Delivery != nil && action.SourcePack == pack.Name && action.Delivery.Binding == pack.NativePlugin.Package.Binding()
			}) {
				return domain.Plan{}, fmt.Errorf("harness %s does not implement native delivery for plugin pack %q", h.ID(), pack.Name)
			}
		}
		frag.Apply(&plan)
	}

	return plan, nil
}

// ValidatePluginTargets reports every unsupported selected component before
// any target's sync starts. It does not infer or enable content dependencies.
func ValidatePluginTargets(profile domain.Profile, targets []domain.Harness, namespaced bool) error {
	var unsupported []string
	for _, target := range targets {
		if _, _, err := ProfileForHarness(profile, target, PlanRequest{ConfigDir: "/aipack-config", Namespaced: namespaced}); err != nil {
			unsupported = append(unsupported, err.Error())
		}
		for _, pack := range profile.Packs {
			selection := pack.NativePlugin
			if selection == nil || selection.Package.Harness == target {
				continue
			}
			var components []string
			if plugin.GenericContentSelection(*selection, target) {
				components = plugin.Compatibility(*selection, target).Unsupported
				if len(components) == 0 {
					if _, err := plugin.ReadFiles(filepath.Join(selection.Root, "upstream")); err != nil {
						unsupported = append(unsupported, fmt.Sprintf("plugin pack %q: delivery to %s: %v", pack.Name, target, err))
					}
					continue
				}
				unsupported = append(unsupported, fmt.Sprintf("plugin pack %q: delivery to %s has unsupported selected components: %s", pack.Name, target, strings.Join(components, ", ")))
				continue
			}
			if selection.Package.Harness == domain.HarnessCodex && (target == domain.HarnessClaudeCode || target == domain.HarnessOpenCode || target == domain.HarnessCline) &&
				(selection.Package.Format == plugin.CodexLegacy || selection.Package.Format == plugin.AgentPlugins) && selection.Package.ConverterVersion == plugin.ConverterVersion {
				components = plugin.Compatibility(*selection, target).Unsupported
				if len(components) == 0 {
					// Validate the complete portable payload before another target can apply.
					var err error
					if target == domain.HarnessOpenCode {
						_, _, err = plugin.RenderCodexForOpenCode(*selection, "/aipack-payload", "/aipack-data")
					} else {
						_, _, err = plugin.RenderCodexForClaude(*selection)
					}
					if err != nil {
						unsupported = append(unsupported, fmt.Sprintf("native plugin pack %q: delivery to %s: %v", pack.Name, target, err))
					}
					continue
				}
				unsupported = append(unsupported, fmt.Sprintf("native plugin pack %q: delivery to %s has unsupported selected components: %s", pack.Name, target, strings.Join(components, ", ")))
				continue
			}
			for category, ids := range selection.Selected {
				for _, id := range ids {
					components = append(components, string(category)+"/"+id)
				}
			}
			if selection.SettingsEnabled {
				for _, path := range selection.Package.SettingsFiles {
					components = append(components, "settings/"+path)
				}
			}
			slices.Sort(components)
			message := fmt.Sprintf("native plugin pack %q targets %s; delivery to %s is not supported", pack.Name, selection.Package.Harness, target)
			if len(components) > 0 {
				message += "; unsupported selected components: " + strings.Join(components, ", ")
			}
			unsupported = append(unsupported, message)
		}
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("%s", strings.Join(unsupported, "\n"))
	}
	return nil
}

// ledgerPath computes the ledger file path for a plan request.
// Expects a single-harness PlanRequest (per-harness plan+apply).
// Resolves ConfigDir from Home when not explicitly set.
func ledgerPath(req PlanRequest) string {
	if len(req.Harnesses) == 0 {
		return ""
	}
	home := req.Home
	if home == "" {
		home = req.ProjectDir
	}
	return LedgerPath(config.FallbackConfigDir(req.ConfigDir, home), req.Scope, req.ProjectDir, req.Harnesses[0])
}
