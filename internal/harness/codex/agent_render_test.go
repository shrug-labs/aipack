package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestRenderAgentTOML_BasicAgent(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "explorer",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "explorer",
			Description: "Fast codebase exploration",
			Tools:       []string{"Read", "Grep"},
			Harness: map[string]map[string]any{
				"codex": {
					"model":                  "o3",
					"model_reasoning_effort": "high",
				},
			},
		},
		Body: []byte("You are an exploration agent."),
	}

	out, warnings, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	s := string(out)
	if !strings.Contains(s, `name = 'explorer'`) {
		t.Fatalf("expected name field, got:\n%s", s)
	}
	if !strings.Contains(s, `description = 'Fast codebase exploration'`) {
		t.Fatalf("expected description field, got:\n%s", s)
	}
	if !strings.Contains(s, "You are an exploration agent.") {
		t.Fatal("expected developer_instructions containing body")
	}
	if !strings.Contains(s, `model = 'o3'`) {
		t.Fatalf("expected model from harness.codex, got:\n%s", s)
	}
	if !strings.Contains(s, `model_reasoning_effort = 'high'`) {
		t.Fatalf("expected reasoning_effort from harness.codex, got:\n%s", s)
	}
}

func TestRenderAgentTOML_WithMCPServers(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "investigator",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "investigator",
			Description: "Investigates issues",
			MCPServers:  []string{"deploy-tool"},
		},
		Body: []byte("You investigate."),
	}
	servers := []domain.MCPServer{
		{Name: "deploy-tool", Transport: "stdio", Command: []string{"deploy-tool", "serve"}, Env: map[string]string{"TOKEN": "x"}},
		{Name: "unused", Transport: "stdio", Command: []string{"other"}},
	}

	out, _, err := RenderAgentTOML(agent, servers, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, "deploy-tool") {
		t.Fatal("expected referenced MCP server 'deploy-tool'")
	}
	if strings.Contains(s, "unused") {
		t.Fatal("should not include unreferenced MCP server")
	}
}

func TestRenderAgentTOML_WithSkills(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "reviewer",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "reviewer",
			Description: "Reviews code",
			Skills:      []string{"deep-research"},
		},
		Body: []byte("You review code."),
	}
	skillPaths := map[string]string{
		"deep-research": "~/.codex/skills/deep-research",
		"unrelated":     "~/.codex/skills/unrelated",
	}

	out, _, err := RenderAgentTOML(agent, nil, skillPaths, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, "deep-research") {
		t.Fatal("expected referenced skill path")
	}
	if strings.Contains(s, "unrelated") {
		t.Fatal("should not include unreferenced skill")
	}
}

func TestRenderAgentTOML_NoHarnessOverrides(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "simple",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "simple",
			Description: "Simple agent",
		},
		Body: []byte("You are simple."),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, `name = 'simple'`) {
		t.Fatal("expected name")
	}
	if strings.Contains(s, "model") {
		t.Fatal("should not contain model when no harness overrides")
	}
}

func TestRenderAgentTOML_EmptyBody(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "empty",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "empty",
			Description: "Empty body agent",
		},
		Body: []byte(""),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "developer_instructions = ''") {
		t.Fatalf("expected empty developer_instructions, got:\n%s", string(out))
	}
}

func TestBuildAgentRegistration(t *testing.T) {
	t.Parallel()
	reg := BuildAgentRegistration("explorer", "Fast codebase exploration", "./agents/explorer.toml")
	if reg["description"] != "Fast codebase exploration" {
		t.Fatal("expected description")
	}
	if reg["config_file"] != "./agents/explorer.toml" {
		t.Fatal("expected config_file")
	}
}

