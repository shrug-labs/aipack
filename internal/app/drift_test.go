package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// seedLockfileWithInventory writes a lockfile for one pack with the given
// previous inventory recorded. Used by drift-detection tests.
func seedLockfileWithInventory(t *testing.T, configDir, packName string, inv domain.PackInventory) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			packName: {
				Origin:      "local",
				Method:      "local",
				InstalledAt: "2026-04-01T00:00:00Z",
				Ref:         "v0.1.0",
				Resolved:    &inv,
			},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), lf); err != nil {
		t.Fatalf("SaveLockfile: %v", err)
	}
}

// driftProfile builds a minimal resolved profile with the given rule/skill
// set for one pack. Used as the "current" state in drift tests.
func driftProfile(packName, packRoot string, rules, skills []string) domain.Profile {
	var rs []domain.Rule
	for _, id := range rules {
		rs = append(rs, domain.Rule{Name: id, Raw: []byte("body"), SourcePack: packName})
	}
	var ss []domain.Skill
	for _, id := range skills {
		ss = append(ss, domain.Skill{Name: id, DirPath: packRoot, SourcePack: packName})
	}
	return domain.Profile{
		Packs: []domain.Pack{{
			Name:   packName,
			Root:   packRoot,
			Rules:  rs,
			Skills: ss,
		}},
	}
}

func seedDriftPackOnDisk(t *testing.T, packRoot string, rules []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packRoot, filepath.Base(packRoot))
	for _, rule := range rules {
		path := filepath.Join(packRoot, "rules", rule+".md")
		if err := os.WriteFile(path, []byte("---\nname: "+rule+"\n---\nbody\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunSync_DriftOutput_RemovedRuleAppearsInReport(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packRoot := filepath.Join(configDir, "packs", "my-pack")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedDriftPackOnDisk(t, packRoot, []string{"rule-a"})

	// Previous inventory had two rules; profile will see only one now.
	seedLockfileWithInventory(t, configDir, "my-pack", domain.PackInventory{
		Rules: []string{"rule-a", "rule-b"},
	})

	profile := driftProfile("my-pack", packRoot, []string{"rule-a"}, nil)
	// Simulate that the profile still references the removed rule — a
	// BrokenRef for rule-b, which the sync drift output should mark as
	// "affects your profile."
	profile.BrokenRefs = []domain.BrokenRef{
		{PackName: "my-pack", Category: domain.CategoryRules, Direction: "include", ID: "rule-b"},
	}

	var stdout bytes.Buffer
	reg := testRegistry()
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir: configDir,
			Scope:     domain.ScopeGlobal,
			Harnesses: []domain.Harness{domain.HarnessCodex},
			Home:      home,
		},
		Force:  true,
		Yes:    true,
		Quiet:  true,
		DryRun: true, // dry-run is enough: drift output runs before the dry-run early return
		NowFn:  func() time.Time { return time.Unix(0, 0) },
	}, reg, &stdout, &stdout)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	out := stdout.String()
	wantSubstrings := []string{
		"Drift detected in my-pack",
		"Removed (affects your profile):",
		"rule-b",
		"referenced via rules.include",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("drift output missing %q:\n---\n%s---", s, out)
		}
	}
}

func TestRunSync_DriftWriteback_AfterSuccessfulApply(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packRoot := filepath.Join(configDir, "packs", "my-pack")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedDriftPackOnDisk(t, packRoot, []string{"rule-a", "rule-b"})

	// Seed lockfile with an old inventory.
	seedLockfileWithInventory(t, configDir, "my-pack", domain.PackInventory{
		Rules: []string{"rule-a", "rule-b"},
	})

	// Current profile has only rule-a.
	profile := driftProfile("my-pack", packRoot, []string{"rule-a"}, nil)

	reg := testRegistry()
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir: configDir,
			Scope:     domain.ScopeGlobal,
			Harnesses: []domain.Harness{domain.HarnessCodex},
			Home:      home,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
		NowFn: func() time.Time { return time.Unix(1713000000, 0).UTC() },
	}, reg, new(bytes.Buffer), new(bytes.Buffer))
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	// Reload lockfile and check that the inventory has been updated to
	// the full pack contents on disk, not the selector-filtered profile
	// subset.
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["my-pack"]
	if !ok {
		t.Fatal("my-pack entry missing from lockfile after sync")
	}
	if meta.Resolved == nil {
		t.Fatal("Resolved inventory not written back after sync")
	}
	if got, want := meta.Resolved.Rules, []string{"rule-a", "rule-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("lockfile Resolved.Rules = %v, want %v", got, want)
	}
}

func TestRunSync_DriftWriteback_SkippedOnDryRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packRoot := filepath.Join(configDir, "packs", "my-pack")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedDriftPackOnDisk(t, packRoot, []string{"rule-a", "rule-b"})

	oldInv := domain.PackInventory{Rules: []string{"rule-a", "rule-b"}}
	seedLockfileWithInventory(t, configDir, "my-pack", oldInv)

	profile := driftProfile("my-pack", packRoot, []string{"rule-a"}, nil)

	reg := testRegistry()
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir: configDir,
			Scope:     domain.ScopeGlobal,
			Harnesses: []domain.Harness{domain.HarnessCodex},
			Home:      home,
		},
		DryRun: true,
		Force:  true,
		Yes:    true,
		Quiet:  true,
		NowFn:  func() time.Time { return time.Unix(0, 0) },
	}, reg, new(bytes.Buffer), new(bytes.Buffer))
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	// Lockfile should still show the old inventory — dry-run must not write.
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Resolved == nil {
		t.Fatal("Resolved inventory disappeared")
	}
	if len(meta.Resolved.Rules) != 2 {
		t.Errorf("dry-run mutated lockfile: Rules = %v, want [rule-a rule-b]", meta.Resolved.Rules)
	}
}

func TestRunSync_DriftOutput_NoBaselineSkipsSilently(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packRoot := filepath.Join(configDir, "packs", "my-pack")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedDriftPackOnDisk(t, packRoot, []string{"rule-a"})

	// No lockfile seeded — first sync. Drift output should be empty.
	profile := driftProfile("my-pack", packRoot, []string{"rule-a"}, nil)

	var stdout bytes.Buffer
	reg := testRegistry()
	_, _, err := RunSync(context.Background(), engine.New(nil, nil), profile, SyncRequest{
		TargetSpec: TargetSpec{
			ConfigDir: configDir,
			Scope:     domain.ScopeGlobal,
			Harnesses: []domain.Harness{domain.HarnessCodex},
			Home:      home,
		},
		DryRun: true,
		Force:  true,
		Yes:    true,
		Quiet:  true,
		NowFn:  func() time.Time { return time.Unix(0, 0) },
	}, reg, &stdout, &stdout)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if strings.Contains(stdout.String(), "Drift detected") {
		t.Errorf("unexpected drift output on first sync:\n%s", stdout.String())
	}
}
