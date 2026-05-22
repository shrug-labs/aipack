package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/testutil"
)

// --- Plan tests ---

func TestPlan_Project_AgentsOverride_WithRulesAndAgents(t *testing.T) {
	projectDir := t.TempDir()

	// Write existing AGENTS.md to preserve.
	existing := "# Project agents\n\nhello\n"
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "rule-a", Raw: []byte("rule-a"), SourcePack: "pack-a"},
					{Name: "rule-b", Raw: []byte("rule-b"), SourcePack: "pack-a"},
				},
				Agents: []domain.Agent{
					{Name: "reviewer", Body: []byte("Reviews code"), Frontmatter: domain.AgentFrontmatter{Name: "reviewer", Description: "Code review specialist"}, Raw: []byte("# Reviewer\nReviews code"), SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// AGENTS.override.md should contain rules only — no agents.
	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	var overrideContent string
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			overrideContent = string(w.Content)
		}
	}
	if overrideContent == "" {
		t.Fatalf("expected write to %q; got writes: %v", overridePath, writeDsts(f.Writes))
	}
	if !strings.HasPrefix(overrideContent, "<!-- aipack managed; DO NOT EDIT by hand -->") {
		t.Fatal("expected managed header")
	}
	if !strings.Contains(overrideContent, "rule-a") {
		t.Fatal("expected rules content")
	}
	// Agent should NOT be in AGENTS.override.md.
	if strings.Contains(overrideContent, "### reviewer") {
		t.Fatal("agent should not be inlined in AGENTS.override.md")
	}
	// Verify preserved existing.
	if !strings.Contains(overrideContent, "preserved from existing AGENTS.md") {
		t.Fatal("expected preserved existing AGENTS.md content")
	}

	// Agent should be rendered as a native TOML, NOT promoted to a skill.
	agentTOMLPath := filepath.Join(projectDir, ".codex", "agents", "reviewer.toml")
	var agentContent string
	for _, w := range f.Writes {
		if w.Dst == agentTOMLPath {
			agentContent = string(w.Content)
		}
	}
	if agentContent == "" {
		t.Fatalf("expected native agent TOML at %q; got writes: %v", agentTOMLPath, writeDsts(f.Writes))
	}
	if !strings.Contains(agentContent, "Reviews code") {
		t.Fatal("expected agent body in developer_instructions")
	}
	if !strings.Contains(agentContent, "name = 'reviewer'") {
		t.Fatal("expected name field in agent TOML")
	}

	// Agent should NOT be promoted to a skill.
	skillPath := filepath.Join(projectDir, ".agents", "skills", "reviewer", "SKILL.md")
	for _, w := range f.Writes {
		if w.Dst == skillPath {
			t.Fatal("agent should NOT be promoted to skill — should be native TOML")
		}
	}

	// Config.toml settings should contain agent registration.
	for _, s := range f.Settings {
		if strings.Contains(string(s.Desired), "reviewer") && strings.Contains(string(s.Desired), "config_file") {
			return // found it
		}
	}
	t.Fatal("expected agent registration in config.toml settings")
}

func TestPlan_Project_AgentsOverride_NamespacedRules(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  projectDir,
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{
						Name:       "rule-a",
						Raw:        []byte("---\nname: rule-a\ndescription: Rule A\n---\nRule body\n"),
						SourcePack: "pack-a",
					},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	var content string
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			content = string(w.Content)
		}
	}
	if content == "" {
		t.Fatalf("expected write to %q; got writes: %v", overridePath, writeDsts(f.Writes))
	}
	for _, want := range []string{"<!-- source: rule-a__aipack__pack-a.md -->", "name: rule-a__aipack__pack-a", "Rule body"} {
		if !strings.Contains(content, want) {
			t.Fatalf("namespaced AGENTS.override.md missing %q:\n%s", want, content)
		}
	}
}

func TestPlan_Project_AgentsOverride_NoExistingAgents(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{{Name: "rule-only", Raw: []byte("rule-only\n"), SourcePack: "pack-a"}},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	var content string
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			content = string(w.Content)
		}
	}
	if content == "" {
		t.Fatalf("expected write to AGENTS.override.md")
	}
	if strings.Contains(content, "preserved from existing") {
		t.Fatal("should not have preserved section when no AGENTS.md exists")
	}
}

func TestPlan_Project_PluginsConfig(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Plugins: []domain.Plugin{
					{Name: "linear", Source: "github:linear/linear-codex-plugin", SourcePack: "pack-a"},
					{Name: "superpowers", Source: "github:obra/superpowers", Marketplace: "github:obra/superpowers-marketplace", SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Settings) != 1 {
		t.Fatalf("settings actions = %d, want 1", len(f.Settings))
	}
	var root map[string]any
	if err := toml.Unmarshal(f.Settings[0].Desired, &root); err != nil {
		t.Fatalf("unmarshal config.toml: %v\n%s", err, f.Settings[0].Desired)
	}
	plugins, ok := root["plugins"].(map[string]any)
	if !ok {
		t.Fatalf("plugins table missing in:\n%s", f.Settings[0].Desired)
	}
	for _, key := range []string{"linear@openai-curated", "superpowers@superpowers-marketplace"} {
		entry, ok := plugins[key].(map[string]any)
		if !ok {
			t.Fatalf("plugins.%q missing in %v", key, plugins)
		}
		if entry["enabled"] != true {
			t.Fatalf("plugins.%q.enabled = %v, want true", key, entry["enabled"])
		}
	}
}

func TestPlan_Project_PluginsIgnoreSkipSettings(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:        domain.ScopeProject,
		TargetDir:    projectDir,
		SkipSettings: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Plugins: []domain.Plugin{{Name: "linear", Source: "github:linear/linear-codex-plugin", SourcePack: "pack-a"}},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Settings) != 0 {
		t.Fatalf("settings actions = %d, want 0 with skip settings", len(f.Settings))
	}
	if len(f.MCP) != 1 {
		t.Fatalf("MCP/plugin actions = %d, want 1", len(f.MCP))
	}
	var root map[string]any
	if err := toml.Unmarshal(f.MCP[0].Desired, &root); err != nil {
		t.Fatalf("unmarshal managed config: %v\n%s", err, f.MCP[0].Desired)
	}
	plugins, ok := root["plugins"].(map[string]any)
	if !ok {
		t.Fatalf("plugins table missing from skip-settings action:\n%s", f.MCP[0].Desired)
	}
	entry, ok := plugins["linear@openai-curated"].(map[string]any)
	if !ok || entry["enabled"] != true {
		t.Fatalf("plugins.linear@openai-curated = %v, want enabled=true", plugins["linear@openai-curated"])
	}
}

