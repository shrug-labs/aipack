package cline

import (
	"os"
	"path/filepath"
	"runtime"
)

// settingsGlobalPath returns the global cline_mcp_settings.json path using
// the provided home directory. Prefer this over settingsGlobalPathFromEnv.
func settingsGlobalPath(home string) string {
	if home == "" {
		return ""
	}
	suffix := filepath.Join("globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", suffix)
	case "linux":
		return filepath.Join(home, ".config", "Code", "User", suffix)
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Code", "User", suffix)
		}
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", suffix)
	default:
		return filepath.Join(home, ".config", "Code", "User", suffix)
	}
}
