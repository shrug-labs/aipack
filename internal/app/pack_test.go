package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/testutil"

	"gopkg.in/yaml.v3"
)

var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

func writePackManifest(t *testing.T, dir string, name string) {
	t.Helper()
	m := map[string]any{
		"schema_version": 2,
		"name":           name,
		"version":        "1.0.0",
		"root":           ".",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeEmptyProfile(t *testing.T, configDir string, profileName string) {
	t.Helper()
	profileDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "schema_version: 1\npacks: []\n"
	if err := os.WriteFile(filepath.Join(profileDir, profileName+".yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildPackZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPackInstall_EmitsEvents(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "evt-pack")

	events := make(chan PackInstallEvent, 8)
	go func() {
		// Drain so the install goroutine never blocks if we miscounted.
		for range events {
		}
	}()
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Events:    events,
	}, nil)
	close(events)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Re-run with a synchronous collector to verify the phase sequence.
	configDir2 := t.TempDir()
	collected := []PackInstallEvent{}
	collectCh := make(chan PackInstallEvent, 8)
	done := make(chan struct{})
	go func() {
		for ev := range collectCh {
			collected = append(collected, ev)
		}
		close(done)
	}()
	err = PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir2,
		Link:      true,
		Events:    collectCh,
	}, nil)
	close(collectCh)
	<-done
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Must start with Starting and end with Done. Linking flow must emit
	// the Linking phase between them. No Err event on success.
	if len(collected) < 3 {
		t.Fatalf("expected ≥3 events, got %d: %+v", len(collected), collected)
	}
	if collected[0].Phase != PackInstallPhaseStarting {
		t.Errorf("first event phase = %q, want %q", collected[0].Phase, PackInstallPhaseStarting)
	}
	last := collected[len(collected)-1]
	if !last.Done || last.Err != nil {
		t.Errorf("last event = %+v, want Done=true Err=nil", last)
	}
	sawLinking := false
	for _, ev := range collected {
		if ev.Phase == PackInstallPhaseLinking {
			sawLinking = true
		}
		if ev.Err != nil {
			t.Errorf("unexpected Err on event %+v", ev)
		}
	}
	if !sawLinking {
		t.Errorf("expected a Linking phase in: %+v", collected)
	}
}

func TestPackInstall_Link(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeEmptyProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       true,
		Profile:   "default",
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "test-pack")
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink, got %v", fi.Mode())
	}
	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != packDir {
		t.Fatalf("symlink target = %q, want %q", target, packDir)
	}

	// Verify profile was updated.
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	b, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var cfg config.ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if len(cfg.Packs) != 1 || cfg.Packs[0].Name != "test-pack" {
		t.Fatalf("expected 1 pack named test-pack, got %+v", cfg.Packs)
	}
}

func TestPackInstall_Copy(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	// Also write a content file to verify it's copied.
	rulesDir := filepath.Join(packDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "test-rule.md"), []byte("# Test Rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      false,
		Add:       false,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "test-pack")
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected copy (not symlink)")
	}

	// Verify content was copied.
	got, err := os.ReadFile(filepath.Join(dest, "rules", "test-rule.md"))
	if err != nil {
		t.Fatalf("read copied rule: %v", err)
	}
	if string(got) != "# Test Rule\n" {
		t.Fatalf("copied content = %q, want %q", got, "# Test Rule\n")
	}
}

