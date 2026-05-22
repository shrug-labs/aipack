package config

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// ErrProfileNoPacks signals that a profile has no packs configured. Callers
// can match it via errors.Is to surface a first-mile setup hint instead of
// the raw resolver error.
var ErrProfileNoPacks = errors.New("profile packs must be configured")

const (
	capRules     = "rules"
	capAgents    = "agents"
	capWorkflows = "workflows"
	capSkills    = "skills"
	capHooks     = "hooks"
	capPlugins   = "plugins"
)

type ResolvedPack struct {
	Name      string
	Root      string
	Manifest  PackManifest
	Rules     []string
	Agents    []string
	Workflows []string
	Skills    []string
	Hooks     []string
	Plugins   []string
	MCP       map[string]ResolvedMCPServer
}

// ContentIDs returns the resolved id list for the given authored category.
// Empty for unknown categories.
func (rp ResolvedPack) ContentIDs(cat domain.PackCategory) []string {
	switch cat {
	case domain.CategoryRules:
		return rp.Rules
	case domain.CategoryAgents:
		return rp.Agents
	case domain.CategoryWorkflows:
		return rp.Workflows
	case domain.CategorySkills:
		return rp.Skills
	case domain.CategoryHooks:
		return rp.Hooks
	case domain.CategoryPlugins:
		return rp.Plugins
	}
	return nil
}

type ResolvedMCPServer struct {
	AllowedTools       []string
	AlwaysAllowedTools []string
	DisabledTools      []string
}

// ResolveResult holds the outputs of ResolveProfile.
type ResolveResult struct {
	Packs             []ResolvedPack
	SettingsPacks     []string
	CollisionWarnings []domain.Warning
	// BrokenRefs are profile references that were in a previous pack
	// inventory (per the lockfile) but are not in the current resolve.
	// Only populated when prevInventories is supplied.
	BrokenRefs []domain.BrokenRef
}

type ResolveOptions struct {
	CollisionStrategy CollisionStrategy
	Namespaced        bool
	PrevInventories   map[string]domain.PackInventory
}

// ResolveProfile resolves a profile. When prevInventories is non-nil, an
// exact-ID reference that isn't in the current pack inventory but WAS in
// the previous one becomes a BrokenRef instead of a hard error — the pack
// removed content the profile still references. Typos (IDs that were
// never in the pack) still hard-error.
func ResolveProfile(cfg ProfileConfig, profilePath string, configDir string, strategy CollisionStrategy, prevInventories map[string]domain.PackInventory) (ResolveResult, error) {
	return ResolveProfileWithOptions(cfg, profilePath, configDir, ResolveOptions{
		CollisionStrategy: strategy,
		PrevInventories:   prevInventories,
	})
}

