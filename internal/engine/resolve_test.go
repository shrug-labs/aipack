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
	eng := New(nil, nil)
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

	profile, warnings, err := eng.Resolve(profileCfg, profilePath, configDir, config.CollisionError)
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
	eng := New(nil, nil)
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

	profile, _, err := eng.Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir, config.CollisionError)
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
	eng := New(nil, nil)
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

	profile, _, err := eng.Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir, config.CollisionError)
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
	eng := New(nil, nil)
	dir := t.TempDir()
	configDir := createTestPack(t, dir)

	profileCfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "test-pack"}},
	}

	profile, _, err := eng.Resolve(profileCfg, filepath.Join(dir, "profile.yaml"), configDir, config.CollisionError)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.HasContent() {
		t.Error("HasContent() should be true for pack with rules")
	}
}

// ---------------------------------------------------------------------------
// User story tests
// ---------------------------------------------------------------------------

// "My pack ships a wrapper script in extras/. The MCP server config references
// it via {pack:root}. After Engine.Resolve, the command has the absolute path."
func TestResolve_MCPPackRootExpandsToAbsolutePath(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	packDir := filepath.Join(dir, "packs", "wrapped")
	for _, sub := range []string{"mcp", "wrappers"} {
		os.MkdirAll(filepath.Join(packDir, sub), 0o755)
	}
	writeManifest(t, packDir, config.PackManifest{
		SchemaVersion: 1, Name: "wrapped", Version: "1", Root: ".",
		Extras: []string{"wrappers"},
		MCP:    config.MCPPack{Servers: map[string]config.MCPDefaults{"proxy": {}}},
	})
	os.WriteFile(filepath.Join(packDir, "wrappers", "proxy.py"),
		[]byte("#!/usr/bin/env python3\n"), 0o644)
	os.WriteFile(filepath.Join(packDir, "mcp", "proxy.json"), []byte(`{
		"name": "proxy",
		"command": ["python3", "{pack:root}/wrappers/proxy.py", "--port", "8080"]
	}`), 0o644)

	profile, warnings, err := eng.Resolve(config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name: "wrapped",
			MCP:  map[string]config.MCPServerConfig{"proxy": {Enabled: config.BoolPtr(true)}},
		}},
	}, filepath.Join(dir, "p.yaml"), dir, config.CollisionError)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Errorf("unexpected warning: %s: %s", w.Field, w.Message)
	}
	if len(profile.MCPServers) != 1 {
		t.Fatalf("got %d servers, want 1", len(profile.MCPServers))
	}
	// The command should contain the absolute pack path, not the {pack:root} token.
	cmd := profile.MCPServers[0].Command
	wantArg := filepath.Join(packDir, "wrappers", "proxy.py")
	if len(cmd) < 2 || cmd[1] != wantArg {
		t.Fatalf("Command = %v, want [python3 %s --port 8080]", cmd, wantArg)
	}
}

// "My profile sets params.pypi_url. My MCP server config uses {params.pypi_url}
// in its command. After resolve, the URL is expanded."
func TestResolve_ProfileParamsExpandInMCPCommand(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	packDir := filepath.Join(dir, "packs", "parameterized")
	os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755)
	writeManifest(t, packDir, config.PackManifest{
		SchemaVersion: 1, Name: "parameterized", Version: "1", Root: ".",
		MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{"pypi": {}}},
	})
	os.WriteFile(filepath.Join(packDir, "mcp", "pypi.json"), []byte(`{
		"name": "pypi",
		"command": ["uv", "run", "--default-index", "{params.pypi_url}", "pypi-mcp"],
		"env": {"INDEX_URL": "{params.pypi_url}"}
	}`), 0o644)

	profile, _, err := eng.Resolve(config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Params:        map[string]string{"pypi_url": "https://pypi.internal.example.com/simple"},
		Packs: []config.PackEntry{{
			Name: "parameterized",
			MCP:  map[string]config.MCPServerConfig{"pypi": {Enabled: config.BoolPtr(true)}},
		}},
	}, filepath.Join(dir, "p.yaml"), dir, config.CollisionError)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.MCPServers) != 1 {
		t.Fatalf("got %d servers, want 1", len(profile.MCPServers))
	}
	srv := profile.MCPServers[0]
	wantURL := "https://pypi.internal.example.com/simple"
	if srv.Command[3] != wantURL {
		t.Errorf("Command[3] = %q, want %q", srv.Command[3], wantURL)
	}
	if srv.Env["INDEX_URL"] != wantURL {
		t.Errorf("Env[INDEX_URL] = %q, want %q", srv.Env["INDEX_URL"], wantURL)
	}
}