func TestFilterMCPServers(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "a", Command: []string{"a"}},
		{Name: "b", Command: []string{"b"}},
		{Name: "c", Command: []string{"c"}},
	}
	filtered := filterMCPServers(servers, []string{"a", "c"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
	if filtered[0].Name != "a" || filtered[1].Name != "c" {
		t.Fatalf("got %v %v", filtered[0].Name, filtered[1].Name)
	}
}

// --- Edge cases and adversarial tests ---

func TestRenderAgentTOML_HarnessOverridesDoNotClobberRequired(t *testing.T) {
	// If harness.codex contains "name" or "description" or "developer_instructions",
	// the harness override should win (pack author explicitly set it).
	t.Parallel()
	agent := domain.Agent{
		Name: "test-agent",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "test-agent",
			Description: "Original description",
			Harness: map[string]map[string]any{
				"codex": {
					"description": "Override description from harness config",
					"model":       "o3",
				},
			},
		},
		Body: []byte("You are a test agent."),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// harness.codex.description should override the frontmatter description
	// because harness overrides are applied after the base fields.
	if !strings.Contains(s, "Override description from harness config") {
		t.Fatalf("expected harness override to win for description, got:\n%s", s)
	}
}

func TestRenderAgentTOML_MultilineBody(t *testing.T) {
	t.Parallel()
	body := "# System Prompt\n\nYou are a specialized agent.\n\n## Rules\n\n1. Do X\n2. Do Y\n3. Never do Z\n"
	agent := domain.Agent{
		Name: "multiline",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "multiline",
			Description: "Multiline body test",
		},
		Body: []byte(body),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// The full body should survive TOML encoding — multiline strings must be preserved.
	if !strings.Contains(s, "Never do Z") {
		t.Fatalf("expected multiline body to be preserved, got:\n%s", s)
	}
	if !strings.Contains(s, "# System Prompt") {
		t.Fatalf("expected markdown headings in body to survive, got:\n%s", s)
	}
}

func TestRenderAgentTOML_SpecialCharsInBody(t *testing.T) {
	t.Parallel()
	body := `You handle "quoted" text, backslashes \, and single quotes '.`
	agent := domain.Agent{
		Name: "special",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "special",
			Description: "Special chars test",
		},
		Body: []byte(body),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the TOML is parseable (round-trip through toml.Unmarshal).
	var parsed map[string]any
	if err := toml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("generated TOML should be parseable: %v\n%s", err, string(out))
	}
	if got := parsed["developer_instructions"].(string); got != body {
		t.Fatalf("body round-trip failed:\ngot:  %q\nwant: %q", got, body)
	}
}

func TestRenderAgentTOML_NonCodexHarnessIgnored(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Name: "multi-harness",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "multi-harness",
			Description: "Agent with multiple harness configs",
			Harness: map[string]map[string]any{
				"codex":  {"model": "o3"},
				"claude": {"model": "opus"},
				"cline":  {"model": "gpt-4"},
			},
		},
		Body: []byte("Multi-harness agent."),
	}

	out, _, err := RenderAgentTOML(agent, nil, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Only codex config should be rendered.
	if !strings.Contains(s, "model = 'o3'") {
		t.Fatal("expected codex model")
	}
	if strings.Contains(s, "opus") {
		t.Fatal("claude harness config should not appear in Codex TOML")
	}
	if strings.Contains(s, "gpt-4") {
		t.Fatal("cline harness config should not appear in Codex TOML")
	}
}

func TestRenderAgentTOML_MCPServerReferencesNonexistent(t *testing.T) {
	// Agent references an MCP server that doesn't exist in the profile.
	t.Parallel()
	agent := domain.Agent{
		Name: "orphan-mcp",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "orphan-mcp",
			Description: "References nonexistent MCP",
			MCPServers:  []string{"nonexistent-server"},
		},
		Body: []byte("Agent body."),
	}
	servers := []domain.MCPServer{
		{Name: "existing", Command: []string{"cmd"}},
	}

	out, _, err := RenderAgentTOML(agent, servers, nil, agent.Frontmatter.Description)
	if err != nil {
		t.Fatal(err)
	}
	// Should produce valid TOML without the missing server — no crash.
	var parsed map[string]any
	if err := toml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("should produce valid TOML even with missing server: %v", err)
	}
	// mcp_servers should be absent (no valid server matched).
	if _, ok := parsed["mcp_servers"]; ok {
		t.Fatal("should not have mcp_servers when no servers matched")
	}
}

