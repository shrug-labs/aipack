package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

// buildPlan constructs a Plan from writes, wiring Desired and the ledger path.
func buildPlan(dir string, writes []domain.WriteAction) domain.Plan {
	p := domain.Plan{
		Writes:  writes,
		Desired: map[string]struct{}{},
		Ledger:  filepath.Join(dir, ".aipack", "ledger.json"),
	}
	for _, w := range writes {
		p.Desired[filepath.Clean(w.Dst)] = struct{}{}
	}
	return p
}

func TestApplyPlan_CreateFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fileA := filepath.Join(dir, "rules", "alpha.md")
	fileB := filepath.Join(dir, "rules", "beta.md")

	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("alpha content"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("beta content"), SourcePack: "pack1"},
	})

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	// Verify files created with correct content.
	gotA, err := os.ReadFile(fileA)
	if err != nil {
		t.Fatalf("ReadFile alpha: %v", err)
	}
	if string(gotA) != "alpha content" {
		t.Errorf("alpha = %q, want %q", gotA, "alpha content")
	}

	gotB, err := os.ReadFile(fileB)
	if err != nil {
		t.Fatalf("ReadFile beta: %v", err)
	}
	if string(gotB) != "beta content" {
		t.Errorf("beta = %q, want %q", gotB, "beta content")
	}

	// Verify ledger has 2 entries.
	lg, _, err := LoadLedger(plan.Ledger)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(lg.Managed) != 2 {
		t.Errorf("ledger entries = %d, want 2", len(lg.Managed))
	}
	if _, ok := lg.Managed[fileA]; !ok {
		t.Errorf("ledger missing %s", fileA)
	}
	if _, ok := lg.Managed[fileB]; !ok {
		t.Errorf("ledger missing %s", fileB)
	}
}

func TestApplyPlan_IdenticalSkipsWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "rules", "same.md")
	content := []byte("identical content")

	// First apply to create the file and ledger entry.
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: content, SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Second apply with identical content should succeed without error.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: content, SourcePack: "pack1"},
	})
	if err := ApplyPlan(plan2, ar, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	// Verify file still has correct content.
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	// Verify ledger still has entry.
	lg, _, err := LoadLedger(plan2.Ledger)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if _, ok := lg.Managed[file]; !ok {
		t.Error("ledger missing entry after identical apply")
	}
}

// TestApplyPlan_IdenticalNoLedger verifies that when a file already exists on
// disk with correct content but has no ledger entry (e.g., fresh ledger after
// adding a harness to the sync scope), the apply still records it in the ledger.
func TestApplyPlan_IdenticalNoLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "skills", "my-skill", "SKILL.md")
	content := []byte("skill content")

	// Pre-create the file on disk (simulates a prior sync with a different ledger).
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply with a fresh ledger — file is DiffIdentical but untracked.
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: content, SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	lg, _, err := LoadLedger(plan.Ledger)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	entry, ok := lg.Managed[file]
	if !ok {
		t.Fatal("ledger missing entry for identical file with no prior ledger entry")
	}
	if entry.SourcePack != "pack1" {
		t.Errorf("SourcePack = %q, want %q", entry.SourcePack, "pack1")
	}
	if entry.Digest == "" {
		t.Error("Digest is empty")
	}
}

