package opencode

import "path/filepath"

// SettingsProjectPaths returns the opencode.json and oh-my-opencode.json paths for a project.
func SettingsProjectPaths(projectDir string) (string, string) {
	base := filepath.Join(projectDir, ".opencode")
	return filepath.Join(base, "opencode.json"), filepath.Join(base, "oh-my-opencode.json")
}

// SettingsGlobalPaths returns the global opencode.json and oh-my-opencode.json paths.
func SettingsGlobalPaths(home string) (string, string) {
	base := filepath.Join(home, ".config", "opencode")
	return filepath.Join(base, "opencode.json"), filepath.Join(base, "oh-my-opencode.json")
}

// ManagedRootsProject returns all paths managed by OpenCode in a project.
func ManagedRootsProject(projectDir string) []string {
	configPath, omoPath := SettingsProjectPaths(projectDir)
	return []string{
		filepath.Join(projectDir, ".opencode", "agents"),
		filepath.Join(projectDir, ".opencode", "commands"),
		filepath.Join(projectDir, ".opencode", "rules"),
		filepath.Join(projectDir, ".opencode", "skills"),
		configPath,
		omoPath,
	}
}

// ManagedRootsGlobal returns all paths managed by OpenCode globally.
func ManagedRootsGlobal(home string) []string {
	configPath, omoPath := SettingsGlobalPaths(home)
	base := filepath.Join(home, ".config", "opencode")
	return []string{
		filepath.Join(base, "agents"),
		filepath.Join(base, "commands"),
		filepath.Join(base, "rules"),
		filepath.Join(base, "skills"),
		configPath,
		omoPath,
	}
}

// StrictExtraDirsProject returns extra directories to check in strict mode.
func StrictExtraDirsProject(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".opencode", "agents"),
		filepath.Join(projectDir, ".opencode", "commands"),
		filepath.Join(projectDir, ".opencode", "rules"),
		filepath.Join(projectDir, ".opencode", "skills"),
	}
}
