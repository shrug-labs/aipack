package app

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// ---------------------------------------------------------------------------
// Shared fixtures
// ---------------------------------------------------------------------------

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
	return syncAndApplyWithOptions(t, profile, scope, projectDir, home, hid, reg, syncAndApplyOptions{})
}

func syncAndApplyNamespaced(t *testing.T, profile domain.Profile, scope domain.Scope, projectDir, home string, hid domain.Harness, reg *harness.Registry) SyncResult {
	t.Helper()
	return syncAndApplyWithOptions(t, profile, scope, projectDir, home, hid, reg, syncAndApplyOptions{Namespaced: true})
}

type syncAndApplyOptions struct {
	Namespaced bool
}

func syncAndApplyWithOptions(t *testing.T, profile domain.Profile, scope domain.Scope, projectDir, home string, hid domain.Harness, reg *harness.Registry, opts syncAndApplyOptions) SyncResult {
	t.Helper()
	result, warnings, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      scope,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{hid},
			Home:       home,
			Namespaced: opts.Namespaced,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
	}, reg, nil, nil)
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
	reg := testRegistry()

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

	plan, err := engine.PlanSync(context.Background(), profile, engine.PlanRequest{
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
	reg := testRegistry()

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
			err := RunClean(context.Background(), engine.New(nil, nil), CleanRequest{
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
	reg := testRegistry()

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
	captureResult, err := h.Capture(context.Background(), harness.CaptureContext{
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

	// Verify skills round-tripped with rendered names stripped back to source IDs.
	foundSkill := false
	for _, s := range captureResult.Skills {
		if s.Name == "diagnose" {
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
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if strings.Contains(strings.ToLower(d.Name()), "deploy") {
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows file locking prevents concurrent rename to the same target")
	}
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
		writers.Go(func() {
			content := makeContent(w)
			for range numIterations {
				if err := util.WriteFileAtomic(dst, content); err != nil {
					t.Errorf("write error: %v", err)
					return
				}
			}
		})
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
	reg := testRegistry()

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
	reg := testRegistry()

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

func TestNamespacedToggle_ReconcilesRenderedContent(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	skillDir := filepath.Join(packRoot, "skills", "diagnose")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: diagnose\ndescription: Diagnose\n---\nDiagnose issues.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Rules: []domain.Rule{
				{Name: "use-cache", Raw: []byte("Use cache when safe.\n"), SourcePack: "test-pack"},
			},
			Workflows: []domain.Workflow{
				{Name: "deploy", Raw: []byte("---\nname: deploy\n---\nDeploy.\n"), SourcePack: "test-pack"},
			},
			Skills: []domain.Skill{
				{Name: "diagnose", DirPath: skillDir, SourcePack: "test-pack"},
			},
		}},
	}

	reg := testRegistry()
	projectDir := t.TempDir()
	home := t.TempDir()

	naturalRule := filepath.Join(projectDir, ".claude", "rules", "use-cache.md")
	naturalWorkflow := filepath.Join(projectDir, ".claude", "commands", "deploy.md")
	naturalSkill := filepath.Join(projectDir, ".claude", "skills", "diagnose", "SKILL.md")
	namespacedRule := filepath.Join(projectDir, ".claude", "rules", "use-cache__aipack__test-pack.md")
	namespacedWorkflow := filepath.Join(projectDir, ".claude", "commands", "deploy__aipack__test-pack.md")
	namespacedSkill := filepath.Join(projectDir, ".claude", "skills", "diagnose__aipack__test-pack", "SKILL.md")

	mustExist := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	mustNotExist := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be absent, stat err=%v", path, err)
		}
	}

	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode, reg)
	for _, path := range []string{naturalRule, naturalWorkflow, naturalSkill} {
		mustExist(path)
	}
	for _, path := range []string{namespacedRule, namespacedWorkflow, namespacedSkill} {
		mustNotExist(path)
	}

	syncAndApplyNamespaced(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode, reg)
	for _, path := range []string{namespacedRule, namespacedWorkflow, namespacedSkill} {
		mustExist(path)
	}
	for _, path := range []string{naturalRule, naturalWorkflow, naturalSkill} {
		mustNotExist(path)
	}
	content, err := os.ReadFile(namespacedSkill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "name: diagnose__aipack__test-pack") {
		t.Fatalf("namespaced skill frontmatter was not rewritten:\n%s", content)
	}

	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode, reg)
	for _, path := range []string{naturalRule, naturalWorkflow, naturalSkill} {
		mustExist(path)
	}
	for _, path := range []string{namespacedRule, namespacedWorkflow, namespacedSkill} {
		mustNotExist(path)
	}
}

func TestNamespacedResolveAndSync_PreservesCrossPackContentCollisions(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	for _, pack := range []string{"pack-a", "pack-b"} {
		packDir := filepath.Join(configDir, "packs", pack)
		writePackManifest(t, packDir, pack)
		writeFile(t, filepath.Join(packDir, "rules", "shared.md"), "---\nname: shared\ndescription: Shared\n---\nrule body\n")
		writeFile(t, filepath.Join(packDir, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviewer\n---\nagent body\n")
		writeFile(t, filepath.Join(packDir, "workflows", "deploy.md"), "---\nname: deploy\ndescription: Deploy\n---\nworkflow body\n")
		writeFile(t, filepath.Join(packDir, "skills", "diagnose", "SKILL.md"), "---\nname: diagnose\ndescription: Diagnose\n---\nskill body\n")
	}

	syncCfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	syncCfg.Defaults.Scope = string(domain.ScopeProject)
	syncCfg.Defaults.Harnesses = []string{string(domain.HarnessClaudeCode)}
	syncCfg.Defaults.CollisionStrategy = config.CollisionError
	syncCfg.Defaults.Namespaced = true

	resolved, warnings, err := ResolveProfile(engine.New(nil, nil), ResolveRequest{
		ConfigDir:   configDir,
		ProfilePath: filepath.Join(configDir, "profiles", "default.yaml"),
		ProfileCfg: config.ProfileConfig{
			SchemaVersion: config.ProfileSchemaVersion,
			Packs:         []config.PackEntry{{Name: "pack-a"}, {Name: "pack-b"}},
		},
		SyncCfg:    syncCfg,
		ProjectDir: t.TempDir(),
		Home:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no collision warnings, got %v", warnings)
	}
	if got := len(resolved.Profile.AllRules()); got != 2 {
		t.Fatalf("resolved rules = %d, want 2", got)
	}
	if got := len(resolved.Profile.AllAgents()); got != 2 {
		t.Fatalf("resolved agents = %d, want 2", got)
	}
	if got := len(resolved.Profile.AllWorkflows()); got != 2 {
		t.Fatalf("resolved workflows = %d, want 2", got)
	}
	if got := len(resolved.Profile.AllSkills()); got != 2 {
		t.Fatalf("resolved skills = %d, want 2", got)
	}

	result, syncWarnings, err := RunSync(context.Background(), engine.New(nil, nil), resolved.Profile, SyncRequest{
		TargetSpec: resolved.TargetSpec,
		Force:      true,
		Yes:        true,
		Quiet:      true,
	}, testRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if len(syncWarnings) != 0 {
		t.Fatalf("expected no sync warnings, got %v", syncWarnings)
	}
	if len(result.Plan.Writes)+len(result.Plan.Copies) == 0 {
		t.Fatal("expected sync to render content")
	}

	for _, rel := range []string{
		filepath.Join(".claude", "rules", "shared__aipack__pack-a.md"),
		filepath.Join(".claude", "rules", "shared__aipack__pack-b.md"),
		filepath.Join(".claude", "agents", "reviewer__aipack__pack-a.md"),
		filepath.Join(".claude", "agents", "reviewer__aipack__pack-b.md"),
		filepath.Join(".claude", "commands", "deploy__aipack__pack-a.md"),
		filepath.Join(".claude", "commands", "deploy__aipack__pack-b.md"),
		filepath.Join(".claude", "skills", "diagnose__aipack__pack-a", "SKILL.md"),
		filepath.Join(".claude", "skills", "diagnose__aipack__pack-b", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(resolved.ProjectDir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}

	agentContent, err := os.ReadFile(filepath.Join(resolved.ProjectDir, ".claude", "agents", "reviewer__aipack__pack-a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentContent), "name: reviewer__aipack__pack-a") {
		t.Fatalf("namespaced agent frontmatter was not rewritten:\n%s", agentContent)
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
	reg := testRegistry()

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
	reg := testRegistry()

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
			_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
				TargetSpec: TargetSpec{
					Scope:      domain.ScopeProject,
					ProjectDir: projectDir,
					Harnesses:  []domain.Harness{hid},
					Home:       home,
				},
				Force: true,
				Yes:   true,
				Quiet: true,
			}, reg, nil, nil)
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
	reg := testRegistry()
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
	reg := testRegistry()

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
			// Normalize per-run target paths in file contents before comparing.
			// Some harnesses (e.g. OpenCode) legitimately embed the target dir
			// in their output (instructions globs, skills.paths) — those paths
			// differ per run because projA/homeA ≠ projB/homeB, but that's not
			// an ordering-dependence concern.
			//
			// On Windows, paths embedded in JSON files get backslash-escaped
			// (`C:\foo` → `C:\\foo`), so we replace both the raw OS form and
			// the JSON-escaped form. On Unix the escaped form is identical to
			// the raw form, so the extra pass is a no-op.
			normalize := func(s, proj, home string) string {
				projEsc := strings.ReplaceAll(proj, `\`, `\\`)
				homeEsc := strings.ReplaceAll(home, `\`, `\\`)
				s = strings.ReplaceAll(s, projEsc, "__PROJ__")
				s = strings.ReplaceAll(s, homeEsc, "__HOME__")
				s = strings.ReplaceAll(s, proj, "__PROJ__")
				s = strings.ReplaceAll(s, home, "__HOME__")
				return s
			}
			for path, contentA := range filesA {
				contentB, ok := filesB[path]
				if !ok {
					t.Errorf("file missing in reordered sync: %s", path)
					continue
				}
				normA := normalize(contentA, projA, homeA)
				normB := normalize(contentB, projB, homeB)
				if normA != normB {
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

// ---------------------------------------------------------------------------
// Test 10: Multi-pack — both packs contribute content
//
// Story: "I compose a profile from two packs — both packs' content appears in
// every harness." Two independently-authored packs each contribute rules and
// agents. After sync, all four content names must be discoverable regardless
// of harness.
// ---------------------------------------------------------------------------

func TestMultiPack_BothPacksContributeContent(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := multiPackProfile(packRoot,
		packWith("alpha", withRules("style-guide"), withAgents("reviewer")),
		packWith("beta", withRules("security"), withAgents("triage")),
	)

	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		env.sync(profile)

		for _, name := range []string{"style-guide", "reviewer", "security", "triage"} {
			if !env.contentExists(name) {
				t.Errorf("expected content %q not found", name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Test 11: Multi-pack — rules and workflows combine
//
// Story: "Rules and workflows from multiple packs land in the same harness
// directories." Two packs each contribute rules and workflows. After sync,
// all four content names must be discoverable.
// ---------------------------------------------------------------------------

func TestMultiPack_WorkflowsAndRulesCombine(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := multiPackProfile(packRoot,
		packWith("ops", withRules("escalation"), withWorkflows("deploy")),
		packWith("dev", withRules("testing"), withWorkflows("review")),
	)

	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		env.sync(profile)

		for _, name := range []string{"escalation", "deploy", "testing", "review"} {
			if !env.contentExists(name) {
				t.Errorf("expected content %q not found", name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Test 12: MCP server rendered per harness
//
// Story: "An MCP server in the profile appears in the harness's rendered
// settings." A profile carries a single stdio MCP server. After syncing to
// each harness at project scope, the server name should appear in the
// rendered settings file — except Cline, which only renders MCP at global
// scope.
// ---------------------------------------------------------------------------

func TestMCPServer_RenderedPerHarness(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	reg := testRegistry()

	profile := profileWith(packRoot, withRules("placeholder"))
	profile.MCPServers = []domain.MCPServer{
		{
			Name:       "test-mcp-server",
			Transport:  domain.TransportStdio,
			Command:    []string{"echo", "hello"},
			SourcePack: "test-pack",
		},
	}

	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			files := collectFiles(t, projectDir)

			if hid == domain.HarnessCline {
				// Cline renders MCP at global scope only; rules should still sync.
				found := false
				for path, content := range files {
					if strings.Contains(path, "placeholder") || strings.Contains(content, "placeholder") {
						found = true
						break
					}
				}
				if !found {
					t.Error("Cline: expected rule 'placeholder' not found at project scope")
				}
				return
			}

			// Non-Cline harnesses: the MCP server name should appear in the
			// rendered settings file (.mcp.json, opencode.json, or config.toml).
			found := false
			for _, content := range files {
				if strings.Contains(content, "test-mcp-server") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: expected MCP server 'test-mcp-server' not found in project files", hid)
			}
		})
	}
}

func TestMCPServer_RemovedFromHarnessOnNextSync(t *testing.T) {
	// Lifecycle gap #1 from aipack-honest-gaps-roadmap.md:
	// "Stale MCP server definitions survive a clean sync."
	// Pattern: sync with an MCP server, then sync again without it (simulates
	// pack removal). Assert the harness MCP config no longer references the
	// removed server while user-irrelevant content elsewhere is unchanged.
	t.Parallel()

	packRoot := t.TempDir()
	reg := testRegistry()

	// Cline renders MCP at global scope so we exercise project scope on the
	// remaining harnesses; Cline gets covered explicitly below.
	for _, hid := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessOpenCode} {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			// First sync: ships acme + sibling MCP servers.
			profileFirst := profileWith(packRoot, withRules("placeholder"))
			profileFirst.MCPServers = []domain.MCPServer{
				{
					Name:       "acme",
					Transport:  domain.TransportStdio,
					Command:    []string{"npx", "acme-mcp"},
					SourcePack: "acme-pack",
				},
				{
					Name:       "sibling",
					Transport:  domain.TransportStdio,
					Command:    []string{"npx", "sibling-mcp"},
					SourcePack: "sibling-pack",
				},
			}
			syncAndApply(t, profileFirst, domain.ScopeProject, projectDir, home, hid, reg)

			files := collectFiles(t, projectDir)
			foundAcme := false
			for _, content := range files {
				if strings.Contains(content, "acme") {
					foundAcme = true
				}
			}
			if !foundAcme {
				t.Fatalf("first sync did not render 'acme'; files=%v", files)
			}

			// Second sync: acme-pack is gone (pack removed), sibling stays.
			profileSecond := profileWith(packRoot, withRules("placeholder"))
			profileSecond.MCPServers = []domain.MCPServer{
				{
					Name:       "sibling",
					Transport:  domain.TransportStdio,
					Command:    []string{"npx", "sibling-mcp"},
					SourcePack: "sibling-pack",
				},
			}
			syncAndApply(t, profileSecond, domain.ScopeProject, projectDir, home, hid, reg)

			files = collectFiles(t, projectDir)
			for path, content := range files {
				if strings.Contains(content, `"acme"`) || strings.Contains(content, `acme-mcp`) {
					t.Fatalf("%s: stale MCP server 'acme' still present in %s after pack removal:\n%s", hid, path, content)
				}
			}
			foundSibling := false
			for _, content := range files {
				if strings.Contains(content, "sibling-mcp") {
					foundSibling = true
				}
			}
			if !foundSibling {
				t.Fatalf("%s: sibling MCP server should still be present after acme removal", hid)
			}
		})
	}
}

func TestMCPServer_RemovedFromClineOnNextSync(t *testing.T) {
	// Cline renders MCP at global scope only. Verify the same stale-removal
	// behavior holds for the home-rooted Cline MCP destination.
	t.Parallel()

	packRoot := t.TempDir()
	reg := testRegistry()
	projectDir := t.TempDir()
	home := t.TempDir()

	profileFirst := profileWith(packRoot, withRules("placeholder"))
	profileFirst.MCPServers = []domain.MCPServer{
		{Name: "acme", Transport: domain.TransportStdio, Command: []string{"npx", "acme-mcp"}, SourcePack: "acme-pack"},
		{Name: "sibling", Transport: domain.TransportStdio, Command: []string{"npx", "sibling-mcp"}, SourcePack: "sibling-pack"},
	}
	syncAndApply(t, profileFirst, domain.ScopeGlobal, projectDir, home, domain.HarnessCline, reg)

	files := collectFiles(t, home)
	foundAcme := false
	for _, content := range files {
		if strings.Contains(content, "acme-mcp") {
			foundAcme = true
		}
	}
	if !foundAcme {
		t.Fatalf("first sync should render acme into Cline global config; files=%v", files)
	}

	profileSecond := profileWith(packRoot, withRules("placeholder"))
	profileSecond.MCPServers = []domain.MCPServer{
		{Name: "sibling", Transport: domain.TransportStdio, Command: []string{"npx", "sibling-mcp"}, SourcePack: "sibling-pack"},
	}
	syncAndApply(t, profileSecond, domain.ScopeGlobal, projectDir, home, domain.HarnessCline, reg)

	// Filter out aipack-internal presync snapshots and ledger files — those
	// intentionally retain pre-sync content for `aipack restore`.
	files = collectFiles(t, home)
	for path, content := range files {
		if isAipackInternal(path) {
			continue
		}
		if strings.Contains(content, `"acme"`) || strings.Contains(content, "acme-mcp") {
			t.Fatalf("Cline: stale MCP server 'acme' still present in %s after pack removal:\n%s", path, content)
		}
	}
	foundSibling := false
	for path, content := range files {
		if isAipackInternal(path) {
			continue
		}
		if strings.Contains(content, "sibling-mcp") {
			foundSibling = true
		}
	}
	if !foundSibling {
		t.Fatalf("Cline: sibling MCP server should still be present after acme removal")
	}
}

func isAipackInternal(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(path, ".config/aipack/") ||
		strings.Contains(path, "appdata/roaming/aipack/") ||
		strings.Contains(path, "/ledger/") ||
		strings.Contains(path, "-presync/")
}

func TestIsAipackInternalHandlesWindowsSeparators(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		`C:\Users\dev\AppData\Roaming\aipack\sync-config.yaml`,
		`AppData\Roaming\aipack\ledger\encoded-project\cline.json`,
		`AppData\Roaming\aipack\some-presync\snapshot.json`,
	} {
		if !isAipackInternal(path) {
			t.Fatalf("isAipackInternal(%q) = false, want true", path)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 13: Codex agent rendered as native TOML
//
// Story: "I define an agent — Codex renders it as a native .toml file in
// .codex/agents/, not as markdown like other harnesses."
// ---------------------------------------------------------------------------

func TestCodex_AgentRenderedAsNativeTOML(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withAgents("reviewer"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// Agent TOML must exist.
	tomlPath := filepath.Join(".codex", "agents", "reviewer.toml")
	content, ok := files[tomlPath]
	if !ok {
		t.Fatalf("expected %s to exist", tomlPath)
	}
	if !strings.Contains(content, "reviewer") {
		t.Error("agent TOML does not contain agent name 'reviewer'")
	}

	// AGENTS.override.md should NOT contain the agent (it's for rules only).
	if override, exists := files["AGENTS.override.md"]; exists {
		if strings.Contains(override, "reviewer") {
			t.Error("AGENTS.override.md should not contain agent 'reviewer' — agents go to .codex/agents/")
		}
	}
}

// ---------------------------------------------------------------------------
// Test 14: Codex rules flattened to AGENTS.override.md
//
// Story: "Multiple rules from my profile are flattened into a single
// AGENTS.override.md, not individual files."
// ---------------------------------------------------------------------------

func TestCodex_RulesFlattenedToOverrideMD(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot,
		ruleWithContent("style", "Use consistent style.\n"),
		ruleWithContent("security", "Never store secrets in code.\n"),
	)
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// AGENTS.override.md must contain both rules' content.
	override, ok := files["AGENTS.override.md"]
	if !ok {
		t.Fatal("expected AGENTS.override.md to exist")
	}
	if !strings.Contains(override, "Use consistent style") {
		t.Error("AGENTS.override.md missing 'Use consistent style'")
	}
	if !strings.Contains(override, "Never store secrets in code") {
		t.Error("AGENTS.override.md missing 'Never store secrets in code'")
	}

	// Individual rule files must NOT exist.
	for path := range files {
		if strings.Contains(path, filepath.Join(".codex", "rules")) {
			t.Errorf("unexpected individual rule file: %s", path)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 15: Existing AGENTS.md preserved below separator
//
// Story: "I have a handwritten AGENTS.md — sync creates AGENTS.override.md
// that preserves my content below a separator."
// ---------------------------------------------------------------------------

func TestCodex_ExistingAGENTSMD_PreservedBelowSeparator(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withRules("team-rule"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()

	// Create a handwritten AGENTS.md before sync.
	agentsMD := filepath.Join(projectDir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("# My Custom Agent Instructions\n\nDo excellent work.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)
	override, ok := files["AGENTS.override.md"]
	if !ok {
		t.Fatal("expected AGENTS.override.md to exist")
	}

	if !strings.Contains(override, "team-rule") {
		t.Error("AGENTS.override.md missing managed rule 'team-rule'")
	}
	if !strings.Contains(override, "My Custom Agent Instructions") {
		t.Error("AGENTS.override.md missing preserved user content")
	}
	if !strings.Contains(override, "preserved from existing AGENTS.md") {
		t.Error("AGENTS.override.md missing preservation separator comment")
	}
}

// ---------------------------------------------------------------------------
// Test 16: Codex workflow promoted to skill directory
//
// Story: "Codex doesn't have native workflows, so my workflow is promoted
// to a skill directory."
// ---------------------------------------------------------------------------

func TestCodex_WorkflowPromotedToSkillDir(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withWorkflows("deploy"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	skillPath := filepath.Join(".agents", "skills", "deploy", "SKILL.md")
	content, ok := files[skillPath]
	if !ok {
		t.Fatalf("expected %s to exist", skillPath)
	}
	if !strings.Contains(content, "source_type: workflow") {
		t.Error("promoted SKILL.md missing 'source_type: workflow' marker")
	}
	if !strings.Contains(content, "deploy") {
		t.Error("promoted SKILL.md missing workflow name 'deploy'")
	}
}

// ---------------------------------------------------------------------------
// Test 17: MCP server in Codex config.toml
//
// Story: "My MCP server appears in Codex's config.toml, not in a JSON file."
// ---------------------------------------------------------------------------

func TestCodex_MCPServerInConfigTOML(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withRules("placeholder"))
	profile.MCPServers = []domain.MCPServer{
		{
			Name:       "lint-server",
			Transport:  domain.TransportStdio,
			Command:    []string{"echo", "lint"},
			SourcePack: "test-pack",
		},
	}
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// config.toml must exist and contain the server.
	configPath := filepath.Join(".codex", "config.toml")
	content, ok := files[configPath]
	if !ok {
		t.Fatalf("expected %s to exist", configPath)
	}
	if !strings.Contains(content, "lint-server") {
		t.Error("config.toml missing MCP server 'lint-server'")
	}

	// .mcp.json should NOT exist (that's Claude Code's format).
	if _, exists := files[".mcp.json"]; exists {
		t.Error("unexpected .mcp.json — Codex uses config.toml for MCP servers")
	}
}

func TestCodex_ProfileSwitchRemovesMCPServerWithRuntimeToolApproval(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	first := profileWith(packRoot, withRules("placeholder"))
	first.MCPServers = []domain.MCPServer{
		{
			Name:       "current",
			Transport:  domain.TransportStdio,
			Command:    []string{"current-command"},
			SourcePack: "test-pack",
		},
		{
			Name:       "previous",
			Transport:  domain.TransportStdio,
			Command:    []string{"previous-command"},
			SourcePack: "test-pack",
		},
	}
	second := profileWith(packRoot, withRules("placeholder"))
	second.MCPServers = []domain.MCPServer{
		{
			Name:       "current",
			Transport:  domain.TransportStdio,
			Command:    []string{"current-command"},
			SourcePack: "test-pack",
		},
	}
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, first, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	configPath := filepath.Join(projectDir, ".codex", "config.toml")
	existing, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml after first sync: %v", err)
	}
	runtimeApprovals := `

[mcp_servers.current.tools.runtime_allowed]
approval_mode = "approve"

[mcp_servers.previous.tools.runtime_allowed]
approval_mode = "approve"
`
	if err := os.WriteFile(configPath, append(existing, []byte(runtimeApprovals)...), 0o644); err != nil {
		t.Fatalf("write config.toml with runtime approvals: %v", err)
	}

	syncAndApply(t, second, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml after profile switch: %v", err)
	}
	content := string(got)
	if strings.Contains(content, "previous") {
		t.Fatalf("removed MCP server should not leave runtime approval tables:\n%s", content)
	}
	if !strings.Contains(content, "runtime_allowed") {
		t.Fatalf("runtime approval for retained MCP server should be preserved:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// Test 18: Codex agent with harness model override
//
// Story: "I set a Codex-specific model on my agent — it renders into the
// native TOML."
// ---------------------------------------------------------------------------

func TestCodex_AgentWithHarnessModelOverride(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot,
		agentWith("explorer", []string{"bash", "read"}, map[string]map[string]any{
			"codex": {"model": "o3", "model_reasoning_effort": "high"},
		}),
	)
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	tomlPath := filepath.Join(".codex", "agents", "explorer.toml")
	content, ok := files[tomlPath]
	if !ok {
		t.Fatalf("expected %s to exist", tomlPath)
	}
	if !strings.Contains(content, "o3") {
		t.Error("agent TOML missing model 'o3'")
	}
	if !strings.Contains(content, "high") {
		t.Error("agent TOML missing reasoning effort 'high'")
	}
}

// ---------------------------------------------------------------------------
// Test 19: User edit preserved without force
//
// Story: "I edited a file that aipack manages — re-sync without --force
// should not overwrite my edit."
//
// The conflict mechanism is engine-level and harness-independent. We test
// with Claude Code where rules are individual .md files (fully managed
// writes, not owned files with merge semantics).
// ---------------------------------------------------------------------------

func TestConflict_UserEditPreservedWithoutForce(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, ruleWithContent("editable", "Original content.\n"))
	reg := testRegistry()
	hid := domain.HarnessClaudeCode
	projectDir := t.TempDir()
	home := t.TempDir()

	// First sync with force — creates the rule file.
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)

	// Find and modify the managed rule file.
	ruleFile := filepath.Join(projectDir, ".claude", "rules", "editable.md")
	if _, err := os.Stat(ruleFile); err != nil {
		t.Fatalf("rule file not created: %v", err)
	}
	if err := os.WriteFile(ruleFile, []byte("User's custom edit.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-sync WITHOUT force. Force: false means conflicts are skipped.
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{hid},
			Home:       home,
		},
		Force: false,
		Yes:   true,
		Quiet: true,
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	// File should still have the user's edit.
	got, err := os.ReadFile(ruleFile)
	if err != nil {
		t.Fatalf("read rule file: %v", err)
	}
	if string(got) != "User's custom edit.\n" {
		t.Errorf("user edit was overwritten: got %q, want %q", got, "User's custom edit.\n")
	}
}

// ---------------------------------------------------------------------------
// Test 20: MCP-only profile syncs without content files
//
// Story: "I have a tools-only pack — just MCP servers, no rules or agents.
// Sync should work and render the server config."
// ---------------------------------------------------------------------------

func TestMCPOnly_SyncsWithoutContentFiles(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	reg := testRegistry()

	// Profile with no content — only MCP servers.
	profile := profileWith(packRoot)
	profile.MCPServers = []domain.MCPServer{
		{
			Name:       "tools-server",
			Transport:  domain.TransportStdio,
			Command:    []string{"echo", "tools"},
			SourcePack: "test-pack",
		},
	}

	// Test across harnesses that support MCP at project scope.
	// Cline renders MCP at global scope only, so skip it.
	for _, hid := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessCodex} {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			files := collectFiles(t, projectDir)

			// MCP server name must appear in some settings file.
			found := false
			for _, content := range files {
				if strings.Contains(content, "tools-server") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: MCP server 'tools-server' not found in any file", hid)
			}

			// No rule or agent content files should exist.
			for path, content := range files {
				for _, marker := range []string{"Rule:", "Agent:"} {
					if strings.Contains(content, marker) {
						t.Errorf("%s: unexpected content marker %q in %s", hid, marker, path)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Plugins: end-to-end sync renders enabledPlugins / [plugins] tables
//
// Story: "A pack declares plugin descriptors, and after sync the harness
// reads its native plugin config." Default-marketplace plugins go in via the
// harness default; source-marketplace plugins also register the marketplace
// (Claude Code's known_marketplaces.json).
// ---------------------------------------------------------------------------

func TestPlugins_RenderedPerHarness(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	reg := testRegistry()

	profile := profileWith(packRoot)
	profile.Packs[0].Plugins = []domain.Plugin{
		{Name: "linear", Source: "github:linear/linear-codex-plugin", SourcePack: "test-pack"},
		{Name: "superpowers", Source: "github:obra/superpowers", Marketplace: "github:obra/superpowers-marketplace", SourcePack: "test-pack"},
	}

	// Claude Code and Codex render plugin references natively. Cline and
	// OpenCode do not have a plugin surface today; skip them.
	for _, hid := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			home := t.TempDir()

			syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
			projectFiles := collectFiles(t, projectDir)
			homeFiles := collectFiles(t, home)

			joined := func(files map[string]string) string {
				var b strings.Builder
				for _, content := range files {
					b.WriteString(content)
				}
				return b.String()
			}

			projectBlob := joined(projectFiles)
			homeBlob := joined(homeFiles)

			// Both bindings must appear in the rendered settings (settings.json
			// for Claude Code, config.toml for Codex). The marketplace name is
			// derived from Plugin.Marketplace ("source:owner/repo" → "repo").
			for _, want := range []string{"linear@", "superpowers@superpowers-marketplace"} {
				if !strings.Contains(projectBlob, want) {
					t.Errorf("%s: missing plugin binding %q in project files; got files=%v", hid, want, keys(projectFiles))
				}
			}

			// Source-marketplace plugins must register their marketplace.
			// Claude Code writes to home (~/.claude/plugins/known_marketplaces.json);
			// Codex inlines marketplace metadata in config.toml.
			combined := projectBlob + homeBlob
			if !strings.Contains(combined, "superpowers-marketplace") {
				t.Errorf("%s: missing source-marketplace registration for superpowers-marketplace", hid)
			}
		})
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// Test 21: Skill nested subdirectories preserved
//
// Story: "My skill has helper subdirectories — they survive the copy to
// the harness."
// ---------------------------------------------------------------------------

func TestSkillNested_SubdirectoriesPreserved(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()

	// Create a skill directory with nested structure on disk.
	skillDir := filepath.Join(packRoot, "skills", "deploy")
	templatesDir := filepath.Join(skillDir, "templates")
	libDir := filepath.Join(skillDir, "lib")
	for _, d := range []string{templatesDir, libDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skillFiles := map[string]string{
		filepath.Join(skillDir, "SKILL.md"):      "---\nname: deploy\n---\nDeploy skill.\n",
		filepath.Join(templatesDir, "prod.yaml"): "env: production\nreplicas: 3\n",
		filepath.Join(libDir, "helper.sh"):       "#!/bin/bash\necho deploy\n",
	}
	for path, content := range skillFiles {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Build profile with the nested skill.
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Skills: []domain.Skill{
				{Name: "deploy", DirPath: skillDir, SourcePack: "test-pack"},
			},
		}},
	}

	reg := testRegistry()
	hid := domain.HarnessClaudeCode
	projectDir := t.TempDir()
	home := t.TempDir()

	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, hid, reg)
	files := collectFiles(t, projectDir)

	// Verify all 3 files exist at destination with matching content.
	wantFiles := map[string]string{
		filepath.Join(".claude", "skills", "deploy", "SKILL.md"):               "---\nname: deploy\n---\nDeploy skill.\n",
		filepath.Join(".claude", "skills", "deploy", "templates", "prod.yaml"): "env: production\nreplicas: 3\n",
		filepath.Join(".claude", "skills", "deploy", "lib", "helper.sh"):       "#!/bin/bash\necho deploy\n",
	}
	for wantPath, wantContent := range wantFiles {
		got, ok := files[wantPath]
		if !ok {
			t.Errorf("expected %s to exist", wantPath)
			continue
		}
		if got != wantContent {
			t.Errorf("%s: content mismatch\n  got:  %q\n  want: %q", wantPath, got, wantContent)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 22: Dry run produces plan without writing
//
// Story: "I want to see what would change without committing."
// ---------------------------------------------------------------------------

func TestDryRun_ProducesPlanWithoutWriting(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := testProfile(t, packRoot)
	reg := testRegistry()
	hid := domain.HarnessClaudeCode
	projectDir := t.TempDir()
	home := t.TempDir()

	// Sync with DryRun: true.
	result, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{hid},
			Home:       home,
		},
		DryRun: true,
		Force:  true,
		Yes:    true,
		Quiet:  true,
	}, reg, nil, nil)
	if err != nil {
		t.Fatalf("RunSync dry-run: %v", err)
	}

	// Plan should have operations.
	totalOps := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings) + len(result.Plan.MCP)
	if totalOps == 0 {
		t.Error("dry-run plan has no operations — expected a non-trivial plan")
	}

	// No files should have been written to disk.
	files := collectFiles(t, projectDir)
	if len(files) != 0 {
		t.Errorf("dry-run should not write files: found %d files", len(files))
		for path := range files {
			t.Logf("  unexpected file: %s", path)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 23: Multiple agents each get their own TOML and config.toml registration
//
// Story: "My pack has three agents — each gets its own .toml file and a
// registration entry in config.toml."
// ---------------------------------------------------------------------------

func TestCodex_MultipleAgents_EachRegisteredInConfigTOML(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withAgents("reviewer", "deployer", "triage"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// Each agent must have a .toml file under .codex/agents/.
	for _, name := range []string{"reviewer", "deployer", "triage"} {
		tomlPath := filepath.Join(".codex", "agents", name+".toml")
		if _, ok := files[tomlPath]; !ok {
			t.Errorf("expected %s to exist", tomlPath)
		}
	}

	// config.toml must exist and contain a registration for each agent.
	configPath := filepath.Join(".codex", "config.toml")
	config, ok := files[configPath]
	if !ok {
		t.Fatal("expected .codex/config.toml to exist")
	}
	for _, name := range []string{"reviewer", "deployer", "triage"} {
		if !strings.Contains(config, name) {
			t.Errorf("config.toml missing agent registration for %q", name)
		}
		configFileRef := filepath.Join(projectDir, ".codex", "agents", name+".toml")
		if !strings.Contains(config, configFileRef) {
			t.Errorf("config.toml missing config_file reference %q", configFileRef)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 24: Agent with MCP reference — server config embedded in TOML
//
// Story: "My agent references an MCP server by name — the server config is
// embedded directly in the agent's TOML."
// ---------------------------------------------------------------------------

func TestCodex_AgentWithMCPReference_ServerEmbeddedInTOML(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()

	// Build a profile with an agent that references an MCP server.
	pack := domain.Pack{
		Name:    "test-pack",
		Version: "1.0.0",
		Root:    packRoot,
		Agents: []domain.Agent{
			{
				Name: "analyzer",
				Frontmatter: domain.AgentFrontmatter{
					Name:       "analyzer",
					MCPServers: []string{"lint-server"},
				},
				Body:       []byte("Analyze code.\n"),
				SourcePack: "test-pack",
			},
		},
	}
	profile := domain.Profile{
		Packs: []domain.Pack{pack},
		MCPServers: []domain.MCPServer{
			{
				Name:       "lint-server",
				Transport:  domain.TransportStdio,
				Command:    []string{"eslint", "--server"},
				SourcePack: "test-pack",
			},
		},
	}
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	tomlPath := filepath.Join(".codex", "agents", "analyzer.toml")
	content, ok := files[tomlPath]
	if !ok {
		t.Fatalf("expected %s to exist", tomlPath)
	}
	if !strings.Contains(content, "lint-server") {
		t.Error("agent TOML missing embedded MCP server name 'lint-server'")
	}
	if !strings.Contains(content, "eslint") {
		t.Error("agent TOML missing MCP server command 'eslint'")
	}
}

// ---------------------------------------------------------------------------
// Test 25: Rules and agents both rendered
//
// Story: "My pack has both rules and agents — Codex renders AGENTS.override.md
// for rules AND native .toml for agents."
// ---------------------------------------------------------------------------

func TestCodex_RulesAndAgents_BothRendered(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot,
		withRules("style", "security"),
		withAgents("reviewer"),
	)
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// Rules must appear in AGENTS.override.md.
	override, ok := files["AGENTS.override.md"]
	if !ok {
		t.Fatal("expected AGENTS.override.md to exist")
	}
	if !strings.Contains(override, "style") {
		t.Error("AGENTS.override.md missing rule 'style'")
	}
	if !strings.Contains(override, "security") {
		t.Error("AGENTS.override.md missing rule 'security'")
	}

	// Agent must be a native TOML file.
	tomlPath := filepath.Join(".codex", "agents", "reviewer.toml")
	if _, ok := files[tomlPath]; !ok {
		t.Error("expected .codex/agents/reviewer.toml to exist")
	}

	// config.toml must reference the agent.
	configPath := filepath.Join(".codex", "config.toml")
	config, ok := files[configPath]
	if !ok {
		t.Fatal("expected .codex/config.toml to exist")
	}
	if !strings.Contains(config, "reviewer") {
		t.Error("config.toml missing agent 'reviewer'")
	}
}

// ---------------------------------------------------------------------------
// Test 26: Agents only — no AGENTS.override.md created
//
// Story: "My pack only has agents, no rules — Codex should NOT create
// AGENTS.override.md since there are no rules to flatten."
// ---------------------------------------------------------------------------

func TestCodex_AgentsOnly_NoOverrideMDCreated(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withAgents("reviewer"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	// Agent TOML must exist.
	tomlPath := filepath.Join(".codex", "agents", "reviewer.toml")
	if _, ok := files[tomlPath]; !ok {
		t.Fatal("expected .codex/agents/reviewer.toml to exist")
	}

	// AGENTS.override.md must NOT exist — no rules to flatten.
	if _, ok := files["AGENTS.override.md"]; ok {
		t.Error("AGENTS.override.md should not exist when there are no rules")
	}
}

// ---------------------------------------------------------------------------
// Test 27: Global scope — correct paths
//
// Story: "I sync at global scope — Codex puts AGENTS.override.md inside
// .codex/ and agents in .codex/agents/."
// ---------------------------------------------------------------------------

func TestCodex_GlobalScope_CorrectPaths(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot,
		ruleWithContent("global-rule", "Global rule content.\n"),
		withAgents("reviewer"),
	)
	reg := testRegistry()

	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeGlobal, home, home, domain.HarnessCodex, reg)

	files := collectFiles(t, home)

	// At global scope, AGENTS.override.md goes inside .codex/ (not at root).
	globalOverride := filepath.Join(".codex", "AGENTS.override.md")
	content, ok := files[globalOverride]
	if !ok {
		t.Fatal("expected .codex/AGENTS.override.md to exist at global scope")
	}
	if !strings.Contains(content, "Global rule content") {
		t.Error(".codex/AGENTS.override.md missing rule content")
	}

	// Root-level AGENTS.override.md should NOT exist at global scope.
	if _, ok := files["AGENTS.override.md"]; ok {
		t.Error("AGENTS.override.md at root should not exist for global scope — should be inside .codex/")
	}

	// Agent TOML at .codex/agents/.
	tomlPath := filepath.Join(".codex", "agents", "reviewer.toml")
	if _, ok := files[tomlPath]; !ok {
		t.Error("expected .codex/agents/reviewer.toml at global scope")
	}

	// config.toml at .codex/.
	configPath := filepath.Join(".codex", "config.toml")
	if _, ok := files[configPath]; !ok {
		t.Error("expected .codex/config.toml at global scope")
	}
}

// ---------------------------------------------------------------------------
// Test 28: config.toml user settings preserved across re-sync
//
// Story: "I added custom_setting to my config.toml — re-sync shouldn't blow
// away my setting."
// ---------------------------------------------------------------------------

func TestCodex_ConfigTOML_UserSettingsPreservedAcrossResync(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	profile := profileWith(packRoot, withAgents("reviewer"))
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()

	// First sync — creates config.toml with agent registration.
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	// Inject a user setting at the top of config.toml (bare key before first table).
	configPath := filepath.Join(projectDir, ".codex", "config.toml")
	existing, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml after first sync: %v", err)
	}
	modified := append([]byte("custom_setting = true\n\n"), existing...)
	if err := os.WriteFile(configPath, modified, 0o644); err != nil {
		t.Fatalf("write modified config.toml: %v", err)
	}

	// Re-sync with force.
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	// Read config.toml again and verify user setting survived.
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml after re-sync: %v", err)
	}
	content := string(got)
	if !strings.Contains(content, "custom_setting") {
		t.Error("user setting 'custom_setting' lost after re-sync")
	}
	// Agent registration should also still be present.
	if !strings.Contains(content, "reviewer") {
		t.Error("agent registration 'reviewer' lost after re-sync")
	}
}

// ---------------------------------------------------------------------------
// Test 29: Rules sorted deterministically
//
// Story: "Rules always appear in alphabetical order in AGENTS.override.md,
// regardless of how they're ordered in my profile."
// ---------------------------------------------------------------------------

func TestCodex_RulesSortedDeterministically(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()
	// Add rules in reverse alphabetical order.
	profile := profileWith(packRoot,
		ruleWithContent("zulu", "Zulu content.\n"),
		ruleWithContent("alpha", "Alpha content.\n"),
		ruleWithContent("mike", "Mike content.\n"),
	)
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)
	override, ok := files["AGENTS.override.md"]
	if !ok {
		t.Fatal("expected AGENTS.override.md to exist")
	}

	// All three rules must appear, in alphabetical order.
	posAlpha := strings.Index(override, "Alpha content")
	posMike := strings.Index(override, "Mike content")
	posZulu := strings.Index(override, "Zulu content")
	if posAlpha == -1 || posMike == -1 || posZulu == -1 {
		t.Fatal("AGENTS.override.md missing one or more rule contents")
	}
	if !(posAlpha < posMike && posMike < posZulu) {
		t.Errorf("rules not sorted alphabetically: alpha@%d, mike@%d, zulu@%d", posAlpha, posMike, posZulu)
	}
}

// ---------------------------------------------------------------------------
// Test 30: Agent with skill reference — skills config in TOML
//
// Story: "My agent references a skill by name — the skill path appears in
// the agent's TOML as a skills config entry."
// ---------------------------------------------------------------------------

func TestCodex_AgentWithSkillReference_SkillsConfigInTOML(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()

	// Create a real skill directory on disk.
	skillDir := filepath.Join(packRoot, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: deploy\n---\nDeploy skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build profile with a skill and an agent that references it.
	pack := domain.Pack{
		Name:    "test-pack",
		Version: "1.0.0",
		Root:    packRoot,
		Skills: []domain.Skill{
			{Name: "deploy", DirPath: skillDir, SourcePack: "test-pack"},
		},
		Agents: []domain.Agent{
			{
				Name: "deployer",
				Frontmatter: domain.AgentFrontmatter{
					Name:   "deployer",
					Skills: []string{"deploy"},
				},
				Body:       []byte("Deploy agent.\n"),
				SourcePack: "test-pack",
			},
		},
	}
	profile := domain.Profile{Packs: []domain.Pack{pack}}
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()
	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	files := collectFiles(t, projectDir)

	tomlPath := filepath.Join(".codex", "agents", "deployer.toml")
	content, ok := files[tomlPath]
	if !ok {
		t.Fatalf("expected %s to exist", tomlPath)
	}
	if !strings.Contains(content, "deploy") {
		t.Error("agent TOML missing skill reference 'deploy'")
	}
	if !strings.Contains(content, "skills") {
		t.Error("agent TOML missing [skills] config section")
	}
}

// ---------------------------------------------------------------------------
// Test 31: Full round trip — sync then capture
//
// Story: "I sync a full profile (rules, agents, workflows, skills) to Codex,
// then capture. The captured content should reflect agents, skills, and
// promoted workflows."
// ---------------------------------------------------------------------------

func TestCodex_FullRoundTrip_SyncThenCapture(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()

	// Create a skill directory on disk.
	skillDir := filepath.Join(packRoot, "skills", "diagnose")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: diagnose\n---\nDiagnose issues.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Rules: []domain.Rule{
				{Name: "no-force-push", Raw: []byte("Never force push to main."), SourcePack: "test-pack"},
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
				{Name: "deploy", Body: []byte("Deploy to staging.\n"), Raw: []byte("---\nname: deploy\n---\nDeploy to staging.\n"), SourcePack: "test-pack"},
			},
			Skills: []domain.Skill{
				{Name: "diagnose", DirPath: skillDir, SourcePack: "test-pack"},
			},
		}},
	}

	reg := testRegistry()
	projectDir := t.TempDir()
	home := t.TempDir()

	syncAndApply(t, profile, domain.ScopeProject, projectDir, home, domain.HarnessCodex, reg)

	// Capture.
	h, err := reg.Lookup(domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	captureResult, err := h.Capture(context.Background(), harness.CaptureContext{
		Scope:      domain.ScopeProject,
		ProjectDir: projectDir,
		Home:       home,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Agents should be captured from native TOML.
	foundAgent := false
	for _, a := range captureResult.Agents {
		if a.Name == "reviewer" {
			foundAgent = true
			break
		}
	}
	if !foundAgent {
		t.Error("agent 'reviewer' not captured")
	}

	// Skills should retain their source ID after Codex rendered-name stripping.
	foundSkill := false
	for _, s := range captureResult.Skills {
		if s.Name == "diagnose" {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Error("skill 'diagnose' not captured")
	}

	// Promoted workflow should be captured as a workflow (via CapturePromotedContent).
	foundWorkflow := false
	for _, w := range captureResult.Workflows {
		if w.Name == "deploy" {
			foundWorkflow = true
			break
		}
	}
	if !foundWorkflow {
		t.Error("workflow 'deploy' not captured (expected promoted workflow to round-trip)")
	}

	// Rules are NOT captured for Codex — they are flattened into AGENTS.override.md
	// which is a one-way transform. This is documented behavior.
	if len(captureResult.Rules) > 0 {
		t.Error("expected no captured rules for Codex (rules are flattened, not individually recoverable)")
	}
}

// ---------------------------------------------------------------------------
// Test 32: Promotion collision — same-pack skill and workflow with same source name
//
// Story: "My pack has a skill and a workflow with the same name — Codex
// should refuse to sync because namespacing separates packs, not content types."
// ---------------------------------------------------------------------------

func TestCodex_PromotionCollision_SamePackSkillAndWorkflowSameName(t *testing.T) {
	t.Parallel()

	packRoot := t.TempDir()

	// Create a skill directory on disk.
	skillDir := filepath.Join(packRoot, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: deploy\n---\nDeploy skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Profile with both a skill and a workflow named "deploy".
	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:    "test-pack",
			Version: "1.0.0",
			Root:    packRoot,
			Skills: []domain.Skill{
				{Name: "deploy", DirPath: skillDir, SourcePack: "test-pack"},
			},
			Workflows: []domain.Workflow{
				{Name: "deploy", Body: []byte("Deploy workflow.\n"), Raw: []byte("---\nname: deploy\n---\nDeploy workflow.\n"), SourcePack: "test-pack"},
			},
		}},
	}
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()

	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessCodex},
			Home:       home,
			Namespaced: true,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
	}, reg, nil, nil)
	if err == nil {
		t.Fatal("expected RunSync to fail with a promotion collision")
	}
	if !strings.Contains(err.Error(), `name collision: skill "deploy" and workflow "deploy"`) {
		t.Fatalf("RunSync error = %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Layout contract tests
// ---------------------------------------------------------------------------

// TestLayoutContract_StructuralInvariants verifies that every harness's Layout
// satisfies structural contracts for both scopes: ValidationRoots are non-empty,
// OwnedFiles paths are absolute, and RemovePaths are under ValidationRoots.
func TestLayoutContract_StructuralInvariants(t *testing.T) {
	t.Parallel()
	reg := testRegistry()

	projectDir := t.TempDir()
	home := t.TempDir()

	for _, scope := range []domain.Scope{domain.ScopeProject, domain.ScopeGlobal} {
		baseDir := projectDir
		if scope == domain.ScopeGlobal {
			baseDir = home
		}
		for _, h := range reg.All() {
			name := string(h.ID()) + "/" + string(scope)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				l := h.Layout(scope, baseDir, home)

				// ValidationRoots must be non-empty — every harness writes somewhere.
				if len(l.ValidationRoots) == 0 {
					t.Error("ValidationRoots is empty")
				}

				// ValidationRoots must be absolute paths.
				for _, r := range l.ValidationRoots {
					if !filepath.IsAbs(r) {
						t.Errorf("ValidationRoot is not absolute: %q", r)
					}
				}

				// RemovePaths must be absolute.
				for _, rp := range l.RemovePaths {
					if !filepath.IsAbs(rp) {
						t.Errorf("RemovePath is not absolute: %q", rp)
					}
				}

				// Each RemovePath must be under at least one ValidationRoot.
				for _, rp := range l.RemovePaths {
					found := false
					for _, vr := range l.ValidationRoots {
						if rp == vr || strings.HasPrefix(rp, vr+string(filepath.Separator)) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("RemovePath %q is not under any ValidationRoot", rp)
					}
				}

				// OwnedFiles paths must be absolute.
				for _, of := range l.OwnedFiles {
					if !filepath.IsAbs(of.Path) {
						t.Errorf("OwnedFile path is not absolute: %q", of.Path)
					}
					if of.Strip == nil {
						t.Errorf("OwnedFile %q has nil Strip", of.Path)
					}
					if of.Reset == nil {
						t.Errorf("OwnedFile %q has nil Reset", of.Path)
					}
				}
			})
		}
	}
}
