package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestScanBytesForSecrets_SSHKey(t *testing.T) {
	t.Parallel()
	input := []byte(strings.Join([]string{"-----BEGIN RSA", "PRIVATE KEY-----"}, " ") + "\nMIIEowIBAAK...")
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for RSA private key, got none")
	}
	found := slices.Contains(findings, "matches forbidden secret pattern: SSH key material")
	if !found {
		t.Errorf("expected SSH key material finding, got %v", findings)
	}
}

func TestScanBytesForSecrets_AKIA(t *testing.T) {
	t.Parallel()
	// AKIA followed by 16 uppercase alphanumeric characters.
	input := []byte("config: " + strings.Join([]string{"AK", "IA", "IOSFODNN7", "EXAMPLE"}, ""))
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for AWS access key, got none")
	}
	found := slices.Contains(findings, "matches forbidden secret pattern: AKIA[0-9A-Z]{16}")
	if !found {
		t.Errorf("expected AKIA finding, got %v", findings)
	}
}

func TestScanBytesForSecrets_CloudResourceID(t *testing.T) {
	t.Parallel()
	input := []byte(`resource = "` + strings.Join([]string{"ocid1", "instance", "oc1", "phx", "abc123"}, ".") + `"`)
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for cloud resource ID, got none")
	}
	found := slices.Contains(findings, "matches forbidden secret pattern: ocid1.*")
	if !found {
		t.Errorf("expected ocid1 finding, got %v", findings)
	}
}

func TestScanBytesForSecrets_Clean(t *testing.T) {
	t.Parallel()
	input := []byte("This is normal content with no secrets.\nJust some regular text.\n")
	findings := scanBytesForSecrets(input)
	if len(findings) != 0 {
		t.Errorf("expected no findings for clean content, got %v", findings)
	}
}

// ---------------------------------------------------------------------------
// Conflict helpers
// ---------------------------------------------------------------------------

func TestCheckFileConflict_NoFile(t *testing.T) {
	t.Parallel()
	conflict, err := checkFileConflict([]byte("hello"), filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Error("expected no conflict when dst does not exist")
	}
}

func TestCheckFileConflict_SameContent(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "same.txt")
	if err := os.WriteFile(dst, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflict, err := checkFileConflict([]byte("hello"), dst)
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Error("expected no conflict for identical content")
	}
}

func TestCheckFileConflict_DifferentContent(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "diff.txt")
	if err := os.WriteFile(dst, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflict, err := checkFileConflict([]byte("new content"), dst)
	if err != nil {
		t.Fatal(err)
	}
	if !conflict {
		t.Error("expected conflict for different content")
	}
}

func TestCheckMCPConflict_IgnoresPackMetadataFormatting(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "jira.json")
	if err := os.WriteFile(dst, []byte("{\n  \"name\": \"jira\",\n  \"transport\": \"stdio\",\n  \"command\": [\"uvx\", \"jira-mcp\"],\n  \"available_tools\": [\"get_issue\"],\n  \"notes\": \"metadata only\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcContent, err := domain.MCPTrackedBytes(domain.MCPServer{
		Name:      "jira",
		Transport: domain.TransportStdio,
		Command:   []string{"uvx", "jira-mcp"},
	})
	if err != nil {
		t.Fatalf("MCPTrackedBytes: %v", err)
	}
	conflict, err := checkMCPConflict(srcContent, dst)
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Fatal("expected no conflict for equivalent MCP runtime config")
	}
}

func TestCheckMCPConflict_NormalizesImplicitStdioTransport(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "jira.json")
	if err := os.WriteFile(dst, []byte("{\n  \"name\": \"jira\",\n  \"transport\": \"stdio\",\n  \"command\": [\"uvx\", \"jira-mcp\"]\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcContent, err := domain.MCPInventoryBytes(domain.MCPServer{
		Name:    "jira",
		Command: []string{"uvx", "jira-mcp"},
	})
	if err != nil {
		t.Fatalf("MCPInventoryBytes: %v", err)
	}
	conflict, err := checkMCPConflict(srcContent, dst)
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Fatal("expected no conflict when stdio transport is implicit in source inventory")
	}
}

