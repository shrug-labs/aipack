package cline

import (
	"os"
	"path/filepath"
	"runtime"
)

// RulesGlobalDir returns the global rules directory for Cline.
func RulesGlobalDir(home string) string {
	return filepath.Join(home, "Documents", "Cline", "Rules", "aipack")
}

func AgentsGlobalDir(home string) string {
	return filepath.Join(home, "Documents", "Cline", "Agents", "aipack")
}

// WorkflowsGlobalDir returns the global workflows directory for Cline.
func WorkflowsGlobalDir(home string) string {
	return filepath.Join(home, "Documents", "Cline", "Workflows", "aipack")
}

// SettingsGlobalPath returns the global cline_mcp_settings.json path using
// the provided home directory. Prefer this over SettingsGlobalPathFromEnv.
func SettingsGlobalPath(home string) string {
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

// ManagedRootsProject returns all paths managed by Cline in a project.
func ManagedRootsProject(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".clinerules"),
		filepath.Join(projectDir, ".clinerules", "agents"),
		filepath.Join(projectDir, ".clinerules", "skills"),
	}
}

// ManagedRootsGlobal returns all paths managed by Cline globally.
func ManagedRootsGlobal(home string) []string {
	out := []string{
		filepath.Join(home, ".cline", "skills"),
		RulesGlobalDir(home),
		AgentsGlobalDir(home),
		WorkflowsGlobalDir(home),
	}
	if p := SettingsGlobalPath(home); filepath.Clean(p) != "." && p != "" {
		out = append(out, p)
	}
	return out
}

// StrictExtraDirsProject returns extra directories to check in strict mode.
func StrictExtraDirsProject(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".clinerules"),
		filepath.Join(projectDir, ".clinerules", "agents"),
		filepath.Join(projectDir, ".clinerules", "workflows"),
	}
}
