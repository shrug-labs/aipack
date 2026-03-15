package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestFindResource_Rule(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name: "core",
			Rules: []domain.Rule{
				{Name: "anti-slop", SourcePack: "core", SourcePath: "/packs/core/rules/anti-slop.md"},
				{Name: "user-baseline", SourcePack: "core", SourcePath: "/packs/core/rules/user-baseline.md"},
			},
		}},
	}

	src := findResource(profile, "rule", "anti-slop")
	if src == nil {
		t.Fatal("expected to find rule anti-slop")
	}
	if src.Pack != "core" {
		t.Errorf("pack = %q, want %q", src.Pack, "core")
	}
	if src.Category != "rules" {
		t.Errorf("category = %q, want %q", src.Category, "rules")
	}
	if src.SourcePath != "/packs/core/rules/anti-slop.md" {
		t.Errorf("source_path = %q, want %q", src.SourcePath, "/packs/core/rules/anti-slop.md")
	}
}

func TestFindResource_NotFound(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:  "core",
			Rules: []domain.Rule{{Name: "existing", SourcePack: "core"}},
		}},
	}

	src := findResource(profile, "rule", "nonexistent")
	if src != nil {
		t.Fatalf("expected nil, got %+v", src)
	}
}

func TestFindResource_Agent(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name: "ops",
			Agents: []domain.Agent{
				{Name: "code-reviewer", SourcePack: "ops", SourcePath: "/packs/ops/agents/code-reviewer.md"},
			},
		}},
	}

	src := findResource(profile, "agent", "code-reviewer")
	if src == nil {
		t.Fatal("expected to find agent code-reviewer")
	}
	if src.Category != "agents" {
		t.Errorf("category = %q, want %q", src.Category, "agents")
	}
}

func TestFindResource_Workflow(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name: "ops",
			Workflows: []domain.Workflow{
				{Name: "deploy", SourcePack: "ops", SourcePath: "/packs/ops/workflows/deploy.md"},
			},
		}},
	}

	src := findResource(profile, "workflow", "deploy")
	if src == nil {
		t.Fatal("expected to find workflow deploy")
	}
	if src.Category != "workflows" {
		t.Errorf("category = %q, want %q", src.Category, "workflows")
	}
}

func TestFindResource_Skill(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name: "ops",
			Skills: []domain.Skill{
				{Name: "deep-research", SourcePack: "ops", DirPath: "/packs/ops/skills/deep-research"},
			},
		}},
	}

	src := findResource(profile, "skill", "deep-research")
	if src == nil {
		t.Fatal("expected to find skill deep-research")
	}
	if src.Category != "skills" {
		t.Errorf("category = %q, want %q", src.Category, "skills")
	}
	if src.SourcePath != "/packs/ops/skills/deep-research" {
		t.Errorf("source_path = %q, want %q", src.SourcePath, "/packs/ops/skills/deep-research")
	}
}

func TestFindResource_MCP(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		MCPServers: []domain.MCPServer{
			{Name: "atlassian", SourcePack: "core"},
		},
	}

	src := findResource(profile, "mcp", "atlassian")
	if src == nil {
		t.Fatal("expected to find mcp atlassian")
	}
	if src.Category != "mcp" {
		t.Errorf("category = %q, want %q", src.Category, "mcp")
	}
	if src.SourcePath != "" {
		t.Errorf("expected empty source_path for MCP, got %q", src.SourcePath)
	}
}

func TestFindResource_MultiPack(t *testing.T) {
	t.Parallel()
	profile := domain.Profile{
		Packs: []domain.Pack{
			{Name: "core", Rules: []domain.Rule{{Name: "shared", SourcePack: "core"}}},
			{Name: "ops", Rules: []domain.Rule{{Name: "team", SourcePack: "ops"}}},
		},
	}

	src := findResource(profile, "rule", "team")
	if src == nil {
		t.Fatal("expected to find rule team")
	}
	if src.Pack != "ops" {
		t.Errorf("pack = %q, want %q", src.Pack, "ops")
	}
}

func TestMatchesWrite(t *testing.T) {
	t.Parallel()

	source := &TraceSource{
		Pack:       "core",
		SourcePath: "/packs/core/rules/anti-slop.md",
		Category:   "rules",
	}

	t.Run("match by source path", func(t *testing.T) {
		wr := domain.WriteAction{
			Dst:        "/home/user/.claude/rules/anti-slop.md",
			Src:        "/packs/core/rules/anti-slop.md",
			SourcePack: "core",
		}
		matched, embedded := matchesWrite(wr, source, "anti-slop")
		if !matched {
			t.Error("expected match by source path")
		}
		if embedded {
			t.Error("expected not embedded")
		}
	})

	t.Run("no match different source", func(t *testing.T) {
		wr := domain.WriteAction{
			Dst:        "/home/user/.claude/rules/other.md",
			Src:        "/packs/core/rules/other.md",
			SourcePack: "core",
		}
		matched, _ := matchesWrite(wr, source, "anti-slop")
		if matched {
			t.Error("expected no match")
		}
	})

	t.Run("composite match in AGENTS.override.md", func(t *testing.T) {
		wr := domain.WriteAction{
			Dst:     "/home/user/.codex/AGENTS.override.md",
			Content: []byte("# aipack managed rules\n\n<!-- source: anti-slop.md -->\n# Anti-Slop\n\nDon't do slop.\n"),
		}
		matched, embedded := matchesWrite(wr, source, "anti-slop")
		if !matched {
			t.Error("expected composite match")
		}
		if !embedded {
			t.Error("expected embedded=true for composite")
		}
	})

	t.Run("composite no match when name absent", func(t *testing.T) {
		wr := domain.WriteAction{
			Dst:     "/home/user/.codex/AGENTS.override.md",
			Content: []byte("# aipack managed rules\n\n<!-- source: other-rule.md -->\n# Other\n\nSomething else.\n"),
		}
		matched, _ := matchesWrite(wr, source, "anti-slop")
		if matched {
			t.Error("expected no match when name not in content")
		}
	})
}

