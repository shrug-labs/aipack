package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

type planHarnessStub struct {
	id       domain.Harness
	fragment domain.Fragment
	roots    []string
	capture  harness.CaptureResult
}

func (s planHarnessStub) ID() domain.Harness { return s.id }
func (s planHarnessStub) Layout(harness.CaptureContext) harness.Layout {
	return harness.Layout{ValidationRoots: s.roots}
}
func (s planHarnessStub) Plan(_ context.Context, _ engine.SyncContext) (domain.Fragment, error) {
	return s.fragment, nil
}
func (s planHarnessStub) Render(_ context.Context, _ harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s planHarnessStub) Capture(_ context.Context, _ harness.CaptureContext) (harness.CaptureResult, error) {
	return s.capture, nil
}

func TestCountProfileContent_StableAcrossHarnesses(t *testing.T) {
	t.Parallel()

	// Profile has known source counts: 2 rules, 1 skill, 1 workflow, 1 agent.
	profile := domain.Profile{
		Packs: []domain.Pack{
			{
				Name:      "core",
				Rules:     []domain.Rule{{Name: "anti-slop"}, {Name: "verify"}},
				Skills:    []domain.Skill{{Name: "debugging"}},
				Workflows: []domain.Workflow{{Name: "deploy"}},
				Agents:    []domain.Agent{{Name: "reviewer"}},
			},
		},
		MCPServers: []domain.MCPServer{
			{Name: "atlassian"}, {Name: "bitbucket"}, {Name: "tickets"}, {Name: "cloud"},
		},
	}

	// A merged plan has 1 MCP config file per harness, not 1 per server.
	mergedPlan := domain.Plan{
		MCP: []domain.SettingsAction{
			{Dst: "/project/.claude/settings.json", Harness: domain.HarnessClaudeCode},
		},
	}

	// CountProfileContent should return source counts, stable regardless of
	// harness count or destination directory names.
	profileCounts := CountProfileContent(profile)
	if profileCounts.Rules != 2 {
		t.Errorf("Rules = %d, want 2", profileCounts.Rules)
	}
	if profileCounts.Skills != 1 {
		t.Errorf("Skills = %d, want 1", profileCounts.Skills)
	}
	if profileCounts.Workflows != 1 {
		t.Errorf("Workflows = %d, want 1", profileCounts.Workflows)
	}
	if profileCounts.Agents != 1 {
		t.Errorf("Agents = %d, want 1", profileCounts.Agents)
	}

	// MCP count should reflect unique servers from the profile,
	// not the number of config files in the plan.
	if len(mergedPlan.MCP) == len(profile.MCPServers) {
		t.Fatal("plan.MCP count unexpectedly equals profile.MCPServers — test premise is wrong")
	}
	if profileCounts.MCP != 4 {
		t.Errorf("MCP = %d, want 4", profileCounts.MCP)
	}
}

