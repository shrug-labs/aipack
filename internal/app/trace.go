package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// TraceRequest holds the parameters for tracing a resource through the sync pipeline.
type TraceRequest struct {
	TargetSpec
	ResourceType string // rule, agent, workflow, skill, plugin, mcp
	ResourceName string // name of the resource to trace
}

// TraceSource describes where a resource comes from in the pack.
type TraceSource struct {
	Pack       string `json:"pack"`
	SourcePath string `json:"source_path"`
	Category   string `json:"category"` // rules, agents, workflows, skills, plugins, mcp
}

// TraceCandidate describes an exact active-profile resource match that can be traced.
type TraceCandidate struct {
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Pack         string `json:"pack"`
	SourcePath   string `json:"source_path,omitempty"`
	Category     string `json:"category"`
}

// TraceDestination describes where a resource lands in a harness location.
type TraceDestination struct {
	Harness  string          `json:"harness"`
	Path     string          `json:"path"`
	Embedded bool            `json:"embedded,omitempty"` // true when resource is composited into a multi-resource file
	State    string          `json:"state"`              // create, identical, managed, untracked, error
	DiffKind domain.DiffKind `json:"diff_kind"`
}

// TraceResult holds the full trace of a resource from source to all destinations.
type TraceResult struct {
	ResourceType string             `json:"resource_type"`
	ResourceName string             `json:"resource_name"`
	Found        bool               `json:"found"`
	Source       *TraceSource       `json:"source,omitempty"`
	Destinations []TraceDestination `json:"destinations"`
}

// RunTrace traces a resource through the sync pipeline, showing where it comes
// from and where it would land in each harness location.
func RunTrace(ctx context.Context, eng *engine.Engine, profile domain.Profile, req TraceRequest, reg *harness.Registry) (TraceResult, error) {
	result := TraceResult{
		ResourceType: req.ResourceType,
		ResourceName: req.ResourceName,
	}
	knownPacks := knownPacksFromRoots(resolvePackRoots(profile))

	// Find the resource in the profile.
	source := findResource(profile, req.ResourceType, req.ResourceName)
	if source == nil {
		return result, nil
	}
	result.Found = true
	result.Source = source

	// Build per-harness plans and aggregate destinations.
	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			continue
		}
		planReq := engine.PlanRequest{
			ConfigDir:  req.ConfigDir,
			Scope:      req.Scope,
			Harnesses:  []domain.Harness{hid},
			ProjectDir: req.ProjectDir,
			Home:       req.Home,
			Namespaced: req.Namespaced,
		}
		plan, err := engine.PlanSync(ctx, profile, planReq, planners)
		if err != nil {
			continue
		}
		var lg domain.Ledger
		if plan.Ledger != "" {
			if l, _, lerr := eng.LoadLedger(plan.Ledger); lerr == nil {
				lg = l
			}
		}
		h, err := reg.Lookup(hid)
		if err != nil {
			continue
		}
		captured, err := h.Capture(ctx, harness.CaptureContext{
			Scope:      req.Scope,
			ProjectDir: req.ProjectDir,
			Home:       req.Home,
			KnownPacks: knownPacks,
		})
		if err != nil {
			continue
		}
		currentMCP, err := capturedMCPDigests(captured)
		if err != nil {
			continue
		}
		dests := matchDestinations(eng, plan, source, lg, currentMCP, req.ResourceType, req.ResourceName, hid)
		result.Destinations = append(result.Destinations, dests...)
	}

	return result, nil
}

// findResource locates a resource in the profile by type and name.
func findResource(profile domain.Profile, resType, name string) *TraceSource {
	cat, ok := domain.ParseSingularLabel(resType)
	if !ok {
		return nil
	}
	var found *TraceSource
	walkTraceResources(profile, func(resourceCat domain.PackCategory, resourceName string, source TraceSource) bool {
		if resourceCat == cat && resourceName == name {
			found = &source
			return false
		}
		return true
	})
	return found
}

