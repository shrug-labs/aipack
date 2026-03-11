package domain

import (
	"path/filepath"
	"strings"
)

type AuthoredContentKind string

const (
	ContentRules     AuthoredContentKind = "rules"
	ContentAgents    AuthoredContentKind = "agents"
	ContentWorkflows AuthoredContentKind = "workflows"
	ContentSkills    AuthoredContentKind = "skills"
	SkillEntryFile                       = "SKILL.md"
)

func AuthoredContentKinds() []AuthoredContentKind {
	return []AuthoredContentKind{ContentRules, ContentAgents, ContentWorkflows, ContentSkills}
}

func (k AuthoredContentKind) DirName() string {
	return string(k)
}

func (k AuthoredContentKind) PrimaryRelPath(id string) string {
	switch k {
	case ContentSkills:
		return filepath.ToSlash(filepath.Join(k.DirName(), id, SkillEntryFile))
	default:
		return filepath.ToSlash(filepath.Join(k.DirName(), id+".md"))
	}
}

func MatchPrimaryContentFile(rel string) (AuthoredContentKind, string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	switch AuthoredContentKind(parts[0]) {
	case ContentRules, ContentAgents, ContentWorkflows:
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".md") {
			return "", "", false
		}
		return AuthoredContentKind(parts[0]), strings.TrimSuffix(parts[1], ".md"), true
	case ContentSkills:
		if len(parts) != 3 || parts[2] != SkillEntryFile {
			return "", "", false
		}
		return ContentSkills, parts[1], true
	default:
		return "", "", false
	}
}
