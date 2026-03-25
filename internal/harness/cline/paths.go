package cline

import (
	"path/filepath"
	"runtime"

	"github.com/shrug-labs/aipack/internal/domain"
)

// Paths describes the filesystem locations Cline uses for a given scope.
// All paths are relative to the scope's base directory (project dir or $HOME).
// Cline does not support agents as separate content — they are promoted to skills.
type Paths struct {
	RulesDir     string
	WorkflowsDir string
	SkillsDir    string
}

// ProjectPaths defines Cline's project-scope paths (relative to project dir).
// SkillsDir uses .agents/skills — shared with Codex — because Cline reads
// both .clinerules/ and .agents/ natively. Using a single canonical skills
// location prevents duplication when multiple harnesses target the same project.
var ProjectPaths = Paths{
	RulesDir:     ".clinerules",
	WorkflowsDir: ".clinerules/workflows",
	SkillsDir:    ".agents/skills",
}

// GlobalPathsFor returns Cline's global-scope paths rooted at home.
// On Windows the Documents folder may be redirected (e.g. OneDrive), so the
// actual location is resolved via the shell API rather than hardcoded.
func GlobalPathsFor(home string) Paths {
	docs := documentsDir(home)
	return Paths{
		RulesDir:     filepath.Join(docs, "Cline", "Rules"),
		WorkflowsDir: filepath.Join(docs, "Cline", "Workflows"),
		SkillsDir:    filepath.Join(home, ".agents", "skills"),
	}
}

// PathsForScope returns the Paths for the given scope.
// For global scope the returned paths are absolute.
func PathsForScope(scope domain.Scope, home string) Paths {
	if scope == domain.ScopeProject {
		return ProjectPaths
	}
	return GlobalPathsFor(home)
}

// MCPSettingsPath returns the global cline_mcp_settings.json path.
// This is platform-dependent and cannot be a constant.
// Cline MCP settings are always global, even in project scope.
func MCPSettingsPath(home string) string {
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
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", suffix)
	default:
		return filepath.Join(home, ".config", "Code", "User", suffix)
	}
}

func mcpMirrorSettingsPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json")
}

func mcpSettingsPaths(home string) []string {
	paths := []string{MCPSettingsPath(home), mcpMirrorSettingsPath(home)}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		clean := filepath.Clean(p)
		if clean == "." {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
