package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/testutil"
)

// ---------------------------------------------------------------------------
// classifyFileKind tests
// ---------------------------------------------------------------------------

func Test_classifyFileKind_Create(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "missing.md")
	lg := domain.NewLedger()

	kind, err := eng.classifyFileKind(dst, []byte("content"), lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffCreate {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffCreate)
	}
}

func Test_classifyFileKind_Identical(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "same.md")
	content := []byte("hello\n")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()

	kind, err := eng.classifyFileKind(dst, content, lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffIdentical)
	}
}

func Test_classifyFileKind_Managed(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "managed.md")
	oldContent := []byte("old\n")
	newContent := []byte("new\n")
	if err := os.WriteFile(dst, oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(dst, oldContent, "pack1", nil, time.Now())

	kind, err := eng.classifyFileKind(dst, newContent, lg)
	if err != nil {
		t.Fatal(err)
	}
	if kind != domain.DiffManaged {
		t.Errorf("Kind = %q, want %q", kind, domain.DiffManaged)
	}
}

func Test_classifyFileKind_Conflict(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
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

	kind, err := eng.classifyFileKind(dst, newContent, lg)
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
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "new.md")
	lg := domain.NewLedger()

	fd, err := eng.ClassifyFile(dst, []byte("content"), "new.md", "pack1", lg)
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

func TestClassifyWrite_ModeDriftIsManaged(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "hook")
	content := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()

	fd, err := eng.ClassifyWrite(domain.WriteAction{
		Dst:         dst,
		Content:     content,
		SourcePack:  "pack1",
		DesiredMode: 0o755,
	}, "hook", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffManaged {
		t.Fatalf("Kind = %q, want %q", fd.Kind, domain.DiffManaged)
	}
	if fd.DesiredMode != 0o755 {
		t.Fatalf("DesiredMode = %o, want 755", fd.DesiredMode)
	}
}

func TestMemFS_ReportsWrittenFileMode(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	dir := "/hooks"
	path := filepath.Join(dir, "PreToolUse")
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := fs.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("Stat mode = %o, want 755", got)
	}
	entries, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	entryInfo, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if got := entryInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("DirEntry mode = %o, want 755", got)
	}
}

func TestClassifyFile_Identical(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "same.md")
	content := []byte("hello world\n")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()

	fd, err := eng.ClassifyFile(dst, content, "same.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffIdentical {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffIdentical)
	}
}

func TestClassifyFile_Managed(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
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

	fd, err := eng.ClassifyFile(dst, newContent, "managed.md", "pack1", lg)
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
	eng := New(nil, nil)
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

	fd, err := eng.ClassifyFile(dst, newContent, "conflict.md", "pack1", lg)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Kind != domain.DiffConflict {
		t.Errorf("Kind = %q, want %q", fd.Kind, domain.DiffConflict)
	}
}