// TestPackInstall_PopulatesResolvedInventory asserts that pack install
// writes a non-nil Resolved inventory to the lockfile, so drift detection
// and doctor's broken_refs check work against a just-installed pack
// without waiting for the first sync. Regression guard for finding #3
// in projects/aipack-pack-update-bugs-2026-04-14.md.
func TestPackInstall_PopulatesResolvedInventory(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()

	// Build a pack with one rule, one workflow, and one skill so the
	// inventory has something to capture in every vector.
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "skills", "demo-skill"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": 2,
		"name":           "resolved-test",
		"version":        "0.1.0",
		"root":           ".",
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "rule-a.md"), []byte("# rule-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "workflows", "wf-a.md"), []byte("# wf-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "skills", "demo-skill", "SKILL.md"),
		[]byte("---\nname: demo-skill\ndescription: demo skill for test\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["resolved-test"]
	if !ok {
		t.Fatal("pack not recorded in lockfile")
	}
	if meta.Resolved == nil {
		t.Fatal("meta.Resolved is nil — should be populated at install time")
	}
	if !slices.Contains(meta.Resolved.Rules, "rule-a") {
		t.Errorf("Resolved.Rules = %v, want to contain rule-a", meta.Resolved.Rules)
	}
	if !slices.Contains(meta.Resolved.Workflows, "wf-a") {
		t.Errorf("Resolved.Workflows = %v, want to contain wf-a", meta.Resolved.Workflows)
	}
	if _, ok := meta.Resolved.Skills["demo-skill"]; !ok {
		t.Errorf("Resolved.Skills missing demo-skill: %v", meta.Resolved.Skills)
	}
}

func TestPackInstall_Copy_CopiesRepoRelativeExtras(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	packDir := filepath.Join(repoDir, "packs", "my-pack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(repoDir, "shared-scripts")
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "my-pack",
		Version:       "1.0.0",
		Root:          ".",
		Extras:        []string{"../../shared-scripts"},
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Add:       false,
		With:      domain.BundledAll(),
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	destDir := filepath.Join(configDir, "packs", "my-pack")
	got, err := os.ReadFile(filepath.Join(destDir, "shared-scripts", "helper.sh"))
	if err != nil {
		t.Fatalf("expected repo-relative extras to be copied: %v", err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("copied extras = %q", string(got))
	}

	installed, err := config.LoadPackManifest(filepath.Join(destDir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if len(installed.Extras) != 1 || installed.Extras[0] != "shared-scripts" {
		t.Fatalf("installed extras = %v, want [shared-scripts]", installed.Extras)
	}
}

func TestPackInstall_InPlace(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeEmptyProfile(t, configDir, "default")

	// Place pack directly inside packs dir — the dangerous case.
	packDir := filepath.Join(configDir, "packs", "resident-pack")
	writePackManifest(t, packDir, "resident-pack")
	os.MkdirAll(filepath.Join(packDir, "rules"), 0o700)
	os.WriteFile(filepath.Join(packDir, "rules", "keep-me.md"), []byte("# Alive\n"), 0o600)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Content must survive.
	got, err := os.ReadFile(filepath.Join(packDir, "rules", "keep-me.md"))
	if err != nil {
		t.Fatalf("pack content destroyed: %v", err)
	}
	if string(got) != "# Alive\n" {
		t.Fatalf("content = %q", got)
	}

	// Origin recorded as local.
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if meta := lf.Packs["resident-pack"]; meta.Method != config.MethodLocal {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodLocal)
	}

	if !strings.Contains(out.String(), "recording in-place") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPackInstall_DefaultNoProfileRegistration(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeEmptyProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       false,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Profile should not be modified.
	b, err := os.ReadFile(filepath.Join(configDir, "profiles", "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Packs) != 0 {
		t.Fatalf("expected 0 packs (not added to profile), got %d", len(cfg.Packs))
	}
}

func TestPackInstall_NameOverride(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "original-name")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Name:      "custom-name",
		Link:      true,
		Add:       false,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "custom-name")
	if _, err := os.Lstat(dest); err != nil {
		t.Fatalf("expected pack at custom-name, got: %v", err)
	}
}

func TestPackInstall_ReplacesExisting(t *testing.T) {
	t.Parallel()
	packDir1 := t.TempDir()
	packDir2 := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir1, "test-pack")
	writePackManifest(t, packDir2, "test-pack")

	var out bytes.Buffer
	_ = PackInstall(context.Background(), PackInstallRequest{PackPath: packDir1, ConfigDir: configDir, Link: true, Add: false}, &out)
	out.Reset()
	err := PackInstall(context.Background(), PackInstallRequest{PackPath: packDir2, ConfigDir: configDir, Link: true, Add: false}, &out)
	if err != nil {
		t.Fatalf("second PackInstall: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "test-pack")
	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != packDir2 {
		t.Fatalf("symlink target = %q, want %q (should be updated)", target, packDir2)
	}
}

func TestPackInstall_Idempotent_NoDuplicateProfileEntries(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	req := PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}

	// Run PackInstall twice.
	var out bytes.Buffer
	if err := PackInstall(context.Background(), req, &out); err != nil {
		t.Fatalf("first PackInstall: %v", err)
	}
	out.Reset()
	if err := PackInstall(context.Background(), req, &out); err != nil {
		t.Fatalf("second PackInstall: %v", err)
	}

	// Verify profile has exactly one source and one pack entry.
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	b, _ := os.ReadFile(profilePath)
	var cfg config.ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}

	packCount := 0
	for _, p := range cfg.Packs {
		if p.Name == "test-pack" {
			packCount++
		}
	}
	if packCount != 1 {
		t.Fatalf("expected 1 pack entry, got %d", packCount)
	}

	// Verify output says "already in profile" on second run.
	if !strings.Contains(out.String(), "already in profile") {
		t.Fatalf("expected 'already in profile' message on second add, got: %s", out.String())
	}
}

// writeTestSyncConfig writes a minimal sync-config.yaml in configDir.
func writeTestSyncConfig(t *testing.T, configDir string) {
	t.Helper()
	content := "schema_version: 1\ndefaults:\n  profile: default\n"
	scPath := config.SyncConfigPath(configDir)
	if err := os.MkdirAll(filepath.Dir(scPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isBareClone(args []string) bool {
	return slices.Contains(args, "--bare")
}

// fakeCloneGitFn returns a RunGitFn that fakes a git clone by writing a pack.json
// into the target directory (the last argument to "git clone ... <dir>").
func fakeCloneGitFn(t *testing.T, packName string) func(ctx context.Context, args ...string) error {
	t.Helper()
	return func(_ context.Context, args ...string) error {
		// "clone --depth 1 <url> <dir>" or "-C <dir> fetch ..." etc.
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, dir, packName)
		}
		return nil
	}
}

func fakeCloneGitFnWithSetup(t *testing.T, setup func(dir string)) func(ctx context.Context, args ...string) error {
	t.Helper()
	return func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			setup(args[len(args)-1])
		}
		return nil
	}
}

func TestPackInstall_URL_ClonesIntoPacksDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected pack.json at dest: %v", err)
	}
	if !strings.Contains(out.String(), "Cloned:") {
		t.Fatalf("expected 'Cloned:' in output, got: %s", out.String())
	}
}

