package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// TraceRequest holds the parameters for tracing a resource through the sync pipeline.
type TraceRequest struct {
	TargetSpec
	ProfileName   string
	ProfileConfig config.ProfileConfig
	ResourceType  string // rule, agent, workflow, skill, plugin, mcp
	ResourceName  string // name of the resource to trace
	Diagnostic    *TraceDiagnostic
}

// TraceSource describes where a resource comes from in the pack.
type TraceSource struct {
	Pack       string `json:"pack"`
	SourcePath string `json:"source_path"`
	Category   string `json:"category"` // rules, agents, workflows, skills, plugins, mcp
}

// TraceCandidate describes an exact active-profile resource match that can be traced.
type TraceCandidate struct {
	ResourceType string            `json:"resource_type"`
	ResourceName string            `json:"resource_name"`
	Pack         string            `json:"pack"`
	SourcePath   string            `json:"source_path,omitempty"`
	Category     string            `json:"category"`
	ProfileState TraceProfileState `json:"profile_state,omitempty"`
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
	ProfileState TraceProfileState  `json:"profile_state"`
	Blockers     []string           `json:"blockers,omitempty"`
	Remediation  []string           `json:"remediation,omitempty"`
	Source       *TraceSource       `json:"source,omitempty"`
	Destinations []TraceDestination `json:"destinations"`
}

// TraceProfileState describes whether a traced resource is active in a profile.
type TraceProfileState string

const (
	TraceProfileStateActive                TraceProfileState = "active"
	TraceProfileStatePackDisabled          TraceProfileState = "pack_disabled"
	TraceProfileStateContentExcluded       TraceProfileState = "content_excluded"
	TraceProfileStateInstalledNotInProfile TraceProfileState = "installed_not_in_profile"
	TraceProfileStateNotInstalled          TraceProfileState = "not_installed"
)

// TraceDiagnostic describes an inactive resource and how to activate it.
type TraceDiagnostic struct {
	Candidate   TraceCandidate
	Blockers    []string
	Remediation []string
}

func (d TraceDiagnostic) source() TraceSource {
	return TraceSource{
		Pack:       d.Candidate.Pack,
		SourcePath: d.Candidate.SourcePath,
		Category:   d.Candidate.Category,
	}
}

// RunTrace traces a resource through the sync pipeline, showing where it comes
// from and where it would land in each harness location.
func RunTrace(ctx context.Context, eng *engine.Engine, profile domain.Profile, req TraceRequest, reg *harness.Registry) (TraceResult, error) {
	result := TraceResult{
		ResourceType: req.ResourceType,
		ResourceName: req.ResourceName,
		ProfileState: TraceProfileStateNotInstalled,
	}
	knownPacks := knownPacksFromRoots(resolvePackRoots(profile))

	// Find the resource in the profile.
	source := findResource(profile, req.ResourceType, req.ResourceName)
	if source == nil {
		if req.Diagnostic != nil {
			applyTraceDiagnosticMatch(&result, *req.Diagnostic)
			return result, nil
		}
		applyTraceDiagnostic(&result, req)
		return result, nil
	}
	result.Found = true
	result.ProfileState = TraceProfileStateActive
	result.Source = source

	// Build per-harness plans and aggregate destinations.
	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			continue
		}
		planReq := planRequestForTarget(req.TargetSpec, req.ConfigDir, false, hid)
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
		captured, err := h.Capture(ctx, captureContextForHarness(req.TargetSpec, hid, knownPacks))
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

func applyTraceDiagnostic(result *TraceResult, req TraceRequest) {
	matches := findTraceDiagnostics(req.ProfileConfig, req.ConfigDir, req.ResourceType, req.ResourceName, req.ProfileName)
	if len(matches) == 0 {
		result.Blockers = []string{traceTypeLabel(req.ResourceType) + " " + req.ResourceName + " is not installed or active in the profile"}
		result.Remediation = []string{traceSearchCommand(req.ResourceName)}
		return
	}
	applyTraceDiagnosticMatch(result, matches[0])
}

