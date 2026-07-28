package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/index"
)

type syncStubHarness struct {
	id         domain.Harness
	fragment   domain.Fragment
	roots      []string
	staleRoots []string
}

func (s syncStubHarness) ID() domain.Harness { return s.id }
func (s syncStubHarness) Layout(domain.Scope, string, string) harness.Layout {
	return harness.Layout{ValidationRoots: s.roots, StaleRoots: s.staleRoots}
}
func (s syncStubHarness) Plan(_ context.Context, _ engine.SyncContext) (domain.Fragment, error) {
	return s.fragment, nil
}
func (s syncStubHarness) Render(_ context.Context, _ harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s syncStubHarness) Capture(_ context.Context, _ harness.CaptureContext) (harness.CaptureResult, error) {
	return harness.CaptureResult{}, nil
}

func TestRunSync_RejectsMultipleHarnesses(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	reg := harness.NewRegistry(
		syncStubHarness{id: domain.HarnessClaudeCode},
		syncStubHarness{id: domain.HarnessCodex},
	)

	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       home,
		},
		DryRun: true,
	}, reg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "one harness per run") {
		t.Fatalf("RunSync error = %v, want one-harness rejection", err)
	}
}

func TestRunSyncEach_RunsEachHarnessIndependently(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	reg := harness.NewRegistry(
		syncStubHarness{id: domain.HarnessClaudeCode},
		syncStubHarness{id: domain.HarnessCodex},
	)

	results, _, err := RunSyncEach(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       home,
		},
		DryRun: true,
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSyncEach error = %v, want nil", err)
	}
	if len(results) != 2 {
		t.Fatalf("RunSyncEach returned %d results, want 2", len(results))
	}
	if results[0].Harness != domain.HarnessClaudeCode || results[1].Harness != domain.HarnessCodex {
		t.Fatalf("results harness order = [%s, %s], want [claudecode, codex]", results[0].Harness, results[1].Harness)
	}
}

func TestRunSyncEach_RejectsNoHarness(t *testing.T) {
	t.Parallel()

	_, _, err := RunSyncEach(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{Scope: domain.ScopeProject, ProjectDir: t.TempDir(), Home: t.TempDir()},
		DryRun:     true,
	}, harness.NewRegistry(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one harness") {
		t.Fatalf("RunSyncEach error = %v, want at-least-one-harness rejection", err)
	}
}

func TestPlanWithDiffsEach_AggregatesAcrossHarnesses(t *testing.T) {
	t.Parallel()

	reg := harness.NewRegistry(
		syncStubHarness{id: domain.HarnessClaudeCode},
		syncStubHarness{id: domain.HarnessCodex},
	)

	summary, err := PlanWithDiffsEach(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: t.TempDir(),
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       t.TempDir(),
		},
	}, reg)
	if err != nil {
		t.Fatalf("PlanWithDiffsEach error = %v, want nil", err)
	}
	// Empty profile means no pending changes for either harness.
	if summary.TotalChanges() != 0 {
		t.Fatalf("TotalChanges = %d, want 0", summary.TotalChanges())
	}

	if _, err := PlanWithDiffsEach(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{Scope: domain.ScopeProject, ProjectDir: t.TempDir(), Home: t.TempDir()},
	}, reg); err == nil || !strings.Contains(err.Error(), "at least one harness") {
		t.Fatalf("PlanWithDiffsEach zero-harness error = %v, want rejection", err)
	}
}

func TestRunSync_DryRunDoesNotMigrateLedgers(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()
	managedRoot := filepath.Join(projectDir, ".claude")
	oldLedgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")

	eng := engine.New(nil, nil)
	oldLedger := domain.NewLedger()
	oldLedger.Managed[filepath.Join(managedRoot, "rules", "sample.md")] = domain.Entry{
		Digest:     "abc123",
		SourcePack: "demo",
	}
	if err := eng.SaveLedger(oldLedgerPath, oldLedger, false); err != nil {
		t.Fatalf("SaveLedger(old): %v", err)
	}

	reg := harness.NewRegistry(syncStubHarness{
		id:    domain.HarnessClaudeCode,
		roots: []string{managedRoot},
	})

	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
			Home:       home,
		},
		DryRun: true,
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	newLedgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	if _, statErr := os.Stat(newLedgerPath); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run should not create migrated ledger %s; stat err=%v", newLedgerPath, statErr)
	}

	info, statErr := os.Stat(oldLedgerPath)
	if statErr != nil {
		t.Fatalf("old ledger missing after dry-run: %v", statErr)
	}
	if info.ModTime().After(time.Now().Add(1 * time.Second)) {
		t.Fatalf("unexpected old ledger timestamp after dry-run: %v", info.ModTime())
	}
}

