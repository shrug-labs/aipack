package opencode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
		Profile: domain.Profile{
			Packs: []domain.Pack{{
				Rules: []domain.Rule{
					{Name: "team", Raw: []byte("# Team rules"), SourcePack: "pack-a", SourcePath: "/pack/rules/team.md"},
				},
				Agents: []domain.Agent{
					{
						Name:       "reviewer",
						Raw:        []byte("---\nname: reviewer\ntools:\n  - bash\n---\n# Reviewer agent\n"),
						SourcePack: "pack-a",
						SourcePath: "/pack/agents/reviewer.md",
					},
				},
			}},
		},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Rule write → .opencode/rules/team.md
	wantRule := filepath.Join(projectDir, ".opencode", "rules", "team.md")
	found := false
	for _, w := range f.Writes {
		if w.Dst == wantRule {
			found = true
			if w.SourcePack != "pack-a" {
				t.Fatalf("rule SourcePack: got %q want %q", w.SourcePack, "pack-a")
			}
		}
	}
	if !found {
		t.Fatalf("expected write to %q; got writes: %v", wantRule, writeDsts(f.Writes))
	}

	// Agent write → .opencode/agents/reviewer.md
	wantAgent := filepath.Join(projectDir, ".opencode", "agents", "reviewer.md")
	found = false
	for _, w := range f.Writes {
		if w.Dst == wantAgent {
			found = true
			// Verify tools were transformed from list -> map for OpenCode.
			if bytes.Contains(w.Content, []byte("tools:\n  -")) {
				t.Fatalf("expected OpenCode agent tools to be a map, but found YAML list in content:\n%s", string(w.Content))
			}
			if !bytes.Contains(w.Content, []byte("tools:\n")) || !bytes.Contains(w.Content, []byte("bash: true")) {
				t.Fatalf("expected transformed tools map in agent write; got content:\n%s", string(w.Content))
			}
		}
	}
	if !found {
		t.Fatalf("expected write to %q; got writes: %v", wantAgent, writeDsts(f.Writes))
	}
}

func TestPlan_Project_SkillsAndWorkflows(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
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

	// Workflow write → .opencode/commands/onboard.md
	wantWf := filepath.Join(projectDir, ".opencode", "commands", "onboard.md")
	found := false
	for _, w := range f.Writes {
		if w.Dst == wantWf {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected write to %q; got writes: %v", wantWf, writeDsts(f.Writes))
	}

	// Skill copy → .opencode/skills/deploy
	wantSkill := filepath.Join(projectDir, ".opencode", "skills", "deploy")
	foundCopy := false
	for _, c := range f.Copies {
		if c.Dst == wantSkill {
			foundCopy = true
			if c.Kind != domain.CopyKindDir {
				t.Fatalf("skill copy kind: got %q want %q", c.Kind, domain.CopyKindDir)
			}
		}
	}
	if !foundCopy {
		t.Fatalf("expected copy to %q; got copies: %v", wantSkill, copyDsts(f.Copies))
	}
}

func TestPlan_Global_ContentPaths(t *testing.T) {
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

	ocBase := filepath.Join(home, ".config", "opencode")

	// Rule → ~/.config/opencode/rules/global-rule.md
	wantRule := filepath.Join(ocBase, "rules", "global-rule.md")
	assertHasWriteDst(t, f.Writes, wantRule)

	// Agent → ~/.config/opencode/agents/planner.md
	wantAgent := filepath.Join(ocBase, "agents", "planner.md")
	assertHasWriteDst(t, f.Writes, wantAgent)

	// Workflow → ~/.config/opencode/commands/deploy.md
	wantWf := filepath.Join(ocBase, "commands", "deploy.md")
	assertHasWriteDst(t, f.Writes, wantWf)

	// Skill → ~/.config/opencode/skills/diagnose
	wantSkill := filepath.Join(ocBase, "skills", "diagnose")
	found := false
	for _, c := range f.Copies {
		if c.Dst == wantSkill {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected copy to %q; got copies: %v", wantSkill, copyDsts(f.Copies))
	}
}

func TestCapture_Project_AgentToolsMapBecomesNeutralList(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	agentsDir := filepath.Join(projectDir, ".opencode", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\ntools:\n  bash: true\n  read: true\n---\nReview\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("Agents = %d, want 1", len(res.Agents))
	}
	tools := map[string]bool{}
	for _, tool := range res.Agents[0].Frontmatter.Tools {
		tools[tool] = true
	}
	if !tools["bash"] || !tools["read"] {
		t.Fatalf("expected neutral tool list to include bash/read, got %v", res.Agents[0].Frontmatter.Tools)
	}
}

func TestPlan_Settings_WithMCP(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
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

	if len(f.Settings) == 0 {
		t.Fatal("expected settings action for opencode.json")
	}
	sa := f.Settings[0]
	if sa.Harness != domain.HarnessOpenCode {
		t.Fatalf("settings harness: got %q want %q", sa.Harness, domain.HarnessOpenCode)
	}

	// Verify JSON has mcp and tools keys.
	var root map[string]any
	if err := json.Unmarshal(sa.Desired, &root); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if _, ok := root["mcp"]; !ok {
		t.Fatal("settings missing 'mcp' key")
	}
	if _, ok := root["tools"]; !ok {
		t.Fatal("settings missing 'tools' key")
	}
}

func TestPlan_Settings_ResolvesAvailableEnvAndPreservesMissingRefs(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	t.Setenv("MON_ENV_FILE", "/tmp/mon.env")
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "monitoring", Command: []string{"uvx", "--env-file", "{env:MON_ENV_FILE}", "{env:HOME}/server.py"}, Env: map[string]string{}},
				{Name: "bad", Command: []string{"node", "server.js"}, Env: map[string]string{"TOKEN": "{env:MISSING_VAR}"}},
			},
		},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(f.Settings) == 0 {
		t.Fatal("expected settings action for opencode.json")
	}

	var root map[string]any
	if err := json.Unmarshal(f.Settings[0].Desired, &root); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	mcp := root["mcp"].(map[string]any)
	mon, ok := mcp["monitoring"].(map[string]any)
	if !ok {
		t.Fatal("monitoring server should be present")
	}
	cmd := mon["command"].([]any)
	if len(cmd) < 4 || cmd[2] != "/tmp/mon.env" || cmd[3] != "/tmp/test-home/server.py" {
		t.Fatalf("available env refs should be resolved: got %v", cmd)
	}
	bad, ok := mcp["bad"].(map[string]any)
	if !ok {
		t.Fatal("bad server should be preserved in opencode.json")
	}
	env := bad["environment"].(map[string]any)
	if env["TOKEN"] != "{env:MISSING_VAR}" {
		t.Fatalf("env placeholder should be preserved: got %v", env["TOKEN"])
	}
}

