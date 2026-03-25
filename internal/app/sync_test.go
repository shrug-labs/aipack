package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

type syncStubHarness struct {
	id       domain.Harness
	fragment domain.Fragment
	roots    []string
}

func (s syncStubHarness) ID() domain.Harness { return s.id }
func (s syncStubHarness) Layout(domain.Scope, string, string) harness.Layout {
	return harness.Layout{ValidationRoots: s.roots}
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

func TestRunSync_AggregatesCountsAcrossHarnesses(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	claudeHarness := syncStubHarness{
		id: domain.HarnessClaudeCode,
		fragment: domain.Fragment{
			Copies: []domain.CopyAction{{
				Src:  filepath.Join(projectDir, "pack", "rules", "alpha.md"),
				Dst:  filepath.Join(projectDir, ".claude", "rules", "alpha.md"),
				Kind: domain.CopyKindFile,
			}},
		},
	}
	codexHarness := syncStubHarness{
		id: domain.HarnessCodex,
		fragment: domain.Fragment{
			Copies: []domain.CopyAction{{
				Src:  filepath.Join(projectDir, "pack", "skills", "beta"),
				Dst:  filepath.Join(projectDir, ".agents", "skills", "beta"),
				Kind: domain.CopyKindDir,
			}},
			Settings: []domain.SettingsAction{{
				Dst:     filepath.Join(projectDir, ".codex", "config.toml"),
				Desired: []byte("[mcp_servers]\n"),
				Harness: domain.HarnessCodex,
			}},
		},
	}
	reg := harness.NewRegistry(claudeHarness, codexHarness)

	var stdout, stderr bytes.Buffer
	result, _, err := RunSync(context.Background(), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       home,
		},
		DryRun: true,
	}, reg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	if got := len(result.Plan.Copies); got != 2 {
		t.Fatalf("copies = %d, want 2 (1 rule + 1 skill)", got)
	}
	if got := len(result.Plan.Settings); got != 1 {
		t.Fatalf("settings = %d, want 1", got)
	}
}

func TestRunSync_DryRunDoesNotMigrateLedgers(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()
	managedRoot := filepath.Join(projectDir, ".claude")
	oldLedgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")

	oldLedger := domain.NewLedger()
	oldLedger.Managed[filepath.Join(managedRoot, "rules", "sample.md")] = domain.Entry{
		Digest:     "abc123",
		SourcePack: "demo",
	}
	if err := engine.SaveLedger(oldLedgerPath, oldLedger, false); err != nil {
		t.Fatalf("SaveLedger(old): %v", err)
	}

	reg := harness.NewRegistry(syncStubHarness{
		id:    domain.HarnessClaudeCode,
		roots: []string{managedRoot},
	})

	var stdout, stderr bytes.Buffer
	_, _, err := RunSync(context.Background(), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
			Home:       home,
		},
		DryRun: true,
	}, reg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	newLedgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
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
	codexDst := filepath.Join(projectDir, ".agents", "skills", "demo-agent", "SKILL.md")
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

	writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
		claudeDst: {SourcePack: "core", Digest: domain.SingleFileDigest([]byte("alpha"))},
	})
	writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessCodex), map[string]domain.Entry{
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
			roots: []string{filepath.Join(projectDir, ".agents")},
		},
	)

	var stdout, stderr bytes.Buffer
	_, _, err := RunSync(context.Background(), domain.Profile{}, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       home,
		},
		DryRun: true,
	}, reg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	if got := stdout.String(); got != "plan: 0 changes, 2 identical\n" {
		t.Fatalf("stdout = %q, want %q", got, "plan: 0 changes, 2 identical\n")
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
			name:     "new skill reports copy",
			setupDst: func(t *testing.T) { /* no destination */ },
			wantOut:  "copy: " + skillDst + "\nplan: 1 changes, 0 identical\n",
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
				writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
					filepath.Join(skillDst, "SKILL.md"): {SourcePack: "pack", Digest: domain.SingleFileDigest([]byte("v2 content"))},
				})
			},
			wantOut: "plan: 0 changes, 1 identical\n",
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
				writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
					filepath.Join(skillDst, "SKILL.md"): {SourcePack: "pack", Digest: domain.SingleFileDigest([]byte("v1 content"))},
				})
			},
			wantOut: "update: " + skillDst + "\nplan: 1 changes, 0 identical\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean destination and ledger between subtests.
			os.RemoveAll(skillDst)
			lgPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
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

			reg := harness.NewRegistry(syncStubHarness{
				id:    domain.HarnessClaudeCode,
				roots: []string{filepath.Join(projectDir, ".claude")},
			})

			var buf bytes.Buffer
			printDryRun(plan, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
					Home:       home,
				},
			}, reg, &buf)

			if got := buf.String(); got != tt.wantOut {
				t.Errorf("output = %q, want %q", got, tt.wantOut)
			}
		})
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

	registryPath := filepath.Join(packRoot, "registry.yaml")
	registryYAML := "schema_version: 1\npacks:\n  embedded-pack:\n    repo: https://example.com/embedded.git\n    description: embedded pack\n"
	if err := os.WriteFile(registryPath, []byte(registryYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	warnings := processEmbeddedRegistries(domain.Profile{
		Packs: []domain.Pack{{
			Name:       "demo",
			Root:       packRoot,
			Registries: []string{"registry.yaml"},
		}},
	}, home, &stderr)
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