func ResolveProfileWithOptions(cfg ProfileConfig, profilePath string, configDir string, opts ResolveOptions) (ResolveResult, error) {
	strategy := opts.CollisionStrategy
	prevInventories := opts.PrevInventories
	if strategy == "" {
		strategy = CollisionLastWins
	}
	if len(cfg.Packs) == 0 {
		return ResolveResult{}, ErrProfileNoPacks
	}

	var collisions []collisionInfo
	var collisionWarnings []domain.Warning
	var brokenRefs []domain.BrokenRef

	var packs []ResolvedPack
	var settingsPacks []string
	seenServers := map[string]string{}

	// vectorState tracks seen IDs and override owners for each string-slice
	// resource vector (rules, agents, workflows, skills). Keyed by label.
	type vectorState struct {
		label      string
		namespaced bool
		seen       map[string]string
		owner      map[string]string
		field      func(*ResolvedPack) *[]string
		overrides  func(PackEntry) []string
	}
	vectors := []vectorState{
		{capRules, true, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Rules }, func(pe PackEntry) []string { return pe.Overrides.Rules }},
		{capAgents, true, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Agents }, func(pe PackEntry) []string { return pe.Overrides.Agents }},
		{capWorkflows, true, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Workflows }, func(pe PackEntry) []string { return pe.Overrides.Workflows }},
		{capSkills, true, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Skills }, func(pe PackEntry) []string { return pe.Overrides.Skills }},
		{capHooks, true, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Hooks }, func(pe PackEntry) []string { return pe.Overrides.Hooks }},
		{capPlugins, false, map[string]string{}, map[string]string{}, func(p *ResolvedPack) *[]string { return &p.Plugins }, func(pe PackEntry) []string { return pe.Overrides.Plugins }},
	}

	// Pre-scan: build override owner maps so the declaring pack wins
	// regardless of pack ordering. Disabled packs are excluded — a
	// disabled pack should not participate in conflict resolution.
	overrideOwnerMCP := map[string]string{}
	for _, pc := range cfg.Packs {
		if !defaultTrue(pc.Enabled) {
			continue
		}
		name := strings.TrimSpace(pc.Name)
		for _, v := range vectors {
			for _, id := range v.overrides(pc) {
				v.owner[id] = name
			}
		}
		for _, id := range pc.Overrides.MCP {
			overrideOwnerMCP[id] = name
		}
	}

	for _, packCfg := range cfg.Packs {
		packName := strings.TrimSpace(packCfg.Name)
		if packName == "" {
			return ResolveResult{}, errors.New("profile packs entries must have name")
		}
		if strings.Contains(packName, domain.RenderedIdentitySeparator) {
			return ResolveResult{}, fmt.Errorf("profile pack %q must not contain %q (reserved for rendered content identity)",
				packName, domain.RenderedIdentitySeparator)
		}
		if !defaultTrue(packCfg.Enabled) {
			continue
		}

		packRoot := filepath.Join(configDir, "packs", packName)
		quiet := packCfg.Quiet

		manifestPath := filepath.Join(packRoot, "pack.json")
		manifest, err := LoadPackManifest(manifestPath)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("pack %q manifest: %w", packName, err)
		}
		packRoot = ResolvePackRoot(manifestPath, manifest.Root)
		if packRoot == "" {
			return ResolveResult{}, fmt.Errorf("pack %q root could not be resolved", packName)
		}
		if err := DiscoverContent(&manifest, packRoot); err != nil {
			return ResolveResult{}, fmt.Errorf("pack %q content discovery: %w", packName, err)
		}
		if err := validatePackInventory(packName, packRoot, manifest); err != nil {
			return ResolveResult{}, err
		}

		var prevInv *domain.PackInventory
		if prev, ok := prevInventories[packName]; ok {
			prevInv = &prev
		}

		rules, broken, err := resolveVector(packName, capRules, manifest.Rules, packCfg.Rules, quiet, prevInv)
		if err != nil {
			return ResolveResult{}, err
		}
		brokenRefs = append(brokenRefs, broken...)
		agents, broken, err := resolveVector(packName, capAgents, manifest.Agents, packCfg.Agents, quiet, prevInv)
		if err != nil {
			return ResolveResult{}, err
		}
		brokenRefs = append(brokenRefs, broken...)
		workflows, broken, err := resolveVector(packName, capWorkflows, manifest.Workflows, packCfg.Workflows, quiet, prevInv)
		if err != nil {
			return ResolveResult{}, err
		}
		brokenRefs = append(brokenRefs, broken...)
		skills, broken, err := resolveVector(packName, capSkills, manifest.Skills, packCfg.Skills, quiet, prevInv)
		if err != nil {
			return ResolveResult{}, err
		}
		brokenRefs = append(brokenRefs, broken...)
		var hooks []string
		if defaultTrue(packCfg.Hooks.Enabled) {
			hooks, broken, err = resolveVector(packName, capHooks, manifest.Hooks, packCfg.Hooks.VectorSelector, quiet, prevInv)
			if err != nil {
				return ResolveResult{}, err
			}
			brokenRefs = append(brokenRefs, broken...)
		}
		plugins, broken, err := resolveVector(packName, capPlugins, manifest.Plugins, packCfg.Plugins, quiet, prevInv)
		if err != nil {
			return ResolveResult{}, err
		}
		brokenRefs = append(brokenRefs, broken...)

		packResolved := ResolvedPack{
			Name:      packName,
			Root:      packRoot,
			Manifest:  manifest,
			Rules:     rules,
			Agents:    agents,
			Workflows: workflows,
			Skills:    skills,
			Hooks:     hooks,
			Plugins:   plugins,
			MCP:       map[string]ResolvedMCPServer{},
		}

		// Override resolution is order-independent. The owner maps built
		// above determine winners regardless of pack ordering.
		//
		// We must not mutate the current pack's slice while ranging over it
		// (slices.DeleteFunc shifts elements, causing the range to skip items).
		// Instead, collect IDs to strip and apply after the loop.
		resolveVectorCollision := func(v *vectorState, id, prev string, stripFromCurrent *[]string) bool {
			owner := v.owner[id]
			if owner == "" {
				if opts.Namespaced && v.namespaced {
					return true
				}
				switch strategy {
				case CollisionFirstWins:
					*stripFromCurrent = append(*stripFromCurrent, id)
					collisionWarnings = append(collisionWarnings, domain.Warning{
						Field:   v.label,
						Message: fmt.Sprintf("%s %q: %q wins over %q (first-wins)", v.label, id, prev, packName),
					})
					return false
				case CollisionLastWins:
					stripFromPack(packs, prev, id, v.field)
					collisionWarnings = append(collisionWarnings, domain.Warning{
						Field:   v.label,
						Message: fmt.Sprintf("%s %q: %q wins over %q (last-wins)", v.label, id, packName, prev),
					})
					return true
				default: // CollisionError
					collisions = append(collisions, collisionInfo{kind: v.label, id: id, packA: prev, packB: packName})
					return false
				}
			}
			if owner == packName {
				stripFromPack(packs, prev, id, v.field)
				return true
			}
			*stripFromCurrent = append(*stripFromCurrent, id)
			return false
		}
		for vi := range vectors {
			v := &vectors[vi]
			var stripFromCurrent []string
			for _, id := range *v.field(&packResolved) {
				if prev, ok := v.seen[id]; ok {
					if !resolveVectorCollision(v, id, prev, &stripFromCurrent) {
						continue
					}
				}
				v.seen[id] = packName
			}
			if len(stripFromCurrent) > 0 {
				drop := map[string]struct{}{}
				for _, id := range stripFromCurrent {
					drop[id] = struct{}{}
				}
				f := v.field(&packResolved)
				*f = slices.DeleteFunc(*f, func(s string) bool {
					_, ok := drop[s]
					return ok
				})
			}
		}

		// Quiet semantics for MCP mirror content vectors: include nothing
		// unless the profile entry explicitly opts in. Non-quiet packs keep
		// the manifest-derived default so new servers an upstream pack adds
		// become available automatically. A non-quiet disable-only map keeps
		// that default and applies the listed entries as exclusions; maps with
		// any enabled entry or tool policy keep the existing inclusive semantics.
		mcpSelection := ResolveProfileMCPSelection(manifest.MCP, packCfg.MCP, quiet)
		manifestMCPSet := ToStringSet(manifest.MCP)
		for name, serverCfg := range mcpSelection {
			if !manifestMCPSet[name] {
				return ResolveResult{}, fmt.Errorf("pack %q references unknown mcp server %q", packName, name)
			}
			if !defaultTrue(serverCfg.Enabled) {
				continue
			}
			entry := ResolvedMCPServer{
				AllowedTools:       normalizeList(serverCfg.AllowedTools),
				AlwaysAllowedTools: normalizeList(serverCfg.AlwaysAllowedTools),
				DisabledTools:      normalizeList(serverCfg.DisabledTools),
			}
			packResolved.MCP[name] = entry
			if prev, ok := seenServers[name]; ok {
				owner := overrideOwnerMCP[name]
				if owner == "" {
					switch strategy {
					case CollisionFirstWins:
						delete(packResolved.MCP, name)
						collisionWarnings = append(collisionWarnings, domain.Warning{
							Field:   "mcp",
							Message: fmt.Sprintf("mcp %q: %q wins over %q (first-wins)", name, prev, packName),
						})
						continue
					case CollisionLastWins:
						stripMCP(packs, prev, name)
						collisionWarnings = append(collisionWarnings, domain.Warning{
							Field:   "mcp",
							Message: fmt.Sprintf("mcp %q: %q wins over %q (last-wins)", name, packName, prev),
						})
						// fall through to update seenServers
					default: // CollisionError
						collisions = append(collisions, collisionInfo{kind: "mcp", id: name, packA: prev, packB: packName})
						continue
					}
				} else if owner == packName {
					stripMCP(packs, prev, name)
				} else {
					delete(packResolved.MCP, name)
					continue
				}
			}
			seenServers[name] = packName
		}

		// Settings: packs contribute if they have config files and are not
		// opted out. Quiet packs opt out by default — a nil Settings.Enabled
		// suppresses contribution — but an explicit Settings.Enabled: true
		// still opts in.
		contributeSettings := manifest.Configs.HasAnyConfigs() && !settingsDisabled(packCfg.Settings.Enabled)
		if quiet && packCfg.Settings.Enabled == nil {
			contributeSettings = false
		}
		if contributeSettings {
			settingsPacks = append(settingsPacks, packName)
		}

		packs = append(packs, packResolved)
	}

	if len(collisions) > 0 {
		return ResolveResult{}, formatCollisionError(collisions)
	}

	if len(packs) == 0 {
		return ResolveResult{}, errors.New("no enabled packs in profile")
	}

	// Post-resolution validation: every override ID must match actual
	// resolved content. Unmatched IDs were never in the pack (typo) unless
	// the owner pack's previous inventory had them, in which case they
	// drifted out and become BrokenRefs instead of fatal errors.
	for _, v := range vectors {
		cat := labelToCategory(v.label)
		for id, owner := range v.owner {
			if _, ok := v.seen[id]; ok {
				continue
			}
			if prev, ok := prevInventories[owner]; ok && cat != "" && prev.Contains(cat, id) {
				brokenRefs = append(brokenRefs, domain.BrokenRef{
					PackName:  owner,
					Category:  cat,
					Direction: "overrides",
					ID:        id,
				})
				continue
			}
			return ResolveResult{}, fmt.Errorf("pack %q overrides.%s references %q, but no pack provides that %s", owner, v.label, id, v.label)
		}
	}
	for id, owner := range overrideOwnerMCP {
		if _, ok := seenServers[id]; ok {
			continue
		}
		if prev, ok := prevInventories[owner]; ok && prev.Contains(domain.CategoryMCP, id) {
			brokenRefs = append(brokenRefs, domain.BrokenRef{
				PackName:  owner,
				Category:  domain.CategoryMCP,
				Direction: "overrides",
				ID:        id,
			})
			continue
		}
		return ResolveResult{}, fmt.Errorf("pack %q overrides.mcp references %q, but no pack provides that server", owner, id)
	}

	return ResolveResult{
		Packs:             packs,
		SettingsPacks:     settingsPacks,
		CollisionWarnings: collisionWarnings,
		BrokenRefs:        brokenRefs,
	}, nil
}

