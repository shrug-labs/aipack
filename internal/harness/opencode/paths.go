package opencode

import "path/filepath"

// baseSettingsFile is the only hardcoded OpenCode config filename.
// All other config files in a pack's opencode/ directory are treated
// as drop-in files and deployed as-is.
const baseSettingsFile = "opencode.json"

// settingsProjectPath returns the opencode.json path for a project.
func settingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".opencode", baseSettingsFile)
}

// settingsGlobalPath returns the global opencode.json path.
func settingsGlobalPath(home string) string {
	return filepath.Join(home, ".config", "opencode", baseSettingsFile)
}

// configBaseProject returns the OpenCode config base directory for a project.
func configBaseProject(projectDir string) string {
	return filepath.Join(projectDir, ".opencode")
}

// configBaseGlobal returns the global OpenCode config base directory.
func configBaseGlobal(home string) string {
	return filepath.Join(home, ".config", "opencode")
}

