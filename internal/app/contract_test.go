package app

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Behavioral contracts for the sync system.
//
// Each test is a named guarantee, written from the user's perspective:
// "If I do X, then Y should hold." Tests use real harness implementations
// and exercise the full RunSync/RunClean pipeline.
//
// These tests focus on state transitions between syncs — the cases where
// the engine must make decisions, not just write files.
// ---------------------------------------------------------------------------

// Syncing the same profile twice produces identical filesystem state.
// Catches non-deterministic settings merge, duplicate file creation,
// and timestamp-dependent content.
func TestContract_SyncIsIdempotent(t *testing.T) {
	t.Parallel()
	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		profile := profileWith(env.packRoot,
			withRules("no-force-push", "test-first"),
			withAgents("reviewer"),
		)

		env.sync(profile)
		first := env.files()

		env.sync(profile)
		second := env.files()

		if len(first) != len(second) {
			t.Fatalf("file count changed: %d -> %d", len(first), len(second))
		}
		for path, content := range first {
			if got, ok := second[path]; !ok {
				t.Errorf("file disappeared on re-sync: %s", path)
			} else if got != content {
				t.Errorf("content changed on re-sync: %s", path)
			}
		}
		for path := range second {
			if _, ok := first[path]; !ok {
				t.Errorf("new file appeared on re-sync: %s", path)
			}
		}
	})
}

// Syncing a reduced profile removes content for a deleted rule.
// Works across per-file harnesses (file deleted) and bundle-file harnesses
// (content removed from the bundle).
func TestContract_SyncDeletesRemovedContent(t *testing.T) {
	t.Parallel()
	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		full := profileWith(env.packRoot, withRules("keep-this", "remove-this"))
		env.sync(full)

		if !env.contentExists("remove-this") {
			t.Fatal("precondition: 'remove-this' content not found after sync")
		}

		reduced := profileWith(env.packRoot, withRules("keep-this"))
		env.sync(reduced)

		if env.contentExists("remove-this") {
			t.Error("sync should have removed content for deleted rule")
		}
		if !env.contentExists("keep-this") {
			t.Error("sync removed surviving rule's content")
		}
	})
}

// Syncing a reduced profile cleans up stale content immediately and the
// ledger stays consistent.
func TestContract_SyncCleansUpImmediately(t *testing.T) {
	t.Parallel()
	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		full := profileWith(env.packRoot, withRules("keep-this", "doomed"))
		env.sync(full)

		if !env.contentExists("doomed") {
			t.Fatal("precondition: 'doomed' content not found after sync")
		}

		// First reduced sync should remove 'doomed' immediately.
		reduced := profileWith(env.packRoot, withRules("keep-this"))
		env.sync(reduced)

		if env.contentExists("doomed") {
			t.Error("stale content should be removed on first reduced sync")
		}

		// A second sync should be a no-op — no orphans, no surprises.
		env.sync(reduced)

		if env.contentExists("doomed") {
			t.Error("stale content reappeared after second sync")
		}
		if !env.contentExists("keep-this") {
			t.Error("surviving content was removed")
		}
	})
}

// Re-syncing with changed rule content updates the managed output.
func TestContract_ResyncReflectsContentChanges(t *testing.T) {
	t.Parallel()
	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		v1 := profileWith(env.packRoot, ruleWithContent("evolving", "version-one-marker\n"))
		env.sync(v1)

		if !env.contentExists("version-one-marker") {
			t.Fatal("precondition: v1 content not found after sync")
		}

		v2 := profileWith(env.packRoot, ruleWithContent("evolving", "version-two-marker\n"))
		env.sync(v2)

		if env.contentExists("version-one-marker") {
			t.Error("old content still present after re-sync")
		}
		if !env.contentExists("version-two-marker") {
			t.Error("new content not present after re-sync")
		}
	})
}

// Removing one pack from a multi-pack profile removes only that pack's
// content. The surviving pack is untouched.
func TestContract_MultiPackRemovalPreservesSurvivingPack(t *testing.T) {
	t.Parallel()
	forAllHarnesses(t, func(t *testing.T, env *contractEnv) {
		both := multiPackProfile(env.packRoot,
			packWith("core", withRules("core-rule")),
			packWith("extras", withRules("extras-rule")),
		)
		env.sync(both)

		if !env.contentExists("core-rule") || !env.contentExists("extras-rule") {
			t.Fatal("precondition: both packs' rule content should exist")
		}

		coreOnly := multiPackProfile(env.packRoot,
			packWith("core", withRules("core-rule")),
		)
		env.sync(coreOnly)

		if env.contentExists("extras-rule") {
			t.Error("removed pack's rule content should be gone after sync")
		}
		if !env.contentExists("core-rule") {
			t.Error("surviving pack's rule content was removed")
		}
	})
}
