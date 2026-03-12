package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	capRules     = "rules"
	capAgents    = "agents"
	capWorkflows = "workflows"
	capSkills    = "skills"
	capMCP       = "mcp"
)

type ResolvedPack struct {
	Name      string
	Root      string
	Manifest  PackManifest
	Rules     []string
	Agents    []string
	Workflows []string
	Skills    []string
	MCP       map[string]ResolvedMCPServer
}

type ResolvedMCPServer struct {
	AllowedTools  []string
	DisabledTools []string
}

func ResolveProfile(cfg ProfileConfig, profilePath string, configDir string) ([]ResolvedPack, string, error) {
	if len(cfg.Packs) == 0 {
		return nil, "", errors.New("profile packs must be configured")
	}

	var packs []ResolvedPack
	var settingsPack string
	seenRules := map[string]string{}
	seenAgents := map[string]string{}
	seenWorkflows := map[string]string{}
	seenSkills := map[string]string{}
	seenServers := map[string]string{}

	for _, packCfg := range cfg.Packs {
		packName := strings.TrimSpace(packCfg.Name)
		if packName == "" {
			return nil, "", errors.New("profile packs entries must have name")
		}
		if !defaultTrue(packCfg.Enabled) {
			continue
		}

		manifestPath := filepath.Join(configDir, "packs", packName, "pack.json")
		manifest, err := LoadPackManifest(manifestPath)
		if err != nil {
			return nil, "", fmt.Errorf("pack %q manifest: %w", packName, err)
		}
		packRoot := ResolvePackRoot(manifestPath, manifest.Root)
		if packRoot == "" {
			return nil, "", fmt.Errorf("pack %q root could not be resolved", packName)
		}
		if err := DiscoverContent(&manifest, packRoot); err != nil {
			return nil, "", fmt.Errorf("pack %q content discovery: %w", packName, err)
		}
		if err := validatePackInventory(packName, packRoot, manifest); err != nil {
			return nil, "", err
		}

		rules, err := resolveVector(packName, capRules, manifest.Rules, packCfg.Rules)
		if err != nil {
			return nil, "", err
		}
		agents, err := resolveVector(packName, capAgents, manifest.Agents, packCfg.Agents)
		if err != nil {
			return nil, "", err
		}
		workflows, err := resolveVector(packName, capWorkflows, manifest.Workflows, packCfg.Workflows)
		if err != nil {
			return nil, "", err
		}
		skills, err := resolveVector(packName, capSkills, manifest.Skills, packCfg.Skills)
		if err != nil {
			return nil, "", err
		}

		packResolved := ResolvedPack{
			Name:      packName,
			Root:      packRoot,
			Manifest:  manifest,
			Rules:     rules,
			Agents:    agents,
			Workflows: workflows,
			Skills:    skills,
			MCP:       map[string]ResolvedMCPServer{},
		}

		overrideRules := toSet(packCfg.Overrides.Rules)
		overrideAgents := toSet(packCfg.Overrides.Agents)
		overrideWorkflows := toSet(packCfg.Overrides.Workflows)
		overrideSkills := toSet(packCfg.Overrides.Skills)
		overrideMCP := toSet(packCfg.Overrides.MCP)

		if err := validateOverrides(packName, capRules, overrideRules, seenRules); err != nil {
			return nil, "", err
		}
		if err := validateOverrides(packName, capAgents, overrideAgents, seenAgents); err != nil {
			return nil, "", err
		}
		if err := validateOverrides(packName, capWorkflows, overrideWorkflows, seenWorkflows); err != nil {
			return nil, "", err
		}
		if err := validateOverrides(packName, capSkills, overrideSkills, seenSkills); err != nil {
			return nil, "", err
		}
		if err := validateOverrides(packName, capMCP, overrideMCP, seenServers); err != nil {
			return nil, "", err
		}

		for _, id := range rules {
			if prev, ok := seenRules[id]; ok {
				if _, allowed := overrideRules[id]; !allowed {
					return nil, "", fmt.Errorf("rules id %q appears in both %q and %q (add to packs[%s].overrides.rules to override)", id, prev, packName, packName)
				}
			}
			seenRules[id] = packName
		}
		for _, id := range agents {
			if prev, ok := seenAgents[id]; ok {
				if _, allowed := overrideAgents[id]; !allowed {
					return nil, "", fmt.Errorf("agents id %q appears in both %q and %q (add to packs[%s].overrides.agents to override)", id, prev, packName, packName)
				}
			}
			seenAgents[id] = packName
		}
		for _, id := range workflows {
			if prev, ok := seenWorkflows[id]; ok {
				if _, allowed := overrideWorkflows[id]; !allowed {
					return nil, "", fmt.Errorf("workflows id %q appears in both %q and %q (add to packs[%s].overrides.workflows to override)", id, prev, packName, packName)
				}
			}
			seenWorkflows[id] = packName
		}
		for _, id := range skills {
			if prev, ok := seenSkills[id]; ok {
				if _, allowed := overrideSkills[id]; !allowed {
					return nil, "", fmt.Errorf("skills id %q appears in both %q and %q (add to packs[%s].overrides.skills to override)", id, prev, packName, packName)
				}
			}
			seenSkills[id] = packName
		}

		mcpSelection := packCfg.MCP
		if len(mcpSelection) == 0 {
			mcpSelection = map[string]MCPServerConfig{}
			for name := range manifest.MCP.Servers {
				mcpSelection[name] = MCPServerConfig{Enabled: BoolPtr(true)}
			}
		}
		for name, serverCfg := range mcpSelection {
			if _, ok := manifest.MCP.Servers[name]; !ok {
				return nil, "", fmt.Errorf("pack %q references unknown mcp server %q", packName, name)
			}
			if !defaultTrue(serverCfg.Enabled) {
				continue
			}
			tools := serverCfg.AllowedTools
			if len(tools) == 0 {
				tools = manifest.MCP.Servers[name].DefaultAllowedTools
			}
			entry := ResolvedMCPServer{
				AllowedTools:  normalizeList(tools),
				DisabledTools: normalizeList(serverCfg.DisabledTools),
			}
			packResolved.MCP[name] = entry
			if prev, ok := seenServers[name]; ok {
				if _, allowed := overrideMCP[name]; !allowed {
					return nil, "", fmt.Errorf("mcp server %q appears in both %q and %q (add to packs[%s].overrides.mcp to override)", name, prev, packName, packName)
				}
			}
			seenServers[name] = packName
		}

		if settingsEnabled(packCfg.Settings.Enabled) {
			if settingsPack != "" {
				return nil, "", fmt.Errorf("multiple packs have settings.enabled: %q and %q; only one is allowed per profile", settingsPack, packName)
			}
			settingsPack = packName
		}

		packs = append(packs, packResolved)
	}
	if len(packs) == 0 {
		return nil, "", errors.New("no enabled packs in profile")
	}
	return packs, settingsPack, nil
}

