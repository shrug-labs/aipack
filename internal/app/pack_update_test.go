package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// env sets up configDir + sync-config for update tests.
type updateEnv struct {
	configDir string
	out       bytes.Buffer
}

func newUpdateEnv(t *testing.T) *updateEnv {
	t.Helper()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	return &updateEnv{configDir: configDir}
}

func (e *updateEnv) addLink(t *testing.T, name string) {
	t.Helper()
	packDir := t.TempDir()
	writePackManifest(t, packDir, name)
	e.out.Reset()
	PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: e.configDir, Link: true,
		NowFn: func() time.Time { return fixedNow },
	}, &e.out)
}

func (e *updateEnv) addClone(t *testing.T, name string, gitFn func(context.Context, ...string) error) {
	t.Helper()
	e.out.Reset()
	PackInstall(context.Background(), PackInstallRequest{
		URL: "https://github.com/example/" + name, ConfigDir: e.configDir,
		RunGitFn: gitFn, URLOKFn: func(context.Context, string) (bool, error) { return true, nil },
		NowFn: func() time.Time { return fixedNow }, GitHashFn: fakeHashFn(fakeHash1),
	}, &e.out)
}

func (e *updateEnv) update(t *testing.T, name string, opts ...func(*PackUpdateRequest)) ([]PackUpdateResult, error) {
	t.Helper()
	req := PackUpdateRequest{
		ConfigDir: e.configDir, Name: name,
		NowFn: func() time.Time { return fixedNow },
	}
	for _, o := range opts {
		o(&req)
	}
	e.out.Reset()
	// Keep stdout in the helper so line-shape assertions stay cheap.
	return PackUpdate(context.Background(), req, &e.out, nil)
}

func TestPackUpdate_Link_VerifiesTarget(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addLink(t, "test-pack")

	results, err := e.update(t, "test-pack")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}
}

// TestPackUpdate_Local_BackfillsResolvedInventory asserts that running
// pack update against a MethodLocal pack whose lockfile entry lacks a
// Resolved inventory backfills the baseline. MethodLocal packs take a
// bespoke early-return path (no clone, no buildUpToDateResult), so the
// backfill has to be wired explicitly on that branch.
func TestPackUpdate_Local_BackfillsResolvedInventory(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	// Write the pack directly into configDir/packs/ so PackInstall
	// detects the in-place case and records method = MethodLocal.
	packsDir := filepath.Join(e.configDir, "packs")
	packDir := filepath.Join(packsDir, "local-backfill")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "local-backfill")
	if err := os.WriteFile(filepath.Join(packDir, "rules", "rule-a.md"), []byte("# rule-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: e.configDir,
		NowFn: func() time.Time { return fixedNow },
	}, &e.out); err != nil {
		t.Fatalf("install: %v", err)
	}

	lfPath := config.LockfilePath(e.configDir)
	lf, err := config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["local-backfill"]
	if meta.Method != config.MethodLocal {
		t.Fatalf("precondition: method = %q, want %q", meta.Method, config.MethodLocal)
	}
	// Simulate pre-v0.22: clear Resolved so the update path has to backfill.
	meta.Resolved = nil
	lf.Packs["local-backfill"] = meta
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	results, err := e.update(t, "local-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}

	lf, err = config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	got := lf.Packs["local-backfill"]
	if got.Resolved == nil {
		t.Fatal("Resolved is still nil after update — MethodLocal backfill did not fire")
	}
	if !slices.Contains(got.Resolved.Rules, "rule-a") {
		t.Errorf("Resolved.Rules = %v, want to contain rule-a", got.Resolved.Rules)
	}
}

// TestPackUpdate_UpToDate_BackfillsResolvedInventory asserts that running
// pack update against an up-to-date pack whose lockfile entry lacks a
// Resolved inventory (simulating a pre-v0.22 install) backfills the
// baseline so drift detection and doctor broken_refs work going forward.
// Regression guard for finding #3 in
// projects/aipack-pack-update-bugs-2026-04-14.md (fast-path wire).
func TestPackUpdate_UpToDate_BackfillsResolvedInventory(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	// Build a source pack with real content so the inventory has
	// something to capture on backfill.
	packDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "backfill-test")
	if err := os.WriteFile(filepath.Join(packDir, "rules", "rule-a.md"), []byte("# rule-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Link install so update takes the MethodLink → buildUpToDateResult
	// branch (no clone, no re-extraction).
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: e.configDir, Link: true,
		NowFn: func() time.Time { return fixedNow },
	}, &e.out); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Simulate pre-v0.22: clear Resolved from the lockfile entry.
	lfPath := config.LockfilePath(e.configDir)
	lf, err := config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["backfill-test"]
	if meta.Resolved == nil {
		t.Fatal("precondition: install should have populated Resolved")
	}
	meta.Resolved = nil
	lf.Packs["backfill-test"] = meta
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatalf("SaveLockfile: %v", err)
	}

	results, err := e.update(t, "backfill-test")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}

	// Re-read the lockfile and assert Resolved is now populated.
	lf, err = config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatalf("LoadLockfile after update: %v", err)
	}
	got := lf.Packs["backfill-test"]
	if got.Resolved == nil {
		t.Fatal("Resolved is still nil after update — backfill did not fire")
	}
	if !slices.Contains(got.Resolved.Rules, "rule-a") {
		t.Errorf("Resolved.Rules = %v, want to contain rule-a", got.Resolved.Rules)
	}
}