func TestPackInstall_URL_CloneFailure_CleansUpTempDirs(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "ssh://git@example.com/PROJ/repo.git",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn: func(_ context.Context, args ...string) error {
			if len(args) > 0 && args[0] == "clone" {
				return fmt.Errorf("simulated clone failure")
			}
			return nil
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err == nil {
		t.Fatal("expected clone failure")
	}

	assertNoStagingDirs(t, filepath.Join(configDir, "packs"))
	assertNoStagingDirs(t, packStagingDir(configDir))
}

func TestPackInstall_URL_SubPath_CleansUpCloneDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	// Fake a mono-repo clone that contains a pack in a subdirectory.
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			// Write pack.json inside the subpath directory.
			writePackManifest(t, filepath.Join(dir, "my-sub-pack"), "my-sub-pack")
		}
		return nil
	}

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "ssh://git@example.com/mono-repo.git",
		SubPath:   "my-sub-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall subpath: %v", err)
	}

	// The pack should be installed.
	dest := filepath.Join(configDir, "packs", "my-sub-pack")
	if _, serr := os.Stat(filepath.Join(dest, "pack.json")); serr != nil {
		t.Fatalf("expected pack.json at dest: %v", serr)
	}

	// No staging dirs should remain — the clone dir must be cleaned up.
	assertNoStagingDirs(t, filepath.Join(configDir, "packs"))
	assertNoStagingDirs(t, packStagingDir(configDir))
}

func TestPackInstall_URL_SubPath_ResolvesSymlinks(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	// Fake a mono-repo clone with shared content at root and a symlink in the pack.
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			// Shared content at repo root.
			sharedDir := filepath.Join(dir, "shared")
			if err := os.MkdirAll(sharedDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(sharedDir, "base.md"), []byte("shared rule"), 0o644); err != nil {
				return err
			}
			// Pack in subdirectory with a symlink to shared content.
			packDir := filepath.Join(dir, "my-pack")
			writePackManifest(t, packDir, "my-pack")
			rulesDir := filepath.Join(packDir, "rules")
			if err := os.MkdirAll(rulesDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(rulesDir, "local.md"), []byte("local rule"), 0o644); err != nil {
				return err
			}
			// Symlink: my-pack/rules/shared.md -> ../../shared/base.md
			if err := os.Symlink(filepath.Join(sharedDir, "base.md"), filepath.Join(rulesDir, "shared.md")); err != nil {
				return err
			}
		}
		return nil
	}

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "ssh://git@example.com/mono-repo.git",
		SubPath:   "my-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall with symlinks: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "my-pack")

	// Symlink should be resolved to a regular file.
	got, err := os.ReadFile(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("reading resolved symlink: %v", err)
	}
	if string(got) != "shared rule" {
		t.Errorf("resolved content = %q, want %q", string(got), "shared rule")
	}

	// Verify it's a regular file, not a symlink.
	info, err := os.Lstat(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file at install destination, got symlink")
	}

	// Regular file should be present too.
	got, err = os.ReadFile(filepath.Join(dest, "rules", "local.md"))
	if err != nil {
		t.Fatalf("reading regular file: %v", err)
	}
	if string(got) != "local rule" {
		t.Errorf("regular content = %q, want %q", string(got), "local rule")
	}

	// Shared directory should NOT be in the installed pack.
	if _, serr := os.Stat(filepath.Join(dest, "shared")); !os.IsNotExist(serr) {
		t.Error("shared/ directory should not exist in installed pack")
	}
}

func TestPackInstall_Path_ResolvesSymlinks(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	// Create a repo-like structure with .git marker.
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(repoRoot, "shared")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "common.md"), []byte("common rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	packDir := filepath.Join(repoRoot, "my-pack")
	writePackManifest(t, packDir, "my-pack")
	rulesDir := filepath.Join(packDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "own.md"), []byte("own rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink to shared content.
	if err := os.Symlink(filepath.Join(sharedDir, "common.md"), filepath.Join(rulesDir, "common.md")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Add:       false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall path with symlinks: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "my-pack")

	got, err := os.ReadFile(filepath.Join(dest, "rules", "common.md"))
	if err != nil {
		t.Fatalf("reading resolved symlink: %v", err)
	}
	if string(got) != "common rule" {
		t.Errorf("resolved content = %q, want %q", string(got), "common rule")
	}

	// Should be a regular file.
	info, err := os.Lstat(filepath.Join(dest, "rules", "common.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file, got symlink")
	}
}

// assertNoStagingDirs fails if dir contains any temp/staging directories.
func assertNoStagingDirs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // dir may not exist
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "clone-") || strings.HasPrefix(n, "archive-") ||
			strings.HasPrefix(n, "copy-") || strings.HasPrefix(n, "subdir-") ||
			strings.HasPrefix(n, "extract-") || strings.HasPrefix(n, "http-tarball-") ||
			strings.HasPrefix(n, ".clone-") || strings.HasPrefix(n, ".archive-") ||
			strings.HasPrefix(n, ".copy-") || strings.HasPrefix(n, ".subdir-") ||
			strings.HasPrefix(n, ".extract-") || strings.HasPrefix(n, ".http-tarball-") {
			t.Fatalf("orphaned staging dir in %s: %s", dir, n)
		}
	}
}

func TestPackInstall_URL_GenericRepository_SkipsPackURLProbe(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	urlChecks := 0
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://example.com/team/my-pack.git",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn: func(context.Context, string) (bool, error) {
			urlChecks++
			return true, nil
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}
	if urlChecks != 0 {
		t.Fatalf("URLOKFn called %d times, want 0 for generic repo URL", urlChecks)
	}
	dest := filepath.Join(configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected pack.json at dest: %v", err)
	}
}

func TestPackInstall_URL_CloudDevOpsDetails_UsesDerivedCloneURL(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	var cloneURL string
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" && !isBareClone(args) {
			cloneURL = args[3]
			writePackManifest(t, args[len(args)-1], "my-pack")
		}
		return nil
	}
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://devops.example.internal/devops-coderepository/namespaces/demo-ns/projects/TEAM/repositories/demo-repo/details?_ctx=us-region-1%2Cdevops_scm_central",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}
	if cloneURL != "https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo" {
		t.Fatalf("clone URL = %q", cloneURL)
	}
}

