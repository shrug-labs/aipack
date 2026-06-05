package cline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/testutil"
)

// --- Plan tests ---

func TestPlan_Project_RulesAndAgents(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  projectDir,
		Home:       t.TempDir(),
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "team", Raw: []byte("# Team rules"), SourcePack: "pack-a"},
				},
				Agents: []domain.Agent{
					{Name: "reviewer", Body: []byte("Review code carefully"), SourcePack: "pack-a", SourcePath: "/pack/agents/reviewer.md"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Rule write → .clinerules/team__aipack__pack-a.md
	wantRule := filepath.Join(projectDir, ".clinerules", "team__aipack__pack-a.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	// Agent promoted to skill → .agents/skills/reviewer__aipack__pack-a/SKILL.md
	wantAgent := filepath.Join(projectDir, ".agents", "skills", "reviewer__aipack__pack-a", "SKILL.md")
	assertHasWriteDst(t, f.Writes, wantAgent)
}

func TestPlan_Project_NamespacedAgentSkillRefsUseAgentPack(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	skillADir := testutil.SkillDir(t, "deploy")
	skillBDir := testutil.SkillDir(t, "deploy")
	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  projectDir,
		Home:       t.TempDir(),
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{
				{
					Name: "pack-b",
					Skills: []domain.Skill{
						{Name: "deploy", DirPath: skillBDir, SourcePack: "pack-b"},
					},
				},
				{
					Name: "pack-a",
					Agents: []domain.Agent{{
						Name: "reviewer",
						Frontmatter: domain.AgentFrontmatter{
							Name:   "reviewer",
							Skills: []string{"deploy"},
						},
						Body:       []byte("Review code carefully"),
						SourcePack: "pack-a",
						SourcePath: "/pack/agents/reviewer.md",
					}},
					Skills: []domain.Skill{
						{Name: "deploy", DirPath: skillADir, SourcePack: "pack-a"},
					},
				},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantAgent := filepath.Join(projectDir, ".agents", "skills", "reviewer__aipack__pack-a", "SKILL.md")
	for _, w := range f.Writes {
		if w.Dst != wantAgent {
			continue
		}
		content := string(w.Content)
		if !strings.Contains(content, "deploy__aipack__pack-a") {
			t.Fatalf("expected agent skill ref to use pack-a skill:\n%s", content)
		}
		if strings.Contains(content, "deploy__aipack__pack-b") {
			t.Fatalf("agent skill ref used wrong pack:\n%s", content)
		}
		return
	}
	t.Fatalf("expected write to %q", wantAgent)
}

func TestPlan_Project_WorkflowsAndSkills(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	skillDir := testutil.SkillDir(t, "deploy")
	ctx := engine.SyncContext{
		Scope:      domain.ScopeProject,
		TargetDir:  projectDir,
		Home:       t.TempDir(),
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Workflows: []domain.Workflow{
					{Name: "onboard", Raw: []byte("# Onboard"), SourcePack: "pack-a"},
				},
				Skills: []domain.Skill{
					{Name: "deploy", DirPath: skillDir, SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Workflow → .clinerules/workflows/onboard__aipack__pack-a.md
	wantWf := filepath.Join(projectDir, ".clinerules", "workflows", "onboard__aipack__pack-a.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → .agents/skills/deploy__aipack__pack-a/SKILL.md
	wantSkill := filepath.Join(projectDir, ".agents", "skills", "deploy__aipack__pack-a", domain.SkillEntryFile)
	assertHasWriteDst(t, f.Writes, wantSkill)
}

func TestPlan_Global_Content(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	skillDir := testutil.SkillDir(t, "diagnose")
	ctx := engine.SyncContext{
		Scope:      domain.ScopeGlobal,
		TargetDir:  home,
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "global-rule", Raw: []byte("# Global"), SourcePack: "pack-a"},
				},
				Agents: []domain.Agent{
					{Name: "planner", Body: []byte("Plan the work"), SourcePack: "pack-a"},
				},
				Workflows: []domain.Workflow{
					{Name: "deploy", Raw: []byte("# Deploy"), SourcePack: "pack-a"},
				},
				Skills: []domain.Skill{
					{Name: "diagnose", DirPath: skillDir, SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	docs := documentsDir(home)
	wantRule := filepath.Join(docs, "Cline", "Rules", "global-rule__aipack__pack-a.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	// Agent promoted to skill → ~/.agents/skills/planner__aipack__pack-a/SKILL.md
	wantAgent := filepath.Join(home, ".agents", "skills", "planner__aipack__pack-a", "SKILL.md")
	assertHasWriteDst(t, f.Writes, wantAgent)

	wantWf := filepath.Join(docs, "Cline", "Workflows", "deploy__aipack__pack-a.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → ~/.agents/skills/diagnose__aipack__pack-a/SKILL.md
	wantSkill := filepath.Join(home, ".agents", "skills", "diagnose__aipack__pack-a", domain.SkillEntryFile)
	assertHasWriteDst(t, f.Writes, wantSkill)
}

func TestPlan_Global_UsesCLINEDIRAndCLINE_DATA_DIR(t *testing.T) {
	home := t.TempDir()
	clineDir := filepath.Join(t.TempDir(), ".cline")
	dataDir := filepath.Join(t.TempDir(), "cline-data")
	t.Setenv("CLINE_DIR", clineDir)
	t.Setenv("CLINE_DATA_DIR", dataDir)

	skillDir := testutil.SkillDir(t, "diagnose")
	ctx := engine.SyncContext{
		Scope:      domain.ScopeGlobal,
		TargetDir:  home,
		Namespaced: true,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "global-rule", Raw: []byte("# Global"), SourcePack: "pack-a"},
				},
				Agents: []domain.Agent{
					{Name: "planner", Body: []byte("Plan the work"), SourcePack: "pack-a"},
				},
				Workflows: []domain.Workflow{
					{Name: "deploy", Raw: []byte("# Deploy"), SourcePack: "pack-a"},
				},
				Skills: []domain.Skill{
					{Name: "diagnose", DirPath: skillDir, SourcePack: "pack-a"},
				},
			}},
			MCPServers: []domain.MCPServer{
				{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	assertHasWriteDst(t, f.Writes, filepath.Join(clineDir, "rules", "global-rule__aipack__pack-a.md"))
	assertHasWriteDst(t, f.Writes, filepath.Join(clineDir, "skills", "planner__aipack__pack-a", domain.SkillEntryFile))
	assertHasWriteDst(t, f.Writes, filepath.Join(dataDir, "workflows", "deploy__aipack__pack-a.md"))
	assertHasWriteDst(t, f.Writes, filepath.Join(clineDir, "skills", "diagnose__aipack__pack-a", domain.SkillEntryFile))

	if len(f.MCP) != 1 {
		t.Fatalf("MCP action count = %d, want 1", len(f.MCP))
	}
	if f.MCP[0].Dst != filepath.Join(dataDir, "settings", "cline_mcp_settings.json") {
		t.Fatalf("MCP dst = %q, want %q", f.MCP[0].Dst, filepath.Join(dataDir, "settings", "cline_mcp_settings.json"))
	}
}

func TestPlan_Project_HooksWrappers(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Home:      t.TempDir(),
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

	want := filepath.Join(projectDir, ".clinerules", "hooks", clineHookWrapperFile("PreToolUse"))
	write := mustFindWrite(t, f.Writes, want)
	if write.Category != domain.CategoryHooks {
		t.Fatalf("hook wrapper category = %q, want %q", write.Category, domain.CategoryHooks)
	}
	if write.SourcePack != "hooks-pack" {
		t.Fatalf("hook wrapper SourcePack = %q, want hooks-pack", write.SourcePack)
	}
	if runtime.GOOS != "windows" && write.DesiredMode != 0o755 {
		t.Fatalf("hook wrapper mode = %o, want 755", write.DesiredMode)
	}
	content := string(write.Content)
	// Wrapper content is platform-specific: Node.js source on Unix, a
	// PowerShell script with the handler JSON base64-encoded on Windows.
	var needles []string
	if runtime.GOOS == "windows" {
		needles = []string{
			`$ErrorActionPreference = "Continue"`,
			`ConvertFrom-AipackHookOutput`,
			`Merge-AipackHookOutputs`,
		}
	} else {
		needles = []string{
			`#!/usr/bin/env node`,
			`"tool": "Bash"`,
			`"command": "echo audit"`,
			`"timeoutSeconds": 5`,
			`JSON.stringify({ cancel: false })`,
			`preToolUse?.toolName`,
		}
	}
	for _, needle := range needles {
		if !strings.Contains(content, needle) {
			t.Fatalf("hook wrapper missing %q:\n%s", needle, content)
		}
	}
}

func TestPlan_Global_HooksUseDocumentsPathEvenWithCLINEDIR(t *testing.T) {
	home := t.TempDir()
	clineDir := filepath.Join(t.TempDir(), ".cline")
	dataDir := filepath.Join(t.TempDir(), "cline-data")
	t.Setenv("CLINE_DIR", clineDir)
	t.Setenv("CLINE_DATA_DIR", dataDir)
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "hooks-pack",
				Hooks: []domain.Hook{{
					ID:         "session",
					SourcePack: "hooks-pack",
					Events: []domain.HookEvent{{
						On: domain.HookEventRunStart,
						Handler: domain.HookHandler{
							Type:    domain.HookHandlerTypeCommand,
							Command: "echo start",
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
	want := filepath.Join(documentsDir(home), "Cline", "Hooks", clineHookWrapperFile("TaskStart"))
	assertHasWriteDst(t, f.Writes, want)
	unwanted := filepath.Join(clineDir, "hooks", clineHookWrapperFile("TaskStart"))
	if hasWriteDst(f.Writes, unwanted) {
		t.Fatalf("hooks should not follow CLINE_DIR override; found unexpected %q", unwanted)
	}
}

func TestRender_HookOnlyIncludesWrapper(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	f, err := Harness{}.Render(context.Background(), harness.RenderContext{
		OutDir: outDir,
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Name: "hooks-pack",
				Hooks: []domain.Hook{{
					ID:         "compact",
					SourcePack: "hooks-pack",
					Events: []domain.HookEvent{{
						On: domain.HookEventCompactBefore,
						Handler: domain.HookHandler{
							Type:    domain.HookHandlerTypeCommand,
							Command: "echo compact",
						},
					}},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertHasWriteDst(t, f.Writes, filepath.Join(outDir, "cline", "hooks", clineHookWrapperFile("PreCompact")))
}

func TestPlan_Global_MCP(t *testing.T) {
	home := t.TempDir()
	// Need HOME set for MCPSettingsPath (platform-dependent).
	t.Setenv("HOME", home)

	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}, AllowedTools: []string{"bar"}},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// MCP goes into MCP actions for Cline (always, not gated by SkipSettings).
	if len(f.MCP) == 0 {
		t.Fatal("expected MCP settings action")
	}
	for _, action := range f.MCP {
		if action.Harness != domain.HarnessCline {
			t.Fatalf("MCP harness: got %q want %q", action.Harness, domain.HarnessCline)
		}
	}

	wantTargets := mcpSettingsPaths(home)
	if len(f.MCP) != len(wantTargets) {
		t.Fatalf("MCP action count: got %d want %d", len(f.MCP), len(wantTargets))
	}

	have := map[string]bool{}
	for _, action := range f.MCP {
		have[action.Dst] = true
	}
	for _, target := range wantTargets {
		if !have[target] {
			t.Fatalf("missing MCP target action for %s", target)
		}
	}

	if len(f.MCPServers) != 1 {
		t.Fatalf("MCP server action count: got %d want 1", len(f.MCPServers))
	}
	if got, want := f.MCPServers[0].ConfigPath, MCPSettingsPath(home); got != want {
		t.Fatalf("MCP server config path: got %s want %s", got, want)
	}
}

func TestPlan_Global_MCPMergeMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "test-server", Command: []string{"echo", "hi"}},
			},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, action := range f.MCP {
		if !action.MergeMode {
			t.Errorf("MCP action for %s must use MergeMode to preserve user-added servers", action.Dst)
		}
	}
}

func TestPlan_Global_NoMCP(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile:   domain.Profile{},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.MCP) != 0 {
		t.Fatalf("expected no MCP actions when no MCP servers; got %d", len(f.MCP))
	}
}

// --- Render tests ---

func TestRenderBytes_MergesBase(t *testing.T) {
	t.Parallel()
	baseObj := map[string]any{
		"someOtherSetting": map[string]any{"k": "v"},
		"mcpServers":       map[string]any{"old": map[string]any{"disabled": true}},
	}
	base, err := json.Marshal(baseObj)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}

	servers := []domain.MCPServer{
		{Name: "foo", Timeout: 30, Command: []string{"echo", "hi"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(base, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Non-MCP keys preserved.
	if got["someOtherSetting"] == nil {
		t.Fatal("someOtherSetting should be preserved")
	}

	// Old MCP entries merged.
	mcp := got["mcpServers"].(map[string]any)
	if _, ok := mcp["old"]; !ok {
		t.Fatal("existing mcpServers entry should be preserved")
	}
	if _, ok := mcp["foo"]; !ok {
		t.Fatal("new mcpServers entry should be added")
	}
}

func TestRenderBytes_ResolvesEnvRefs(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	t.Setenv("BB_TOKEN", "secret-token")

	servers := []domain.MCPServer{
		{
			Name:    "bb",
			Command: []string{"node", "{env:HOME}/server.js"},
			Env:     map[string]string{"BB_TOKEN": "{env:BB_TOKEN}"},
		},
	}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := got["mcpServers"].(map[string]any)
	bb := mcp["bb"].(map[string]any)
	args := bb["args"].([]any)
	if len(args) == 0 || !strings.Contains(args[0].(string), "/tmp/test-home") {
		t.Fatalf("args not resolved: %v", args)
	}
	env := bb["env"].(map[string]any)
	if env["BB_TOKEN"] != "secret-token" {
		t.Fatalf("BB_TOKEN not resolved: %v", env["BB_TOKEN"])
	}
}

func TestRenderBytes_EnvRefsResolved(t *testing.T) {
	t.Setenv("HOME", "/tmp/resolved-home")
	servers := []domain.MCPServer{
		{
			Name:    "bb",
			Command: []string{"node", "{env:HOME}/server.js"},
			Env:     map[string]string{},
		},
	}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := got["mcpServers"].(map[string]any)
	bb := mcp["bb"].(map[string]any)
	args := bb["args"].([]any)
	// Env refs resolved from process environment at sync time.
	if len(args) == 0 || !strings.Contains(args[0].(string), "/tmp/resolved-home") {
		t.Fatalf("expected resolved HOME in args: %v", args)
	}
}

func TestRenderBytes_PopulatesAlwaysAllow(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}, AllowedTools: []string{"bar", "baz"}},
	}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := got["mcpServers"].(map[string]any)
	foo := mcp["foo"].(map[string]any)
	allowRaw := foo["alwaysAllow"].([]any)
	if len(allowRaw) != 2 {
		t.Fatalf("alwaysAllow length: got %d want 2", len(allowRaw))
	}
	if allowRaw[0] != "foo_bar" {
		t.Fatalf("alwaysAllow[0]: got %q want %q", allowRaw[0], "foo_bar")
	}
}

func TestRenderBytes_UnionsAllowedAndAlwaysAllowed(t *testing.T) {
	t.Parallel()
	// Cline has no visibility/prompt distinction — both fields collapse
	// into the single alwaysAllow list.
	servers := []domain.MCPServer{
		{
			Name:               "foo",
			Command:            []string{"echo", "hi"},
			Env:                map[string]string{},
			AllowedTools:       []string{"read", "list"},
			AlwaysAllowedTools: []string{"search", "read"}, // "read" overlaps
		},
	}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcp := got["mcpServers"].(map[string]any)
	foo := mcp["foo"].(map[string]any)
	allowRaw := foo["alwaysAllow"].([]any)
	if len(allowRaw) != 3 {
		t.Fatalf("alwaysAllow length: got %d want 3 (dedup union), list=%v", len(allowRaw), allowRaw)
	}
	// Sorted output; "read" deduped.
	want := []string{"foo_list", "foo_read", "foo_search"}
	for i, w := range want {
		if allowRaw[i] != w {
			t.Errorf("alwaysAllow[%d]: got %q want %q", i, allowRaw[i], w)
		}
	}
}

func TestRenderBytes_PopulatesTimeout(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "foo", Timeout: 30, Command: []string{"echo"}, Env: map[string]string{}},
		{Name: "bar", Timeout: 0, Command: []string{"echo"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := got["mcpServers"].(map[string]any)
	fooTimeout := int(mcp["foo"].(map[string]any)["timeout"].(float64))
	barTimeout := int(mcp["bar"].(map[string]any)["timeout"].(float64))

	if fooTimeout != 30 {
		t.Fatalf("foo timeout: got %d want 30", fooTimeout)
	}
	if barTimeout != 10 {
		t.Fatalf("bar timeout: got %d want 10 (default)", barTimeout)
	}
}

func TestRenderBytes_DoesNotHTMLEscapeCommands(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{{
		Name:    "shell",
		Command: []string{"sh", "-lc", "echo '<ok>' && run 2> >(tee log >&2)"},
	}}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}
	content := string(out)
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(content, escaped) {
			t.Fatalf("Cline settings should not HTML-escape %s:\n%s", escaped, content)
		}
	}
	if !strings.Contains(content, `echo '<ok>' && run 2> >(tee log >&2)`) {
		t.Fatalf("command changed unexpectedly:\n%s", content)
	}
}

func TestRenderBytes_StreamableHTTPUsesClineTransportType(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{{
		Name:      "remote",
		Transport: domain.TransportStreamableHTTP,
		URL:       "https://example.invalid/mcp",
	}}

	out, _, err := RenderBytes(nil, servers)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}
	content := string(out)
	if !strings.Contains(content, `"type": "streamableHttp"`) {
		t.Fatalf("streamable-http should render as Cline streamableHttp:\n%s", content)
	}
	if strings.Contains(content, `"type": "streamable-http"`) {
		t.Fatalf("Cline settings should not use canonical transport spelling directly:\n%s", content)
	}
}

func TestParseClineSettings_StreamableHTTPUsesCanonicalTransport(t *testing.T) {
	t.Parallel()
	settings := []byte(`{"mcpServers":{"remote":{"type":"streamableHttp","url":"https://example.invalid/mcp"}}}`)

	servers := map[string]domain.MCPServer{}
	parseClineSettings(servers, map[string][]string{}, settings)

	got, ok := servers["remote"]
	if !ok {
		t.Fatal("expected remote server to be parsed")
	}
	if got.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("transport: got %q want %q", got.Transport, domain.TransportStreamableHTTP)
	}
}

// --- Strip via Layout tests ---

func TestLayout_StripManaged_RemovesMCPServers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := Harness{}
	layout := h.Layout(domain.ScopeGlobal, home, home)
	if len(layout.OwnedFiles) == 0 {
		t.Skip("no owned files on this platform")
	}
	input := []byte(`{"mcpServers": {"foo": {}, "userKept": {"x": 1}}, "otherSetting": "keep"}`)
	ctx := harness.EditContext{ManagedMCPServers: map[string]struct{}{"foo": {}}}
	out, err := layout.StripManaged(input, layout.OwnedFiles[0].Path, ctx)
	if err != nil {
		t.Fatalf("StripManaged: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	servers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers should be preserved when the user has their own servers")
	}
	if _, ok := servers["foo"]; ok {
		t.Fatal("managed server foo should be stripped")
	}
	if _, ok := servers["userKept"]; !ok {
		t.Fatal("user-added server userKept should be preserved")
	}
	if got["otherSetting"] != "keep" {
		t.Fatal("otherSetting should be preserved")
	}
}

// --- Capture tests ---

func TestCapture_Project(t *testing.T) {
	projectDir := t.TempDir()

	// Create .clinerules/ with a rule file.
	rulesDir := filepath.Join(projectDir, ".clinerules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "team.md"), []byte("# Team"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a promoted agent in Cline's skills dir (.agents/skills).
	agentSkillDir := filepath.Join(projectDir, ".agents", "skills", "reviewer")
	if err := os.MkdirAll(agentSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	promotedAgent := "---\nname: reviewer\ndescription: Reviews code\nsource_type: agent\ntools:\n  - bash\n---\n\nReview\n"
	if err := os.WriteFile(filepath.Join(agentSkillDir, "SKILL.md"), []byte(promotedAgent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create workflows subdir.
	wfDir := filepath.Join(rulesDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "deploy.md"), []byte("# Deploy"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Rules) == 0 {
		t.Fatal("expected Rules to be populated")
	}
	if len(res.Agents) == 0 {
		t.Fatal("expected Agents to be populated from promoted skill")
	}
	if len(res.Workflows) == 0 {
		t.Fatal("expected Workflows to be populated")
	}

	// Check copies: rule and workflow have flat copy paths.
	foundRule, foundWf := false, false
	for _, c := range res.Copies {
		if c.Dst == filepath.Join("rules", "team.md") {
			foundRule = true
		}
		if c.Dst == filepath.Join("workflows", "deploy.md") {
			foundWf = true
		}
	}
	if !foundRule {
		t.Fatal("expected rule copy at rules/team.md")
	}
	if !foundWf {
		t.Fatal("expected workflow copy at workflows/deploy.md")
	}

	// Agent is captured as a write (re-rendered), not a copy.
	foundAgentWrite := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("agents", "reviewer.md") {
			foundAgentWrite = true
		}
	}
	if !foundAgentWrite {
		t.Fatal("expected agent write at agents/reviewer.md")
	}

	// Typed fields.
	if len(res.Rules) != 1 || res.Rules[0].Name != "team" {
		t.Fatalf("expected typed rule 'team'; got %v", res.Rules)
	}
	if len(res.Agents) != 1 || res.Agents[0].Name != "reviewer" {
		t.Fatalf("expected typed agent 'reviewer'; got %v", res.Agents)
	}
	if len(res.Workflows) != 1 || res.Workflows[0].Name != "deploy" {
		t.Fatalf("expected typed workflow 'deploy'; got %v", res.Workflows)
	}
}

func TestCapture_Project_MCP(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	// Create MCP settings at the global path.
	settingsPath := MCPSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := []byte(`{"mcpServers":{"test-server":{"command":"echo","args":["hi"],"env":{}}}}`)
	if err := os.WriteFile(settingsPath, mcpJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// MCP settings should be captured even in project scope.
	foundSettings := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("configs", "cline", "cline_mcp_settings.json") {
			foundSettings = true
		}
	}
	if !foundSettings {
		t.Fatal("expected MCP settings write in project-scope capture")
	}
	if len(res.MCPServers) == 0 {
		t.Fatal("expected MCP servers to be parsed from captured settings")
	}
	if _, ok := res.MCPServers["test-server"]; !ok {
		t.Fatal("expected test-server in captured MCP servers")
	}
}

func TestCapture_Project_MCP_CanonicalMissingMirrorWarns(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	mirrorPath := mcpMirrorSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := []byte(`{"mcpServers":{"mirror-server":{"command":"echo","args":["hi"],"env":{}}}}`)
	if err := os.WriteFile(mirrorPath, mcpJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if _, ok := res.MCPServers["mirror-server"]; !ok {
		t.Fatalf("expected mirror MCP server when canonical settings are missing; got %+v", res.MCPServers)
	}

	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "falling back to secondary path") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatal("expected warning about falling back from canonical Cline MCP settings path")
	}
}

func TestCapture_Project_MCP_FallsBackToMirrorWhenCanonicalMissing(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	mirrorPath := mcpMirrorSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := []byte(`{"mcpServers":{"mirror-server":{"command":"echo","args":["hi"],"env":{}}}}`)
	if err := os.WriteFile(mirrorPath, mcpJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if _, ok := res.MCPServers["mirror-server"]; !ok {
		t.Fatalf("expected mirror MCP server to be captured; got %+v", res.MCPServers)
	}
	if len(res.MCP) != 1 {
		t.Fatalf("captured MCP count: got %d want 1", len(res.MCP))
	}
	if got := res.MCP[0].HarnessPath; got != filepath.Clean(mirrorPath) {
		t.Fatalf("captured MCP harness path: got %s want %s", got, filepath.Clean(mirrorPath))
	}
}

func TestCapture_Project_MCP_MirrorDiffWarns(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	canonicalPath := MCPSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalJSON := []byte(`{"mcpServers":{"canonical-server":{"command":"echo","args":["a"],"env":{}}}}`)
	if err := os.WriteFile(canonicalPath, canonicalJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	mirrorPath := mcpMirrorSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mirrorJSON := []byte(`{"mcpServers":{"mirror-server":{"command":"echo","args":["b"],"env":{}}}}`)
	if err := os.WriteFile(mirrorPath, mirrorJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if _, ok := res.MCPServers["canonical-server"]; !ok {
		t.Fatalf("expected canonical MCP server from %s", canonicalPath)
	}
	if _, ok := res.MCPServers["mirror-server"]; ok {
		t.Fatalf("did not expect mirror MCP server in canonical capture")
	}

	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "differ from capture source") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatal("expected warning about differing secondary Cline MCP settings")
	}
}

func TestCapture_Global_Agents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	// Create a promoted agent in Cline's global skills directory (.agents/skills).
	agentSkillDir := filepath.Join(home, ".agents", "skills", "planner")
	if err := os.MkdirAll(agentSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	promotedAgent := "---\nname: planner\ndescription: Plans work\nsource_type: agent\ntools:\n  - bash\n---\n\nPlan\n"
	if err := os.WriteFile(filepath.Join(agentSkillDir, "SKILL.md"), []byte(promotedAgent), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(context.Background(), harness.CaptureContext{
		Scope: domain.ScopeGlobal,
		Home:  home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Name != "planner" {
		t.Fatalf("expected typed agent 'planner'; got %v", res.Agents)
	}
	// Promoted agents are captured as writes (re-rendered), not copies.
	found := false
	for _, w := range res.Writes {
		if w.Dst == filepath.Join("agents", "planner.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected agent write at agents/planner.md")
	}
}

// --- Layout tests ---

func TestLayout_IncludesHooksValidationButDoesNotRemoveHooks(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	layout := Harness{}.Layout(domain.ScopeProject, projectDir, t.TempDir())
	hooksDir := filepath.Join(projectDir, ".clinerules", "hooks")
	if !containsString(layout.ValidationRoots, hooksDir) {
		t.Fatalf("validation roots should include hooks dir %q: %v", hooksDir, layout.ValidationRoots)
	}
	if containsString(layout.RemovePaths, hooksDir) {
		t.Fatalf("hooks dir %q must not be a RemovePath: %v", hooksDir, layout.RemovePaths)
	}
}

func TestRenderHookWrappers_SourceMatcherDoesNotWarn(t *testing.T) {
	t.Parallel()
	wrappers, _, warnings, err := RenderHookWrappers([]domain.Hook{{
		ID:         "session",
		SourcePack: "hooks-pack",
		Events: []domain.HookEvent{{
			On:    domain.HookEventRunStart,
			Match: domain.HookMatch{Source: "startup"},
			Handler: domain.HookHandler{
				Type:    domain.HookHandlerTypeCommand,
				Command: "echo start",
			},
		}},
	}})
	if err != nil {
		t.Fatalf("RenderHookWrappers: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	content := string(wrappers[clineHookWrapperFile("TaskStart")])
	searchContent := clineWrapperSearchContent(t, content)
	for _, needle := range clineSourceMatcherNeedles() {
		if !strings.Contains(searchContent, needle) {
			t.Fatalf("wrapper missing %q:\n%s", needle, content)
		}
	}
}

func TestRenderHookWrappers_NodeWrapperMergesHandlerJSONOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("node wrapper is not used on Windows")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	wrappers, _, _, err := RenderHookWrappers([]domain.Hook{{
		ID:         "audit",
		SourcePack: "hooks-pack",
		Events: []domain.HookEvent{{
			On:    domain.HookEventToolBefore,
			Match: domain.HookMatch{Tool: "Bash"},
			Handlers: []domain.HookHandler{
				{
					Type:    domain.HookHandlerTypeCommand,
					Command: "printf '%s' '{\"cancel\":true,\"contextModification\":\"ctx1\"}'",
				},
				{
					Type:    domain.HookHandlerTypeCommand,
					Command: "printf '%s\n%s' 'debug' '{\"cancel\":false,\"contextModification\":\"ctx2\",\"errorMessage\":\"err\"}'",
				},
			},
		}},
	}})
	if err != nil {
		t.Fatalf("RenderHookWrappers: %v", err)
	}

	wrapperPath := filepath.Join(t.TempDir(), clineHookWrapperFile("PreToolUse"))
	if err := os.WriteFile(wrapperPath, wrappers[clineHookWrapperFile("PreToolUse")], 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, wrapperPath)
	cmd.Stdin = strings.NewReader(`{"preToolUse":{"toolName":"Bash"}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wrapper execution failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("wrapper output is not JSON: %v\n%s", err, out)
	}
	if result["cancel"] != true {
		t.Fatalf("cancel = %v, want true; output=%s", result["cancel"], out)
	}
	if result["contextModification"] != "ctx1\n\nctx2" {
		t.Fatalf("contextModification = %q, want merged context; output=%s", result["contextModification"], out)
	}
	if result["errorMessage"] != "err" {
		t.Fatalf("errorMessage = %q, want err; output=%s", result["errorMessage"], out)
	}
}

func TestRenderClinePowerShellWrapperMergesHandlerJSONOutput(t *testing.T) {
	t.Parallel()
	content := renderClinePowerShellWrapper([]byte(`[{"command":"echo ok","label":"hooks-pack/audit"}]`))
	for _, needle := range []string{
		"ConvertFrom-AipackHookOutput",
		"Merge-AipackHookOutputs",
		"RedirectStandardOutput = $true",
		"ConvertTo-Json -Compress",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("PowerShell wrapper missing %q:\n%s", needle, content)
		}
	}
}

func TestRenderHookWrappers_CommandWindowsOnlyAccepted(t *testing.T) {
	t.Parallel()
	wrappers, _, _, err := RenderHookWrappers([]domain.Hook{{
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
		t.Fatalf("RenderHookWrappers: %v", err)
	}
	content := string(wrappers[clineHookWrapperFile("PreToolUse")])
	if !strings.Contains(clineWrapperSearchContent(t, content), "powershell ./scan.ps1") {
		t.Fatalf("wrapper missing command_windows payload:\n%s", content)
	}
}

func clineWrapperSearchContent(t *testing.T, content string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return content
	}
	return content + "\n" + decodeClinePowerShellHandlers(t, content)
}

func decodeClinePowerShellHandlers(t *testing.T, content string) string {
	t.Helper()
	const prefix = `[System.Convert]::FromBase64String("`
	start := strings.Index(content, prefix)
	if start < 0 {
		t.Fatalf("PowerShell wrapper missing handler JSON base64:\n%s", content)
	}
	start += len(prefix)
	end := strings.Index(content[start:], `")`)
	if end < 0 {
		t.Fatalf("PowerShell wrapper has unterminated handler JSON base64:\n%s", content)
	}
	raw, err := base64.StdEncoding.DecodeString(content[start : start+end])
	if err != nil {
		t.Fatalf("decode PowerShell handler JSON: %v", err)
	}
	return string(raw)
}

func clineSourceMatcherNeedles() []string {
	if runtime.GOOS == "windows" {
		return []string{`"source": "startup"`, "Get-SourceName $payload $handler.event", "Test-MatcherMatch"}
	}
	return []string{`"source": "startup"`, "sourceName(payload, handler.event)", "caseInsensitive: true"}
}

func TestClineWrapperMatchersUseRegex(t *testing.T) {
	t.Parallel()
	if !strings.Contains(clineNodeWrapperRuntime, "function matcherMatches(") {
		t.Fatalf("node wrapper missing matcherMatches helper:\n%s", clineNodeWrapperRuntime)
	}
	if !strings.Contains(clineNodeWrapperRuntime, "new RegExp(pattern,") {
		t.Fatalf("node wrapper matcher is not regex-based:\n%s", clineNodeWrapperRuntime)
	}
	if strings.Contains(clineNodeWrapperRuntime, `replace(/\*/g`) {
		t.Fatalf("node wrapper still contains glob-escape logic:\n%s", clineNodeWrapperRuntime)
	}
	if !strings.Contains(clinePowerShellWrapperRuntime, "function Test-MatcherMatch(") {
		t.Fatalf("powershell wrapper missing Test-MatcherMatch helper:\n%s", clinePowerShellWrapperRuntime)
	}
	if !strings.Contains(clinePowerShellWrapperRuntime, "-match $pattern") {
		t.Fatalf("powershell wrapper matcher is not regex-based:\n%s", clinePowerShellWrapperRuntime)
	}
}

func TestRenderHookWrappers_NodeWrapperRegexToolMatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("node wrapper is not used on Windows")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	wrappers, _, _, err := RenderHookWrappers([]domain.Hook{{
		ID:         "audit",
		SourcePack: "hooks-pack",
		Events: []domain.HookEvent{{
			On:    domain.HookEventToolBefore,
			Match: domain.HookMatch{Tool: "Edit|Write"},
			Handler: domain.HookHandler{
				Type:    domain.HookHandlerTypeCommand,
				Command: `printf '%s' '{"cancel":true}'`,
			},
		}},
	}})
	if err != nil {
		t.Fatalf("RenderHookWrappers: %v", err)
	}
	wrapperPath := filepath.Join(t.TempDir(), clineHookWrapperFile("PreToolUse"))
	if err := os.WriteFile(wrapperPath, wrappers[clineHookWrapperFile("PreToolUse")], 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(toolName string) map[string]any {
		t.Helper()
		cmd := exec.Command(node, wrapperPath)
		cmd.Stdin = strings.NewReader(`{"preToolUse":{"toolName":"` + toolName + `"}}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("wrapper execution failed for %q: %v", toolName, err)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("wrapper output is not JSON: %v\n%s", err, out)
		}
		return result
	}

	// Regex alternation must match "Write"; the old glob escaped "|" and matched
	// neither branch.
	if got := run("Write"); got["cancel"] != true {
		t.Fatalf("tool=Write: cancel = %v, want true (regex alternation should match)", got["cancel"])
	}
	if got := run("Read"); got["cancel"] == true {
		t.Fatalf("tool=Read: cancel = true, want false (Read must not match Edit|Write)")
	}
}

// --- Helpers ---

func assertHasWriteDst(t *testing.T, writes []domain.WriteAction, dst string) {
	t.Helper()
	for _, w := range writes {
		if w.Dst == dst {
			return
		}
	}
	var dsts []string
	for _, w := range writes {
		dsts = append(dsts, w.Dst)
	}
	t.Fatalf("expected write to %q; got writes: %v", dst, dsts)
}

func mustFindWrite(t *testing.T, writes []domain.WriteAction, dst string) domain.WriteAction {
	t.Helper()
	for _, w := range writes {
		if w.Dst == dst {
			return w
		}
	}
	var dsts []string
	for _, w := range writes {
		dsts = append(dsts, w.Dst)
	}
	t.Fatalf("expected write to %q; got writes: %v", dst, dsts)
	return domain.WriteAction{}
}

func hasWriteDst(writes []domain.WriteAction, dst string) bool {
	for _, w := range writes {
		if w.Dst == dst {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	return slices.Contains(items, want)
}
