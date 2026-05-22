package cline

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// addPromotedAgents converts agents to SKILL.md WriteActions under skillsDir.
// Each agent becomes a skill directory with a generated SKILL.md containing
// enriched frontmatter that preserves agent metadata for round-trip capture.
func addPromotedAgents(f *domain.Fragment, skillsDir string, namespaced bool, agents []domain.Agent, skills []domain.Skill) error {
	skillRefs := harness.NewSkillRefResolver(skills, namespaced)
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
		if !strings.HasPrefix(desc, harness.DescPrefixAgent) {
			desc = harness.DescPrefixAgent + desc
		}
		renderedName := harness.ContentName(namespaced, a.SourcePack, name)
		renderedSkills, err := skillRefs.Render(a.SourcePack, a.Frontmatter.Skills)
		if err != nil {
			return fmt.Errorf("render promoted agent %s: %w", name, err)
		}
		fm := harness.PromotedFrontmatter{
			Name:            renderedName,
			Description:     desc,
			SourceType:      harness.SourceTypeAgent,
			Tools:           a.Frontmatter.Tools,
			DisallowedTools: a.Frontmatter.DisallowedTools,
			Skills:          renderedSkills,
			MCPServers:      a.Frontmatter.MCPServers,
		}
		content := harness.BuildPromotedMD(fm, body)
		harness.AddPromotedSkillWrite(f, skillsDir, renderedName, []byte(content), a.SourcePack, a.SourcePath)
	}
	return nil
}
