package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
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

	ar := ApplyRequest{Quiet: true, Stderr: io.Discard}
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
		Quiet:  true,
		Stderr: io.Discard,
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
	ar := ApplyRequest{Quiet: true, Stderr: io.Discard}
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
		Quiet:  true,
		Stderr: io.Discard,
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

	ar := ApplyRequest{Quiet: true, Stderr: io.Discard}
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
		Quiet:  true,
		Stderr: io.Discard,
	}
	if _, err := engConfirm.ApplyPlan(context.Background(), plan2, ar2, []string{dir}); err != nil {
		t.Fatal(err)
	}

	// File should be deleted.
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("user-modified file should be deleted when user confirms")
	}
}