func TestPlan_SkipSettings_MergeModePlugin(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ctx := engine.SyncContext{
		Scope:        domain.ScopeProject,
		TargetDir:    projectDir,
		SkipSettings: true,
		Profile: domain.Profile{
			MCPServers: []domain.MCPServer{
				{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}},
			},
		},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(f.Settings) != 0 {
		t.Fatalf("expected no settings (skip_settings=true), got %d", len(f.Settings))
	}
	if len(f.Plugins) == 0 {
		t.Fatal("expected MergeMode plugin when skip_settings=true")
	}
	if !f.Plugins[0].MergeMode {
		t.Fatal("expected MergeMode=true on plugin")
	}
}

func TestPlan_OhMyOpenCode_Plugin(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	omoContent := []byte(`{"custom": true}`)
	ctx := engine.SyncContext{
		Scope:     domain.ScopeProject,
		TargetDir: projectDir,
		Profile: domain.Profile{
			Plugins: domain.PluginsBundle{
				domain.HarnessOpenCode: []domain.ConfigFile{
					{Filename: "oh-my-opencode.json", Content: omoContent},
				},
			},
		},
	}

	f, err := Harness{}.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	found := false
	for _, p := range f.Plugins {
		if p.Label == "oh-my-opencode.json" {
			found = true
			if string(p.Desired) != string(omoContent) {
				t.Fatalf("omo content mismatch")
			}
		}
	}
	if !found {
		t.Fatal("expected oh-my-opencode.json plugin action")
	}
}

// --- Render tests ---

func TestRenderBytes_MergesBase(t *testing.T) {
	t.Parallel()
	base := []byte(`{"unrelated": {"k": "v"}}`)
	servers := []domain.MCPServer{
		{Name: "foo", Command: []string{"echo", "hi"}, Env: map[string]string{}, AllowedTools: []string{"bar"}},
	}

	out, _, err := RenderBytes(base, servers, InstructionsSpec{Manage: false}, SkillsSpec{Manage: false}, false)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Non-MCP keys preserved.
	if root["unrelated"] == nil {
		t.Fatal("unrelated key should be preserved")
	}

	// MCP entry present.
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatal("mcp key missing or wrong type")
	}
	if _, ok := mcp["foo"]; !ok {
		t.Fatal("mcp missing foo entry")
	}

	// Tools present.
	tools, ok := root["tools"].(map[string]any)
	if !ok {
		t.Fatal("tools key missing or wrong type")
	}
	if tools["foo_bar"] != true {
		t.Fatalf("tools[foo_bar]: got %v want true", tools["foo_bar"])
	}
	if tools["foo_*"] != false {
		t.Fatalf("tools[foo_*]: got %v want false", tools["foo_*"])
	}
}

