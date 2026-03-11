package opencode

import (
	"encoding/json"
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

func buildMCPEntries(servers []domain.MCPServer, resolveEnv bool) (map[string]opencodeMCPEntry, []domain.Warning) {
	expanded := servers
	var warnings []domain.Warning
	if resolveEnv {
		expanded, warnings = engine.ExpandMCPForRenderBestEffort(servers)
	} else {
		expanded, warnings = engine.ExpandMCPForRender(servers, false, "")
	}

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
	return mcp, warnings
}

func buildToolsMap(servers []domain.MCPServer) map[string]bool {
	tools := map[string]bool{}
	prefixSet := map[string]struct{}{}
	for _, s := range servers {
		name := engine.NormalizeServerName(s.Name)
		for _, t := range s.AllowedTools {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			tools[name+"_"+t] = true
			prefixSet[name] = struct{}{}
		}
	}
	for p := range prefixSet {
		tools[p+"_*"] = false
	}
	return tools
}

// RenderBytes produces the full opencode.json content.
func RenderBytes(base []byte, servers []domain.MCPServer, instr InstructionsSpec, skills SkillsSpec, resolveEnv bool) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	entries, warnings := buildMCPEntries(servers, resolveEnv)
	root["mcp"] = entries
	root["tools"] = buildToolsMap(servers)
	MergeInstructions(root, instr)
	MergeSkills(root, skills)

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderManagedKeysOnly produces a JSON object containing ONLY the sync-managed
// keys (mcp, tools, instructions, skills). Used for MergeMode Plugin actions.
func RenderManagedKeysOnly(servers []domain.MCPServer, instr InstructionsSpec, skills SkillsSpec, resolveEnv bool) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	entries, warnings := buildMCPEntries(servers, resolveEnv)
	root["mcp"] = entries
	root["tools"] = buildToolsMap(servers)

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

// StripManagedKeys removes sync-managed keys from rendered settings.
func StripManagedKeys(rendered []byte, filename string) ([]byte, error) {
	if filename == "oh-my-opencode.json" {
		return rendered, nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(rendered, &root); err != nil {
		return nil, err
	}
	delete(root, "mcp")
	delete(root, "tools")
	delete(root, "instructions")
	delete(root, "skills")
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