func TestPackInstall_CloneFallback_BadSubPath_ListsAvailablePacks(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			// Repo has two packs and one non-pack directory.
			writePackManifest(t, filepath.Join(dir, "actual-pack"), "actual-pack")
			writePackManifest(t, filepath.Join(dir, "other-pack"), "other-pack")
			if err := os.MkdirAll(filepath.Join(dir, "not-a-pack"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "ssh://git@example.com/PROJ/repo.git",
		SubPath:   "wrong-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err == nil {
		t.Fatal("expected error for missing sub-path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "wrong-pack") {
		t.Fatalf("error should mention the requested path: %s", msg)
	}
	if !strings.Contains(msg, "actual-pack") || !strings.Contains(msg, "other-pack") {
		t.Fatalf("error should list available packs: %s", msg)
	}
	if strings.Contains(msg, "not-a-pack") {
		t.Fatalf("error should not list non-pack directories: %s", msg)
	}
}

func TestPackInstall_URL_GitHubBlobSubdir_InstallsExtractedPackAndRecordsSubPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	urlChecks := 0
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, filepath.Join(dir, "packs", "team"), "team-pack")
			if err := os.WriteFile(filepath.Join(dir, "packs", "team", "marker.txt"), []byte("v1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  gitFn,
		URLOKFn: func(_ context.Context, raw string) (bool, error) {
			urlChecks++
			if raw != "https://raw.githubusercontent.com/example/repo/main/packs/team/pack.json" {
				t.Fatalf("unexpected pack URL %q", raw)
			}
			return true, nil
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}
	if urlChecks != 1 {
		t.Fatalf("URLOKFn called %d times, want 1", urlChecks)
	}
	dest := filepath.Join(configDir, "packs", "team-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected extracted pack.json at dest: %v", err)
	}
	// Non-content files (marker.txt) should NOT be in the extracted pack.
	if _, err := os.Stat(filepath.Join(dest, "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("marker.txt should not survive extraction (non-content file)")
	}
	if _, err := os.Stat(filepath.Join(dest, "packs")); !os.IsNotExist(err) {
		t.Fatalf("expected subtree extraction, got packs dir stat err=%v", err)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["team-pack"]
	if meta.SubPath != "packs/team" {
		t.Fatalf("sub_path = %q", meta.SubPath)
	}
	if meta.Ref != "main" {
		t.Fatalf("ref = %q", meta.Ref)
	}
	if meta.Origin != "https://github.com/example/repo" {
		t.Fatalf("origin = %q, want canonical repo URL", meta.Origin)
	}
}

func TestPackInstall_URL_AddsToProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	writeEmptyProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Add:       true,
		Profile:   "default",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}

	// Verify profile was updated.
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	b, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var cfg config.ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if len(cfg.Packs) != 1 || cfg.Packs[0].Name != "my-pack" {
		t.Fatalf("expected 1 pack named my-pack, got %+v", cfg.Packs)
	}
}

func TestPackInstall_URL_RecordsOriginInSyncConfig(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["my-pack"]
	if !ok {
		t.Fatal("expected my-pack in InstalledPacks")
	}
	if meta.Method != config.MethodClone {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodClone)
	}
	if meta.Origin != "https://github.com/example/my-pack" {
		t.Fatalf("origin = %q, want URL", meta.Origin)
	}
	if meta.InstalledAt != fixedNow.UTC().Format(time.RFC3339) {
		t.Fatalf("installed_at = %q, want %q", meta.InstalledAt, fixedNow.UTC().Format(time.RFC3339))
	}
}

func TestPackInstall_ArchiveURLInstallsAndRecordsArchiveMethod(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/pack.json":    `{"schema_version":2,"name":"archive-pack","version":"1.0.0","root":"."}`,
		"repo-main/rules/foo.md": "# Foo\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	archiveURL := srv.URL + "/pack.zip"

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       archiveURL,
		Archive:   true,
		ConfigDir: configDir,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall archive URL: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(configDir, "packs", "archive-pack", "rules", "foo.md"))
	if err != nil {
		t.Fatalf("read installed archive rule: %v", err)
	}
	if string(got) != "# Foo\n" {
		t.Fatalf("installed rule = %q", got)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["archive-pack"]
	if meta.Method != config.MethodArchive {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodArchive)
	}
	if meta.Origin != archiveURL {
		t.Fatalf("origin = %q, want %q", meta.Origin, archiveURL)
	}
	if meta.CommitHash != "" {
		t.Fatalf("commit_hash = %q, want empty for archive install", meta.CommitHash)
	}
	if meta.InstalledAt != fixedNow.UTC().Format(time.RFC3339) {
		t.Fatalf("installed_at = %q, want %q", meta.InstalledAt, fixedNow.UTC().Format(time.RFC3339))
	}
}

