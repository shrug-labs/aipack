package codex

import (
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

	f, err := Harness{}.Plan(ctx)
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

	// Agent should be promoted to a skill.
	skillPath := filepath.Join(projectDir, ".agents", "skills", "reviewer", "SKILL.md")
	var skillContent string
	for _, w := range f.Writes {
		if w.Dst == skillPath {
			skillContent = string(w.Content)
		}
	}
	if skillContent == "" {
		t.Fatalf("expected promoted agent skill at %q; got writes: %v", skillPath, writeDsts(f.Writes))
	}
	if !strings.Contains(skillContent, "Reviews code") {
		t.Fatal("expected agent body in promoted skill")
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

	f, err := Harness{}.Plan(ctx)
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

	f, err := Harness{}.Plan(ctx)
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

	f, err := Harness{}.Plan(ctx)
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

	f, err := Harness{}.Plan(ctx)
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

	out, _, err := RenderBytes(base, servers, false)
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

	out, _, err := RenderBytes(nil, servers, false)
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

	out, _, err := RenderBytes(nil, servers, false)
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

func TestRenderBytes_EnvRefsTransformed(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "monitoring", Command: []string{"uvx", "--env-file", "{env:MON_ENV_FILE}", "monitoring_mcp@latest"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, false)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	mon := mcp["monitoring"].(map[string]any)

	// Env refs should be transformed to shell syntax, no bash -lc wrapping.
	if mon["command"] != "uvx" {
		t.Fatalf("expected direct command 'uvx': got %q", mon["command"])
	}
	args, ok := mon["args"].([]any)
	if !ok || len(args) < 3 {
		t.Fatalf("expected 3 args: %v", mon["args"])
	}
	if args[1] != "$MON_ENV_FILE" {
		t.Fatalf("expected env ref transformed to $MON_ENV_FILE: got %q", args[1])
	}
}

func TestRenderBytes_DirectCommand(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "plain", Command: []string{"node", "server.js"}, Env: map[string]string{}},
	}

	out, _, err := RenderBytes(nil, servers, false)
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

func TestRenderBytes_SkipsNonStdio(t *testing.T) {
	t.Parallel()
	servers := []domain.MCPServer{
		{Name: "stdio-srv", Command: []string{"node", "srv.js"}, Env: map[string]string{}},
		{Name: "sse-srv", Transport: domain.TransportSSE, URL: "https://example.com/sse"},
	}

	out, _, err := RenderBytes(nil, servers, false)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcp := root["mcp_servers"].(map[string]any)
	if _, ok := mcp["stdio-srv"]; !ok {
		t.Fatal("stdio server should be included")
	}
	if _, ok := mcp["sse-srv"]; ok {
		t.Fatal("non-stdio server should be skipped")
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

// --- StripManagedKeys tests ---

func TestStripManagedKeys_RemovesMCPServers(t *testing.T) {
	t.Parallel()
	input := []byte("foo = 'bar'\n\n[mcp_servers.test]\nenabled = true\ncommand = 'echo'\n")
	out, err := StripManagedKeys(input)
	if err != nil {
		t.Fatalf("StripManagedKeys: %v", err)
	}

	var root map[string]any
	if err := toml.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := root["mcp_servers"]; ok {
		t.Fatal("mcp_servers should be stripped")
	}
	if root["foo"] != "bar" {
		t.Fatal("non-MCP keys should be preserved")
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

	res, err := Harness{}.Capture(harness.CaptureContext{
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

// --- Managed roots tests ---

func TestManagedRootsProject(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	roots := ManagedRootsProject(projectDir)

	wantOverride := filepath.Join(projectDir, "AGENTS.override.md")
	found := false
	for _, r := range roots {
		if r == wantOverride {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing AGENTS.override.md in managed roots; got %v", roots)
	}
}

// --- Promote tests ---

func TestPromoteWorkflows_GeneratesSkillMD(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	workflows := []domain.Workflow{
		{
			Name:        "starfix",
			Frontmatter: domain.WorkflowFrontmatter{Name: "starfix", Description: "StarFix remediation workflow"},
			Body:        []byte("## Steps\n\n1. Run starfix\n2. Verify"),
			SourcePack:  "pack-a",
			SourcePath:  "/packs/pack-a/workflows/starfix.md",
		},
	}

	addPromotedWorkflows(&f, "/project", filepath.Join(".agents", "skills"), workflows)

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".agents", "skills", "starfix", "SKILL.md")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "name: starfix") {
		t.Fatal("expected name in frontmatter")
	}
	if !strings.Contains(content, "description: StarFix remediation workflow") {
		t.Fatal("expected description in frontmatter")
	}
	if !strings.Contains(content, "## Steps") {
		t.Fatal("expected body content")
	}
	// Desired should be the skill directory, not the SKILL.md file.
	wantDir := filepath.Join("/project", ".agents", "skills", "starfix")
	if len(f.Desired) != 1 || f.Desired[0] != wantDir {
		t.Fatalf("desired: got %v want [%s]", f.Desired, wantDir)
	}
}

func TestPromoteAgents_GeneratesSkillMD(t *testing.T) {
	t.Parallel()
	var f domain.Fragment
	agents := []domain.Agent{
		{
			Name:        "reviewer",
			Frontmatter: domain.AgentFrontmatter{Name: "reviewer", Description: "Code review specialist"},
			Body:        []byte("You are a code reviewer."),
			SourcePack:  "pack-a",
			SourcePath:  "/packs/pack-a/agents/reviewer.md",
		},
	}

	addPromotedAgents(&f, "/project", filepath.Join(".agents", "skills"), agents)

	if len(f.Writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(f.Writes))
	}
	w := f.Writes[0]
	wantDst := filepath.Join("/project", ".agents", "skills", "reviewer", "SKILL.md")
	if w.Dst != wantDst {
		t.Fatalf("dst: got %q want %q", w.Dst, wantDst)
	}
	content := string(w.Content)
	if !strings.Contains(content, "name: reviewer") {
		t.Fatal("expected name in frontmatter")
	}
	if !strings.Contains(content, "description: Code review specialist") {
		t.Fatal("expected description in frontmatter")
	}
	if !strings.Contains(content, "You are a code reviewer.") {
		t.Fatal("expected body content")
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
	if !strings.Contains(content, "description: \"Workflow: deploy\"") {
		t.Fatalf("expected fallback description, got:\n%s", content)
	}
}

func TestBuildSkillMD_Format(t *testing.T) {
	t.Parallel()
	got := buildSkillMD("my-skill", "Does something useful", "# Instructions\n\nDo the thing.")
	want := "---\nname: my-skill\ndescription: Does something useful\n---\n\n# Instructions\n\nDo the thing.\n"
	if got != want {
		t.Fatalf("buildSkillMD:\ngot:\n%s\nwant:\n%s", got, want)
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
