package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

// ---------------------------------------------------------------------------
// ClassifyFileKind tests
// ---------------------------------------------------------------------------

func TestClassifyFileKind_Create(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "missing.md")
	lg := domain.NewLedger()

	kind, err := ClassifyFileKind(dst, []byte("content"), lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffCreate {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffCreate)
	}
}

func TestClassifyFileKind_Identical(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "same.md")
	content := []byte("hello\n")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()

	kind, err := ClassifyFileKind(dst, content, lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffIdentical)
	}
}

func TestClassifyFileKind_Managed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "managed.md")
	oldContent := []byte("old\n")
	newContent := []byte("new\n")
	if err := os.WriteFile(dst, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(dst, oldContent, "pack1", nil, time.Now())

	kind, err := ClassifyFileKind(dst, newContent, lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffManaged {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffManaged)
	}
}

func TestClassifyFileKind_Conflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "conflict.md")
	origContent := []byte("original\n")
	userModified := []byte("user edited\n")
	newContent := []byte("new pack content\n")
	if err := os.WriteFile(dst, origContent, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(dst, origContent, "pack1", nil, time.Now())
	if err := os.WriteFile(dst, userModified, 0o644); err != nil {
		t.Fatal(err)
	}

	kind, err := ClassifyFileKind(dst, newContent, lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffConflict {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffConflict)
	}
}

// ---------------------------------------------------------------------------
// ClassifyFile tests
// ---------------------------------------------------------------------------

func TestClassifyFile_Create(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "new.md")
	lg := domain.NewLedger()

	fd, err := ClassifyFile(dst, []byte("content"), "new.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffCreate {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffCreate)
	}
	if fd.SourcePack != "pack1" {
		t.Errorf("SourcePack = %q", fd.SourcePack)
	}
}

func TestClassifyFile_Identical(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "same.md")
	content := []byte("hello world\n")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()

	fd, err := ClassifyFile(dst, content, "same.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffIdentical)
	}
}

func TestClassifyFile_Managed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "managed.md")
	oldContent := []byte("old content\n")
	newContent := []byte("new content\n")

	if err := os.WriteFile(dst, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Record the old content in the ledger.
	lg := domain.NewLedger()
	lg.Record(dst, oldContent, "pack1", nil, time.Now())

	fd, err := ClassifyFile(dst, newContent, "managed.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffManaged {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffManaged)
	}
	if fd.Diff == "" {
		t.Error("Diff should be non-empty for managed files")
	}
}

func TestClassifyFile_Conflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "conflict.md")
	origContent := []byte("original\n")
	userModified := []byte("user modified\n")
	newContent := []byte("new pack content\n")

	// Write original and record in ledger.
	if err := os.WriteFile(dst, origContent, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(dst, origContent, "pack1", nil, time.Now())

	// Simulate user editing the file.
	if err := os.WriteFile(dst, userModified, 0o644); err != nil {
		t.Fatal(err)
	}

	fd, err := ClassifyFile(dst, newContent, "conflict.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffConflict {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffConflict)
	}
}

func TestClassifyCopy_Dir(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "a.md"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(src, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "b.md"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	lg := domain.NewLedger()
	diffs, err := ClassifyCopy(src, dst, "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 2 {
		t.Fatalf("diffs = %d, want 2", len(diffs))
	}
	// All should be create since dst is empty.
	for _, d := range diffs {
		if d.Kind != domain.DiffCreate {
			t.Errorf("diff %q Kind = %q, want %q", d.Dst, d.Kind, domain.DiffCreate)
		}
	}
}

func TestUnifiedDiff_Basic(t *testing.T) {
	t.Parallel()
	a := []byte("line1\nline2\nline3\n")
	b := []byte("line1\nchanged\nline3\n")

	diff := UnifiedDiff(a, b, "a.txt", "b.txt")
	if diff == "" {
		t.Error("diff should be non-empty")
	}
	if !contains(diff, "-line2") || !contains(diff, "+changed") {
		t.Errorf("diff doesn't contain expected changes:\n%s", diff)
	}
}

func TestUnifiedDiff_Identical(t *testing.T) {
	t.Parallel()
	a := []byte("same\n")
	diff := UnifiedDiff(a, a, "a", "b")
	if diff != "" {
		t.Errorf("diff should be empty for identical content, got:\n%s", diff)
	}
}

func TestComputeSettingsDiffs_MergeMode_NonExistentFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "nonexistent-settings.json")
	lg := domain.NewLedger()

	settings := []domain.SettingsAction{
		{
			Dst:       dst,
			Desired:   []byte(`{"key": "value"}`),
			Harness:   domain.HarnessClaudeCode,
			Label:     "test-settings",
			MergeMode: true,
		},
	}
	diffs, err := ComputeSettingsDiffs(settings, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	if diffs[0].Kind != domain.DiffCreate {
		t.Errorf("Kind = %q, want %q (file doesn't exist)", diffs[0].Kind, domain.DiffCreate)
	}
}