func TestPlan_Project_CodexHooksJSONAndTrustState(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hooksPath := filepath.Join(projectDir, ".codex", "hooks.json")
	ctx := engine.SyncContext{
		ConfigDir: "/tmp/aipack-config",
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "pack-a",
				Hooks: []domain.Hook{{
					ID:         "tool-audit",
					SourcePack: "pack-a",
					Events: []domain.HookEvent{{
						On:    domain.HookEventToolAfter,
						Match: domain.HookMatch{Tool: "*"},
						Handlers: []domain.HookHandler{{
							Type:          "command",
							Command:       "python3 /tmp/post.py",
							Timeout:       "5",
							StatusMessage: "sending",
						}},
					}},
				}},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var hooksJSON []byte
	for _, w := range f.Writes {
		if w.Dst == hooksPath {
			hooksJSON = w.Content
			if w.SourcePack != "pack-a" {
				t.Fatalf("hooks.json SourcePack = %q, want pack-a", w.SourcePack)
			}
		}
	}
	if len(hooksJSON) == 0 {
		t.Fatalf("expected hooks.json write; got writes: %v", writeDsts(f.Writes))
	}
	var hooksRoot map[string]any
	if err := json.Unmarshal(hooksJSON, &hooksRoot); err != nil {
		t.Fatalf("unmarshal hooks.json: %v\n%s", err, hooksJSON)
	}
	if _, ok := hooksRoot["hooks"].(map[string]any)["PostToolUse"]; !ok {
		t.Fatalf("PostToolUse missing from hooks.json:\n%s", hooksJSON)
	}
	postGroups := hooksRoot["hooks"].(map[string]any)["PostToolUse"].([]any)
	postGroup := postGroups[0].(map[string]any)
	handlers := postGroup["hooks"].([]any)
	handler := handlers[0].(map[string]any)
	if command := handler["command"].(string); command != "python3 /tmp/post.py" {
		t.Fatalf("hook command = %q, want direct pack command", command)
	}
	if len(f.Settings) != 1 {
		t.Fatalf("settings actions = %d, want 1", len(f.Settings))
	}
	state := codexHookStateFromTOML(t, f.Settings[0].Desired)
	key := hooksPath + ":post_tool_use:0:0"
	entry, ok := state[key].(map[string]any)
	if !ok {
		t.Fatalf("hook state %q missing in %v", key, state)
	}
	if got, _ := entry["trusted_hash"].(string); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("trusted_hash = %v, want sha256", entry["trusted_hash"])
	}
}

func TestPlan_Project_CodexHooksIgnoreSkipSettings(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:        domain.ScopeProject,
		TargetDir:    projectDir,
		SkipSettings: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "pack-a",
				Hooks: []domain.Hook{{
					ID:         "tool-audit",
					SourcePack: "pack-a",
					Events: []domain.HookEvent{{
						On:    domain.HookEventToolAfter,
						Match: domain.HookMatch{Tool: "*"},
						Handlers: []domain.HookHandler{{
							Type:    "command",
							Command: "python3 /tmp/post.py",
						}},
					}},
				}},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Settings) != 0 {
		t.Fatalf("settings actions = %d, want 0 with skip settings", len(f.Settings))
	}
	if len(f.MCP) != 1 {
		t.Fatalf("managed config actions = %d, want 1", len(f.MCP))
	}
	state := codexHookStateFromTOML(t, f.MCP[0].Desired)
	key := filepath.Join(projectDir, ".codex", "hooks.json") + ":post_tool_use:0:0"
	if _, ok := state[key]; !ok {
		t.Fatalf("hook state %q missing in %v", key, state)
	}
}

func TestPlan_Project_AgentsOverride_SourcePackSingle(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "r1", Raw: []byte("rule-1"), SourcePack: "alpha"},
					{Name: "r2", Raw: []byte("rule-2"), SourcePack: "alpha"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			if w.SourcePack != "alpha" {
				t.Fatalf("AGENTS.override.md SourcePack = %q, want %q", w.SourcePack, "alpha")
			}
			return
		}
	}
	t.Fatal("expected write to AGENTS.override.md")
}

func TestPlan_Project_AgentsOverride_SourcePackComposite(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{
				{Rules: []domain.Rule{{Name: "r1", Raw: []byte("rule-1"), SourcePack: "alpha"}}},
				{Rules: []domain.Rule{{Name: "r2", Raw: []byte("rule-2"), SourcePack: "beta"}}},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			if w.SourcePack != "(composite)" {
				t.Fatalf("AGENTS.override.md SourcePack = %q, want %q", w.SourcePack, "(composite)")
			}
			return
		}
	}
	t.Fatal("expected write to AGENTS.override.md")
}

