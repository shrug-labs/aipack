package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// TestOpenCodeMigration_LegacyPackSourcePathsPruned verifies the upgrade
// path for users who previously synced with an aipack version that wrote
// pack-source globs into opencode.json's `instructions` array and
// `skills.paths` array. Those entries pointed at each pack's source
// directory, which bypassed profile enable/disable — OpenCode at runtime
// saw every rule in the pack, regardless of what the profile selected.
//
// Upgrading to the current version should, on the next sync:
//   - remove the legacy pack-source entries (they live in the ledger's
//     previous managed overlay, so the three-way merge recognizes them
//     as aipack-owned and safe to prune)
//   - add a single entry pointing at aipack's rendered managed directory,
//     which contains only the profile-selected content
//   - preserve any user-added entries that were NOT in the previous overlay
//
// The test drives the migration through the public sync pipeline
// (RunSync) with a real on-disk ledger and opencode.json, then asserts
// the observable outcome on disk. It intentionally avoids importing any
// engine-internal types (ComputeSettingsDiffs, MergeOp, PrevManagedOverlay
// types, etc.) so it survives refactoring of the three-way merge
// mechanism itself — it tests behavior, not implementation.
func TestOpenCodeMigration_LegacyPackSourcePathsPruned(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	packsParent := t.TempDir()
	packRootA := filepath.Join(packsParent, "pack-a")
	packRootB := filepath.Join(packsParent, "pack-b")

	// Create pack source directories. A skill for pack-a needs a real
	// on-disk SKILL.md so the copy phase succeeds.
	skillDirA := filepath.Join(packRootA, "skills", "deploy")
	if err := os.MkdirAll(skillDirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDirA, "SKILL.md"),
		[]byte("---\nname: deploy\n---\nDeploy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{packRootA, packRootB} {
		if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Main-era managed content: one glob + one dir per pack's rules, and
	// one pack-source skills dir per pack. This is exactly the shape the
	// pre-v0.20.1 opencode harness wrote into opencode.json.
	legacyGlobA := filepath.Join(packRootA, "rules", "*.md")
	legacyDirA := filepath.Join(packRootA, "rules")
	legacyGlobB := filepath.Join(packRootB, "rules", "*.md")
	legacyDirB := filepath.Join(packRootB, "rules")
	legacyInstructions := []string{legacyGlobA, legacyDirA, legacyGlobB, legacyDirB}

	legacySkillsA := filepath.Join(packRootA, "skills")
	legacySkillsB := filepath.Join(packRootB, "skills")
	legacySkillsPaths := []string{legacySkillsA, legacySkillsB}

	// User-added entries that must survive the migration.
	userInstr := filepath.Join(home, "my-hand-written-instructions.md")
	userSkillsDir := filepath.Join(home, "my-custom-skills")

	// Pre-state on disk: opencode.json with both legacy managed content
	// and user entries interleaved. Matches what a user upgrading from
	// main would have at the moment they run `aipack sync`.
	opencodeDir := filepath.Join(projectDir, ".opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(opencodeDir, "opencode.json")
	preSyncDoc := map[string]any{
		"instructions": append(append([]string{}, legacyInstructions...), userInstr),
		"skills": map[string]any{
			"paths": append(append([]string{}, legacySkillsPaths...), userSkillsDir),
		},
	}
	preSyncBytes, err := json.MarshalIndent(preSyncDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, preSyncBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-state ledger: an entry for opencode.json with ManagedOverlay
	// containing only the legacy managed content (no user entries). This
	// is the record of "what aipack previously wrote", which is how the
	// apply-time three-way merge distinguishes removable managed entries
	// from user content it must preserve.
	overlayDoc := map[string]any{
		"instructions": legacyInstructions,
		"skills":       map[string]any{"paths": legacySkillsPaths},
	}
	overlayBytes, err := json.MarshalIndent(overlayDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t,
		testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessOpenCode),
		map[string]domain.Entry{
			settingsPath: {
				Digest:         domain.SingleFileDigest(preSyncBytes),
				ManagedOverlay: overlayBytes,
			},
		})

	// Profile with two packs. Only pack-a has a skill; both have rules.
	// Rule.SourcePath points at a real file in the pack's rules dir — this
	// is what earlier aipack versions relied on to decide whether a pack's
	// rules dir should contribute a glob entry to the instructions array.
	// Without it, the realistic main-era code path doesn't fire, so we'd
	// be testing a degenerate scenario no real user hits.
	ruleSrcA := filepath.Join(packRootA, "rules", "alpha.md")
	ruleSrcB := filepath.Join(packRootB, "rules", "beta.md")
	if err := os.WriteFile(ruleSrcA, []byte("A rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ruleSrcB, []byte("B rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := domain.Profile{Packs: []domain.Pack{
		{
			Name: "pack-a", Version: "1.0.0", Root: packRootA,
			Rules: []domain.Rule{
				{Name: "alpha", Raw: []byte("A rule.\n"), SourcePack: "pack-a", SourcePath: ruleSrcA},
			},
			Skills: []domain.Skill{
				{Name: "deploy", DirPath: skillDirA, SourcePack: "pack-a"},
			},
		},
		{
			Name: "pack-b", Version: "1.0.0", Root: packRootB,
			Rules: []domain.Rule{
				{Name: "beta", Raw: []byte("B rule.\n"), SourcePack: "pack-b", SourcePath: ruleSrcB},
			},
		},
	}}

	_, _, err = RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessOpenCode},
			Home:       home,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
	}, testRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	postBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read post-sync opencode.json: %v", err)
	}
	var post map[string]any
	if err := json.Unmarshal(postBytes, &post); err != nil {
		t.Fatalf("unmarshal post-sync opencode.json: %v\ncontent:\n%s", err, postBytes)
	}

	// --- instructions assertions -----------------------------------------

	instrAny, ok := post["instructions"].([]any)
	if !ok {
		t.Fatalf("instructions not []any: %T = %v", post["instructions"], post["instructions"])
	}
	instrSet := anyArrayStringSet(instrAny)

	for _, legacy := range legacyInstructions {
		if instrSet[legacy] {
			t.Errorf("legacy pack-source instruction not pruned: %s\nfinal instructions: %v",
				legacy, instrAny)
		}
	}

	renderedRulesGlob := filepath.Join(projectDir, ".opencode", "rules", "*.md")
	if !instrSet[renderedRulesGlob] {
		t.Errorf("rendered rules glob missing from instructions\nwant: %s\ngot: %v",
			renderedRulesGlob, instrAny)
	}

	if !instrSet[userInstr] {
		t.Errorf("user-added instruction entry not preserved\nwant: %s\ngot: %v",
			userInstr, instrAny)
	}

	// --- skills.paths assertions -----------------------------------------

	skillsObj, ok := post["skills"].(map[string]any)
	if !ok {
		t.Fatalf("skills not map: %T", post["skills"])
	}
	skillsPathsAny, ok := skillsObj["paths"].([]any)
	if !ok {
		t.Fatalf("skills.paths not []any: %T", skillsObj["paths"])
	}
	skillsSet := anyArrayStringSet(skillsPathsAny)

	for _, legacy := range legacySkillsPaths {
		if skillsSet[legacy] {
			t.Errorf("legacy pack-source skills path not pruned: %s\nfinal skills.paths: %v",
				legacy, skillsPathsAny)
		}
	}

	renderedSkillsDir := filepath.Join(projectDir, ".opencode", "skills")
	if !skillsSet[renderedSkillsDir] {
		t.Errorf("rendered skills dir missing from skills.paths\nwant: %s\ngot: %v",
			renderedSkillsDir, skillsPathsAny)
	}

	if !skillsSet[userSkillsDir] {
		t.Errorf("user-added skills path not preserved\nwant: %s\ngot: %v",
			userSkillsDir, skillsPathsAny)
	}
}

// anyArrayStringSet extracts the string elements of a []any into a set
// keyed by string. Non-string elements are ignored — the caller has
// already asserted the array element shape.
func anyArrayStringSet(arr []any) map[string]bool {
	set := map[string]bool{}
	for _, v := range arr {
		if s, ok := v.(string); ok {
			set[s] = true
		}
	}
	return set
}
