package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

// createTestPack creates a minimal pack directory for testing Resolve.
// It installs the pack at configDir/packs/test-pack/.
// Returns the configDir (pass to Resolve as configDir).
func createTestPack(t *testing.T, configDir string) string {
	t.Helper()

	packDir := filepath.Join(configDir, "packs", "test-pack")
	for _, sub := range []string{"rules", "agents", "workflows", "skills/my-skill", "mcp"} {
		if err := os.MkdirAll(filepath.Join(packDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Pack manifest.
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "test-pack",
		Version:       "0.1.0",
		Root:          ".",
		Rules:         []string{"alpha-rule"},
		Agents:        []string{"beta-agent"},
		Workflows:     []string{"gamma-workflow"},
		Skills:        []string{"my-skill"},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Rule with frontmatter.
	rule := "---\ndescription: Alpha rule description\npaths:\n  - src/\n---\nAlpha rule body.\n"
	if err := os.WriteFile(filepath.Join(packDir, "rules", "alpha-rule.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	// Agent with frontmatter.
	agent := "---\nname: Beta Agent\ndescription: Does beta things\ntools:\n  - read\n  - write\n---\nBeta agent system prompt.\n"
	if err := os.WriteFile(filepath.Join(packDir, "agents", "beta-agent.md"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Workflow with frontmatter.
	workflow := "---\ntitle: Gamma Workflow\ndescription: A test workflow\n---\nWorkflow steps.\n"
	if err := os.WriteFile(filepath.Join(packDir, "workflows", "gamma-workflow.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	// Skill with SKILL.md.
	skill := "---\nname: My Skill\ndescription: Test skill\n---\nSkill instructions.\n"
	if err := os.WriteFile(filepath.Join(packDir, "skills", "my-skill", "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}

	return configDir
}

func TestResolve_TypedContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := createTestPack(t, dir)

	profileCfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Params: map[string]string{
			"team_url": "https://example.com",
		},
		Packs: []config.PackEntry{
			{Name: "test-pack"},
		},
	}

	profilePath := filepath.Join(dir, "profile.yaml")

	profile, warnings, err := Resolve(profileCfg, profilePath, configDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	// Verify params.
	if profile.Params["team_url"] != "https://example.com" {
		t.Errorf("Params[team_url] = %q", profile.Params["team_url"])
	}

	// Verify packs.
	if len(profile.Packs) != 1 {
		t.Fatalf("Packs = %d, want 1", len(profile.Packs))
	}
	pk := profile.Packs[0]
	if pk.Name != "test-pack" {
		t.Errorf("Pack.Name = %q", pk.Name)
	}
	if pk.Version != "0.1.0" {
		t.Errorf("Pack.Version = %q", pk.Version)
	}

	// Verify typed rules via AllRules.
	rules := profile.AllRules()
	if len(rules) != 1 {
		t.Fatalf("AllRules() = %d, want 1", len(rules))
	}
	r := rules[0]
	if r.Name != "alpha-rule" {
		t.Errorf("Rule.Name = %q", r.Name)
	}
	if r.Frontmatter.Description != "Alpha rule description" {
		t.Errorf("Rule.Frontmatter.Description = %q", r.Frontmatter.Description)
	}
	if len(r.Frontmatter.Paths) != 1 || r.Frontmatter.Paths[0] != "src/" {
		t.Errorf("Rule.Frontmatter.Paths = %v", r.Frontmatter.Paths)
	}
	if string(r.Body) != "Alpha rule body.\n" {
		t.Errorf("Rule.Body = %q", r.Body)
	}
	if r.SourcePack != "test-pack" {
		t.Errorf("Rule.SourcePack = %q", r.SourcePack)
	}

	// Verify typed agents via AllAgents.
	agents := profile.AllAgents()
	if len(agents) != 1 {
		t.Fatalf("AllAgents() = %d, want 1", len(agents))
	}
	a := agents[0]
	if a.Name != "Beta Agent" {
		t.Errorf("Agent.Name = %q (should use frontmatter name)", a.Name)
	}
	if a.Frontmatter.Description != "Does beta things" {
		t.Errorf("Agent.Frontmatter.Description = %q", a.Frontmatter.Description)
	}
	if len(a.Frontmatter.Tools) != 2 {
		t.Errorf("Agent.Frontmatter.Tools = %v", a.Frontmatter.Tools)
	}

	// Verify typed workflows via AllWorkflows.
	workflows := profile.AllWorkflows()
	if len(workflows) != 1 {
		t.Fatalf("AllWorkflows() = %d, want 1", len(workflows))
	}
	wf := workflows[0]
	if wf.Frontmatter.Title != "Gamma Workflow" {
		t.Errorf("Workflow.Frontmatter.Title = %q", wf.Frontmatter.Title)
	}

	// Verify typed skills via AllSkills.
	skills := profile.AllSkills()
	if len(skills) != 1 {
		t.Fatalf("AllSkills() = %d, want 1", len(skills))
	}
	sk := skills[0]
	if sk.Name != "my-skill" {
		t.Errorf("Skill.Name = %q", sk.Name)
	}
	if sk.Frontmatter.Name != "My Skill" {
		t.Errorf("Skill.Frontmatter.Name = %q", sk.Frontmatter.Name)
	}
	if sk.SourcePack != "test-pack" {
		t.Errorf("Skill.SourcePack = %q", sk.SourcePack)
	}
}

func TestResolve_MultiPack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")

	// Create two packs directly in the installed-packs directory.
	pack1 := filepath.Join(configDir, "packs", "pack-a")
	pack2 := filepath.Join(configDir, "packs", "pack-b")
	for _, p := range []string{pack1, pack2} {
		for _, sub := range []string{"rules", "agents"} {
			if err := os.MkdirAll(filepath.Join(p, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Pack A manifest.
	writeManifest(t, pack1, config.PackManifest{
		SchemaVersion: 1, Name: "pack-a", Version: "1.0.0", Root: ".",
		Rules: []string{"rule-a"}, Agents: []string{"agent-a"},
	})
	if err := os.WriteFile(filepath.Join(pack1, "rules", "rule-a.md"), []byte("Rule A body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack1, "agents", "agent-a.md"), []byte("Agent A body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pack B manifest.
	writeManifest(t, pack2, config.PackManifest{
		SchemaVersion: 1, Name: "pack-b", Version: "2.0.0", Root: ".",
		Rules: []string{"rule-b"},
	})
	if err := os.WriteFile(filepath.Join(pack2, "rules", "rule-b.md"), []byte("---\ndescription: Rule B\n---\nRule B body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	profileCfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{
			{Name: "pack-a"},
			{Name: "pack-b"},
		},
	}

	profile, _, err := Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(profile.Packs) != 2 {
		t.Fatalf("Packs = %d, want 2", len(profile.Packs))
	}
	if profile.Packs[0].Version != "1.0.0" || profile.Packs[1].Version != "2.0.0" {
		t.Errorf("Pack versions: %q, %q", profile.Packs[0].Version, profile.Packs[1].Version)
	}

	// AllRules should flatten across packs in order.
	rules := profile.AllRules()
	if len(rules) != 2 {
		t.Fatalf("AllRules() = %d, want 2", len(rules))
	}
	if rules[0].Name != "rule-a" || rules[1].Name != "rule-b" {
		t.Errorf("AllRules names: %q, %q", rules[0].Name, rules[1].Name)
	}
	if rules[1].Frontmatter.Description != "Rule B" {
		t.Errorf("rules[1].Frontmatter.Description = %q", rules[1].Frontmatter.Description)
	}

	// AllAgents — only pack-a has an agent.
	agents := profile.AllAgents()
	if len(agents) != 1 {
		t.Fatalf("AllAgents() = %d, want 1", len(agents))
	}
	if agents[0].SourcePack != "pack-a" {
		t.Errorf("Agent.SourcePack = %q", agents[0].SourcePack)
	}
}

func TestResolve_WithMCPServers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	packDir := filepath.Join(configDir, "packs", "pack-mcp")
	if err := os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755); err != nil {
		t.Fatal(err)
	}

	// MCP server definition.
	mcpDef := `{"name": "myserver", "transport": "stdio", "command": ["node", "server.js"], "env": {"API_KEY": "{params.api_key}"}, "available_tools": ["tool-a", "tool-b"]}`
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "myserver.json"), []byte(mcpDef), 0o644); err != nil {
		t.Fatal(err)
	}

	writeManifest(t, packDir, config.PackManifest{
		SchemaVersion: 1, Name: "pack-mcp", Version: "1.0.0", Root: ".",
		MCP: config.MCPPack{
			Servers: map[string]config.MCPDefaults{
				"myserver": {DefaultAllowedTools: []string{"tool-a"}},
			},
		},
	})

	enabled := true
	profileCfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Params:        map[string]string{"api_key": "secret123"},
		Packs: []config.PackEntry{
			{
				Name: "pack-mcp",
				MCP: map[string]config.MCPServerConfig{
					"myserver": {Enabled: &enabled, AllowedTools: []string{"tool-a", "tool-b"}},
				},
			},
		},
	}

	profile, _, err := Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(profile.MCPServers) != 1 {
		t.Fatalf("MCPServers = %d, want 1", len(profile.MCPServers))
	}
	srv := profile.MCPServers[0]
	if srv.Name != "myserver" {
		t.Errorf("MCPServer.Name = %q", srv.Name)
	}
	// Globals should be expanded in env.
	if srv.Env["API_KEY"] != "secret123" {
		t.Errorf("MCPServer.Env[API_KEY] = %q, want expanded global", srv.Env["API_KEY"])
	}
	if len(srv.AllowedTools) != 2 {
		t.Errorf("MCPServer.AllowedTools = %v", srv.AllowedTools)
	}
	if srv.SourcePack != "pack-mcp" {
		t.Errorf("MCPServer.SourcePack = %q", srv.SourcePack)
	}
}

func TestResolve_HasContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configDir := createTestPack(t, dir)

	profileCfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "test-pack"}},
	}

	profile, _, err := Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.HasContent() {
		t.Error("HasContent() should be true for pack with rules")
	}
}

func writeManifest(t *testing.T, packDir string, m config.PackManifest) {
	t.Helper()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
