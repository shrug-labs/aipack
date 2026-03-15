package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestPlan_Project_Rules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{
						Name:       "no-secrets",
						Raw:        []byte("---\npaths:\n  - \"**/*.go\"\n---\nNever commit secrets.\n"),
						SourcePack: "test-pack",
					},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Writes) != 1 {
		t.Fatalf("writes: got %d want 1", len(f.Writes))
	}
	if !strings.HasSuffix(f.Writes[0].Dst, filepath.Join(".claude", "rules", "no-secrets.md")) {
		t.Errorf("rule dst: got %q", f.Writes[0].Dst)
	}
}

func TestPlan_Project_TransformsAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Agents: []domain.Agent{
					{
						Name: "readonly",
						Frontmatter: domain.AgentFrontmatter{
							Description:     "Read-only agent",
							Tools:           []string{"atlassian_jira_get_issue", "atlassian_confluence_search"},
							DisallowedTools: []string{"write", "edit"},
							Skills:          []string{"triage"},
							MCPServers:      []string{"atlassian"},
						},
						Body: []byte("System prompt body.\n"),
					},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Writes) != 1 {
		t.Fatalf("writes: got %d want 1", len(f.Writes))
	}

	w := f.Writes[0]
	if !strings.HasSuffix(w.Dst, filepath.Join(".claude", "agents", "readonly.md")) {
		t.Errorf("agent dst: got %q", w.Dst)
	}

	out := string(w.Content)
	if !strings.Contains(out, "name: readonly") {
		t.Error("missing name derived from filename")
	}
	// MCP tools filtered out when mcpServers is set — tools: should be omitted
	if strings.Contains(out, "tools:") {
		t.Errorf("tools: should be omitted when all tools are MCP tools covered by mcpServers, got:\n%s", out)
	}
	if !strings.Contains(out, "disallowedTools: Write, Edit") {
		t.Errorf("disallowedTools should be PascalCase, got:\n%s", out)
	}
	if !strings.Contains(out, "System prompt body.") {
		t.Error("body not preserved")
	}
}

func TestPlan_Project_Skills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	skillDir := filepath.Join(t.TempDir(), "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Skills: []domain.Skill{
					{Name: "my-skill", DirPath: skillDir, SourcePack: "test"},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Copies) != 1 {
		t.Fatalf("copies: got %d want 1", len(f.Copies))
	}
	if !strings.HasSuffix(f.Copies[0].Dst, filepath.Join(".claude", "skills", "my-skill")) {
		t.Errorf("skill dst: got %q", f.Copies[0].Dst)
	}
}

func TestPlan_Project_WorkflowsAsCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Workflows: []domain.Workflow{
					{
						Name:       "deploy",
						Raw:        []byte("# Deploy"),
						SourcePack: "test",
					},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Writes) != 1 {
		t.Fatalf("writes: got %d want 1", len(f.Writes))
	}
	if !strings.HasSuffix(f.Writes[0].Dst, filepath.Join(".claude", "commands", "deploy.md")) {
		t.Errorf("workflow dst: got %q", f.Writes[0].Dst)
	}
}

func TestPlan_Project_EmptyContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile:   domain.Profile{},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Writes) != 0 || len(f.Copies) != 0 {
		t.Errorf("expected no writes/copies for empty content, got %d writes %d copies", len(f.Writes), len(f.Copies))
	}
}

func TestPlan_Project_NoManagedMd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "rule", Raw: []byte("Some rule"), SourcePack: "test"},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, w := range f.Writes {
		if strings.Contains(w.Dst, managedFile) {
			t.Errorf("should not write %s, got write to %s", managedFile, w.Dst)
		}
	}
	for _, c := range f.Copies {
		if strings.Contains(c.Dst, managedFile) {
			t.Errorf("should not copy %s, got copy to %s", managedFile, c.Dst)
		}
	}
}