func TestRenderBytes_ResolvesEnvRefs(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	t.Setenv("TEST_TOKEN", "secret")

	servers := []domain.MCPServer{
		{
			Name:    "bb",
			Command: []string{"node", "{env:HOME}/server.js"},
			Env:     map[string]string{"TOKEN": "{env:TEST_TOKEN}"},
		},
	}

	out, _, err := RenderBytes(nil, servers, InstructionsSpec{Manage: false}, SkillsSpec{Manage: false}, true)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp"].(map[string]any)
	bb := mcp["bb"].(map[string]any)
	cmd := bb["command"].([]any)
	if len(cmd) < 2 || cmd[1] != "/tmp/test-home/server.js" {
		t.Fatalf("command not resolved: %v", cmd)
	}
	env := bb["environment"].(map[string]any)
	if env["TOKEN"] != "secret" {
		t.Fatalf("env TOKEN not resolved: %v", env["TOKEN"])
	}
}

func TestRenderBytes_PreservesServerOnMissingEnvVar(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	t.Setenv("MISSING_VAR", "")

	servers := []domain.MCPServer{
		{Name: "ok", Command: []string{"node", "{env:HOME}/ok.js"}, Env: map[string]string{}},
		{Name: "bad", Command: []string{"node", "{env:MISSING_VAR}/bad.js"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, InstructionsSpec{Manage: false}, SkillsSpec{Manage: false}, true)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp"].(map[string]any)
	if _, ok := mcp["ok"]; !ok {
		t.Fatal("ok server should be present")
	}
	bad, ok := mcp["bad"].(map[string]any)
	if !ok {
		t.Fatal("bad server should be preserved when env var is missing")
	}
	cmd := bad["command"].([]any)
	if len(cmd) < 2 || cmd[1] != "{env:MISSING_VAR}/bad.js" {
		t.Fatalf("missing env ref should stay literal: got %v", cmd)
	}
}

func TestRenderBytes_PopulatesTimeout(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "foo", Timeout: 60, Command: []string{"echo"}, Env: map[string]string{}},
		{Name: "bar", Timeout: 0, Command: []string{"echo"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, InstructionsSpec{Manage: false}, SkillsSpec{Manage: false}, false)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp"].(map[string]any)
	fooTimeout := int(mcp["foo"].(map[string]any)["timeout"].(float64))
	barTimeout := int(mcp["bar"].(map[string]any)["timeout"].(float64))

	if fooTimeout != 60000 {
		t.Fatalf("foo timeout: got %d want 60000", fooTimeout)
	}
	if barTimeout != 10000 {
		t.Fatalf("bar timeout: got %d want 10000", barTimeout)
	}
}

// --- Instructions/Skills spec tests ---

func TestBuildInstructionsSpec_Disabled(t *testing.T) {
	t.Parallel()
	spec := BuildInstructionsSpec([]string{"/rules"}, []string{"/rules/team.md"}, false)
	if spec.Manage {
		t.Fatal("expected Manage=false when disabled")
	}
}

func TestBuildInstructionsSpec_Enabled(t *testing.T) {
	t.Parallel()
	spec := BuildInstructionsSpec([]string{"/rules"}, []string{"/rules/team.md"}, true)
	if !spec.Manage {
		t.Fatal("expected Manage=true")
	}
	if len(spec.Managed) == 0 {
		t.Fatal("expected managed entries")
	}
	if len(spec.Desired) == 0 {
		t.Fatal("expected desired entries")
	}
}

func TestBuildSkillsSpec_Disabled(t *testing.T) {
	t.Parallel()
	spec := BuildSkillsSpec([]string{"/skills"}, []string{"/skills"}, false)
	if spec.Manage {
		t.Fatal("expected Manage=false when disabled")
	}
}

func TestMergeInstructions_NoManage(t *testing.T) {
	t.Parallel()
	root := map[string]any{"instructions": []any{"user-rule.md"}}
	MergeInstructions(root, InstructionsSpec{Manage: false})
	// Should not touch instructions.
	if root["instructions"] == nil {
		t.Fatal("instructions should not be removed when not managing")
	}
}

// --- Managed roots tests ---

func TestManagedRootsProject(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	roots := ManagedRootsProject(projectDir)

	wantRules := filepath.Join(projectDir, ".opencode", "rules")
	wantSkills := filepath.Join(projectDir, ".opencode", "skills")

	foundRules, foundSkills := false, false
	for _, r := range roots {
		if r == wantRules {
			foundRules = true
		}
		if r == wantSkills {
			foundSkills = true
		}
	}
	if !foundRules {
		t.Fatalf("missing rules dir %q; got %v", wantRules, roots)
	}
	if !foundSkills {
		t.Fatalf("missing skills dir %q; got %v", wantSkills, roots)
	}
}

func TestStrictExtraDirsProject(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	dirs := StrictExtraDirsProject(projectDir)

	wantRules := filepath.Join(projectDir, ".opencode", "rules")
	found := false
	for _, d := range dirs {
		if d == wantRules {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing rules dir %q; got %v", wantRules, dirs)
	}
}

// --- Capture tests ---

func TestCapture_Project(t *testing.T) {
	projectDir := t.TempDir()

	// Create agents dir with a file.
	agentsDir := filepath.Join(projectDir, ".opencode", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("# Reviewer"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create rules dir.
	rulesDir := filepath.Join(projectDir, ".opencode", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "team.md"), []byte("# Team"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(res.Agents) == 0 {
		t.Fatal("expected Agents to be populated")
	}
	if len(res.Rules) == 0 {
		t.Fatal("expected Rules to be populated")
	}

	// Check copies include agents and rules.
	foundAgent, foundRule := false, false
	for _, c := range res.Copies {
		if c.Dst == filepath.Join("agents", "reviewer.md") {
			foundAgent = true
		}
		if c.Dst == filepath.Join("rules", "team.md") {
			foundRule = true
		}
	}
	if !foundAgent {
		t.Fatalf("missing agent copy; got %v", copyDsts(res.Copies))
	}
	if !foundRule {
		t.Fatalf("missing rule copy; got %v", copyDsts(res.Copies))
	}

	// Typed fields.
	if len(res.Agents) != 1 || res.Agents[0].Name != "reviewer" {
		t.Fatalf("expected typed agent 'reviewer'; got %v", res.Agents)
	}
	if len(res.Rules) != 1 || res.Rules[0].Name != "team" {
		t.Fatalf("expected typed rule 'team'; got %v", res.Rules)
	}
}

func TestCapture_Project_ParsesUnprefixedAllowedTools(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	opencodeDir := filepath.Join(projectDir, ".opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{
	  "mcp": {
	    "foo": {
	      "enabled": true,
	      "type": "local",
	      "command": ["echo", "hi"]
	    }
	  },
	  "tools": {
	    "foo_bar": true,
	    "foo_bar_baz": true,
	    "foo_*": false
	  }
	}`)
	if err := os.WriteFile(filepath.Join(opencodeDir, "opencode.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Harness{}.Capture(harness.CaptureContext{Scope: domain.ScopeProject, ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	got := res.AllowedTools["foo"]
	if len(got) != 2 || got[0] != "bar" || got[1] != "bar_baz" {
		t.Fatalf("AllowedTools[foo] = %v, want [bar bar_baz]", got)
	}
	if len(res.MCPServers) != 1 || len(res.MCPServers["foo"].Command) != 2 {
		t.Fatalf("expected captured foo MCP server, got %+v", res.MCPServers)
	}
}

// --- StripManagedKeys tests ---

func TestStripManagedKeys_RemovesMCPAndTools(t *testing.T) {
	t.Parallel()
	input := []byte(`{"mcp": {"foo": {}}, "tools": {"bar": true}, "custom": "keep"}`)
	out, err := StripManagedKeys(input, "opencode.json")
	if err != nil {
		t.Fatalf("StripManagedKeys: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := root["mcp"]; ok {
		t.Fatal("mcp should be stripped")
	}
	if _, ok := root["tools"]; ok {
		t.Fatal("tools should be stripped")
	}
	if root["custom"] != "keep" {
		t.Fatal("custom key should be preserved")
	}
}

func TestStripManagedKeys_OhMyOpenCode_PassThrough(t *testing.T) {
	t.Parallel()
	input := []byte(`{"mcp": {"foo": {}}}`)
	out, err := StripManagedKeys(input, "oh-my-opencode.json")
	if err != nil {
		t.Fatalf("StripManagedKeys: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := root["mcp"]; !ok {
		t.Fatal("oh-my-opencode.json should pass through without stripping")
	}
}

// --- Helpers ---

func writeDsts(writes []domain.WriteAction) []string {
	var out []string
	for _, w := range writes {
		out = append(out, w.Dst)
	}
	return out
}

func copyDsts(copies []domain.CopyAction) []string {
	var out []string
	for _, c := range copies {
		out = append(out, c.Dst)
	}
	return out
}

func assertHasWriteDst(t *testing.T, writes []domain.WriteAction, dst string) {
	t.Helper()
	for _, w := range writes {
		if w.Dst == dst {
			return
		}
	}
	t.Fatalf("expected write to %q; got writes: %v", dst, writeDsts(writes))
}
