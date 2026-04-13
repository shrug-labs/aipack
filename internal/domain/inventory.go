package domain

import (
	"slices"
	"sort"
	"time"
)

// PackInventory records an installed pack's resolved content at the time
// of the last successful sync. Pack-level, profile-independent. Consumed
// by sync-time drift detection. Rules, agents, and workflows are tracked
// by ID only — their drift signal is presence/absence. Skills and MCP
// servers carry richer per-item snapshots because they ship more than a
// name (skills have descriptions and bundled assets; servers have
// required refs and tool sets).
type PackInventory struct {
	InventoriedAt time.Time `yaml:"inventoried_at"`
	// CapturedAtRef is the installed pack ref at the moment this inventory
	// was recorded (e.g. "v1.9.0" or a commit hash). Used by drift output
	// so the header can show the version transition when the user moves a
	// pin and then runs sync.
	CapturedAtRef string                       `yaml:"captured_at_ref,omitempty"`
	Rules         []string                     `yaml:"rules,omitempty"`
	Agents        []string                     `yaml:"agents,omitempty"`
	Workflows     []string                     `yaml:"workflows,omitempty"`
	Skills        map[string]SkillSnapshot     `yaml:"skills,omitempty"`
	MCPServers    map[string]MCPServerSnapshot `yaml:"mcp_servers,omitempty"`
}

// SkillSnapshot captures what a skill ships: its description (from
// SKILL.md frontmatter, which is the load-bearing "when does this skill
// apply" signal) and the sorted list of bundled assets — every file in
// the skill directory other than SKILL.md itself, relative to the skill
// directory. Drift detection surfaces description changes and added or
// removed assets.
type SkillSnapshot struct {
	Description string   `yaml:"description,omitempty"`
	Assets      []string `yaml:"assets,omitempty"`
}

// SkillChange is the per-skill diff for a skill that exists in both the
// previous and current inventory but has differences worth surfacing.
type SkillChange struct {
	OldDescription string
	NewDescription string
	AddedAssets    []string
	RemovedAssets  []string
}

// Empty reports whether the change has any content.
func (c SkillChange) Empty() bool {
	return c.OldDescription == c.NewDescription &&
		len(c.AddedAssets) == 0 && len(c.RemovedAssets) == 0
}

// MCPServerSnapshot captures the drift-relevant state of an MCP server as
// declared by the pack:
//   - AvailableTools: the full tool surface the server exposes (from the
//     server's own inventory JSON).
//   - RequiredRefs: the {params.*} and {env:*} references the raw server
//     strings make — both must resolve at render time.
//   - DefaultAllowedTools: the pack author's recommended default allowed
//     tools list from MCPDefaults, distinct from the profile-resolved
//     AllowedTools on domain.MCPServer.
type MCPServerSnapshot struct {
	AvailableTools      []string      `yaml:"available_tools,omitempty"`
	RequiredRefs        []RequiredRef `yaml:"required_refs,omitempty"`
	DefaultAllowedTools []string      `yaml:"default_allowed_tools,omitempty"`
}

// MCPServerChange is the per-server diff for a server that exists in both
// the previous and current inventory but has differences worth surfacing.
type MCPServerChange struct {
	AddedTools   []string
	RemovedTools []string
	AddedRefs    []RequiredRef
	RemovedRefs  []RequiredRef
}

// Empty reports whether the change has any content.
func (c MCPServerChange) Empty() bool {
	return len(c.AddedTools) == 0 && len(c.RemovedTools) == 0 &&
		len(c.AddedRefs) == 0 && len(c.RemovedRefs) == 0
}

// Contains reports whether the inventory has an item with the given ID in
// the given category. Used by the resolver to distinguish drifted-out
// references (was there, now isn't) from typos (never was there).
func (inv PackInventory) Contains(category PackCategory, id string) bool {
	switch category {
	case CategoryRules:
		return slices.Contains(inv.Rules, id)
	case CategoryAgents:
		return slices.Contains(inv.Agents, id)
	case CategoryWorkflows:
		return slices.Contains(inv.Workflows, id)
	case CategorySkills:
		_, ok := inv.Skills[id]
		return ok
	case CategoryMCP:
		_, ok := inv.MCPServers[id]
		return ok
	}
	return false
}

// InventoryDiff is the per-pack result of comparing two inventory snapshots.
type InventoryDiff struct {
	PackName         string
	AddedRules       []string
	RemovedRules     []string
	AddedAgents      []string
	RemovedAgents    []string
	AddedWorkflows   []string
	RemovedWorkflows []string
	AddedSkills      map[string]SkillSnapshot
	RemovedSkills    []string
	ChangedSkills    map[string]SkillChange
	AddedServers     map[string]MCPServerSnapshot
	RemovedServers   []string
	ChangedServers   map[string]MCPServerChange
}

// Empty reports whether the diff contains any changes.
func (d InventoryDiff) Empty() bool {
	return len(d.AddedRules) == 0 && len(d.RemovedRules) == 0 &&
		len(d.AddedAgents) == 0 && len(d.RemovedAgents) == 0 &&
		len(d.AddedWorkflows) == 0 && len(d.RemovedWorkflows) == 0 &&
		len(d.AddedSkills) == 0 && len(d.RemovedSkills) == 0 &&
		len(d.ChangedSkills) == 0 &&
		len(d.AddedServers) == 0 && len(d.RemovedServers) == 0 &&
		len(d.ChangedServers) == 0
}

// BrokenRef is a profile reference that was in a previous pack inventory but
// isn't in the current one — a pack removed content the profile references.
// Emitted by the resolver, surfaced in sync drift output.
type BrokenRef struct {
	PackName  string
	Category  PackCategory
	Direction string // "include", "exclude", "overrides", or "" for MCP entries
	ID        string
}

// SortedCopy returns a sorted copy without mutating the input.
func SortedCopy(xs []string) []string {
	out := make([]string, len(xs))
	copy(out, xs)
	sort.Strings(out)
	return out
}
