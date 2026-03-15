package opencode

import "path/filepath"

// baseSettingsFile is the only hardcoded OpenCode config filename.
// All other config files in a pack's opencode/ directory are treated
// as drop-in files and deployed as-is.
const baseSettingsFile = "opencode.json"

// SettingsProjectPath returns the opencode.json path for a project.
func SettingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".opencode", baseSettingsFile)
}

// SettingsGlobalPath returns the global opencode.json path.
func SettingsGlobalPath(home string) string {
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

// ManagedRootsProject returns all paths managed by OpenCode in a project.
// Includes the config base directory to cover drop-in settings files
// (e.g., oh-my-opencode.json) deployed alongside the base settings file.
func ManagedRootsProject(projectDir string) []string {
	base := configBaseProject(projectDir)
	return []string{
		base,
		filepath.Join(base, "agents"),
		filepath.Join(base, "commands"),
		filepath.Join(base, "rules"),
		filepath.Join(base, "skills"),
	}
}

// ManagedRootsGlobal returns all paths managed by OpenCode globally.
func ManagedRootsGlobal(home string) []string {
	base := configBaseGlobal(home)
	return []string{
		base,
		filepath.Join(base, "agents"),
		filepath.Join(base, "commands"),
		filepath.Join(base, "rules"),
		filepath.Join(base, "skills"),
	}
}

// StrictExtraDirsProject returns extra directories to check in strict mode.
func StrictExtraDirsProject(projectDir string) []string {
	base := configBaseProject(projectDir)
	return []string{
		filepath.Join(base, "agents"),
		filepath.Join(base, "commands"),
		filepath.Join(base, "rules"),
		filepath.Join(base, "skills"),
	}
}
