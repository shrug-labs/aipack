package claudecode

import (
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestTransformAgent_NameFromFrontmatter(t *testing.T) {
	t.Parallel()
	// When frontmatter has name, the parser sets agent.Name to that value.
	agent := domain.Agent{
		Name: "custom-name",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "custom-name",
			Description: "test",
			Tools:       []string{"read"},
		},
		Body: []byte("Body\n"),
	}

	out, err := TransformAgent(agent)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "name: custom-name") {
		t.Error("should use name from frontmatter")
	}
	if !strings.Contains(s, "tools: Read") {
		t.Errorf("tools should be PascalCase comma-separated string, got:\n%s", s)
	}
}

func TestTransformAgent_FilenameAsName(t *testing.T) {
	t.Parallel()
	// MCP tools (atlassian_*) should be filtered out when mcpServers is set.
	// They're accessible via mcpServers instead.
	agent := domain.Agent{
		Name: "readonly",
		Frontmatter: domain.AgentFrontmatter{
			Description:     "Read-only agent",
			Tools:           []string{"atlassian_jira_get_issue", "atlassian_confluence_search"},
			DisallowedTools: []string{"write", "edit"},
			Skills:          []string{"triage"},
			MCPServers:      []string{"atlassian"},
		},
		Body: []byte("System prompt body.\n"),
	}

	out, err := TransformAgent(agent)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "name: readonly") {
		t.Error("missing name derived from filename")
	}
	if !strings.Contains(s, "description: Read-only agent") {
		t.Error("missing description")
	}
	// MCP tools filtered out — tools: should be omitted entirely
	if strings.Contains(s, "tools:") {
		t.Errorf("tools: should be omitted when all tools are MCP tools covered by mcpServers, got:\n%s", s)
	}
	if !strings.Contains(s, "disallowedTools: Write, Edit") {
		t.Errorf("disallowedTools should be comma-separated PascalCase, got:\n%s", s)
	}
	if strings.Contains(s, "disallowed_tools:") {
		t.Error("should not have snake_case disallowed_tools in output")
	}
	if !strings.Contains(s, "skills:") || !strings.Contains(s, "- triage") {
		t.Error("skills should be YAML list")
	}
	if !strings.Contains(s, "mcpServers:") || !strings.Contains(s, "- atlassian") {
		t.Error("mcpServers should be YAML list (camelCase key)")
	}
	if strings.Contains(s, "mcp_servers:") {
		t.Error("should not have snake_case mcp_servers in output")
	}
	if !strings.Contains(s, "System prompt body.") {
		t.Error("body not preserved")
	}
}

func TestTransformAgent_NoFrontmatter(t *testing.T) {
	t.Parallel()
	content := []byte("Just a plain markdown body with no frontmatter.\n")
	agent := domain.Agent{
		Name: "plain",
		Raw:  content,
	}

	out, err := TransformAgent(agent)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	if string(out) != string(content) {
		t.Errorf("no-frontmatter file should pass through unchanged, got:\n%s", out)
	}
}

func TestTransformAgent_AllFields(t *testing.T) {
	t.Parallel()
	// jira_get_issue is an MCP tool (prefix "atlassian" not matched, but "jira" not
	// in mcpServers either — however, the pack convention uses server names like
	// "atlassian" which covers atlassian_* tools). Here we test with a monitoring_ tool too.
	agent := domain.Agent{
		Name: "full",
		Frontmatter: domain.AgentFrontmatter{
			Description:     "Full agent",
			Tools:           []string{"read", "grep", "atlassian_jira_get_issue", "monitoring_get_regions"},
			DisallowedTools: []string{"write", "bash"},
			Skills:          []string{"triage", "deploy"},
			MCPServers:      []string{"atlassian", "monitoring"},
		},
		Body: []byte("Body.\n"),
	}

	out, err := TransformAgent(agent)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	s := string(out)
	// MCP tools (atlassian_*, monitoring_*) filtered out; only native tools remain
	if !strings.Contains(s, "tools: Read, Grep") {
		t.Errorf("tools should contain only native tools after MCP filtering, got:\n%s", s)
	}
	if strings.Contains(s, "atlassian_jira_get_issue") || strings.Contains(s, "monitoring_get_regions") {
		t.Errorf("MCP tools should be filtered out when mcpServers is set, got:\n%s", s)
	}
	if !strings.Contains(s, "disallowedTools: Write, Bash") {
		t.Errorf("disallowedTools should be comma-separated PascalCase, got:\n%s", s)
	}
	if !strings.Contains(s, "skills:") || !strings.Contains(s, "- triage") || !strings.Contains(s, "- deploy") {
		t.Error("skills not preserved as YAML list")
	}
	if !strings.Contains(s, "mcpServers:") || !strings.Contains(s, "- atlassian") || !strings.Contains(s, "- monitoring") {
		t.Error("mcpServers not preserved as YAML list")
	}
}