func TestClassifyCopy_Dir(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
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
	diffs, err := eng.ClassifyCopy(src, dst, "pack1", lg)
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

func TestClassifyCopy_RootDirectorySymlink(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)

	eng := New(nil, nil)
	root := t.TempDir()
	realSkill := filepath.Join(root, "skills", "linked-skill")
	if err := os.MkdirAll(filepath.Join(realSkill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "scripts", "run.sh"), []byte("run"), 0o644); err != nil {
		t.Fatal(err)
	}

	packSkills := filepath.Join(root, "pack", "skills")
	if err := os.MkdirAll(packSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(packSkills, "linked-skill")
	testutil.Symlink(t, "../../skills/linked-skill", link)

	dst := filepath.Join(t.TempDir(), ".agents", "skills", "linked-skill")
	diffs, err := eng.ClassifyCopy(link, dst, "pack1", domain.NewLedger())
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, d := range diffs {
		if d.Kind != domain.DiffCreate {
			t.Errorf("diff %q Kind = %q, want %q", d.Dst, d.Kind, domain.DiffCreate)
		}
		got[filepath.ToSlash(d.Dst)] = true
	}
	for _, want := range []string{
		filepath.ToSlash(filepath.Join(dst, "SKILL.md")),
		filepath.ToSlash(filepath.Join(dst, "scripts", "run.sh")),
	} {
		if !got[want] {
			t.Fatalf("missing diff for %s; got %v", want, got)
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
	eng := New(nil, nil)
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
	diffs, err := eng.ComputeSettingsDiffs(settings, lg)
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
	eng := New(nil, nil)
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
	diffs, err := eng.ComputeSettingsDiffs(settings, lg)
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
	eng := New(nil, nil)
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

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{
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
	eng := New(nil, nil)
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

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{
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

func TestComputeSettingsDiffs_MergeMode_RestoresDeletedManagedStringArrayItem(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")

	prevOnDisk := []byte(`{
  "permissions": {
    "allow": ["Bash(echo:*)", "Read(*)", "user-added"]
  }
}
`)
	onDisk := []byte(`{
  "permissions": {
    "allow": ["Bash(echo:*)", "user-added"]
  }
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []byte(`{
  "permissions": {
    "allow": ["Bash(echo:*)", "Read(*)"]
  }
}
`)
	lg := domain.NewLedger()
	lg.Record(dst, prevOnDisk, "permissions-pack", managed, time.Now())

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{
		{Dst: dst, Desired: managed, Harness: domain.HarnessClaudeCode, Label: "settings.json", MergeMode: true},
	}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	if diffs[0].Kind == domain.DiffIdentical {
		t.Fatalf("Kind = %q, want managed repair for deleted managed permission", diffs[0].Kind)
	}
	got := string(diffs[0].Desired)
	if !strings.Contains(got, `"Read(*)"`) {
		t.Fatalf("deleted managed permission should be restored, got:\n%s", got)
	}
	if !strings.Contains(got, `"user-added"`) {
		t.Fatalf("user-added permission should be preserved, got:\n%s", got)
	}
}

func TestComputeSettingsDiffs_MergeMode_RestoresDeletedManagedClaudeHookGroup(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.local.json")

	prevOnDisk := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "managed"}]},
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "user"}]}
    ]
  }
}
`)
	onDisk := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "user"}]}
    ]
  }
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "managed"}]}
    ]
  }
}
`)
	lg := domain.NewLedger()
	lg.Record(dst, prevOnDisk, "hooks-pack", managed, time.Now())

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{
		{Dst: dst, Desired: managed, Harness: domain.HarnessClaudeCode, Label: "settings.local.json", MergeMode: true},
	}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	if diffs[0].Kind == domain.DiffIdentical {
		t.Fatalf("Kind = %q, want managed repair for deleted managed hook", diffs[0].Kind)
	}
	got := string(diffs[0].Desired)
	if !strings.Contains(got, `"command": "managed"`) {
		t.Fatalf("deleted managed hook group should be restored, got:\n%s", got)
	}
	if !strings.Contains(got, `"command": "user"`) {
		t.Fatalf("user hook group should be preserved, got:\n%s", got)
	}
}

func TestComputeSettingsDiffs_MergeMode_RemovesStaleMCPServer(t *testing.T) {
	// Lifecycle gap #1 from aipack-honest-gaps-roadmap.md:
	// "Stale MCP server definitions survive a clean sync."
	// Reproduce: on-disk has mcpServers.acme + user-added mcpServers.userTool;
	// previous overlay had mcpServers.acme; new overlay no longer has it.
	// Expected: acme deleted, userTool preserved.
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")

	onDisk := []byte(`{
  "mcpServers": {
    "acme": {
      "command": "npx",
      "args": ["acme-mcp"]
    },
    "userTool": {
      "command": "user-binary"
    }
  },
  "userPref": "keep-me"
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	prevManaged := []byte(`{
  "mcpServers": {
    "acme": {
      "command": "npx",
      "args": ["acme-mcp"]
    }
  }
}
`)

	// New plan no longer ships acme — pack was removed.
	nextManaged := []byte(`{
  "mcpServers": {}
}
`)

	lg := domain.NewLedger()
	lg.Record(dst, onDisk, "acme-pack", prevManaged, time.Now())

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{
		{Dst: dst, Desired: nextManaged, Harness: domain.HarnessClaudeCode, Label: "settings.json", MergeMode: true},
	}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	got := string(diffs[0].Desired)
	if strings.Contains(got, "acme") {
		t.Fatalf("stale MCP entry should be removed when prev overlay had it but new overlay does not, got:\n%s", got)
	}
	if !strings.Contains(got, "userTool") {
		t.Fatalf("user-added MCP entry should be preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "userPref") {
		t.Fatalf("unrelated user keys should be preserved, got:\n%s", got)
	}
}

func TestComputeSettingsDiffs_MergeModeLabelsMergedDesired(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "config.toml")

	onDisk := []byte(`
[mcp_servers.github]
enabled_tools = ["get_pr"]

[mcp_servers.github.tools.get_pr]
approval_mode = "approve"
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	prevManaged := []byte(`
[mcp_servers.github]
enabled_tools = ["get_pr"]
`)
	nextManaged := []byte(`
[mcp_servers.github]
enabled_tools = ["get_pr", "list_repos"]
`)
	lg := domain.NewLedger()
	lg.Record(dst, onDisk, "pack", prevManaged, time.Now())

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{{
		Dst: dst, Desired: nextManaged, Harness: domain.HarnessCodex, Label: "config.toml", MergeMode: true,
	}}, lg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	if !strings.Contains(diffs[0].Diff, "+++ config.toml (after merge)") {
		t.Fatalf("diff should label merged destination, got:\n%s", diffs[0].Diff)
	}
	if strings.Contains(diffs[0].Diff, "-approval_mode") {
		t.Fatalf("diff should not show semantically preserved approval_mode as removed, got:\n%s", diffs[0].Diff)
	}
}

func TestComputeSettingsDiffs_MergeMode_EmptyDesiredPreservesNestedUserKeys(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")

	onDisk := []byte(`{
  "permissions": {
    "allow": ["mcp__server__tool", "user_permission"],
    "deny": ["mcp__server__delete"]
  },
  "theme": "dark"
}
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}
	prevManaged := []byte(`{
  "permissions": {
    "allow": ["mcp__server__tool"],
    "deny": ["mcp__server__delete"]
  }
}
`)
	lg := domain.NewLedger()
	lg.Record(dst, onDisk, "pack", prevManaged, time.Now())

	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{{
		Dst:       dst,
		Desired:   []byte("{}\n"),
		Harness:   domain.HarnessClaudeCode,
		Label:     "settings.json",
		MergeMode: true,
	}}, lg)
	if err != nil {
		t.Fatalf("ComputeSettingsDiffs: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("got %d diffs, want 1", len(diffs))
	}
	got := string(diffs[0].Desired)
	if strings.Contains(got, "mcp__server__tool") || strings.Contains(got, "mcp__server__delete") {
		t.Fatalf("managed permission entries should be removed, got:\n%s", got)
	}
	if !strings.Contains(got, "user_permission") || !strings.Contains(got, `"theme": "dark"`) {
		t.Fatalf("nested user keys should be preserved, got:\n%s", got)
	}
}

