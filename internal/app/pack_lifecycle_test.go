package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"

	"gopkg.in/yaml.v3"
)

func TestPackDelete(t *testing.T) {
	t.Parallel()

	t.Run("removes from disk", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
		}, &out)

		out.Reset()
		if _, err := PackDelete(configDir, "test-pack", &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(configDir, "packs", "test-pack")); !os.IsNotExist(err) {
			t.Fatal("pack should be removed")
		}
	})

	t.Run("not installed", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		_, err := PackDelete(t.TempDir(), "nonexistent", &out)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("clears origin from sync-config", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		lf, _ := config.LoadLockfile(config.LockfilePath(configDir))
		if _, ok := lf.Packs["test-pack"]; !ok {
			t.Fatal("origin should exist before remove")
		}

		out.Reset()
		PackDelete(configDir, "test-pack", &out)
		lf, _ = config.LoadLockfile(config.LockfilePath(configDir))
		if _, ok := lf.Packs["test-pack"]; ok {
			t.Fatal("origin should be cleared")
		}
	})

	t.Run("removes from profile", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeEmptyProfile(t, configDir, "default")
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			Add: true, Profile: "default",
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		out.Reset()
		PackDelete(configDir, "test-pack", &out)

		b, _ := os.ReadFile(filepath.Join(configDir, "profiles", "default.yaml"))
		var cfg config.ProfileConfig
		yaml.Unmarshal(b, &cfg)
		for _, p := range cfg.Packs {
			if p.Name == "test-pack" {
				t.Fatal("pack entry should be removed from profile")
			}
		}
		if !strings.Contains(out.String(), "Removed") {
			t.Fatalf("output = %s", out.String())
		}
	})

	t.Run("returns bundled profiles", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()

		m := map[string]any{
			"schema_version": 2, "name": "test-pack", "version": "1.0.0",
			"root": ".", "profiles": []string{"dev"},
		}
		b, _ := json.Marshal(m)
		os.MkdirAll(packDir, 0o700)
		os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600)
		os.MkdirAll(filepath.Join(packDir, "profiles"), 0o700)
		os.WriteFile(filepath.Join(packDir, "profiles", "dev.yaml"),
			[]byte("schema_version: 1\npacks: []\n"), 0o600)

		writeEmptyProfile(t, configDir, "default")
		writeTestSyncConfig(t, configDir)

		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			Add: true, Profile: "default",
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		teamProfile := filepath.Join(configDir, "profiles", "dev.yaml")
		if _, err := os.Stat(teamProfile); err != nil {
			t.Fatalf("bundled profile should exist: %v", err)
		}

		out.Reset()
		result, _ := PackDelete(configDir, "test-pack", &out)

		if len(result.BundledProfiles) != 1 || result.BundledProfiles[0] != "dev" {
			t.Fatalf("BundledProfiles = %v", result.BundledProfiles)
		}
		// Still on disk (caller must remove).
		if _, err := os.Stat(teamProfile); err != nil {
			t.Fatal("profile should still exist before explicit removal")
		}

		out.Reset()
		RemoveBundledProfiles(configDir, result.BundledProfiles, &out)
		if _, err := os.Stat(teamProfile); !os.IsNotExist(err) {
			t.Fatal("profile should be removed")
		}
		if !strings.Contains(out.String(), "Removed bundled profile") {
			t.Fatalf("output = %s", out.String())
		}
	})
}

func TestPackAdd_QuietUpdatesExistingEntry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	var out bytes.Buffer
	PackAdd(configDir, "default", "my-pack", nil, &out)

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	for _, p := range cfg.Packs {
		if p.Name == "my-pack" && p.Quiet {
			t.Fatal("should not be quiet initially")
		}
	}

	out.Reset()
	t2 := true
	PackAdd(configDir, "default", "my-pack", &t2, &out)

	cfg, _ = config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "my-pack" {
			found = &cfg.Packs[i]
		}
	}
	if found == nil || !found.Quiet {
		t.Fatal("expected Quiet=true after re-add")
	}
}

