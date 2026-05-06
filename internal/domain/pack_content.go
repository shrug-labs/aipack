package domain

import (
	"path"
	"path/filepath"
	"strings"
)

const SkillEntryFile = "SKILL.md"

// RuleHarnessSeparator encodes `/` in slashed rule ids at the harness
// filename level. Only rules encode; other content types use leaf ids
// verbatim.
//
// The decode in IDFromHarnessFilename is a plain ReplaceAll, so a rule id
// containing this literal would round-trip ambiguously. Manifest validation
// in `internal/config/pack_inventory.go` rejects authored rule ids
// containing `__` for that reason — the validator and the decode are
// coupled. Don't relax one without the other.
const RuleHarnessSeparator = "__"

// HarnessFilename returns the basename (no extension) for an id of this
// category — encoded for rules, verbatim otherwise.
func (c PackCategory) HarnessFilename(id string) string {
	if c == CategoryRules {
		return strings.ReplaceAll(id, "/", RuleHarnessSeparator)
	}
	return id
}

// IDFromHarnessFilename inverts HarnessFilename.
func (c PackCategory) IDFromHarnessFilename(name string) string {
	if c == CategoryRules {
		return strings.ReplaceAll(name, RuleHarnessSeparator, "/")
	}
	return name
}

// MatchPrimaryContentFile maps a pack-relative path to (category, id, true).
// Rules preserve the slashed path as id (nested authoring is part of the id).
// Agents, workflows, skills, and plugins accept any depth; the id is the leaf
// (the subdirectory is authoring organization, not part of the id).
func MatchPrimaryContentFile(rel string) (PackCategory, string, bool) {
	slashed := filepath.ToSlash(rel)
	idx := strings.IndexByte(slashed, '/')
	if idx <= 0 || idx == len(slashed)-1 {
		return "", "", false
	}
	cat := PackCategory(slashed[:idx])
	rest := slashed[idx+1:]
	switch cat {
	case CategoryRules:
		if !strings.HasSuffix(rest, ".md") {
			return "", "", false
		}
		return cat, strings.TrimSuffix(rest, ".md"), true
	case CategoryAgents, CategoryWorkflows:
		if !strings.HasSuffix(rest, ".md") {
			return "", "", false
		}
		return cat, path.Base(strings.TrimSuffix(rest, ".md")), true
	case CategoryPlugins:
		if !strings.HasSuffix(rest, ".json") {
			return "", "", false
		}
		return cat, path.Base(strings.TrimSuffix(rest, ".json")), true
	case CategorySkills:
		if !strings.HasSuffix(rest, "/"+SkillEntryFile) {
			return "", "", false
		}
		dir := strings.TrimSuffix(rest, "/"+SkillEntryFile)
		if dir == "" {
			return "", "", false
		}
		return CategorySkills, path.Base(dir), true
	default:
		return "", "", false
	}
}