func TestCheckDirConflict_NoDir(t *testing.T) {
	t.Parallel()
	srcDir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflict, err := checkDirConflict(srcDir, filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Error("expected no conflict when dst dir does not exist")
	}
}

func TestCheckDirConflict_SameContent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	srcDir := filepath.Join(base, "src")
	dstDir := filepath.Join(base, "dst")
	for _, d := range []string{srcDir, dstDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "a.md"), []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	conflict, err := checkDirConflict(srcDir, dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if conflict {
		t.Error("expected no conflict for identical directories")
	}
}

func TestCheckDirConflict_DifferentContent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	srcDir := filepath.Join(base, "src")
	dstDir := filepath.Join(base, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "a.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflict, err := checkDirConflict(srcDir, dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if !conflict {
		t.Error("expected conflict for different directory content")
	}
}

// ---------------------------------------------------------------------------
// Stub harness for integration tests
// ---------------------------------------------------------------------------

type stubHarness struct {
	id      domain.Harness
	capture harness.CaptureResult
}

func (s stubHarness) ID() domain.Harness                                 { return s.id }
func (s stubHarness) Layout(domain.Scope, string, string) harness.Layout { return harness.Layout{} }
func (s stubHarness) Plan(_ context.Context, _ engine.SyncContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s stubHarness) Render(_ context.Context, _ harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s stubHarness) Capture(_ context.Context, _ harness.CaptureContext) (harness.CaptureResult, error) {
	return s.capture, nil
}

