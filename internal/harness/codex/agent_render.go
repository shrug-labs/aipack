package codex

import (
	"maps"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
)

// RenderAgentTOML produces a self-contained Codex agent TOML file from a pack
// agent. The body becomes developer_instructions, harness.codex overrides
// become top-level keys, and referenced MCP servers / skills are resolved and
// embedded. The description parameter is the caller-resolved description
// (with fallback applied) to keep the TOML and config.toml registration consistent.
func RenderAgentTOML(agent domain.Agent, allMCPServers []domain.MCPServer, skillPaths map[string]string, description string) ([]byte, []domain.Warning, error) {
	root := map[string]any{
		"name":                   agent.Name,
		"description":            description,
		"developer_instructions": strings.TrimSpace(string(agent.Body)),
	}

	// Apply harness-specific overrides from harness.codex.
	if codexCfg, ok := agent.Frontmatter.Harness["codex"]; ok {
		maps.Copy(root, codexCfg)
	}

	// Resolve referenced MCP servers into the agent TOML.
	// Filter to referenced names, then let buildMCPEntries handle expansion.
	var warnings []domain.Warning
	if len(agent.Frontmatter.MCPServers) > 0 {
		referenced := filterMCPServers(allMCPServers, agent.Frontmatter.MCPServers)
		entries, mcpWarnings := buildMCPEntries(referenced)
		warnings = append(warnings, mcpWarnings...)
		if len(entries) > 0 {
			root["mcp_servers"] = entries
		}
	}

	// Resolve referenced skills into skills.config entries.
	if len(agent.Frontmatter.Skills) > 0 {
		var configs []map[string]any
		for _, skillName := range agent.Frontmatter.Skills {
			if p, ok := skillPaths[skillName]; ok {
				configs = append(configs, map[string]any{
					"enabled": true,
					"path":    p,
				})
			}
		}
		if len(configs) > 0 {
			root["skills"] = map[string]any{"config": configs}
		}
	}

	out, err := toml.Marshal(root)
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// BuildAgentRegistration produces the config.toml [agents.<name>] entry that
// points to the agent's TOML config file.
func BuildAgentRegistration(name, description, configFile string) map[string]any {
	return map[string]any{
		"description": description,
		"config_file": configFile,
	}
}

// filterMCPServers returns only the servers whose names appear in the refs list.
// The returned servers are unexpanded — buildMCPEntries handles expansion.
func filterMCPServers(servers []domain.MCPServer, refs []string) []domain.MCPServer {
	want := make(map[string]bool, len(refs))
	for _, r := range refs {
		want[r] = true
	}
	var out []domain.MCPServer
	for _, s := range servers {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	return out
}
