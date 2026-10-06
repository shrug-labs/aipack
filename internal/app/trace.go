package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// TraceRequest holds the parameters for tracing a resource through the sync pipeline.
type TraceRequest struct {
	TargetSpec
	ProfileName   string
	ProfileConfig config.ProfileConfig
	ResourceType  string // rule, agent, workflow, skill, plugin, mcp
	ResourceName  string // name of the resource to trace
	PackName      string // optional source-pack filter
	MCPTool       bool   // resolve an observed target tool name to its source server
	Diagnostic    *TraceDiagnostic
}

// TraceSource describes where a resource comes from in the pack.
type TraceSource struct {
	PluginSource       *domain.PluginSource             `json:"plugin_source,omitempty"`
	Origin             string                           `json:"origin,omitempty"`
	SubPath            string                           `json:"sub_path,omitempty"`
	CommitHash         string                           `json:"commit_hash,omitempty"`
	ConverterVersion   int                              `json:"converter_version,omitempty"`
	MaterializedDigest string                           `json:"materialized_digest,omitempty"`
	Selected           map[domain.PackCategory][]string `json:"selected,omitempty"`
	Pack               string                           `json:"pack"`
	SourcePath         string                           `json:"source_path"`
	SourcePaths        []string                         `json:"source_paths,omitempty"`
	NativeBinding      string                           `json:"native_binding,omitempty"`
	Category           string                           `json:"category"` // rules, agents, workflows, skills, plugins, mcp
}

// TraceCandidate describes an exact active-profile resource match that can be traced.
type TraceCandidate struct {
	ResourceType  string            `json:"resource_type"`
	ResourceName  string            `json:"resource_name"`
	Pack          string            `json:"pack"`
	SourcePath    string            `json:"source_path,omitempty"`
	SourcePaths   []string          `json:"source_paths,omitempty"`
	NativeBinding string            `json:"native_binding,omitempty"`
	Category      string            `json:"category"`
	ProfileState  TraceProfileState `json:"profile_state,omitempty"`
}

// TraceDestination describes where a resource lands in a harness location.
type TraceDestination struct {
	PlannedGeneration   string          `json:"planned_generation,omitempty"`
	DeliveredGeneration string          `json:"delivered_generation,omitempty"`
	MarketplaceSource   string          `json:"marketplace_source,omitempty"`
	Harness             string          `json:"harness"`
	Path                string          `json:"path"`
	Embedded            bool            `json:"embedded,omitempty"` // true when resource is composited into a multi-resource file
	State               string          `json:"state"`              // create, identical, managed, untracked, error
	DiffKind            domain.DiffKind `json:"diff_kind"`
	Location            string          `json:"location,omitempty"` // native package or installed cache
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
		Pack:          d.Candidate.Pack,
		SourcePath:    d.Candidate.SourcePath,
		SourcePaths:   d.Candidate.SourcePaths,
		NativeBinding: d.Candidate.NativeBinding,
		Category:      d.Candidate.Category,
	}
}