// writeLedger writes a ledger JSON file at path with the given entries.
func writeLedger(t *testing.T, path string, entries map[string]domain.Entry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"schema_version":     1,
		"updated_at_epoch_s": 0,
		"tool":               "aipack",
		"managed":            entries,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSaveTestManifest writes a minimal pack.json.
func writeSaveTestManifest(t *testing.T, packRoot, name string) {
	t.Helper()
	m := config.PackManifest{
		SchemaVersion: 2,
		Name:          name,
		Version:       "0.1.0",
		Root:          ".",
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// RunRoundTrip integration tests
// ---------------------------------------------------------------------------

func TestRunRoundTrip_PackSideConflict_Errors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	// Ledger digest represents the original content.
	origContent := []byte("original")
	origDigest := domain.SingleFileDigest(origContent)

	// Harness file changed (different from ledger).
	harnessFile := filepath.Join(projectDir, ".claude", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("harness edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pack file also changed (different from ledger).
	packFile := filepath.Join(packRoot, "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(packFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packFile, []byte("pack edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write ledger.
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: origDigest},
	})

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessFile, Dst: "rules/a.md", Kind: domain.CopyKindFile,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	_, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected error containing 'conflict', got: %v", err)
	}

	// Pack file should be unchanged.
	got, _ := os.ReadFile(packFile)
	if string(got) != "pack edit" {
		t.Errorf("pack file should be unchanged, got %q", got)
	}
}

func TestRunRoundTrip_PackSideConflict_Force(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	origContent := []byte("original")
	origDigest := domain.SingleFileDigest(origContent)

	harnessFile := filepath.Join(projectDir, ".claude", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("harness edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	packFile := filepath.Join(packRoot, "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(packFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packFile, []byte("pack edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: origDigest},
	})

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessFile, Dst: "rules/a.md", Kind: domain.CopyKindFile,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
		Force:     true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("expected 1 conflict, got %d", len(result.Conflicts))
	}
	if len(result.SavedFiles) != 1 {
		t.Errorf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	// Pack file should be overwritten with harness content.
	got, _ := os.ReadFile(packFile)
	if string(got) != "harness edit" {
		t.Errorf("pack file should be overwritten, got %q", got)
	}
}
func TestRunRoundTrip_AgentPackAlreadyNeutral_DoesNotConflict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	harnessFile := filepath.Join(projectDir, ".opencode", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	native := []byte("---\nname: reviewer\ntools:\n  bash: true\n---\nReview\n")
	if err := os.WriteFile(harnessFile, native, 0o644); err != nil {
		t.Fatal(err)
	}

	neutralBytes, err := engine.RenderAgentBytes(domain.Agent{
		Frontmatter: domain.AgentFrontmatter{Name: "reviewer", Tools: []string{"bash"}},
		Body:        []byte("Review\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	neutral := string(neutralBytes)
	packFile := filepath.Join(packRoot, "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(packFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packFile, []byte(neutral), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: "old-digest"},
	})

	stub := stubHarness{
		id: "opencode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessFile, Dst: "agents/reviewer.md", Kind: domain.CopyKindFile,
			}},
			Agents: []domain.Agent{{
				Name:        "reviewer",
				Frontmatter: domain.AgentFrontmatter{Name: "reviewer", Tools: []string{"bash"}},
				Body:        []byte("Review\n"),
				Raw:         native,
				SourcePath:  harnessFile,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"opencode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", result.Conflicts)
	}
	got, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != neutral {
		t.Fatalf("pack file = %q, want neutral %q (RoundTrip)", got, neutral)
	}
}

// ---------------------------------------------------------------------------
// Finding #1: Directory-backed saves don't propagate deletions
// ---------------------------------------------------------------------------
func TestRunRoundTrip_DirSave_PropagatesDeletions(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	// Pre-populate pack skill dir with an extra file.
	packSkillDir := filepath.Join(packRoot, "skills", "my-skill")
	if err := os.MkdirAll(packSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "extra.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source skill dir only has SKILL.md.
	srcSkillDir := filepath.Join(projectDir, ".claude", "skills", "my-skill")
	if err := os.MkdirAll(srcSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkillDir, "SKILL.md"), []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		srcSkillDir: {SourcePack: "my-pack", Digest: "old-digest"},
	})

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: srcSkillDir, Dst: "skills/my-skill", Kind: domain.CopyKindDir,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
		Force:     true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SavedFiles) == 0 {
		t.Fatal("expected at least 1 saved file")
	}

	// SKILL.md should be updated.
	got, err := os.ReadFile(filepath.Join(packSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "updated" {
		t.Errorf("SKILL.md = %q, want %q", got, "updated")
	}

	// extra.md should be removed.
	if _, err := os.Stat(filepath.Join(packSkillDir, "extra.md")); !os.IsNotExist(err) {
		t.Error("extra.md should have been deleted from pack dir, but still exists")
	}
}

// ---------------------------------------------------------------------------
// Finding #4: Forced settings saves should persist immediately
// ---------------------------------------------------------------------------

func TestRunRoundTrip_SettingsSave_ForceWritesAndAdvancesLedger(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	eng := engine.New(nil, nil)
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	// Simulate a tracked settings file that has changed.
	settingsFile := filepath.Join(projectDir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	origContent := []byte(`{"key":"original"}`)
	newContent := []byte(`{"key":"updated"}`)
	if err := os.WriteFile(settingsFile, newContent, 0o644); err != nil {
		t.Fatal(err)
	}

	origDigest := domain.SingleFileDigest(origContent)

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		settingsFile: {SourcePack: "my-pack", Digest: origDigest},
	})

	// Pack settings destination.
	packSettingsDir := filepath.Join(packRoot, "configs", "claudecode")
	if err := os.MkdirAll(packSettingsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:     settingsFile,
				Dst:     "configs/claudecode/settings.local.json",
				Content: newContent,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
		Force:     true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.PendingSettings) != 0 {
		t.Fatalf("PendingSettings = %d, want 0", len(result.PendingSettings))
	}
	if len(result.SavedFiles) != 1 {
		t.Fatalf("SavedFiles = %d, want 1", len(result.SavedFiles))
	}

	got, err := os.ReadFile(filepath.Join(packRoot, "configs", "claudecode", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newContent) {
		t.Fatalf("saved settings = %q, want %q", got, newContent)
	}

	// Reload ledger — once the forced save writes the pack-side file, the
	// harness digest should advance so the next round-trip is clean.
	lg, _, lErr := eng.LoadLedger(ledgerPath)
	if lErr != nil {
		t.Fatal(lErr)
	}
	entry, ok := lg.Managed[settingsFile]
	if !ok {
		t.Fatal("expected ledger entry for settings file")
	}
	if entry.Digest == origDigest {
		t.Errorf("ledger digest did not advance after forced settings save")
	}

	// A second round-trip with the same content should now be clean.
	result2, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.PendingSettings) != 0 {
		t.Errorf("second round-trip PendingSettings = %d, want 0", len(result2.PendingSettings))
	}
	if result2.UnchangedCount != 1 {
		t.Errorf("second round-trip UnchangedCount = %d, want 1", result2.UnchangedCount)
	}
}

// ---------------------------------------------------------------------------
// Finding #5: Modified skill dirs falsely conflict after sync
// ---------------------------------------------------------------------------

func TestRunRoundTrip_SkillDir_NoFalseConflict_AfterSync(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	// Set up a skill dir in the harness with two files.
	srcSkillDir := filepath.Join(projectDir, ".claude", "skills", "my-skill")
	if err := os.MkdirAll(srcSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := []byte("---\ndescription: my skill\n---\nDo stuff\n")
	helperContent := []byte("Helper text\n")
	if err := os.WriteFile(filepath.Join(srcSkillDir, "SKILL.md"), skillContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkillDir, "helper.md"), helperContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Set up pack dir with the same content.
	packSkillDir := filepath.Join(packRoot, "skills", "my-skill")
	if err := os.MkdirAll(packSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "SKILL.md"), skillContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "helper.md"), helperContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Ledger has per-file entries (as sync would create).
	skillPath := filepath.Join(srcSkillDir, "SKILL.md")
	helperPath := filepath.Join(srcSkillDir, "helper.md")
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		skillPath:  {SourcePack: "my-pack", Digest: domain.SingleFileDigest(skillContent)},
		helperPath: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(helperContent)},
	})

	// Now modify only SKILL.md in the harness.
	modifiedSkill := []byte("---\ndescription: my skill\n---\nDo stuff better\n")
	if err := os.WriteFile(filepath.Join(srcSkillDir, "SKILL.md"), modifiedSkill, 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: srcSkillDir, Dst: "skills/my-skill", Kind: domain.CopyKindDir,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	// Round-trip should save cleanly without --force.
	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatalf("expected clean save, got error: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(result.Conflicts))
	}
	if len(result.SavedFiles) != 1 {
		t.Errorf("expected 1 saved file, got %d", len(result.SavedFiles))
	}

	// Verify the pack was updated.
	got, err := os.ReadFile(filepath.Join(packSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(modifiedSkill) {
		t.Errorf("pack SKILL.md = %q, want %q", got, modifiedSkill)
	}
}

// TestRunRoundTrip_NestedSkill_WritesToOriginalPath pins the save contract
// for skills authored under organizational subdirectories. Pack source lives
// at `<pack>/skills/group/alpha/SKILL.md`; the harness receives a flat
// `<harness>/skills/alpha/SKILL.md` on sync. When the user edits that file
// and runs save, the change must land on the original nested path — not at
// `<pack>/skills/alpha/` which would duplicate the skill and break the next
// sync's same-leaf collision check.
func TestRunRoundTrip_NestedSkill_WritesToOriginalPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	// Pack authored with nested skill.
	nestedPackSkillDir := filepath.Join(packRoot, "skills", "group", "alpha")
	if err := os.MkdirAll(nestedPackSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origContent := []byte("---\ndescription: alpha\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(nestedPackSkillDir, "SKILL.md"), origContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Harness has the skill at the flat location (as sync would have written).
	harnessSkillDir := filepath.Join(projectDir, ".claude", "skills", "alpha")
	if err := os.MkdirAll(harnessSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harnessFile := filepath.Join(harnessSkillDir, "SKILL.md")
	if err := os.WriteFile(harnessFile, origContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Ledger records the harness file's digest from sync.
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(origContent)},
	})

	// User edits the harness file.
	modified := []byte("---\ndescription: alpha\n---\nupdated body\n")
	if err := os.WriteFile(harnessFile, modified, 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessSkillDir, Dst: "skills/alpha", Kind: domain.CopyKindDir,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	if _, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{"claudecode"}, Home: home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg); err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}

	// The nested original must carry the update.
	got, err := os.ReadFile(filepath.Join(nestedPackSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read nested pack SKILL.md: %v", err)
	}
	if string(got) != string(modified) {
		t.Errorf("nested pack SKILL.md = %q, want updated %q", got, modified)
	}
	// And no flat duplicate should appear — the next sync would reject it
	// as a same-leaf collision.
	flatDup := filepath.Join(packRoot, "skills", "alpha")
	if _, err := os.Stat(flatDup); err == nil {
		t.Errorf("save created flat duplicate at %s; nested skill save must update original path only", flatDup)
	}
}

func TestRunRoundTrip_NestedSkillAsset_WritesToOriginalPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	nestedPackSkillDir := filepath.Join(packRoot, "skills", "group", "alpha")
	if err := os.MkdirAll(nestedPackSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedPackSkillDir, "SKILL.md"), []byte("---\nname: alpha\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origHelper := []byte("helper\n")
	packHelper := filepath.Join(nestedPackSkillDir, "helper.md")
	if err := os.WriteFile(packHelper, origHelper, 0o644); err != nil {
		t.Fatal(err)
	}

	harnessSkillDir := filepath.Join(projectDir, ".agents", "skills", "alpha")
	if err := os.MkdirAll(harnessSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harnessHelper := filepath.Join(harnessSkillDir, "helper.md")
	if err := os.WriteFile(harnessHelper, origHelper, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessHelper: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(origHelper)},
	})

	modifiedHelper := []byte("updated helper\n")
	if err := os.WriteFile(harnessHelper, modifiedHelper, 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: domain.HarnessCodex,
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessHelper, Dst: "skills/alpha/helper.md", Kind: domain.CopyKindFile,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	if _, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{domain.HarnessCodex}, Home: home,
			Namespaced: true,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg); err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}

	got, err := os.ReadFile(packHelper)
	if err != nil {
		t.Fatalf("read nested helper: %v", err)
	}
	if string(got) != string(modifiedHelper) {
		t.Errorf("nested helper = %q, want %q", got, modifiedHelper)
	}
	flatDup := filepath.Join(packRoot, "skills", "alpha")
	if _, err := os.Stat(flatDup); err == nil {
		t.Errorf("save created flat duplicate at %s; nested skill asset save must update original path only", flatDup)
	}
}

func TestRunRoundTrip_CodexNamespacedSkillWrite_NoFalseConflict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	packSkillDir := filepath.Join(packRoot, "skills", "alpha")
	if err := os.MkdirAll(packSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	packOriginal := []byte("---\nname: alpha\ndescription: Alpha\n---\nbody\n")
	packFile := filepath.Join(packSkillDir, "SKILL.md")
	if err := os.WriteFile(packFile, packOriginal, 0o644); err != nil {
		t.Fatal(err)
	}

	harnessFile := filepath.Join(projectDir, ".agents", "skills", "alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	renderedOriginal, err := harness.RewriteFrontmatterName(packOriginal, "alpha__aipack__my-pack")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, renderedOriginal, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(renderedOriginal)},
	})

	renderedModified := []byte("---\nname: alpha__aipack__my-pack\ndescription: Alpha\n---\nupdated body\n")
	if err := os.WriteFile(harnessFile, renderedModified, 0o644); err != nil {
		t.Fatal(err)
	}
	sourceModified := []byte("---\ndescription: Alpha\nname: alpha\n---\nupdated body\n")

	stub := stubHarness{
		id: domain.HarnessCodex,
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          harnessFile,
				Dst:          "skills/alpha/SKILL.md",
				Content:      sourceModified,
				IsContent:    true,
				SourceDigest: domain.SingleFileDigest(renderedModified),
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{domain.HarnessCodex}, Home: home,
			Namespaced: true,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", result.Conflicts)
	}
	got, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(sourceModified) {
		t.Errorf("pack SKILL.md = %q, want %q", got, sourceModified)
	}
}

