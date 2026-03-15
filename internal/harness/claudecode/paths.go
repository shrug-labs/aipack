package claudecode

import "path/filepath"

const managedFile = "CLAUDE.managed.md"

// SettingsProjectPath returns the settings.local.json path for a project.
func SettingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "settings.local.json")
}

// MCPProjectPath returns the .mcp.json path for a project.
func MCPProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".mcp.json")
}

// MCPGlobalPath returns the global MCP config path (~/.claude.json).
// Claude Code reads global MCP config from ~/.claude.json,
// not from ~/.mcp.json or ~/.claude/.mcp.json.
func MCPGlobalPath(home string) string {
	return filepath.Join(home, ".claude.json")
}

// SettingsGlobalPath returns the global settings.local.json path.
func SettingsGlobalPath(home string) string {
	return filepath.Join(home, ".claude", "settings.local.json")
}

// ManagedMdPath returns the legacy CLAUDE.managed.md path. Kept for prune
// migration — new syncs no longer write this file.
func ManagedMdPath(projectDir string) string {
	return filepath.Join(projectDir, ".claude", managedFile)
}

// ManagedRootsProject returns all paths managed by Claude Code in a project.
func ManagedRootsProject(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".claude", "rules"),
		filepath.Join(projectDir, ".claude", "agents"),
		filepath.Join(projectDir, ".claude", "skills"),
		filepath.Join(projectDir, ".claude", "commands"),
		ManagedMdPath(projectDir),
		MCPProjectPath(projectDir),
		SettingsProjectPath(projectDir),
	}
}

// ManagedRootsGlobal returns all paths managed by Claude Code globally.
func ManagedRootsGlobal(home string) []string {
	base := filepath.Join(home, ".claude")
	return []string{
		filepath.Join(base, "rules"),
		filepath.Join(base, "agents"),
		filepath.Join(base, "skills"),
		filepath.Join(base, "commands"),
		MCPGlobalPath(home),
		SettingsGlobalPath(home),
	}
}
