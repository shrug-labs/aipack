package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// pipelineStub extends stubHarness with ManagedRoots support.
type pipelineStub struct {
	id      domain.Harness
	capture harness.CaptureResult
	roots   []string
}

func (s pipelineStub) ID() domain.Harness { return s.id }
func (s pipelineStub) Plan(engine.SyncContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s pipelineStub) Render(harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s pipelineStub) ManagedRoots(domain.Scope, string, string) []string    { return s.roots }
func (s pipelineStub) SettingsPaths(domain.Scope, string, string) []string   { return nil }
func (s pipelineStub) StrictExtraDirs(domain.Scope, string, string) []string { return nil }
func (s pipelineStub) PackRelativePaths() []string                           { return nil }
func (s pipelineStub) StripManagedSettings(b []byte, _ string) ([]byte, error) {
	return b, nil
}
func (s pipelineStub) Capture(harness.CaptureContext) (harness.CaptureResult, error) {
	return s.capture, nil
}
func (s pipelineStub) CleanActions(domain.Scope, string, string) []harness.CleanAction { return nil }

func TestDetectHarnessesWithContent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)

	stub1 := pipelineStub{id: "claudecode", roots: []string{rulesDir}}
	stub2 := pipelineStub{id: "opencode", roots: []string{filepath.Join(tmp, "nonexistent")}}
	reg := harness.NewRegistry(stub1, stub2)

	result := DetectHarnessesWithContent(domain.ScopeProject, tmp, tmp, reg)
	if len(result) != 1 {
		t.Fatalf("expected 1 harness with content, got %d", len(result))
	}
	if result[0] != "claudecode" {
		t.Errorf("expected claudecode, got %s", result[0])
	}
}

func TestDetectHarnessesWithContent_None(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	stub := pipelineStub{id: "claudecode", roots: []string{filepath.Join(tmp, "nope")}}
	reg := harness.NewRegistry(stub)

	result := DetectHarnessesWithContent(domain.ScopeProject, tmp, tmp, reg)
	if len(result) != 0 {
		t.Fatalf("expected no harnesses, got %d", len(result))
	}
}

func TestDiscoverContentVectors(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Create a harness file and capture result with rules.
	rulesFile := filepath.Join(tmp, "rules", "a.md")
	os.MkdirAll(filepath.Dir(rulesFile), 0o755)
	os.WriteFile(rulesFile, []byte("rule content"), 0o644)

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{
				{Src: rulesFile, Dst: "rules/a.md", Kind: domain.CopyKindFile},
			},
		},
	}
	reg := harness.NewRegistry(stub)

	vectors, err := DiscoverContentVectors("claudecode", domain.ScopeProject, tmp, tmp, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("expected at least one vector")
	}
	found := false
	for _, v := range vectors {
		if v == domain.CategoryRules {
			found = true
		}
	}
	if !found {
		t.Errorf("expected rules in vectors, got %v", vectors)
	}
}

func TestDiscoverContentVectors_IncludesPromotedContentCategories(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	stub := pipelineStub{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          filepath.Join(tmp, ".agents", "skills", "demo-agent", "SKILL.md"),
				Dst:          filepath.Join("agents", "demo-agent.md"),
				Content:      []byte("---\nname: demo-agent\n---\nbody\n"),
				IsContent:    true,
				SourceDigest: domain.SingleFileDigest([]byte("promoted")),
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	vectors, err := DiscoverContentVectors("codex", domain.ScopeProject, tmp, tmp, reg)
	if err != nil {
		t.Fatal(err)
	}

	foundAgent := false
	foundSettings := false
	for _, v := range vectors {
		if v == domain.CategoryAgents {
			foundAgent = true
		}
		if v == domain.CategorySettings {
			foundSettings = true
		}
	}
	if !foundAgent {
		t.Fatalf("expected agents in vectors, got %v", vectors)
	}
	if foundSettings {
		t.Fatalf("did not expect settings in vectors for promoted content, got %v", vectors)
	}
}

func TestDiscoverSaveFiles_NoLedger(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	os.MkdirAll(projectDir, 0o755)

	// Create harness file.
	rulesFile := filepath.Join(home, "rules", "a.md")
	os.MkdirAll(filepath.Dir(rulesFile), 0o755)
	os.WriteFile(rulesFile, []byte("rule content"), 0o644)

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{
				{Src: rulesFile, Dst: "rules/a.md", Kind: domain.CopyKindFile},
			},
		},
	}
	reg := harness.NewRegistry(stub)

	candidates, _, err := DiscoverSaveFiles(DiscoverSaveRequest{
		HarnessID:  "claudecode",
		Categories: []domain.PackCategory{domain.CategoryRules},
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		ConfigDir:  configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].State != FileUntracked {
		t.Errorf("expected untracked, got state %d", candidates[0].State)
	}
	if !candidates[0].Selected {
		t.Error("expected untracked candidate to be selected by default")
	}
}

