package domain

import (
	"path/filepath"
	"strings"
)

const SkillEntryFile = "SKILL.md"

// MatchPrimaryContentFile matches a pack-relative path against the primary
// content file patterns for authored categories (rules, agents, workflows,
// skills). Returns the category, resource ID, and true on match.
func MatchPrimaryContentFile(rel string) (PackCategory, string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	switch PackCategory(parts[0]) {
	case CategoryRules, CategoryAgents, CategoryWorkflows:
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".md") {
			return "", "", false
		}
		return PackCategory(parts[0]), strings.TrimSuffix(parts[1], ".md"), true
	case CategorySkills:
		if len(parts) != 3 || parts[2] != SkillEntryFile {
			return "", "", false
		}
		return CategorySkills, parts[1], true
	default:
		return "", "", false
	}
}