// ResolveProfileMCPSelection applies profile MCP selection semantics to a
// pack's manifest-declared servers.
func ResolveProfileMCPSelection(manifestMCP []string, profileMCP map[string]MCPServerConfig, quiet bool) map[string]MCPServerConfig {
	if len(profileMCP) == 0 {
		if quiet {
			return nil
		}
		return defaultMCPSelection(manifestMCP)
	}
	if !quiet && MCPSelectionIsDisableOnly(profileMCP) {
		out := defaultMCPSelection(manifestMCP)
		maps.Copy(out, profileMCP)
		return out
	}
	return profileMCP
}

func defaultMCPSelection(manifestMCP []string) map[string]MCPServerConfig {
	out := make(map[string]MCPServerConfig, len(manifestMCP))
	for _, name := range manifestMCP {
		out[name] = MCPServerConfig{}
	}
	return out
}

// MCPSelectionIsDisableOnly reports whether a profile MCP map only subtracts
// servers from the non-quiet manifest default.
func MCPSelectionIsDisableOnly(profileMCP map[string]MCPServerConfig) bool {
	if len(profileMCP) == 0 {
		return false
	}
	for _, cfg := range profileMCP {
		if !cfg.IsDisableOnly() {
			return false
		}
	}
	return true
}

func stripFromPack(packs []ResolvedPack, packName, id string, field func(*ResolvedPack) *[]string) {
	for i := range packs {
		if packs[i].Name == packName {
			s := field(&packs[i])
			*s = slices.DeleteFunc(*s, func(v string) bool { return v == id })
			return
		}
	}
}

