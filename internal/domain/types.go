package domain

import (
	"strings"
)

// Harness identifies a supported AI harness.
type Harness string

const (
	HarnessClaudeCode Harness = "claudecode"
	HarnessOpenCode   Harness = "opencode"
	HarnessCodex      Harness = "codex"
	HarnessCline      Harness = "cline"
)

var allHarnesses = []Harness{HarnessCline, HarnessClaudeCode, HarnessCodex, HarnessOpenCode}

// AllHarnesses returns a copy of the known harness list.
func AllHarnesses() []Harness {
	out := make([]Harness, len(allHarnesses))
	copy(out, allHarnesses)
	return out
}

// HarnessNames returns the string names of all harnesses.
func HarnessNames() []string {
	out := make([]string, len(allHarnesses))
	for i, h := range allHarnesses {
		out[i] = string(h)
	}
	return out
}

// HarnessNamesJoined returns harness names joined by sep.
func HarnessNamesJoined(sep string) string {
	return strings.Join(HarnessNames(), sep)
}

// ParseHarness parses a raw string into a Harness, returning false if unknown.
func ParseHarness(raw string) (Harness, bool) {
	want := strings.ToLower(strings.TrimSpace(raw))
	if want == "" {
		return "", false
	}
	for _, h := range allHarnesses {
		if want == string(h) {
			return h, true
		}
	}
	return "", false
}

// Scope identifies project vs global sync scope.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// ParseScope parses a scope string, returning the scope and true if valid.
func ParseScope(raw string) (Scope, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ScopeProject):
		return ScopeProject, true
	case string(ScopeGlobal):
		return ScopeGlobal, true
	default:
		return "", false
	}
}

// PackCategory classifies a pack directory category.
type PackCategory string

const (
	CategoryRules     PackCategory = "rules"
	CategoryAgents    PackCategory = "agents"
	CategoryWorkflows PackCategory = "workflows"
	CategorySkills    PackCategory = "skills"
	CategoryMCP       PackCategory = "mcp"
	CategorySettings  PackCategory = "settings"
)

// AllPackCategories returns pack content categories in display order.
// Settings is excluded — it is not user-authored pack content but rather
// harness configuration derived from MCP server definitions and settings packs.
func AllPackCategories() []PackCategory {
	return []PackCategory{CategoryRules, CategoryAgents, CategoryWorkflows, CategorySkills, CategoryMCP}
}

// AuthoredCategories returns the subset of categories that have authored
// markdown files with YAML frontmatter (i.e. everything except MCP).
func AuthoredCategories() []PackCategory {
	return []PackCategory{CategoryRules, CategoryAgents, CategoryWorkflows, CategorySkills}
}

// IsAuthored returns true for categories with authored markdown+frontmatter files.
func (c PackCategory) IsAuthored() bool {
	return c == CategoryRules || c == CategoryAgents ||
		c == CategoryWorkflows || c == CategorySkills
}

// DirName returns the directory name for this category within a pack.
func (c PackCategory) DirName() string {
	return string(c)
}

// Ext returns the file extension for content in this category.
func (c PackCategory) Ext() string {
	if c == CategoryMCP {
		return ".json"
	}
	return ".md"
}

// PrimaryRelPath returns the relative path to the primary file for an item
// in this category (e.g. "rules/triage.md", "skills/oncall/SKILL.md").
func (c PackCategory) PrimaryRelPath(id string) string {
	if c == CategorySkills {
		return c.DirName() + "/" + id + "/" + SkillEntryFile
	}
	return c.DirName() + "/" + id + c.Ext()
}

// Label returns a human-readable plural display label for the category.
func (c PackCategory) Label() string {
	switch c {
	case CategoryRules:
		return "Rules"
	case CategoryAgents:
		return "Agents"
	case CategoryWorkflows:
		return "Workflows"
	case CategorySkills:
		return "Skills"
	case CategoryMCP:
		return "MCP Servers"
	case CategorySettings:
		return "Settings"
	}
	return string(c)
}

// SingularLabel returns a human-readable singular label for the category.
func (c PackCategory) SingularLabel() string {
	switch c {
	case CategoryRules:
		return "Rule"
	case CategoryAgents:
		return "Agent"
	case CategoryWorkflows:
		return "Workflow"
	case CategorySkills:
		return "Skill"
	case CategoryMCP:
		return "MCP Server"
	case CategorySettings:
		return "Setting"
	}
	return string(c)
}

// ParseSingularLabel parses a lowercase singular label (e.g. "rule", "agent")
// into a PackCategory. Returns the category and true if recognized, or the
// zero value and false if unrecognized.
func ParseSingularLabel(s string) (PackCategory, bool) {
	switch s {
	case "rule":
		return CategoryRules, true
	case "agent":
		return CategoryAgents, true
	case "workflow":
		return CategoryWorkflows, true
	case "skill":
		return CategorySkills, true
	case "mcp":
		return CategoryMCP, true
	case "setting":
		return CategorySettings, true
	}
	return "", false
}

// CopyKind distinguishes file from directory copy actions.
type CopyKind string

const (
	CopyKindFile CopyKind = "file"
	CopyKindDir  CopyKind = "dir"
)

// DiffKind classifies a file against on-disk state.
type DiffKind string

const (
	DiffCreate    DiffKind = "create"    // file doesn't exist on disk
	DiffIdentical DiffKind = "identical" // desired == on-disk
	DiffManaged   DiffKind = "managed"   // on-disk matches ledger digest (safe to update)
	DiffConflict  DiffKind = "conflict"  // on-disk modified by user since last sync
	DiffUntracked DiffKind = "untracked" // exists on disk but not in ledger
	DiffError     DiffKind = "error"     // classification failed (e.g. permission error)
)
