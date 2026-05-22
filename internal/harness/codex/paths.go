package codex

import "github.com/shrug-labs/aipack/internal/domain"

// Paths describes the filesystem locations Codex uses for a given scope.
// All paths are relative to the scope's base directory (project dir or $HOME).
// Rules are flattened into AGENTS.override.md during Plan. Agents are rendered
// as native TOML files under AgentsDir.
type Paths struct {
	SkillsDir    string
	AgentsDir    string
	OverrideFile string
	SettingsFile string
	HooksFile    string
}

// ProjectPaths defines Codex's project-scope paths (relative to project dir).
var ProjectPaths = Paths{
	SkillsDir:    ".agents/skills",
	AgentsDir:    ".codex/agents",
	OverrideFile: "AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
	HooksFile:    ".codex/hooks.json",
}

// GlobalPaths defines Codex's global-scope paths (relative to $HOME).
var GlobalPaths = Paths{
	SkillsDir:    ".agents/skills",
	AgentsDir:    ".codex/agents",
	OverrideFile: ".codex/AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
	HooksFile:    ".codex/hooks.json",
}

// PathsForScope returns the Paths for the given scope.
func PathsForScope(scope domain.Scope) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	return GlobalPaths
}