// TestPlanWithDiffs_PerServerMCPActionsProduceNoOps verifies that per-server
// MCPActions do not generate PlanOps. Per-server tracking exists for ledger
// bookkeeping during apply; the file-level SettingsAction diff is authoritative
// for the TUI. Per-server digest comparison is incompatible with MergeMode
// settings where the three-way merge preserves user additions.
func TestPlanWithDiffs_PerServerMCPActionsProduceNoOps(t *testing.T) {
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

	settings := domain.SettingsAction{
		Dst:        configPath,
		Desired:    []byte("[mcp_servers.jira]\ncommand='uvx'\n"),
		Harness:    domain.HarnessCodex,
		SourcePack: "settings-pack",
	}
	serverContent, err := domain.MCPInventoryBytes(domain.MCPServer{
		Name:      "jira",
		Transport: domain.TransportStdio,
		Command:   []string{"uvx", "jira-mcp"},
	})
	if err != nil {
		t.Fatalf("MCPInventoryBytes: %v", err)
	}
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		configPath: {SourcePack: "settings-pack", Digest: domain.SingleFileDigest(settings.Desired)},
	})

	reg := harness.NewRegistry(planHarnessStub{
		id: "codex",
		fragment: domain.Fragment{
			Settings: []domain.SettingsAction{settings},
			MCPServers: []domain.MCPAction{{
				Name:       "jira",
				ConfigPath: configPath,
				Content:    serverContent,
				SourcePack: "core",
				Harness:    domain.HarnessCodex,
				Embedded:   true,
			}},
			Desired: []string{configPath},
		},
		roots: []string{filepath.Dir(configPath)},
	})

	summary, err := PlanWithDiffs(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			Harnesses:  []domain.Harness{"codex"},
			ProjectDir: projectDir,
			Home:       home,
		},
	}, reg)
	if err != nil {
		t.Fatalf("PlanWithDiffs: %v", err)
	}

	// Per-server MCPActions should not produce PlanOps — the file-level
	// SettingsAction (which is DiffIdentical here) is authoritative.
	if summary.NumMCP != 0 {
		t.Fatalf("NumMCP = %d, want 0", summary.NumMCP)
	}
	if len(summary.Ops) != 0 {
		t.Fatalf("expected 0 ops, got %d", len(summary.Ops))
	}
}

func TestPlanWithDiffs_SkipsCleanPromotedContentUsingSourceDigest(t *testing.T) {
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

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		dstPath: {
			SourcePack: "core",
			Digest:     domain.SingleFileDigest(promotedBytes),
		},
	})

	reg := harness.NewRegistry(planHarnessStub{
		id: "codex",
		fragment: domain.Fragment{
			Writes: []domain.WriteAction{{
				Dst:          dstPath,
				Content:      renderedBytes,
				SourcePack:   "core",
				IsContent:    true,
				SourceDigest: domain.SingleFileDigest(promotedBytes),
			}},
			Desired: []string{dstPath},
		},
		roots: []string{filepath.Dir(dstPath)},
	})

	summary, err := PlanWithDiffs(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			Harnesses:  []domain.Harness{"codex"},
			ProjectDir: projectDir,
			Home:       home,
		},
	}, reg)
	if err != nil {
		t.Fatalf("PlanWithDiffs: %v", err)
	}

	if summary.TotalChanges() != 0 {
		t.Fatalf("TotalChanges = %d, want 0; ops=%+v", summary.TotalChanges(), summary.Ops)
	}
}