func TestComputeSettingsDiffs_MergeMode_EmptyDesired_NonExistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "nonexistent.json")
	lg := domain.NewLedger()

	settings := []domain.SettingsAction{
		{
			Dst:       dst,
			Desired:   []byte{},
			Harness:   domain.HarnessClaudeCode,
			Label:     "empty-settings",
			MergeMode: true,
		},
	}
	diffs, err := ComputeSettingsDiffs(settings, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	// Empty desired + non-existent file should be create, not identical.
	if diffs[0].Kind != domain.DiffCreate {
		t.Errorf("Kind = %q, want %q", diffs[0].Kind, domain.DiffCreate)
	}
}

func TestComputeSettingsDiffs_MergeMode_NoOpsIsIdentical(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")

	// On-disk file has keys in a specific order (simulates harness-reformatted file).
	onDisk := []byte(`{
  "userState": "active",
  "mcpServers": {
    "svc": {
      "command": "npx",
      "args": ["svc-mcp"]
    }
  },
  "anotherKey": true
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	// Managed overlay is just mcpServers — same values as on-disk.
	managed := []byte(`{
  "mcpServers": {
    "svc": {
      "command": "npx",
      "args": ["svc-mcp"]
    }
  }
}
`)

	lg := domain.NewLedger()
	// Simulate previous sync having recorded this file with the managed overlay.
	lg.Record(dst, onDisk, "test", managed, time.Now())

	diffs, err := ComputeSettingsDiffs([]domain.SettingsAction{
		{Dst: dst, Desired: managed, Harness: domain.HarnessClaudeCode, Label: "test", MergeMode: true},
	}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	if diffs[0].Kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q — merge should be no-op when managed keys match on-disk", diffs[0].Kind, domain.DiffIdentical)
	}
	// Desired should be the on-disk content, not re-sorted.
	if !bytes.Equal(diffs[0].Desired, onDisk) {
		t.Error("Desired should be on-disk content when merge is no-op")
	}
}

func TestComputeSettingsDiffs_MergeMode_ReformattedFileIsIdentical(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")

	// Simulates a harness that rewrote the file after sync (different key order,
	// new keys added) but managed keys are unchanged.
	onDisk := []byte(`{
  "zeta": "added-by-harness",
  "mcpServers": {
    "svc": {
      "command": "npx",
      "args": ["svc-mcp"]
    }
  },
  "alpha": 42
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []byte(`{
  "mcpServers": {
    "svc": {
      "command": "npx",
      "args": ["svc-mcp"]
    }
  }
}
`)

	// Previous sync recorded a DIFFERENT file content (before harness reformatted).
	prevContent := []byte(`{
  "alpha": 42,
  "mcpServers": {
    "svc": {
      "command": "npx",
      "args": ["svc-mcp"]
    }
  }
}
`)
	lg := domain.NewLedger()
	lg.Record(dst, prevContent, "test", managed, time.Now())

	diffs, err := ComputeSettingsDiffs([]domain.SettingsAction{
		{Dst: dst, Desired: managed, Harness: domain.HarnessClaudeCode, Label: "test", MergeMode: true},
	}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	// Even though the full file digest changed (harness reformatted + added keys),
	// managed keys are unchanged, so this should be identical.
	if diffs[0].Kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q — harness reformatting should not trigger update", diffs[0].Kind, domain.DiffIdentical)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