// FindTraceCandidates finds exact active-profile resources by name across all
// traceable categories.
func FindTraceCandidates(profile domain.Profile, name string) []TraceCandidate {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var out []TraceCandidate
	walkTraceResources(profile, func(cat domain.PackCategory, resourceName string, source TraceSource) bool {
		if resourceName != name {
			return true
		}
		out = append(out, TraceCandidate{
			ResourceType: traceResourceType(cat),
			ResourceName: resourceName,
			Pack:         source.Pack,
			SourcePath:   source.SourcePath,
			Category:     source.Category,
		})
		return true
	})
	return out
}

func walkTraceResources(profile domain.Profile, visit func(domain.PackCategory, string, TraceSource) bool) {
	for _, r := range profile.AllRules() {
		if !visit(domain.CategoryRules, r.Name, TraceSource{Pack: r.SourcePack, SourcePath: r.SourcePath, Category: string(domain.CategoryRules)}) {
			return
		}
	}
	for _, a := range profile.AllAgents() {
		if !visit(domain.CategoryAgents, a.Name, TraceSource{Pack: a.SourcePack, SourcePath: a.SourcePath, Category: string(domain.CategoryAgents)}) {
			return
		}
	}
	for _, w := range profile.AllWorkflows() {
		if !visit(domain.CategoryWorkflows, w.Name, TraceSource{Pack: w.SourcePack, SourcePath: w.SourcePath, Category: string(domain.CategoryWorkflows)}) {
			return
		}
	}
	for _, s := range profile.AllSkills() {
		if !visit(domain.CategorySkills, s.Name, TraceSource{Pack: s.SourcePack, SourcePath: s.DirPath, Category: string(domain.CategorySkills)}) {
			return
		}
	}
	for _, h := range profile.AllHooks() {
		if !visit(domain.CategoryHooks, h.ID, TraceSource{Pack: h.SourcePack, SourcePath: h.SourcePath, Category: string(domain.CategoryHooks)}) {
			return
		}
	}
	for _, p := range profile.AllPlugins() {
		if !visit(domain.CategoryPlugins, p.Name, TraceSource{Pack: p.SourcePack, SourcePath: p.SourcePath, Category: string(domain.CategoryPlugins)}) {
			return
		}
	}
	for _, m := range profile.MCPServers {
		if !visit(domain.CategoryMCP, m.Name, TraceSource{Pack: m.SourcePack, Category: string(domain.CategoryMCP)}) {
			return
		}
	}
}

func traceResourceType(cat domain.PackCategory) string {
	if cat == domain.CategoryMCP {
		return "mcp"
	}
	return strings.ToLower(cat.SingularLabel())
}

// matchDestinations finds plan actions that correspond to the traced resource
// and classifies each destination's on-disk state.
func matchDestinations(eng *engine.Engine, plan domain.Plan, source *TraceSource, lg domain.Ledger, currentMCP map[string]string, resType, resName string, hid domain.Harness) []TraceDestination {
	var dests []TraceDestination

	cat, _ := domain.ParseSingularLabel(resType)
	switch cat {
	case domain.CategorySkills:
		for _, cp := range plan.Copies {
			if matchesCopy(cp, source) {
				dest := TraceDestination{
					Harness: string(hid),
					Path:    cp.Dst,
				}
				dest.State, dest.DiffKind = classifyCopyState(cp, lg)
				dests = append(dests, dest)
			}
		}
	case domain.CategoryMCP:
		for _, action := range plan.MCPServers {
			if action.Name == resName {
				dest := TraceDestination{
					Harness:  string(action.Harness),
					Path:     action.ConfigPath,
					Embedded: action.Embedded,
				}
				dest.State, dest.DiffKind = classifyMCPState(action, currentMCP, lg)
				dests = append(dests, dest)
			}
		}
	case domain.CategoryPlugins:
		for _, action := range append(plan.Settings, plan.MCP...) {
			if strings.Contains(string(action.Desired), resName+"@") {
				dest := TraceDestination{
					Harness:  string(action.Harness),
					Path:     action.Dst,
					Embedded: true,
				}
				dest.State, dest.DiffKind = classifySettingsState(eng, action, lg)
				dests = append(dests, dest)
			}
		}
	case domain.CategoryHooks:
		for _, wr := range plan.Writes {
			if filepath.Base(wr.Dst) != "hooks.json" {
				continue
			}
			if wr.SourcePack != source.Pack && wr.SourcePack != "(composite)" {
				continue
			}
			dest := TraceDestination{
				Harness:  string(hid),
				Path:     wr.Dst,
				Embedded: true,
			}
			dest.State, dest.DiffKind = classifyWriteState(eng, wr, lg)
			dests = append(dests, dest)
		}
	default:
		// Rules, agents, workflows use WriteActions.
		for _, wr := range plan.Writes {
			matched, embedded := matchesWrite(wr, source, resName)
			if matched {
				dest := TraceDestination{
					Harness:  string(hid),
					Path:     wr.Dst,
					Embedded: embedded,
				}
				dest.State, dest.DiffKind = classifyWriteState(eng, wr, lg)
				dests = append(dests, dest)
			}
		}
	}

	return dests
}