func TestRunRoundTrip_RenderedWorkflowWrite_NoFalseConflict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	packWorkflowDir := filepath.Join(packRoot, "workflows")
	if err := os.MkdirAll(packWorkflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	packOriginal := []byte("---\nname: deploy\ndescription: Deploy\n---\nbody\n")
	packFile := filepath.Join(packWorkflowDir, "deploy.md")
	if err := os.WriteFile(packFile, packOriginal, 0o644); err != nil {
		t.Fatal(err)
	}

	harnessFile := filepath.Join(projectDir, ".claude", "commands", "deploy__aipack__my-pack.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	renderedOriginal, err := harness.RewriteFrontmatterName(packOriginal, "deploy__aipack__my-pack")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, renderedOriginal, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(renderedOriginal)},
	})

	renderedModified := []byte("---\nname: deploy__aipack__my-pack\ndescription: Deploy\n---\nupdated body\n")
	if err := os.WriteFile(harnessFile, renderedModified, 0o644); err != nil {
		t.Fatal(err)
	}
	sourceModified := []byte("---\ndescription: Deploy\nname: deploy\n---\nupdated body\n")

	stub := stubHarness{
		id: domain.HarnessClaudeCode,
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          harnessFile,
				Dst:          "workflows/deploy.md",
				Content:      sourceModified,
				IsContent:    true,
				SourceDigest: domain.SingleFileDigest(renderedModified),
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Home: home,
			Namespaced: true,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", result.Conflicts)
	}
	got, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(sourceModified) {
		t.Errorf("pack workflow = %q, want %q", got, sourceModified)
	}
}