func TestIdentifyHarness(t *testing.T) {
	t.Parallel()
	reg := testRegistry()
	home := "/home/user"
	projectDir := "/home/user/project"
	tests := []struct {
		path string
		want domain.Harness
	}{
		{"/home/user/project/.claude/rules/test.md", domain.HarnessClaudeCode},
		{"/home/user/project/.opencode/rules/test.md", domain.HarnessOpenCode},
		{"/home/user/project/.clinerules/test.md", domain.HarnessCline},
		{"/home/user/project/.agents/skills/foo/SKILL.md", domain.HarnessCodex},
		{"/home/user/project/AGENTS.override.md", domain.HarnessCodex},
		{"/some/unknown/path.md", ""},
	}
	for _, tc := range tests {
		got := harness.IdentifyHarness(reg, domain.ScopeProject, projectDir, home, tc.path)
		if got != tc.want {
			t.Errorf("IdentifyHarness(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestRunTrace_ClassifiesPromotedContentWithSourceDigest(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	dstPath := filepath.Join(projectDir, ".agents", "skills", "demo-agent", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		t.Fatal(err)
	}

	promotedBytes := []byte("---\nname: demo-agent\n---\nbody\n")
	renderedBytes := []byte("---\nname: demo-agent\ndescription: promoted\n---\nbody\n")
	if err := os.WriteFile(dstPath, promotedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		dstPath: {
			SourcePack: "core",
			Digest:     domain.SingleFileDigest(promotedBytes),
		},
	})

	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name: "core",
			Agents: []domain.Agent{{
				Name:       "demo-agent",
				SourcePack: "core",
				SourcePath: filepath.Join(home, "packs", "core", "agents", "demo-agent.md"),
			}},
		}},
	}

	reg := harness.NewRegistry(planHarnessStub{
		id: "codex",
		fragment: domain.Fragment{
			Writes: []domain.WriteAction{{
				Dst:          dstPath,
				Content:      renderedBytes,
				SourcePack:   "core",
				Src:          filepath.Join(home, "packs", "core", "agents", "demo-agent.md"),
				IsContent:    true,
				SourceDigest: domain.SingleFileDigest(promotedBytes),
			}},
			Desired: []string{dstPath},
		},
		roots: []string{filepath.Dir(dstPath)},
	})

	result, err := RunTrace(profile, TraceRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
		},
		ResourceType: "agent",
		ResourceName: "demo-agent",
	}, reg)
	if err != nil {
		t.Fatalf("RunTrace: %v", err)
	}

	if len(result.Destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(result.Destinations))
	}
	if result.Destinations[0].State != "identical" {
		t.Fatalf("state = %q, want identical", result.Destinations[0].State)
	}
}

func TestRunTrace_ClassifiesTrackedMCPConflictFromLiveConfig(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	configPath := filepath.Join(projectDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[mcp_servers.jira]\ncommand='uvx'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	desiredServer := domain.MCPServer{
		Name:       "jira",
		Transport:  domain.TransportStdio,
		Command:    []string{"uvx", "jira-mcp"},
		SourcePack: "core",
	}
	desiredContent, err := domain.MCPTrackedBytes(desiredServer)
	if err != nil {
		t.Fatalf("MCPTrackedBytes(desired): %v", err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		domain.MCPLedgerKey(configPath, "jira"): {
			SourcePack: "core",
			Digest:     domain.SingleFileDigest(desiredContent),
		},
	})

	liveServer := desiredServer
	liveServer.Command = []string{"uvx", "jira-mcp", "--debug"}

	profile := domain.Profile{
		MCPServers: []domain.MCPServer{desiredServer},
	}

	reg := harness.NewRegistry(planHarnessStub{
		id: "codex",
		fragment: domain.Fragment{
			MCPServers: []domain.MCPAction{{
				Name:       "jira",
				ConfigPath: configPath,
				Content:    desiredContent,
				SourcePack: "core",
				Harness:    domain.HarnessCodex,
				Embedded:   true,
			}},
			Desired: []string{configPath},
		},
		capture: harness.CaptureResult{
			MCP: []domain.CapturedMCP{{
				Server:      liveServer,
				HarnessPath: configPath,
			}},
		},
		roots: []string{filepath.Dir(configPath)},
	})

	result, err := RunTrace(profile, TraceRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
		},
		ResourceType: "mcp",
		ResourceName: "jira",
	}, reg)
	if err != nil {
		t.Fatalf("RunTrace: %v", err)
	}

	if len(result.Destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(result.Destinations))
	}
	if result.Destinations[0].State != "conflict" {
		t.Fatalf("state = %q, want conflict", result.Destinations[0].State)
	}
	if result.Destinations[0].DiffKind != domain.DiffConflict {
		t.Fatalf("diff_kind = %q, want %q", result.Destinations[0].DiffKind, domain.DiffConflict)
	}
}
