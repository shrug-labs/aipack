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
func addPromotedWorkflows(f *domain.Fragment, baseDir, subDir string, namespaced bool, workflows []domain.Workflow) {
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
		if !strings.HasPrefix(desc, harness.DescPrefixWorkflow) {
			desc = harness.DescPrefixWorkflow + desc
		}
		renderedName := harness.ContentName(namespaced, w.SourcePack, name)
		fm := harness.PromotedFrontmatter{
			Name:        renderedName,
			Description: desc,
			SourceType:  harness.SourceTypeWorkflow,
			Metadata:    w.Frontmatter.Metadata,
		}
		content := harness.BuildPromotedMD(fm, body)
		harness.AddPromotedSkillWrite(f, filepath.Join(baseDir, subDir), renderedName, []byte(content), w.SourcePack, w.SourcePath)
	}
}

// addNativeAgents renders agents as native Codex TOML files under agentsDir
// and returns a map of registration entries for config.toml [agents.<name>].
// Each agent becomes a self-contained .toml file with developer_instructions,
// harness-specific config, resolved MCP servers, and skill references.
func addNativeAgents(
	f *domain.Fragment,
	agents []domain.Agent,
	ctx nativeAgentRenderContext,
) (map[string]map[string]any, []domain.Warning) {
	if len(agents) == 0 {
		return nil, nil
	}

	skillRefs := harness.NewSkillRefResolver(ctx.Skills, ctx.Namespaced)

	regs := map[string]map[string]any{}
	var warnings []domain.Warning
	f.Desired = append(f.Desired, ctx.AgentsDir)
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

		renderedName := harness.ContentName(ctx.Namespaced, a.SourcePack, name)
		renderedSkills, err := skillRefs.Render(a.SourcePack, a.Frontmatter.Skills)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Path:    a.SourcePath,
				Message: fmt.Sprintf("render native agent %s: %v", name, err),
			})
			continue
		}
		skillPaths := map[string]string{}
		for _, s := range renderedSkills {
			skillPaths[s] = filepath.Join(ctx.SkillsBase, ctx.SkillsSubDir, s)
		}
		renderedAgent := a
		renderedAgent.Name = renderedName
		renderedAgent.Frontmatter.Name = renderedName
		renderedAgent.Frontmatter.Skills = renderedSkills

		tomlBytes, renderWarnings, err := RenderAgentTOML(renderedAgent, ctx.AllMCPServers, skillPaths, desc)
		warnings = append(warnings, renderWarnings...)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Path:    a.SourcePath,
				Message: fmt.Sprintf("render native agent %s: %v", name, err),
			})
			continue
		}

		dst := filepath.Join(ctx.AgentsDir, renderedName+".toml")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    tomlBytes,
			SourcePack: a.SourcePack,
			Src:        a.SourcePath,
		})
		f.Desired = append(f.Desired, dst)

		// Codex accepts relative config_file paths in some code paths, but
		// app-backed clients can deserialize the agents table without a base
		// path. Use the destination path we already resolved for sync.
		regs[renderedName] = BuildAgentRegistration(renderedName, desc, filepath.Clean(dst))
	}
	return regs, warnings
}

type nativeAgentRenderContext struct {
	AgentsDir     string
	Skills        []domain.Skill
	AllMCPServers []domain.MCPServer
	SkillsBase    string
	SkillsSubDir  string
	Namespaced    bool
}