func TestPackInstall_LocalArchiveOriginUsesAbsolutePath(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/pack.json":    `{"schema_version":2,"name":"local-archive-pack","version":"1.0.0","root":"."}`,
		"repo-main/rules/foo.md": "# Foo\n",
	})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	archiveDir, err := os.MkdirTemp(wd, ".tmp-pack-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(archiveDir) })
	archivePath := filepath.Join(archiveDir, "pack.zip")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	archiveArg, err := filepath.Rel(wd, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	expectedOrigin, err := filepath.Abs(archiveArg)
	if err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	var out bytes.Buffer
	err = PackInstall(context.Background(), PackInstallRequest{
		PackPath:  archiveArg,
		ConfigDir: configDir,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall local archive path: %v", err)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["local-archive-pack"]
	if meta.Method != config.MethodArchive {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodArchive)
	}
	if meta.Origin != expectedOrigin {
		t.Fatalf("origin = %q, want %q", meta.Origin, expectedOrigin)
	}
}

func TestPackInstall_LocalDirectoryWithArchiveSuffixInstallsAsPath(t *testing.T) {
	t.Parallel()
	packDir := filepath.Join(t.TempDir(), "team.zip")
	configDir := t.TempDir()
	writePackManifest(t, packDir, "suffix-pack")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall directory with archive suffix: %v", err)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["suffix-pack"]
	if meta.Method != config.MethodLink {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodLink)
	}
}

func TestPackFetchArchiveMaterializesStagingWithoutFinalInstall(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/pack.json":    `{"schema_version":2,"name":"archive-staged","version":"1.0.0","root":"."}`,
		"repo-main/rules/foo.md": "# Foo\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	configDir := t.TempDir()
	packsDir := PacksDir(configDir)
	result, err := packFetchArchive(context.Background(), PackInstallRequest{
		URL:       srv.URL + "/pack.zip",
		Archive:   true,
		ConfigDir: configDir,
	}, nil)
	if err != nil {
		t.Fatalf("packFetchArchive: %v", err)
	}
	defer os.RemoveAll(result.destDir)

	finalDir := filepath.Join(packsDir, "archive-staged")
	if filepath.Clean(result.destDir) == filepath.Clean(finalDir) {
		t.Fatalf("packFetchArchive returned final install dir %s, want staging dir", result.destDir)
	}
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("packFetchArchive should not install final pack dir, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(result.destDir, "pack.json")); err != nil {
		t.Fatalf("staged pack.json missing: %v", err)
	}
}

func TestPackInstall_ArchiveURLContentPathsWithoutPackJSON(t *testing.T) {
	t.Parallel()
	archive := buildPackZip(t, map[string]string{
		"repo-main/material/rules/foo.md": "# Foo\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	archiveURL := srv.URL + "/content.zip"

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       archiveURL,
		Archive:   true,
		Name:      "archive-content",
		ConfigDir: configDir,
		ContentPaths: map[domain.PackCategory]string{
			domain.CategoryRules: "material/rules",
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall archive content_paths: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(configDir, "packs", "archive-content", "rules", "foo.md"))
	if err != nil {
		t.Fatalf("read installed archive content-path rule: %v", err)
	}
	if string(got) != "# Foo\n" {
		t.Fatalf("installed rule = %q", got)
	}
}

func TestPackInstall_PathRecordsOriginInSyncConfig(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall path: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["test-pack"]
	if !ok {
		t.Fatal("expected test-pack in InstalledPacks")
	}
	if meta.Method != config.MethodLink {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodLink)
	}
	if meta.Origin != packDir {
		t.Fatalf("origin = %q, want %q", meta.Origin, packDir)
	}
}

func TestPackInstall_URL_And_Path_MutuallyExclusive(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  "/some/path",
		URL:       "https://github.com/example/pack",
		ConfigDir: configDir,
	}, &out)
	if err == nil {
		t.Fatal("expected error for mutually exclusive URL+path")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error should mention 'mutually exclusive', got: %v", err)
	}
}

func TestPackInstall_NeitherURLNorPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		ConfigDir: configDir,
	}, &out)
	if err == nil {
		t.Fatal("expected error when neither URL nor path provided")
	}
}

func TestPackInstall_AddMissingProfile(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Add:       true,
		Profile:   "nonexistent",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err == nil {
		t.Fatal("expected error when profile does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify pack was NOT installed (early validation).
	packsDir := PacksDir(configDir)
	destDir := filepath.Join(packsDir, "test-pack")
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		t.Fatal("pack should not be installed when profile validation fails")
	}
}

func TestPackInstall_AddAutoCreatesDefaultProfile(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	// Config directory exists but has no sync-config or profile.
	// PackInstall with Add=true and no explicit profile (defaults to "default")
	// should auto-create the default profile instead of failing.
	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Add:       true,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Default profile should exist and contain the pack.
	profPath := filepath.Join(configDir, "profiles", "default.yaml")
	prof, err := config.LoadProfile(profPath)
	if err != nil {
		t.Fatalf("loading auto-created profile: %v", err)
	}
	found := false
	for _, p := range prof.Packs {
		if p.Name == "test-pack" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-pack not found in auto-created profile")
	}
}

const fakeHash1 = "aabbccdd1122334455667788"
const fakeHash2 = "11223344556677889900aabb"

func fakeHashFn(hash string) func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) { return hash, nil }
}

func TestPackInstall_URL_RecordsCommitHash(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Add:       false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.CommitHash != fakeHash1 {
		t.Fatalf("commit_hash = %q, want %q", meta.CommitHash, fakeHash1)
	}
}

func TestWarnPackJSONVersionMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		requestedVersion string
		manifestVersion  string
		wantWarning      bool
	}{
		{"both match", "1.2.3", "1.2.3", false},
		{"both match with v-prefix", "v1.2.3", "v1.2.3", false},
		{"mixed prefix match", "1.2.3", "v1.2.3", false},
		{"different versions", "1.2.3", "1.0.0", true},
		{"no requested version", "", "1.0.0", false},
		{"no manifest version", "1.2.3", "", false},
		{"requested is commit hash", "abc1234", "1.0.0", false},
		{"requested is latest", "latest", "1.0.0", false},
		{"manifest is non-semver", "1.2.3", "release-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			warnPackJSONVersionMismatch(&buf, tt.requestedVersion, tt.manifestVersion)
			gotWarning := strings.Contains(buf.String(), "Warning:")
			if gotWarning != tt.wantWarning {
				t.Errorf("warning emitted = %v, want %v (output: %q)",
					gotWarning, tt.wantWarning, buf.String())
			}
		})
	}
}