func TestPlan_Project_WithWorkflows(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Workflows: []domain.Workflow{
					{Name: "promote", Body: []byte("Promote a tier"), Frontmatter: domain.WorkflowFrontmatter{Name: "promote", Description: "Promote a release tier"}, Raw: []byte("# Promote\nPromote a tier"), SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// No rules → AGENTS.override.md should NOT be written.
	overridePath := filepath.Join(projectDir, "AGENTS.override.md")
	for _, w := range f.Writes {
		if w.Dst == overridePath {
			t.Fatal("AGENTS.override.md should not be written when there are no rules")
		}
	}

	// Workflow should be promoted to a skill.
	skillPath := filepath.Join(projectDir, ".agents", "skills", "promote", "SKILL.md")
	var skillContent string
	for _, w := range f.Writes {
		if w.Dst == skillPath {
			skillContent = string(w.Content)
		}
	}
	if skillContent == "" {
		t.Fatalf("expected promoted workflow skill at %q; got writes: %v", skillPath, writeDsts(f.Writes))
	}
	if !strings.Contains(skillContent, "name: promote") {
		t.Fatal("expected source content name in skill frontmatter")
	}
	if !strings.Contains(skillContent, "Promote a tier") {
		t.Fatal("expected workflow body in promoted skill")
	}
}

func TestPlan_Project_Skills(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Skills: []domain.Skill{
					{Name: "deploy", DirPath: "/pack/skills/deploy", SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantSkill := filepath.Join(projectDir, ".agents", "skills", "deploy")
	found := false
	for _, c := range f.Copies {
		if c.Dst == wantSkill {
			found = true
			if c.Kind != domain.CopyKindDir {
				t.Fatalf("skill copy kind: got %q want %q", c.Kind, domain.CopyKindDir)
			}
		}
	}
	if !found {
		t.Fatalf("expected copy to %q", wantSkill)
	}
}

func TestPlan_Project_SkillsRenderedWithNamespacedFrontmatter(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	skillDir := testutil.SkillDir(t, "deploy")
	if err := os.WriteFile(filepath.Join(skillDir, "helper.md"), []byte("helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  projectDir,
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Skills: []domain.Skill{
					{Name: "deploy", DirPath: skillDir, SourcePack: "pack-a", Assets: []string{"helper.md"}},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	skillPath := filepath.Join(projectDir, ".agents", "skills", "deploy__aipack__pack-a", domain.SkillEntryFile)
	var skillContent string
	for _, w := range f.Writes {
		if w.Dst == skillPath {
			skillContent = string(w.Content)
		}
	}
	if !strings.Contains(skillContent, "name: deploy__aipack__pack-a") {
		t.Fatalf("rendered SKILL.md missing namespaced frontmatter:\n%s", skillContent)
	}

	helperDst := filepath.Join(projectDir, ".agents", "skills", "deploy__aipack__pack-a", "helper.md")
	foundHelper := false
	for _, c := range f.Copies {
		if c.Dst == helperDst && c.Kind == domain.CopyKindFile {
			foundHelper = true
		}
	}
	if !foundHelper {
		t.Fatalf("expected helper asset copy to %q; got %v", helperDst, f.Copies)
	}
}

func TestPlan_Project_Settings(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Settings) == 0 {
		t.Fatal("expected settings action for config.toml")
	}
	sa := f.Settings[0]
	if sa.Harness != domain.HarnessCodex {
		t.Fatalf("settings harness: got %q want %q", sa.Harness, domain.HarnessCodex)
	}
	// Verify TOML has mcp_servers.
	var root map[string]any
	if err := toml.Unmarshal(sa.Desired, &root); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if _, ok := root["mcp_servers"]; !ok {
		t.Fatal("settings missing 'mcp_servers' key")
	}
}

func TestPlan_Project_BaseSettingsOnly(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			BaseSettings: domain.SettingsBundle{
				domain.HarnessCodex: []domain.ConfigFile{
					{Filename: "config.toml", Content: []byte("model = \"gpt-test\"\n")},
				},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Settings) != 1 {
		t.Fatalf("settings actions = %d, want 1", len(f.Settings))
	}

	var root map[string]any
	if err := toml.Unmarshal(f.Settings[0].Desired, &root); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if root["model"] != "gpt-test" {
		t.Fatalf("model = %v, want gpt-test", root["model"])
	}

	ctx.SkipSettings = true
	f, err = Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan with skip settings: %v", err)
	}
	if len(f.Settings) != 0 || len(f.MCP) != 0 {
		t.Fatalf("skip-settings base-only actions: settings=%d mcp=%d, want none", len(f.Settings), len(f.MCP))
	}
}

// --- Render tests ---

func TestRenderBytes_MergesBase(t *testing.T) {
	t.Parallel()
	base := []byte("foo = 'bar'\n")
	servers := []domain.MCPServer{
		{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}, AllowedTools: []string{"get_issue"}},
	}

	out, _, err := RenderBytes(base, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if root["foo"] != "bar" {
		t.Fatalf("base key 'foo' should be preserved: %v", root["foo"])
	}
	mcp, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatal("mcp_servers missing")
	}
	if _, ok := mcp["foo"]; !ok {
		t.Fatal("mcp_servers missing foo entry")
	}
}

func TestRenderBytes_AlwaysAllowedToolsRendersApprovalMode(t *testing.T) {
	t.Parallel()
	// AlwaysAllowedTools should emit a [mcp_servers.<name>.tools.<tool>]
	// approval_mode = "approve" stanza for each entry, and also union into
	// enabled_tools so the tool is callable at all.
	servers := []domain.MCPServer{
		{
			Name:               "bitbucket",
			Command:            []string{"node", "bitbucket.js"},
			Env:                map[string]string{},
			AllowedTools:       []string{"list_repos", "get_pr_diff"},
			AlwaysAllowedTools: []string{"get_pr_diff"}, // sensitive read auto-approved
		},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	bb := root["mcp_servers"].(map[string]any)["bitbucket"].(map[string]any)

	// enabled_tools contains the union (sorted, deduped).
	enabledAny, ok := bb["enabled_tools"].([]any)
	if !ok {
		t.Fatalf("enabled_tools missing or wrong type: %T %v", bb["enabled_tools"], bb["enabled_tools"])
	}
	got := make([]string, len(enabledAny))
	for i, v := range enabledAny {
		got[i] = v.(string)
	}
	wantEnabled := []string{"get_pr_diff", "list_repos"}
	if len(got) != 2 || got[0] != wantEnabled[0] || got[1] != wantEnabled[1] {
		t.Errorf("enabled_tools = %v, want %v", got, wantEnabled)
	}

	// tools.get_pr_diff.approval_mode = "approve".
	tools, ok := bb["tools"].(map[string]any)
	if !ok {
		t.Fatalf("tools table missing: %v", bb)
	}
	getPR, ok := tools["get_pr_diff"].(map[string]any)
	if !ok {
		t.Fatalf("tools.get_pr_diff missing: %v", tools)
	}
	if getPR["approval_mode"] != "approve" {
		t.Errorf("approval_mode = %v, want \"approve\"", getPR["approval_mode"])
	}

	// list_repos is NOT in tools (it's only in allowed_tools, not always_allowed).
	if _, exists := tools["list_repos"]; exists {
		t.Errorf("tools should not contain list_repos (not in AlwaysAllowedTools)")
	}
}

func TestRenderBytes_AlwaysAllowedOnly(t *testing.T) {
	t.Parallel()
	// AlwaysAllowedTools implies visibility — a tool in always_allowed but
	// NOT in allowed_tools must still appear in enabled_tools, otherwise
	// Codex would hide it entirely.
	servers := []domain.MCPServer{
		{
			Name:               "docs",
			Command:            []string{"docs-server"},
			Env:                map[string]string{},
			AlwaysAllowedTools: []string{"search"},
		},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	docs := root["mcp_servers"].(map[string]any)["docs"].(map[string]any)
	enabledAny := docs["enabled_tools"].([]any)
	if len(enabledAny) != 1 || enabledAny[0] != "search" {
		t.Errorf("enabled_tools = %v, want [search] (AlwaysAllowed implies visibility)", enabledAny)
	}
	tools := docs["tools"].(map[string]any)
	if tools["search"].(map[string]any)["approval_mode"] != "approve" {
		t.Errorf("tools.search.approval_mode missing")
	}
}

func TestRenderBytes_ToolNamesWithSpecialCharsRoundTrip(t *testing.T) {
	t.Parallel()
	// OCI-style tool names commonly contain dots/slashes/hyphens which are
	// not valid TOML bare keys. go-toml/v2 auto-quotes them on marshal and
	// unquotes on unmarshal; this test pins the behavior so a future toml
	// library swap can't silently mangle names.
	specialNames := []string{
		"with.dot",
		"with-hyphen",
		"with:colon",
		"with/slash",
		"namespace.action.v2",
	}
	servers := []domain.MCPServer{
		{
			Name:               "oci",
			Command:            []string{"cloud-mcp"},
			Env:                map[string]string{},
			AllowedTools:       specialNames,
			AlwaysAllowedTools: specialNames,
		},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	// 1) The raw TOML must round-trip through the Codex capture parser and
	//    surface identical tool names on both allowed + always-allowed sides.
	captured := map[string]domain.MCPServer{}
	allowed := map[string][]string{}
	alwaysAllowed := map[string][]string{}
	if warnings := parseCodexSettings(captured, allowed, alwaysAllowed, out); len(warnings) > 0 {
		t.Fatalf("parseCodexSettings warnings: %+v", warnings)
	}
	wantSorted := append([]string(nil), specialNames...)
	slices.Sort(wantSorted)

	if !slices.Equal(allowed["oci"], wantSorted) {
		t.Errorf("round-trip allowed_tools = %v, want %v", allowed["oci"], wantSorted)
	}
	if !slices.Equal(alwaysAllowed["oci"], wantSorted) {
		t.Errorf("round-trip always_allowed_tools = %v, want %v", alwaysAllowed["oci"], wantSorted)
	}
}

func TestRenderBytes_EnvIncluded(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "srv", Command: []string{"node", "srv.js"}, Env: map[string]string{"API_KEY": "secret", "TOKEN": "abc"}},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	srv := mcp["srv"].(map[string]any)
	env, ok := srv["env"].(map[string]any)
	if !ok {
		t.Fatalf("expected env table; got %v", srv["env"])
	}
	if env["API_KEY"] != "secret" {
		t.Fatalf("env API_KEY: got %q want %q", env["API_KEY"], "secret")
	}
	if env["TOKEN"] != "abc" {
		t.Fatalf("env TOKEN: got %q want %q", env["TOKEN"], "abc")
	}
}

func TestRenderBytes_OmitsEmptyEnv(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "plain", Command: []string{"echo", "hi"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	plain := mcp["plain"].(map[string]any)
	if _, ok := plain["env"]; ok {
		t.Fatal("empty env should be omitted from TOML output")
	}
}

func TestRenderBytes_EnvRefsResolved(t *testing.T) {
	t.Setenv("MON_ENV_FILE", "/etc/monitoring/.env")
	servers := []domain.MCPServer{
		{Name: "monitoring", Command: []string{"uvx", "--env-file", "{env:MON_ENV_FILE}", "monitoring_mcp@latest"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	mon := mcp["monitoring"].(map[string]any)

	// Env refs resolved from process environment at sync time.
	if mon["command"] != "uvx" {
		t.Fatalf("expected direct command 'uvx': got %q", mon["command"])
	}
	args, ok := mon["args"].([]any)
	if !ok || len(args) < 3 {
		t.Fatalf("expected 3 args: %v", mon["args"])
	}
	if args[1] != "/etc/monitoring/.env" {
		t.Fatalf("expected resolved env value '/etc/monitoring/.env': got %q", args[1])
	}
}

func TestRenderBytes_DirectCommand(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "plain", Command: []string{"node", "server.js"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	plain := mcp["plain"].(map[string]any)

	if plain["command"] != "node" {
		t.Fatalf("expected direct command: got %q", plain["command"])
	}
}

func TestRenderBytes_NonStdioIncluded(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "stdio-srv", Command: []string{"node", "srv.js"}, Env: map[string]string{}},
		{Name: "sse-srv", Transport: domain.TransportSSE, URL: "https://example.com/sse"},
	}

	out, warnings, err := RenderBytes(nil, servers, nil)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Message, "does not support") {
			t.Fatalf("should not warn about non-stdio support: %s", w.Message)
		}
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	if _, ok := mcp["stdio-srv"]; !ok {
		t.Fatal("stdio server should be included")
	}
	if _, ok := mcp["sse-srv"]; !ok {
		t.Fatal("non-stdio server should be included")
	}
	sse := mcp["sse-srv"].(map[string]any)
	if sse["type"] != domain.TransportSSE {
		t.Fatalf("expected type %q, got %q", domain.TransportSSE, sse["type"])
	}
	if sse["url"] != "https://example.com/sse" {
		t.Fatalf("expected url preserved, got %q", sse["url"])
	}
}

// --- BuildManagedContent tests ---

func TestBuildManagedContent_RulesOnly(t *testing.T) {
	t.Parallel()
	content := BuildManagedContent("# Shared rules", "rule-a\nrule-b")

	for _, want := range []string{
		"<!-- aipack managed; DO NOT EDIT by hand -->",
		"# Shared rules",
		"rule-a",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in output:\n%s", want, content)
		}
	}
	// No agent or workflow sections.
	if strings.Contains(content, "## Agent Profiles") {
		t.Fatal("no agent section expected")
	}
	if strings.Contains(content, "## Available Workflows") {
		t.Fatal("no workflow section expected")
	}
}

func TestBuildManagedContent_Empty(t *testing.T) {
	t.Parallel()
	content := BuildManagedContent("# Rules", "")
	if !strings.Contains(content, "<!-- aipack managed") {
		t.Fatal("expected managed header")
	}
	if strings.Contains(content, "# Rules") {
		t.Fatal("rules heading should be omitted when rules are empty")
	}
}

// --- Strip via Layout tests ---

func TestLayout_StripManaged_RemovesMCPServersAndAgents(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	h := Harness{}
	layout := h.Layout(domain.ScopeProject, projectDir, projectDir)
	if len(layout.OwnedFiles) == 0 {
		t.Fatal("expected at least one owned file")
	}
	input := []byte("foo = 'bar'\n\n[mcp_servers.test]\nenabled = true\ncommand = 'echo'\n\n[agents.reviewer]\ndescription = 'Reviews code'\nconfig_file = './agents/reviewer.toml'\n")
	out, err := layout.StripManaged(input, layout.OwnedFiles[0].Path)
	if err != nil {
		t.Fatalf("StripManaged: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := root["mcp_servers"]; ok {
		t.Fatal("mcp_servers should be stripped")
	}
	if _, ok := root["agents"]; ok {
		t.Fatal("agents should be stripped")
	}
	if root["foo"] != "bar" {
		t.Fatal("non-managed keys should be preserved")
	}
}

func TestLayout_StripManaged_RemovesOnlyAIPackHookState(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	h := Harness{}
	layout := h.Layout(domain.ScopeProject, projectDir, projectDir)
	hooksPath := filepath.Join(projectDir, ".codex", "hooks.json")
	otherPath := filepath.Join(projectDir, ".codex", "other-hooks.json")
	input := []byte(`
[hooks.state."` + hooksPath + `:post_tool_use:0:0"]
trusted_hash = "sha256:managed"

[hooks.state."` + otherPath + `:post_tool_use:0:0"]
trusted_hash = "sha256:user"
`)

	out, err := layout.StripManaged(input, layout.OwnedFiles[0].Path)
	if err != nil {
		t.Fatalf("StripManaged: %v", err)
	}
	state := codexHookStateFromTOML(t, out)
	if _, ok := state[hooksPath+":post_tool_use:0:0"]; ok {
		t.Fatalf("managed hook state was not stripped: %v", state)
	}
	if _, ok := state[otherPath+":post_tool_use:0:0"]; !ok {
		t.Fatalf("user hook state was stripped: %v", state)
	}
}

// --- Capture tests ---

func TestCapture_Project(t *testing.T) {
	projectDir := t.TempDir()

	// Create skills dir.
	skillDir := filepath.Join(projectDir, ".agents", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Deploy"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Skills) == 0 {
		t.Fatal("expected Skills to be populated")
	}

	// AGENTS.override.md is generated content — must NOT be captured.
	for _, c := range res.Copies {
		if filepath.Base(c.Dst) == "AGENTS.override.md" {
			t.Fatal("AGENTS.override.md should not be captured (generated content)")
		}
	}

	// Typed skills field.
	if len(res.Skills) != 1 || res.Skills[0].Name != "deploy" {
		t.Fatalf("expected typed skill 'deploy'; got %v", res.Skills)
	}
}

func TestCapture_Project_DisabledTools(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	// Write config.toml with both enabled_tools and disabled_tools.
	configDir := filepath.Join(projectDir, ".codex")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `[mcp_servers.atlassian]
enabled = true
command = "node"
args = ["server.js"]
enabled_tools = ["get_issue", "search"]
disabled_tools = ["delete_issue", "create_issue"]
startup_timeout_sec = 10
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	srv, ok := res.MCPServers["atlassian"]
	if !ok {
		t.Fatal("expected atlassian server in captured MCPServers")
	}
	if len(srv.DisabledTools) != 2 {
		t.Fatalf("DisabledTools = %v, want [create_issue delete_issue]", srv.DisabledTools)
	}
	// Sorted by parseCodexSettings.
	if srv.DisabledTools[0] != "create_issue" || srv.DisabledTools[1] != "delete_issue" {
		t.Fatalf("DisabledTools = %v, want [create_issue delete_issue]", srv.DisabledTools)
	}
	tools, ok := res.AllowedTools["atlassian"]
	if !ok || len(tools) != 2 {
		t.Fatalf("AllowedTools = %v, want [get_issue search]", tools)
	}
}

func TestCapture_Project_AlwaysAllowedTools(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	configDir := filepath.Join(projectDir, ".codex")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Per-tool approval_mode round-trip: the bitbucket server has
	// enabled_tools [get_pr, list_repos], and get_pr is additionally
	// marked approval_mode = "approve" via the nested
	// [mcp_servers.bitbucket.tools.get_pr] table.
	configContent := `[mcp_servers.bitbucket]
enabled = true
command = "node"
args = ["bitbucket.js"]
enabled_tools = ["get_pr", "list_repos"]
startup_timeout_sec = 10

[mcp_servers.bitbucket.tools.get_pr]
approval_mode = "approve"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if _, ok := res.MCPServers["bitbucket"]; !ok {
		t.Fatal("expected bitbucket server in captured MCPServers")
	}
	allowed, ok := res.AllowedTools["bitbucket"]
	if !ok || len(allowed) != 2 || allowed[0] != "get_pr" || allowed[1] != "list_repos" {
		t.Fatalf("AllowedTools = %v, want [get_pr list_repos]", allowed)
	}
	always, ok := res.AlwaysAllowedTools["bitbucket"]
	if !ok {
		t.Fatalf("AlwaysAllowedTools[bitbucket] missing; got %v", res.AlwaysAllowedTools)
	}
	if len(always) != 1 || always[0] != "get_pr" {
		t.Fatalf("AlwaysAllowedTools = %v, want [get_pr]", always)
	}
}

// --- Layout tests ---

// --- Promote tests ---

func TestPromoteWorkflows_GeneratesSkillMD(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{
			Name:        "remediate",
			Frontmatter: domain.WorkflowFrontmatter{Name: "remediate", Description: "Automated issue remediation workflow"},
			Body:        []byte("## Steps\n\n1. Run remediate\n2. Verify"),
			SourcePack:  "pack-a",
			SourcePath:  "/packs/pack-a/workflows/remediate.md",
		},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), true, workflows)

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".agents", "skills", "remediate__aipack__pack-a", "SKILL.md")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "name: remediate__aipack__pack-a") {
		t.Fatal("expected rendered content name in frontmatter")
	}
	if !strings.Contains(content, "[Workflow] Automated issue remediation workflow") {
		t.Fatal("expected [Workflow] prefixed description in frontmatter")
	}
	if !strings.Contains(content, "source_type: workflow") {
		t.Fatal("expected source_type: workflow in frontmatter")
	}
	if !strings.Contains(content, "## Steps") {
		t.Fatal("expected body content")
	}
	// Desired should include both the skill directory and the SKILL.md file.
	wantDir := filepath.Join("/project", ".agents", "skills", "remediate__aipack__pack-a")
	wantFile := filepath.Join(wantDir, "SKILL.md")
	if len(f.Desired) != 2 || f.Desired[0] != wantDir || f.Desired[1] != wantFile {
		t.Fatalf("desired: got %v want [%s %s]", f.Desired, wantDir, wantFile)
	}
}

func TestNativeAgents_GeneratesToml(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{
			Name: "reviewer",
			Frontmatter: domain.AgentFrontmatter{
				Name:        "reviewer",
				Description: "Code review specialist",
				Tools:       []string{"read", "grep"},
				Skills:      []string{"code-review"},
				Harness: map[string]map[string]any{
					"codex": {"model": "o3", "model_reasoning_effort": "high"},
				},
			},
			Body:       []byte("You are a code reviewer."),
			SourcePack: "pack-a",
			SourcePath: "/packs/pack-a/agents/reviewer.md",
		},
	}

	regs, _ := addNativeAgents(&f, agents, nativeAgentRenderContext{
		AgentsDir:    "/project/.codex/agents",
		SkillsBase:   "/project",
		SkillsSubDir: filepath.Join(".agents", "skills"),
	})

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".codex", "agents", "reviewer.toml")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "You are a code reviewer.") {
		t.Fatal("expected body in developer_instructions")
	}
	if !strings.Contains(content, "name = 'reviewer'") {
		t.Fatal("expected name in TOML")
	}
	if !strings.Contains(content, "model = 'o3'") {
		t.Fatal("expected model from harness.codex")
	}

	// Verify registration entry was produced.
	reg, ok := regs["reviewer"]
	if !ok {
		t.Fatal("expected registration entry for reviewer")
	}
	if reg["config_file"] != wantDst {
		t.Fatalf("config_file = %v", reg["config_file"])
	}
}

func TestNativeAgents_NamespacedGeneratesToml(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{
			Name: "reviewer",
			Frontmatter: domain.AgentFrontmatter{
				Name:        "reviewer",
				Description: "Code review specialist",
			},
			Body:       []byte("You are a code reviewer."),
			SourcePack: "pack-a",
			SourcePath: "/packs/pack-a/agents/reviewer.md",
		},
	}

	regs, _ := addNativeAgents(&f, agents, nativeAgentRenderContext{
		AgentsDir:    "/project/.codex/agents",
		SkillsBase:   "/project",
		SkillsSubDir: filepath.Join(".agents", "skills"),
		Namespaced:   true,
	})

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".codex", "agents", "reviewer__aipack__pack-a.toml")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "name = 'reviewer__aipack__pack-a'") {
		t.Fatalf("expected namespaced name in TOML:\n%s", content)
	}
	if _, ok := regs["reviewer"]; ok {
		t.Fatal("registration should use rendered namespaced key")
	}
	reg, ok := regs["reviewer__aipack__pack-a"]
	if !ok {
		t.Fatalf("expected namespaced registration, got %v", regs)
	}
	if reg["config_file"] != wantDst {
		t.Fatalf("config_file = %v", reg["config_file"])
	}
}

