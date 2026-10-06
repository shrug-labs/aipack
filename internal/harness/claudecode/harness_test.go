package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestPlanNativeMCPPolicy(t *testing.T) {
	source, root := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".claude-plugin/plugin.json"), []byte(`{"name":"probe","version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".mcp.json"), []byte(`{"mcpServers":{"Probe.with-dots":{"command":"echo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := plugin.MaterializeClaude(source, root, "alias", domain.PluginSource{Marketplace: "market"})
	if err != nil {
		t.Fatal(err)
	}
	m.NativePlugin.Name = "catalog-probe"
	p := domain.Profile{Packs: []domain.Pack{{Name: "alias", NativePlugin: &domain.NativePluginSelection{
		Package: *m.NativePlugin, Root: root, Selected: map[domain.PackCategory][]string{domain.CategoryMCP: m.MCP},
		MCPPolicy: map[string]domain.NativeMCPPolicy{"Probe.with-dots": {AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}},
	}}}}
	ctx := engine.SyncContext{Profile: p, Scope: domain.ScopeGlobal, TargetDir: t.TempDir(), Home: t.TempDir(), ConfigDir: t.TempDir()}
	for _, skip := range []bool{false, true} {
		ctx.SkipSettings = skip
		f, err := (Harness{}).Plan(context.Background(), ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.NativePlugins) != 1 || len(f.MCPServers) != 0 {
			t.Fatal("native connection was flattened or delivery omitted")
		}
		if f.NativePlugins[0].Package.Binding() != "catalog-probe@market" {
			t.Fatal("component namespace replaced native installation identity")
		}
		if f.NativePlugins[0].Selection != p.Packs[0].NativePlugin {
			t.Fatal("native planning lost source and profile selection inputs")
		}
		actions := f.Settings
		if skip {
			actions = f.MCP
		}
		if len(actions) != 1 || !strings.Contains(string(actions[0].Desired), "mcp__plugin_probe_Probe_with-dots__observe") || !strings.Contains(string(actions[0].Desired), "mcp__plugin_probe_Probe_with-dots__hidden") {
			t.Fatalf("native permissions missing with skip-settings=%t: %+v", skip, actions)
		}
	}
	ctx.Profile.MCPServers = []domain.MCPServer{{Name: "plugin_probe_Probe.with-dots", SourcePack: "ordinary"}}
	if _, err := (Harness{}).Plan(context.Background(), ctx); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("ordinary/native namespace collision accepted: %v", err)
	}
	ctx.Profile.MCPServers = nil
	other := *p.Packs[0].NativePlugin
	other.Package.Name = "another-binding"
	other.Package.Marketplace = "other-market"
	ctx.Profile.Packs = append(ctx.Profile.Packs, domain.Pack{Name: "other", NativePlugin: &other})
	if _, err := (Harness{}).Plan(context.Background(), ctx); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("cross-market namespace collision accepted: %v", err)
	}
}

func TestCaptureMCPPermissionsRequireConnection(t *testing.T) {
	servers := map[string]domain.MCPServer{"Probe.with-dots": {Name: "Probe.with-dots", Command: []string{"echo"}}}
	allowed := map[string][]string{}
	warnings := parseSettingsPermissions(servers, allowed, []byte(`{"permissions":{"allow":["mcp__Probe_with-dots__observe","mcp__plugin_probe_Probe__observe"],"deny":["mcp__Probe_with-dots__hidden","mcp__plugin_probe_Probe__hidden"]}}`))
	if len(warnings) != 0 || len(servers) != 1 || len(allowed) != 1 || len(allowed["Probe.with-dots"]) != 1 || len(servers["Probe.with-dots"].DisabledTools) != 1 {
		t.Fatalf("capture renamed ordinary connections or flattened native permissions: %+v %+v %+v", servers, allowed, warnings)
	}
}

func TestPlanRejectsOrdinaryMCPNamespaceCollisions(t *testing.T) {
	t.Parallel()
	ctx := engine.SyncContext{Home: t.TempDir(), TargetDir: t.TempDir(), Scope: domain.ScopeProject,
		Profile: domain.Profile{MCPServers: []domain.MCPServer{
			{Name: "service.a", SourcePack: "trusted", Command: []string{"true"}, AlwaysAllowedTools: []string{"execute"}},
			{Name: "service_a", SourcePack: "imported", Command: []string{"false"}},
		}}}
	if _, err := (Harness{}).Plan(t.Context(), ctx); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("accepted colliding approval namespaces: %v", err)
	}
	ctx.Profile.MCPServers[1].Name = "other"
	if _, err := (Harness{}).Plan(t.Context(), ctx); err != nil {
		t.Fatalf("rejected distinct namespaces: %v", err)
	}
}