func TestRunSync_DryRunUsesPerHarnessLedgerForClassification(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	claudeDst := filepath.Join(projectDir, ".claude", "rules", "alpha.md")
	codexDst := filepath.Join(projectDir, ".codex", "skills", "demo-agent", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(claudeDst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexDst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeDst, []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexDst, []byte("demo-agent"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
		claudeDst: {SourcePack: "core", Digest: domain.SingleFileDigest([]byte("alpha"))},
	})
	writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex), map[string]domain.Entry{
		codexDst: {SourcePack: "core", Digest: domain.SingleFileDigest([]byte("demo-agent"))},
	})

	reg := harness.NewRegistry(
		syncStubHarness{
			id: domain.HarnessClaudeCode,
			fragment: domain.Fragment{
				Writes: []domain.WriteAction{{
					Dst:        claudeDst,
					Content:    []byte("alpha"),
					SourcePack: "core",
				}},
				Desired: []string{claudeDst},
			},
			roots: []string{filepath.Join(projectDir, ".claude")},
		},
		syncStubHarness{
			id: domain.HarnessCodex,
			fragment: domain.Fragment{
				Writes: []domain.WriteAction{{
					Dst:        codexDst,
					Content:    []byte("demo-agent"),
					SourcePack: "core",
				}},
				Desired: []string{codexDst},
			},
			roots: []string{filepath.Join(projectDir, ".codex")},
		},
	)

	var out bytes.Buffer
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
		},
		DryRun: true,
	}, reg, &out, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	if got := out.String(); got != "plan: 0 file ops from 0 content, 1 identical\n" {
		t.Fatalf("output = %q, want %q", got, "plan: 0 file ops from 0 content, 1 identical\n")
	}
}

func TestRunSync_ApplyOutputUsesHarnessFullDestinations(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()
	configDir := t.TempDir()
	dst := filepath.Join(projectDir, ".codex", "skills", "deploy", "SKILL.md")

	reg := harness.NewRegistry(syncStubHarness{
		id: domain.HarnessCodex,
		fragment: domain.Fragment{
			Writes: []domain.WriteAction{{
				Dst:        dst,
				Content:    []byte("skill body"),
				SourcePack: "core",
			}},
			Desired: []string{dst},
		},
		roots: []string{filepath.Join(projectDir, ".codex")},
	})

	var stderr bytes.Buffer
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir:  configDir,
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
		},
	}, reg, nil, &stderr)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	want := "  create: [codex] " + domain.DisplayPath(dst) + "\n"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
	}
}