func TestNativeAgents_NamespacedSkillRefsUseAgentPack(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{
			Name: "reviewer",
			Frontmatter: domain.AgentFrontmatter{
				Name:   "reviewer",
				Skills: []string{"deploy"},
			},
			Body:       []byte("You are a code reviewer."),
			SourcePack: "pack-a",
			SourcePath: "/packs/pack-a/agents/reviewer.md",
		},
	}
	skills := []domain.Skill{
		{Name: "deploy", SourcePack: "pack-b", DirPath: "/packs/pack-b/skills/deploy"},
		{Name: "deploy", SourcePack: "pack-a", DirPath: "/packs/pack-a/skills/deploy"},
	}

	_, warnings := addNativeAgents(&f, agents, nativeAgentRenderContext{
		AgentsDir:    "/project/.codex/agents",
		Skills:       skills,
		SkillsBase:   "/project",
		SkillsSubDir: filepath.Join(".agents", "skills"),
		Namespaced:   true,
	})

	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	content := string(f.Writes[0].Content)
	if !strings.Contains(content, filepath.Join("/project", ".agents", "skills", "deploy__aipack__pack-a")) {
		t.Fatalf("expected agent skill config to reference pack-a skill:\n%s", content)
	}
	if strings.Contains(content, "deploy__aipack__pack-b") {
		t.Fatalf("agent skill config referenced wrong pack:\n%s", content)
	}
}