// TestPackUpdate_NonSemverVersionTreatedAsLiteralRef verifies that
// --version "main" on update is dispatched as a literal ref to checkout
// rather than rejected as an invalid version. Parallel to the
// install-side test.
func TestPackUpdate_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "main"
		r.RunGitFn = func(context.Context, ...string) error { return nil }
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return nil, nil
		}
	})
	// Any error must not be an up-front "invalid version" rejection —
	// under the unified ref model, validation was removed and literal
	// refs flow through to the dispatch.
	if err != nil && strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version: %v", err)
	}
	// The update should dispatch through the literal-ref path rather than
	// short-circuiting on validation. A successful dispatch produces a
	// result entry (StatusUpdated or similar) even with a fake git runner.
	if len(results) == 0 && err == nil {
		t.Fatal("update produced no results and no error — dispatch did not run")
	}
}

func TestPackUpdate_Copy_ReCopies(t *testing.T) {
	t.Parallel()
	// Set up a source pack with a rule file.
	packDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "test-pack", Version: "1.0.0", Root: ".",
		Rules: []string{"test-rule"},
	}
	config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(packDir, "rules"), 0o700)
	os.WriteFile(filepath.Join(packDir, "rules", "test-rule.md"), []byte("old content\n"), 0o600)

	e := newUpdateEnv(t)
	PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: e.configDir,
		NowFn: func() time.Time { return fixedNow },
	}, &e.out)

	// Modify source.
	os.WriteFile(filepath.Join(packDir, "rules", "test-rule.md"), []byte("new content\n"), 0o600)

	results, _ := e.update(t, "test-pack")
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	got, _ := os.ReadFile(filepath.Join(e.configDir, "packs", "test-pack", "rules", "test-rule.md"))
	if string(got) != "new content\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestPackUpdate_Clone_ReclonesAndExtracts(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	cloned := false
	updateGit := func(_ context.Context, args ...string) error {
		if len(args) >= 1 && args[0] == "clone" {
			cloned = true
			dir := args[len(args)-1]
			writePackManifest(t, dir, "my-pack")
			os.MkdirAll(filepath.Join(dir, "rules"), 0o755)
			os.WriteFile(filepath.Join(dir, "rules", "updated.md"),
				[]byte("---\nname: updated\n---\nnew rule\n"), 0o644)
		}
		return nil
	}

	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = updateGit
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}
	if !cloned {
		t.Fatal("expected re-clone")
	}
	packDir := filepath.Join(e.configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(packDir, "rules", "updated.md")); err != nil {
		t.Fatal("updated.md missing")
	}
	if _, err := os.Stat(filepath.Join(packDir, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git should not exist")
	}
}

func TestPackUpdate_Clone_SubPath(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	// Install from subpath.
	installGit := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			packDir := filepath.Join(dir, "packs", "team")
			writePackManifest(t, packDir, "team-pack")
			os.MkdirAll(filepath.Join(packDir, "rules"), 0o755)
			os.WriteFile(filepath.Join(packDir, "rules", "v1-rule.md"),
				[]byte("---\nname: v1-rule\n---\nv1\n"), 0o644)
		}
		return nil
	}
	e.out.Reset()
	PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: e.configDir, RunGitFn: installGit,
		URLOKFn: func(context.Context, string) (bool, error) { return true, nil },
		NowFn:   func() time.Time { return fixedNow },
	}, &e.out)

	// Update — new content replaces old.
	packCloneCalls := 0
	updateGit := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" && !isBareClone(args) {
			packCloneCalls++
			dir := args[len(args)-1]
			packDir := filepath.Join(dir, "packs", "team")
			writePackManifest(t, packDir, "team-pack")
			os.MkdirAll(filepath.Join(packDir, "rules"), 0o755)
			os.WriteFile(filepath.Join(packDir, "rules", "v2-rule.md"),
				[]byte("---\nname: v2-rule\n---\nv2\n"), 0o644)
		}
		return nil
	}

	results, _ := e.update(t, "team-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = updateGit
		r.GitHashFn = fakeHashFn("newww")
	})
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}
	if packCloneCalls != 1 {
		t.Fatalf("pack clone calls = %d", packCloneCalls)
	}
	packDir := filepath.Join(e.configDir, "packs", "team-pack")
	if _, err := os.Stat(filepath.Join(packDir, "rules", "v2-rule.md")); err != nil {
		t.Fatal("v2-rule.md missing")
	}
	if _, err := os.Stat(filepath.Join(packDir, "rules", "v1-rule.md")); !os.IsNotExist(err) {
		t.Fatal("v1-rule.md should be gone")
	}
}