func TestPlan_Global_WritesToGlobalDirs(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "global-rule", Raw: []byte("Global rule"), SourcePack: "test"},
				},
				Workflows: []domain.Workflow{
					{Name: "global-cmd", Raw: []byte("# Global command"), SourcePack: "test"},
				},
				Skills: []domain.Skill{
					{Name: "global-skill", DirPath: filepath.Join(t.TempDir(), "global-skill"), SourcePack: "test"},
				},
			}},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Rules → ~/.claude/rules/.
	var hasRule bool
	for _, w := range f.Writes {
		if strings.Contains(w.Dst, filepath.Join(".claude", "rules", "global-rule.md")) {
			hasRule = true
		}
	}
	if !hasRule {
		t.Error("expected rule written to ~/.claude/rules/")
	}

	// Workflows → ~/.claude/commands/.
	var hasCmd bool
	for _, w := range f.Writes {
		if strings.Contains(w.Dst, filepath.Join(".claude", "commands", "global-cmd.md")) {
			hasCmd = true
		}
	}
	if !hasCmd {
		t.Error("expected workflow written to ~/.claude/commands/")
	}

	// Skills → ~/.claude/skills/.
	var hasSkill bool
	for _, c := range f.Copies {
		if strings.Contains(c.Dst, filepath.Join(".claude", "skills", "global-skill")) {
			hasSkill = true
		}
	}
	if !hasSkill {
		t.Error("expected skill copied to ~/.claude/skills/")
	}
}

func TestPlan_Project_MCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{
					Name:         "atlassian",
					Command:      []string{"npx", "atlassian-mcp"},
					Env:          map[string]string{"API_KEY": "val"},
					AllowedTools: []string{"jira_get_issue"},
				},
			},
		},
		SkipSettings: true,
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// MCP always syncs as MCP action (not gated by --skip-settings).
	if len(f.MCP) < 1 {
		t.Fatal("expected at least 1 MCP action")
	}

	var hasMCP, hasSettings bool
	for _, p := range f.MCP {
		if p.Label == ".mcp.json" {
			hasMCP = true
			if !p.MergeMode {
				t.Error(".mcp.json action should use MergeMode to preserve non-managed keys")
			}
		}
		if strings.Contains(p.Label, "settings.local.json") {
			hasSettings = true
		}
	}
	if !hasMCP {
		t.Error("expected .mcp.json as MCP action")
	}
	// When skip-settings but MCP exists, settings permissions go as MCP action too.
	if !hasSettings {
		t.Error("expected settings.local.json as MCP action when skip-settings + MCP servers exist")
	}
	// No Settings entries when skip-settings.
	if len(f.Settings) != 0 {
		t.Errorf("expected no Settings when skip-settings, got %d", len(f.Settings))
	}
	if len(f.MCPServers) != 1 {
		t.Fatalf("expected 1 MCP server action, got %d", len(f.MCPServers))
	}
	var tracked domain.MCPServer
	if err := json.Unmarshal(f.MCPServers[0].Content, &tracked); err != nil {
		t.Fatalf("unmarshal tracked MCP action: %v", err)
	}
	if tracked.Timeout != 0 {
		t.Fatalf("tracked timeout: got %d want 0 (Claude render/capture omits timeout)", tracked.Timeout)
	}
	if tracked.Transport != domain.TransportStdio {
		t.Fatalf("tracked transport: got %q want %q", tracked.Transport, domain.TransportStdio)
	}
}

func TestPlan_Project_SettingsWhenNotSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{
					Name:         "atlassian",
					Command:      []string{"npx", "atlassian-mcp"},
					AllowedTools: []string{"jira_get_issue"},
				},
			},
		},
		SkipSettings: false,
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Settings should be in Settings (not Plugins) when not skipped.
	if len(f.Settings) != 1 {
		t.Fatalf("expected 1 settings action, got %d", len(f.Settings))
	}
	if !f.Settings[0].MergeMode {
		t.Error("settings should use MergeMode")
	}
}

