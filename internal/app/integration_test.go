package app

import (
	"bytes"
	"io"
	"os"
	"sync"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
	"github.com/shrug-labs/aipack/internal/util"
)

// ---------------------------------------------------------------------------
// Shared fixtures
// ---------------------------------------------------------------------------

// realRegistry returns a registry with all real harness implementations.
func realRegistry() *harness.Registry {
	return harness.NewRegistry(
		ccharness.Harness{}, clharness.Harness{}, cxharness.Harness{}, ocharness.Harness{},
	)
}

// testProfile builds a non-trivial profile with content in every vector.
// The pack root is created on disk so that skill copy sources exist.
func testProfile(t *testing.T, packRoot string) domain.Profile {
	t.Helper()

	// Create a skill directory with a file on disk (copies need real sources).
	skillDir := filepath.Join(packRoot, "skills", "diagnose")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: diagnose\n---\nDiagnose issues.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Rules: []domain.Rule{
				{Name: "no-force-push", Raw: []byte("Never force push to main."), SourcePack: "test-pack"},
				{Name: "test-first", Raw: []byte("Write tests before code."), SourcePack: "test-pack"},
			},
			Agents: []domain.Agent{
				{
					Name:        "reviewer",
					Frontmatter: domain.AgentFrontmatter{Name: "reviewer"},
					Body:        []byte("Review code changes.\n"),
					SourcePack:  "test-pack",
				},
			},
			Workflows: []domain.Workflow{
				{Name: "deploy", Raw: []byte("---\nname: deploy\n---\nDeploy to staging.\n"), SourcePack: "test-pack"},
			},
			Skills: []domain.Skill{
				{Name: "diagnose", DirPath: skillDir, SourcePack: "test-pack"},
			},
		}},
	}
}

// reducedProfile returns a profile with only one rule and no agents, workflows,
// or skills. Used to test that sync removes content no longer in the profile.
func reducedProfile(t *testing.T, packRoot string) domain.Profile {
	t.Helper()
	return domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Rules: []domain.Rule{
				{Name: "no-force-push", Raw: []byte("Never force push to main."), SourcePack: "test-pack"},
			},
		}},
	}
}

// syncAndApply runs a full sync (plan + apply) with real harnesses for a single
// harness and returns the result. Fails the test on error.
func syncAndApply(t *testing.T, profile domain.Profile, scope domain.Scope, projectDir, home string, hid domain.Harness, reg *harness.Registry) SyncResult {
	t.Helper()
	result, warnings, err := RunSync(profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      scope,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{hid},
			Home:       home,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
	}, reg, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunSync(%s): %v", hid, err)
	}
	for _, w := range warnings {
		t.Logf("warning: %s: %s", w.Field, w.Message)
	}
	return result
}

// collectFiles walks dir and returns a map of relative paths to content.
// Delegates to walkDir (defined in contract_helpers_test.go).
func collectFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	return walkDir(t, dir)
}

// ---------------------------------------------------------------------------
// Test 1: Plan destinations ⊆ Layout.ValidationRoots
//
// For every real harness, plan a non-trivial profile and assert that every
// write, copy, and settings destination falls under a ValidationRoot.
// This is a structural invariant — if it breaks, sync refuses to write.
// ---------------------------------------------------------------------------

func TestPlanDestinations_WithinValidationRoots(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid)+"/project", func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()
			assertPlanWithinRoots(t, profile, reg, hid, domain.ScopeProject, projectDir, home)
		})
		t.Run(string(hid)+"/global", func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			assertPlanWithinRoots(t, profile, reg, hid, domain.ScopeGlobal, home, home)
		})
	}
}