func applyTraceDiagnosticMatch(result *TraceResult, match TraceDiagnostic) {
	result.Found = true
	result.ResourceType = match.Candidate.ResourceType
	result.ResourceName = match.Candidate.ResourceName
	result.ProfileState = match.Candidate.ProfileState
	source := match.source()
	result.Source = &source
	result.Blockers = match.Blockers
	result.Remediation = match.Remediation
}

// FindTraceDiagnosticCandidates finds exact inactive resources by name across
// profile entries and installed packs. It is used by smart-name trace fallback
// after active-profile candidates have missed.
func FindTraceDiagnosticCandidates(profileCfg config.ProfileConfig, configDir, name, profileName string) []TraceDiagnostic {
	matches := findTraceDiagnostics(profileCfg, configDir, "", name, profileName)
	out := make([]TraceDiagnostic, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		candidate := match.Candidate
		key := candidate.ResourceType + "\x00" + candidate.ResourceName + "\x00" + candidate.Pack + "\x00" + string(candidate.ProfileState)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, match)
	}
	return out
}

func findTraceDiagnostics(profileCfg config.ProfileConfig, configDir, resType, name, profileName string) []TraceDiagnostic {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	catFilter, ok := traceCategoryFilter(resType)
	if !ok {
		return nil
	}

	var matches []TraceDiagnostic
	profilePackNames := map[string]struct{}{}
	for _, pe := range profileCfg.Packs {
		profilePackNames[pe.Name] = struct{}{}
	}

	packs, _ := ResolveProfilePacks(configDir, profileCfg.Packs)
	tree := BuildContentTree(packs, profileCfg.Packs)
	for _, item := range tree.Items {
		if catFilter != "" && item.Category != catFilter {
			continue
		}
		if item.ID != name || item.Enabled {
			continue
		}
		pack := tree.Packs[item.PackIdx]
		pe := profileCfg.Packs[pack.Index]
		matches = append(matches, traceDiagnosticFromProfileItem(pack, pe, item, profileName))
	}
	if len(matches) > 0 {
		return orderTraceDiagnostics(matches)
	}

	installed, err := PackListDetailed(configDir)
	if err != nil {
		return nil
	}
	for _, pack := range installed {
		if _, inProfile := profilePackNames[pack.Name]; inProfile {
			continue
		}
		for _, cat := range TraceableCategories() {
			if catFilter != "" && cat != catFilter {
				continue
			}
			if !slices.Contains(pack.ContentIDs(cat), name) {
				continue
			}
			candidate := TraceCandidate{
				ResourceType: traceResourceType(cat),
				ResourceName: name,
				Pack:         pack.Name,
				SourcePath:   traceInstalledSourcePath(pack, cat, name),
				Category:     string(cat),
				ProfileState: TraceProfileStateInstalledNotInProfile,
			}
			matches = append(matches, TraceDiagnostic{
				Candidate: candidate,
				Blockers: []string{
					"pack " + pack.Name + " is installed but not listed in profile " + profileName,
				},
				Remediation: installedPackRemediation(pack.installQuiet, pack.Name, name, traceResourceType(cat), profileName),
			})
		}
	}
	return orderTraceDiagnostics(matches)
}

func traceCategoryFilter(resType string) (domain.PackCategory, bool) {
	if resType == "" {
		return "", true
	}
	cat, ok := domain.ParseSingularLabel(resType)
	if !ok || !IsTraceableCategory(cat) {
		return "", false
	}
	return cat, true
}