func TestPackUpdate_All(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addLink(t, "pack-a")
	e.addLink(t, "pack-b")

	results, err := e.update(t, "", func(r *PackUpdateRequest) { r.All = true; r.Name = "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

// TestPackUpdate_All_PreservesOrder pins the user-visible ordering contract
// of the parallel update loop: regardless of which goroutine finishes first,
// results[] must match os.ReadDir's alphabetical order. The race detector
// running over the normal suite catches any loose shared-state access in
// the parallel phase.
//
// Five packs against the production packUpdateConcurrency cap of 3 forces
// at least two packs to wait on the semaphore, so the test exercises the
// queue path even when individual mock-git calls return instantly.
//
// Note: stdout line order is NOT guaranteed in parallel mode. Workers write
// terminal lines through a shared mutex for line atomicity, not order
// preservation, so lines appear in completion order rather than alphabetical.
// This test asserts that every pack's terminal line appears, not their
// relative position.
func TestPackUpdate_All_PreservesOrder(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	// Five clone packs with distinct names. os.ReadDir returns these in
	// alphabetical order, so that's what results[] must match.
	names := []string{"pack-a", "pack-b", "pack-c", "pack-d", "pack-e"}
	installCount := len(names)
	for _, n := range names {
		e.addClone(t, n, fakeCloneGitFn(t, n))
	}

	updateGit := func(_ context.Context, args ...string) error {
		if len(args) >= 1 && args[0] == "clone" && !isBareClone(args) {
			dir := args[len(args)-1]
			url := args[len(args)-2]
			writePackManifest(t, dir, filepath.Base(url))
		}
		return nil
	}

	results, err := e.update(t, "", func(r *PackUpdateRequest) {
		r.All = true
		r.Name = ""
		r.RunGitFn = updateGit
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(results) != installCount {
		t.Fatalf("expected %d results, got %d", installCount, len(results))
	}

	// results[] order is alphabetical (os.ReadDir guarantee, preserved by
	// the serial post-phase iteration).
	for i, want := range names {
		if results[i].Name != want {
			t.Errorf("results[%d].Name = %q, want %q (full order: %v)",
				i, results[i].Name, want, packNames(results))
		}
	}

	// Every pack's terminal "Updated (clone)" line must appear in the
	// output (order is no longer guaranteed — see test docstring).
	out := e.out.String()
	for _, n := range names {
		if !strings.Contains(out, "Updated (clone): "+n) {
			t.Errorf("missing 'Updated (clone): %s' in output:\n%s", n, out)
		}
	}
}

// packNames extracts result names for diagnostic output.
func packNames(rs []PackUpdateResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestPackUpdate_NotInstalled(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	results, _ := e.update(t, "nonexistent")
	if results[0].Status != StatusError {
		t.Fatalf("status = %q, want error", results[0].Status)
	}
}

// TestPackUpdate_All_PreCancelledContextMarksAllCancelled verifies that the
// dispatch loop's fast-path ctx.Err() check fires on a pre-cancelled context
// and produces a "cancelled" PackUpdateResult for every pack instead of
// dispatching workers that would just fail-fast on the first ctx-aware op.
// Pre-cancelling makes the test deterministic — no goroutine scheduling races.
func TestPackUpdate_All_PreCancelledContextMarksAllCancelled(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addLink(t, "pack-a")
	e.addLink(t, "pack-b")
	e.addLink(t, "pack-c")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e.out.Reset()
	results, err := PackUpdate(ctx, PackUpdateRequest{
		ConfigDir: e.configDir,
		All:       true,
	}, &e.out, nil)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != StatusError {
			t.Errorf("pack %s: status = %q, want %q", r.Name, r.Status, StatusError)
		}
		if r.Message != "cancelled" {
			t.Errorf("pack %s: message = %q, want %q", r.Name, r.Message, "cancelled")
		}
	}
}

func TestPackUpdate_Clone_TracksCommitHash(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if results[0].Status != StatusUpdated || results[0].CommitHash != fakeHash2 {
		t.Fatalf("status=%q hash=%q", results[0].Status, results[0].CommitHash)
	}
	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	if lf.Packs["my-pack"].CommitHash != fakeHash2 {
		t.Fatalf("stored hash = %q", lf.Packs["my-pack"].CommitHash)
	}
	if !strings.Contains(e.out.String(), "aabbccd") || !strings.Contains(e.out.String(), "1122334") {
		t.Fatalf("expected hash transition in output: %s", e.out.String())
	}
}

func TestPackUpdate_Clone_UpToDate(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(context.Context, ...string) error { return nil }
		r.GitHashFn = fakeHashFn(fakeHash1) // same hash
	})
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q", results[0].Status)
	}
	if !strings.Contains(e.out.String(), "Up-to-date") {
		t.Fatalf("output = %s", e.out.String())
	}
}

func TestPackUpdate_Clone_LsRemoteSkipsClone(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	cloned := false
	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				cloned = true
			}
			return nil
		}
		r.GitLsRemoteFn = func(context.Context, string, string) (string, error) {
			return fakeHash1, nil // same as stored hash
		}
	})
	if cloned {
		t.Fatal("expected ls-remote to skip the clone")
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}
}

func TestPackUpdate_Clone_LsRemoteFails_FallsThrough(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	cloned := false
	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				cloned = true
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitLsRemoteFn = func(context.Context, string, string) (string, error) {
			return "", fmt.Errorf("network error")
		}
		r.GitHashFn = fakeHashFn(fakeHash2) // different hash → update
	})
	if !cloned {
		t.Fatal("expected clone when ls-remote fails")
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
}

