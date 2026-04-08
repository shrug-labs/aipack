package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	return PackUpdate(context.Background(), req, &e.out)
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

func TestPackUpdate_Copy_ReCopies(t *testing.T) {
	t.Parallel()
	// Set up a source pack with a rule file.
	packDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "test-pack", Version: "1.0.0", Root: ".",
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

func TestPackUpdate_NotInstalled(t *testing.T) {
	t.Parallel()
	e := newUpdateEnv(t)
	results, _ := e.update(t, "nonexistent")
	if results[0].Status != StatusError {
		t.Fatalf("status = %q, want error", results[0].Status)
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
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(e.configDir))
	if sc.InstalledPacks["my-pack"].CommitHash != fakeHash2 {
		t.Fatalf("stored hash = %q", sc.InstalledPacks["my-pack"].CommitHash)
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
			SchemaVersion: 1, Name: "my-pack", Version: "1.0.0", Root: ".",
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
			SchemaVersion: 1, Name: "my-pack", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 1, Name: "pref-pack", Version: "1.0.0", Root: ".",
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
	}, &out)
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
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	meta := sc.InstalledPacks["pref-pack"]
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
		SchemaVersion: 1, Name: "copy-pack", Version: "1.0.0", Root: ".",
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
	}, &out)
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