func TestDiscoverSaveFiles_WithLedger(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	os.MkdirAll(projectDir, 0o755)

	// Create harness files.
	cleanFile := filepath.Join(home, "rules", "clean.md")
	modFile := filepath.Join(home, "rules", "mod.md")
	os.MkdirAll(filepath.Dir(cleanFile), 0o755)
	os.WriteFile(cleanFile, []byte("clean content"), 0o644)
	os.WriteFile(modFile, []byte("modified content"), 0o644)

	// Write ledger with matching digest for clean, non-matching for mod.
	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		cleanFile: {SourcePack: "test-pack", Digest: domain.SingleFileDigest([]byte("clean content"))},
		modFile:   {SourcePack: "test-pack", Digest: "old-digest"},
	})

	// Create a minimal pack for classification.
	packRoot := filepath.Join(configDir, "packs", "test-pack")
	writeSaveTestManifest(t, packRoot, "test-pack")

	// Write sync-config so ResolveActiveProfile can find the profile.
	os.MkdirAll(configDir, 0o755)
	writeSyncConfig(t, configDir, "default", []string{"claudecode"}, "project")
	writeMinimalProfile(t, configDir, "default", "test-pack")

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{
				{Src: cleanFile, Dst: "rules/clean.md", Kind: domain.CopyKindFile},
				{Src: modFile, Dst: "rules/mod.md", Kind: domain.CopyKindFile},
			},
		},
	}
	reg := harness.NewRegistry(stub)

	candidates, _, err := DiscoverSaveFiles(DiscoverSaveRequest{
		HarnessID:  "claudecode",
		Categories: []domain.PackCategory{domain.CategoryRules},
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		ConfigDir:  configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	// Find each candidate.
	var clean, mod *SaveCandidate
	for i := range candidates {
		switch candidates[i].HarnessPath {
		case cleanFile:
			clean = &candidates[i]
		case modFile:
			mod = &candidates[i]
		}
	}
	if clean == nil || mod == nil {
		t.Fatal("missing expected candidates")
	}
	if clean.Selected {
		t.Error("clean file should not be selected by default")
	}
	if !mod.Selected {
		t.Error("modified file should be selected by default")
	}
}

func TestDiscoverSaveFiles_SelectsTrackedSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	os.MkdirAll(projectDir, 0o755)

	settingsPath := filepath.Join(home, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"managed":false}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		settingsPath: {SourcePack: "test-pack", Digest: domain.SingleFileDigest([]byte(`{"managed":true}`))},
	})

	packRoot := filepath.Join(configDir, "packs", "test-pack")
	writeSaveTestManifest(t, packRoot, "test-pack")
	writeSyncConfig(t, configDir, "default", []string{"claudecode"}, "project")
	writeMinimalProfile(t, configDir, "default", "test-pack")

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Dst:     filepath.Join("configs", "claudecode", "settings.local.json"),
				Src:     settingsPath,
				Content: []byte(`{"managed":false}`),
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	candidates, _, err := DiscoverSaveFiles(DiscoverSaveRequest{
		HarnessID:  "claudecode",
		Categories: []domain.PackCategory{domain.CategorySettings},
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		ConfigDir:  configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].State != FileSettings {
		t.Fatalf("expected settings state, got %v", candidates[0].State)
	}
	if !candidates[0].Selected {
		t.Fatal("expected tracked settings change to be selected")
	}
}