func TestPackUpdate_Clone_LsRemoteSkippedWhenWithAddsCategory(t *testing.T) {
	t.Parallel()
	cloneWithExtras := fakeCloneGitFnWithSetup(t, func(dir string) {
		os.MkdirAll(filepath.Join(dir, "scripts"), 0o700)
		os.WriteFile(filepath.Join(dir, "scripts", "helper.sh"), []byte("#!/bin/sh\n"), 0o600)
		config.SavePackManifest(filepath.Join(dir, "pack.json"), config.PackManifest{
			SchemaVersion: 2, Name: "my-pack", Version: "1.0.0", Root: ".",
			Extras: []string{"scripts"},
		})
	})

	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", cloneWithExtras)

	cloned := false
	lsRemoteCalled := false
	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.With = domain.NewBundledSet(domain.BundledExtras) // new category
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				cloned = true
				cloneWithExtras(context.Background(), args...)
			}
			return nil
		}
		r.GitLsRemoteFn = func(context.Context, string, string) (string, error) {
			lsRemoteCalled = true
			return fakeHash1, nil // same hash — but should still clone
		}
		r.GitHashFn = fakeHashFn(fakeHash1)
	})
	if lsRemoteCalled {
		t.Fatal("ls-remote should not be called when --with adds new categories")
	}
	if !cloned {
		t.Fatal("expected clone when --with adds new category")
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
}

func TestPackUpdate_Clone_UpToDate_WithExtrasRehydrates(t *testing.T) {
	t.Parallel()
	cloneWithExtras := fakeCloneGitFnWithSetup(t, func(dir string) {
		os.MkdirAll(filepath.Join(dir, "scripts"), 0o700)
		os.WriteFile(filepath.Join(dir, "scripts", "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o600)
		config.SavePackManifest(filepath.Join(dir, "pack.json"), config.PackManifest{
			SchemaVersion: 2, Name: "my-pack", Version: "1.0.0", Root: ".",
			Extras: []string{"scripts"},
		})
	})

	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", cloneWithExtras)

	// Extras should be absent after initial install (remote default = decline).
	packDir := filepath.Join(e.configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(packDir, "scripts", "helper.sh")); !os.IsNotExist(err) {
		t.Fatal("extras should be absent initially")
	}

	// Update with --with extras: rehydrates.
	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.With = domain.NewBundledSet(domain.BundledExtras)
		r.RunGitFn = cloneWithExtras
		r.GitHashFn = fakeHashFn(fakeHash1)
	})
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}
	got, err := os.ReadFile(filepath.Join(packDir, "scripts", "helper.sh"))
	if err != nil {
		t.Fatalf("expected extras: %v", err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestPackUpdate_Copy_PreferencesCarryForward(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	for _, sub := range []string{"rules", "skills", "extras"} {
		dir := filepath.Join(srcDir, sub)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "init.md"),
			fmt.Appendf(nil, "---\nname: %s-init\n---\ncontent\n", sub), 0o644)
	}
	os.MkdirAll(filepath.Join(srcDir, "profiles"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "profiles", "dev.yaml"),
		[]byte("packs:\n  - essentials\n"), 0o644)
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), config.PackManifest{
		SchemaVersion: 2, Name: "pref-pack", Version: "1.0.0", Root: ".",
		Rules: []string{"rules/init.md"}, Skills: []string{"skills/init.md"},
		Profiles: []string{"dev"}, Extras: []string{"extras/init.md"},
	})

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	packDir := filepath.Join(configDir, "packs", "pref-pack")
	os.MkdirAll(filepath.Dir(packDir), 0o755)
	os.CopyFS(packDir, os.DirFS(srcDir))

	packRecordOrigin(configDir, "pref-pack", config.InstalledPackMeta{
		Origin: srcDir, Method: config.MethodCopy,
		InstalledAt: fixedNow.UTC().Format(time.RFC3339),
		Approved:    []domain.BundledCategory{domain.BundledExtras},
		Declined:    []domain.BundledCategory{domain.BundledProfiles},
	})

	// Modify source to trigger update.
	os.WriteFile(filepath.Join(srcDir, "rules", "new-rule.md"),
		[]byte("---\nname: new-rule\n---\nnew\n"), 0o644)

	var out bytes.Buffer
	results, _ := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir, Name: "pref-pack",
		NowFn: func() time.Time { return fixedNow },
	}, &out, nil)
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}

	// Core dirs present.
	for _, f := range []string{"rules/new-rule.md", "skills/init.md", "extras/init.md"} {
		if _, err := os.Stat(filepath.Join(packDir, f)); err != nil {
			t.Fatalf("%s should be present", f)
		}
	}
	// Declined category absent.
	if _, err := os.Stat(filepath.Join(packDir, "profiles")); !os.IsNotExist(err) {
		t.Fatal("profiles/ should be absent (declined)")
	}

	// Prefs persisted.
	lf, _ := config.LoadLockfile(config.LockfilePath(configDir))
	meta := lf.Packs["pref-pack"]
	approvedSet := map[domain.BundledCategory]bool{}
	for _, a := range meta.Approved {
		approvedSet[a] = true
	}
	if !approvedSet[domain.BundledExtras] {
		t.Errorf("extras should be approved, got %v", meta.Approved)
	}
	declinedSet := map[domain.BundledCategory]bool{}
	for _, d := range meta.Declined {
		declinedSet[d] = true
	}
	if !declinedSet[domain.BundledProfiles] {
		t.Errorf("profiles should be declined, got %v", meta.Declined)
	}
}

