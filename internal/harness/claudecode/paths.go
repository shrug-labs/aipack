package claudecode

import "github.com/shrug-labs/aipack/internal/domain"

// Paths describes the filesystem locations Claude Code uses for a given scope.
// All paths are relative to the scope's base directory (project dir or $HOME).
type Paths struct {
	RulesDir     string
	AgentsDir    string
	WorkflowsDir string
	SkillsDir    string
	SettingsFile string
	MCPFile      string
}

// ProjectPaths defines Claude Code's project-scope paths (relative to project dir).
var ProjectPaths = Paths{
	RulesDir:     ".claude/rules",
	AgentsDir:    ".claude/agents",
	WorkflowsDir: ".claude/commands",
	SkillsDir:    ".claude/skills",
	SettingsFile: ".claude/settings.local.json",
	MCPFile:      ".mcp.json",
}

// GlobalPaths defines Claude Code's global-scope paths (relative to $HOME).
// Content paths are identical to project scope; SettingsFile and MCPFile differ.
//
// SettingsFile is settings.json, not settings.local.json: Claude Code only
// recognizes settings.local.json at project scope. At user scope the sole
// settings file is ~/.claude/settings.json, so managed hooks and permissions
// written to ~/.claude/settings.local.json would never load. This shares the
// file with enabledPlugins (also ~/.claude/settings.json); planMCPAndSettings
// folds plugins into the managed action to avoid two same-destination merges.
var GlobalPaths = Paths{
	RulesDir:     ".claude/rules",
	AgentsDir:    ".claude/agents",
	WorkflowsDir: ".claude/commands",
	SkillsDir:    ".claude/skills",
	SettingsFile: ".claude/settings.json",
	MCPFile:      ".claude.json",
}

// PathsForScope returns the Paths for the given scope.
func PathsForScope(scope domain.Scope) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	return GlobalPaths
}