func traceDiagnosticFromProfileItem(pack ProfilePackInfo, pe config.PackEntry, item ContentItem, profileName string) TraceDiagnostic {
	cat := item.Category
	state := TraceProfileStateContentExcluded
	var blockers []string
	var remediation []string
	if !config.PackEnabled(pe.Enabled) {
		state = TraceProfileStatePackDisabled
		blockers = append(blockers, "pack "+pack.Name+" is disabled in profile "+profileName)
		remediation = append(remediation, tracePackProfileCommand("enable", pack.Name, profileName))
	}
	if blocker := contentExcludedBlocker(pack.Name, pe, cat, item.ID, profileName); blocker != "" {
		blockers = append(blockers, blocker)
	}
	if len(blockers) == 0 {
		blockers = append(blockers, traceResourceType(cat)+" "+item.ID+" from pack "+pack.Name+" is excluded in profile "+profileName)
	}
	if state == TraceProfileStateContentExcluded || len(blockers) > 1 {
		remediation = append(remediation, traceProfileIncludeCommand(item.ID, traceResourceType(cat), pack.Name, profileName))
	}
	remediation = append(remediation, traceSyncCommand(profileName))

	return TraceDiagnostic{
		Candidate: TraceCandidate{
			ResourceType: traceResourceType(cat),
			ResourceName: item.ID,
			Pack:         pack.Name,
			SourcePath:   traceProfileSourcePath(pack, cat, item.ID),
			Category:     string(cat),
			ProfileState: state,
		},
		Blockers:    blockers,
		Remediation: remediation,
	}
}

func contentExcludedBlocker(packName string, pe config.PackEntry, cat domain.PackCategory, id, profileName string) string {
	if cat == domain.CategoryMCP {
		if cfg, ok := pe.MCP[id]; ok && !config.PackEnabled(cfg.Enabled) {
			return "mcp " + id + " from pack " + packName + " is disabled in profile " + profileName
		}
		if cfg, ok := config.ResolveProfileMCPSelection([]string{id}, pe.MCP, pe.Quiet)[id]; ok && config.PackEnabled(cfg.Enabled) {
			return ""
		}
		if pe.Quiet {
			return "quiet pack " + packName + " does not include mcp " + id + " in profile " + profileName
		}
		return "mcp " + id + " from pack " + packName + " is excluded in profile " + profileName
	}
	if cat == domain.CategoryHooks && pe.Hooks.Enabled != nil && !*pe.Hooks.Enabled {
		return "hooks are disabled for pack " + packName + " in profile " + profileName
	}
	if sel := pe.VectorSelectorFor(cat); sel != nil {
		if selectorListContains(sel.Exclude, id) {
			return traceResourceType(cat) + " " + id + " from pack " + packName + " is excluded in profile " + profileName
		}
		if sel.Include != nil && !selectorListContains(sel.Include, id) {
			return traceResourceType(cat) + " " + id + " from pack " + packName + " is not included by the profile include list"
		}
		if pe.Quiet && (sel.Include == nil || !selectorListContains(sel.Include, id)) {
			return "quiet pack " + packName + " does not include " + traceResourceType(cat) + " " + id + " in profile " + profileName
		}
	}
	return ""
}

func traceProfileSourcePath(pack ProfilePackInfo, cat domain.PackCategory, id string) string {
	if cat == domain.CategoryMCP {
		return ""
	}
	return pack.ContentPath(cat, id)
}

func traceInstalledSourcePath(pack PackShowEntry, cat domain.PackCategory, id string) string {
	if cat == domain.CategoryMCP {
		return ""
	}
	return pack.ContentPath(cat, id)
}

func orderTraceDiagnostics(matches []TraceDiagnostic) []TraceDiagnostic {
	slices.SortStableFunc(matches, func(a, b TraceDiagnostic) int {
		ap := traceDiagnosticPriority(a.Candidate.ProfileState)
		bp := traceDiagnosticPriority(b.Candidate.ProfileState)
		if ap != bp {
			return ap - bp
		}
		if a.Candidate.Pack != b.Candidate.Pack {
			return strings.Compare(a.Candidate.Pack, b.Candidate.Pack)
		}
		if a.Candidate.ResourceType != b.Candidate.ResourceType {
			return strings.Compare(a.Candidate.ResourceType, b.Candidate.ResourceType)
		}
		return strings.Compare(a.Candidate.ResourceName, b.Candidate.ResourceName)
	})
	return matches
}

