package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestScanBytesForSecrets_SSHKey(t *testing.T) {
	t.Parallel()
	input := []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAK...")
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for RSA private key, got none")
	}
	found := false
	for _, f := range findings {
		if f == "matches forbidden secret pattern: SSH key material" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SSH key material finding, got %v", findings)
	}
}

func TestScanBytesForSecrets_AKIA(t *testing.T) {
	t.Parallel()
	// AKIA followed by 16 uppercase alphanumeric characters.
	input := []byte(`config: AKIAIOSFODNN7EXAMPLE`)
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for AWS access key, got none")
	}
	found := false
	for _, f := range findings {
		if f == "matches forbidden secret pattern: AKIA[0-9A-Z]{16}" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AKIA finding, got %v", findings)
	}
}

func TestScanBytesForSecrets_CloudResourceID(t *testing.T) {
	t.Parallel()
	input := []byte(`resource = "ocid1.instance.oc1.phx.abc123"`)
	findings := scanBytesForSecrets(input)
	if len(findings) == 0 {
		t.Fatal("expected findings for cloud resource ID, got none")
	}
	found := false
	for _, f := range findings {
		if f == "matches forbidden secret pattern: ocid1.*" {
			found = true
			break
		}
	}
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

func TestScanSnapshotForSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a .json file containing a secret — scanSnapshotForSecrets skips .md files.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"key": "ocid1.vault.oc1.phx.abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a .md file without secrets — should be skipped by the scanner.
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Safe readme\nNo secrets here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := scanSnapshotForSecrets(dir)
	if len(findings) == 0 {
		t.Fatal("expected findings from config.json, got none")
	}

	// Verify the finding references the json file, not the md file.
	foundJSON := false
	foundMD := false
	for _, f := range findings {
		if filepath.ToSlash(f) == "config.json: matches forbidden secret pattern: ocid1.*" {
			foundJSON = true
		}
		if filepath.Base(f) == "readme.md" {
			foundMD = true
		}
	}
	if !foundJSON {
		t.Errorf("expected config.json in findings, got %v", findings)
	}
	if foundMD {
		t.Errorf("readme.md should be skipped, but appeared in findings: %v", findings)
	}
}

func TestBuildPackManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create the expected directory structure.
	for _, sub := range []string{"rules", "agents", "workflows", "skills/s1", "mcp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "a.md"), []byte("rule a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "b.md"), []byte("agent b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflows", "c.md"), []byte("workflow c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "s1", "SKILL.md"), []byte("skill s1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp", "m.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := map[string]domain.MCPServer{
		"m": {Name: "m", Command: []string{"test"}},
	}
	allowedTools := map[string][]string{
		"m": {"tool1", "tool2"},
	}

	manifest, err := buildPackManifest(dir, "1.0.0", servers, allowedTools)
	if err != nil {
		t.Fatal(err)
	}

	if manifest.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", manifest.Version, "1.0.0")
	}

	// Rules
	if len(manifest.Rules) != 1 || manifest.Rules[0] != "a" {
		t.Errorf("Rules = %v, want [a]", manifest.Rules)
	}

	// Agents
	if len(manifest.Agents) != 1 || manifest.Agents[0] != "b" {
		t.Errorf("Agents = %v, want [b]", manifest.Agents)
	}

	// Workflows
	if len(manifest.Workflows) != 1 || manifest.Workflows[0] != "c" {
		t.Errorf("Workflows = %v, want [c]", manifest.Workflows)
	}

	// Skills
	if len(manifest.Skills) != 1 || manifest.Skills[0] != "s1" {
		t.Errorf("Skills = %v, want [s1]", manifest.Skills)
	}

	// MCP servers
	mDefaults, ok := manifest.MCP.Servers["m"]
	if !ok {
		t.Fatal("expected MCP server 'm' in manifest")
	}
	if len(mDefaults.DefaultAllowedTools) != 2 {
		t.Errorf("DefaultAllowedTools = %v, want 2 items", mDefaults.DefaultAllowedTools)
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

func (s stubHarness) ID() domain.Harness                               { return s.id }
func (s stubHarness) Plan(engine.SyncContext) (domain.Fragment, error) { return domain.Fragment{}, nil }
func (s stubHarness) Render(harness.RenderContext) (domain.Fragment, error) {
	return domain.Fragment{}, nil
}
func (s stubHarness) ManagedRoots(domain.Scope, string, string) []string      { return nil }
func (s stubHarness) SettingsPaths(domain.Scope, string, string) []string     { return nil }
func (s stubHarness) StrictExtraDirs(domain.Scope, string, string) []string   { return nil }
func (s stubHarness) PackRelativePaths() []string                             { return nil }
func (s stubHarness) StripManagedSettings(b []byte, _ string) ([]byte, error) { return b, nil }
func (s stubHarness) Capture(harness.CaptureContext) (harness.CaptureResult, error) {
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
		SchemaVersion: 1,
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
// RunToPack integration tests
// ---------------------------------------------------------------------------

func TestRunToPack_ExistingPack_NoConflict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	// Create pack with manifest but no rules/a.md yet.
	writeSaveTestManifest(t, packRoot, packName)

	// Harness file to capture.
	harnessFile := filepath.Join(home, "harness", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("new rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write ledger so the file is not skipped.
	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: packName, Digest: "old-digest"},
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

	// Write sync-config so registration doesn't fail.
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SavedFiles) != 1 {
		t.Errorf("expected 1 saved file, got %d", len(result.SavedFiles))
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(result.Conflicts))
	}

	// Verify file was written.
	got, err := os.ReadFile(filepath.Join(packRoot, "rules", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new rule" {
		t.Errorf("pack file = %q, want %q", got, "new rule")
	}

	// Verify ledger was updated with the saved file's provenance.
	lg, _, lErr := engine.LoadLedger(ledgerPath)
	if lErr != nil {
		t.Fatal(lErr)
	}
	entry, ok := lg.Managed[harnessFile]
	if !ok {
		t.Fatal("expected ledger entry for harness file after save")
	}
	if entry.SourcePack != packName {
		t.Errorf("ledger source_pack = %q, want %q", entry.SourcePack, packName)
	}
	wantDigest := domain.SingleFileDigest([]byte("new rule"))
	if entry.Digest != wantDigest {
		t.Errorf("ledger digest = %q, want %q", entry.Digest, wantDigest)
	}
}

func TestRunToPack_ExistingPack_Conflict_Errors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	writeSaveTestManifest(t, packRoot, packName)

	// Existing pack file with different content.
	rulesDir := filepath.Join(packRoot, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "a.md"), []byte("pack version"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Harness file with different content.
	harnessFile := filepath.Join(home, "harness", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("harness version"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: packName, Digest: "old-digest"},
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

	_, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
	}, reg)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected error containing 'conflict', got: %v", err)
	}

	// Verify pack file was NOT overwritten.
	got, _ := os.ReadFile(filepath.Join(rulesDir, "a.md"))
	if string(got) != "pack version" {
		t.Errorf("pack file should be unchanged, got %q", got)
	}
}

func TestRunToPack_ExistingPack_Conflict_Force(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	writeSaveTestManifest(t, packRoot, packName)

	rulesDir := filepath.Join(packRoot, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "a.md"), []byte("pack version"), 0o644); err != nil {
		t.Fatal(err)
	}

	harnessFile := filepath.Join(home, "harness", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("harness version"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: packName, Digest: "old-digest"},
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

	result, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
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

	// Verify file WAS overwritten.
	got, _ := os.ReadFile(filepath.Join(rulesDir, "a.md"))
	if string(got) != "harness version" {
		t.Errorf("pack file should be overwritten, got %q", got)
	}
}

