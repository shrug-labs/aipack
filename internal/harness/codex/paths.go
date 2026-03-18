package codex

import "github.com/shrug-labs/aipack/internal/domain"

// Paths describes the filesystem locations Codex uses for a given scope.
// All paths are relative to the scope's base directory (project dir or $HOME).
// Codex does not support rules, agents, or workflows as separate content —
// rules are flattened into AGENTS.override.md during Plan.
type Paths struct {
	SkillsDir    string
	OverrideFile string
	SettingsFile string
}

// ProjectPaths defines Codex's project-scope paths (relative to project dir).
var ProjectPaths = Paths{
	SkillsDir:    ".agents/skills",
	OverrideFile: "AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
}

// GlobalPaths defines Codex's global-scope paths (relative to $HOME).
var GlobalPaths = Paths{
	SkillsDir:    ".agents/skills",
	OverrideFile: ".codex/AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
}

// PathsForScope returns the Paths for the given scope.
func PathsForScope(scope domain.Scope) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	return GlobalPaths
}