// "My profile has two MCP servers. One requires GITHUB_TOKEN (not set).
// The other is self-contained. I expect 1 server + 1 warning, not a crash."
func TestResolve_UnresolvableEnvSkipsServerOthersSurvive(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	packDir := filepath.Join(dir, "packs", "mixed-env")
	os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755)
	writeManifest(t, packDir, config.PackManifest{
		SchemaVersion: 1, Name: "mixed-env", Version: "1", Root: ".",
		MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{
			"needs-token": {},
			"standalone":  {},
		}},
	})
	os.WriteFile(filepath.Join(packDir, "mcp", "needs-token.json"), []byte(`{
		"name": "needs-token",
		"command": ["mcp-github"],
		"env": {"GITHUB_TOKEN": "{env:AIPACK_TEST_NONEXISTENT_VAR_12345}"}
	}`), 0o644)
	os.WriteFile(filepath.Join(packDir, "mcp", "standalone.json"), []byte(`{
		"name": "standalone",
		"command": ["echo", "hello"]
	}`), 0o644)

	profile, warnings, err := eng.Resolve(config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name: "mixed-env",
			MCP: map[string]config.MCPServerConfig{
				"needs-token": {Enabled: config.BoolPtr(true)},
				"standalone":  {Enabled: config.BoolPtr(true)},
			},
		}},
	}, filepath.Join(dir, "p.yaml"), dir, config.CollisionError)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.MCPServers) != 1 {
		t.Fatalf("got %d servers, want 1 (standalone only)", len(profile.MCPServers))
	}
	if profile.MCPServers[0].Name != "standalone" {
		t.Fatalf("surviving server = %q, want standalone", profile.MCPServers[0].Name)
	}
	// Should have exactly one warning about the skipped server.
	found := false
	for _, w := range warnings {
		if w.Field == "mcp.needs-token" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for skipped needs-token server, got %v", warnings)
	}
}

// "My pack.json leaves rules as nil but agents as []. On disk, both dirs
// have .md files. Rules should be auto-discovered; agents should not."
func TestResolve_DiscoveryNilPopulatesEmptyArrayOpts(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	packDir := filepath.Join(dir, "packs", "discovery")
	for _, sub := range []string{"rules", "agents"} {
		os.MkdirAll(filepath.Join(packDir, sub), 0o755)
	}
	// Manifest: Rules nil (discover), Agents empty (also discover — empty
	// arrays trigger auto-discovery the same as nil/omitted).
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "discovery", Version: "1", Root: ".",
		Agents: []string{}, // empty = also triggers discovery
		MCP:    config.MCPPack{Servers: map[string]config.MCPDefaults{}},
	}
	writeManifest(t, packDir, manifest)

	// Both dirs have content on disk.
	os.WriteFile(filepath.Join(packDir, "rules", "discovered.md"),
		[]byte("---\nname: discovered\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(packDir, "agents", "also-discovered.md"),
		[]byte("---\nname: also-discovered\ndescription: d\ntools: [read]\n---\nbody\n"), 0o644)

	profile, _, err := eng.Resolve(config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "discovery"}},
	}, filepath.Join(dir, "p.yaml"), dir, config.CollisionError)
	if err != nil {
		t.Fatal(err)
	}
	rules := profile.AllRules()
	if len(rules) != 1 || rules[0].Name != "discovered" {
		t.Fatalf("AllRules() = %v, want [discovered]", rules)
	}
	agents := profile.AllAgents()
	if len(agents) != 1 || agents[0].Name != "also-discovered" {
		t.Fatalf("AllAgents() = %v, want [also-discovered] (empty array triggers discovery)", agents)
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