func TestApplyPlan_RecordsPerServerMCPLedgerEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	configPath := filepath.Join(dir, ".claude", "settings.local.json")
	configContent := []byte(`{"mcpServers":{"jira":{"command":"uvx","args":["jira-mcp"]}}}`)
	serverContent, err := domain.MCPInventoryBytes(domain.MCPServer{
		Name:      "jira",
		Transport: domain.TransportStdio,
		Command:   []string{"uvx", "jira-mcp"},
	})
	if err != nil {
		t.Fatalf("MCPInventoryBytes: %v", err)
	}

	plan := domain.Plan{
		MCP: []domain.SettingsAction{{
			Dst:        configPath,
			Desired:    configContent,
			Harness:    domain.HarnessClaudeCode,
			Label:      ".mcp.json",
			SourcePack: "pack1",
		}},
		MCPServers: []domain.MCPAction{{
			Name:       "jira",
			ConfigPath: configPath,
			Content:    serverContent,
			SourcePack: "pack1",
			Harness:    domain.HarnessClaudeCode,
		}},
		Desired: map[string]struct{}{
			filepath.Clean(configPath): {},
		},
		Ledger: filepath.Join(dir, ".aipack", "ledger.json"),
	}

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	lg, _, err := LoadLedger(plan.Ledger)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if _, ok := lg.Managed[filepath.Clean(configPath)]; !ok {
		t.Fatal("ledger missing MCP config entry")
	}
	entry, ok := lg.Managed[domain.MCPLedgerKey(configPath, "jira")]
	if !ok {
		t.Fatal("ledger missing per-server MCP entry")
	}
	if entry.SourcePack != "pack1" {
		t.Fatalf("SourcePack = %q, want pack1", entry.SourcePack)
	}
	if entry.Digest != domain.SingleFileDigest(serverContent) {
		t.Fatalf("Digest = %q, want %q", entry.Digest, domain.SingleFileDigest(serverContent))
	}
}

func TestApplyPlan_ManagedUpdates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "rules", "rule.md")
	oldContent := []byte("old content")
	newContent := []byte("new content")

	// First apply writes old content.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: oldContent, SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Verify old content written.
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(oldContent) {
		t.Fatalf("after first apply: %q, want %q", got, oldContent)
	}

	// Second apply with new content. File is managed (unchanged since last sync),
	// so it should be updated.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: newContent, SourcePack: "pack1"},
	})
	if err := ApplyPlan(plan2, ar, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	got, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newContent) {
		t.Errorf("after update: %q, want %q", got, newContent)
	}
}

func TestApplyPlan_ConflictSkipsWithoutForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "rules", "rule.md")
	originalContent := []byte("original")
	userEditContent := []byte("user edited this")
	desiredContent := []byte("new desired")

	// First apply writes original content and records in ledger.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: originalContent, SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Simulate user editing the file (creates a conflict).
	if err := os.WriteFile(file, userEditContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Second apply with new desired content, no Force.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: desiredContent, SourcePack: "pack1"},
	})
	if err := ApplyPlan(plan2, ar, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	// File should still contain user edits (conflict skipped).
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(userEditContent) {
		t.Errorf("content = %q, want %q (user edit preserved)", got, userEditContent)
	}
}

func TestApplyPlan_ConflictAppliesWithForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "rules", "rule.md")
	originalContent := []byte("original")
	userEditContent := []byte("user edited this")
	desiredContent := []byte("new desired")

	// First apply writes original content and records in ledger.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: originalContent, SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Simulate user editing the file.
	if err := os.WriteFile(file, userEditContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Second apply with Force=true should overwrite user edits.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: desiredContent, SourcePack: "pack1"},
	})
	ar2 := ApplyRequest{Force: true, Quiet: true}
	if err := ApplyPlan(plan2, ar2, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(desiredContent) {
		t.Errorf("content = %q, want %q (force applied)", got, desiredContent)
	}
}

func TestApplyPlan_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "rules", "alpha.md")
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: []byte("content"), SourcePack: "pack1"},
	})

	ar := ApplyRequest{DryRun: true, Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	// No files should be created.
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should not exist after dry run")
	}

	// No ledger file should be written.
	if _, err := os.Stat(plan.Ledger); !os.IsNotExist(err) {
		t.Error("ledger should not exist after dry run")
	}
}