func stripMCP(packs []ResolvedPack, packName, name string) {
	for i := range packs {
		if packs[i].Name == packName {
			delete(packs[i].MCP, name)
			return
		}
	}
}

// defaultTrue delegates to PackEnabled for nil-defaults-to-true semantics.
var defaultTrue = PackEnabled

// settingsDisabled delegates to SettingsDisabled for the opt-out model.
var settingsDisabled = SettingsDisabled

// labelToCategory maps the internal capXxx label strings to domain categories.
// Unknown labels return empty string (BrokenRef reporting still works, the
// resolver just can't consult prevInv — acceptable for the single call
// site that passes the label).
func labelToCategory(label string) domain.PackCategory {
	switch label {
	case capRules:
		return domain.CategoryRules
	case capAgents:
		return domain.CategoryAgents
	case capWorkflows:
		return domain.CategoryWorkflows
	case capSkills:
		return domain.CategorySkills
	case capHooks:
		return domain.CategoryHooks
	case capPlugins:
		return domain.CategoryPlugins
	}
	return ""
}

func resolveVector(packName string, label string, inventory []string, sel VectorSelector, quiet bool, prevInv *domain.PackInventory) ([]string, []domain.BrokenRef, error) {
	if sel.Include != nil && sel.Exclude != nil {
		return nil, nil, fmt.Errorf("pack %q %s cannot set both include and exclude", packName, label)
	}
	inv := normalizeList(inventory)
	invSet := map[string]struct{}{}
	for _, v := range inv {
		invSet[v] = struct{}{}
	}

	// Quiet packs include nothing unless an explicit non-empty include list
	// is provided. Exclude selectors have no effect (nothing to subtract from).
	if quiet {
		if sel.Include != nil {
			include := normalizeList(*sel.Include)
			if len(include) > 0 {
				return expandSelectors(packName, label, "include", include, inv, invSet, prevInv)
			}
		}
		return nil, nil, nil
	}

	if sel.Include != nil {
		include := normalizeList(*sel.Include)
		if len(include) == 0 {
			return inv, nil, nil // empty include = include all (backward compat)
		}
		return expandSelectors(packName, label, "include", include, inv, invSet, prevInv)
	}
	if sel.Exclude == nil {
		return inv, nil, nil
	}
	exclude := normalizeList(*sel.Exclude)
	matched, broken, err := expandSelectors(packName, label, "exclude", exclude, inv, invSet, prevInv)
	if err != nil {
		return nil, nil, err
	}
	matchedSet := map[string]struct{}{}
	for _, v := range matched {
		matchedSet[v] = struct{}{}
	}
	out := make([]string, 0, len(inv))
	for _, v := range inv {
		if _, ok := matchedSet[v]; ok {
			continue
		}
		out = append(out, v)
	}
	return out, broken, nil
}