func TestPackUpdate_Copy_CopiesRepoRelativeExtras(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0o700)
	srcDir := filepath.Join(repoDir, "packs", "copy-pack")
	os.MkdirAll(srcDir, 0o700)
	sharedDir := filepath.Join(repoDir, "shared-scripts")
	os.MkdirAll(sharedDir, 0o700)
	os.WriteFile(filepath.Join(sharedDir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o600)
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), config.PackManifest{
		SchemaVersion: 2, Name: "copy-pack", Version: "1.0.0", Root: ".",
		Extras: []string{"../../shared-scripts"},
	})

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	packDir := filepath.Join(configDir, "packs", "copy-pack")
	os.MkdirAll(filepath.Dir(packDir), 0o700)
	os.CopyFS(packDir, os.DirFS(srcDir))
	packRecordOrigin(configDir, "copy-pack", config.InstalledPackMeta{
		Origin: srcDir, Method: config.MethodCopy,
		InstalledAt: fixedNow.UTC().Format(time.RFC3339),
		Approved:    []domain.BundledCategory{domain.BundledExtras},
		Declined:    []domain.BundledCategory{domain.BundledProfiles, domain.BundledRegistries},
	})

	var out bytes.Buffer
	results, _ := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir, Name: "copy-pack",
		NowFn: func() time.Time { return fixedNow },
	}, &out, nil)
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}
	got, err := os.ReadFile(filepath.Join(packDir, "shared-scripts", "helper.sh"))
	if err != nil {
		t.Fatalf("expected repo-relative extras: %v", err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("content = %q", got)
	}
	installed, _ := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if len(installed.Extras) != 1 || installed.Extras[0] != "shared-scripts" {
		t.Fatalf("extras = %v, want [shared-scripts]", installed.Extras)
	}
}

// --- Version-aware update tests ---

// patchLockfileRef sets the Ref field for a pack in the lockfile. Passing a
// semver tag like "v1.0.0" or a commit hash marks the pack as pinned.
func patchLockfileRef(t *testing.T, configDir, name, ref string) {
	t.Helper()
	lfPath := config.LockfilePath(configDir)
	lf, err := config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	m := lf.Packs[name]
	m.Ref = ref
	lf.Packs[name] = m
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}
}

func TestPackUpdate_Clone_PinnedSkipsUpdate(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	// Pin the pack at a semver version.
	patchLockfileRef(t, e.configDir, "my-pack", "v1.0.0")

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(context.Context, ...string) error { return nil }
		r.GitHashFn = fakeHashFn(fakeHash2) // different hash — would update if not pinned
		r.GitLsRemoteFn = func(_ context.Context, _, _ string) (string, error) {
			return fakeHash2, nil // remote has moved
		}
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v2.0.0"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date (pinned pack should skip)", results[0].Status)
	}
	if !strings.Contains(e.out.String(), "Pinned") {
		t.Fatalf("output should mention Pinned: %s", e.out.String())
	}
	if !strings.Contains(e.out.String(), "v2.0.0") {
		t.Fatalf("output should mention latest available version: %s", e.out.String())
	}
}

func TestPackUpdate_Clone_PinnedCommitHashSkipsUpdate(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	// Pin at a commit hash.
	patchLockfileRef(t, e.configDir, "my-pack", "aabbccdd")

	var lsRemoteRef string
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.GitLsRemoteFn = func(_ context.Context, _, ref string) (string, error) {
			lsRemoteRef = ref
			return fakeHash2, nil // remote has moved
		}
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return nil, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpToDate {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}
	if lsRemoteRef != "" {
		t.Fatalf("ls-remote ref = %q, want default-branch HEAD lookup", lsRemoteRef)
	}
	if !strings.Contains(e.out.String(), "remote has moved") {
		t.Fatalf("output should mention remote drift: %s", e.out.String())
	}
}

func TestPackUpdate_Clone_VersionFlagMovesSemverPin(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	patchLockfileRef(t, e.configDir, "my-pack", "v1.0.0")

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "2.0.0"
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"v2.0.0"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	meta := lf.Packs["my-pack"]
	if meta.Ref != "v2.0.0" {
		t.Errorf("ref = %q, want v2.0.0", meta.Ref)
	}
	if !isPinned(meta) {
		t.Errorf("expected pin to be recorded after version move")
	}
}

func TestPackUpdate_Clone_VersionLatestUnpins(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	patchLockfileRef(t, e.configDir, "my-pack", "v1.0.0")

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "latest"
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	meta := lf.Packs["my-pack"]
	if meta.Ref != "" {
		t.Errorf("ref = %q, want empty (default branch)", meta.Ref)
	}
	if isPinned(meta) {
		t.Errorf("expected latest to unpin the pack")
	}
}