func classifySettingsState(eng *engine.Engine, action domain.SettingsAction, lg domain.Ledger) (string, domain.DiffKind) {
	fd, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{action}, lg)
	if err != nil || len(fd) == 0 {
		return string(domain.DiffError), domain.DiffError
	}
	return string(fd[0].Kind), fd[0].Kind
}

// matchesWrite checks if a WriteAction corresponds to the traced resource.
// Returns (matched, embedded) where embedded is true when the resource is
// composited into a multi-resource file (e.g. Codex AGENTS.override.md).
func matchesWrite(wr domain.WriteAction, source *TraceSource, resName string) (bool, bool) {
	// Direct match by source path (most reliable — individual file per resource).
	if source.SourcePath != "" && wr.Src == source.SourcePath {
		return true, false
	}
	// Match by destination filename (for harnesses that use individual files).
	base := filepath.Base(wr.Dst)
	name := strings.TrimSuffix(base, ".md")
	srcName := strings.TrimSuffix(filepath.Base(source.SourcePath), ".md")
	if name == srcName && wr.SourcePack == source.Pack {
		return true, false
	}
	// Composite match: some harnesses (Codex) flatten all rules into a single
	// file (AGENTS.override.md). Check if the write content contains the
	// resource source marker (<!-- source: name.md -->).
	if isCompositeFile(wr.Dst) && bytes.Contains(wr.Content, []byte("<!-- source: "+resName+".md -->")) {
		return true, true
	}
	return false, false
}

// isCompositeFile returns true for files known to aggregate multiple resources.
func isCompositeFile(path string) bool {
	base := filepath.Base(path)
	return base == "AGENTS.override.md" || base == "AGENTS.md"
}

// matchesCopy checks if a CopyAction corresponds to the traced skill.
func matchesCopy(cp domain.CopyAction, source *TraceSource) bool {
	if source.SourcePath != "" && cp.Src == source.SourcePath {
		return true
	}
	return cp.SourcePack == source.Pack &&
		filepath.Base(cp.Dst) == filepath.Base(source.SourcePath)
}

// classifyWriteState determines the on-disk state of a write destination.
func classifyWriteState(eng *engine.Engine, wr domain.WriteAction, lg domain.Ledger) (string, domain.DiffKind) {
	kind, err := classifyWriteKind(eng, wr, lg)
	if err != nil {
		return string(domain.DiffError), domain.DiffError
	}
	return string(kind), kind
}

// classifyCopyState determines the on-disk state of a copy destination.
func classifyCopyState(cp domain.CopyAction, lg domain.Ledger) (string, domain.DiffKind) {
	if _, err := os.Stat(cp.Dst); os.IsNotExist(err) {
		return "create", domain.DiffCreate
	}
	// Check if any child files are in the ledger.
	prefix := cp.Dst + string(filepath.Separator)
	tracked := false
	for k := range lg.Managed {
		if strings.HasPrefix(k, prefix) {
			tracked = true
			break
		}
	}
	if tracked {
		if dirChildrenClean(cp.Dst, lg) {
			return "identical", domain.DiffIdentical
		}
		return "managed", domain.DiffManaged
	}
	return "untracked", domain.DiffUntracked
}

func classifyMCPState(action domain.MCPAction, current map[string]string, lg domain.Ledger) (string, domain.DiffKind) {
	kind, err := classifyMCPAction(action, current, lg)
	if err != nil {
		return string(domain.DiffError), domain.DiffError
	}
	return string(kind), kind
}
