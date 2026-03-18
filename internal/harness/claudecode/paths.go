package claudecode

import "path/filepath"

// settingsProjectPath returns the settings.local.json path for a project.
func settingsProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "settings.local.json")
}

// mcpProjectPath returns the .mcp.json path for a project.
func mcpProjectPath(projectDir string) string {
	return filepath.Join(projectDir, ".mcp.json")
}

// mcpGlobalPath returns the global MCP config path (~/.claude.json).
// Claude Code reads global MCP config from ~/.claude.json,
// not from ~/.mcp.json or ~/.claude/.mcp.json.
func mcpGlobalPath(home string) string {
	return filepath.Join(home, ".claude.json")
}

// settingsGlobalPath returns the global settings.local.json path.
func settingsGlobalPath(home string) string {
	return filepath.Join(home, ".claude", "settings.local.json")
}

