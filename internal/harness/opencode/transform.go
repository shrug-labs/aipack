package opencode

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// NativeAgentFrontmatter is the OpenCode native agent frontmatter schema.
//
// OpenCode expects `tools` to be a YAML record/map (not a YAML list).
// Example:
//
//	tools:
//	  bash: true
//
// This differs from the harness-neutral pack schema (domain.AgentFrontmatter)
// which uses `Tools []string`.
type NativeAgentFrontmatter struct {
	Name            string          `yaml:"name,omitempty"`
	Description     string          `yaml:"description,omitempty"`
	Mode            string          `yaml:"mode,omitempty"`
	Temperature     *float64        `yaml:"temperature,omitempty"`
	Tools           map[string]bool `yaml:"tools,omitempty"`
	DisallowedTools []string        `yaml:"disallowed_tools,omitempty"`
	Skills          []string        `yaml:"skills,omitempty"`
	MCPServers      []string        `yaml:"mcp_servers,omitempty"`
}

// TransformAgent converts a harness-neutral agent markdown file into
// OpenCode's native agent format.
//
// Today this primarily rewrites frontmatter.tools from a YAML list into a YAML
// map, because OpenCode's config loader expects a record.
func TransformAgent(agent domain.Agent) ([]byte, error) {
	fmBytes, body, err := domain.SplitFrontmatter(agent.Raw)
	if err != nil {
		return nil, err
	}

	// No frontmatter: nothing we can safely transform.
	if len(fmBytes) == 0 {
		return agent.Raw, nil
	}

	// First parse as a generic YAML map so we can preserve unknown keys.
	var generic map[string]any
	if err := yaml.Unmarshal(fmBytes, &generic); err != nil {
		// If frontmatter is invalid, preserve raw.
		return agent.Raw, nil
	}

	// If tools list is present, rewrite it into a map.
	// We parse into harness-neutral struct to interpret tools list.
	var fm domain.AgentFrontmatter
	_ = yaml.Unmarshal(fmBytes, &fm) // best-effort
	if len(fm.Tools) > 0 {
		m := map[string]bool{}
		for _, t := range fm.Tools {
			if t == "" {
				continue
			}
			m[t] = true
		}
		generic["tools"] = m
	}

	newFm, err := yaml.Marshal(generic)
	if err != nil {
		return nil, err
	}

	out := bytes.NewBuffer(nil)
	out.WriteString("---\n")
	out.Write(bytes.TrimRight(newFm, "\n"))
	out.WriteString("\n---\n")
	out.Write(body)
	return out.Bytes(), nil
}

func ReverseTransformAgent(raw []byte, filename string) (domain.Agent, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return domain.Agent{}, err
	}

	var fm domain.AgentFrontmatter
	if len(fmBytes) > 0 {
		_ = yaml.Unmarshal(fmBytes, &fm)

		var generic map[string]any
		if err := yaml.Unmarshal(fmBytes, &generic); err != nil {
			return domain.Agent{}, err
		}
		fm.Tools = reverseTools(generic["tools"])
	}

	name := fm.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(filename), ".md")
	}

	return domain.Agent{
		Name:        name,
		Frontmatter: fm,
		Body:        body,
		Raw:         raw,
	}, nil
}

func reverseTools(v any) []string {
	switch t := v.(type) {
	case map[string]any:
		tools := make([]string, 0, len(t))
		for name, enabled := range t {
			if b, ok := enabled.(bool); ok && !b {
				continue
			}
			tools = append(tools, name)
		}
		sort.Strings(tools)
		return tools
	case []any:
		tools := make([]string, 0, len(t))
		for _, item := range t {
			if name, ok := item.(string); ok && name != "" {
				tools = append(tools, name)
			}
		}
		return tools
	default:
		return nil
	}
}
