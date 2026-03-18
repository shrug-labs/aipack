package codex

import (
	"path/filepath"
)

// settingsProjectPath returns the config.toml path for a project.
func settingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".codex", "config.toml")
}

// settingsGlobalPath returns the global config.toml path (~/.codex/config.toml).
func settingsGlobalPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