func TestIsPinned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		meta config.InstalledPackMeta
		want bool
	}{
		{"semver ref", config.InstalledPackMeta{Ref: "v1.2.3"}, true},
		{"semver ref no v", config.InstalledPackMeta{Ref: "1.2.3"}, true},
		{"prerelease semver", config.InstalledPackMeta{Ref: "v1.0.0-beta.1"}, true},
		{"commit hash", config.InstalledPackMeta{Ref: "abc1234def5678"}, true},
		{"short commit hash", config.InstalledPackMeta{Ref: "aabbccd"}, true},
		{"branch name", config.InstalledPackMeta{Ref: "develop"}, false},
		{"main branch", config.InstalledPackMeta{Ref: "main"}, false},
		{"empty ref", config.InstalledPackMeta{Ref: ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isPinned(tt.meta); got != tt.want {
				t.Errorf("isPinned(%+v) = %v, want %v", tt.meta, got, tt.want)
			}
		})
	}
}

func TestPackInstall_URL_VersionRecordedInLockfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "1.2.3",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v1.2.3", "v2.0.0"}, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with version: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "v1.2.3" {
		t.Errorf("ref = %q, want v1.2.3 (version should translate to v-prefixed tag)", meta.Ref)
	}
	if !isPinned(meta) {
		t.Errorf("expected isPinned to be true for semver ref %q", meta.Ref)
	}
}

func TestPackInstall_URL_ExactSemverPreservesRemoteBareTag(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var clonedRef string
	gitFn := func(_ context.Context, args ...string) error {
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

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "v1.2.3",
		RunGitFn:  gitFn,
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"1.2.3", "2.0.0"}, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with exact semver: %v", err)
	}
	if clonedRef != "1.2.3" {
		t.Fatalf("clone ref = %q, want raw remote tag", clonedRef)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "1.2.3" {
		t.Fatalf("ref = %q, want raw remote tag", meta.Ref)
	}
}

// TestPackInstall_URL_NamespacedSemverPreservesRawTag covers the end-to-end
// flow for namespaced semver tags (Go-module convention). Installing with
// Ref: "my-pack/v1.0.0" must resolve against the prefix-scoped tag list,
// clone the raw namespaced tag unchanged, and record the raw spelling in
// the lockfile so pack_update can derive the prefix on subsequent commands.
// The tag list deliberately includes flat and foreign-prefix noise to
// prove the prefix filter isolates correctly.
func TestPackInstall_URL_NamespacedSemverPreservesRawTag(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var clonedRef string
	gitFn := func(_ context.Context, args ...string) error {
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

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "my-pack/v1.0.0",
		RunGitFn:  gitFn,
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{
				"v1.5.0",            // flat — must be ignored when prefix is set
				"other-pack/v1.0.0", // wrong prefix — must be ignored
				"my-pack/v1.0.0",    // target
				"my-pack/v2.0.0",    // prefix-match but not the requested version
			}, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with namespaced semver: %v", err)
	}
	// Raw namespaced spelling must reach git clone — stripping the
	// prefix ("v1.0.0") or double-prepending ("my-pack/my-pack/v1.0.0")
	// would break checkout against the remote.
	if clonedRef != "my-pack/v1.0.0" {
		t.Fatalf("clone ref = %q, want my-pack/v1.0.0", clonedRef)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "my-pack/v1.0.0" {
		t.Fatalf("lockfile ref = %q, want my-pack/v1.0.0", meta.Ref)
	}
	if !isPinned(meta) {
		t.Errorf("namespaced semver ref should satisfy isPinned; got ref=%q", meta.Ref)
	}
}

// TestPackInstall_URL_NonSemverVersionTreatedAsLiteralRef verifies that
// non-semver strings passed to --version (like "main") are treated as
// literal git refs rather than rejected up-front. Under the unified ref
// model, --version and --ref share one dispatch path; any string is a
// legitimate ref attempt. The test asserts the dispatch ran (git was
// invoked with "main" as the branch arg) and no "invalid version" error
// short-circuited the install.
func TestPackInstall_URL_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var gitArgs [][]string
	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "main",
		RunGitFn: func(_ context.Context, args ...string) error {
			gitArgs = append(gitArgs, append([]string(nil), args...))
			return nil
		},
		URLOKFn: func(context.Context, string) (bool, error) { return true, nil },
	}, &out)
	// Install fails downstream (fake git does nothing, so there's no
	// pack.json to load). That's fine — the point is: the error is NOT an
	// up-front "invalid version" rejection.
	if err != nil && strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version: %v", err)
	}
	// Git must have been called (proving the classifier dispatch ran).
	if len(gitArgs) == 0 {
		t.Fatal("git should be invoked — RefLiteral dispatch calls clone with the raw ref")
	}
	// Verify "main" was passed as the branch/ref arg somewhere in the
	// clone invocation.
	found := false
	for _, call := range gitArgs {
		for _, a := range call {
			if a == "main" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("git clone should have been called with ref 'main'; got calls: %v", gitArgs)
	}
}

