package app

import (
	"bytes"
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
func (s syncStubHarness) Plan(engine.SyncContext) (domain.Fragment, error) {
	return s.fragment, nil
}
func (s syncStubHarness) Render(harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s syncStubHarness) ManagedRoots(domain.Scope, string, string) []string      { return s.roots }
func (s syncStubHarness) SettingsPaths(domain.Scope, string, string) []string     { return nil }
func (s syncStubHarness) StrictExtraDirs(domain.Scope, string, string) []string   { return nil }
func (s syncStubHarness) PackRelativePaths() []string                             { return nil }
func (s syncStubHarness) StripManagedSettings(b []byte, _ string) ([]byte, error) { return b, nil }
func (s syncStubHarness) Capture(harness.CaptureContext) (harness.CaptureResult, error) {
	return harness.CaptureResult{}, nil
}
func (s syncStubHarness) CleanActions(domain.Scope, string, string) []harness.CleanAction {
	return nil
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
	result, err := RunSync(domain.Profile{}, SyncRequest{
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

	counts := CountContentTypes(result.Plan)
	if counts.Rules != 1 || counts.Skills != 1 {
		t.Fatalf("counts = %+v, want 1 rule and 1 skill", counts)
	}
	if len(result.Plan.Settings) != 1 {
		t.Fatalf("settings_count = %d, want 1", len(result.Plan.Settings))
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
	_, err := RunSync(domain.Profile{}, SyncRequest{
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

	newLedgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
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

	writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode"), map[string]domain.Entry{
		claudeDst: {SourcePack: "core", Digest: domain.SingleFileDigest([]byte("alpha"))},
	})
	writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex"), map[string]domain.Entry{
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
	_, err := RunSync(domain.Profile{}, SyncRequest{
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

func TestProcessEmbeddedRegistries_MergesIntoRegistryCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
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
	err := processEmbeddedRegistries(domain.Profile{
		Packs: []domain.Pack{{
			Name:       "demo",
			Root:       packRoot,
			Registries: []string{"registry.yaml"},
		}},
	}, home, &stderr)
	if err != nil {
		t.Fatalf("processEmbeddedRegistries: %v", err)
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
