package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
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
		t.Fatal("expected name in skill frontmatter")
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

// --- Layout tests ---

func TestLayout_Project(t *testing.T) {
	t.Parallel()
	h := Harness{}
	layout := h.Layout(domain.ScopeProject, "/proj", "/home")

	want := map[string]bool{
		filepath.Join("/proj", ".agents", "skills"):     false,
		filepath.Join("/proj", ".codex", "agents"):      false,
		filepath.Join("/proj", "AGENTS.override.md"):    false,
		filepath.Join("/proj", ".codex", "config.toml"): false,
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
		filepath.Join("/proj", ".agents", "skills"):  false,
		filepath.Join("/proj", ".codex", "agents"):   false,
		filepath.Join("/proj", "AGENTS.override.md"): false,
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

	if len(layout.OwnedFiles) != 1 {
		t.Fatalf("expected 1 OwnedFile, got %d", len(layout.OwnedFiles))
	}
}

func TestLayout_Global(t *testing.T) {
	t.Parallel()
	h := Harness{}
	layout := h.Layout(domain.ScopeGlobal, "/home", "/home")

	want := map[string]bool{
		filepath.Join("/home", ".agents", "skills"):            false,
		filepath.Join("/home", ".codex", "agents"):             false,
		filepath.Join("/home", ".codex", "AGENTS.override.md"): false,
		filepath.Join("/home", ".codex", "config.toml"):        false,
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
		filepath.Join("/home", ".agents", "skills"):            false,
		filepath.Join("/home", ".codex", "agents"):             false,
		filepath.Join("/home", ".codex", "AGENTS.override.md"): false,
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

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), workflows)

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".agents", "skills", "remediate", "SKILL.md")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "name: remediate") {
		t.Fatal("expected name in frontmatter")
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
	wantDir := filepath.Join("/project", ".agents", "skills", "remediate")
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

	regs, _ := addNativeAgents(&f, "/project/.codex/agents", agents, nil, "/project", filepath.Join(".agents", "skills"))

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
	if reg["config_file"] != "./agents/reviewer.toml" {
		t.Fatalf("config_file = %v", reg["config_file"])
	}
}

func TestPromoteWorkflow_EmptyBody_Skipped(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{Name: "empty", Body: []byte("  \n  "), SourcePack: "pack-a"},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), workflows)

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

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), workflows)

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

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), workflows)

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

// --- Helpers ---

func writeDsts(writes []domain.WriteAction) []string {
	var out []string
	for _, w := range writes {
		out = append(out, w.Dst)
	}
	return out
}