func TestCaptureGlobalStateWithoutMCPServers(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"firstStartTime":"owned","migrationVersion":14,"hasReset":true,"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := (Harness{}).Capture(context.Background(), harness.CaptureContext{Scope: domain.ScopeGlobal, Home: home})
	if err != nil || len(got.Warnings) != 0 || len(got.MCPServers) != 0 {
		t.Fatalf("ordinary native state was treated as MCP configuration: %+v (%v)", got, err)
	}
}

func TestPlan_Project_Rules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h := Harness{}
	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  dir,
		Namespaced: true,
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

	f, err := h.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Writes) != 1 {
		t.Fatalf("writes: got %d want 1", len(f.Writes))
	}
	if !strings.HasSuffix(f.Writes[0].Dst, filepath.Join(".claude", "rules", "no-secrets__aipack__test-pack.md")) {
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

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Writes) != 0 || len(f.Copies) != 0 {
		t.Errorf("expected no writes/copies for empty content, got %d writes %d copies", len(f.Writes), len(f.Copies))
	}
}

func TestPlan_Project_HookOnlySkipSettingsEmitsManagedSettings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ctx := engine.SyncContext{
		Scope:        domain.ScopeProject,
		TargetDir:    dir,
		SkipSettings: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "hooks-pack",
				Hooks: []domain.Hook{{
					ID:         "audit-bash",
					SourcePack: "hooks-pack",
					Events: []domain.HookEvent{{
						On:    domain.HookEventToolBefore,
						Match: domain.HookMatch{Tool: "Bash"},
						Handler: domain.HookHandler{
							Type:    domain.HookHandlerTypeCommand,
							Command: "echo audit",
							Timeout: "5s",
						},
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
		t.Fatalf("settings = %d, want 0 with SkipSettings", len(f.Settings))
	}
	if len(f.MCP) != 1 {
		t.Fatalf("managed settings actions = %d, want 1", len(f.MCP))
	}
	var root map[string]any
	if err := json.Unmarshal(f.MCP[0].Desired, &root); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := root["hooks"].(map[string]any)
	groups := hooks["PreToolUse"].([]any)
	group := groups[0].(map[string]any)
	if group["matcher"] != "Bash" {
		t.Fatalf("matcher = %v, want Bash", group["matcher"])
	}
	handlers := group["hooks"].([]any)
	handler := handlers[0].(map[string]any)
	if handler["command"] != "echo audit" {
		t.Fatalf("command = %v, want echo audit", handler["command"])
	}
	if handler["timeout"].(float64) != 5 {
		t.Fatalf("timeout = %v, want 5", handler["timeout"])
	}
}

func TestRenderHooks_CommandWindowsOnlySkippedOnNonWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("command_windows-only hook emits the windows command on Windows")
	}
	events, _, err := RenderHooks([]domain.Hook{{
		ID:         "win-scan",
		SourcePack: "hooks-pack",
		Events: []domain.HookEvent{{
			On:    domain.HookEventToolBefore,
			Match: domain.HookMatch{Tool: "Bash"},
			Handler: domain.HookHandler{
				Type:           domain.HookHandlerTypeCommand,
				CommandWindows: "powershell ./scan.ps1",
			},
		}},
	}})
	if err != nil {
		t.Fatalf("RenderHooks: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("command_windows-only hook should be skipped on non-Windows, got events: %v", events)
	}
}

func TestRenderHooks_WildcardMatcherOmitted(t *testing.T) {
	t.Parallel()
	events, _, err := RenderHooks([]domain.Hook{{
		ID:         "all-tools",
		SourcePack: "hooks-pack",
		Events: []domain.HookEvent{{
			On:    domain.HookEventToolBefore,
			Match: domain.HookMatch{Tool: "*"},
			Handler: domain.HookHandler{
				Type:    domain.HookHandlerTypeCommand,
				Command: "echo audit",
			},
		}},
	}})
	if err != nil {
		t.Fatalf("RenderHooks: %v", err)
	}
	group := events["PreToolUse"][0].(map[string]any)
	if _, ok := group["matcher"]; ok {
		t.Fatalf("wildcard matcher should be omitted, got group: %v", group)
	}
}

func TestPlan_Global_SettingsToSettingsJSON_FoldsNativePlugins(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source, packRoot := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".claude-plugin/plugin.json"), []byte(`{"name":"linear","version":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := plugin.MaterializeClaude(source, packRoot, "p", domain.PluginSource{Marketplace: "market"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Home:      home,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "p",
				Hooks: []domain.Hook{{
					ID:         "audit-bash",
					SourcePack: "p",
					Events: []domain.HookEvent{{
						On:    domain.HookEventToolBefore,
						Match: domain.HookMatch{Tool: "Bash"},
						Handler: domain.HookHandler{
							Type:    domain.HookHandlerTypeCommand,
							Command: "echo audit",
							Timeout: "5s",
						},
					}},
				}},
				NativePlugin: &domain.NativePluginSelection{
					Package: *manifest.NativePlugin,
					Root:    packRoot,
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	localPath := filepath.Join(home, ".claude", "settings.local.json")

	// Managed hooks must target ~/.claude/settings.json; Claude Code does not
	// read a user-level settings.local.json. Exactly one action may target the
	// file — a second same-destination merge would clobber the first.
	var managed *domain.SettingsAction
	targeting := 0
	for i := range f.Settings {
		if f.Settings[i].Dst == localPath {
			t.Errorf("global managed settings written to settings.local.json (Claude Code ignores it)")
		}
		if f.Settings[i].Dst == settingsPath {
			managed = &f.Settings[i]
			targeting++
		}
	}
	for i := range f.MCP {
		if f.MCP[i].Dst == localPath {
			t.Errorf("global managed settings written to settings.local.json (Claude Code ignores it)")
		}
		if f.MCP[i].Dst == settingsPath {
			targeting++
		}
	}
	if managed == nil {
		t.Fatalf("no managed settings action targeting %s; got Settings=%+v MCP=%+v", settingsPath, f.Settings, f.MCP)
	}
	if targeting != 1 {
		t.Fatalf("expected exactly 1 action targeting %s, got %d (plugin fold failed — same-Dst actions would clobber)", settingsPath, targeting)
	}

	var root map[string]any
	if err := json.Unmarshal(managed.Desired, &root); err != nil {
		t.Fatalf("unmarshal managed settings: %v\n%s", err, managed.Desired)
	}
	if _, ok := root["hooks"].(map[string]any); !ok {
		t.Errorf("managed settings missing hooks: %v", root)
	}
	enabled, ok := root["enabledPlugins"].(map[string]any)
	if !ok || enabled["linear@market"] != true {
		t.Errorf("managed settings missing folded enabledPlugins: %v", root["enabledPlugins"])
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

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
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
			SettingsPacks: []string{"test"},
		},
	}

	f, err := h.Plan(context.Background(), ctx)
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
			SettingsPacks: []string{"test"},
		},
	}

	f, err := h.Plan(context.Background(), ctx)
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

	f, err := h.Plan(context.Background(), ctx)
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
	res, err := h.Capture(context.Background(), harness.CaptureContext{
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
		dst := filepath.ToSlash(c.Dst)
		switch {
		case strings.HasPrefix(dst, "rules/"):
			ruleFound = true
		case strings.HasPrefix(dst, "agents/"):
			agentFound = true
		case strings.HasPrefix(dst, "workflows/"):
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
	res, err := h.Capture(context.Background(), harness.CaptureContext{
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

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{Scope: domain.ScopeProject, ProjectDir: dir})
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

func TestCapture_Project_ParsesClaudeHTTPAsStreamableHTTP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mcpJSON := []byte(`{
	  "mcpServers": {
	    "remote": {
	      "type": "http",
	      "url": "https://example.invalid/mcp"
	    }
	  }
	}`)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{Scope: domain.ScopeProject, ProjectDir: dir})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	got, ok := res.MCPServers["remote"]
	if !ok {
		t.Fatalf("expected remote MCP server, got %+v", res.MCPServers)
	}
	if got.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("transport: got %q want %q", got.Transport, domain.TransportStreamableHTTP)
	}
}
