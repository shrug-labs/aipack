package codex

import (
	"path/filepath"
)

// SettingsProjectPath returns the config.toml path for a project.
func SettingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".codex", "config.toml")
}

// SettingsGlobalPath returns the global config.toml path (~/.codex/config.toml).
func SettingsGlobalPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// ManagedRootsProject returns all paths managed by Codex in a project.
func ManagedRootsProject(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".agents", "skills"),
		filepath.Join(projectDir, "AGENTS.override.md"),
		SettingsProjectPath(projectDir),
	}
}

// ManagedRootsGlobal returns all paths managed by Codex globally.
func ManagedRootsGlobal(home string) []string {
	codexHome := filepath.Join(home, ".codex")
	return []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(codexHome, "rules"),
		filepath.Join(codexHome, "AGENTS.override.md"),
		SettingsGlobalPath(home),
	}
}