func TestPromoteWorkflow_EmptyBody_Skipped(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{Name: "empty", Body: []byte("  \n  "), SourcePack: "pack-a"},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), false, workflows)

	if len(f.Writes) != 0 {
		t.Fatalf("expected 0 writes for empty body, got %d", len(f.Writes))
	}
}

func TestPromoteWorkflow_FallbackDescription(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{Name: "deploy", Body: []byte("deploy steps"), SourcePack: "pack-a"},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), false, workflows)

	content := string(f.Writes[0].Content)
	if !strings.Contains(content, "[Workflow] Workflow: deploy") {
		t.Fatalf("expected prefixed fallback description, got:\n%s", content)
	}
}

func TestPromoteWorkflow_DescriptionPrefix_NotDuplicated(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{
			Name:        "check",
			Frontmatter: domain.WorkflowFrontmatter{Name: "check", Description: "[Workflow] already prefixed"},
			Body:        []byte("steps here"),
			SourcePack:  "pack-a",
		},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), false, workflows)

	content := string(f.Writes[0].Content)
	if strings.Contains(content, "[Workflow] [Workflow]") {
		t.Fatal("description prefix was doubled")
	}
	if !strings.Contains(content, "[Workflow] already prefixed") {
		t.Fatalf("expected single prefix, got:\n%s", content)
	}
}

