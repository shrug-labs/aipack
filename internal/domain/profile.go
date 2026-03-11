package domain

import (
	"path/filepath"
	"sort"
)

// NewProfile returns a Profile with initialized maps, safe for mutation.
func NewProfile() Profile {
	return Profile{
		Params: map[string]string{},
	}
}

// Profile is a fully resolved profile ready for sync/render.
// Produced by engine.Resolve() from a ProfileConfig.
// All content is parsed, params are expanded, permissions are applied.
type Profile struct {
	Params       map[string]string
	Packs        []Pack
	MCPServers   []MCPServer
	BaseSettings SettingsBundle
	Plugins      PluginsBundle

	// SettingsPack is the name of the single pack that provides base settings
	// for all harnesses. At most one pack per profile can have settings.enabled.
	SettingsPack string
}

// Pack is a resolved pack within a profile, carrying fully-typed content.
type Pack struct {
	Name       string
	Version    string
	Root       string // absolute path to pack directory
	Rules      []Rule
	Agents     []Agent
	Workflows  []Workflow
	Skills     []Skill
	Registries []string // relative paths to embedded registry YAML files
}

// AllRules returns all rules across all packs, in pack order.
func (p Profile) AllRules() []Rule {
	var out []Rule
	for _, pk := range p.Packs {
		out = append(out, pk.Rules...)
	}
	return out
}

// AllAgents returns all agents across all packs, in pack order.
func (p Profile) AllAgents() []Agent {
	var out []Agent
	for _, pk := range p.Packs {
		out = append(out, pk.Agents...)
	}
	return out
}

// AllWorkflows returns all workflows across all packs, in pack order.
func (p Profile) AllWorkflows() []Workflow {
	var out []Workflow
	for _, pk := range p.Packs {
		out = append(out, pk.Workflows...)
	}
	return out
}

// AllSkills returns all skills across all packs, in pack order.
func (p Profile) AllSkills() []Skill {
	var out []Skill
	for _, pk := range p.Packs {
		out = append(out, pk.Skills...)
	}
	return out
}

// SettingsPackName returns the pack name that provides base settings,
// or "" if none is configured. The harness parameter is accepted for
// call-site compatibility but ignored — one pack provides settings for all.
func (p Profile) SettingsPackName(_ Harness) string {
	return p.SettingsPack
}

// HasContent reports whether the profile has any rules, agents, workflows, or skills.
func (p Profile) HasContent() bool {
	for _, pk := range p.Packs {
		if len(pk.Rules) > 0 || len(pk.Agents) > 0 || len(pk.Workflows) > 0 || len(pk.Skills) > 0 {
			return true
		}
	}
	return false
}

// RuleDirs returns sorted, deduplicated rule directories for packs with rules.
func (p Profile) RuleDirs() []string {
	return p.packSubdirs("rules", func(pk Pack) bool { return len(pk.Rules) > 0 })
}

// SkillRoots returns sorted, deduplicated skill directories for packs with skills.
func (p Profile) SkillRoots() []string {
	return p.packSubdirs("skills", func(pk Pack) bool { return len(pk.Skills) > 0 })
}

// packSubdirs collects sorted, deduplicated pack subdirectories matching a predicate.
func (p Profile) packSubdirs(subdir string, include func(Pack) bool) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, pk := range p.Packs {
		if include(pk) {
			d := filepath.Join(pk.Root, subdir)
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}