func TestRunSync_CodexLegacyStaleRootOwnership(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		clineOwns       bool
		activeHarnesses []domain.Harness
		wantLegacy      bool
	}{
		{
			name:            "removes orphaned legacy codex skill",
			clineOwns:       false,
			activeHarnesses: []domain.Harness{domain.HarnessCodex},
		},
		{
			name:            "removes inactive cline current skill",
			clineOwns:       true,
			activeHarnesses: []domain.Harness{domain.HarnessCodex},
		},
		{
			name:            "preserves active cline current skill",
			clineOwns:       true,
			activeHarnesses: []domain.Harness{domain.HarnessCodex, domain.HarnessCline},
			wantLegacy:      true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			projectDir := t.TempDir()
			home := t.TempDir()

			legacyDst := filepath.Join(projectDir, ".agents", "skills", "deploy", "SKILL.md")
			codexDst := filepath.Join(projectDir, ".codex", "skills", "deploy", "SKILL.md")
			legacyContent := []byte("legacy managed skill")
			codexContent := []byte("codex managed skill")

			if err := os.MkdirAll(filepath.Dir(legacyDst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacyDst, legacyContent, 0o644); err != nil {
				t.Fatal(err)
			}
			writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex), map[string]domain.Entry{
				legacyDst: {SourcePack: "core", Digest: domain.SingleFileDigest(legacyContent)},
			})
			if tc.clineOwns {
				writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCline), map[string]domain.Entry{
					legacyDst: {SourcePack: "core", Digest: domain.SingleFileDigest(legacyContent)},
				})
			}

			reg := harness.NewRegistry(
				syncStubHarness{
					id:    domain.HarnessCline,
					roots: []string{filepath.Join(projectDir, ".agents", "skills")},
				},
				syncStubHarness{
					id: domain.HarnessCodex,
					fragment: domain.Fragment{
						Writes: []domain.WriteAction{{
							Dst:        codexDst,
							Content:    codexContent,
							SourcePack: "core",
						}},
						Desired: []string{codexDst},
					},
					roots:      []string{filepath.Join(projectDir, ".codex")},
					staleRoots: []string{filepath.Join(projectDir, ".agents", "skills")},
				},
			)

			_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:           domain.ScopeProject,
					ProjectDir:      projectDir,
					Harnesses:       []domain.Harness{domain.HarnessCodex},
					ActiveHarnesses: tc.activeHarnesses,
					Home:            home,
				},
			}, reg, nil, nil)
			if err != nil {
				t.Fatalf("RunSync: %v", err)
			}

			if tc.wantLegacy {
				got, err := os.ReadFile(legacyDst)
				if err != nil {
					t.Fatalf("cline skill should remain: %v", err)
				}
				if string(got) != string(legacyContent) {
					t.Fatalf("cline skill content = %q, want %q", got, legacyContent)
				}
			} else if _, err := os.Stat(legacyDst); !os.IsNotExist(err) {
				t.Fatalf("legacy codex skill path should be removed; stat err=%v", err)
			}

			got, err := os.ReadFile(codexDst)
			if err != nil {
				t.Fatalf("codex skill should be written: %v", err)
			}
			if string(got) != string(codexContent) {
				t.Fatalf("codex skill content = %q, want %q", got, codexContent)
			}
			codexLedger, _, err := engine.New(nil, nil).LoadLedger(testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex))
			if err != nil {
				t.Fatalf("LoadLedger: %v", err)
			}
			if _, ok := codexLedger.Managed[filepath.Clean(legacyDst)]; ok {
				t.Fatalf("codex stale ledger claim for legacy path should be pruned")
			}
			if tc.clineOwns && !tc.wantLegacy {
				clineLedger, _, err := engine.New(nil, nil).LoadLedger(testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCline))
				if err != nil {
					t.Fatalf("LoadLedger cline: %v", err)
				}
				if _, ok := clineLedger.Managed[filepath.Clean(legacyDst)]; ok {
					t.Fatalf("inactive cline stale ledger claim should be pruned")
				}
			}
		})
	}
}

func TestRunSync_WritesLedgerToExplicitConfigDir(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()
	configDir := t.TempDir()
	dst := filepath.Join(projectDir, ".codex", "config.toml")

	reg := harness.NewRegistry(syncStubHarness{
		id: domain.HarnessCodex,
		fragment: domain.Fragment{
			Settings: []domain.SettingsAction{{
				Dst:     dst,
				Desired: []byte("[mcp_servers]\n"),
				Harness: domain.HarnessCodex,
			}},
			Desired: []string{dst},
		},
		roots: []string{filepath.Join(projectDir, ".codex")},
	})

	// TestRunSync_WritesLedgerToExplicitConfigDir doesn't assert on stdout
	// or stderr, only on filesystem state, so drop the writers entirely.
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir:  configDir,
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
		},
		Yes: true,
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	gotLedger := filepath.Join(configDir, "ledger", engine.EncodeProjectPath(projectDir), "codex.json")
	if _, err := os.Stat(gotLedger); err != nil {
		t.Fatalf("expected ledger in config dir: %v", err)
	}

	defaultCfgDir, derr := config.DefaultConfigDir(home)
	if derr != nil {
		t.Fatalf("DefaultConfigDir: %v", derr)
	}
	defaultLedger := filepath.Join(defaultCfgDir, "ledger", engine.EncodeProjectPath(projectDir), "codex.json")
	if _, err := os.Stat(defaultLedger); !os.IsNotExist(err) {
		t.Fatalf("unexpected ledger outside config dir: stat err=%v", err)
	}
}