func TestDiscoverSaveFiles_MCPCandidates(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	os.MkdirAll(projectDir, 0o755)

	settingsPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("[mcp_servers.test]\ncommand='echo'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stub := pipelineStub{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Dst:     filepath.Join("configs", "codex", "config.toml"),
				Src:     settingsPath,
				Content: []byte("[mcp_servers.test]\ncommand='echo'\n"),
			}},
			MCPServers: map[string]domain.MCPServer{
				"test": {
					Name:      "test",
					Transport: domain.TransportStdio,
					Command:   []string{"echo", "hi"},
				},
			},
			AllowedTools: map[string][]string{
				"test": {"list"},
			},
		},
	}
	reg := harness.NewRegistry(stub)

	candidates, _, err := DiscoverSaveFiles(DiscoverSaveRequest{
		HarnessID:  "codex",
		Categories: []domain.PackCategory{domain.CategoryMCP},
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		ConfigDir:  configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 MCP candidate, got %d", len(candidates))
	}
	if candidates[0].Category != domain.CategoryMCP {
		t.Fatalf("expected MCP category, got %q", candidates[0].Category)
	}
	if candidates[0].RelPath != "test" {
		t.Fatalf("expected rel path test, got %q", candidates[0].RelPath)
	}
	if !candidates[0].Selected {
		t.Fatal("expected MCP candidate to be selected")
	}
}

func TestRunSavePipeline_NewPack(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)

	// Create sync-config so registerNewPack can load it.
	writeSyncConfig(t, configDir, "default", []string{"claudecode"}, "project")

	// Create harness file.
	harnessFile := filepath.Join(home, "rules", "a.md")
	os.MkdirAll(filepath.Dir(harnessFile), 0o755)
	os.WriteFile(harnessFile, []byte("rule content"), 0o644)

	stub := pipelineStub{id: "claudecode"}
	reg := harness.NewRegistry(stub)

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "a",
				Category:    domain.CategoryRules,
				Kind:        domain.CopyKindFile,
				State:       FileUntracked,
			},
			Selected: true,
		}},
		PackName:   "new-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
		CreatePack: true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if !result.PackCreated {
		t.Error("expected PackCreated=true")
	}
	if len(result.SavedFiles) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	// Verify file written to pack.
	packFile := filepath.Join(configDir, "packs", "new-pack", "rules", "a.md")
	content, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatalf("pack file not written: %v", err)
	}
	if string(content) != "rule content" {
		t.Errorf("unexpected pack file content: %q", content)
	}

	// Verify manifest updated.
	m, err := config.LoadPackManifest(filepath.Join(configDir, "packs", "new-pack", "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rules) == 0 {
		t.Error("expected manifest to include rule 'a'")
	}

	// Verify sync-config updated.
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := sc.InstalledPacks["new-pack"]
	if !ok {
		t.Fatal("pack not registered in sync-config")
	}
	if meta.Method != config.MethodLocal {
		t.Errorf("expected method %q, got %q", config.MethodLocal, meta.Method)
	}
}

func TestRunSavePipeline_ExistingPack(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	writeSaveTestManifest(t, packRoot, "test-pack")
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)

	// Create harness file.
	harnessFile := filepath.Join(home, "rules", "b.md")
	os.MkdirAll(filepath.Dir(harnessFile), 0o755)
	os.WriteFile(harnessFile, []byte("new rule"), 0o644)

	stub := pipelineStub{id: "claudecode"}
	reg := harness.NewRegistry(stub)

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "b",
				Category:    domain.CategoryRules,
				Kind:        domain.CopyKindFile,
				State:       FileUntracked,
			},
			Selected: true,
		}},
		PackName:   "test-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if result.PackCreated {
		t.Error("did not expect PackCreated for existing pack")
	}
	if len(result.SavedFiles) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	// Verify file exists in pack.
	packFile := filepath.Join(packRoot, "rules", "b.md")
	content, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatalf("pack file not written: %v", err)
	}
	if string(content) != "new rule" {
		t.Errorf("unexpected content: %q", content)
	}

	// Verify ledger updated.
	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lg.Managed[harnessFile]
	if !ok {
		t.Fatal("harness file not in ledger")
	}
	if entry.SourcePack != "test-pack" {
		t.Errorf("expected source pack test-pack, got %q", entry.SourcePack)
	}
}