func TestPackUpdate_Clone_UnpinnedCarriesForwardEmptyVersion(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	// Verify no pin set initially.
	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	if isPinned(lf.Packs["my-pack"]) {
		t.Fatalf("expected no pin initially, got ref=%q", lf.Packs["my-pack"].Ref)
	}

	results, _ := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q", results[0].Status)
	}

	lf, _ = config.LoadLockfile(config.LockfilePath(e.configDir))
	if isPinned(lf.Packs["my-pack"]) {
		t.Errorf("unpinned pack became pinned after update: ref=%q", lf.Packs["my-pack"].Ref)
	}
}

// TestPackUpdate_HTTPTarballMigratesToClone verifies that legacy packs
// installed via the (now-removed) HTTP tarball method are transparently
// re-installed via clone on the next update, with their lockfile entry
// migrated to method=clone.
func TestPackUpdate_HTTPTarballMigratesToClone(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	// Manually seed the lockfile with a legacy http-tarball entry. We can't
	// install via tarball anymore — that path is gone — so simulate the
	// pre-migration state by writing the entry directly.
	packDir := filepath.Join(PacksDir(e.configDir), "legacy-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "legacy-pack")

	lfPath := config.LockfilePath(e.configDir)
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"legacy-pack": {
				Origin:      "https://github.com/example/legacy-pack",
				Method:      config.MethodHTTPTarball,
				InstalledAt: fixedNow.UTC().Format(time.RFC3339),
				// no commit hash — tarball installs never had one
			},
		},
	}
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	// Update should clone (not download tarball) and migrate the method.
	cloned := false
	results, err := e.update(t, "legacy-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				cloned = true
				writePackManifest(t, args[len(args)-1], "legacy-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !cloned {
		t.Fatal("expected git clone to be called for migration")
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if results[0].Method != config.MethodClone {
		t.Errorf("result.Method = %q, want %q", results[0].Method, config.MethodClone)
	}
	if !strings.Contains(e.out.String(), "migrated") {
		t.Errorf("expected 'migrated' in output, got: %s", e.out.String())
	}

	// Verify lockfile reflects the migration.
	lf, _ = config.LoadLockfile(lfPath)
	got := lf.Packs["legacy-pack"]
	if got.Method != config.MethodClone {
		t.Errorf("lockfile method = %q, want %q", got.Method, config.MethodClone)
	}
	if got.CommitHash != fakeHash2 {
		t.Errorf("lockfile commit_hash = %q, want %q", got.CommitHash, fakeHash2)
	}
}

// TestPackUpdate_HTTPTarballURLFailsHelpfully verifies that legacy packs
// whose Origin is an actual tarball URL (`.tar.gz`, `/archive/`, etc.)
// return a targeted error instead of falling through to `git clone
// <tarball-url>`, which would produce an opaque "not a git repository"
// failure. Without a registry remap there is no path forward, so the
// error message points at manual reinstall.
func TestPackUpdate_HTTPTarballURLFailsHelpfully(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		origin string
	}{
		{"tar.gz suffix", "https://example.com/pack.tar.gz"},
		{"github archive path", "https://github.com/example/pack/archive/refs/tags/v1.0.tar.gz"},
		{"zip suffix", "https://example.com/pack.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newUpdateEnv(t)
			packDir := filepath.Join(PacksDir(e.configDir), "tarball-pack")
			if err := os.MkdirAll(packDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writePackManifest(t, packDir, "tarball-pack")
			lfPath := config.LockfilePath(e.configDir)
			lf := config.Lockfile{
				LockVersion: config.LockfileVersion,
				Packs: map[string]config.InstalledPackMeta{
					"tarball-pack": {
						Origin:      tc.origin,
						Method:      config.MethodHTTPTarball,
						InstalledAt: fixedNow.UTC().Format(time.RFC3339),
					},
				},
			}
			if err := config.SaveLockfile(lfPath, lf); err != nil {
				t.Fatal(err)
			}

			results, err := e.update(t, "tarball-pack", func(r *PackUpdateRequest) {
				r.RunGitFn = func(context.Context, ...string) error {
					t.Fatal("git should not be called when origin is a tarball URL")
					return nil
				}
			})
			if err != nil {
				t.Fatalf("update returned error: %v", err)
			}
			if results[0].Status != StatusError {
				t.Fatalf("status = %q, want error", results[0].Status)
			}
			if !strings.Contains(results[0].Message, "tarball URL") {
				t.Errorf("error message should mention tarball URL: %s", results[0].Message)
			}
			if !strings.Contains(results[0].Message, "pack delete") {
				t.Errorf("error message should suggest manual reinstall: %s", results[0].Message)
			}
		})
	}
}