// RunTrace traces a resource through the sync pipeline, showing where it comes
// from and where it would land in each harness location.
func RunTrace(ctx context.Context, eng *engine.Engine, profile domain.Profile, req TraceRequest, reg *harness.Registry) (result TraceResult, err error) {
	if req.MCPTool {
		name, pack, err := traceMCPToolSource(ctx, profile, req, reg)
		if err != nil {
			return TraceResult{}, err
		}
		if name == "" {
			return TraceResult{ResourceType: "mcp", ResourceName: req.ResourceName, ProfileState: TraceProfileStateNotInstalled}, nil
		}
		req.ResourceName, req.PackName = name, pack
	}
	result = TraceResult{
		ResourceType: req.ResourceType,
		ResourceName: req.ResourceName,
		ProfileState: TraceProfileStateNotInstalled,
	}
	knownPacks := knownPacksFromRoots(resolvePackRoots(profile))

	// Find the resource in the profile.
	source := findResource(profile, req.ResourceType, req.ResourceName, req.PackName)
	if (req.ResourceType == "skill" || req.ResourceType == "mcp") && req.PackName == "" {
		candidates := FindTraceCandidatesForTargets(profile, req.ResourceName, req.TargetSpec)
		candidates = slices.DeleteFunc(candidates, func(candidate TraceCandidate) bool { return candidate.ResourceType != req.ResourceType })
		if len(candidates) > 1 {
			return result, fmt.Errorf("%s %q has multiple source packs for the requested targets; select one with --pack", req.ResourceType, req.ResourceName)
		}
		if len(candidates) == 1 {
			source = findResource(profile, req.ResourceType, candidates[0].ResourceName, candidates[0].Pack)
		}
	}
	if source == nil {
		if req.Diagnostic != nil && (req.PackName == "" || req.Diagnostic.Candidate.Pack == req.PackName) {
			applyTraceDiagnosticMatch(&result, *req.Diagnostic)
			populateNativeTraceProvenance(req.ConfigDir, &result)
			return result, nil
		}
		applyTraceDiagnostic(&result, req)
		populateNativeTraceProvenance(req.ConfigDir, &result)
		return result, nil
	}
	result.Found = true
	result.ProfileState = TraceProfileStateActive
	result.Source = source
	populateNativeTraceProvenance(req.ConfigDir, &result)
	for _, pack := range profile.Packs {
		if pack.Name == source.Pack && pack.NativePlugin != nil {
			source.Selected = pack.NativePlugin.Selected
		}
	}

	// Build per-harness plans and aggregate destinations.
	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			continue
		}
		planReq := planRequestForTarget(req.TargetSpec, req.ConfigDir, false, hid)
		plan, err := engine.PlanSync(ctx, profile, planReq, planners)
		if err != nil {
			result.Blockers = append(result.Blockers, string(hid)+": "+err.Error())
			continue
		}
		if req.ResourceType == "skill" || req.ResourceType == "mcp" {
			effective, _, err := engine.ProfileForHarness(profile, hid, planReq)
			if err != nil {
				result.Blockers = append(result.Blockers, string(hid)+": "+err.Error())
				continue
			}
			if findResource(effective, req.ResourceType, req.ResourceName, source.Pack) == nil {
				result.Blockers = append(result.Blockers, string(hid)+": "+req.ResourceType+" "+req.ResourceName+" from pack "+source.Pack+" is suppressed by profile collision/override policy")
				continue
			}
		}
		var lg domain.Ledger
		if plan.Ledger != "" {
			l, warnings, lerr := eng.LoadLedger(plan.Ledger)
			if lerr != nil {
				result.Blockers = append(result.Blockers, string(hid)+": "+lerr.Error())
				continue
			}
			if len(warnings) > 0 {
				for _, warning := range warnings {
					result.Blockers = append(result.Blockers, string(hid)+": "+warning.String())
				}
				continue
			}
			lg = l
		}
		if source.NativeBinding != "" {
			dests, err := matchNativeTraceDestinations(eng, req, plan, source, lg)
			if err != nil {
				result.Blockers = append(result.Blockers, string(hid)+": "+err.Error())
				continue
			} else {
				result.Destinations = append(result.Destinations, dests...)
			}
			if len(dests) > 0 {
				continue
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

// traceMCPToolSource compares forward target namespaces, never reverses a lossy
// sanitizer. Known foreign namespaces participate in ambiguity checks.
func traceMCPToolSource(ctx context.Context, profile domain.Profile, req TraceRequest, reg *harness.Registry) (string, string, error) {
	if req.ResourceType != "mcp" || len(req.Harnesses) != 1 {
		return "", "", fmt.Errorf("MCP tool lookup requires type mcp and one explicit harness")
	}
	hid := req.Harnesses[0]
	if hid != domain.HarnessClaudeCode && hid != domain.HarnessOpenCode && hid != domain.HarnessCodex {
		return "", "", fmt.Errorf("MCP tool lookup is not supported for %s", hid)
	}
	effective, _, err := engine.ProfileForHarness(profile, hid, planRequestForTarget(req.TargetSpec, req.ConfigDir, false, hid))
	if err != nil {
		return "", "", err
	}
	h, err := reg.Lookup(hid)
	if err != nil {
		return "", "", err
	}
	captured, err := h.Capture(ctx, captureContextForHarness(req.TargetSpec, hid, knownPacksFromRoots(resolvePackRoots(profile))))
	if err != nil {
		return "", "", err
	}
	if len(captured.Warnings) > 0 {
		return "", "", fmt.Errorf("cannot resolve MCP tool ownership with target capture warnings: %v", captured.Warnings)
	}
	servers := map[string][]domain.MCPServer{}
	for name := range captured.MCPServers {
		servers[name] = nil
	}
	for _, server := range effective.MCPServers {
		servers[server.Name] = append(servers[server.Name], server)
	}
	for _, pack := range effective.Packs {
		if pack.NativePlugin == nil {
			continue
		}
		selection := pack.NativePlugin
		for _, id := range selection.Selected[domain.CategoryMCP] {
			name := id
			if hid == domain.HarnessClaudeCode {
				namespace, err := plugin.ClaudeNamespace(*selection)
				if err != nil {
					return "", "", err
				}
				name = "plugin:" + namespace + ":" + id
			} else if hid == domain.HarnessOpenCode {
				name = selection.Package.Binding() + ":" + id
			}
			servers[name] = append(servers[name], domain.MCPServer{Name: id, SourcePack: pack.Name})
		}
	}
	var matches []string
	for name := range servers {
		namespace := plugin.OpenCodeToolName(name)
		prefix := namespace + "_"
		if hid != domain.HarnessOpenCode {
			if hid == domain.HarnessCodex {
				namespace = strings.Map(func(c rune) rune {
					if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
						return c
					}
					return '_'
				}, name)
			}
			prefix = "mcp__" + namespace + "__"
		}
		if strings.HasPrefix(req.ResourceName, prefix) && len(req.ResourceName) > len(prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", "", nil
	}
	if len(matches) != 1 || len(servers[matches[0]]) > 1 {
		slices.Sort(matches)
		return "", "", fmt.Errorf("MCP tool %q has ambiguous target namespaces: %s", req.ResourceName, strings.Join(matches, ", "))
	}
	owners := servers[matches[0]]
	if len(owners) == 0 || (req.PackName != "" && req.PackName != owners[0].SourcePack) {
		return "", "", nil
	}
	return owners[0].Name, owners[0].SourcePack, nil
}

func populateNativeTraceProvenance(configDir string, result *TraceResult) {
	if result.Source == nil || result.Source.NativeBinding == "" {
		return
	}
	lock, err := config.LoadLockfileReadOnlyMerged(configDir)
	if err != nil {
		result.Blockers = append(result.Blockers, "native source metadata: "+err.Error())
		return
	}
	source := result.Source
	packDir := filepath.Join(PacksDir(configDir), source.Pack)
	if _, err := os.Lstat(packDir); err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("pack %q is not installed", source.Pack)
		} else {
			err = fmt.Errorf("stat pack %q: %w", source.Pack, err)
		}
		result.Blockers = append(result.Blockers, "native source metadata: "+err.Error())
		return
	}
	entry := lock.Packs[source.Pack]
	source.PluginSource, source.Origin, source.SubPath = entry.Plugin, entry.Origin, entry.SubPath
	source.CommitHash, source.MaterializedDigest = entry.CommitHash, entry.MaterializedDigest
	if manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json")); err == nil && manifest.NativePlugin != nil {
		source.ConverterVersion = manifest.NativePlugin.ConverterVersion
	}
}