func TestBuildPromotedMD_Format(t *testing.T) {
	t.Parallel()
	fm := harness.PromotedFrontmatter{
		Name:        "my-skill",
		Description: "Does something useful",
		SourceType:  "agent",
		Tools:       []string{"read"},
	}
	got := harness.BuildPromotedMD(fm, "# Instructions\n\nDo the thing.")
	if !strings.Contains(got, "name: my-skill") {
		t.Fatal("expected name")
	}
	if !strings.Contains(got, "description: Does something useful") {
		t.Fatal("expected description")
	}
	if !strings.Contains(got, "source_type: agent") {
		t.Fatal("expected source_type")
	}
	if !strings.Contains(got, "- read") {
		t.Fatal("expected tools list")
	}
	if !strings.Contains(got, "# Instructions") {
		t.Fatal("expected body")
	}
	if !strings.HasPrefix(got, "---\n") {
		t.Fatal("expected YAML frontmatter opening")
	}
}

func TestBuildPromotedMD_PlainSkill(t *testing.T) {
	t.Parallel()
	fm := harness.PromotedFrontmatter{
		Name:        "deploy",
		Description: "Deploy to prod",
	}
	got := harness.BuildPromotedMD(fm, "Deploy steps here.")
	if strings.Contains(got, "source_type") {
		t.Fatal("plain skill should not have source_type")
	}
	if strings.Contains(got, "tools") {
		t.Fatal("plain skill should not have tools")
	}
}

// --- Capture promoted content tests ---

func TestCapturePromoted_Agent(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	// Write a promoted agent SKILL.md with enriched frontmatter.
	skillDir := filepath.Join(projectDir, ".agents", "skills", "reviewer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: reviewer\ndescription: Code review specialist\nsource_type: agent\ntools:\n    - read\n    - grep\ndisallowed_tools:\n    - bash\nskills:\n    - code-review\nmcp_servers:\n    - atlassian\n---\n\nYou are a code reviewer.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Should be captured as an agent, not a skill.
	if len(res.Skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(res.Skills))
	}
	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(res.Agents))
	}

	a := res.Agents[0]
	if a.Name != "reviewer" {
		t.Fatalf("agent name: got %q want %q", a.Name, "reviewer")
	}
	if a.Frontmatter.Description != "Code review specialist" {
		t.Fatalf("agent description: got %q", a.Frontmatter.Description)
	}
	if len(a.Frontmatter.Tools) != 2 || a.Frontmatter.Tools[0] != "read" {
		t.Fatalf("agent tools: got %v", a.Frontmatter.Tools)
	}
	if len(a.Frontmatter.DisallowedTools) != 1 || a.Frontmatter.DisallowedTools[0] != "bash" {
		t.Fatalf("agent disallowed_tools: got %v", a.Frontmatter.DisallowedTools)
	}
	if len(a.Frontmatter.Skills) != 1 || a.Frontmatter.Skills[0] != "code-review" {
		t.Fatalf("agent skills: got %v", a.Frontmatter.Skills)
	}
	if len(a.Frontmatter.MCPServers) != 1 || a.Frontmatter.MCPServers[0] != "atlassian" {
		t.Fatalf("agent mcp_servers: got %v", a.Frontmatter.MCPServers)
	}

	// Should have a WriteAction targeting agents/ directory with content flags.
	found := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("agents", "reviewer.md") {
			found = true
			if !w.IsContent {
				t.Error("promoted agent WriteAction should have IsContent=true")
			}
			if w.SourceDigest == "" {
				t.Error("promoted agent WriteAction should have SourceDigest set")
			}
			if w.SourceDigest != domain.SingleFileDigest([]byte(content)) {
				t.Errorf("SourceDigest = %q, want digest of SKILL.md content", w.SourceDigest)
			}
		}
	}
	if !found {
		t.Fatalf("expected WriteAction for agents/reviewer.md; got writes: %v", writeDsts(res.Writes))
	}
}