// isGlobPattern reports whether s contains glob metacharacters.
func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandSelectors resolves a list of selectors (exact IDs or glob patterns)
// against the inventory. Exact IDs must exist; glob patterns may match zero items.
// When prevInv is non-nil and an exact-ID selector isn't in the current
// inventory but WAS in prevInv, it's reported as a BrokenRef and skipped
// instead of erroring.
func expandSelectors(packName, label, direction string, selectors, inv []string, invSet map[string]struct{}, prevInv *domain.PackInventory) ([]string, []domain.BrokenRef, error) {
	seen := map[string]struct{}{}
	var out []string
	var broken []domain.BrokenRef
	cat := labelToCategory(label)
	for _, sel := range selectors {
		if isGlobPattern(sel) {
			for _, id := range inv {
				matched, merr := filepath.Match(sel, id)
				if merr != nil {
					return nil, nil, fmt.Errorf("pack %q %s %s: invalid glob %q: %w", packName, label, direction, sel, merr)
				}
				if matched {
					if _, ok := seen[id]; !ok {
						seen[id] = struct{}{}
						out = append(out, id)
					}
				}
			}
		} else {
			if _, ok := invSet[sel]; !ok {
				if prevInv != nil && cat != "" && prevInv.Contains(cat, sel) {
					broken = append(broken, domain.BrokenRef{
						PackName:  packName,
						Category:  cat,
						Direction: direction,
						ID:        sel,
					})
					continue
				}
				return nil, nil, fmt.Errorf("pack %q %s %s references unknown id %q", packName, label, direction, sel)
			}
			if _, ok := seen[sel]; !ok {
				seen[sel] = struct{}{}
				out = append(out, sel)
			}
		}
	}
	slices.Sort(out)
	return out, broken, nil
}

