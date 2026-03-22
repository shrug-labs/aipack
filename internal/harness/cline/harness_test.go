package cline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// --- Plan tests ---

func TestPlan_Project_RulesAndAgents(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Home:      t.TempDir(),
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

	// Rule write → .clinerules/team.md
	wantRule := filepath.Join(projectDir, ".clinerules", "team.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	// Agent promoted to skill → .agents/skills/reviewer/SKILL.md
	wantAgent := filepath.Join(projectDir, ".agents", "skills", "reviewer", "SKILL.md")
	assertHasWriteDst(t, f.Writes, wantAgent)
}

func TestPlan_Project_WorkflowsAndSkills(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Home:      t.TempDir(),
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Workflows: []domain.Workflow{
					{Name: "onboard", Raw: []byte("# Onboard"), SourcePack: "pack-a"},
				},
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

	// Workflow → .clinerules/workflows/onboard.md
	wantWf := filepath.Join(projectDir, ".clinerules", "workflows", "onboard.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → .agents/skills/deploy
	wantSkill := filepath.Join(projectDir, ".agents", "skills", "deploy")
	found := false
	for _, c := range f.Copies {
		if c.Dst == wantSkill {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected skill copy to %q", wantSkill)
	}
}

func TestPlan_Global_Content(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
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
					{Name: "diagnose", DirPath: "/pack/skills/diagnose", SourcePack: "pack-a"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantRule := filepath.Join(home, "Documents", "Cline", "Rules", "global-rule.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	// Agent promoted to skill → ~/.agents/skills/planner/SKILL.md
	wantAgent := filepath.Join(home, ".agents", "skills", "planner", "SKILL.md")
	assertHasWriteDst(t, f.Writes, wantAgent)

	wantWf := filepath.Join(home, "Documents", "Cline", "Workflows", "deploy.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → ~/.agents/skills/diagnose
	wantSkill := filepath.Join(home, ".agents", "skills", "diagnose")
	found := false
	for _, c := range f.Copies {
		if c.Dst == wantSkill {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected skill copy to %q", wantSkill)
	}
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

// --- Strip via Layout tests ---

func TestLayout_StripManaged_RemovesMCPServers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := Harness{}
	layout := h.Layout(domain.ScopeGlobal, home, home)
	if len(layout.OwnedFiles) == 0 {
		t.Skip("no owned files on this platform")
	}
	input := []byte(`{"mcpServers": {"foo": {}}, "otherSetting": "keep"}`)
	out, err := layout.StripManaged(input, layout.OwnedFiles[0].Path)
	if err != nil {
		t.Fatalf("StripManaged: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["mcpServers"]; ok {
		t.Fatal("mcpServers should be stripped")
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

	// Create a promoted agent in skills dir (.agents/skills, shared with Codex).
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

	// Create a promoted agent in the global skills directory (.agents/skills, shared with Codex).
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

func TestLayout_Project(t *testing.T) {
	t.Parallel()
	h := Harness{}
	layout := h.Layout(domain.ScopeProject, "/proj", "/home")

	want := map[string]bool{
		"/proj/.clinerules":           false,
		"/proj/.clinerules/workflows": false,
		"/proj/.agents/skills":        false,
	}
	// MCP settings path is also a managed root (always global for Cline).
	mcpPaths := mcpSettingsPaths("/home")
	for _, p := range mcpPaths {
		want[p] = false
	}
	for _, r := range layout.ValidationRoots {
		if _, ok := want[r]; ok {
			want[r] = true
		} else {
			t.Errorf("unexpected validation root: %s", r)
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("missing validation root: %s", path)
		}
	}
	wantRemove := map[string]bool{
		"/proj/.clinerules/workflows": false,
		"/proj/.agents/skills":        false,
	}
	for _, p := range layout.RemovePaths {
		if _, ok := wantRemove[p]; ok {
			wantRemove[p] = true
		} else {
			t.Errorf("unexpected RemovePath: %s", p)
		}
	}
	for path, found := range wantRemove {
		if !found {
			t.Errorf("missing RemovePath: %s", path)
		}
	}

	if len(layout.OwnedFiles) != len(mcpPaths) {
		t.Fatalf("expected %d OwnedFiles for MCP settings, got %d", len(mcpPaths), len(layout.OwnedFiles))
	}
	haveOwned := map[string]bool{}
	for _, of := range layout.OwnedFiles {
		haveOwned[of.Path] = true
	}
	for _, p := range mcpPaths {
		if !haveOwned[p] {
			t.Fatalf("missing OwnedFile for %s", p)
		}
	}
}

func TestLayout_Global(t *testing.T) {
	t.Parallel()
	h := Harness{}
	layout := h.Layout(domain.ScopeGlobal, "/home", "/home")

	want := map[string]bool{
		"/home/.agents/skills":                                    false,
		filepath.Join("/home", "Documents", "Cline", "Rules"):     false,
		filepath.Join("/home", "Documents", "Cline", "Workflows"): false,
	}
	mcpPaths := mcpSettingsPaths("/home")
	for _, p := range mcpPaths {
		want[p] = false
	}
	for _, r := range layout.ValidationRoots {
		if _, ok := want[r]; ok {
			want[r] = true
		} else {
			t.Errorf("unexpected validation root: %s", r)
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("missing validation root: %s", path)
		}
	}
	wantRemove := map[string]bool{
		"/home/.agents/skills":                                    false,
		filepath.Join("/home", "Documents", "Cline", "Rules"):     false,
		filepath.Join("/home", "Documents", "Cline", "Workflows"): false,
	}
	for _, p := range layout.RemovePaths {
		if _, ok := wantRemove[p]; ok {
			wantRemove[p] = true
		} else {
			t.Errorf("unexpected remove path: %s", p)
		}
	}
	for path, found := range wantRemove {
		if !found {
			t.Errorf("missing remove path: %s", path)
		}
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