func TestAddNativeAgents_EmptyBody_Skipped(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{Name: "empty", Body: []byte("  \n  "), SourcePack: "pack-a"},
	}

	regs, _ := addNativeAgents(&f, agents, nativeAgentRenderContext{
		AgentsDir:    "/agents",
		SkillsBase:   "/project",
		SkillsSubDir: ".codex/skills",
	})

	if len(f.Writes) != 0 {
		t.Fatalf("expected 0 writes for empty body, got %d", len(f.Writes))
	}
	if len(regs) != 0 {
		t.Fatalf("expected 0 registrations for empty body, got %d", len(regs))
	}
}

func TestAddNativeAgents_MultipleAgents(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{
			Name:        "explorer",
			Frontmatter: domain.AgentFrontmatter{Name: "explorer", Description: "Explores"},
			Body:        []byte("explore"),
			SourcePack:  "pack-a",
		},
		{
			Name:        "reviewer",
			Frontmatter: domain.AgentFrontmatter{Name: "reviewer", Description: "Reviews"},
			Body:        []byte("review"),
			SourcePack:  "pack-a",
		},
	}

	regs, _ := addNativeAgents(&f, agents, nativeAgentRenderContext{
		AgentsDir:    "/project/.codex/agents",
		SkillsBase:   "/project",
		SkillsSubDir: ".codex/skills",
	})

	if len(f.Writes) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(f.Writes))
	}
	if len(regs) != 2 {
		t.Fatalf("expected 2 registrations, got %d", len(regs))
	}
	if _, ok := regs["explorer"]; !ok {
		t.Fatal("missing explorer registration")
	}
	if _, ok := regs["reviewer"]; !ok {
		t.Fatal("missing reviewer registration")
	}
	wantConfigFile := filepath.Join("/project", ".codex", "agents", "explorer.toml")
	if got := regs["explorer"]["config_file"]; got != wantConfigFile {
		t.Fatalf("explorer config_file = %v, want %v", got, wantConfigFile)
	}
}

func TestRenderBytes_WithAgentRegistrations(t *testing.T) {
	t.Parallel()
	agentRegs := map[string]map[string]any{
		"explorer": {"description": "Explores", "config_file": "./agents/explorer.toml"},
	}

	out, _, err := RenderBytes(nil, nil, agentRegs)
	if err != nil {
		t.Fatal(err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agents, ok := root["agents"].(map[string]any)
	if !ok {
		t.Fatalf("expected agents table, got %T", root["agents"])
	}
	explorer, ok := agents["explorer"].(map[string]any)
	if !ok {
		t.Fatal("expected explorer registration")
	}
	if explorer["config_file"] != "./agents/explorer.toml" {
		t.Fatalf("config_file = %v", explorer["config_file"])
	}
}

func TestRenderBytes_AgentRegsPreserveExistingGlobalSettings(t *testing.T) {
	// Base config has [agents] with max_threads. Merging registrations should preserve it.
	t.Parallel()
	base := []byte("[agents]\nmax_threads = 4\n")
	agentRegs := map[string]map[string]any{
		"explorer": {"description": "Explores", "config_file": "./agents/explorer.toml"},
	}

	out, _, err := RenderBytes(base, nil, agentRegs)
	if err != nil {
		t.Fatal(err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agents := root["agents"].(map[string]any)
	// max_threads should be preserved from base.
	maxThreads, ok := agents["max_threads"]
	if !ok {
		t.Fatal("max_threads should be preserved from base config")
	}
	// TOML parses integers as int64.
	if maxThreads != int64(4) {
		t.Fatalf("max_threads = %v (type %T)", maxThreads, maxThreads)
	}
	// Explorer registration should also be present.
	if _, ok := agents["explorer"]; !ok {
		t.Fatal("explorer registration missing after merge")
	}
}
