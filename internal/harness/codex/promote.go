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
		if !strings.HasPrefix(desc, harness.DescPrefixWorkflow) {
			desc = harness.DescPrefixWorkflow + desc
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

// addNativeAgents renders agents as native Codex TOML files under agentsDir
// and returns a map of registration entries for config.toml [agents.<name>].
// Each agent becomes a self-contained .toml file with developer_instructions,
// harness-specific config, resolved MCP servers, and skill references.
func addNativeAgents(
	f *domain.Fragment,
	agentsDir string,
	agents []domain.Agent,
	allMCPServers []domain.MCPServer,
	skillsBase, skillsSubDir string,
) (map[string]map[string]any, []domain.Warning) {
	if len(agents) == 0 {
		return nil, nil
	}

	// Build a lookup of skill name → rendered path for skill resolution.
	// Skills are rendered under skillsBase/skillsSubDir/<name>.
	skillPaths := map[string]string{}
	// We don't have the full skill list here, but agents reference skills by
	// name and the paths are deterministic.
	for _, a := range agents {
		for _, s := range a.Frontmatter.Skills {
			skillPaths[s] = filepath.Join(skillsBase, skillsSubDir, s)
		}
	}

	regs := map[string]map[string]any{}
	var warnings []domain.Warning
	f.Desired = append(f.Desired, agentsDir)
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

		tomlBytes, renderWarnings, err := RenderAgentTOML(a, allMCPServers, skillPaths, desc)
		warnings = append(warnings, renderWarnings...)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Path:    a.SourcePath,
				Message: fmt.Sprintf("render native agent %s: %v", name, err),
			})
			continue
		}

		dst := filepath.Join(agentsDir, name+".toml")
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
		regs[name] = BuildAgentRegistration(name, desc, filepath.Clean(dst))
	}
	return regs, warnings
}