func TestRunSavePipeline_PrefersNormalizedContentForFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSaveTestManifest(t, packRoot, "test-pack")

	harnessFile := filepath.Join(home, ".claude", "agents", "demo.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("---\nname: demo\ntools: {}\n---\nharness-native\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	normalized := []byte("---\nname: demo\ndescription: canonical\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\npack-native\n")
	reg := harness.NewRegistry(pipelineStub{id: "claudecode"})

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "demo",
				Category:    domain.CategoryAgents,
				Kind:        domain.CopyKindFile,
				State:       FileModified,
				Content:     normalized,
			},
			Selected: true,
		}},
		PackName:   "test-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SavedFiles) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	packFile := filepath.Join(packRoot, "agents", "demo.md")
	got, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if string(got) != string(normalized) {
		t.Fatalf("saved content mismatch:\n%s", got)
	}
}

func TestRunSavePipeline_LedgerDigestUsesRawBytes(t *testing.T) {
	// Verify the ledger records the digest of the raw harness file, not the
	// normalized Content. Change detection reads raw bytes from disk, so the
	// ledger must match to avoid phantom "modified" state after save.
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	writeSaveTestManifest(t, packRoot, "test-pack")

	rawContent := []byte("---\nname: demo\ntools: {}\n---\nharness-native\n")
	normalizedContent := []byte("---\nname: demo\n---\npack-native\n")

	harnessFile := filepath.Join(home, ".claude", "agents", "demo.md")
	os.MkdirAll(filepath.Dir(harnessFile), 0o755)
	os.WriteFile(harnessFile, rawContent, 0o644)

	reg := harness.NewRegistry(pipelineStub{id: "claudecode"})
	_, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "demo",
				Category:    domain.CategoryAgents,
				Kind:        domain.CopyKindFile,
				State:       FileModified,
				Content:     normalizedContent,
			},
			Selected: true,
		}},
		PackName:   "test-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Pack should have normalized content.
	packFile := filepath.Join(packRoot, "agents", "demo.md")
	got, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if string(got) != string(normalizedContent) {
		t.Fatalf("pack content should be normalized, got:\n%s", got)
	}

	// Ledger digest should match the raw harness bytes, not normalized.
	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lg.Managed[harnessFile]
	if !ok {
		t.Fatal("harness file not in ledger")
	}
	wantDigest := domain.SingleFileDigest(rawContent)
	if entry.Digest != wantDigest {
		t.Errorf("ledger digest should match raw harness bytes\n  got:  %s\n  want: %s", entry.Digest, wantDigest)
		if entry.Digest == domain.SingleFileDigest(normalizedContent) {
			t.Error("ledger recorded normalized digest — this causes phantom drift")
		}
	}

	// Confirm change detection sees the file as clean (no phantom drift).
	changed, err := contentChangedSinceLedger(rawContent, harnessFile, entry.Digest, domain.CopyKindFile)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("file should be clean after save, but change detection reports modified (phantom drift)")
	}
}