func TestApplyPlan_CopyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create source file.
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(srcDir, "template.md")
	if err := os.WriteFile(srcFile, []byte("template content"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstFile := filepath.Join(dir, "output", "template.md")
	plan := domain.Plan{
		Copies: []domain.CopyAction{
			{Src: srcFile, Dst: dstFile, Kind: domain.CopyKindFile, SourcePack: "pack1"},
		},
		Desired: map[string]struct{}{
			filepath.Clean(dstFile): {},
		},
		Ledger: filepath.Join(dir, ".aipack", "ledger.json"),
	}

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	got, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "template content" {
		t.Errorf("content = %q, want %q", got, "template content")
	}
}

func TestApplyPlan_CopyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create source directory with 2 files.
	srcDir := filepath.Join(dir, "src", "skill")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("file a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.md"), []byte("file b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(dir, "output", "skill")
	plan := domain.Plan{
		Copies: []domain.CopyAction{
			{Src: srcDir, Dst: dstDir, Kind: domain.CopyKindDir, SourcePack: "pack1"},
		},
		Desired: map[string]struct{}{
			filepath.Clean(dstDir): {},
		},
		Ledger: filepath.Join(dir, ".aipack", "ledger.json"),
	}

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	// Verify both files copied.
	gotA, err := os.ReadFile(filepath.Join(dstDir, "a.md"))
	if err != nil {
		t.Fatalf("ReadFile a.md: %v", err)
	}
	if string(gotA) != "file a" {
		t.Errorf("a.md = %q, want %q", gotA, "file a")
	}

	gotB, err := os.ReadFile(filepath.Join(dstDir, "b.md"))
	if err != nil {
		t.Fatalf("ReadFile b.md: %v", err)
	}
	if string(gotB) != "file b" {
		t.Errorf("b.md = %q, want %q", gotB, "file b")
	}
}

// TestApplyPlan_CopyDirIdenticalNoLedger verifies that re-syncing a skill
// directory when destination files already exist but the ledger is fresh
// records all files in the ledger (not just the ones that changed).
func TestApplyPlan_CopyDirIdenticalNoLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	srcDir := filepath.Join(dir, "src", "skill")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(dir, "output", "skill")

	// Pre-create destination files (simulates prior sync with different ledger).
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := domain.Plan{
		Copies: []domain.CopyAction{
			{Src: srcDir, Dst: dstDir, Kind: domain.CopyKindDir, SourcePack: "pack1"},
		},
		Desired: map[string]struct{}{
			filepath.Clean(dstDir): {},
		},
		Ledger: filepath.Join(dir, ".aipack", "ledger.json"),
	}

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	lg, _, err := LoadLedger(plan.Ledger)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}

	// Both files must be tracked even though neither was written.
	for _, name := range []string{"SKILL.md", "extra.md"} {
		key := filepath.Join(dstDir, name)
		if _, ok := lg.Managed[key]; !ok {
			t.Errorf("ledger missing entry for %s", name)
		}
	}
}

func TestApplyPlan_PruneDeletesOrphaned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fileA := filepath.Join(dir, "rules", "alpha.md")
	fileB := filepath.Join(dir, "rules", "beta.md")

	// First apply creates both files.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("alpha"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("beta"), SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Verify both exist.
	if _, err := os.Stat(fileA); err != nil {
		t.Fatalf("fileA missing after first apply: %v", err)
	}
	if _, err := os.Stat(fileB); err != nil {
		t.Fatalf("fileB missing after first apply: %v", err)
	}

	// Second apply only has fileA. Prune=true, Yes=true should delete fileB.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("alpha"), SourcePack: "pack1"},
	})
	ar2 := ApplyRequest{Prune: true, Yes: true, Quiet: true}
	if err := ApplyPlan(plan2, ar2, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	// fileA should remain.
	if _, err := os.Stat(fileA); err != nil {
		t.Errorf("fileA should still exist: %v", err)
	}

	// fileB should be deleted.
	if _, err := os.Stat(fileB); !os.IsNotExist(err) {
		t.Error("fileB should be deleted after prune")
	}

	// Ledger should only have fileA.
	lg, _, err := LoadLedger(plan2.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lg.Managed[fileA]; !ok {
		t.Error("ledger should still have fileA")
	}
	if _, ok := lg.Managed[fileB]; ok {
		t.Error("ledger should not have fileB after prune")
	}
}

func TestApplyPlan_StaleLedgerEntriesReconciledWithoutPrune(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fileA := filepath.Join(dir, "rules", "alpha.md")
	fileB := filepath.Join(dir, "rules", "beta.md")

	// First apply creates both files.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("alpha"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("beta"), SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("first ApplyPlan: %v", err)
	}

	// Second apply only has fileA. Prune=false should leave fileB on disk
	// but remove its ledger entry so it stops showing as a prune candidate.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("alpha"), SourcePack: "pack1"},
	})
	ar2 := ApplyRequest{Prune: false, Quiet: true}
	if err := ApplyPlan(plan2, ar2, []string{dir}); err != nil {
		t.Fatalf("second ApplyPlan: %v", err)
	}

	// fileB should still exist on disk (not deleted without Prune).
	if _, err := os.Stat(fileB); err != nil {
		t.Errorf("fileB should still exist on disk: %v", err)
	}

	// Ledger should only have fileA — fileB's entry should be reconciled away.
	lg, _, err := LoadLedger(plan2.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lg.Managed[fileA]; !ok {
		t.Error("ledger should still have fileA")
	}
	if _, ok := lg.Managed[fileB]; ok {
		t.Error("ledger should not have fileB after reconciliation")
	}

	// PruneCandidatesWithLedger should report nothing (ledger is clean).
	candidates, err := PruneCandidatesWithLedger(plan2, []string{dir}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) > 0 {
		t.Errorf("expected no prune candidates after reconciliation, got %v", candidates)
	}
}

