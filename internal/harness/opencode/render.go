package opencode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

type opencodeMCPEntry struct {
	Enabled     bool              `json:"enabled"`
	Type        string            `json:"type"`
	Command     []string          `json:"command,omitempty"`
	URL         string            `json:"url,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Timeout     int               `json:"timeout"`
}

func buildMCPEntries(servers []domain.MCPServer) (map[string]opencodeMCPEntry, []domain.MCPServer, []domain.Warning) {
	expanded, warnings := engine.ExpandMCPServers(servers)

	mcp := map[string]opencodeMCPEntry{}
	for _, s := range expanded {
		timeout := s.Timeout
		if timeout == 0 {
			timeout = 10 // default: 10 seconds (×1000 → ms for OpenCode)
		}
		entry := opencodeMCPEntry{
			Enabled: true,
			Timeout: timeout * 1000,
		}
		if s.IsStdio() {
			entry.Type = "local"
			entry.Command = s.Command
			if len(s.Env) > 0 {
				entry.Environment = s.Env
			}
		} else {
			entry.Type = s.Transport
			entry.URL = s.URL
		}
		mcp[s.Name] = entry
	}
	return mcp, expanded, warnings
}

// buildToolsMap emits OpenCode's per-tool enable map under the `tools` key.
// Both AllowedTools and AlwaysAllowedTools union into the same boolean map
// (both become `true`) because OpenCode's public `tools` field is binary —
// there is no distinct per-tool auto-approve target yet. When any profiles
// set AlwaysAllowedTools on OpenCode servers, a single aggregated warning
// is emitted listing the affected servers so the user knows the auto-approve
// intent is not enforced per-tool.
//
// TODO: confirm upstream per-tool auto-approve syntax (Slack discussion
// referenced by user, 2026-04-15). Once the target is known, emit distinct
// JSON for AlwaysAllowedTools instead of folding it into `tools`.
func buildToolsMap(servers []domain.MCPServer) (map[string]bool, []domain.Warning) {
	tools := map[string]bool{}
	prefixSet := map[string]struct{}{}
	var alwaysAllowServers []string
	for _, s := range servers {
		name := engine.NormalizeServerName(s.Name)
		for _, t := range engine.UnionToolLists(s.AllowedTools, s.AlwaysAllowedTools) {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			tools[name+"_"+t] = true
			prefixSet[name] = struct{}{}
		}
		for _, t := range s.DisabledTools {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			tools[name+"_"+t] = false
		}
		if len(s.AlwaysAllowedTools) > 0 {
			alwaysAllowServers = append(alwaysAllowServers, s.Name)
		}
	}
	for p := range prefixSet {
		tools[p+"_*"] = false
	}
	var warnings []domain.Warning
	if len(alwaysAllowServers) > 0 {
		slices.Sort(alwaysAllowServers)
		warnings = append(warnings, domain.Warning{
			Field: "mcp.always_allowed_tools",
			Message: fmt.Sprintf(
				"OpenCode: always_allowed_tools unioned into the legacy `tools` map for: %s. "+
					"Tools are callable but will prompt per call — per-tool auto-approve rendering is not yet implemented "+
					"(upstream syntax pending confirmation).",
				strings.Join(alwaysAllowServers, ", ")),
		})
	}
	return tools, warnings
}

// RenderBytes produces the full opencode.json content.
func RenderBytes(base []byte, servers []domain.MCPServer, instr InstructionsSpec, skills SkillsSpec) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	entries, expanded, warnings := buildMCPEntries(servers)
	root["mcp"] = entries
	toolsMap, toolWarnings := buildToolsMap(expanded)
	root["tools"] = toolsMap
	warnings = append(warnings, toolWarnings...)
	MergeInstructions(root, instr)
	MergeSkills(root, skills)

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderManagedKeysOnly produces a JSON object containing ONLY the sync-managed
// keys (mcp, tools, instructions, skills). Used for MergeMode MCP actions.
func RenderManagedKeysOnly(servers []domain.MCPServer, instr InstructionsSpec, skills SkillsSpec) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	entries, expanded, warnings := buildMCPEntries(servers)
	root["mcp"] = entries
	toolsMap, toolWarnings := buildToolsMap(expanded)
	root["tools"] = toolsMap
	warnings = append(warnings, toolWarnings...)

	if instr.Manage && len(instr.Desired) > 0 {
		root["instructions"] = instr.Desired
	}
	if skills.Manage && len(skills.Desired) > 0 {
		root["skills"] = map[string]any{"paths": skills.Desired}
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}