func traceDiagnosticPriority(state TraceProfileState) int {
	switch state {
	case TraceProfileStatePackDisabled:
		return 0
	case TraceProfileStateContentExcluded:
		return 1
	case TraceProfileStateInstalledNotInProfile:
		return 2
	default:
		return 3
	}
}

func traceTypeLabel(resType string) string {
	if resType == "" {
		return "resource"
	}
	return resType
}

func installedPackRemediation(installQuiet bool, packName, id, kind, profileName string) []string {
	remediation := []string{tracePackProfileCommand("add", packName, profileName)}
	if installQuiet {
		remediation = append(remediation, traceProfileIncludeCommand(id, kind, packName, profileName))
	}
	remediation = append(remediation, traceSyncCommand(profileName))
	return remediation
}

func traceSearchCommand(name string) string {
	return util.ShellCommandWithOperand([]string{"aipack", "search"}, name)
}

func tracePackProfileCommand(action, packName, profileName string) string {
	return util.ShellCommandWithOperand([]string{"aipack", "pack", action}, packName, "--profile", profileName)
}

func traceProfileIncludeCommand(id, kind, packName, profileName string) string {
	return util.ShellCommandWithOperand([]string{"aipack", "profile", "include"}, id, "--kind", kind, "--pack", packName, "--profile", profileName)
}

func traceSyncCommand(profileName string) string {
	return util.ShellCommand("aipack", "sync", "--profile", profileName)
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

// TraceableCategories returns the resource categories supported by trace.
func TraceableCategories() []domain.PackCategory {
	return profileContentSearchCategories()
}

// IsTraceableCategory reports whether trace supports the category.
func IsTraceableCategory(cat domain.PackCategory) bool {
	return slices.Contains(TraceableCategories(), cat)
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
		for _, action := range append(plan.Settings, plan.MCP...) {
			if !matchesTraceRefs(action.TraceRefs, source, resName) {
				continue
			}
			dest := TraceDestination{
				Harness:  string(action.Harness),
				Path:     action.Dst,
				Embedded: true,
			}
			dest.State, dest.DiffKind = classifySettingsState(eng, action, lg)
			dests = append(dests, dest)
		}
		for _, wr := range plan.Writes {
			if len(wr.TraceRefs) > 0 {
				if !matchesTraceRefs(wr.TraceRefs, source, resName) {
					continue
				}
			} else {
				if wr.Category != domain.CategoryHooks && !isHookDestinationFallback(wr.Dst) {
					continue
				}
				if wr.SourcePack != source.Pack && wr.SourcePack != "(composite)" {
					continue
				}
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

func matchesTraceRefs(refs []domain.TraceRef, source *TraceSource, resName string) bool {
	for _, ref := range refs {
		if ref.Category != sourceCategory(source) {
			continue
		}
		if ref.Name != resName {
			continue
		}
		if ref.SourcePack != "" && source != nil && ref.SourcePack != source.Pack {
			continue
		}
		return true
	}
	return false
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
	if wr.Category != "" && wr.Category != domain.CategorySettings && wr.Category != sourceCategory(source) {
		return false, false
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

func sourceCategory(source *TraceSource) domain.PackCategory {
	if source == nil {
		return ""
	}
	return domain.PackCategory(source.Category)
}

func isHookDestinationFallback(dst string) bool {
	if filepath.Base(dst) == "hooks.json" {
		return true
	}
	// Walk parents using fixed-point detection: filepath.Dir is idempotent at
	// the root of any volume ("/" on Unix, "C:\\" on Windows), so compare
	// against the previous value rather than against any specific sentinel.
	dir := filepath.Dir(dst)
	for {
		if strings.EqualFold(filepath.Base(dir), "hooks") {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
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