// TestPackInstall_URL_PartialSemverResolves covers the partial version flow:
// --version v1 queries the remote tags, picks the highest stable v1.x.x, and
// pins the lockfile to the resolved exact version (not the partial). This
// keeps partial installs forward-compatible with a future channel model while
// giving users a useful discovery shortcut today.
func TestPackInstall_URL_PartialSemverResolves(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var listCalls int
	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "v1",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			listCalls++
			return []string{"v1.0.0", "v1.5.0", "v2.0.0", "v1.6.0-beta.1"}, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with partial version: %v", err)
	}
	if listCalls != 1 {
		t.Errorf("expected 1 ListRemoteTagsFn call, got %d", listCalls)
	}
	if !strings.Contains(out.String(), "Resolved v1 → v1.5.0") {
		t.Errorf("expected resolution message in output, got: %s", out.String())
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "v1.5.0" {
		t.Errorf("ref = %q, want v1.5.0 (highest stable v1.x.x, skipping v2 and prerelease)", meta.Ref)
	}
}

// TestPackInstall_URL_PartialSemverNoMatch exercises the error path when
// --version vN doesn't match any stable semver tag in the remote.
func TestPackInstall_URL_PartialSemverNoMatch(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "v3",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v2.0.0"}, nil
		},
	}, &out)
	if err == nil {
		t.Fatal("expected error for unresolvable partial version")
	}
	if !strings.Contains(err.Error(), "resolving partial version") {
		t.Errorf("error should mention partial resolution: %v", err)
	}
}

func TestPackInstall_URL_SemverRefRecordedAsPin(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "v2.0.0",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v2.0.0"}, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with semver ref: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "v2.0.0" {
		t.Errorf("ref = %q, want v2.0.0", meta.Ref)
	}
	if !isPinned(meta) {
		t.Errorf("expected isPinned to be true for semver ref %q", meta.Ref)
	}
}

func TestPackInstall_URL_BranchRefNoVersion(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "develop",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with branch ref: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "develop" {
		t.Errorf("ref = %q, want develop", meta.Ref)
	}
	if isPinned(meta) {
		t.Errorf("expected branch ref to not count as pinned, got isPinned=true")
	}
}