// TestPackUpdate_HTTPTarballRegistryRepoint verifies that when a registry
// entry remaps a legacy tarball origin to a different git URL, the
// migration prints the URL transition before cloning. This is a
// deliberate observability hook so users notice unintentional repoints.
func TestPackUpdate_HTTPTarballRegistryRepoint(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)

	packDir := filepath.Join(PacksDir(e.configDir), "moved-pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "moved-pack")

	lfPath := config.LockfilePath(e.configDir)
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"moved-pack": {
				Origin:      "https://github.com/old-org/moved-pack",
				Method:      config.MethodHTTPTarball,
				InstalledAt: fixedNow.UTC().Format(time.RFC3339),
			},
		},
	}
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	// Seed a registry entry that remaps the origin to a new git URL.
	regYAML := `schema_version: 1
packs:
  moved-pack:
    repo: https://github.com/new-org/moved-pack
    description: relocated
`
	cacheDir := config.RegistriesCacheDir(e.configDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "test.yaml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	scPath := config.SyncConfigPath(e.configDir)
	sc, _ := config.LoadSyncConfig(scPath)
	sc.RegistrySources = []config.RegistrySourceEntry{{Name: "test", URL: "https://example.com/r.yaml"}}
	if err := config.SaveSyncConfig(scPath, sc); err != nil {
		t.Fatal(err)
	}

	results, err := e.update(t, "moved-pack", func(r *PackUpdateRequest) {
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "moved-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	out := e.out.String()
	if !strings.Contains(out, "old-org") || !strings.Contains(out, "new-org") {
		t.Errorf("expected URL transition in output, got: %s", out)
	}
	if !strings.Contains(out, "Migrating") {
		t.Errorf("expected 'Migrating' header in output, got: %s", out)
	}
}

// TestPackUpdate_Clone_PinnedDriftCheckUnavailable verifies that a network
// failure during `pack update` of a pinned pack surfaces as an explicit
// "(drift check unavailable)" hint rather than collapsing silently into the
// "pinned at X" line. Offline users get to distinguish "up-to-date" from
// "we don't know."
func TestPackUpdate_Clone_PinnedDriftCheckUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("semver pin", func(t *testing.T) {
		t.Parallel()
		e := newUpdateEnv(t)
		e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))
		patchLockfileRef(t, e.configDir, "my-pack", "v1.0.0")

		_, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
			r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
				return nil, fmt.Errorf("simulated network failure")
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.out.String(), "drift check unavailable") {
			t.Errorf("expected drift-check-unavailable hint, got: %s", e.out.String())
		}
		if !strings.Contains(e.out.String(), "pinned at v1.0.0") {
			t.Errorf("expected pin label in output, got: %s", e.out.String())
		}
	})

	t.Run("commit hash pin", func(t *testing.T) {
		t.Parallel()
		e := newUpdateEnv(t)
		e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))
		patchLockfileRef(t, e.configDir, "my-pack", "aabbccdd")

		_, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
			r.GitLsRemoteFn = func(context.Context, string, string) (string, error) {
				return "", fmt.Errorf("simulated network failure")
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.out.String(), "drift check unavailable") {
			t.Errorf("expected drift-check-unavailable hint, got: %s", e.out.String())
		}
	})
}

// TestPackUpdate_Clone_PinMoveSameCommit guards against a fast-path bug:
// moving a pin between two tags that happen to resolve to the same commit
// hash must still persist the new ref/version. Without the guard, the
// ls-remote fast path would return "up-to-date" from the old metadata and
// silently drop the pin move.
func TestPackUpdate_Clone_PinMoveSameCommit(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	// Seed the lockfile as if the pack were already pinned at v1.0.0 with
	// a known commit hash.
	lfPath := config.LockfilePath(e.configDir)
	lf, _ := config.LoadLockfile(lfPath)
	m := lf.Packs["my-pack"]
	m.Ref = "v1.0.0"
	m.CommitHash = fakeHash1
	lf.Packs["my-pack"] = m
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	// Move pin to v2.0.0 while ls-remote reports that tag resolves to the
	// same commit hash as the current pin.
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "2.0.0"
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash1) // identical commit hash
		r.GitLsRemoteFn = func(_ context.Context, _, _ string) (string, error) {
			return fakeHash1, nil
		}
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"v2.0.0"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}

	// The lockfile must reflect the new pin even though the commit hash
	// didn't change.
	lf, _ = config.LoadLockfile(lfPath)
	got := lf.Packs["my-pack"]
	if got.Ref != "v2.0.0" {
		t.Errorf("ref = %q, want v2.0.0 (pin move must persist)", got.Ref)
	}
}

func TestPackUpdate_Clone_VersionFlagPreservesRemoteBareSemverTag(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	var clonedRef string
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "v2.0.0"
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "--branch" {
						clonedRef = args[i+1]
						break
					}
				}
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"2.0.0"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if clonedRef != "2.0.0" {
		t.Fatalf("clone ref = %q, want raw remote tag", clonedRef)
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	meta := lf.Packs["my-pack"]
	if meta.Ref != "2.0.0" {
		t.Fatalf("ref = %q, want raw remote tag", meta.Ref)
	}
}

