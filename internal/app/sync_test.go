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
	"github.com/shrug-labs/aipack/internal/index"
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
	result, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
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

	var stdout, stderr bytes.Buffer
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
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
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), domain.Profile{}, SyncRequest{
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

	if got := stdout.String(); got != "plan: 0 file ops from 0 content, 2 identical\n" {
		t.Fatalf("stdout = %q, want %q", got, "plan: 0 file ops from 0 content, 2 identical\n")
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
			wantOut:  "create: " + skillDst + "\nplan: 1 file ops from 1 skill, 0 identical\n",
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
				writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode), map[string]domain.Entry{
					filepath.Join(skillDst, "SKILL.md"): {SourcePack: "pack", Digest: domain.SingleFileDigest([]byte("v1 content"))},
				})
			},
			wantOut: "update: " + skillDst + "\nplan: 1 file ops from 1 skill, 0 identical\n",
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
			printDryRun(engine.New(nil, nil), plan, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
					Home:       home,
				},
			}, reg, ContentCounts{Skills: 1}, &buf)

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

	if err := updateIndex(profile, home); err != nil {
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

	if err := updateIndex(profile, home); err != nil {
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