func TestRunSync_CodexOnlySyncRetiresInactiveClineLegacyCombinedLedgerFiles(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()
	eng := engine.New(nil, nil)

	legacyDst := filepath.Join(projectDir, ".agents", "skills", "deploy", "SKILL.md")
	legacyContent := []byte("cline-owned legacy skill")
	codexDst := filepath.Join(projectDir, ".codex", "skills", "deploy", "SKILL.md")
	codexContent := []byte("codex managed skill")

	if err := os.MkdirAll(filepath.Dir(legacyDst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyDst, legacyContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// A legacy project-local combined ledger (pre per-harness ledgers) tracking a
	// skill under .agents/skills — the directory Cline writes to now. No
	// per-harness ledgers exist yet, mirroring a fresh upgrade.
	combined := domain.NewLedger()
	combined.Managed[legacyDst] = domain.Entry{SourcePack: "core", Digest: domain.SingleFileDigest(legacyContent)}
	oldLedger := filepath.Join(projectDir, ".aipack", "ledger.json")
	if err := os.MkdirAll(filepath.Dir(oldLedger), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveLedger(oldLedger, combined, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	reg := harness.NewRegistry(
		syncStubHarness{
			id:    domain.HarnessCline,
			roots: []string{filepath.Join(projectDir, ".agents", "skills")},
		},
		syncStubHarness{
			id: domain.HarnessCodex,
			fragment: domain.Fragment{
				Writes: []domain.WriteAction{{
					Dst:        codexDst,
					Content:    codexContent,
					SourcePack: "core",
				}},
				Desired: []string{codexDst},
			},
			roots:      []string{filepath.Join(projectDir, ".codex")},
			staleRoots: []string{filepath.Join(projectDir, ".agents", "skills")},
		},
	)

	// Sync Codex only. Codex moved out of .agents/skills to .codex/skills, but
	// Codex still discovers .agents/skills at runtime. Because Cline is not an
	// active target, the migrated Cline ledger entry must be retired so Codex
	// does not see duplicate skills.
	_, _, err := RunSync(context.Background(), eng, domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:           domain.ScopeProject,
			ProjectDir:      projectDir,
			Harnesses:       []domain.Harness{domain.HarnessCodex},
			ActiveHarnesses: []domain.Harness{domain.HarnessCodex},
			Home:            home,
		},
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	if _, err := os.Stat(legacyDst); !os.IsNotExist(err) {
		t.Fatalf("inactive cline legacy skill should be removed by a codex-only sync; stat err=%v", err)
	}

	// The legacy entry migrates to the harness that lives there now (Cline),
	// then is pruned because Cline is inactive for this sync.
	clineLedger, _, err := eng.LoadLedger(testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCline))
	if err != nil {
		t.Fatalf("LoadLedger cline: %v", err)
	}
	if _, ok := clineLedger.Managed[filepath.Clean(legacyDst)]; ok {
		t.Fatalf("inactive cline legacy .agents/skills entry should be pruned")
	}
	codexLedger, _, err := eng.LoadLedger(testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex))
	if err != nil {
		t.Fatalf("LoadLedger codex: %v", err)
	}
	if _, ok := codexLedger.Managed[filepath.Clean(legacyDst)]; ok {
		t.Fatalf("codex must not claim a file under cline's live .agents/skills root")
	}
}

func TestPrintDryRun_ClassifiesSkillCopies(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	// Set up a skill source directory with one file.
	skillSrc := filepath.Join(projectDir, "pack", "skills", "my-skill")
	skillDst := filepath.Join(projectDir, ".claude", "skills", "my-skill")
	if err := os.MkdirAll(skillSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillSrc, "SKILL.md"), []byte("v2 content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		setupDst   func(t *testing.T)
		setupLedge func(t *testing.T)
		wantOut    string
	}{
		{
			name:     "new skill reports create",
			setupDst: func(t *testing.T) { /* no destination */ },
			wantOut:  "create: [claudecode] " + domain.DisplayPath(skillDst) + "\nplan: 1 file ops from 1 skill, 0 identical\n",
		},
		{
			name: "identical skill is silent",
			setupDst: func(t *testing.T) {
				t.Helper()
				if err := os.MkdirAll(skillDst, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillDst, "SKILL.md"), []byte("v2 content"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			setupLedge: func(t *testing.T) {
				t.Helper()
				writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
					filepath.Join(skillDst, "SKILL.md"): {SourcePack: "pack", Digest: domain.SingleFileDigest([]byte("v2 content"))},
				})
			},
			wantOut: "plan: 0 file ops from 1 skill, 1 identical\n",
		},
		{
			name: "changed skill reports update",
			setupDst: func(t *testing.T) {
				t.Helper()
				if err := os.MkdirAll(skillDst, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillDst, "SKILL.md"), []byte("v1 content"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			setupLedge: func(t *testing.T) {
				t.Helper()
				writeLedger(t, testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
					filepath.Join(skillDst, "SKILL.md"): {SourcePack: "pack", Digest: domain.SingleFileDigest([]byte("v1 content"))},
				})
			},
			wantOut: "update: [claudecode] " + domain.DisplayPath(skillDst) + "\nplan: 1 file ops from 1 skill, 0 identical\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean destination and ledger between subtests.
			os.RemoveAll(skillDst)
			lgPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
			os.Remove(lgPath)

			tt.setupDst(t)
			if tt.setupLedge != nil {
				tt.setupLedge(t)
			}

			plan := domain.Plan{
				Copies: []domain.CopyAction{{
					Src:        skillSrc,
					Dst:        skillDst,
					Kind:       domain.CopyKindDir,
					SourcePack: "pack",
				}},
			}

			var buf bytes.Buffer
			printDryRun(engine.New(nil, nil), plan, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
					Home:       home,
				},
			}, ContentCounts{Skills: 1}, &buf)

			if got := buf.String(); got != tt.wantOut {
				t.Errorf("output = %q, want %q", got, tt.wantOut)
			}
		})
	}
}