// assertPlanWithinRoots plans a profile for the given harness and scope, then
// verifies every destination falls under a ValidationRoot or the ledger dir.
func assertPlanWithinRoots(t *testing.T, profile domain.Profile, reg *harness.Registry, hid domain.Harness, scope domain.Scope, baseDir, home string) {
	t.Helper()

	h, err := reg.Lookup(hid)
	if err != nil {
		t.Fatal(err)
	}
	layout := h.Layout(scope, baseDir, home)

	planners, err := reg.AsPlanners([]domain.Harness{hid})
	if err != nil {
		t.Fatal(err)
	}

	projectDir := baseDir
	if scope == domain.ScopeGlobal {
		projectDir = ""
	}

	plan, err := engine.PlanSync(profile, engine.PlanRequest{
		Scope:      scope,
		ProjectDir: projectDir,
		Home:       home,
	}, planners)
	if err != nil {
		t.Fatal(err)
	}

	roots := layout.ValidationRoots
	if plan.Ledger != "" {
		roots = append(roots, filepath.Dir(plan.Ledger))
	}

	for _, w := range plan.Writes {
		if !domain.IsUnderAny(filepath.Clean(w.Dst), roots) {
			t.Errorf("write destination %s not under any ValidationRoot %v", w.Dst, layout.ValidationRoots)
		}
	}
	for _, c := range plan.Copies {
		if !domain.IsUnderAny(filepath.Clean(c.Dst), roots) {
			t.Errorf("copy destination %s not under any ValidationRoot %v", c.Dst, layout.ValidationRoots)
		}
	}
	for _, s := range plan.Settings {
		if !domain.IsUnderAny(filepath.Clean(s.Dst), roots) {
			t.Errorf("settings destination %s not under any ValidationRoot %v", s.Dst, layout.ValidationRoots)
		}
	}
	for _, m := range plan.MCP {
		if !domain.IsUnderAny(filepath.Clean(m.Dst), roots) {
			t.Errorf("MCP destination %s not under any ValidationRoot %v", m.Dst, layout.ValidationRoots)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2: Sync then clean returns filesystem to baseline
//
// For each harness: snapshot the empty project dir, sync a real profile,
// verify files were created, then clean and assert we're back to baseline.
// This is the test that catches orphaned paths.
// ---------------------------------------------------------------------------

func TestSyncThenClean_ReturnsToBaseline(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// Snapshot: empty baseline.
			baselineBefore := collectFiles(t, projectDir)

			// Sync.
			result := syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}

			// Verify something was created.
			afterSync := collectFiles(t, projectDir)
			if len(afterSync) <= len(baselineBefore) {
				t.Fatalf("sync should have created files: before=%d after=%d", len(baselineBefore), len(afterSync))
			}

			// Clean.
			var stderr bytes.Buffer
			err := RunClean(CleanRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{hid},
					Home:       home,
				},
				WipeLedger: true,
				Yes:        true,
				Stderr:     &stderr,
			}, reg)
			if err != nil {
				t.Fatalf("RunClean: %v", err)
			}

			// Build set of OwnedFile paths — these are partially managed and
			// survive clean (they get reset, not deleted).
			h, _ := reg.Lookup(hid)
			layout := h.Layout(domain.ScopeProject, projectDir, home)
			ownedPaths := map[string]struct{}{}
			for _, of := range layout.OwnedFiles {
				rel, err := filepath.Rel(projectDir, of.Path)
				if err == nil {
					ownedPaths[rel] = struct{}{}
				}
			}

			// Verify: no managed files remain (except OwnedFiles).
			afterClean := collectFiles(t, projectDir)
			for relPath := range afterSync {
				if _, inBaseline := baselineBefore[relPath]; inBaseline {
					continue
				}
				if _, isOwned := ownedPaths[relPath]; isOwned {
					continue // partially-owned files survive clean
				}
				if _, stillPresent := afterClean[relPath]; stillPresent {
					t.Errorf("orphaned after clean: %s", relPath)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 3: Sync then capture round-trips content
//
// Sync a profile to a harness, then capture from the same harness and verify
// the captured content matches the original profile data. Rules, agents,
// workflows, and skills should survive the round trip.
// ---------------------------------------------------------------------------

func TestSyncThenCapture_ContentFidelity(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	// Test with Claude Code — the most complete harness.
	hid := domain.HarnessClaudeCode
	projectDir := t.TempDir()
	home := t.TempDir()

	// Sync.
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)

	// Capture.
	h, err := reg.Lookup(hid)
	if err != nil {
		t.Fatal(err)
	}
	captureResult, err := h.Capture(harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Verify rules round-tripped.
	wantRules := map[string]bool{"no-force-push": false, "test-first": false}
	for _, r := range captureResult.Rules {
		if _, ok := wantRules[r.Name]; ok {
			wantRules[r.Name] = true
		}
	}
	for name, found := range wantRules {
		if !found {
			t.Errorf("rule %q not captured after sync", name)
		}
	}

	// Verify agents round-tripped.
	wantAgents := map[string]bool{"reviewer": false}
	for _, a := range captureResult.Agents {
		if _, ok := wantAgents[a.Name]; ok {
			wantAgents[a.Name] = true
		}
	}
	for name, found := range wantAgents {
		if !found {
			t.Errorf("agent %q not captured after sync", name)
		}
	}

	// Verify skills round-tripped (as copy actions).
	foundSkill := false
	for _, c := range captureResult.Copies {
		if strings.Contains(c.Src, "diagnose") || strings.Contains(c.Dst, "diagnose") {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Error("skill 'diagnose' not captured after sync")
	}

	// Verify workflows round-tripped (captured via rules or copies depending on harness).
	// Claude Code captures workflows as rules from the commands/ dir, or as copies.
	// Just verify the file exists on disk.
	commandsDir := filepath.Join(projectDir, ".claude", "commands")
	rulesDir := filepath.Join(projectDir, ".claude", "rules")
	foundWorkflow := false
	for _, dir := range []string{commandsDir, rulesDir} {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if strings.Contains(strings.ToLower(info.Name()), "deploy") {
				foundWorkflow = true
			}
			return nil
		})
	}
	if !foundWorkflow {
		t.Error("workflow 'deploy' not found on disk after sync")
	}
}

// ---------------------------------------------------------------------------
// Test 4: Concurrent WriteFileAtomic
//
// Race multiple goroutines writing to the same destination path. Assert no
// partial writes or data loss — every final read should contain complete
// content from one of the writers.
// ---------------------------------------------------------------------------

func TestWriteFileAtomic_Concurrent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "target.json")

	const numWriters = 10
	const numIterations = 50

	// Each writer produces self-identifying content: all non-newline bytes are
	// the same character. A torn write would mix characters from two writers.
	makeContent := func(writer int) []byte {
		line := strings.Repeat(string(rune('A'+writer)), 200) + "\n"
		return []byte(strings.Repeat(line, 10))
	}

	var writers sync.WaitGroup
	stop := make(chan struct{})

	for w := range numWriters {
		writers.Add(1)
		go func() {
			defer writers.Done()
			content := makeContent(w)
			for range numIterations {
				if err := util.WriteFileAtomic(dst, content); err != nil {
					t.Errorf("write error: %v", err)
					return
				}
			}
		}()
	}

	// Reader: continuously verify content integrity while writers are active.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(dst)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				t.Errorf("read error: %v", err)
				return
			}
			if len(data) == 0 {
				continue
			}
			var ch byte
			for _, b := range data {
				if b == '\n' {
					continue
				}
				if ch == 0 {
					ch = b
				} else if b != ch {
					t.Errorf("torn write: saw both %c and %c in single read", ch, b)
					return
				}
			}
		}
	}()

	writers.Wait()
	close(stop)
	<-readerDone

	// Final read: file should contain complete content from one writer.
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("final file is empty")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Idempotency — syncing the same profile twice yields identical state
//
// For each harness: sync a profile, snapshot the filesystem, sync the same
// profile again, and assert the filesystem is identical. A non-idempotent sync
// would append duplicate content, create extra files, or alter existing files.
// ---------------------------------------------------------------------------