func TestCapturePromoted_Workflow(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	skillDir := filepath.Join(projectDir, ".agents", "skills", "remediate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: remediate\ndescription: StarFix remediation\nsource_type: workflow\n---\n\n## Steps\n\n1. Run remediate\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(res.Skills))
	}
	if len(res.Workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(res.Workflows))
	}

	wf := res.Workflows[0]
	if wf.Name != "remediate" {
		t.Fatalf("workflow name: got %q", wf.Name)
	}
	if wf.Frontmatter.Description != "StarFix remediation" {
		t.Fatalf("workflow description: got %q", wf.Frontmatter.Description)
	}

	found := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("workflows", "remediate.md") {
			found = true
			if !w.IsContent {
				t.Error("promoted workflow WriteAction should have IsContent=true")
			}
			if w.SourceDigest == "" {
				t.Error("promoted workflow WriteAction should have SourceDigest set")
			}
		}
	}
	if !found {
		t.Fatalf("expected WriteAction for workflows/remediate.md; got writes: %v", writeDsts(res.Writes))
	}
}

func TestCapturePromoted_WorkflowStripsRenderedContentName(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	skillDir := filepath.Join(projectDir, ".agents", "skills", "remediate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: remediate__aipack__pack-a\ndescription: StarFix remediation\nsource_type: workflow\n---\n\n## Steps\n\n1. Run remediate\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		KnownPacks: map[string]struct{}{"pack-a": {}},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(res.Workflows))
	}
	wf := res.Workflows[0]
	if wf.Name != "remediate" || wf.Frontmatter.Name != "remediate" {
		t.Fatalf("workflow names = %q/%q, want remediate/remediate", wf.Name, wf.Frontmatter.Name)
	}
}

func TestCapturePromoted_PlainSkill(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	// Plain skill — no source_type in frontmatter.
	skillDir := filepath.Join(projectDir, ".agents", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: deploy\ndescription: Deploy to prod\n---\n\n# Deploy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(res.Agents))
	}
	if len(res.Workflows) != 0 {
		t.Fatalf("expected 0 workflows, got %d", len(res.Workflows))
	}
	if len(res.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(res.Skills))
	}
	if res.Skills[0].Name != "deploy" {
		t.Fatalf("skill name: got %q", res.Skills[0].Name)
	}
}

func TestCapturePromoted_PlainSkillStripsRenderedContentName(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	skillDir := filepath.Join(projectDir, ".agents", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nname: deploy__aipack__pack-a\ndescription: Deploy to prod\n---\n\n# Deploy\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "helper.md"), []byte("helper\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		KnownPacks: map[string]struct{}{"pack-a": {}},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Skills) != 1 || res.Skills[0].Name != "deploy" {
		t.Fatalf("skills = %v, want deploy", res.Skills)
	}

	foundSkillWrite := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("skills", "deploy", "SKILL.md") {
			foundSkillWrite = true
			if !strings.Contains(string(w.Content), "name: deploy") {
				t.Fatalf("captured SKILL.md did not strip namespace:\n%s", string(w.Content))
			}
			if w.SourceDigest != domain.SingleFileDigest(raw) {
				t.Fatalf("SourceDigest = %q, want raw harness digest", w.SourceDigest)
			}
		}
	}
	if !foundSkillWrite {
		t.Fatalf("expected WriteAction for skills/deploy/SKILL.md; got %v", writeDsts(res.Writes))
	}

	foundAssetCopy := false
	for _, c := range res.Copies {
		if c.Dst == filepath.Join("skills", "deploy", "helper.md") && c.Kind == domain.CopyKindFile {
			foundAssetCopy = true
		}
	}
	if !foundAssetCopy {
		t.Fatalf("expected helper asset CopyAction; got %v", res.Copies)
	}
}

