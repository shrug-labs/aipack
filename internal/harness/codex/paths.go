package codex

import (
	"github.com/shrug-labs/aipack/internal/domain"
)

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
	SkillsDir:    ".codex/skills",
	AgentsDir:    ".codex/agents",
	OverrideFile: "AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
	HooksFile:    ".codex/hooks.json",
}

// GlobalPaths defines Codex's global-scope paths (relative to $HOME).
var GlobalPaths = Paths{
	SkillsDir:    ".codex/skills",
	AgentsDir:    ".codex/agents",
	OverrideFile: ".codex/AGENTS.override.md",
	SettingsFile: ".codex/config.toml",
	HooksFile:    ".codex/hooks.json",
}

// GlobalCodexHomePaths defines Codex's global-scope paths when CODEX_HOME is
// set. CODEX_HOME points at the Codex config directory itself, not a home
// directory that should receive an extra .codex suffix.
var GlobalCodexHomePaths = Paths{
	SkillsDir:    "skills",
	AgentsDir:    "agents",
	OverrideFile: "AGENTS.override.md",
	SettingsFile: "config.toml",
	HooksFile:    "hooks.json",
}

// PathsForScope returns the Paths for the given scope.
func PathsForScope(scope domain.Scope, targetConfigDir bool) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	if targetConfigDir {
		return GlobalCodexHomePaths
	}
	return GlobalPaths
}

// LegacySkillsDir is the old Codex shared skill root. It is kept only for
// stale cleanup of ledger-managed files; current Codex renders use .codex/skills.
const LegacySkillsDir = ".agents/skills"