// TestRunRoundTrip_NestedAgent_WritesToOriginalPath pins the save contract
// for agents authored under organizational subdirectories. Same shape as
// the nested-skill test above but exercises the per-file CopyAction path
// (agents/workflows come as CopyKindFile with `agents/<id>.md` dst).
func TestRunRoundTrip_NestedAgent_WritesToOriginalPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	// Pack has a nested agent.
	nestedAgentDir := filepath.Join(packRoot, "agents", "team-a")
	if err := os.MkdirAll(nestedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origContent := []byte("---\nname: reviewer\n---\nagent body\n")
	nestedAgentFile := filepath.Join(nestedAgentDir, "reviewer.md")
	if err := os.WriteFile(nestedAgentFile, origContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Harness has the agent flat (as sync would have written).
	harnessAgentFile := filepath.Join(projectDir, ".claude", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(harnessAgentFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessAgentFile, origContent, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessAgentFile: {SourcePack: "my-pack", Digest: domain.SingleFileDigest(origContent)},
	})

	modified := []byte("---\nname: reviewer\n---\nupdated body\n")
	if err := os.WriteFile(harnessAgentFile, modified, 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: "claudecode",
		capture: harness.CaptureResult{
			Copies: []domain.CopyAction{{
				Src: harnessAgentFile, Dst: "agents/reviewer.md", Kind: domain.CopyKindFile,
			}},
			Agents: []domain.Agent{{
				Name: "reviewer", Raw: modified, SourcePack: "my-pack",
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	if _, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{"claudecode"}, Home: home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg); err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}

	got, err := os.ReadFile(nestedAgentFile)
	if err != nil {
		t.Fatalf("read nested pack agent: %v", err)
	}
	if string(got) != string(modified) {
		t.Errorf("nested pack agent = %q, want updated %q", got, modified)
	}
	flatDup := filepath.Join(packRoot, "agents", "reviewer.md")
	if _, err := os.Stat(flatDup); err == nil {
		t.Errorf("save created flat duplicate at %s; nested agent save must update original path only", flatDup)
	}
}

// ---------------------------------------------------------------------------
// Content writes (promoted agents/workflows) in round-trip
// ---------------------------------------------------------------------------

func TestRunRoundTrip_ContentWrite_SavedDirectly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	eng := engine.New(nil, nil)
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	// Simulate a promoted agent SKILL.md on disk (the harness side).
	skillFile := filepath.Join(projectDir, ".agents", "skills", "my-agent", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	promotedContent := []byte("---\nname: my-agent\nsource_type: agent\n---\n\nagent body\n")
	if err := os.WriteFile(skillFile, promotedContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// The ledger records the digest of the promoted SKILL.md (as sync would).
	origDigest := domain.SingleFileDigest(promotedContent)

	// Simulate that the SKILL.md was modified (user edited the agent body).
	modifiedPromoted := []byte("---\nname: my-agent\nsource_type: agent\n---\n\nupdated agent body\n")
	if err := os.WriteFile(skillFile, modifiedPromoted, 0o644); err != nil {
		t.Fatal(err)
	}
	modifiedDigest := domain.SingleFileDigest(modifiedPromoted)

	// Re-rendered agent content (what capture would produce).
	reRendered := []byte("---\nname: my-agent\n---\n\nupdated agent body\n")

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		skillFile: {SourcePack: "my-pack", Digest: origDigest},
	})

	// Pre-populate pack agent file.
	packAgentDir := filepath.Join(packRoot, "agents")
	if err := os.MkdirAll(packAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	stub := stubHarness{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          skillFile,
				Dst:          "agents/my-agent.md",
				Content:      reRendered,
				IsContent:    true,
				SourceDigest: modifiedDigest,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"codex"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Content write should produce a SavedFile, not a PendingSettingsChange.
	if len(result.SavedFiles) != 1 {
		t.Fatalf("SavedFiles = %d, want 1", len(result.SavedFiles))
	}
	if len(result.PendingSettings) != 0 {
		t.Fatalf("PendingSettings = %d, want 0", len(result.PendingSettings))
	}

	// The re-rendered agent content should be written to the pack.
	got, err := os.ReadFile(filepath.Join(packAgentDir, "my-agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(reRendered) {
		t.Errorf("pack agent = %q, want %q", got, reRendered)
	}

	// Ledger should be updated with the SourceDigest (promoted SKILL.md hash),
	// not the re-rendered content hash.
	lg, _, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lg.Managed[skillFile]
	if !ok {
		t.Fatal("expected ledger entry for skill file")
	}
	if entry.Digest != modifiedDigest {
		t.Errorf("ledger digest = %q, want SourceDigest %q", entry.Digest, modifiedDigest)
	}
}

// TestRunRoundTrip_NestedAgent_ContentWriteHonorsAuthoredPath pins the save
// contract for agents authored under organizational subdirectories and
// captured via the WriteAction path (native-TOML agents on Codex, promoted
// agents on Cline/OpenCode-promote). A flat Dst of `agents/<id>.md` must
// resolve through the manifest's RelPath to the original nested location —
// otherwise the save creates `<packRoot>/agents/<id>.md` alongside the
// authored `<packRoot>/agents/team-a/<id>.md`, which trips the same-leaf
// collision in DiscoverIDsByLeaf on the next sync and bricks sync until
// reconciled.
//
// Companion to TestRunRoundTrip_NestedAgent_WritesToOriginalPath (which
// covers the CopyKindFile path used by harnesses that keep native agents
// as `.md` files). Together they pin both capture shapes against the
// nested-authoring invariant.
func TestRunRoundTrip_NestedAgent_ContentWriteHonorsAuthoredPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")
	writeSaveTestManifest(t, packRoot, "my-pack")

	// Pack has a nested agent authored under an organizational subdir.
	nestedAgentDir := filepath.Join(packRoot, "agents", "team-a")
	if err := os.MkdirAll(nestedAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origAgent := []byte("---\nname: reviewer\n---\nagent body\n")
	nestedAgentFile := filepath.Join(nestedAgentDir, "reviewer.md")
	if err := os.WriteFile(nestedAgentFile, origAgent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Harness has the agent materialized as a promoted SKILL.md (as Cline or
	// OpenCode-promote would produce) or as a native TOML (Codex). The Write
	// shape is identical — Dst is the flat `agents/reviewer.md`.
	skillFile := filepath.Join(projectDir, ".agents", "skills", "reviewer", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	origSkill := []byte("---\nname: reviewer\nsource_type: agent\n---\n\nagent body\n")
	if err := os.WriteFile(skillFile, origSkill, 0o644); err != nil {
		t.Fatal(err)
	}
	origSkillDigest := domain.SingleFileDigest(origSkill)

	// User edits the promoted SKILL.md.
	modifiedSkill := []byte("---\nname: reviewer\nsource_type: agent\n---\n\nupdated body\n")
	if err := os.WriteFile(skillFile, modifiedSkill, 0o644); err != nil {
		t.Fatal(err)
	}
	modifiedSkillDigest := domain.SingleFileDigest(modifiedSkill)
	reRendered := []byte("---\nname: reviewer\n---\nupdated body\n")

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		skillFile: {SourcePack: "my-pack", Digest: origSkillDigest},
	})

	stub := stubHarness{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          skillFile,
				Dst:          "agents/reviewer.md",
				Content:      reRendered,
				IsContent:    true,
				SourceDigest: modifiedSkillDigest,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	// Use Force=true to proceed past the conflict detection that compares
	// the pack file bytes against the Write's re-rendered bytes — in a real
	// flow the pack file holds the authored baseline, differs from the
	// re-rendered output, and the user resolves by forcing save. The test's
	// subject is the dst resolution, not the conflict-detection policy.
	if _, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope: "project", ProjectDir: projectDir,
			Harnesses: []domain.Harness{"codex"}, Home: home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
		Force:     true,
	}, reg); err != nil {
		t.Fatalf("RunRoundTrip: %v", err)
	}

	// The nested original must carry the re-rendered content.
	got, err := os.ReadFile(nestedAgentFile)
	if err != nil {
		t.Fatalf("read nested pack agent: %v", err)
	}
	if string(got) != string(reRendered) {
		t.Errorf("nested pack agent = %q, want re-rendered %q", got, reRendered)
	}
	// And no flat duplicate may appear — the next sync would reject it
	// as a same-leaf collision.
	flatDup := filepath.Join(packRoot, "agents", "reviewer.md")
	if _, err := os.Stat(flatDup); err == nil {
		t.Errorf("save created flat duplicate at %s; nested agent Write must resolve to authored path", flatDup)
	}
}

func TestRunRoundTrip_ContentWrite_UnchangedSkipped(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "pack")

	skillFile := filepath.Join(projectDir, ".agents", "skills", "my-wf", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	promotedContent := []byte("---\nname: my-wf\nsource_type: workflow\n---\n\nwf body\n")
	if err := os.WriteFile(skillFile, promotedContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Ledger digest matches the on-disk SKILL.md — nothing changed.
	sourceDigest := domain.SingleFileDigest(promotedContent)

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessCodex)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		skillFile: {SourcePack: "my-pack", Digest: sourceDigest},
	})

	reRendered := []byte("---\nname: my-wf\n---\n\nwf body\n")

	stub := stubHarness{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          skillFile,
				Dst:          "workflows/my-wf.md",
				Content:      reRendered,
				IsContent:    true,
				SourceDigest: sourceDigest,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := RunRoundTrip(context.Background(), engine.New(nil, nil), RoundTripRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{"codex"},
			Home:       home,
		},
		PackRoots: map[string]string{"my-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.SavedFiles) != 0 {
		t.Errorf("SavedFiles = %d, want 0 (unchanged)", len(result.SavedFiles))
	}
	if result.UnchangedCount != 1 {
		t.Errorf("UnchangedCount = %d, want 1", result.UnchangedCount)
	}
}