func TestIdempotency_SyncTwiceIdentical(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// First sync.
			result := syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}
			afterFirst := collectFiles(t, projectDir)

			// Second sync — same profile.
			syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			afterSecond := collectFiles(t, projectDir)

			// File set must be identical.
			for path, content := range afterFirst {
				content2, ok := afterSecond[path]
				if !ok {
					t.Errorf("file disappeared after second sync: %s", path)
					continue
				}
				if content != content2 {
					t.Errorf("file content changed after second sync: %s", path)
				}
			}
			for path := range afterSecond {
				if _, ok := afterFirst[path]; !ok {
					t.Errorf("new file appeared after second sync: %s", path)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 6: Subtraction — removing content from the profile removes it from disk
//
// For each harness: sync a full profile (2 rules, agent, workflow, skill), then
// sync a reduced profile (1 rule only). The removed content's files should be
// deleted from disk by the ledger-based stale cleanup.
// ---------------------------------------------------------------------------

func TestSubtraction_RemovedContentCleaned(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	fullProfile := testProfile(t, packRoot)
	reduced := reducedProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// Sync full profile.
			result := syncAndApply(t, fullProfile, domain.ScopeProject, projectDir, home, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}
			afterFull := collectFiles(t, projectDir)

			// Sync reduced profile — fewer rules, no agents/workflows/skills.
			syncAndApply(t, reduced, domain.ScopeProject, projectDir, home, hid, reg)
			afterReduced := collectFiles(t, projectDir)

			// The reduced sync should have fewer files than the full sync.
			if len(afterReduced) >= len(afterFull) {
				t.Errorf("expected fewer files after reduced sync: full=%d reduced=%d", len(afterFull), len(afterReduced))
			}

			// Specifically, look for content that should be gone. Walk the
			// full set and verify that files containing removed content names
			// are no longer present (excluding ledger and owned settings files).
			h, _ := reg.Lookup(hid)
			layout := h.Layout(domain.ScopeProject, projectDir, home)
			ownedPaths := map[string]struct{}{}
			for _, of := range layout.OwnedFiles {
				rel, err := filepath.Rel(projectDir, of.Path)
				if err == nil {
					ownedPaths[rel] = struct{}{}
				}
			}

			removedNames := []string{"test-first", "reviewer", "deploy", "diagnose"}
			for path := range afterFull {
				if _, isOwned := ownedPaths[path]; isOwned {
					continue
				}
				if _, stillPresent := afterReduced[path]; stillPresent {
					for _, name := range removedNames {
						if strings.Contains(strings.ToLower(path), name) {
							t.Errorf("removed content still present after reduced sync: %s", path)
						}
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 7: Convergence — sync restores managed files after manual edits
//
// For each harness: sync a profile, manually overwrite a managed file, then
// sync again with Force. The file should be restored to the profile's content.
// ---------------------------------------------------------------------------

func TestConvergence_ManualEditsOverwritten(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// Sync.
			result := syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}
			afterSync := collectFiles(t, projectDir)

			// Build set of owned-file paths (settings/MCP config) that use
			// merge semantics — corrupting them tests a different property.
			h, _ := reg.Lookup(hid)
			layout := h.Layout(domain.ScopeProject, projectDir, home)
			ownedPaths := map[string]struct{}{}
			for _, of := range layout.OwnedFiles {
				rel, err := filepath.Rel(projectDir, of.Path)
				if err == nil {
					ownedPaths[rel] = struct{}{}
				}
			}

			// Corrupt every fully-managed file with garbage (skip owned files).
			for path := range afterSync {
				if _, isOwned := ownedPaths[path]; isOwned {
					continue
				}
				abs := filepath.Join(projectDir, path)
				if err := os.WriteFile(abs, []byte("CORRUPTED BY TEST"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Re-sync — Force ensures managed files are overwritten.
			syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			afterResync := collectFiles(t, projectDir)

			for path, original := range afterSync {
				if _, isOwned := ownedPaths[path]; isOwned {
					continue
				}
				restored, ok := afterResync[path]
				if !ok {
					t.Errorf("file not restored after re-sync: %s", path)
					continue
				}
				if restored == "CORRUPTED BY TEST" {
					t.Errorf("file not overwritten after re-sync: %s", path)
					continue
				}
				if restored != original {
					t.Errorf("file content differs after re-sync: %s", path)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 7b: Convergence with corrupted owned files
//
// Sync a profile, corrupt ALL files (including owned settings/MCP JSON), then
// re-sync. A resilient sync should recover owned files rather than failing to
// parse the garbage.
// ---------------------------------------------------------------------------

func TestConvergence_CorruptedOwnedFiles(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// Sync.
			result := syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}

			// Corrupt ALL files, including owned settings/MCP config.
			afterSync := collectFiles(t, projectDir)
			for path := range afterSync {
				abs := filepath.Join(projectDir, path)
				if err := os.WriteFile(abs, []byte("CORRUPTED BY TEST"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Re-sync with force — should recover, not fail.
			_, _, err := RunSync(profile, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{hid},
					Home:       home,
				},
				Force: true,
				Yes:   true,
				Quiet: true,
			}, reg, io.Discard, io.Discard)
			if err != nil {
				t.Errorf("RunSync with corrupted owned files: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 8: Cross-harness isolation — syncing one harness doesn't affect another
//
// Sync harness A to a project directory, snapshot its files, then sync harness
// B to the same directory. Harness A's files should be unchanged.
// ---------------------------------------------------------------------------

func TestCrossHarnessIsolation(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()
	harnesses := domain.AllHarnesses()

	projectDir := t.TempDir()
	home := t.TempDir()

	// Sync all harnesses one at a time, recording each one's files.
	snapshots := map[domain.Harness]map[string]string{}
	for _, hid := range harnesses {
		syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
		snapshots[hid] = collectFiles(t, projectDir)
	}

	// For each pair (A synced before B): every file that A created and that
	// is under A's validation roots should be unchanged after B synced.
	for i, a := range harnesses {
		ha, _ := reg.Lookup(a)
		layoutA := ha.Layout(domain.ScopeProject, projectDir, home)

		for j := i + 1; j < len(harnesses); j++ {
			b := harnesses[j]
			t.Run(string(a)+"_then_"+string(b), func(t *testing.T) {
				// Compare A's files in the final snapshot against A's
				// snapshot taken right after A synced.
				final := snapshots[harnesses[len(harnesses)-1]]
				snapshotA := snapshots[a]

				for path, contentA := range snapshotA {
					abs := filepath.Join(projectDir, path)
					if !domain.IsUnderAny(abs, layoutA.ValidationRoots) {
						continue // not A's file
					}
					contentFinal, ok := final[path]
					if !ok {
						t.Errorf("harness %s file deleted after syncing %s: %s", a, b, path)
						continue
					}
					if contentA != contentFinal {
						t.Errorf("harness %s file modified after syncing %s: %s", a, b, path)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Test 9: Profile ordering independence — reordered profile yields same output
//
// Sync a profile, snapshot. Then sync a profile with the same content but
// rules, agents, workflows, and skills in reversed order. The filesystem
// should be identical.
// ---------------------------------------------------------------------------

func TestOrderingIndependence(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := realRegistry()

	// Build a reordered profile: reverse the slices within the pack.
	reordered := testProfile(t, packRoot)
	for i := range reordered.Packs {
		p := &reordered.Packs[i]
		reverseRules(p.Rules)
		reverseAgents(p.Agents)
		reverseWorkflows(p.Workflows)
		reverseSkills(p.Skills)
	}

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()

			// Sync original order.
			projA := t.TempDir()
			homeA := t.TempDir()
			result := syncAndApply(t, profile, domain.ScopeProject, projA, homeA, hid, reg)
			totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
			if totalOps == 0 {
				t.Skip("harness produced no operations for this profile")
			}
			filesA := collectFiles(t, projA)

			// Sync reordered.
			projB := t.TempDir()
			homeB := t.TempDir()
			syncAndApply(t, reordered, domain.ScopeProject, projB, homeB, hid, reg)
			filesB := collectFiles(t, projB)

			// Same file set.
			if len(filesA) != len(filesB) {
				t.Fatalf("different file counts: original=%d reordered=%d", len(filesA), len(filesB))
			}
			for path, contentA := range filesA {
				contentB, ok := filesB[path]
				if !ok {
					t.Errorf("file missing in reordered sync: %s", path)
					continue
				}
				if contentA != contentB {
					t.Errorf("file content differs with reordered profile: %s", path)
				}
			}
		})
	}
}

// reverse helpers for TestOrderingIndependence.
func reverseRules(s []domain.Rule)         { reverseSlice(s) }
func reverseAgents(s []domain.Agent)       { reverseSlice(s) }
func reverseWorkflows(s []domain.Workflow) { reverseSlice(s) }
func reverseSkills(s []domain.Skill)       { reverseSlice(s) }

func reverseSlice[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