// TestPackInstall_UpgradeWithLegacyInstalledPacks verifies the migration data
// loss fix: a user upgrading from a sync-config with installed_packs and
// running pack install before doctor must not lose their legacy entries.
func TestPackInstall_UpgradeWithLegacyInstalledPacks(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Seed sync-config with legacy installed_packs (no lockfile yet — the
	// state of a fresh upgrade from before lockfile-based pack metadata).
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.Defaults.Profile = "default"
	sc.Defaults.Harnesses = []string{"claudecode"}
	sc.Defaults.Scope = "global"
	sc.InstalledPacks = map[string]config.InstalledPackMeta{
		"legacy-a": {Origin: "https://example.com/legacy-a.git", Method: config.MethodClone, CommitHash: "aaa111"},
		"legacy-b": {Origin: "https://example.com/legacy-b.git", Method: config.MethodClone, CommitHash: "bbb222"},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
	writeEmptyProfile(t, configDir, "default")

	// Run pack install on a new pack — must not orphan the legacy entries.
	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/new-pack",
		ConfigDir: configDir,
		RunGitFn:  fakeCloneGitFn(t, "new-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Lockfile must contain all three packs: 2 migrated + 1 newly installed.
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	for _, name := range []string{"legacy-a", "legacy-b", "new-pack"} {
		if _, ok := lf.Packs[name]; !ok {
			t.Errorf("lockfile missing pack %q after install (legacy migration was lost)", name)
		}
	}
	if lf.Packs["legacy-a"].CommitHash != "aaa111" {
		t.Errorf("legacy-a commit_hash = %q, want aaa111", lf.Packs["legacy-a"].CommitHash)
	}

	// Sync-config installed_packs must be cleared after migration.
	reloadedSC, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedSC.InstalledPacks) != 0 {
		t.Errorf("sync-config still has %d installed_packs after migration", len(reloadedSC.InstalledPacks))
	}
}

func TestPackInstall_URL_LatestSentinelClearsPin(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Ref:       "latest", // CLI sentinel — should resolve to empty (track HEAD)
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall URL with @latest: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta := lf.Packs["my-pack"]
	if meta.Ref != "" {
		t.Errorf("ref = %q, want empty (latest should leave ref empty for default branch)", meta.Ref)
	}
	if isPinned(meta) {
		t.Errorf("expected unpinned after latest sentinel, got isPinned=true")
	}
}

func TestValidatePackName(t *testing.T) {
	t.Parallel()

	valid := []string{"my-pack", "test_pack", "pack123"}
	for _, name := range valid {
		if err := validatePackName(name); err != nil {
			t.Errorf("validatePackName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []struct {
		name string
		desc string
	}{
		{"../evil", "parent traversal"},
		{"../../.claude/rules/pwned", "deep traversal"},
		{"path/traversal", "forward slash"},
		{"back\\slash", "backslash"},
		{"null\x00byte", "null byte"},
	}
	for _, tc := range invalid {
		if err := validatePackName(tc.name); err == nil {
			t.Errorf("validatePackName(%q) [%s] expected error, got nil", tc.name, tc.desc)
		}
	}
}

func TestPackWarnMCPServers(t *testing.T) {
	t.Parallel()
	manifest := config.PackManifest{
		MCP: []string{"bitbucket", "srv-a"},
	}
	var out bytes.Buffer
	packWarnMCPServers(manifest, &out)
	output := out.String()
	if !strings.Contains(output, "WARNING") {
		t.Error("expected WARNING in output")
	}
	if !strings.Contains(output, "srv-a") {
		t.Errorf("expected 'jira' in output, got: %s", output)
	}
	if !strings.Contains(output, "bitbucket") {
		t.Errorf("expected 'bitbucket' in output, got: %s", output)
	}
}

func TestPackWarnMCPServers_NoServers(t *testing.T) {
	t.Parallel()
	manifest := config.PackManifest{}
	var out bytes.Buffer
	packWarnMCPServers(manifest, &out)
	if out.Len() > 0 {
		t.Errorf("expected no output for pack without MCP servers, got: %s", out.String())
	}
}

// setupBundledConflictTest creates a pack with a "team" profile and a configDir
// with a stale version of that profile already in place. Returns packDir, configDir.
func TestPackInstall_URL_QuietAddsQuietInProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	writeEmptyProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:       "https://github.com/example/quiet-pack",
		ConfigDir: configDir,
		Add:       true,
		Profile:   "default",
		Quiet:     boolPtrIf(true),
		RunGitFn:  fakeCloneGitFn(t, "quiet-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "quiet-pack" {
			found = &cfg.Packs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("quiet-pack not found in profile")
	}
	if !found.Quiet {
		t.Fatal("expected Quiet=true in profile entry")
	}
	if !strings.Contains(out.String(), "quiet pack") {
		t.Fatalf("expected 'quiet pack' in output, got: %s", out.String())
	}
}

func TestPackInstall_URL_ContentPathsRecordedInSyncConfig(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)

	paths := map[domain.PackCategory]string{
		domain.CategorySkills: "nested/skills",
		domain.CategoryRules:  "config/rules",
	}

	// Clone mock that creates non-standard layout (no pack.json).
	cloneFn := func(_ context.Context, args ...string) error {
		dir := args[len(args)-1]
		os.MkdirAll(filepath.Join(dir, "nested", "skills", "triage"), 0o755)
		os.WriteFile(filepath.Join(dir, "nested", "skills", "triage", "SKILL.md"),
			[]byte("---\nname: triage\ndescription: t\n---\nbody\n"), 0o644)
		os.MkdirAll(filepath.Join(dir, "config", "rules"), 0o755)
		os.WriteFile(filepath.Join(dir, "config", "rules", "guide.md"),
			[]byte("---\nname: guide\n---\nbody\n"), 0o644)
		return nil
	}

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		URL:          "https://github.com/example/ext-repo",
		ConfigDir:    configDir,
		Add:          false,
		Name:         "ext-repo",
		ContentPaths: paths,
		RunGitFn:     cloneFn,
		URLOKFn:      func(context.Context, string) (bool, error) { return true, nil },
		NowFn:        func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["ext-repo"]
	if !ok {
		t.Fatal("expected ext-repo in InstalledPacks")
	}
	if meta.ContentPaths[domain.CategorySkills] != "nested/skills" {
		t.Fatalf("expected skills=nested/skills, got %q", meta.ContentPaths[domain.CategorySkills])
	}
	if meta.ContentPaths[domain.CategoryRules] != "config/rules" {
		t.Fatalf("expected rules=config/rules, got %q", meta.ContentPaths[domain.CategoryRules])
	}

	// Verify standard layout was produced.
	packDir := filepath.Join(configDir, "packs", "ext-repo")
	if _, err := os.Stat(filepath.Join(packDir, "pack.json")); err != nil {
		t.Fatal("pack.json missing from extracted pack")
	}
	if _, err := os.Stat(filepath.Join(packDir, "skills", "triage", "SKILL.md")); err != nil {
		t.Fatal("skills/triage/SKILL.md missing from extracted pack")
	}
}

func TestPackInstall_Path_QuietAddsQuietInProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	packDir := filepath.Join(t.TempDir(), "local-quiet")
	os.MkdirAll(packDir, 0o755)
	writePackManifest(t, packDir, "local-quiet")

	var out bytes.Buffer
	err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Add:       true,
		Profile:   "default",
		Quiet:     boolPtrIf(true),
		Link:      true,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	cfg, err := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "local-quiet" {
			found = &cfg.Packs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("local-quiet not found in profile")
	}
	if !found.Quiet {
		t.Fatal("expected Quiet=true in profile entry for local install with --quiet")
	}
}

func TestPackLifecycle_InstallListUpdateShowRemove(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	m := map[string]any{
		"schema_version": 2, "name": "lifecycle-pack", "version": "1.0.0",
		"root": ".", "rules": []string{"test-rule"},
	}
	mb, _ := json.Marshal(m)
	os.MkdirAll(packDir, 0o700)
	os.WriteFile(filepath.Join(packDir, "pack.json"), mb, 0o600)
	os.MkdirAll(filepath.Join(packDir, "rules"), 0o700)
	os.WriteFile(filepath.Join(packDir, "rules", "test-rule.md"), []byte("# Test Rule\n"), 0o600)

	var out bytes.Buffer

	// Install (copy, with registration)
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// List
	entries, _ := PackList(configDir)
	if len(entries) != 1 || entries[0].Name != "lifecycle-pack" || entries[0].Method != config.MethodCopy {
		t.Fatalf("List: %+v", entries)
	}

	// Update
	results, _ := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir, Name: "lifecycle-pack",
		NowFn: func() time.Time { return fixedNow },
	}, &out, nil)
	if results[0].Status != StatusUpdated {
		t.Fatalf("Update: %q", results[0].Status)
	}

	// Show
	show, _ := PackShow(configDir, "lifecycle-pack")
	if show.Version != "1.0.0" || len(show.Rules) == 0 {
		t.Fatalf("Show: version=%q rules=%v", show.Version, show.Rules)
	}

	// Remove
	PackDelete(configDir, "lifecycle-pack", &out)
	entries, _ = PackList(configDir)
	if len(entries) != 0 {
		t.Fatalf("List after remove: %d", len(entries))
	}
	if _, err := PackShow(configDir, "lifecycle-pack"); err == nil {
		t.Fatal("Show after remove should error")
	}
}
