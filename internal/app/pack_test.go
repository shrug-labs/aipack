package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		"schema_version": 1,
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
		SchemaVersion: 1,
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
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("load sync-config: %v", err)
	}
	if meta := sc.InstalledPacks["resident-pack"]; meta.Method != config.MethodLocal {
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
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta := sc.InstalledPacks["team-pack"]
	if meta.SubPath != "packs/team" {
		t.Fatalf("sub_path = %q", meta.SubPath)
	}
	if meta.Ref != "main" {
		t.Fatalf("ref = %q", meta.Ref)
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

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta, ok := sc.InstalledPacks["my-pack"]
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

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta, ok := sc.InstalledPacks["test-pack"]
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

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta := sc.InstalledPacks["my-pack"]
	if meta.CommitHash != fakeHash1 {
		t.Fatalf("commit_hash = %q, want %q", meta.CommitHash, fakeHash1)
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
		MCP: config.MCPPack{
			Servers: map[string]config.MCPDefaults{
				"jira":      {DefaultAllowedTools: []string{"get_issue", "search"}},
				"bitbucket": {DefaultAllowedTools: []string{"list_repos"}},
			},
		},
	}
	var out bytes.Buffer
	packWarnMCPServers(manifest, &out)
	output := out.String()
	if !strings.Contains(output, "WARNING") {
		t.Error("expected WARNING in output")
	}
	if !strings.Contains(output, "jira (2 tools)") {
		t.Errorf("expected 'jira (2 tools)' in output, got: %s", output)
	}
	if !strings.Contains(output, "bitbucket (1 tool)") {
		t.Errorf("expected 'bitbucket (1 tool)' in output, got: %s", output)
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
		Quiet:     true,
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

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	meta, ok := sc.InstalledPacks["ext-repo"]
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
		Quiet:     true,
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
		"schema_version": 1, "name": "lifecycle-pack", "version": "1.0.0",
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
	}, &out)
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