func applyTraceDiagnostic(result *TraceResult, req TraceRequest) {
	matches := findTraceDiagnostics(req.ProfileConfig, req.ConfigDir, req.ResourceType, req.ResourceName, req.ProfileName, req.PackName)
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
func FindTraceDiagnosticCandidates(profileCfg config.ProfileConfig, configDir, name, profileName string, packFilter ...string) []TraceDiagnostic {
	matches := findTraceDiagnostics(profileCfg, configDir, "", name, profileName, packFilter...)
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

func findTraceDiagnostics(profileCfg config.ProfileConfig, configDir, resType, name, profileName string, packFilter ...string) []TraceDiagnostic {
	packName := ""
	if len(packFilter) > 0 {
		packName = packFilter[0]
	}
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
	for _, pack := range packs {
		if packName != "" && pack.Name != packName {
			continue
		}
		native := pack.Manifest.NativePlugin
		if (catFilter != "" && catFilter != domain.CategoryPlugins) || native == nil || !traceNameMatches(domain.CategoryPlugins, native.Name, name, native.Binding()) || config.PackEnabled(profileCfg.Packs[pack.Index].Enabled) {
			continue
		}
		paths := nativeTracePaths(pack.Root, native, domain.CategoryPlugins, native.Name)
		matches = append(matches, TraceDiagnostic{Candidate: TraceCandidate{ResourceType: "plugin", ResourceName: native.Name, Pack: pack.Name,
			SourcePath: paths[0], SourcePaths: paths, NativeBinding: native.Binding(), Category: string(domain.CategoryPlugins), ProfileState: TraceProfileStatePackDisabled},
			Blockers:    []string{"pack " + pack.Name + " is disabled in profile " + profileName},
			Remediation: []string{tracePackProfileCommand("enable", pack.Name, profileName), traceSyncCommand(profileName)}})
	}
	tree := BuildContentTree(packs, profileCfg.Packs)
	for _, item := range tree.Items {
		if catFilter != "" && item.Category != catFilter {
			continue
		}
		pack := tree.Packs[item.PackIdx]
		if !traceNameMatches(item.Category, item.ID, name, nativeTraceBinding(pack.Manifest.NativePlugin)) || item.Enabled {
			continue
		}
		if packName != "" && pack.Name != packName {
			continue
		}
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
		if packName != "" && pack.Name != packName {
			continue
		}
		if _, inProfile := profilePackNames[pack.Name]; inProfile {
			continue
		}
		for _, cat := range TraceableCategories() {
			if catFilter != "" && cat != catFilter {
				continue
			}
			wholeNativePlugin := cat == domain.CategoryPlugins && pack.NativePlugin != nil && traceNameMatches(cat, pack.NativePlugin.Name, name, pack.NativePlugin.Binding())
			var id string
			if wholeNativePlugin {
				id = pack.NativePlugin.Name
			} else {
				ids := pack.ContentIDs(cat)
				i := slices.IndexFunc(ids, func(id string) bool {
					return traceNameMatches(cat, id, name, nativeTraceBinding(pack.NativePlugin))
				})
				if i < 0 {
					continue
				}
				id = ids[i]
			}
			candidate := TraceCandidate{
				ResourceType:  traceResourceType(cat),
				ResourceName:  id,
				Pack:          pack.Name,
				SourcePath:    traceInstalledSourcePath(pack, cat, id),
				SourcePaths:   nativeTracePaths(pack.Path, pack.NativePlugin, cat, id),
				NativeBinding: nativeTraceBinding(pack.NativePlugin),
				Category:      string(cat),
				ProfileState:  TraceProfileStateInstalledNotInProfile,
			}
			if wholeNativePlugin {
				candidate.SourcePath = candidate.SourcePaths[0]
			}
			matches = append(matches, TraceDiagnostic{
				Candidate: candidate,
				Blockers: []string{
					"pack " + pack.Name + " is installed but not listed in profile " + profileName,
				},
				Remediation: installedPackRemediation(pack.installQuiet && !wholeNativePlugin, pack.Name, id, traceResourceType(cat), profileName),
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
			ResourceType:  traceResourceType(cat),
			ResourceName:  item.ID,
			Pack:          pack.Name,
			SourcePath:    traceProfileSourcePath(pack, cat, item.ID),
			SourcePaths:   nativeTracePaths(pack.Root, pack.Manifest.NativePlugin, cat, item.ID),
			NativeBinding: nativeTraceBinding(pack.Manifest.NativePlugin),
			Category:      string(cat),
			ProfileState:  state,
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
	if cat == domain.CategoryMCP && pack.Manifest.NativePlugin == nil {
		return ""
	}
	return pack.ContentPath(cat, id)
}

func traceInstalledSourcePath(pack PackShowEntry, cat domain.PackCategory, id string) string {
	if cat == domain.CategoryMCP && pack.NativePlugin == nil {
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
func findResource(profile domain.Profile, resType, name string, packFilter ...string) *TraceSource {
	cat, ok := domain.ParseSingularLabel(resType)
	if !ok {
		return nil
	}
	var found *TraceSource
	walkTraceResources(profile, func(resourceCat domain.PackCategory, resourceName string, source TraceSource) bool {
		matches := resourceName == name || cat == domain.CategoryPlugins && traceNameMatches(cat, resourceName, name, source.NativeBinding)
		if resourceCat == cat && matches && (len(packFilter) == 0 || packFilter[0] == "" || source.Pack == packFilter[0]) {
			found = &source
			return false
		}
		return true
	})
	return found
}

// FindTraceCandidates finds active-profile resources by source name or native
// plugin skill identity across all traceable categories.
func FindTraceCandidates(profile domain.Profile, name string) []TraceCandidate {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var out []TraceCandidate
	walkTraceResources(profile, func(cat domain.PackCategory, resourceName string, source TraceSource) bool {
		if !traceNameMatches(cat, resourceName, name, source.NativeBinding) && !(cat == domain.CategorySkills && name == harness.RenderedContentName(source.Pack, resourceName)) {
			return true
		}
		out = append(out, TraceCandidate{
			ResourceType:  traceResourceType(cat),
			ResourceName:  resourceName,
			Pack:          source.Pack,
			SourcePath:    source.SourcePath,
			SourcePaths:   source.SourcePaths,
			NativeBinding: source.NativeBinding,
			Category:      source.Category,
		})
		return true
	})
	return out
}

// FindTraceCandidatesForTargets resolves ordinary skill and MCP names against the same
// target projection as sync, retaining original plugin identities for attribution.
// Explicit native aliases still locate their source even when delivery is blocked.
func FindTraceCandidatesForTargets(profile domain.Profile, name string, target TargetSpec) []TraceCandidate {
	candidates := FindTraceCandidates(profile, name)
	if len(target.Harnesses) == 0 {
		return candidates
	}
	var active []TraceCandidate
	for _, hid := range target.Harnesses {
		effective, _, err := engine.ProfileForHarness(profile, hid, planRequestForTarget(target, target.ConfigDir, false, hid))
		if err != nil {
			return candidates // Keep source lookup available so trace can report planning blockers.
		}
		for _, candidate := range candidates {
			if (candidate.ResourceType == "skill" || candidate.ResourceType == "mcp") && name == candidate.ResourceName && findResource(effective, candidate.ResourceType, candidate.ResourceName, candidate.Pack) == nil {
				continue
			}
			if !slices.ContainsFunc(active, func(previous TraceCandidate) bool {
				return previous.ResourceType == candidate.ResourceType && previous.ResourceName == candidate.ResourceName && previous.Pack == candidate.Pack
			}) {
				active = append(active, candidate)
			}
		}
	}
	return active
}

func traceNameMatches(cat domain.PackCategory, id, name, binding string) bool {
	if name == id {
		return true
	}
	if cat == domain.CategoryPlugins && binding != "" {
		return name == binding
	}
	if cat != domain.CategorySkills || binding == "" {
		return false
	}
	pluginName, _, _ := strings.Cut(binding, "@")
	return name == pluginName+":"+id || name == binding+":"+id
}

func walkTraceResources(profile domain.Profile, visit func(domain.PackCategory, string, TraceSource) bool) {
	for _, pack := range profile.Packs {
		if pack.NativePlugin == nil {
			continue
		}
		selection := pack.NativePlugin
		for _, cat := range TraceableCategories() {
			ids := selection.Selected[cat]
			if cat == domain.CategoryPlugins {
				ids = []string{selection.Package.Name}
			}
			for _, id := range ids {
				paths := nativeTracePaths(selection.Root, &selection.Package, cat, id)
				source := TraceSource{Pack: pack.Name, Category: string(cat), SourcePaths: paths, NativeBinding: selection.Package.Binding()}
				if len(paths) > 0 {
					source.SourcePath = paths[0]
				}
				if !visit(cat, id, source) {
					return
				}
			}
		}
	}
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
	for _, m := range profile.MCPServers {
		if !visit(domain.CategoryMCP, m.Name, TraceSource{Pack: m.SourcePack, Category: string(domain.CategoryMCP)}) {
			return
		}
	}
}

func nativeTracePaths(root string, native *domain.NativePlugin, cat domain.PackCategory, id string) []string {
	if native == nil {
		return nil
	}
	var paths []string
	rels := native.Components[cat][id]
	if cat == domain.CategoryPlugins && id == native.Name {
		rels = []string{native.Manifest}
	}
	for _, rel := range rels {
		path := filepath.Join(root, "pack.json")
		if rel != "" {
			path = filepath.Join(root, "upstream", rel)
		}
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}

func nativeTraceBinding(native *domain.NativePlugin) string {
	if native == nil {
		return ""
	}
	return native.Binding()
}

func matchNativeTraceDestinations(eng *engine.Engine, req TraceRequest, plan domain.Plan, source *TraceSource, ledger domain.Ledger) ([]TraceDestination, error) {
	ops, err := nativePluginPlanOps(eng, SyncRequest{TargetSpec: req.TargetSpec}, plan, ledger)
	if err != nil {
		return nil, err
	}
	var destinations []TraceDestination
	for _, action := range plan.Writes {
		if action.Delivery == nil || action.SourcePack != source.Pack || action.Delivery.Binding != source.NativeBinding {
			continue
		}
		fd, err := eng.ClassifyWrite(action, action.Dst, ledger)
		if err != nil {
			return nil, err
		}
		delivered := ""
		var last int64
		for _, entry := range ledger.Managed {
			if entry.Delivery != nil && entry.Delivery.Binding == source.NativeBinding && entry.Delivery.SettingsPath == action.Delivery.SettingsPath && entry.SyncedAtEpochS >= last {
				delivered, last = entry.Delivery.Generation, entry.SyncedAtEpochS
			}
		}
		paths := []string{action.Dst}
		if domain.PackCategory(source.Category) != domain.CategoryPlugins {
			paths = nil
			for _, original := range source.SourcePaths {
				rel, err := filepath.Rel(action.Src, original)
				if err != nil || !filepath.IsLocal(rel) {
					continue
				}
				paths = append(paths, filepath.Join(action.Dst, rel))
			}
		}
		for _, path := range paths {
			destinations = append(destinations, TraceDestination{Harness: string(domain.HarnessOpenCode), Path: path, Location: "package", Embedded: domain.PackCategory(source.Category) == domain.CategoryMCP,
				State: string(fd.Kind), DiffKind: fd.Kind, PlannedGeneration: action.Delivery.Generation, DeliveredGeneration: delivered})
		}
		for _, settings := range append(plan.Settings, plan.MCP...) {
			if settings.Dst != action.Delivery.SettingsPath || !matchesTraceRefs(settings.TraceRefs, source, req.ResourceName) {
				continue
			}
			state, kind := classifySettingsState(eng, settings, ledger)
			destinations = append(destinations, TraceDestination{Harness: string(domain.HarnessOpenCode), Path: settings.Dst, Location: "activation", Embedded: true,
				State: state, DiffKind: kind, PlannedGeneration: action.Delivery.Generation, DeliveredGeneration: delivered})
		}
	}
	for _, action := range plan.NativePlugins {
		if action.SourcePack != source.Pack || action.Package.Binding() != source.NativeBinding {
			continue
		}
		payload, err := nativePayloadPath(action.Package)
		if err != nil {
			return nil, err
		}
		files := map[string]plugin.File{}
		for _, file := range action.Files {
			files[file.Path] = file
		}
		kind := domain.DiffIdentical
		for _, op := range ops {
			if op.DisplayDst == source.NativeBinding {
				kind = op.DiffKind
			}
		}
		planned, err := nativeGeneration(action)
		if err != nil {
			return nil, err
		}
		delivered := ""
		roots := []struct{ path, location string }{{filepath.Join(action.MarketplaceDir, payload), "package"}}
		if previous, ok := ledger.NativePlugins[source.NativeBinding]; ok && previous.ConfigHome == action.ConfigHome {
			if err := validateNativeRecord(req.ConfigDir, source.NativeBinding, previous); err != nil {
				return nil, err
			}
			roots = append(roots, struct{ path, location string }{previous.CachePath, "cache"})
			delivered = previous.Generation
		}
		cat := domain.PackCategory(source.Category)
		rels := action.Package.Components[cat][req.ResourceName]
		if cat == domain.CategoryPlugins {
			rels = []string{""}
		}
		for _, root := range roots {
			seen := map[string]bool{}
			for _, rel := range rels {
				path := filepath.Join(root.path, rel)
				if seen[path] {
					continue
				}
				seen[path] = true
				destinationKind := kind
				if _, err := os.Stat(path); os.IsNotExist(err) {
					destinationKind = domain.DiffCreate
				} else if err != nil {
					destinationKind = domain.DiffError
				} else if destinationKind == domain.DiffIdentical && rel != "" {
					// A matching package generation does not prove the live file is intact.
					file, exists := files[rel]
					var fileErr error
					if !exists {
						file, fileErr = plugin.ResolvePayloadFile(files, rel)
					}
					if fileErr != nil {
						destinationKind = domain.DiffError
					} else if file.Link != "" {
						link, err := os.Readlink(path)
						if err != nil {
							destinationKind = domain.DiffError
						} else if link != file.Link {
							destinationKind = domain.DiffConflict
						}
					} else if file.Mode.IsRegular() {
						body, err := os.ReadFile(path)
						if err != nil {
							destinationKind = domain.DiffError
						} else if !bytes.Equal(body, file.Content) {
							destinationKind = domain.DiffConflict
						}
					}
				}
				destinations = append(destinations, TraceDestination{Harness: string(action.Package.Harness), Path: path, Location: root.location,
					Embedded: rel == "" || cat == domain.CategoryMCP || cat == domain.CategoryHooks, State: string(destinationKind), DiffKind: destinationKind,
					PlannedGeneration: planned, DeliveredGeneration: delivered, MarketplaceSource: action.MarketplaceDir})
			}
		}
		for _, destination := range matchDestinations(eng, plan, source, ledger, nil, "plugin", action.Package.Name, action.Package.Harness) {
			if destination.Path != action.SettingsPath {
				continue
			}
			destination.Location = "activation"
			destination.PlannedGeneration, destination.DeliveredGeneration = planned, delivered
			destination.MarketplaceSource = action.MarketplaceDir
			destinations = append(destinations, destination)
		}
	}
	return destinations, nil
}

func traceResourceType(cat domain.PackCategory) string {
	if cat == domain.CategoryMCP {
		return "mcp"
	}
	return strings.ToLower(cat.SingularLabel())
}

// TraceableCategories returns the resource categories supported by trace.
func TraceableCategories() []domain.PackCategory {
	return append(profileContentSearchCategories(), domain.CategoryPlugins)
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
		for _, write := range plan.Writes {
			if write.Category != domain.CategorySkills || write.SourcePack != source.Pack || (write.Src != source.SourcePath && write.Src != filepath.Join(source.SourcePath, domain.SkillEntryFile)) {
				continue
			}
			state, kind := classifyWriteState(eng, write, lg)
			dests = append(dests, TraceDestination{Harness: string(hid), Path: write.Dst, State: state, DiffKind: kind})
		}
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
			if action.Name == resName && (action.SourcePack == "" || action.SourcePack == source.Pack) {
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
