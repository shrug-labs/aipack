package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestShouldDelete_DigestMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	content := []byte("managed content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := domain.SingleFileDigest(content)

	eng := New(nil, nil)
	dec, err := eng.shouldDelete(context.Background(), path, false, digest, false)
	if err != nil {
		t.Fatal(err)
	}
	if dec != DeleteYes {
		t.Errorf("should delete when digest matches, got %v", dec)
	}
}

func TestShouldDelete_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := New(nil, nil)
	dec, err := eng.shouldDelete(context.Background(), path, false, "stale-digest", true)
	if err != nil {
		t.Fatal(err)
	}
	if dec != DeleteYes {
		t.Errorf("dry-run should return DeleteYes, got %v", dec)
	}
}

func TestShouldDelete_YesFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := New(nil, nil)
	dec, err := eng.shouldDelete(context.Background(), path, true, "stale-digest", false)
	if err != nil {
		t.Fatal(err)
	}
	if dec != DeleteYes {
		t.Errorf("--yes flag should return DeleteYes, got %v", dec)
	}
}

func TestShouldDelete_NonInteractive_ReturnsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// nil Interactor = non-interactive
	eng := New(nil, nil)
	eng.Interact = nil
	dec, err := eng.shouldDelete(context.Background(), path, false, "stale-digest", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec != DeleteSkippedNonInteractive {
		t.Errorf("nil interactor should return DeleteSkippedNonInteractive, got %v", dec)
	}

	// Interactor that reports non-terminal
	eng2 := New(nil, &FixedInteractor{Terminal: false})
	dec, err = eng2.shouldDelete(context.Background(), path, false, "stale-digest", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec != DeleteSkippedNonInteractive {
		t.Errorf("non-terminal interactor should return DeleteSkippedNonInteractive, got %v", dec)
	}
}

func TestShouldDelete_Interactive_UserConfirms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := New(nil, &FixedInteractor{Terminal: true, Responses: []string{"y"}})
	dec, err := eng.shouldDelete(context.Background(), path, false, "stale-digest", false)
	if err != nil {
		t.Fatal(err)
	}
	if dec != DeleteYes {
		t.Errorf("user said 'y', expected DeleteYes, got %v", dec)
	}
}

func TestShouldDelete_Interactive_UserDeclines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := New(nil, &FixedInteractor{Terminal: true, Responses: []string{"n"}})
	dec, err := eng.shouldDelete(context.Background(), path, false, "stale-digest", false)
	if err != nil {
		t.Fatal(err)
	}
	if dec != DeleteNo {
		t.Errorf("user said 'n', expected DeleteNo, got %v", dec)
	}
}

func TestShouldDelete_Interactive_ContextCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Use an Interactor whose Prompt respects context cancellation.
	eng := New(nil, &contextAwareInteractor{ctx: ctx})
	_, err := eng.shouldDelete(ctx, path, false, "stale-digest", false)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// contextAwareInteractor reports as terminal but returns context error on Prompt.
type contextAwareInteractor struct{ ctx context.Context }

func (c *contextAwareInteractor) IsTerminal() bool { return true }
func (c *contextAwareInteractor) Prompt(ctx context.Context, _ string) (string, error) {
	return "", ctx.Err()
}