func TestPrintDryRun_CountsSettingsMergeAsFileOp(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()
	settingsDst := filepath.Join(projectDir, ".claude", "settings.local.json")

	// A pending settings merge against a non-existent file is a real change.
	// The headline must count it, not print a misleading "0 file ops".
	plan := domain.Plan{
		Settings: []domain.SettingsAction{{
			Dst:       settingsDst,
			Desired:   []byte(`{"permissions":{"allow":["mcp__x__y"]}}`),
			Harness:   domain.HarnessClaudeCode,
			MergeMode: true,
		}},
	}
	var buf bytes.Buffer
	printDryRun(engine.New(nil, nil), plan, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
			Home:       home,
		},
	}, ContentCounts{}, &buf)

	want := "merge: [claudecode] " + domain.DisplayPath(settingsDst) + "\nplan: 1 file ops from 0 content, 0 identical\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestProcessEmbeddedRegistries_MergesIntoRegistryCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configDir, _ := config.DefaultConfigDir(home)
	packRoot := filepath.Join(home, "packs", "demo")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), config.SyncConfig{
		SchemaVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}

	regDir := filepath.Join(packRoot, "registries")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registryYAML := "schema_version: 1\npacks:\n  embedded-pack:\n    repo: https://example.com/embedded.git\n    description: embedded pack\n"
	if err := os.WriteFile(filepath.Join(regDir, "default.yaml"), []byte(registryYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	warnings := processEmbeddedRegistries(domain.Profile{
		Packs: []domain.Pack{{
			Name:       "demo",
			Root:       packRoot,
			Registries: []string{"default"},
		}},
	}, configDir, nil)
	if len(warnings) > 0 {
		t.Fatalf("processEmbeddedRegistries warnings: %v", warnings)
	}

	merged, err := config.LoadMergedRegistry(configDir)
	if err != nil {
		t.Fatalf("LoadMergedRegistry: %v", err)
	}
	if _, ok := merged.Packs["embedded-pack"]; !ok {
		t.Fatalf("expected embedded pack to be installable from merged registry, got %v", merged.Packs)
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	foundSource := false
	for _, src := range syncCfg.RegistrySources {
		if src.Name == embeddedRegistrySourceName {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Fatalf("expected embedded registry source %q in sync-config", embeddedRegistrySourceName)
	}
}

// TestUpdateIndex_QuietPackIndexesFromManifest verifies that quiet packs with
// empty resolved vectors still get their content indexed from the manifest.
// This is the discovery use case: search should show available content even
// when nothing is active in the profile.
func TestUpdateIndex_QuietPackIndexesFromManifest(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir, err := config.DefaultConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a pack directory with real content on disk.
	packRoot := filepath.Join(home, "packs", "quiet-catalog")
	os.MkdirAll(filepath.Join(packRoot, "skills", "triage"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "skills", "triage", "SKILL.md"),
		[]byte("---\nname: triage\ndescription: Triage incoming incidents\n---\nTriage body content\n"), 0o644)
	os.MkdirAll(filepath.Join(packRoot, "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: Deploy services\n---\nDeploy body content\n"), 0o644)
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "style.md"),
		[]byte("---\nname: style\ndescription: Code style guide\n---\nStyle body\n"), 0o644)

	// Write a manifest declaring the content.
	config.SavePackManifest(filepath.Join(packRoot, "pack.json"), config.PackManifest{
		SchemaVersion: 1,
		Name:          "quiet-catalog",
		Version:       "1.0.0",
		Root:          ".",
		Skills:        []string{"triage", "deploy"},
		Rules:         []string{"style"},
	})

	// Simulate quiet pack resolution: the domain.Pack has content on disk
	// but empty resolved vectors (quiet + no include = nothing active).
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "quiet-catalog",
			Version: "1.0.0",
			Root:    packRoot,
			// Skills, Rules, Agents, Workflows all nil — quiet resolution
		}},
	}

	if err := updateIndex(profile, configDir); err != nil {
		t.Fatalf("updateIndex: %v", err)
	}

	// Search should find the manifest content even though nothing is active.
	dbPath := filepath.Join(configDir, "index.db")
	db, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer db.Close()

	results, err := db.Search("", index.SearchFilters{Pack: "quiet-catalog"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 resources (2 skills + 1 rule), got %d: %v", len(results), results)
	}

	// Verify specific resources are searchable.
	results, err = db.Search("triage", index.SearchFilters{Pack: "quiet-catalog"})
	if err != nil {
		t.Fatalf("Search triage: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'triage', got %d", len(results))
	}
	if results[0].Name != "triage" || results[0].Kind != "skill" {
		t.Fatalf("expected skill 'triage', got %+v", results[0])
	}
}