func TestRunSavePipeline_SkipsFilesTrackedToDifferentPack(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	targetPackRoot := filepath.Join(configDir, "packs", "target-pack")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSaveTestManifest(t, targetPackRoot, "target-pack")

	harnessFile := filepath.Join(home, "rules", "shared.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("shared rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "source-pack", Digest: domain.SingleFileDigest([]byte("shared rule"))},
	})

	reg := harness.NewRegistry(pipelineStub{id: "claudecode"})
	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "shared",
				Category:    domain.CategoryRules,
				Kind:        domain.CopyKindFile,
				State:       FileModified,
				PackName:    "source-pack",
			},
			Selected: true,
		}},
		PackName:   "target-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.SavedFiles) != 0 {
		t.Fatalf("expected 0 saved files, got %d", len(result.SavedFiles))
	}
	if _, err := os.Stat(filepath.Join(targetPackRoot, "rules", "shared.md")); !os.IsNotExist(err) {
		t.Fatalf("expected target pack file to remain absent, stat err=%v", err)
	}

	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lg.Managed[harnessFile].SourcePack; got != "source-pack" {
		t.Fatalf("SourcePack = %q, want source-pack", got)
	}
}

func TestRunSavePipeline_SavesSettingsToConfigsAndManifest(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSaveTestManifest(t, packRoot, "test-pack")

	settingsPath := filepath.Join(home, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"allow":["Read"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := harness.NewRegistry(pipelineStub{id: "claudecode"})
	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: settingsPath,
				RelPath:     "settings.local.json",
				Category:    domain.CategorySettings,
				Kind:        domain.CopyKindFile,
				State:       FileSettings,
				Content:     []byte(`{"permissions":{"allow":["Read"]}}`),
			},
			Selected: true,
		}},
		PackName:   "test-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SavedFiles) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	wantPath := filepath.Join(packRoot, "configs", "claudecode", "settings.local.json")
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("reading saved settings: %v", err)
	}
	if string(got) != `{"permissions":{"allow":["Read"]}}` {
		t.Fatalf("saved settings mismatch: %s", got)
	}
	if _, err := os.Stat(filepath.Join(packRoot, "settings", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected legacy settings path present, stat err=%v", err)
	}

	manifest, err := config.LoadPackManifest(filepath.Join(packRoot, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := manifest.Configs.HarnessSettings["claudecode"]
	if len(files) != 1 || files[0] != "settings.local.json" {
		t.Fatalf("HarnessSettings[claudecode] = %v, want [settings.local.json]", files)
	}
}

func TestRunSavePipeline_SecretScan(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	writeSaveTestManifest(t, packRoot, "test-pack")

	// Create harness file with a secret.
	harnessFile := filepath.Join(home, "rules", "secret.md")
	os.MkdirAll(filepath.Dir(harnessFile), 0o755)
	os.WriteFile(harnessFile, []byte("key: AKIAIOSFODNN7EXAMPLE"), 0o644)

	stub := pipelineStub{id: "claudecode"}
	reg := harness.NewRegistry(stub)

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "secret",
				Category:    domain.CategoryRules,
				Kind:        domain.CopyKindFile,
				State:       FileUntracked,
			},
			Selected: true,
		}},
		PackName:  "test-pack",
		ConfigDir: configDir,
		Scope:     domain.ScopeProject, ProjectDir: projectDir, Home: home,
		HarnessID: "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.SecretFindings) == 0 {
		t.Error("expected secret findings for AKIA pattern")
	}
}