func TestReconcileStaleEntries_NonInteractive_WarnsForUserModifiedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	fileA := filepath.Join(dir, "rules", "a.md")
	fileB := filepath.Join(dir, "rules", "b.md")
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: fileA, Content: []byte("content-a"), SourcePack: "pack1"},
		{Dst: fileB, Content: []byte("content-b"), SourcePack: "pack1"},
	})

	ar := ApplyRequest{Quiet: true}
	if _, err := eng.ApplyPlan(context.Background(), plan, ar, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// User modifies both files.
	if err := os.WriteFile(fileA, []byte("edited-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("edited-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second sync: both gone from plan, non-interactive (no --yes).
	// Use a non-interactive engine for this sync.
	engNonInteractive := New(nil, &FixedInteractor{Terminal: false})
	plan2 := buildPlan(dir, nil)
	plan2.Ledger = plan.Ledger
	ar2 := ApplyRequest{
		Quiet: true,
	}
	warnings, err := engNonInteractive.ApplyPlan(context.Background(), plan2, ar2, []string{dir})
	if err != nil {
		t.Fatal(err)
	}

	// Both files should be preserved — non-interactive can't confirm deletion.
	if _, err := os.Stat(fileA); err != nil {
		t.Errorf("fileA should be preserved: %v", err)
	}
	if _, err := os.Stat(fileB); err != nil {
		t.Errorf("fileB should be preserved: %v", err)
	}

	// Should see warnings about the non-interactive skips.
	skippedCount := 0
	for _, w := range warnings {
		if w.Field == "stale" {
			skippedCount++
		}
	}
	if skippedCount < 2 {
		t.Errorf("expected at least 2 stale warnings for non-interactive skips, got %d", skippedCount)
	}
}

func TestReconcileStaleEntries_UserModifiedFilePreserved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	file := filepath.Join(dir, "rules", "a.md")
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: []byte("original"), SourcePack: "pack1"},
	})

	// First sync: create the file and ledger entry.
	ar := ApplyRequest{Quiet: true}
	if _, err := eng.ApplyPlan(context.Background(), plan, ar, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// User modifies the file.
	if err := os.WriteFile(file, []byte("user edited this"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second sync: file is no longer in plan. Interactor says "n".
	engDecline := New(nil, &FixedInteractor{Terminal: true, Responses: []string{"n"}})
	plan2 := buildPlan(dir, nil)
	plan2.Ledger = plan.Ledger
	ar2 := ApplyRequest{
		Quiet: true,
	}
	if _, err := engDecline.ApplyPlan(context.Background(), plan2, ar2, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// File should still exist.
	if _, err := os.Stat(file); err != nil {
		t.Errorf("user-modified file should be preserved when user declines: %v", err)
	}
}

func TestReconcileStaleEntries_UserModifiedFileDeleted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	file := filepath.Join(dir, "rules", "a.md")
	plan := buildPlan(dir, []domain.WriteAction{
		{Dst: file, Content: []byte("original"), SourcePack: "pack1"},
	})

	ar := ApplyRequest{Quiet: true}
	if _, err := eng.ApplyPlan(context.Background(), plan, ar, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// User modifies the file.
	if err := os.WriteFile(file, []byte("user edited this"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second sync: Interactor says "yes" — user confirms deletion.
	engConfirm := New(nil, &FixedInteractor{Terminal: true, Responses: []string{"yes"}})
	plan2 := buildPlan(dir, nil)
	plan2.Ledger = plan.Ledger
	ar2 := ApplyRequest{
		Quiet: true,
	}
	if _, err := engConfirm.ApplyPlan(context.Background(), plan2, ar2, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// File should be deleted.
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("user-modified file should be deleted when user confirms")
	}
}

func TestReconcileStaleEntries_OwnedFileStrippedNotDeleted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	// Simulate .claude.json with user keys + managed mcpServers.
	settingsFile := filepath.Join(dir, ".claude.json")
	initial := []byte(`{
  "verbose": true,
  "mcpServers": {
    "test-server": {"command": "test-mcp"}
  },
  "userPref": "keep-me"
}`)
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsFile, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	// First sync: record the settings file in the ledger via a settings action.
	plan := domain.Plan{
		Writes:  nil,
		Desired: map[string]struct{}{filepath.Clean(settingsFile): {}},
		Ledger:  filepath.Join(dir, ".aipack", "ledger.json"),
		Settings: []domain.SettingsAction{{
			Dst:       settingsFile,
			Desired:   initial,
			Harness:   domain.HarnessClaudeCode,
			Label:     ".claude.json",
			MergeMode: true,
		}},
	}
	ar := ApplyRequest{Quiet: true}
	if _, err := eng.ApplyPlan(context.Background(), plan, ar, []string{dir, settingsFile}); err != nil {
		t.Fatal(err)
	}

	// Second sync: settings file is NOT in desired (all MCP packs disabled).
	plan2 := domain.Plan{
		Desired: map[string]struct{}{},
		Ledger:  plan.Ledger,
	}

	// Provide a strip function that removes only mcpServers.
	stripFn := func(content []byte, _ domain.Ledger) ([]byte, error) {
		// Use the same pattern as claudecode harness: parse, strip, re-serialize.
		var m map[string]any
		if err := json.Unmarshal(content, &m); err != nil {
			return nil, err
		}
		m["mcpServers"] = map[string]any{}
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}

	ar2 := ApplyRequest{
		Yes:   true,
		Quiet: true,
		StripFuncs: map[string]func([]byte, domain.Ledger) ([]byte, error){
			filepath.Clean(settingsFile): stripFn,
		},
	}
	if _, err := eng.ApplyPlan(context.Background(), plan2, ar2, []string{dir, settingsFile}); err != nil {
		t.Fatal(err)
	}

	// File must still exist.
	got, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("owned file should NOT be deleted: %v", err)
	}

	// Verify managed keys were stripped and user keys preserved.
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("invalid JSON after strip: %v", err)
	}
	if v, ok := result["verbose"]; !ok || v != true {
		t.Error("user key 'verbose' should be preserved")
	}
	if v, ok := result["userPref"]; !ok || v != "keep-me" {
		t.Error("user key 'userPref' should be preserved")
	}
	servers, _ := result["mcpServers"].(map[string]any)
	if len(servers) != 0 {
		t.Errorf("mcpServers should be empty after strip, got %v", servers)
	}

	// Ledger entry should be removed.
	lg, _, err := eng.LoadLedger(plan.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := lg.Managed[filepath.Clean(settingsFile)]; exists {
		t.Error("ledger entry should be removed after stripping")
	}
}

func TestReconcileStaleEntries_OwnedFile_PrunesOnlyManagedMCPServers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	settingsFile := filepath.Join(dir, ".claude.json")
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	initial := []byte(`{"mcpServers":{"foo":{},"bar":{}}}`)
	plan := domain.Plan{
		Desired: map[string]struct{}{filepath.Clean(settingsFile): {}},
		Ledger:  ledgerPath,
		Settings: []domain.SettingsAction{{
			Dst: settingsFile, Desired: initial, Harness: domain.HarnessClaudeCode,
			Label: ".claude.json", MergeMode: true,
		}},
		MCPServers: []domain.MCPAction{
			{Name: "foo", ConfigPath: settingsFile, Content: []byte(`{}`), Harness: domain.HarnessClaudeCode},
			{Name: "bar", ConfigPath: settingsFile, Content: []byte(`{}`), Harness: domain.HarnessClaudeCode},
		},
	}
	if _, err := eng.ApplyPlan(context.Background(), plan, ApplyRequest{Quiet: true}, []string{dir, settingsFile}); err != nil {
		t.Fatal(err)
	}

	// Dropping managed MCP entries must preserve user-added servers.
	userEdited := []byte(`{"mcpServers":{"foo":{},"bar":{},"mine":{"command":"x"}}}`)
	if err := os.WriteFile(settingsFile, userEdited, 0o644); err != nil {
		t.Fatal(err)
	}

	plan2 := domain.Plan{Desired: map[string]struct{}{}, Ledger: ledgerPath}

	stripFn := func(content []byte, lg domain.Ledger) ([]byte, error) {
		names := domain.MCPServerNamesForPath(lg.Managed, settingsFile)
		var m map[string]any
		if err := json.Unmarshal(content, &m); err != nil {
			return nil, err
		}
		if servers, ok := m["mcpServers"].(map[string]any); ok {
			for n := range names {
				delete(servers, n)
			}
		}
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}
	ar2 := ApplyRequest{
		Yes:   true,
		Quiet: true,
		StripFuncs: map[string]func([]byte, domain.Ledger) ([]byte, error){
			filepath.Clean(settingsFile): stripFn,
		},
	}
	if _, err := eng.ApplyPlan(context.Background(), plan2, ar2, []string{dir, settingsFile}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("owned file should NOT be deleted: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("invalid JSON after strip: %v", err)
	}
	servers, ok := result["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers should be preserved for the user's server, got %v", result)
	}
	if _, ok := servers["foo"]; ok {
		t.Error("managed server foo should be pruned")
	}
	if _, ok := servers["bar"]; ok {
		t.Error("managed server bar should be pruned")
	}
	if _, ok := servers["mine"]; !ok {
		t.Error("user-added server mine MUST be preserved (regression: the blunt strip wiped it)")
	}
}

func TestReconcileStaleEntries_OwnedFileStripError_WarnsNotDeletes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eng := New(nil, nil)

	settingsFile := filepath.Join(dir, ".claude.json")
	content := []byte(`{"mcpServers": {"x": {}}, "keep": true}`)
	if err := os.WriteFile(settingsFile, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Record in ledger.
	plan := domain.Plan{
		Desired:  map[string]struct{}{filepath.Clean(settingsFile): {}},
		Ledger:   filepath.Join(dir, ".aipack", "ledger.json"),
		Settings: []domain.SettingsAction{{Dst: settingsFile, Desired: content, Harness: domain.HarnessClaudeCode, Label: ".claude.json", MergeMode: true}},
	}
	ar := ApplyRequest{Quiet: true}
	if _, err := eng.ApplyPlan(context.Background(), plan, ar, []string{dir, settingsFile}); err != nil {
		t.Fatal(err)
	}

	// Second sync with a strip function that always errors.
	plan2 := domain.Plan{Desired: map[string]struct{}{}, Ledger: plan.Ledger}
	ar2 := ApplyRequest{
		Yes:   true,
		Quiet: true,
		StripFuncs: map[string]func([]byte, domain.Ledger) ([]byte, error){
			filepath.Clean(settingsFile): func([]byte, domain.Ledger) ([]byte, error) {
				return nil, fmt.Errorf("simulated strip error")
			},
		},
	}
	warnings, err := eng.ApplyPlan(context.Background(), plan2, ar2, []string{dir, settingsFile})
	if err != nil {
		t.Fatal(err)
	}

	// File must still exist (not deleted on strip error).
	if _, err := os.Stat(settingsFile); err != nil {
		t.Errorf("file should be preserved on strip error: %v", err)
	}

	// Should have a warning about the strip error.
	found := false
	for _, w := range warnings {
		if w.Field == "stale" && strings.Contains(w.Message, "strip managed keys") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about strip failure, got: %v", warnings)
	}
}