func TestUpdateIndex_IndexesMCPServersFromManifest(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packRoot := filepath.Join(t.TempDir(), "tool-catalog")
	writeFile(t, filepath.Join(packRoot, "mcp", "jira.json"), `{
  "name": "jira",
  "transport": "stdio",
  "command": ["jira-mcp"],
  "available_tools": ["search_work_items"],
  "notes": "Search Jira work items"
}`)
	if err := config.SavePackManifest(filepath.Join(packRoot, "pack.json"), config.PackManifest{
		SchemaVersion: 2,
		Name:          "tool-catalog",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"jira"},
	}); err != nil {
		t.Fatal(err)
	}

	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "tool-catalog",
			Version: "1.0.0",
			Root:    packRoot,
		}},
	}
	if err := updateIndex(profile, configDir); err != nil {
		t.Fatalf("updateIndex: %v", err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("search_work_items", index.SearchFilters{
		Pack:   "tool-catalog",
		Kind:   "mcp",
		Status: "installed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "jira" {
		t.Fatalf("installed MCP search results = %+v, want jira", results)
	}
}

func TestIndexInstalledPack_IndexesStructuredManifestResources(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packRoot := t.TempDir()
	writeFile(t, filepath.Join(packRoot, "plugins", "linear.json"), `{
  "source": "github:linear/linear-codex-plugin"
}`)
	writeFile(t, filepath.Join(packRoot, "mcp", "jira.json"), `{
  "name": "jira",
  "transport": "stdio",
  "command": ["jira-mcp"],
  "available_tools": ["get_issue"]
}`)
	if err := config.SavePackManifest(filepath.Join(packRoot, "pack.json"), config.PackManifest{
		SchemaVersion: 2,
		Name:          "structured-tools",
		Version:       "1.0.0",
		Root:          ".",
		Plugins:       []string{"linear"},
		MCP:           []string{"jira"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := indexInstalledPack(configDir, "structured-tools", packRoot); err != nil {
		t.Fatalf("indexInstalledPack: %v", err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for kind, name := range map[string]string{"plugin": "linear", "mcp": "jira"} {
		results, err := db.Search("", index.SearchFilters{
			Pack:   "structured-tools",
			Kind:   kind,
			Status: "installed",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Name != name {
			t.Fatalf("installed %s search results = %+v, want %s", kind, results, name)
		}
	}
}

// TestUpdateIndex_PartialIncludeStillIndexesRest verifies that when a quiet
// pack has some content resolved (via include) the remaining manifest items
// are still indexed for discovery. Regression: the == 0 guard only indexed
// from the manifest when the resolved pack had NO items of a type.
func TestUpdateIndex_PartialIncludeStillIndexesRest(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir, err := config.DefaultConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	packRoot := filepath.Join(home, "packs", "partial")
	os.MkdirAll(filepath.Join(packRoot, "skills", "active-skill"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "skills", "active-skill", "SKILL.md"),
		[]byte("---\nname: active-skill\ndescription: The active one\n---\nActive body\n"), 0o644)
	os.MkdirAll(filepath.Join(packRoot, "skills", "dormant-skill"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "skills", "dormant-skill", "SKILL.md"),
		[]byte("---\nname: dormant-skill\ndescription: Not included but discoverable\n---\nDormant body\n"), 0o644)
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "excluded-rule.md"),
		[]byte("---\nname: excluded-rule\ndescription: Excluded but discoverable\n---\nRule body\n"), 0o644)

	config.SavePackManifest(filepath.Join(packRoot, "pack.json"), config.PackManifest{
		SchemaVersion: 1,
		Name:          "partial",
		Version:       "1.0.0",
		Root:          ".",
		Skills:        []string{"active-skill", "dormant-skill"},
		Rules:         []string{"excluded-rule"},
	})

	// Simulate partial resolution: one skill resolved, but the other skill
	// and the rule were excluded (quiet pack with include: [active-skill]).
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "partial",
			Version: "1.0.0",
			Root:    packRoot,
			Skills: []domain.Skill{{
				Name: "active-skill",
				Frontmatter: domain.SkillFrontmatter{
					Description: "The active one",
				},
				DirPath: filepath.Join(packRoot, "skills", "active-skill"),
				Body:    []byte("Active body\n"),
			}},
			// Rules empty — excluded by quiet resolution
		}},
	}

	if err := updateIndex(profile, configDir); err != nil {
		t.Fatalf("updateIndex: %v", err)
	}

	dbPath := filepath.Join(configDir, "index.db")
	db, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer db.Close()

	// All 3 resources should be indexed: 1 resolved + 1 manifest skill + 1 manifest rule.
	results, err := db.Search("", index.SearchFilters{Pack: "partial"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		names := make([]string, len(results))
		for i, r := range results {
			names[i] = r.Kind + ":" + r.Name
		}
		t.Fatalf("expected 3 resources (1 resolved skill + 1 manifest skill + 1 manifest rule), got %d: %v", len(results), names)
	}

	// The dormant skill should be discoverable.
	results, err = db.Search("dormant", index.SearchFilters{Pack: "partial"})
	if err != nil {
		t.Fatalf("Search dormant: %v", err)
	}
	if len(results) != 1 || results[0].Name != "dormant-skill" {
		t.Fatalf("expected dormant-skill, got %v", results)
	}
}