func TestRunToPack_ExistingPack_Conflict_DryRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	writeSaveTestManifest(t, packRoot, packName)

	rulesDir := filepath.Join(packRoot, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "a.md"), []byte("pack version"), 0o644); err != nil {
		t.Fatal(err)
	}

	harnessFile := filepath.Join(home, "harness", "rules", "a.md")
	if err := os.MkdirAll(filepath.Dir(harnessFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessFile, []byte("harness version"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: packName, Digest: "old-digest"},
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

	result, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
		DryRun:    true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("expected 1 conflict, got %d", len(result.Conflicts))
	}

	// Verify file was NOT written.
	got, _ := os.ReadFile(filepath.Join(rulesDir, "a.md"))
	if string(got) != "pack version" {
		t.Errorf("pack file should be unchanged in dry-run, got %q", got)
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
	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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

	_, err := RunRoundTrip(RoundTripRequest{
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

	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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

	result, err := RunRoundTrip(RoundTripRequest{
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

func TestRunToPack_ExistingPack_AgentUsesTypedNeutralBytes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	writeSaveTestManifest(t, packRoot, packName)

	harnessFile := filepath.Join(home, "harness", "agents", "reviewer.md")
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
	if err := os.MkdirAll(filepath.Join(packRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "agents", "reviewer.md"), []byte(neutral), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		harnessFile: {SourcePack: packName, Digest: "old-digest"},
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

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"opencode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", result.Conflicts)
	}
	got, err := os.ReadFile(filepath.Join(packRoot, "agents", "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != neutral {
		t.Fatalf("pack file = %q, want neutral %q (ToPack)", got, neutral)
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

	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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

	result, err := RunRoundTrip(RoundTripRequest{
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

func TestRunToPack_DirSave_PropagatesDeletions(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packName := "test-pack"
	packRoot := filepath.Join(configDir, "packs", packName)

	writeSaveTestManifest(t, packRoot, packName)

	// Pre-populate pack skill dir with an extra file that no longer exists in source.
	packSkillDir := filepath.Join(packRoot, "skills", "my-skill")
	if err := os.MkdirAll(packSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packSkillDir, "extra.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source skill dir only has SKILL.md (extra.md was deleted).
	srcSkillDir := filepath.Join(home, "harness", "skills", "my-skill")
	if err := os.MkdirAll(srcSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkillDir, "SKILL.md"), []byte("skill updated"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(home, "project", ".aipack", "ledger.json")
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		srcSkillDir: {SourcePack: packName, Digest: "old-digest"},
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

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := RunToPack(ToPackRequest{
		TargetSpec: TargetSpec{
			Scope:      "project",
			ProjectDir: filepath.Join(home, "project"),
			Harnesses:  []domain.Harness{"claudecode"},
			Home:       home,
		},
		PackName:  packName,
		ConfigDir: configDir,
		Force:     true,
	}, reg)
	if err != nil {
		t.Fatal(err)
	}

	// SKILL.md should be updated.
	got, err := os.ReadFile(filepath.Join(packSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "skill updated" {
		t.Errorf("SKILL.md = %q, want %q", got, "skill updated")
	}

	// extra.md should have been removed — it no longer exists in source.
	if _, err := os.Stat(filepath.Join(packSkillDir, "extra.md")); !os.IsNotExist(err) {
		t.Error("extra.md should have been deleted from pack dir, but still exists")
	}
}

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

	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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

	result, err := RunRoundTrip(RoundTripRequest{
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
// Finding #2: Snapshot manifests omit Claude Code settings
// ---------------------------------------------------------------------------

func TestBuildPackManifest_DetectsClaudeCodeSettings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create Claude Code settings config.
	ccDir := filepath.Join(dir, "configs", "claudecode")
	if err := os.MkdirAll(ccDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ccDir, "settings.local.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := buildPackManifest(dir, "1.0.0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ccSettings, ok := manifest.Configs.HarnessSettings["claudecode"]
	if !ok {
		t.Fatal("expected claudecode in HarnessSettings, not found")
	}
	if len(ccSettings) != 1 || ccSettings[0] != "settings.local.json" {
		t.Errorf("claudecode HarnessSettings = %v, want [settings.local.json]", ccSettings)
	}
}

// ---------------------------------------------------------------------------
// Finding #4: Forced settings saves don't advance ledger
// ---------------------------------------------------------------------------

func TestRunRoundTrip_SettingsSave_AdvancesLedger(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
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

	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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

	result, err := RunRoundTrip(RoundTripRequest{
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

	// The settings change should be detected.
	if len(result.PendingSettings) == 0 {
		t.Fatal("expected at least 1 pending settings change")
	}

	// Reload ledger — the digest should have been advanced when PendingSettings
	// were recorded, so a second run reports unchanged.
	lg, _, lErr := engine.LoadLedger(ledgerPath)
	if lErr != nil {
		t.Fatal(lErr)
	}
	entry, ok := lg.Managed[settingsFile]
	if !ok {
		t.Fatal("expected ledger entry for settings file")
	}
	newDigest := domain.SingleFileDigest(newContent)
	if entry.Digest != newDigest {
		t.Errorf("ledger digest not advanced: got %q, want %q", entry.Digest, newDigest)
	}

	// A second round-trip with same content should report unchanged.
	result2, err := RunRoundTrip(RoundTripRequest{
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
		t.Errorf("second round-trip should report no pending settings, got %d", len(result2.PendingSettings))
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
	ledgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")
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
	result, err := RunRoundTrip(RoundTripRequest{
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