func TestTransformAgent_NoMCPServers_KeepsAllTools(t *testing.T) {
	t.Parallel()
	// Without mcpServers, all tools pass through unchanged (no filtering).
	agent := domain.Agent{
		Name: "no-mcp",
		Frontmatter: domain.AgentFrontmatter{
			Description: "Agent without MCP",
			Tools:       []string{"read", "jira_get_issue", "confluence_search"},
		},
		Body: []byte("Body.\n"),
	}

	out, err := TransformAgent(agent)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "tools: Read, jira_get_issue, confluence_search") {
		t.Errorf("without mcpServers, all tools should pass through, got:\n%s", s)
	}
}

func TestReverseTransformAgent_Basic(t *testing.T) {
	t.Parallel()
	raw := []byte("---\nname: my-agent\ndescription: test agent\ntools: Read, Grep\ndisallowedTools: Write, Bash\nskills:\n  - triage\nmcpServers:\n  - atlassian\n---\nBody.\n")

	agent, err := ReverseTransformAgent(raw, "my-agent.md")
	if err != nil {
		t.Fatalf("ReverseTransformAgent: %v", err)
	}

	if agent.Name != "my-agent" {
		t.Errorf("name: got %q want %q", agent.Name, "my-agent")
	}
	if agent.Frontmatter.Description != "test agent" {
		t.Errorf("description: got %q", agent.Frontmatter.Description)
	}
	if len(agent.Frontmatter.Tools) != 2 || agent.Frontmatter.Tools[0] != "Read" {
		t.Errorf("tools: got %v", agent.Frontmatter.Tools)
	}
	if len(agent.Frontmatter.DisallowedTools) != 2 || agent.Frontmatter.DisallowedTools[0] != "Write" {
		t.Errorf("disallowed tools: got %v", agent.Frontmatter.DisallowedTools)
	}
	if len(agent.Frontmatter.Skills) != 1 || agent.Frontmatter.Skills[0] != "triage" {
		t.Errorf("skills: got %v", agent.Frontmatter.Skills)
	}
	if len(agent.Frontmatter.MCPServers) != 1 || agent.Frontmatter.MCPServers[0] != "atlassian" {
		t.Errorf("mcp servers: got %v", agent.Frontmatter.MCPServers)
	}
	if string(agent.Body) != "Body.\n" {
		t.Errorf("body: got %q", agent.Body)
	}
}

func TestReverseTransformAgent_NameFromFilename(t *testing.T) {
	t.Parallel()
	raw := []byte("---\ndescription: no name field\n---\nBody.\n")

	agent, err := ReverseTransformAgent(raw, "fallback-name.md")
	if err != nil {
		t.Fatalf("ReverseTransformAgent: %v", err)
	}

	if agent.Name != "fallback-name" {
		t.Errorf("name: got %q want %q", agent.Name, "fallback-name")
	}
}

func TestRoundTrip_TransformReverse(t *testing.T) {
	t.Parallel()
	original := domain.Agent{
		Name: "roundtrip",
		Frontmatter: domain.AgentFrontmatter{
			Description:     "RT agent",
			Tools:           []string{"read", "grep"},
			DisallowedTools: []string{"write"},
			Skills:          []string{"debug"},
			MCPServers:      []string{"monitoring"},
		},
		Body: []byte("Round-trip body.\n"),
	}

	transformed, err := TransformAgent(original)
	if err != nil {
		t.Fatalf("TransformAgent: %v", err)
	}

	reversed, err := ReverseTransformAgent(transformed, "roundtrip.md")
	if err != nil {
		t.Fatalf("ReverseTransformAgent: %v", err)
	}

	if reversed.Name != original.Name {
		t.Errorf("name: got %q want %q", reversed.Name, original.Name)
	}
	if reversed.Frontmatter.Description != original.Frontmatter.Description {
		t.Errorf("description: got %q want %q", reversed.Frontmatter.Description, original.Frontmatter.Description)
	}
	if string(reversed.Body) != string(original.Body) {
		t.Errorf("body: got %q want %q", reversed.Body, original.Body)
	}
}