func TestApplyPlan_DestinationValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write destination is outside the managed root.
	outsidePath := filepath.Join(os.TempDir(), "outside-managed", "evil.md")
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: outsidePath, Content: []byte("bad"), SourcePack: "pack1"},
	})

	ar := ApplyRequest{Quiet: true}
	err := ApplyPlan(plan, ar, []string{dir})
	if err == nil {
		t.Fatal("expected error for write outside managed roots")
	}
	if !strings.Contains(err.Error(), "refusing to write outside managed roots") {
		t.Errorf("error = %q, want it to contain %q", err, "refusing to write outside managed roots")
	}
}

func TestPruneCandidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fileA := filepath.Join(dir, "rules", "a.md")
	fileB := filepath.Join(dir, "rules", "b.md")
	fileC := filepath.Join(dir, "rules", "c.md")

	// Apply a plan with all three files to create ledger entries.
	plan1 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("a"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("b"), SourcePack: "pack1"},
		{Dst: fileC, Content: []byte("c"), SourcePack: "pack1"},
	})
	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan1, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	// Verify all 3 files exist on disk.
	for _, f := range []string{fileA, fileB, fileC} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("file %s missing: %v", f, err)
		}
	}

	// New plan only has A and B in writes/desired. C should be a prune candidate.
	plan2 := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("a"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("b"), SourcePack: "pack1"},
	})

	candidates, err := PruneCandidates(plan2, []string{dir})
	if err != nil {
		t.Fatalf("PruneCandidates: %v", err)
	}

	sort.Strings(candidates)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %v, want exactly [%s]", candidates, fileC)
	}
	if candidates[0] != fileC {
		t.Errorf("candidate = %q, want %q", candidates[0], fileC)
	}
}

func TestApplyPlan_SettingsSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create a settings file.
	settingsPath := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"user_setting": true}` + "\n")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	plan := domain.Plan{
		Settings: []domain.SettingsAction{{
			Dst:     settingsPath,
			Desired: []byte(`{"managed": true}` + "\n"),
			Harness: domain.HarnessClaudeCode,
			Label:   "settings.local.json",
		}},
		Desired: map[string]struct{}{settingsPath: {}},
		Ledger:  filepath.Join(dir, ".aipack", "ledger.json"),
	}

	ar := ApplyRequest{Quiet: true}
	if err := ApplyPlan(plan, ar, []string{dir}); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}

	// Verify presync cache was created with original content.
	cached, err := os.ReadFile(filepath.Join(presyncDir(plan.Ledger), "claudecode--.claude--settings.local.json"))
	if err != nil {
		t.Fatalf("presync cache missing: %v", err)
	}
	if string(cached) != string(original) {
		t.Errorf("presync = %q, want %q", cached, original)
	}
}