func TestRunSavePipeline_MCPServer(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	writeSaveTestManifest(t, packRoot, "test-pack")

	settingsPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}

	server := domain.MCPServer{
		Name:         "test",
		Transport:    domain.TransportStdio,
		Command:      []string{"echo", "hi"},
		AllowedTools: []string{"list"},
	}
	content, err := domain.MCPInventoryBytes(server)
	if err != nil {
		t.Fatal(err)
	}

	stub := pipelineStub{id: "codex"}
	reg := harness.NewRegistry(stub)

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath:  settingsPath,
				RelPath:      "test",
				Category:     domain.CategoryMCP,
				Kind:         domain.CopyKindFile,
				State:        FileUntracked,
				Content:      content,
				AllowedTools: []string{"list"},
			},
			Selected: true,
		}},
		PackName:   "test-pack",
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
		HarnessID:  "codex",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.SavedFiles) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	mcpFile := filepath.Join(packRoot, "mcp", "test.json")
	raw, err := os.ReadFile(mcpFile)
	if err != nil {
		t.Fatalf("expected MCP file to be written: %v", err)
	}

	var saved domain.MCPServer
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("unmarshal saved MCP file: %v", err)
	}
	if saved.Name != "test" {
		t.Fatalf("expected saved MCP name test, got %q", saved.Name)
	}
	if len(saved.AllowedTools) != 0 {
		t.Fatalf("saved MCP inventory should not include runtime allowed_tools: %v", saved.AllowedTools)
	}

	manifest, err := config.LoadPackManifest(filepath.Join(packRoot, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	defaults, ok := manifest.MCP.Servers["test"]
	if !ok {
		t.Fatal("expected MCP manifest entry")
	}
	if len(defaults.DefaultAllowedTools) != 1 || defaults.DefaultAllowedTools[0] != "list" {
		t.Fatalf("expected default allowed tools to be preserved, got %v", defaults.DefaultAllowedTools)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex")
	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lg.Managed[domain.MCPLedgerKey(settingsPath, "test")]
	if !ok {
		t.Fatal("expected MCP ledger entry")
	}
	if entry.SourcePack != "test-pack" {
		t.Fatalf("expected source pack test-pack, got %q", entry.SourcePack)
	}
}

func TestRunSavePipeline_Conflict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(configDir, "packs", "test-pack")

	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	writeSaveTestManifest(t, packRoot, "test-pack")

	// Create existing pack file with different content.
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "c.md"), []byte("pack version"), 0o644)

	// Create harness file with different content.
	harnessFile := filepath.Join(home, "rules", "c.md")
	os.MkdirAll(filepath.Dir(harnessFile), 0o755)
	os.WriteFile(harnessFile, []byte("harness version"), 0o644)

	stub := pipelineStub{id: "claudecode"}
	reg := harness.NewRegistry(stub)

	result, err := RunSavePipeline(SavePipelineRequest{
		Candidates: []SaveCandidate{{
			HarnessFile: HarnessFile{
				HarnessPath: harnessFile,
				RelPath:     "c",
				Category:    domain.CategoryRules,
				Kind:        domain.CopyKindFile,
				State:       FileModified,
			},
			Selected: true,
		}},
		PackName:  "test-pack",
		ConfigDir: configDir,
		Scope:     domain.ScopeProject, ProjectDir: projectDir, Home: home,
		HarnessID: "claudecode",
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(result.Conflicts))
	}
	if len(result.SavedFiles) != 0 {
		t.Errorf("expected 0 saved files with conflict, got %d", len(result.SavedFiles))
	}

	// Verify pack file was NOT overwritten.
	content, _ := os.ReadFile(filepath.Join(packRoot, "rules", "c.md"))
	if string(content) != "pack version" {
		t.Error("pack file should not have been overwritten without --force")
	}
}

func TestInstalledPackNames(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packsDir := filepath.Join(configDir, "packs")

	// Create two packs with manifests and one dir without.
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(packsDir, name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "pack.json"), []byte("{}"), 0o644)
	}
	os.MkdirAll(filepath.Join(packsDir, "not-a-pack"), 0o755)

	names, err := InstalledPackNames(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 packs, got %d: %v", len(names), names)
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("unexpected pack names: %v", names)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func writeSyncConfig(t *testing.T, configDir, profileName string, harnesses []string, scope string) {
	t.Helper()
	var sc config.SyncConfig
	sc.SchemaVersion = 1
	sc.Defaults.Profile = profileName
	sc.Defaults.Harnesses = harnesses
	sc.Defaults.Scope = scope
	sc.InstalledPacks = map[string]config.InstalledPackMeta{}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalProfile(t *testing.T, configDir, name, packName string) {
	t.Helper()
	profilesDir := filepath.Join(configDir, "profiles")
	os.MkdirAll(profilesDir, 0o755)
	content := "schema_version: 2\npacks:\n  - name: " + packName + "\n"
	if err := os.WriteFile(filepath.Join(profilesDir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
