package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// addPromotedWorkflows converts workflows to SKILL.md WriteActions under the
// given subDir (e.g. ".agents/skills"). Each workflow becomes a skill directory
// with a generated SKILL.md containing frontmatter + the workflow body.
func addPromotedWorkflows(f *domain.Fragment, baseDir, subDir string, workflows []domain.Workflow) {
	for _, w := range workflows {
		body := strings.TrimSpace(string(w.Body))
		if body == "" {
			continue
		}
		name := w.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(w.SourcePath), ".md")
		}
		desc := w.Frontmatter.Description
		if desc == "" {
			desc = fmt.Sprintf("Workflow: %s", name)
		}
		content := buildSkillMD(name, desc, body)
		skillDir := filepath.Join(baseDir, subDir, name)
		dst := filepath.Join(skillDir, "SKILL.md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    []byte(content),
			SourcePack: w.SourcePack,
			Src:        w.SourcePath,
		})
		f.Desired = append(f.Desired, skillDir)
	}
}

// addPromotedAgents converts agents to SKILL.md WriteActions under the given
// subDir. Each agent becomes a skill directory with a generated SKILL.md.
func addPromotedAgents(f *domain.Fragment, baseDir, subDir string, agents []domain.Agent) {
	for _, a := range agents {
		body := strings.TrimSpace(string(a.Body))
		if body == "" {
			continue
		}
		name := a.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(a.SourcePath), ".md")
		}
		desc := a.Frontmatter.Description
		if desc == "" {
			desc = fmt.Sprintf("Agent: %s", name)
		}
		content := buildSkillMD(name, desc, body)
		skillDir := filepath.Join(baseDir, subDir, name)
		dst := filepath.Join(skillDir, "SKILL.md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    []byte(content),
			SourcePack: a.SourcePack,
			Src:        a.SourcePath,
		})
		f.Desired = append(f.Desired, skillDir)
	}
}

// buildSkillMD generates SKILL.md content with YAML frontmatter.
func buildSkillMD(name, description, body string) string {
	var buf strings.Builder
	buf.WriteString("---\n")
	buf.WriteString("name: ")
	buf.WriteString(name)
	buf.WriteString("\n")
	buf.WriteString("description: ")
	buf.WriteString(yamlQuote(description))
	buf.WriteString("\n")
	buf.WriteString("---\n\n")
	buf.WriteString(body)
	buf.WriteString("\n")
	return buf.String()
}

// yamlQuote wraps a string in double quotes if it contains characters that
// could break plain YAML scalars (colons, quotes, leading/trailing whitespace).
func yamlQuote(s string) string {
	if strings.ContainsAny(s, ":#\"'{}[]|>&*!%@`") ||
		s != strings.TrimSpace(s) {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}