func TestPlanWithDiffs_ExpandsDirectoryCopyToFileOps(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	srcDir := filepath.Join(home, "packs", "core", "skills", "deploy")
	dstDir := filepath.Join(projectDir, ".agents", "skills", "deploy")
	for rel, body := range map[string]string{
		"SKILL.md":           "# Deploy\n",
		"notes.md":           "notes\n",
		"agents/openai.yaml": "interface:\n  display_name: Deploy\n",
		"hooks/hooks.json":   "{}\n",
	} {
		path := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var fragment domain.Fragment
	fragment.AddSkillCopies(filepath.Dir(dstDir), "", []domain.Skill{{Name: "deploy", DirPath: srcDir, SourcePack: "core"}})
	reg := harness.NewRegistry(planHarnessStub{
		id:       "codex",
		fragment: fragment,
		roots:    []string{filepath.Dir(dstDir)},
	})

	summary, err := PlanWithDiffs(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			Harnesses:  []domain.Harness{"codex"},
			ProjectDir: projectDir,
			Home:       home,
		},
	}, reg)
	if err != nil {
		t.Fatalf("PlanWithDiffs: %v", err)
	}

	if summary.NumSkills != 4 || summary.NumAgents != 0 || summary.NumHooks != 0 {
		t.Fatalf("content counts = skills:%d agents:%d hooks:%d, want skills:4 agents:0 hooks:0", summary.NumSkills, summary.NumAgents, summary.NumHooks)
	}
	if len(summary.Ops) != 4 {
		t.Fatalf("ops = %d, want 4: %+v", len(summary.Ops), summary.Ops)
	}

	got := map[string]appPlanOpForTest{}
	for _, op := range summary.Ops {
		rel, err := filepath.Rel(dstDir, op.Dst)
		if err != nil {
			t.Fatal(err)
		}
		got[filepath.ToSlash(rel)] = appPlanOpForTest{
			Kind:       op.Kind,
			Dst:        op.Dst,
			Src:        op.Src,
			SourcePack: op.SourcePack,
			Content:    string(op.Content),
			Size:       op.Size,
		}
	}
	for name, wantContent := range map[string]string{
		"SKILL.md":           "# Deploy\n",
		"notes.md":           "notes\n",
		"agents/openai.yaml": "interface:\n  display_name: Deploy\n",
		"hooks/hooks.json":   "{}\n",
	} {
		op, ok := got[name]
		if !ok {
			t.Fatalf("missing op for %s; ops=%+v", name, summary.Ops)
		}
		if op.Kind != PlanOpSkill {
			t.Fatalf("%s kind = %q, want %q", name, op.Kind, PlanOpSkill)
		}
		if op.Dst != filepath.Join(dstDir, name) {
			t.Fatalf("%s dst = %q, want file destination", name, op.Dst)
		}
		if op.Src == srcDir {
			t.Fatalf("%s src still points at directory %q", name, op.Src)
		}
		if op.SourcePack != "core" {
			t.Fatalf("%s SourcePack = %q, want core", name, op.SourcePack)
		}
		if op.Content != wantContent {
			t.Fatalf("%s content = %q, want %q", name, op.Content, wantContent)
		}
		if op.Size != len(wantContent) {
			t.Fatalf("%s size = %d, want %d", name, op.Size, len(wantContent))
		}
	}
}

func TestPlanWithDiffs_RenderedSkillAssetsKeepSkillCategory(t *testing.T) {
	t.Parallel()
	for _, namespaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "namespaced"}[namespaced], func(t *testing.T) {
			home := t.TempDir()
			projectDir := filepath.Join(home, "project")
			srcDir := filepath.Join(home, "packs", "core", "skills", "agents")
			skillsDir := filepath.Join(projectDir, ".agents", "skills")
			for rel, body := range map[string]string{
				"SKILL.md":           "---\nname: agents\ndescription: Deploy\n---\n# Deploy\n",
				"agents/openai.yaml": "interface:\n  display_name: Deploy\n",
				"hooks/hooks.json":   "{}\n",
			} {
				writeFile(t, filepath.Join(srcDir, rel), body)
			}
			var fragment domain.Fragment
			harness.AddRenderedSkillCopies(&fragment, skillsDir, "", namespaced, []domain.Skill{{
				Name: "agents", DirPath: srcDir, SourcePack: "core",
				Raw:    []byte("---\nname: agents\ndescription: Deploy\n---\n# Deploy\n"),
				Assets: []string{"agents/openai.yaml", "hooks/hooks.json"},
			}})
			fragment.AddAgentWrites(projectDir, "agents", []domain.Agent{{Name: "reviewer", Raw: []byte("# Reviewer\n"), SourcePack: "core"}})
			reg := harness.NewRegistry(planHarnessStub{
				id: "codex", fragment: fragment, roots: []string{projectDir},
			})
			summary, err := PlanWithDiffs(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
				TargetSpec: TargetSpec{
					Scope: domain.ScopeProject, Harnesses: []domain.Harness{domain.HarnessCodex},
					ProjectDir: projectDir, Home: home,
				},
			}, reg)
			if err != nil {
				t.Fatal(err)
			}
			if summary.NumSkills != 3 || summary.NumAgents != 1 || summary.NumHooks != 0 || len(summary.Ops) != 4 {
				t.Fatalf("wrong skill asset classification: %+v", summary)
			}
		})
	}
}

type appPlanOpForTest struct {
	Kind       PlanOpKind
	Dst        string
	Src        string
	SourcePack string
	Content    string
	Size       int
}