// TestPackUpdate_Clone_NamespacedPinAutoPrependsBareSemver covers the
// auto-prepend path in packUpdateOne: when the installed ref is a
// namespaced tag ("my-pack/v1.0.0"), bare --ref 1.0.1 from the user must
// inherit the "my-pack" prefix and resolve against the prefix-scoped tag
// list. The noise tags (flat v1.0.1, other-pack/v1.0.1) prove the prefix
// filter isolates correctly — neither must be picked.
func TestPackUpdate_Clone_NamespacedPinAutoPrependsBareSemver(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	patchLockfileRef(t, e.configDir, "my-pack", "my-pack/v1.0.0")

	var clonedRef string
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "1.0.1" // bare semver — auto-prepend should kick in
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "--branch" {
						clonedRef = args[i+1]
						break
					}
				}
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{
				"v1.0.1",            // flat — must not satisfy a namespaced request
				"other-pack/v1.0.1", // wrong prefix — must not match
				"my-pack/v1.0.0",
				"my-pack/v1.0.1", // target
			}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	// "v1.0.1" alone would mean the prefix was dropped;
	// "my-pack/my-pack/v1.0.1" would mean it was double-prepended.
	if clonedRef != "my-pack/v1.0.1" {
		t.Fatalf("clone ref = %q, want my-pack/v1.0.1 (auto-prepended)", clonedRef)
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	meta := lf.Packs["my-pack"]
	if meta.Ref != "my-pack/v1.0.1" {
		t.Errorf("lockfile ref = %q, want my-pack/v1.0.1", meta.Ref)
	}
	if !isPinned(meta) {
		t.Errorf("expected pin to be recorded after namespaced update")
	}
}

// TestPackUpdate_Clone_NamespacedPinExplicitRefNoDoublePrefix verifies
// the explicit-prefix branch of the auto-prepend guard: when the user
// types the full "my-pack/v1.0.1" on an already-namespaced install, the
// prefix on the classifier result is non-empty, so auto-prepend must
// NOT fire. Double-prepending would produce "my-pack/my-pack/v1.0.1" and
// fail resolution.
func TestPackUpdate_Clone_NamespacedPinExplicitRefNoDoublePrefix(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	patchLockfileRef(t, e.configDir, "my-pack", "my-pack/v1.0.0")

	var clonedRef string
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "my-pack/v1.0.1" // full namespaced form
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "--branch" {
						clonedRef = args[i+1]
						break
					}
				}
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"my-pack/v1.0.0", "my-pack/v1.0.1"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if clonedRef != "my-pack/v1.0.1" {
		t.Fatalf("clone ref = %q, want my-pack/v1.0.1 (no double-prefix)", clonedRef)
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	if got := lf.Packs["my-pack"].Ref; got != "my-pack/v1.0.1" {
		t.Errorf("lockfile ref = %q, want my-pack/v1.0.1", got)
	}
}

// TestPackUpdate_Clone_NamespacedPinPartialSemverAutoPrepends exercises
// the RefPartialSemver branch of the auto-prepend: installed at
// my-pack/v1.0.0, bare --ref v1 must inherit the prefix and resolve
// against namespaced v1.x.x tags, skipping flat v1.x.x and sibling-pack
// v1.x.x entirely.
func TestPackUpdate_Clone_NamespacedPinPartialSemverAutoPrepends(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	patchLockfileRef(t, e.configDir, "my-pack", "my-pack/v1.0.0")

	var clonedRef string
	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "v1" // bare partial semver
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "--branch" {
						clonedRef = args[i+1]
						break
					}
				}
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{
				"v1.9.0", // flat — must not win the partial match
				"my-pack/v1.0.0",
				"my-pack/v1.5.0", // target — highest stable v1.x.x for my-pack
				"my-pack/v2.0.0",
			}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if clonedRef != "my-pack/v1.5.0" {
		t.Fatalf("clone ref = %q, want my-pack/v1.5.0", clonedRef)
	}
	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	if got := lf.Packs["my-pack"].Ref; got != "my-pack/v1.5.0" {
		t.Errorf("lockfile ref = %q, want my-pack/v1.5.0", got)
	}
}

// TestPackUpdate_Clone_PartialSemverResolvesAndPins exercises partial
// version refs on update — the user wants "latest v1.x.x" without typing
// the exact patch version. The resolved tag lands in the lockfile, not the
// partial — `update --version v1` is a shortcut, not a channel.
func TestPackUpdate_Clone_PartialSemverResolvesAndPins(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "v1"
		r.RunGitFn = func(_ context.Context, args ...string) error {
			if len(args) >= 1 && args[0] == "clone" {
				writePackManifest(t, args[len(args)-1], "my-pack")
			}
			return nil
		}
		r.GitHashFn = fakeHashFn(fakeHash2)
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v1.4.0", "v2.0.0", "v1.5.0-beta.1"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if !strings.Contains(e.out.String(), "Resolved v1 → v1.4.0") {
		t.Errorf("expected resolution message, got: %s", e.out.String())
	}

	lf, _ := config.LoadLockfile(config.LockfilePath(e.configDir))
	meta := lf.Packs["my-pack"]
	if meta.Ref != "v1.4.0" {
		t.Errorf("ref = %q, want v1.4.0 (highest stable v1.x.x)", meta.Ref)
	}
}

// TestPackUpdate_Clone_PartialSemverNoMatch verifies the error path when
// `update --version vN` has no matching stable tag in the remote.
func TestPackUpdate_Clone_PartialSemverNoMatch(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	e.addClone(t, "my-pack", fakeCloneGitFn(t, "my-pack"))

	results, err := e.update(t, "my-pack", func(r *PackUpdateRequest) {
		r.Ref = "v3"
		r.ListRemoteTagsFn = func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v2.0.0"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusError {
		t.Fatalf("status = %q, want error", results[0].Status)
	}
	if !strings.Contains(results[0].Message, "resolving partial version") {
		t.Errorf("message should mention partial resolution: %q", results[0].Message)
	}
}
