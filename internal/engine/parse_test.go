package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/testutil"
)

func TestParseRules_Basic(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\ndescription: Test rule\n---\nRule body\n")
	if err := os.WriteFile(filepath.Join(rulesDir, "alpha.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	rules, warnings, err := eng.parseRules(config.ResolvedPack{Root: dir, Name: "testpack", Rules: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	r := rules[0]
	if r.Name != "alpha" {
		t.Errorf("Name = %q, want %q", r.Name, "alpha")
	}
	if r.Frontmatter.Description != "Test rule" {
		t.Errorf("Frontmatter.Description = %q", r.Frontmatter.Description)
	}
	if string(r.Body) != "Rule body\n" {
		t.Errorf("Body = %q", r.Body)
	}
	if string(r.Raw) != string(content) {
		t.Errorf("Raw mismatch")
	}
	if r.SourcePack != "testpack" {
		t.Errorf("SourcePack = %q", r.SourcePack)
	}
}

func TestParseRules_NoFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("Just a rule body\n")
	if err := os.WriteFile(filepath.Join(rulesDir, "simple.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	rules, _, err := eng.parseRules(config.ResolvedPack{Root: dir, Name: "pack1", Rules: []string{"simple"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if rules[0].Frontmatter.Description != "" {
		t.Errorf("expected empty frontmatter description, got %q", rules[0].Frontmatter.Description)
	}
	if string(rules[0].Body) != "Just a rule body\n" {
		t.Errorf("Body = %q", rules[0].Body)
	}
}

func TestParseAgents_NameFromFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\nname: Custom Name\ndescription: An agent\ntools:\n  - Read\n  - Write\n---\nSystem prompt\n")
	if err := os.WriteFile(filepath.Join(agentsDir, "myagent.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	agents, _, err := eng.parseAgents(config.ResolvedPack{Root: dir, Name: "pack1", Agents: []string{"myagent"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(agents))
	}
	a := agents[0]
	if a.Name != "Custom Name" {
		t.Errorf("Name = %q, want %q", a.Name, "Custom Name")
	}
	if a.Frontmatter.Description != "An agent" {
		t.Errorf("Description = %q", a.Frontmatter.Description)
	}
	if len(a.Frontmatter.Tools) != 2 {
		t.Errorf("Tools = %v", a.Frontmatter.Tools)
	}
}

func TestParseAgents_FallbackToFilename(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\ndescription: No name field\n---\nPrompt\n")
	if err := os.WriteFile(filepath.Join(agentsDir, "bot.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	agents, _, err := eng.parseAgents(config.ResolvedPack{Root: dir, Name: "pack1", Agents: []string{"bot"}})
	if err != nil {
		t.Fatal(err)
	}
	if agents[0].Name != "bot" {
		t.Errorf("Name = %q, want %q", agents[0].Name, "bot")
	}
}

func TestParseWorkflows_Basic(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\ntitle: Deploy\ndescription: Deploy workflow\n---\n1. Build\n2. Deploy\n")
	if err := os.WriteFile(filepath.Join(wfDir, "deploy.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	workflows, _, err := eng.parseWorkflows(config.ResolvedPack{Root: dir, Name: "pack1", Workflows: []string{"deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(workflows))
	}
	if workflows[0].Frontmatter.Title != "Deploy" {
		t.Errorf("Title = %q", workflows[0].Frontmatter.Title)
	}
	if workflows[0].SourcePack != "pack1" {
		t.Errorf("SourcePack = %q", workflows[0].SourcePack)
	}
}

func TestParseSkills_Basic(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "onboard")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\nname: Onboard\ndescription: Onboarding skill\n---\nInstructions\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	skills, _, err := eng.parseSkills(config.ResolvedPack{Root: dir, Name: "pack1", Skills: []string{"onboard"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	if skills[0].Frontmatter.Name != "Onboard" {
		t.Errorf("Name = %q", skills[0].Frontmatter.Name)
	}
	if skills[0].DirPath != skillDir {
		t.Errorf("DirPath = %q", skills[0].DirPath)
	}
}

func TestParseSkills_ExpandsNestedDirectorySymlinkAssets(t *testing.T) {
	t.Parallel()
	repoRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(repoRoot, "shared-config")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "team.toml"), []byte("team = \"example\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packRoot := filepath.Join(repoRoot, "pack")
	skillDir := filepath.Join(packRoot, "skills", "diagnose")
	assetsDir := filepath.Join(skillDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, domain.SkillEntryFile), []byte("---\nname: diagnose\ndescription: Diagnose issues\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, shared, filepath.Join(assetsDir, "shared-config"))

	skills, warnings, err := New(nil, nil).parseSkills(config.ResolvedPack{
		Root: packRoot, Name: "test-pack", Skills: []string{"diagnose"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	if want := []string{"assets/shared-config/team.toml"}; !slices.Equal(skills[0].Assets, want) {
		t.Fatalf("Assets = %v, want %v", skills[0].Assets, want)
	}
	wantBoundary, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if skills[0].SourceBoundary != wantBoundary {
		t.Fatalf("SourceBoundary = %q, want %q", skills[0].SourceBoundary, wantBoundary)
	}
}

func TestParseSkills_RejectsDirectorySymlinkOutsideSourceBoundary(t *testing.T) {
	t.Parallel()
	repoRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "team.toml"), []byte("team = \"example\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packRoot := filepath.Join(repoRoot, "pack")
	skillDir := filepath.Join(packRoot, "skills", "diagnose")
	assetsDir := filepath.Join(skillDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, domain.SkillEntryFile), []byte("---\nname: diagnose\ndescription: Diagnose issues\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, outside, filepath.Join(assetsDir, "shared-config"))

	_, _, err := New(nil, nil).parseSkills(config.ResolvedPack{
		Root: packRoot, Name: "test-pack", Skills: []string{"diagnose"},
	})
	if err == nil || !strings.Contains(err.Error(), "escapes boundary") {
		t.Fatalf("parseSkills error = %v, want boundary escape", err)
	}
}

func TestFlattenRules(t *testing.T) {
	t.Parallel()
	// Nil input returns empty.
	result := FlattenRules(nil)
	if result != "" {
		t.Errorf("FlattenRules(nil) = %q, want empty", result)
	}

	// Pass rules in reverse order — output should be sorted by name.
	domainRules := []domain.Rule{
		{Name: "beta", Raw: []byte("beta content")},
		{Name: "alpha", Raw: []byte("alpha content")},
	}
	result = FlattenRules(domainRules)
	if result == "" {
		t.Fatal("FlattenRules with content should not be empty")
	}
	if !strings.Contains(result, "<!-- source: alpha.md -->") {
		t.Error("expected source comment for alpha")
	}
	if !strings.Contains(result, "<!-- source: beta.md -->") {
		t.Error("expected source comment for beta")
	}

	// Alpha should appear before beta regardless of input order.
	alphaIdx := strings.Index(result, "<!-- source: alpha.md -->")
	betaIdx := strings.Index(result, "<!-- source: beta.md -->")
	if alphaIdx >= betaIdx {
		t.Errorf("expected alpha before beta in sorted output, got alpha@%d beta@%d", alphaIdx, betaIdx)
	}
	if !strings.Contains(result, "---") {
		t.Error("expected separator")
	}

	// Input slice must not be mutated (order preserved).
	if domainRules[0].Name != "beta" || domainRules[1].Name != "alpha" {
		t.Errorf("FlattenRules mutated input slice: got [%s, %s], want [beta, alpha]",
			domainRules[0].Name, domainRules[1].Name)
	}
}

// ---------------------------------------------------------------------------
// Byte-based parse helpers
// ---------------------------------------------------------------------------

func TestParseRuleBytes(t *testing.T) {
	t.Parallel()

	raw := []byte("---\ndescription: Test rule\n---\nRule body\n")
	r, err := ParseRuleBytes(raw, "alpha", "testpack")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "alpha" {
		t.Errorf("Name = %q, want %q", r.Name, "alpha")
	}
	if r.Frontmatter.Description != "Test rule" {
		t.Errorf("Description = %q", r.Frontmatter.Description)
	}
	if string(r.Body) != "Rule body\n" {
		t.Errorf("Body = %q", r.Body)
	}
	if string(r.Raw) != string(raw) {
		t.Error("Raw mismatch")
	}
	if r.SourcePack != "testpack" {
		t.Errorf("SourcePack = %q", r.SourcePack)
	}
}

func TestParseRuleBytes_NoFrontmatter(t *testing.T) {
	t.Parallel()

	raw := []byte("Just a body\n")
	r, err := ParseRuleBytes(raw, "simple", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Frontmatter.Description != "" {
		t.Errorf("expected empty description, got %q", r.Frontmatter.Description)
	}
	if string(r.Body) != "Just a body\n" {
		t.Errorf("Body = %q", r.Body)
	}
}

func TestParseAgentBytes(t *testing.T) {
	t.Parallel()

	raw := []byte("---\nname: Custom Agent\ndescription: An agent\ntools:\n  - Read\n  - Write\n---\nSystem prompt\n")
	a, err := ParseAgentBytes(raw, "myagent", "pack1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Custom Agent" {
		t.Errorf("Name = %q, want %q", a.Name, "Custom Agent")
	}
	if a.Frontmatter.Description != "An agent" {
		t.Errorf("Description = %q", a.Frontmatter.Description)
	}
	if len(a.Frontmatter.Tools) != 2 {
		t.Errorf("Tools = %v", a.Frontmatter.Tools)
	}
	if a.SourcePack != "pack1" {
		t.Errorf("SourcePack = %q", a.SourcePack)
	}
}

func TestParseAgentBytes_FallbackToFilename(t *testing.T) {
	t.Parallel()

	raw := []byte("---\ndescription: No name\n---\nPrompt\n")
	a, err := ParseAgentBytes(raw, "bot", "pack1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "bot" {
		t.Errorf("Name = %q, want %q", a.Name, "bot")
	}
}

func TestParseWorkflowBytes(t *testing.T) {
	t.Parallel()

	raw := []byte("---\ntitle: Deploy\ndescription: Deploy workflow\n---\n1. Build\n2. Deploy\n")
	w, err := ParseWorkflowBytes(raw, "deploy", "pack1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "deploy" {
		t.Errorf("Name = %q", w.Name)
	}
	if w.Frontmatter.Title != "Deploy" {
		t.Errorf("Title = %q", w.Frontmatter.Title)
	}
	if w.SourcePack != "pack1" {
		t.Errorf("SourcePack = %q", w.SourcePack)
	}
}

func TestRenderAgentBytes_NeutralSchema(t *testing.T) {
	t.Parallel()
	a := domain.Agent{
		Frontmatter: domain.AgentFrontmatter{
			Name:            "reviewer",
			Description:     "Reviews changes",
			Tools:           []string{"bash", "read"},
			DisallowedTools: []string{"write"},
			Skills:          []string{"triage"},
			MCPServers:      []string{"atlassian"},
		},
		Body: []byte("Review\n"),
	}

	raw, err := RenderAgentBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "bash: true") {
		t.Fatalf("expected neutral tools list, got native map form:\n%s", raw)
	}
	parsed, err := ParseAgentBytes(raw, "reviewer", "")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Frontmatter.Description != "Reviews changes" {
		t.Fatalf("Description = %q", parsed.Frontmatter.Description)
	}
	if len(parsed.Frontmatter.Tools) != 2 {
		t.Fatalf("Tools = %v, want 2 entries", parsed.Frontmatter.Tools)
	}
	if len(parsed.Frontmatter.DisallowedTools) != 1 || parsed.Frontmatter.DisallowedTools[0] != "write" {
		t.Fatalf("DisallowedTools = %v", parsed.Frontmatter.DisallowedTools)
	}
	if len(parsed.Frontmatter.Skills) != 1 || parsed.Frontmatter.Skills[0] != "triage" {
		t.Fatalf("Skills = %v", parsed.Frontmatter.Skills)
	}
	if len(parsed.Frontmatter.MCPServers) != 1 || parsed.Frontmatter.MCPServers[0] != "atlassian" {
		t.Fatalf("MCPServers = %v", parsed.Frontmatter.MCPServers)
	}
}

func TestRenderAgentBytes_StripsHarness(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Frontmatter: domain.AgentFrontmatter{
			Name:        "explorer",
			Description: "Fast exploration",
			Tools:       []string{"Read"},
			Harness: map[string]map[string]any{
				"codex": {"model": "o3", "model_reasoning_effort": "high"},
			},
		},
		Body: []byte("You explore.\n"),
	}
	out, err := RenderAgentBytes(agent)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "harness") {
		t.Fatalf("rendered markdown should not contain harness block:\n%s", s)
	}
	if strings.Contains(s, "codex") {
		t.Fatalf("rendered markdown should not contain harness-specific keys:\n%s", s)
	}
	if !strings.Contains(s, "explorer") {
		t.Fatal("should preserve name")
	}
	if !strings.Contains(s, "You explore.") {
		t.Fatal("should preserve body")
	}
}

func TestRenderAgentBytes_RoundTripsWithHarness(t *testing.T) {
	t.Parallel()
	agent := domain.Agent{
		Frontmatter: domain.AgentFrontmatter{
			Name:        "reviewer",
			Description: "Reviews changes",
			Tools:       []string{"bash", "read"},
			Harness: map[string]map[string]any{
				"codex": {"model": "o3"},
			},
		},
		Body: []byte("Review\n"),
	}
	raw, err := RenderAgentBytes(agent)
	if err != nil {
		t.Fatal(err)
	}
	// Parse should succeed — harness is stripped, so no round-trip of that field.
	parsed, err := ParseAgentBytes(raw, "reviewer", "")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Frontmatter.Description != "Reviews changes" {
		t.Fatalf("Description = %q", parsed.Frontmatter.Description)
	}
	// Harness should NOT round-trip through markdown rendering.
	if parsed.Frontmatter.Harness != nil {
		t.Fatalf("Harness should be nil after round-trip, got %v", parsed.Frontmatter.Harness)
	}
}

func TestParseRules_MissingFile(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := eng.parseRules(config.ResolvedPack{Root: dir, Name: "pack1", Rules: []string{"nonexistent"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading rule") {
		t.Errorf("error should contain context, got: %s", err)
	}
}

func TestParseAgents_MissingFile(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := eng.parseAgents(config.ResolvedPack{Root: dir, Name: "pack1", Agents: []string{"nonexistent"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading agent") {
		t.Errorf("error should contain context, got: %s", err)
	}
}

func TestParseWorkflows_MissingFile(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := eng.parseWorkflows(config.ResolvedPack{Root: dir, Name: "pack1", Workflows: []string{"nonexistent"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading workflow") {
		t.Errorf("error should contain context, got: %s", err)
	}
}

func TestParseSkills_MissingFile(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills", "nonexistent"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := eng.parseSkills(config.ResolvedPack{Root: dir, Name: "pack1", Skills: []string{"nonexistent"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading skill") {
		t.Errorf("error should contain context, got: %s", err)
	}
}

func TestParseRules_InvalidFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("---\n[invalid yaml\n---\nBody\n")
	if err := os.WriteFile(filepath.Join(rulesDir, "bad.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	rules, warnings, err := eng.parseRules(config.ResolvedPack{Root: dir, Name: "pack1", Rules: []string{"bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if warnings[0].Field != "frontmatter" {
		t.Errorf("warning field = %q", warnings[0].Field)
	}
}

func TestParseAgents_InvalidFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\n[invalid yaml\n---\nBody\n")
	if err := os.WriteFile(filepath.Join(agentsDir, "bad.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	agents, warnings, err := eng.parseAgents(config.ResolvedPack{Root: dir, Name: "pack1", Agents: []string{"bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(agents))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if warnings[0].Field != "frontmatter" {
		t.Errorf("warning field = %q", warnings[0].Field)
	}
}

func TestParseWorkflows_InvalidFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\n[invalid yaml\n---\nBody\n")
	if err := os.WriteFile(filepath.Join(wfDir, "bad.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	workflows, warnings, err := eng.parseWorkflows(config.ResolvedPack{Root: dir, Name: "pack1", Workflows: []string{"bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(workflows))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if warnings[0].Field != "frontmatter" {
		t.Errorf("warning field = %q", warnings[0].Field)
	}
}

func TestParseSkills_InvalidFrontmatter(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "bad")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\n[invalid yaml\n---\nBody\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	skills, warnings, err := eng.parseSkills(config.ResolvedPack{Root: dir, Name: "pack1", Skills: []string{"bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if warnings[0].Field != "frontmatter" {
		t.Errorf("warning field = %q", warnings[0].Field)
	}
}