func TestComputeSettingsDiffs_MergeMode_PreservesRemovedPluginEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "config.toml")
	onDisk := []byte("[plugins.\"linear@openai-curated\"]\nenabled = true\n\n[plugins.\"superpowers@openai-curated\"]\nenabled = true\n")
	if err := os.WriteFile(dst, onDisk, 0o600); err != nil {
		t.Fatal(err)
	}

	prev := []byte("[plugins.\"linear@openai-curated\"]\nenabled = true\n\n[plugins.\"superpowers@openai-curated\"]\nenabled = true\n")
	next := []byte("[plugins.\"superpowers@openai-curated\"]\nenabled = true\n")
	lg := domain.NewLedger()
	lg.Record(dst, onDisk, "pack", prev, time.Now())

	eng := New(nil, nil)
	diffs, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{{
		Dst:       dst,
		Desired:   next,
		Harness:   domain.HarnessCodex,
		Label:     "config.toml",
		MergeMode: true,
	}}, lg)
	if err != nil {
		t.Fatalf("ComputeSettingsDiffs: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs = %d, want 1", len(diffs))
	}
	got := string(diffs[0].Desired)
	if !strings.Contains(got, `[plugins."linear@openai-curated"]`) {
		t.Fatalf("removed plugin entry should be preserved for additive-only plugin sync, got:\n%s", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
