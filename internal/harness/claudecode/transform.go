package claudecode

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// NativeAgentFrontmatter is the Claude Code native subagent frontmatter.
// Tools and DisallowedTools are comma-separated strings (Claude Code format),
// not YAML lists. Skills and MCPServers remain YAML lists.
type NativeAgentFrontmatter struct {
	Name            string   `yaml:"name,omitempty"`
	Description     string   `yaml:"description,omitempty"`
	Tools           string   `yaml:"tools,omitempty"`
	DisallowedTools string   `yaml:"disallowedTools,omitempty"`
	Skills          []string `yaml:"skills,omitempty"`
	MCPServers      []string `yaml:"mcpServers,omitempty"`
}

// knownClaudeTools maps lowercase pack tool names to Claude Code PascalCase.
var knownClaudeTools = map[string]string{
	"read":         "Read",
	"write":        "Write",
	"edit":         "Edit",
	"bash":         "Bash",
	"grep":         "Grep",
	"glob":         "Glob",
	"agent":        "Agent",
	"webfetch":     "WebFetch",
	"websearch":    "WebSearch",
	"notebookedit": "NotebookEdit",
}

// toClaudeToolName converts a pack tool name to Claude Code native format.
func toClaudeToolName(name string) string {
	if cc, ok := knownClaudeTools[strings.ToLower(name)]; ok {
		return cc
	}
	return name
}

// joinToolsForClaude maps tool names to Claude Code format and joins them.
func joinToolsForClaude(tools []string) string {
	mapped := make([]string, len(tools))
	for i, t := range tools {
		mapped[i] = toClaudeToolName(t)
	}
	return strings.Join(mapped, ", ")
}

// isMCPTool returns true if the tool name belongs to one of the given MCP servers.
// A tool belongs to a server if it starts with the server name followed by an underscore.
func isMCPTool(tool string, servers []string) bool {
	lower := strings.ToLower(tool)
	for _, s := range servers {
		if strings.HasPrefix(lower, strings.ToLower(s)+"_") {
			return true
		}
	}
	return false
}

// filterNativeTools returns only the tools that are NOT MCP tools (i.e., belong to
// the known Claude native tool set or don't match any MCP server prefix).
// When mcpServers is present, MCP tools are accessible via mcpServers and must NOT
// appear in tools: (which creates a hard allowlist that blocks mcpServers access).
func filterNativeTools(tools []string, servers []string) []string {
	if len(servers) == 0 {
		return tools
	}
	var native []string
	for _, t := range tools {
		if !isMCPTool(t, servers) {
			native = append(native, t)
		}
	}
	return native
}

// TransformAgent converts a domain.Agent (harness-neutral snake_case frontmatter)
// to Claude Code native format (camelCase frontmatter, comma-separated tools).
// This is a pure function — no file I/O.
func TransformAgent(agent domain.Agent) ([]byte, error) {
	fm := agent.Frontmatter

	// If no frontmatter was parsed, pass through unchanged.
	if fm.Name == "" && fm.Description == "" && len(fm.Tools) == 0 &&
		len(fm.DisallowedTools) == 0 && len(fm.Skills) == 0 && len(fm.MCPServers) == 0 &&
		len(agent.Body) == 0 {
		return agent.Raw, nil
	}

	name := agent.Name
	if name == "" {
		name = fm.Name
	}

	// When mcpServers is present, filter MCP tool names out of tools:.
	// Claude Code's tools: field creates a hard allowlist that blocks MCP tools
	// from mcpServers. MCP tools are accessible via mcpServers instead.
	tools := filterNativeTools(fm.Tools, fm.MCPServers)

	cc := NativeAgentFrontmatter{
		Name:            name,
		Description:     fm.Description,
		Tools:           joinToolsForClaude(tools),
		DisallowedTools: joinToolsForClaude(fm.DisallowedTools),
		Skills:          fm.Skills,
		MCPServers:      fm.MCPServers,
	}

	out, err := yaml.Marshal(&cc)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(out)
	buf.WriteString("---\n")
	buf.Write(agent.Body)
	return buf.Bytes(), nil
}

// ReverseTransformAgent converts Claude Code native camelCase frontmatter
// back to the harness-neutral snake_case format.
func ReverseTransformAgent(raw []byte, filename string) (domain.Agent, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return domain.Agent{}, err
	}

	var fm domain.AgentFrontmatter
	if len(fmBytes) > 0 {
		// Parse as Claude Code native format first.
		var cc NativeAgentFrontmatter
		if err := yaml.Unmarshal(fmBytes, &cc); err != nil {
			return domain.Agent{}, err
		}
		fm = domain.AgentFrontmatter{
			Name:            cc.Name,
			Description:     cc.Description,
			Tools:           splitCommaSeparated(cc.Tools),
			DisallowedTools: splitCommaSeparated(cc.DisallowedTools),
			Skills:          cc.Skills,
			MCPServers:      cc.MCPServers,
		}
	}

	name := fm.Name
	if name == "" {
		name = strings.TrimSuffix(filename, ".md")
	}

	return domain.Agent{
		Name:        name,
		Frontmatter: fm,
		Body:        body,
		Raw:         raw,
	}, nil
}

func splitCommaSeparated(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