type collisionInfo struct{ kind, id, packA, packB string }

// formatCollisionError builds a single error listing all content collisions
// with actionable remediation YAML the user can paste into their profile.
func formatCollisionError(cc []collisionInfo) error {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%d content collision(s) between packs:\n\n", len(cc))
	for _, c := range cc {
		fmt.Fprintf(&buf, "  %s %q: %s vs %s\n", c.kind, c.id, c.packA, c.packB)
	}

	// Group by the later pack (packB) — the natural override candidate.
	packOrder := []string{}
	byPack := map[string]map[string][]string{} // pack → kind → ids
	for _, c := range cc {
		if _, ok := byPack[c.packB]; !ok {
			byPack[c.packB] = map[string][]string{}
			packOrder = append(packOrder, c.packB)
		}
		byPack[c.packB][c.kind] = append(byPack[c.packB][c.kind], c.id)
	}

	buf.WriteString("\nTo resolve, declare overrides in the winning pack's profile entry.\n")
	buf.WriteString("Either pack can be the winner — add the overrides to whichever version you want to keep.\n")
	buf.WriteString("Example (gives the colliding IDs to the later pack):\n\n")
	for _, pack := range packOrder {
		fmt.Fprintf(&buf, "  - name: %s\n    overrides:\n", pack)
		kinds := byPack[pack]
		kindOrder := make([]string, 0, len(kinds))
		for k := range kinds {
			kindOrder = append(kindOrder, k)
		}
		slices.Sort(kindOrder)
		for _, k := range kindOrder {
			quoted := make([]string, len(kinds[k]))
			for i, id := range kinds[k] {
				quoted[i] = fmt.Sprintf("%q", id)
			}
			fmt.Fprintf(&buf, "      %s: [%s]\n", k, strings.Join(quoted, ", "))
		}
	}
	buf.WriteString("\nOr set defaults.collision_strategy in sync-config.yaml to first-wins or last-wins.")
	buf.WriteString("\nFor rule, agent, workflow, skill, and hook collisions, defaults.namespaced: true preserves both packs by rendering provenance-suffixed names; MCP servers, plugins, and settings keys still need a single winner.")
	return errors.New(buf.String())
}

func normalizeList(items []string) []string {
	set := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := set[v]; ok {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
