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
func addPromotedAgents(f *domain.Fragment, skillsDir string, agents []domain.Agent) {
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
		fm := harness.PromotedFrontmatter{
			Name:            name,
			Description:     desc,
			SourceType:      harness.SourceTypeAgent,
			Tools:           a.Frontmatter.Tools,
			DisallowedTools: a.Frontmatter.DisallowedTools,
			Skills:          a.Frontmatter.Skills,
			MCPServers:      a.Frontmatter.MCPServers,
		}
		content := harness.BuildPromotedMD(fm, body)
		skillDir := filepath.Join(skillsDir, name)
		dst := filepath.Join(skillDir, "SKILL.md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    []byte(content),
			SourcePack: a.SourcePack,
			Src:        a.SourcePath,
		})
		f.Desired = append(f.Desired, skillDir, dst)
	}
}