// TestPackAdd_InheritsInstallQuietFromLockfile pins the feature: once a
// pack is installed with InstallQuiet=true on its lockfile meta, every
// profile add of that pack defaults to quiet without needing `-q` again.
// This is the v0.25.0 fix for "I thought installing -q made the pack
// quiet-by-nature" — the quiet intent now persists on the lockfile and
// PackAdd consults it when creating a new profile entry.
func TestPackAdd_InheritsInstallQuietFromLockfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	// Simulate a pack installed with -q by writing its lockfile meta.
	if err := packRecordOrigin(configDir, "quiet-pack", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	// PackAdd without an explicit override — should inherit Quiet=true.
	var out bytes.Buffer
	if err := PackAdd(configDir, "default", "quiet-pack", nil, &out); err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "quiet-pack" {
			found = &cfg.Packs[i]
		}
	}
	if found == nil {
		t.Fatal("pack not added to profile")
	}
	if !found.Quiet {
		t.Error("new profile entry should inherit Quiet=true from lockfile InstallQuiet")
	}
}

// TestPackAdd_NoQuietOverrideDemotesQuietInstall verifies the explicit
// demote path: `pack add --no-quiet` (quietOverride = &false) forces the
// profile entry to non-quiet even when the lockfile says the pack was
// installed quietly.
func TestPackAdd_NoQuietOverrideDemotesQuietInstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	if err := packRecordOrigin(configDir, "pack-x", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	var out bytes.Buffer
	f := false
	if err := PackAdd(configDir, "default", "pack-x", &f, &out); err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	for _, p := range cfg.Packs {
		if p.Name == "pack-x" && p.Quiet {
			t.Error("--no-quiet override should force Quiet=false even when InstallQuiet=true")
		}
	}
}

// TestResolveInstallQuiet_PreservesOnReinstall pins the re-install
// preservation rule: when no explicit flag is passed, a re-install keeps
// the existing InstallQuiet so `pack install --add` (no -q) doesn't
// silently demote a pack that was originally installed with -q.
func TestResolveInstallQuiet_PreservesOnReinstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	if err := packRecordOrigin(configDir, "preserve-pack", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	if got := resolveInstallQuiet(configDir, "preserve-pack", nil); !got {
		t.Error("nil override on a pack with InstallQuiet=true should return true (preserve)")
	}
	f := false
	if got := resolveInstallQuiet(configDir, "preserve-pack", &f); got {
		t.Error("*false override should force InstallQuiet=false regardless of lockfile")
	}
	if got := resolveInstallQuiet(configDir, "unknown-pack", nil); got {
		t.Error("unknown pack with nil override should default to false")
	}
}

func TestProfileMissingPacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		packs    []string
		disabled []string
		install  []string // packs to install on disk
		wantN    int
		wantName string // first expected missing name (empty = skip check)
	}{
		{"mixed", []string{"alpha", "beta", "gamma"}, nil, []string{"alpha"}, 2, "beta"},
		{"all present", []string{"alpha", "beta"}, nil, []string{"alpha", "beta"}, 0, ""},
		{"disabled skipped", []string{"alpha"}, []string{"beta"}, nil, 1, "alpha"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			if len(tc.disabled) > 0 {
				writeShowTestProfileWithDisabled(t, configDir, "test", tc.packs, tc.disabled)
			} else {
				writeShowTestProfile(t, configDir, "test", tc.packs)
			}
			for _, name := range tc.install {
				installShowPack(t, configDir, name)
			}
			missing, err := ProfileMissingPacks(configDir, "test")
			if err != nil {
				t.Fatal(err)
			}
			if len(missing) != tc.wantN {
				t.Fatalf("got %d missing %v, want %d", len(missing), missing, tc.wantN)
			}
			if tc.wantName != "" && (len(missing) == 0 || missing[0] != tc.wantName) {
				t.Errorf("first missing = %v, want %q", missing, tc.wantName)
			}
		})
	}
}

