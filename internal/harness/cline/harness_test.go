package cline

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
					{Name: "reviewer", Raw: []byte("# Reviewer"), SourcePack: "pack-a", SourcePath: "/pack/agents/reviewer.md"},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Rule write → .clinerules/team.md
	wantRule := filepath.Join(projectDir, ".clinerules", "team.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	wantAgent := filepath.Join(projectDir, ".clinerules", "agents", "reviewer.md")
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

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Workflow → .clinerules/workflows/onboard.md
	wantWf := filepath.Join(projectDir, ".clinerules", "workflows", "onboard.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → .clinerules/skills/deploy
	wantSkill := filepath.Join(projectDir, ".clinerules", "skills", "deploy")
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
					{Name: "planner", Raw: []byte("# Planner"), SourcePack: "pack-a"},
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

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	globalRulesDir := RulesGlobalDir(home)
	globalWfDir := WorkflowsGlobalDir(home)

	// Rule → RulesGlobalDir/global-rule.md
	wantRule := filepath.Join(globalRulesDir, "global-rule.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	wantAgent := filepath.Join(AgentsGlobalDir(home), "planner.md")
	assertHasWriteDst(t, f.Writes, wantAgent)

	// Workflow → WorkflowsGlobalDir/deploy.md
	wantWf := filepath.Join(globalWfDir, "deploy.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → ~/.cline/skills/diagnose
	wantSkill := filepath.Join(home, ".cline", "skills", "diagnose")
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

func TestPlan_Global_MCPAsPlugin(t *testing.T) {
	home := t.TempDir()
	// Need HOME set for SettingsGlobalPath.
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

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// MCP goes into Plugins for Cline (always, not gated by SkipSettings).
	if len(f.Plugins) == 0 {
		t.Fatal("expected MCP settings plugin")
	}
	if f.Plugins[0].Harness != domain.HarnessCline {
		t.Fatalf("plugin harness: got %q want %q", f.Plugins[0].Harness, domain.HarnessCline)
	}
}

func TestPlan_Global_NoMCP_NoPlugin(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeGlobal,
		TargetDir: home,
		Profile:   domain.Profile{},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Plugins) != 0 {
		t.Fatalf("expected no plugins when no MCP servers; got %d", len(f.Plugins))
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

	out, _, err := RenderBytes(base, servers, false)
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

	out, _, err := RenderBytes(nil, servers, true)
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

func TestRenderBytes_TransformsEnvRefsToNativeFormat(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{
			Name:    "bb",
			Command: []string{"node", "{env:HOME}/server.js"},
			Env:     map[string]string{},
		},
	}

	out, _, err := RenderBytes(nil, servers, false)
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
	// Cline native format: ${VAR}
	if len(args) == 0 || !strings.Contains(args[0].(string), "${HOME}") {
		t.Fatalf("expected ${HOME} in args: %v", args)
	}
}

func TestRenderBytes_PopulatesAlwaysAllow(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}, AllowedTools: []string{"bar", "baz"}},
	}

	out, _, err := RenderBytes(nil, servers, false)
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

	out, _, err := RenderBytes(nil, servers, false)
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

// --- StripManagedKeys tests ---

func TestStripManagedKeys_RemovesMCPServers(t *testing.T) {
	t.Parallel()
	input := []byte(`{"mcpServers": {"foo": {}}, "otherSetting": "keep"}`)
	out, err := StripManagedKeys(input)
	if err != nil {
		t.Fatalf("StripManagedKeys: %v", err)
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

	// Create .clinerules/ with rule and agent files.
	rulesDir := filepath.Join(projectDir, ".clinerules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "team.md"), []byte("# Team"), 0o600); err != nil {
		t.Fatal(err)
	}
	agentsDir := filepath.Join(rulesDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\ntools:\n  - bash\n---\nReview\n"), 0o600); err != nil {
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

	res, err := Harness{}.Capture(harness.CaptureContext{
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
		t.Fatal("expected Agents to be populated")
	}
	if len(res.Workflows) == 0 {
		t.Fatal("expected Workflows to be populated")
	}

	// Check copies: flat Dst paths (harness-agnostic).
	foundRule, foundAgent, foundWf := false, false, false
	for _, c := range res.Copies {
		if c.Dst == filepath.Join("rules", "team.md") {
			foundRule = true
		}
		if c.Dst == filepath.Join("agents", "reviewer.md") {
			foundAgent = true
		}
		if c.Dst == filepath.Join("workflows", "deploy.md") {
			foundWf = true
		}
	}
	if !foundRule {
		t.Fatal("expected rule copy at rules/team.md")
	}
	if !foundAgent {
		t.Fatal("expected agent copy at agents/reviewer.md")
	}
	if !foundWf {
		t.Fatal("expected workflow copy at workflows/deploy.md")
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

func TestCapture_Global_Agents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	agentsDir := AgentsGlobalDir(home)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "planner.md"), []byte("---\nname: planner\ntools:\n  - bash\n---\nPlan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(harness.CaptureContext{
		Scope: domain.ScopeGlobal,
		Home:  home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Name != "planner" {
		t.Fatalf("expected typed agent 'planner'; got %v", res.Agents)
	}
	found := false
	for _, c := range res.Copies {
		if c.Dst == filepath.Join("agents", "planner.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected agent copy at agents/planner.md")
	}
}

// --- Managed roots tests ---

func TestManagedRootsProject(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	roots := ManagedRootsProject(projectDir)

	wantClinerules := filepath.Join(projectDir, ".clinerules")
	wantAgents := filepath.Join(projectDir, ".clinerules", "agents")
	found := false
	foundAgents := false
	for _, r := range roots {
		if r == wantClinerules {
			found = true
		}
		if r == wantAgents {
			foundAgents = true
		}
	}
	if !found {
		t.Fatalf("missing .clinerules in managed roots; got %v", roots)
	}
	if !foundAgents {
		t.Fatalf("missing .clinerules/agents in managed roots; got %v", roots)
	}
}

func TestManagedRootsGlobal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	roots := ManagedRootsGlobal(home)

	// Should include global rules dir and workflows dir.
	wantRules := RulesGlobalDir(home)
	wantAgents := AgentsGlobalDir(home)
	wantWf := WorkflowsGlobalDir(home)

	foundRules, foundAgents, foundWf := false, false, false
	for _, r := range roots {
		if r == wantRules {
			foundRules = true
		}
		if r == wantAgents {
			foundAgents = true
		}
		if r == wantWf {
			foundWf = true
		}
	}
	if !foundRules {
		t.Fatalf("missing rules dir in global managed roots; got %v", roots)
	}
	if !foundAgents {
		t.Fatalf("missing agents dir in global managed roots; got %v", roots)
	}
	if !foundWf {
		t.Fatalf("missing workflows dir in global managed roots; got %v", roots)
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