func TestPlan_Project_BaseSettingsMergedWithMCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	base := []byte(`{
  "permissions": {
    "allow": ["Bash(go test:*)"]
  }
}`)

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "svc", Command: []string{"svc"}, AllowedTools: []string{"tool1"}},
			},
			BaseSettings: domain.SettingsBundle{
				domain.HarnessClaudeCode: []domain.ConfigFile{
					{Filename: "settings.local.json", Content: base, SourcePack: "test"},
				},
			},
			SettingsPack: "test",
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Settings) != 1 {
		t.Fatalf("expected 1 settings action, got %d", len(f.Settings))
	}

	var got map[string]any
	if err := json.Unmarshal(f.Settings[0].Desired, &got); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	perms, _ := got["permissions"].(map[string]any)
	if perms == nil {
		t.Fatal("missing permissions in rendered settings")
	}
	allow, _ := perms["allow"].([]any)
	if len(allow) != 2 {
		t.Fatalf("allow: got %v want 2 entries", allow)
	}
	if allow[0] != "Bash(go test:*)" {
		t.Errorf("allow[0]: got %v want base permission", allow[0])
	}
	if allow[1] != "mcp__svc__tool1" {
		t.Errorf("allow[1]: got %v want MCP permission", allow[1])
	}
}

func TestPlan_Project_BaseSettingsOnlyNoMCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	base := []byte(`{
  "permissions": {
    "allow": ["Bash(go test:*)"]
  }
}`)

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: dir,
		Profile: domain.Profile{
			BaseSettings: domain.SettingsBundle{
				domain.HarnessClaudeCode: []domain.ConfigFile{
					{Filename: "settings.local.json", Content: base, SourcePack: "test"},
				},
			},
			SettingsPack: "test",
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Base settings alone should emit settings even without MCP servers.
	if len(f.Settings) != 1 {
		t.Fatalf("expected 1 settings action for base-only, got %d", len(f.Settings))
	}

	var got map[string]any
	if err := json.Unmarshal(f.Settings[0].Desired, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms := got["permissions"].(map[string]any)
	allow := perms["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(go test:*)" {
		t.Errorf("allow: got %v want [Bash(go test:*)]", allow)
	}
}

func TestPlan_Global_MCPMergeMode(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "atlassian", Command: []string{"npx", "atlassian-mcp"}},
			},
		},
	}

	f, err := h.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Global MCP targets ~/.claude.json which contains other Claude Code state.
	// MergeMode must be true to preserve non-managed keys.
	var found bool
	for _, p := range f.MCP {
		if p.Label == ".claude.json" {
			found = true
			if !p.MergeMode {
				t.Error(".claude.json action must use MergeMode to preserve existing settings")
			}
		}
	}
	if !found {
		t.Error("expected .claude.json MCP action for global scope")
	}
}

