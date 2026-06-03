package opencode

import (
	"github.com/shrug-labs/aipack/internal/domain"
)

// Paths describes the filesystem locations OpenCode uses for a given scope.
// All paths are relative to the scope's base directory (project dir or $HOME).
type Paths struct {
	ConfigBase   string // base config directory (settings + drop-ins deploy here)
	RulesDir     string
	AgentsDir    string
	WorkflowsDir string
	SkillsDir    string
	PluginsDir   string
	SettingsFile string
	AGENTSFile   string // root-level AGENTS.md capture path
}

// BaseSettingsFile is the primary OpenCode config filename.
// Drop-in files in a pack's opencode/ directory are deployed alongside it.
const BaseSettingsFile = "opencode.json"

// ProjectPaths defines OpenCode's project-scope paths (relative to project dir).
var ProjectPaths = Paths{
	ConfigBase:   ".opencode",
	RulesDir:     ".opencode/rules",
	AgentsDir:    ".opencode/agents",
	WorkflowsDir: ".opencode/commands",
	SkillsDir:    ".opencode/skills",
	PluginsDir:   ".opencode/plugins",
	SettingsFile: ".opencode/" + BaseSettingsFile,
	AGENTSFile:   "AGENTS.md",
}

// GlobalPaths defines OpenCode's global-scope paths (relative to $HOME).
var GlobalPaths = Paths{
	ConfigBase:   ".config/opencode",
	RulesDir:     ".config/opencode/rules",
	AgentsDir:    ".config/opencode/agents",
	WorkflowsDir: ".config/opencode/commands",
	SkillsDir:    ".config/opencode/skills",
	PluginsDir:   ".config/opencode/plugins",
	SettingsFile: ".config/opencode/" + BaseSettingsFile,
	AGENTSFile:   ".config/opencode/AGENTS.md",
}

// GlobalConfigDirPaths defines OpenCode's global-scope paths when
// OPENCODE_CONFIG_DIR is set. That variable points at the config directory
// itself, not a home directory that should receive .config/opencode.
var GlobalConfigDirPaths = Paths{
	ConfigBase:   ".",
	RulesDir:     "rules",
	AgentsDir:    "agents",
	WorkflowsDir: "commands",
	SkillsDir:    "skills",
	PluginsDir:   "plugins",
	SettingsFile: BaseSettingsFile,
	AGENTSFile:   "AGENTS.md",
}

// PathsForScope returns the Paths for the given scope.
func PathsForScope(scope domain.Scope, targetConfigDir bool) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	if targetConfigDir {
		return GlobalConfigDirPaths
	}
	return GlobalPaths
}