func TestPackInstallMissing(t *testing.T) {
	t.Parallel()

	t.Run("all present", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")
		installShowPack(t, configDir, "beta")

		var out bytes.Buffer
		results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test",
		}, &out)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if r.Status != MissingStatusPresent {
				t.Errorf("%s: got %q, want present", r.Pack, r.Status)
			}
		}
	})

	t.Run("not in registry", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")

		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test",
		}, &out)
		if results[1].Status != MissingStatusNotInRegistry {
			t.Errorf("beta: got %q", results[1].Status)
		}
	})

	t.Run("installs from registry", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"beta": {Repo: "https://example.com/repo.git", Ref: "main", Path: "packs/beta"},
		})

		var captured PackInstallRequest
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			captured = req
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if results[1].Status != MissingStatusInstalled {
			t.Errorf("beta: got %q", results[1].Status)
		}
		if captured.URL != "https://example.com/repo.git" || captured.Ref != "main" || captured.SubPath != "packs/beta" {
			t.Errorf("PackInstall called with URL=%q Ref=%q SubPath=%q", captured.URL, captured.Ref, captured.SubPath)
		}
		if captured.Add {
			t.Error("Add should be false")
		}
	})

	t.Run("propagates content paths and quiet", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"ext-catalog"})
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"ext-catalog": {
				Repo: "https://example.com/mono.git", Quiet: true,
				ContentPaths: map[domain.PackCategory]string{
					domain.CategorySkills: "tools/agent/skills",
					domain.CategoryRules:  "tools/agent/rules",
				},
			},
		})
		var captured PackInstallRequest
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			captured = req
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if captured.Quiet == nil || !*captured.Quiet {
			t.Error("Quiet not propagated")
		}
		if captured.ContentPaths[domain.CategorySkills] != "tools/agent/skills" {
			t.Errorf("skills path = %q", captured.ContentPaths[domain.CategorySkills])
		}
	})

	t.Run("error does not stop others", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta", "gamma"})
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"alpha": {Repo: "https://example.com/a.git", Ref: "main"},
			"beta":  {Repo: "https://example.com/b.git", Ref: "main"},
			"gamma": {Repo: "https://example.com/c.git", Ref: "main"},
		})
		calls := 0
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			calls++
			if req.Name == "beta" {
				return fmt.Errorf("simulated failure")
			}
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
		want := []MissingStatus{MissingStatusInstalled, MissingStatusError, MissingStatusInstalled}
		for i, r := range results {
			if r.Status != want[i] {
				t.Errorf("[%d] %s: got %q, want %q", i, r.Pack, r.Status, want[i])
			}
		}
	})
}

// TestPackRename_RebuildsResolvedInventory asserts that after renaming a
// pack, the lockfile entry under the new name has a non-nil Resolved
// inventory. Also verifies the backfill case: a rename of a pre-v0.22
// install (whose lockfile entry lacks Resolved) produces a populated
// baseline under the new name. Regression guard for finding #3 in
// projects/aipack-pack-update-bugs-2026-04-14.md.
func TestPackRename_RebuildsResolvedInventory(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "rename-src")
	if err := os.WriteFile(filepath.Join(packDir, "rules", "rule-a.md"), []byte("# rule-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: false,
	}, &out); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Simulate pre-v0.22: clear Resolved so the rename path has to
	// backfill rather than carry forward.
	lfPath := config.LockfilePath(configDir)
	lf, err := config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["rename-src"]
	meta.Resolved = nil
	lf.Packs["rename-src"] = meta
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(nil, nil)
	if err := PackRename(eng, configDir, "rename-src", "rename-dst", &out); err != nil {
		t.Fatalf("PackRename: %v", err)
	}

	lf, err = config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lf.Packs["rename-src"]; ok {
		t.Error("old name still in lockfile")
	}
	got, ok := lf.Packs["rename-dst"]
	if !ok {
		t.Fatal("new name missing from lockfile")
	}
	if got.Resolved == nil {
		t.Fatal("Resolved is nil after rename — rebuild did not fire")
	}
	if !slices.Contains(got.Resolved.Rules, "rule-a") {
		t.Errorf("Resolved.Rules = %v, want to contain rule-a", got.Resolved.Rules)
	}
}