func TestCapture_Project(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up .claude/ structure.
	for _, subdir := range []string{"rules", "agents", "commands"} {
		if err := os.MkdirAll(filepath.Join(dir, ".claude", subdir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "rules", "rule1.md"), []byte("Rule 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "agents", "agent1.md"), []byte("---\nname: agent1\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "commands", "cmd1.md"), []byte("# Command"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Harness{}
	res, err := h.Capture(harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: dir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Rules) == 0 {
		t.Error("expected Rules to be populated")
	}
	if len(res.Agents) == 0 {
		t.Error("expected Agents to be populated")
	}
	if len(res.Workflows) == 0 {
		t.Error("expected Workflows to be populated")
	}

	// Verify copy destinations.
	var ruleFound, agentFound, cmdFound bool
	for _, c := range res.Copies {
		switch {
		case strings.HasPrefix(c.Dst, "rules/"):
			ruleFound = true
		case strings.HasPrefix(c.Dst, "agents/"):
			agentFound = true
		case strings.HasPrefix(c.Dst, "workflows/"):
			cmdFound = true
		}
	}
	if !ruleFound {
		t.Error("expected rule copy")
	}
	if !agentFound {
		t.Error("expected agent copy")
	}
	if !cmdFound {
		t.Error("expected workflow copy")
	}

	// Verify typed fields are populated.
	if len(res.Rules) != 1 {
		t.Errorf("typed Rules = %d, want 1", len(res.Rules))
	} else if res.Rules[0].Name != "rule1" {
		t.Errorf("Rule.Name = %q, want %q", res.Rules[0].Name, "rule1")
	}
	if len(res.Agents) != 1 {
		t.Errorf("typed Agents = %d, want 1", len(res.Agents))
	} else if res.Agents[0].Name != "agent1" {
		t.Errorf("Agent.Name = %q, want %q", res.Agents[0].Name, "agent1")
	}
	if len(res.Workflows) != 1 {
		t.Errorf("typed Workflows = %d, want 1", len(res.Workflows))
	} else if res.Workflows[0].Name != "cmd1" {
		t.Errorf("Workflow.Name = %q, want %q", res.Workflows[0].Name, "cmd1")
	}
}

func TestCapture_Global(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	// Set up ~/.claude/ structure.
	for _, subdir := range []string{"rules", "agents", "commands"} {
		if err := os.MkdirAll(filepath.Join(home, ".claude", subdir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "rules", "r1.md"), []byte("Global rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "agents", "a1.md"), []byte("---\nname: a1\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Harness{}
	res, err := h.Capture(harness.CaptureContext{
		Scope: domain.ScopeGlobal,
		Home:  home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Rules) != 1 {
		t.Errorf("Rules = %d, want 1", len(res.Rules))
	}
	if len(res.Agents) != 1 {
		t.Errorf("Agents = %d, want 1", len(res.Agents))
	}
}

func TestCapture_Project_ParsesSettingsPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := []byte(`{
	  "mcpServers": {
	    "jira": {
	      "command": "npx",
	      "args": ["jira-mcp"]
	    }
	  }
	}`)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{
	  "permissions": {
	    "allow": ["Bash(go test:*)", "mcp__jira__get_issue"],
	    "deny": ["mcp__jira__delete_issue"]
	  }
	}`)
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(harness.CaptureContext{Scope: domain.ScopeProject, ProjectDir: dir})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got := res.AllowedTools["jira"]; len(got) != 1 || got[0] != "get_issue" {
		t.Fatalf("AllowedTools[jira] = %v, want [get_issue]", got)
	}
	srv, ok := res.MCPServers["jira"]
	if !ok {
		t.Fatalf("expected jira MCP server, got %+v", res.MCPServers)
	}
	// Verify parseMCPJSON correctly unwraps the mcpServers envelope.
	if len(srv.Command) != 2 || srv.Command[0] != "npx" || srv.Command[1] != "jira-mcp" {
		t.Fatalf("Command = %v, want [npx jira-mcp]", srv.Command)
	}
	if len(srv.DisabledTools) != 1 || srv.DisabledTools[0] != "delete_issue" {
		t.Fatalf("DisabledTools = %v, want [delete_issue]", srv.DisabledTools)
	}
	if len(res.Writes) != 1 {
		t.Fatalf("Writes = %d, want 1", len(res.Writes))
	}
	var root map[string]any
	if err := json.Unmarshal(res.Writes[0].Content, &root); err != nil {
		t.Fatalf("unmarshal captured settings: %v", err)
	}
	if root["permissions"] == nil {
		t.Fatal("expected captured settings.local.json to be preserved")
	}
}

func TestManagedRoots_Project(t *testing.T) {
	t.Parallel()
	h := Harness{}
	roots := h.ManagedRoots(domain.ScopeProject, "/proj", "/home")

	// Should include rules, agents, skills, commands, CLAUDE.managed.md, .mcp.json, settings.local.json.
	if len(roots) < 5 {
		t.Errorf("expected at least 5 managed roots, got %d: %v", len(roots), roots)
	}

	var hasRules, hasAgents, hasMCP bool
	for _, r := range roots {
		if strings.Contains(r, "rules") {
			hasRules = true
		}
		if strings.Contains(r, "agents") {
			hasAgents = true
		}
		if strings.Contains(r, ".mcp.json") {
			hasMCP = true
		}
	}
	if !hasRules || !hasAgents || !hasMCP {
		t.Errorf("missing expected managed roots: rules=%v agents=%v mcp=%v", hasRules, hasAgents, hasMCP)
	}
}

func TestManagedRoots_Global(t *testing.T) {
	t.Parallel()
	h := Harness{}
	roots := h.ManagedRoots(domain.ScopeGlobal, "/home/user", "/home/user")

	if len(roots) < 4 {
		t.Errorf("expected at least 4 global managed roots, got %d: %v", len(roots), roots)
	}

	var hasRules, hasAgents bool
	for _, r := range roots {
		if strings.Contains(r, "rules") {
			hasRules = true
		}
		if strings.Contains(r, "agents") {
			hasAgents = true
		}
	}
	if !hasRules || !hasAgents {
		t.Errorf("missing expected global roots: rules=%v agents=%v", hasRules, hasAgents)
	}
}