func TestNativeAgent_RoundTrip(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	// Render an agent via Plan, write the output, then Capture and verify.
	agent := domain.Agent{
		Name: "tester",
		Frontmatter: domain.AgentFrontmatter{
			Name:        "tester",
			Description: "Test runner agent",
			Harness: map[string]map[string]any{
				"codex": {"model": "o3", "model_reasoning_effort": "medium"},
			},
		},
		Body:       []byte("You run tests."),
		SourcePack: "pack-a",
		SourcePath: "/packs/pack-a/agents/tester.md",
	}

	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{Agents: []domain.Agent{agent}}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Write native agent TOML to disk.
	for _, w := range f.Writes {
		dir := filepath.Dir(w.Dst)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(w.Dst, w.Content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Capture back.
	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 1 {
		t.Fatalf("round-trip: expected 1 agent, got %d agents, %d skills", len(res.Agents), len(res.Skills))
	}
	a := res.Agents[0]
	if a.Name != "tester" {
		t.Fatalf("round-trip agent name: got %q", a.Name)
	}
	if a.Frontmatter.Description != "Test runner agent" {
		t.Fatalf("round-trip agent description: got %q", a.Frontmatter.Description)
	}
	if string(a.Body) != "You run tests." {
		t.Fatalf("round-trip agent body: got %q", string(a.Body))
	}
	// Harness-specific fields should round-trip via the harness.codex map.
	codex, ok := a.Frontmatter.Harness["codex"]
	if !ok {
		t.Fatal("round-trip: expected harness.codex config")
	}
	if codex["model"] != "o3" {
		t.Fatalf("round-trip model: got %v", codex["model"])
	}
}

func TestCapture_NativeAgent_WithMCPServers(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentTOML := `name = "investigator"
description = "Investigates issues"
developer_instructions = "You investigate."
model = "o3"

[mcp_servers.deploy-tool]
enabled = true
command = "deploy-tool"
args = ["serve"]
startup_timeout_sec = 10
`
	if err := os.WriteFile(filepath.Join(agentsDir, "investigator.toml"), []byte(agentTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(res.Agents))
	}
	a := res.Agents[0]
	if a.Name != "investigator" {
		t.Fatalf("name = %q", a.Name)
	}
	// MCP server names should be extracted.
	if len(a.Frontmatter.MCPServers) != 1 || a.Frontmatter.MCPServers[0] != "deploy-tool" {
		t.Fatalf("mcp_servers = %v", a.Frontmatter.MCPServers)
	}
	// model should be in harness.codex.
	codex := a.Frontmatter.Harness["codex"]
	if codex["model"] != "o3" {
		t.Fatalf("model = %v", codex["model"])
	}
}

func TestCapture_NativeAgent_StripsNamespacedName(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentTOML := `name = "investigator__aipack__pack-a"
description = "Investigates issues"
developer_instructions = "You investigate."
`
	src := filepath.Join(agentsDir, "investigator__aipack__pack-a.toml")
	if err := os.WriteFile(src, []byte(agentTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		KnownPacks: map[string]struct{}{"pack-a": {}},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(res.Agents))
	}
	a := res.Agents[0]
	if a.Name != "investigator" || a.Frontmatter.Name != "investigator" {
		t.Fatalf("agent names = %q/%q, want investigator/investigator", a.Name, a.Frontmatter.Name)
	}
	found := false
	for _, w := range res.Writes {
		if filepath.ToSlash(w.Dst) == "agents/investigator.md" {
			found = true
			if w.Src != src {
				t.Fatalf("write src = %q, want %q", w.Src, src)
			}
		}
	}
	if !found {
		t.Fatalf("expected WriteAction for agents/investigator.md; got writes: %v", writeDsts(res.Writes))
	}
}

func TestCapture_NativeAgent_MalformedTOML_WarnsDoesNotCrash(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write malformed TOML.
	if err := os.WriteFile(filepath.Join(agentsDir, "broken.toml"), []byte("this is not valid toml [[["), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also write a valid one to verify partial capture.
	validTOML := `name = "valid"
description = "Valid agent"
developer_instructions = "Valid body."
`
	if err := os.WriteFile(filepath.Join(agentsDir, "valid.toml"), []byte(validTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture should not error on malformed agent: %v", err)
	}

	// Should have captured the valid agent.
	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 valid agent, got %d", len(res.Agents))
	}
	if res.Agents[0].Name != "valid" {
		t.Fatalf("expected valid agent, got %q", res.Agents[0].Name)
	}
	// Should have a warning about the broken one.
	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "parse native agent TOML") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatal("expected warning about malformed agent TOML")
	}
}

func TestCapture_NativeAgent_IgnoresNonTOMLFiles(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a .md file and a subdirectory — both should be ignored.
	if err := os.WriteFile(filepath.Join(agentsDir, "readme.md"), []byte("# Ignore me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentsDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("expected 0 agents from non-TOML files, got %d", len(res.Agents))
	}
}

func TestCapture_NativeAgent_MinimalTOML_NoNameOrDescription(t *testing.T) {
	// Real-world Codex agent TOMLs often contain only model_reasoning_effort
	// and developer_instructions — no name or description (those live in the
	// config.toml registration). Capture should handle this gracefully,
	// deriving the name from the filename.
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal TOML matching real-world agent_roles format.
	minimalTOML := "model_reasoning_effort = \"xhigh\"\n\ndeveloper_instructions = \"\"\"\nYou are the Explorer role.\n\nMission:\n- Read the codebase heavily and build strong context.\n\"\"\"\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "explorer.toml"), []byte(minimalTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(res.Agents))
	}
	a := res.Agents[0]
	// Name should be derived from filename when missing.
	if a.Name != "explorer" {
		t.Fatalf("name = %q, want explorer (from filename)", a.Name)
	}
	// Description should be empty (not present in TOML).
	if a.Frontmatter.Description != "" {
		t.Fatalf("description should be empty, got %q", a.Frontmatter.Description)
	}
	// Body should contain the developer_instructions.
	if !strings.Contains(string(a.Body), "Explorer role") {
		t.Fatalf("body = %q, expected Explorer role content", string(a.Body))
	}
	// model_reasoning_effort should be in harness.codex.
	codex, ok := a.Frontmatter.Harness["codex"]
	if !ok {
		t.Fatal("expected harness.codex config")
	}
	if codex["model_reasoning_effort"] != "xhigh" {
		t.Fatalf("reasoning_effort = %v", codex["model_reasoning_effort"])
	}
}

func TestCapture_NativeAgent_MultipleRealWorldAgents(t *testing.T) {
	// Simulate a real multi-agent setup with different reasoning efforts.
	t.Parallel()
	projectDir := t.TempDir()

	agentsDir := filepath.Join(projectDir, ".codex", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	agents := map[string]string{
		"explorer.toml":    "model_reasoning_effort = \"xhigh\"\ndeveloper_instructions = \"You are the Explorer.\"\n",
		"code_writer.toml": "model_reasoning_effort = \"high\"\ndeveloper_instructions = \"You write code.\"\n",
		"reviewer.toml":    "model_reasoning_effort = \"xhigh\"\ndeveloper_instructions = \"You review PRs.\"\n",
	}
	for name, content := range agents {
		if err := os.WriteFile(filepath.Join(agentsDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(res.Agents))
	}

	// Verify each agent was captured with correct reasoning effort.
	byName := map[string]domain.Agent{}
	for _, a := range res.Agents {
		byName[a.Name] = a
	}

	explorer, ok := byName["explorer"]
	if !ok {
		t.Fatal("missing explorer")
	}
	if explorer.Frontmatter.Harness["codex"]["model_reasoning_effort"] != "xhigh" {
		t.Fatal("explorer should have xhigh reasoning")
	}

	writer, ok := byName["code_writer"]
	if !ok {
		t.Fatal("missing code_writer")
	}
	if writer.Frontmatter.Harness["codex"]["model_reasoning_effort"] != "high" {
		t.Fatal("code_writer should have high reasoning")
	}

	reviewer, ok := byName["reviewer"]
	if !ok {
		t.Fatal("missing reviewer")
	}
	if string(reviewer.Body) != "You review PRs." {
		t.Fatalf("reviewer body = %q", string(reviewer.Body))
	}
}

// TestCaptureNativeAgents_FlatOnly verifies that captureNativeAgents reads
// only direct child .toml files of agentsDir and ignores any TOML nested
// in subdirectories — agents are flat by definition (id is the harness
// invocation handle and must equal the file basename).
func TestCaptureNativeAgents_FlatOnly(t *testing.T) {
	t.Parallel()
	agentsDir := t.TempDir()

	writeTOML := func(rel, content string) {
		t.Helper()
		full := filepath.Join(agentsDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Flat agent — captured.
	writeTOML("flat.toml", `name = "flat"
description = "Flat agent"
developer_instructions = "flat body"
`)
	// Agent with name omitted — name falls back to file basename.
	writeTOML("explorer.toml", `description = "No name field"
developer_instructions = "explorer body"
`)
	// Nested TOML — must be silently ignored.
	writeTOML("group/should-skip.toml", `name = "should-skip"
description = "nested — must be ignored"
developer_instructions = "body"
`)
	// Non-TOML file — must be ignored even if it contains a name key.
	writeTOML("README.md", `name = "ignore me"`)

	res := &harness.CaptureResult{}
	captureNativeAgents(agentsDir, nil, res)

	names := map[string]bool{}
	for _, a := range res.Agents {
		names[a.Name] = true
	}
	for _, want := range []string{"flat", "explorer"} {
		if !names[want] {
			t.Errorf("missing agent name %q; got %v", want, names)
		}
	}
	if len(res.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d: %v", len(res.Agents), names)
	}
	if names["should-skip"] {
		t.Errorf("nested TOML was captured; got %v", names)
	}

	gotWriteDsts := map[string]bool{}
	for _, w := range res.Writes {
		gotWriteDsts[filepath.ToSlash(w.Dst)] = true
	}
	for _, want := range []string{"agents/flat.md", "agents/explorer.md"} {
		if !gotWriteDsts[want] {
			t.Errorf("missing Write for %q; got %v", want, gotWriteDsts)
		}
	}
}

// --- Helpers ---

func codexHookStateFromTOML(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := toml.Unmarshal(content, &root); err != nil {
		t.Fatalf("unmarshal config.toml: %v\n%s", err, content)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks table missing in:\n%s", content)
	}
	state, ok := hooks["state"].(map[string]any)
	if !ok {
		t.Fatalf("hooks.state table missing in:\n%s", content)
	}
	return state
}

func writeDsts(writes []domain.WriteAction) []string {
	var out []string
	for _, w := range writes {
		out = append(out, w.Dst)
	}
	return out
}
