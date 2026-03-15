package app

import (
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
func (s planHarnessStub) Plan(engine.SyncContext) (domain.Fragment, error) {
	return s.fragment, nil
}
func (s planHarnessStub) Render(harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s planHarnessStub) ManagedRoots(domain.Scope, string, string) []string    { return s.roots }
func (s planHarnessStub) SettingsPaths(domain.Scope, string, string) []string   { return nil }
func (s planHarnessStub) StrictExtraDirs(domain.Scope, string, string) []string { return nil }
func (s planHarnessStub) PackRelativePaths() []string                           { return nil }
func (s planHarnessStub) StripManagedSettings(b []byte, _ string) ([]byte, error) {
	return b, nil
}
func (s planHarnessStub) Capture(harness.CaptureContext) (harness.CaptureResult, error) {
	return s.capture, nil
}
func (s planHarnessStub) CleanActions(domain.Scope, string, string) []harness.CleanAction { return nil }

func TestPlanWithDiffs_ClassifiesMCPServersFirstClass(t *testing.T) {
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
	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex")
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

	summary, err := PlanWithDiffs(domain.Profile{}, SyncRequest{
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

	if summary.NumMCP != 1 {
		t.Fatalf("NumMCP = %d, want 1", summary.NumMCP)
	}
	if len(summary.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(summary.Ops))
	}
	if summary.Ops[0].Kind != PlanOpMCP {
		t.Fatalf("Kind = %q, want %q", summary.Ops[0].Kind, PlanOpMCP)
	}
}

func TestPlanWithDiffs_ClassifiesTrackedMCPConflictFromLiveConfig(t *testing.T) {
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
		Name:      "jira",
		Transport: domain.TransportStdio,
		Command:   []string{"uvx", "jira-mcp"},
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

	summary, err := PlanWithDiffs(domain.Profile{}, SyncRequest{
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

	if len(summary.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(summary.Ops))
	}
	if summary.Ops[0].Kind != PlanOpMCP {
		t.Fatalf("Kind = %q, want %q", summary.Ops[0].Kind, PlanOpMCP)
	}
	if summary.Ops[0].DiffKind != domain.DiffConflict {
		t.Fatalf("DiffKind = %q, want %q", summary.Ops[0].DiffKind, domain.DiffConflict)
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

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex")
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

	summary, err := PlanWithDiffs(domain.Profile{}, SyncRequest{
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