func toSet(items []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		set[v] = struct{}{}
	}
	return set
}

func validateOverrides(packName string, label string, overrides map[string]struct{}, seen map[string]string) error {
	for id := range overrides {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("pack %q overrides.%s references unknown id %q", packName, label, id)
		}
	}
	return nil
}

func defaultTrue(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func settingsEnabled(v *bool) bool {
	return v != nil && *v
}

func resolveVector(packName string, label string, inventory []string, sel VectorSelector) ([]string, error) {
	if sel.Include != nil && sel.Exclude != nil {
		return nil, fmt.Errorf("pack %q %s cannot set both include and exclude", packName, label)
	}
	inv := normalizeList(inventory)
	invSet := map[string]struct{}{}
	for _, v := range inv {
		invSet[v] = struct{}{}
	}
	if sel.Include != nil {
		include := normalizeList(*sel.Include)
		if len(include) == 0 {
			return inv, nil
		}
		return expandSelectors(packName, label, "include", include, inv, invSet)
	}
	if sel.Exclude == nil {
		return inv, nil
	}
	exclude := normalizeList(*sel.Exclude)
	matched, err := expandSelectors(packName, label, "exclude", exclude, inv, invSet)
	if err != nil {
		return nil, err
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
	return out, nil
}

// isGlobPattern reports whether s contains glob metacharacters.
func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandSelectors resolves a list of selectors (exact IDs or glob patterns)
// against the inventory. Exact IDs must exist; glob patterns may match zero items.
func expandSelectors(packName, label, direction string, selectors, inv []string, invSet map[string]struct{}) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, sel := range selectors {
		if isGlobPattern(sel) {
			for _, id := range inv {
				matched, merr := filepath.Match(sel, id)
				if merr != nil {
					return nil, fmt.Errorf("pack %q %s %s: invalid glob %q: %w", packName, label, direction, sel, merr)
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
				return nil, fmt.Errorf("pack %q %s %s references unknown id %q", packName, label, direction, sel)
			}
			if _, ok := seen[sel]; !ok {
				seen[sel] = struct{}{}
				out = append(out, sel)
			}
		}
	}
	sort.Strings(out)
	return out, nil
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
	sort.Strings(out)
	return out
}
