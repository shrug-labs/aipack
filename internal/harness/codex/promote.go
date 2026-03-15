package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// addPromotedWorkflows converts workflows to SKILL.md WriteActions under the
// given subDir (e.g. ".agents/skills"). Each workflow becomes a skill directory
// with a generated SKILL.md containing enriched frontmatter + the workflow body.
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
		fm := harness.PromotedFrontmatter{
			Name:        name,
			Description: desc,
			SourceType:  harness.SourceTypeWorkflow,
			Metadata:    w.Frontmatter.Metadata,
		}
		content := harness.BuildPromotedMD(fm, body)
		skillDir := filepath.Join(baseDir, subDir, name)
		dst := filepath.Join(skillDir, "SKILL.md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    []byte(content),
			SourcePack: w.SourcePack,
			Src:        w.SourcePath,
		})
		f.Desired = append(f.Desired, skillDir, dst)
	}
}

// addPromotedAgents converts agents to SKILL.md WriteActions under the given
// subDir. Each agent becomes a skill directory with a generated SKILL.md
// containing enriched frontmatter that preserves agent metadata.
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
		skillDir := filepath.Join(baseDir, subDir, name)
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
