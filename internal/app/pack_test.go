package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
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

func writeSeedProfile(t *testing.T, configDir string, profileName string) {
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

func TestPackAdd_Link(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
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

func TestPackAdd_Copy(t *testing.T) {
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
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      false,
		Register:  false,
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
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

func TestPackAdd_InPlace(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedProfile(t, configDir, "default")

	// Place pack directly inside packs dir — the dangerous case.
	packDir := filepath.Join(configDir, "packs", "resident-pack")
	writePackManifest(t, packDir, "resident-pack")
	os.MkdirAll(filepath.Join(packDir, "rules"), 0o700)
	os.WriteFile(filepath.Join(packDir, "rules", "keep-me.md"), []byte("# Alive\n"), 0o600)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
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

	if !strings.Contains(out.String(), "registering in-place") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPackAdd_NoRegister(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
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
		t.Fatalf("expected 0 packs (no register), got %d", len(cfg.Packs))
	}
}

func TestPackAdd_NameOverride(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "original-name")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Name:      "custom-name",
		Link:      true,
		Register:  false,
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "custom-name")
	if _, err := os.Lstat(dest); err != nil {
		t.Fatalf("expected pack at custom-name, got: %v", err)
	}
}

func TestPackList_Empty(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	entries, err := PackList(configDir)
	if err != nil {
		t.Fatalf("PackList: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestPackList_AfterAdd(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, &out)

	entries, err := PackList(configDir)
	if err != nil {
		t.Fatalf("PackList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "test-pack" {
		t.Fatalf("name = %q, want %q", entries[0].Name, "test-pack")
	}
	if entries[0].Method != config.MethodLink {
		t.Fatalf("expected method=link, got %q", entries[0].Method)
	}
	if entries[0].Version != "1.0.0" {
		t.Fatalf("version = %q, want %q", entries[0].Version, "1.0.0")
	}
}

func TestPackRemove(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, &out)

	out.Reset()
	if _, err := PackRemove(configDir, "test-pack", &out); err != nil {
		t.Fatalf("PackRemove: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "test-pack")
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected pack to be removed, got: %v", err)
	}
}

func TestPackRemove_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	_, err := PackRemove(configDir, "nonexistent", &out)
	if err == nil {
		t.Fatal("expected error for removing nonexistent pack")
	}
}

func TestPackAdd_ReplacesExisting(t *testing.T) {
	t.Parallel()
	packDir1 := t.TempDir()
	packDir2 := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir1, "test-pack")
	writePackManifest(t, packDir2, "test-pack")

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{PackPath: packDir1, ConfigDir: configDir, Link: true, Register: false}, &out)
	out.Reset()
	err := PackAdd(context.Background(), PackAddRequest{PackPath: packDir2, ConfigDir: configDir, Link: true, Register: false}, &out)
	if err != nil {
		t.Fatalf("second PackAdd: %v", err)
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

func TestPackAdd_Idempotent_NoDuplicateProfileEntries(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedProfile(t, configDir, "default")
	writeSeedSyncConfig(t, configDir)

	req := PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}

	// Run PackAdd twice.
	var out bytes.Buffer
	if err := PackAdd(context.Background(), req, &out); err != nil {
		t.Fatalf("first PackAdd: %v", err)
	}
	out.Reset()
	if err := PackAdd(context.Background(), req, &out); err != nil {
		t.Fatalf("second PackAdd: %v", err)
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

	// Verify output says "already registered" on second run.
	if !strings.Contains(out.String(), "already registered") {
		t.Fatalf("expected 'already registered' message on second add, got: %s", out.String())
	}
}

// writeSeedSyncConfig writes a minimal sync-config.yaml in configDir.
func writeSeedSyncConfig(t *testing.T, configDir string) {
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

func TestPackAdd_URL_ClonesIntoPacksDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}

	dest := filepath.Join(configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected pack.json at dest: %v", err)
	}
	if !strings.Contains(out.String(), "Cloned:") {
		t.Fatalf("expected 'Cloned:' in output, got: %s", out.String())
	}
}

func TestPackAdd_URL_CloneFailure_CleansUpTempDirs(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "ssh://git@example.com/PROJ/repo.git",
		ConfigDir: configDir,
		Register:  false,
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

func TestPackAdd_URL_SubPath_CleansUpCloneDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

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
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "ssh://git@example.com/mono-repo.git",
		SubPath:   "my-sub-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd subpath: %v", err)
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

func TestPackAdd_URL_SubPath_ResolvesSymlinks(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

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
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "ssh://git@example.com/mono-repo.git",
		SubPath:   "my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd with symlinks: %v", err)
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

func TestPackAdd_Path_ResolvesSymlinks(t *testing.T) {
	t.Parallel()
	testutil.SkipWithoutSymlinks(t)
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

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
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd path with symlinks: %v", err)
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
			strings.HasPrefix(n, ".clone-") || strings.HasPrefix(n, ".archive-") ||
			strings.HasPrefix(n, ".copy-") || strings.HasPrefix(n, ".subdir-") {
			t.Fatalf("orphaned staging dir in %s: %s", dir, n)
		}
	}
}

func TestPackAdd_URL_GenericRepository_SkipsPackURLProbe(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	urlChecks := 0
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://example.com/team/my-pack.git",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn: func(context.Context, string) (bool, error) {
			urlChecks++
			return true, nil
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}
	if urlChecks != 0 {
		t.Fatalf("URLOKFn called %d times, want 0 for generic repo URL", urlChecks)
	}
	dest := filepath.Join(configDir, "packs", "my-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected pack.json at dest: %v", err)
	}
}

func TestPackAdd_URL_CloudDevOpsDetails_UsesDerivedCloneURL(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	var cloneURL string
	gitFn := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			cloneURL = args[3]
			writePackManifest(t, args[len(args)-1], "my-pack")
		}
		return nil
	}
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://devops.example.internal/devops-coderepository/namespaces/demo-ns/projects/TEAM/repositories/demo-repo/details?_ctx=us-region-1%2Cdevops_scm_central",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  gitFn,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}
	if cloneURL != "https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo" {
		t.Fatalf("clone URL = %q", cloneURL)
	}
}

func TestPackAdd_CloneFallback_BadSubPath_ListsAvailablePacks(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

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
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "ssh://git@example.com/PROJ/repo.git",
		SubPath:   "wrong-pack",
		ConfigDir: configDir,
		Register:  false,
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

func TestPackAdd_URL_GitHubBlobSubdir_InstallsExtractedPackAndRecordsSubPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

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
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: configDir,
		Register:  false,
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
		t.Fatalf("PackAdd URL: %v", err)
	}
	if urlChecks != 1 {
		t.Fatalf("URLOKFn called %d times, want 1", urlChecks)
	}
	dest := filepath.Join(configDir, "packs", "team-pack")
	if _, err := os.Stat(filepath.Join(dest, "pack.json")); err != nil {
		t.Fatalf("expected extracted pack.json at dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "marker.txt")); err != nil {
		t.Fatalf("expected extracted marker.txt at dest: %v", err)
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

func TestPackUpdate_Clone_SubPath_ReclonesAndExtractsSubtree(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	installGit := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, filepath.Join(dir, "packs", "team"), "team-pack")
			if err := os.WriteFile(filepath.Join(dir, "packs", "team", "marker.txt"), []byte("v1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  installGit,
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}

	cloneCalls := 0
	pullCalled := false
	updateGit := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			cloneCalls++
			dir := args[len(args)-1]
			writePackManifest(t, filepath.Join(dir, "packs", "team"), "team-pack")
			if err := os.WriteFile(filepath.Join(dir, "packs", "team", "marker.txt"), []byte("v2"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if len(args) >= 3 && args[2] == "pull" {
			pullCalled = true
		}
		return nil
	}
	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "team-pack",
		RunGitFn:  updateGit,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 || results[0].Status != "updated" {
		t.Fatalf("expected 1 updated result, got %+v", results)
	}
	if cloneCalls != 1 {
		t.Fatalf("clone calls = %d, want 1", cloneCalls)
	}
	if pullCalled {
		t.Fatal("expected subpath update to reclone instead of pull")
	}
	b, err := os.ReadFile(filepath.Join(configDir, "packs", "team-pack", "marker.txt"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(b) != "v2" {
		t.Fatalf("marker = %q, want v2", string(b))
	}
}

func TestPackAdd_URL_RegistersInProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  true,
		Profile:   "default",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
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

func TestPackAdd_URL_RecordsOriginInSyncConfig(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
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

func TestPackAdd_PathRecordsOriginInSyncConfig(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd path: %v", err)
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

func TestPackRemove_ClearsOriginFromSyncConfig(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	// Verify origin exists before remove.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if _, ok := sc.InstalledPacks["test-pack"]; !ok {
		t.Fatal("expected origin to exist before remove")
	}

	out.Reset()
	if _, err := PackRemove(configDir, "test-pack", &out); err != nil {
		t.Fatalf("PackRemove: %v", err)
	}

	sc, _ = config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if _, ok := sc.InstalledPacks["test-pack"]; ok {
		t.Fatal("expected origin to be cleared after remove")
	}
}

func TestPackRemove_DeregistersFromProfile(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedProfile(t, configDir, "default")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Verify pack is registered in profile.
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	b, _ := os.ReadFile(profilePath)
	var cfg config.ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if len(cfg.Packs) == 0 {
		t.Fatal("expected pack to be registered before remove")
	}

	// Remove pack.
	out.Reset()
	if _, err := PackRemove(configDir, "test-pack", &out); err != nil {
		t.Fatalf("PackRemove: %v", err)
	}

	// Verify pack is deregistered from profile.
	b, _ = os.ReadFile(profilePath)
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal profile after remove: %v", err)
	}
	for _, p := range cfg.Packs {
		if p.Name == "test-pack" {
			t.Fatal("expected pack entry to be removed from profile")
		}
	}
	// Verify output mentions deregistration.
	if !strings.Contains(out.String(), "Deregistered") {
		t.Fatalf("expected deregistration message, got: %s", out.String())
	}
}

func TestPackRemove_ReturnsSeededProfiles(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()

	// Write a manifest that declares a seeded profile.
	m := map[string]any{
		"schema_version": 1,
		"name":           "test-pack",
		"version":        "1.0.0",
		"root":           ".",
		"profiles":       []string{"profiles/team.yaml"},
	}
	b, _ := json.Marshal(m)
	os.MkdirAll(packDir, 0o700)
	os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600)

	// Write the seed profile inside the pack.
	os.MkdirAll(filepath.Join(packDir, "profiles"), 0o700)
	os.WriteFile(filepath.Join(packDir, "profiles", "team.yaml"),
		[]byte("schema_version: 1\npacks: []\n"), 0o600)

	writeSeedProfile(t, configDir, "default")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	// Verify the seeded profile was created.
	teamProfile := filepath.Join(configDir, "profiles", "team.yaml")
	if _, err := os.Stat(teamProfile); err != nil {
		t.Fatalf("expected seeded profile to exist: %v", err)
	}

	// Remove pack.
	out.Reset()
	result, err := PackRemove(configDir, "test-pack", &out)
	if err != nil {
		t.Fatalf("PackRemove: %v", err)
	}

	// Result should list the seeded profile by name.
	if len(result.SeededProfiles) != 1 {
		t.Fatalf("expected 1 seeded profile, got %d", len(result.SeededProfiles))
	}
	if result.SeededProfiles[0] != "team" {
		t.Fatalf("expected seeded profile name %q, got %q", "team", result.SeededProfiles[0])
	}

	// Profile file should still exist (caller is responsible for removal).
	if _, err := os.Stat(teamProfile); err != nil {
		t.Fatalf("expected seeded profile to still exist before explicit removal: %v", err)
	}

	// Now remove seeded profiles.
	out.Reset()
	RemoveSeededProfiles(configDir, result.SeededProfiles, &out)
	if _, err := os.Stat(teamProfile); !os.IsNotExist(err) {
		t.Fatalf("expected seeded profile to be removed")
	}
	if !strings.Contains(out.String(), "Removed seeded profile") {
		t.Fatalf("expected removal message, got: %s", out.String())
	}
}

func TestPackList_ShowsOriginInfo(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	entries, err := PackList(configDir)
	if err != nil {
		t.Fatalf("PackList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Origin != packDir {
		t.Fatalf("origin = %q, want %q", entries[0].Origin, packDir)
	}
	if entries[0].Method != config.MethodLink {
		t.Fatalf("method = %q, want %q", entries[0].Method, config.MethodLink)
	}
}

func TestPackAdd_URL_And_Path_MutuallyExclusive(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
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

func TestPackAdd_NeitherURLNorPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		ConfigDir: configDir,
	}, &out)
	if err == nil {
		t.Fatal("expected error when neither URL nor path provided")
	}
}

func TestPackAdd_RegisterMissingProfile(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Register:  true,
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

func TestPackAdd_RegisterAutoCreatesDefaultProfile(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	// Config directory exists but has no sync-config or profile.
	// PackAdd with Register=true and no explicit profile (defaults to "default")
	// should auto-create the default profile instead of failing.
	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Register:  true,
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

// --- Update tests ---

func TestPackUpdate_Link_VerifiesTarget(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "test-pack",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "up-to-date" {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}
}

func TestPackUpdate_Copy_ReCopies(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      false,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	// Modify the source pack to verify re-copy works.
	extraFile := filepath.Join(packDir, "extra.txt")
	if err := os.WriteFile(extraFile, []byte("new content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "test-pack",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "updated" {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}

	// Verify the extra file was copied.
	dest := filepath.Join(configDir, "packs", "test-pack", "extra.txt")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read extra.txt: %v", err)
	}
	if string(got) != "new content\n" {
		t.Fatalf("extra.txt = %q, want %q", got, "new content\n")
	}
}

func TestPackUpdate_Clone_PullsFFOnly(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	// Simulate a cloned pack.
	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	pullCalled := false
	fakeGit := func(_ context.Context, args ...string) error {
		if len(args) >= 3 && args[2] == "pull" {
			pullCalled = true
		}
		return nil
	}

	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		RunGitFn:  fakeGit,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "updated" {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if !pullCalled {
		t.Fatal("expected git pull to be called")
	}
}

func TestPackUpdate_All(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	pack1 := t.TempDir()
	pack2 := t.TempDir()
	writePackManifest(t, pack1, "pack-a")
	writePackManifest(t, pack2, "pack-b")

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{PackPath: pack1, ConfigDir: configDir, Link: true, Register: false, NowFn: func() time.Time { return fixedNow }}, &out)
	_ = PackAdd(context.Background(), PackAddRequest{PackPath: pack2, ConfigDir: configDir, Link: true, Register: false, NowFn: func() time.Time { return fixedNow }}, &out)

	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		All:       true,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate --all: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestPackUpdate_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	var out bytes.Buffer
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "nonexistent",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "error" {
		t.Fatalf("status = %q, want error", results[0].Status)
	}
}

// --- Show tests ---

func TestPackShow_HappyPath(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	entry, err := PackShow(configDir, "test-pack")
	if err != nil {
		t.Fatalf("PackShow: %v", err)
	}
	if entry.Name != "test-pack" {
		t.Fatalf("name = %q, want test-pack", entry.Name)
	}
	if entry.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", entry.Version)
	}
	if entry.Method != config.MethodLink {
		t.Fatalf("method = %q, want link", entry.Method)
	}
	if entry.Origin != packDir {
		t.Fatalf("origin = %q, want %q", entry.Origin, packDir)
	}
}

func TestPackShow_DiscoversContentFromDisk(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()

	// Write a manifest with no content fields — discovery should find them.
	writePackManifest(t, packDir, "discover-pack")

	// Create content on disk.
	rulesDir := filepath.Join(packDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "my-rule.md"), []byte("# rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(packDir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeSeedSyncConfig(t, configDir)
	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	entry, err := PackShow(configDir, "discover-pack")
	if err != nil {
		t.Fatalf("PackShow: %v", err)
	}
	if len(entry.Rules) != 1 || entry.Rules[0] != "my-rule" {
		t.Fatalf("Rules = %v, want [my-rule]", entry.Rules)
	}
	if len(entry.Skills) != 1 || entry.Skills[0] != "my-skill" {
		t.Fatalf("Skills = %v, want [my-skill]", entry.Skills)
	}
}

func TestPackShow_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, err := PackShow(configDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for showing nonexistent pack")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error should mention 'not installed', got: %v", err)
	}
}

func TestPackList_BrokenSymlink(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, &out)

	// Delete the symlink target to create a broken link.
	if err := os.RemoveAll(packDir); err != nil {
		t.Fatal(err)
	}

	entries, err := PackList(configDir)
	if err != nil {
		t.Fatalf("PackList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Method != config.MethodLink {
		t.Fatalf("expected method=link, got %q", entries[0].Method)
	}
	// The target was deleted; verify the path is retained but the target is gone.
	if entries[0].Path == "" {
		t.Fatal("expected Path to still contain the original target")
	}
	if _, err := os.Stat(entries[0].Path); err == nil {
		t.Fatal("expected broken symlink (target should not exist)")
	}
}

// TestPackLifecycle_AddListUpdateShowRemove exercises the full pack lifecycle through
// the service layer: add → list → update → show → remove.
func TestPackLifecycle_AddListUpdateShowRemove(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writeSeedProfile(t, configDir, "default")
	writeSeedSyncConfig(t, configDir)

	// Write a pack manifest with a rules entry.
	m := map[string]any{
		"schema_version": 1,
		"name":           "lifecycle-pack",
		"version":        "1.0.0",
		"root":           ".",
		"rules":          []string{"test-rule"},
	}
	mb, _ := json.Marshal(m)
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), mb, 0o600); err != nil {
		t.Fatal(err)
	}

	// Write the rules file that the manifest references.
	rulesDir := filepath.Join(packDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "test-rule.md"), []byte("# Test Rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// --- Step 1: Add (copy, with profile registration) ---
	var addOut bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      false, // copy mode
		Register:  true,
		Profile:   "default",
		NowFn:     func() time.Time { return fixedNow },
	}, &addOut)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// --- Step 2: List — verify pack appears ---
	entries, err := PackList(configDir)
	if err != nil {
		t.Fatalf("PackList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("PackList: expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "lifecycle-pack" {
		t.Fatalf("PackList: name = %q, want %q", entries[0].Name, "lifecycle-pack")
	}
	if entries[0].Method != config.MethodCopy {
		t.Fatalf("PackList: method = %q, want %q", entries[0].Method, config.MethodCopy)
	}
	if entries[0].Version != "1.0.0" {
		t.Fatalf("PackList: version = %q, want %q", entries[0].Version, "1.0.0")
	}

	// --- Step 3: Update (copy method: re-copies from origin) ---
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "lifecycle-pack",
		NowFn:     func() time.Time { return fixedNow },
	}, &addOut)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("PackUpdate: expected 1 result, got %d", len(results))
	}
	if results[0].Status != "updated" {
		t.Fatalf("PackUpdate: status = %q, want %q", results[0].Status, "updated")
	}
	if results[0].Method != config.MethodCopy {
		t.Fatalf("PackUpdate: method = %q, want %q", results[0].Method, config.MethodCopy)
	}

	// --- Step 4: Show — verify details ---
	showEntry, err := PackShow(configDir, "lifecycle-pack")
	if err != nil {
		t.Fatalf("PackShow: %v", err)
	}
	if showEntry.Name != "lifecycle-pack" {
		t.Fatalf("PackShow: name = %q, want %q", showEntry.Name, "lifecycle-pack")
	}
	if showEntry.Version != "1.0.0" {
		t.Fatalf("PackShow: version = %q, want %q", showEntry.Version, "1.0.0")
	}
	if showEntry.Method != config.MethodCopy {
		t.Fatalf("PackShow: method = %q, want %q", showEntry.Method, config.MethodCopy)
	}
	if len(showEntry.Rules) == 0 {
		t.Fatal("PackShow: expected at least one rule")
	}
	foundRule := false
	for _, r := range showEntry.Rules {
		if r == "test-rule" {
			foundRule = true
		}
	}
	if !foundRule {
		t.Fatalf("PackShow: expected 'test-rule' in rules, got %v", showEntry.Rules)
	}

	// --- Step 5: Remove ---
	var removeOut bytes.Buffer
	_, err = PackRemove(configDir, "lifecycle-pack", &removeOut)
	if err != nil {
		t.Fatalf("PackRemove: %v", err)
	}

	// Verify it's gone.
	entries, err = PackList(configDir)
	if err != nil {
		t.Fatalf("PackList after remove: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("PackList after remove: expected 0 entries, got %d", len(entries))
	}

	// Verify show returns error.
	_, err = PackShow(configDir, "lifecycle-pack")
	if err == nil {
		t.Fatal("PackShow after remove: expected error")
	}
}

// --- Commit hash tracking tests ---

const fakeHash1 = "aabbccdd1122334455667788"
const fakeHash2 = "11223344556677889900aabb"

func fakeHashFn(hash string) func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) { return hash, nil }
}

func TestPackAdd_URL_RecordsCommitHash(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
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

func TestPackUpdate_Clone_TracksCommitHash(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	// Install with hash1.
	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Update — new hash.
	fakeGit := func(_ context.Context, args ...string) error { return nil }
	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		RunGitFn:  fakeGit,
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash2),
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "updated" {
		t.Fatalf("status = %q, want updated", results[0].Status)
	}
	if results[0].CommitHash != fakeHash2 {
		t.Fatalf("result commit_hash = %q, want %q", results[0].CommitHash, fakeHash2)
	}

	// Verify sync-config was updated.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if sc.InstalledPacks["my-pack"].CommitHash != fakeHash2 {
		t.Fatalf("stored commit_hash = %q, want %q", sc.InstalledPacks["my-pack"].CommitHash, fakeHash2)
	}

	// Verify output shows the transition.
	if !strings.Contains(out.String(), "aabbccdd1122") {
		t.Fatalf("expected old short hash in output, got: %s", out.String())
	}
	if !strings.Contains(out.String(), "112233445566") {
		t.Fatalf("expected new short hash in output, got: %s", out.String())
	}
}

func TestPackUpdate_Clone_UpToDate(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	// Install with hash1.
	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)

	// Update with same hash — should be up-to-date.
	fakeGit := func(_ context.Context, args ...string) error { return nil }
	out.Reset()
	results, err := PackUpdate(context.Background(), PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		RunGitFn:  fakeGit,
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "up-to-date" {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}
	if !strings.Contains(out.String(), "Up-to-date") {
		t.Fatalf("expected 'Up-to-date' in output, got: %s", out.String())
	}
}

func TestPackShow_IncludesCommitHash(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(context.Background(), PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)

	entry, err := PackShow(configDir, "my-pack")
	if err != nil {
		t.Fatalf("PackShow: %v", err)
	}
	if entry.CommitHash != fakeHash1 {
		t.Fatalf("commit_hash = %q, want %q", entry.CommitHash, fakeHash1)
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

// --- ProfileMissingPacks / PackInstallMissing tests ---

func writeTestProfile(t *testing.T, configDir, name string, packNames []string) {
	t.Helper()
	packs := make([]map[string]any, len(packNames))
	for i, n := range packNames {
		packs[i] = map[string]any{"name": n}
	}
	cfg := map[string]any{"schema_version": 2, "packs": packs}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestProfileWithDisabled(t *testing.T, configDir, name string, enabled, disabled []string) {
	t.Helper()
	var packs []map[string]any
	for _, n := range enabled {
		packs = append(packs, map[string]any{"name": n})
	}
	for _, n := range disabled {
		packs = append(packs, map[string]any{"name": n, "enabled": false})
	}
	cfg := map[string]any{"schema_version": 2, "packs": packs}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestRegistry(t *testing.T, configDir string, packs map[string]config.RegistryEntry) {
	t.Helper()
	reg := config.Registry{SchemaVersion: 1, Packs: packs}
	b, err := yaml.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	// Write as a cached registry source.
	cacheDir := config.RegistriesCacheDir(configDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SourceCachePath(configDir, "test-source"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	// Register the source in sync-config so LoadMergedRegistry finds it.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	sc.SchemaVersion = 1
	sc.RegistrySources = append(sc.RegistrySources, config.RegistrySourceEntry{
		Name: "test-source", URL: "https://example.com/test-registry.yaml",
	})
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
}

func TestProfileMissingPacks_Mixed(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta", "gamma"})

	// Install only alpha.
	writePackManifest(t, filepath.Join(configDir, "packs", "alpha"), "alpha")

	missing, err := ProfileMissingPacks(configDir, "test")
	if err != nil {
		t.Fatalf("ProfileMissingPacks: %v", err)
	}
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing, got %d: %v", len(missing), missing)
	}
	if missing[0] != "beta" || missing[1] != "gamma" {
		t.Errorf("expected [beta, gamma], got %v", missing)
	}
}

func TestProfileMissingPacks_AllPresent(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta"})

	writePackManifest(t, filepath.Join(configDir, "packs", "alpha"), "alpha")
	writePackManifest(t, filepath.Join(configDir, "packs", "beta"), "beta")

	missing, err := ProfileMissingPacks(configDir, "test")
	if err != nil {
		t.Fatalf("ProfileMissingPacks: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected 0 missing, got %d: %v", len(missing), missing)
	}
}

func TestProfileMissingPacks_DisabledPackSkipped(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfileWithDisabled(t, configDir, "test", []string{"alpha"}, []string{"beta"})

	// Neither installed — but beta is disabled so should not appear.
	missing, err := ProfileMissingPacks(configDir, "test")
	if err != nil {
		t.Fatalf("ProfileMissingPacks: %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing, got %d: %v", len(missing), missing)
	}
	if missing[0] != "alpha" {
		t.Errorf("expected [alpha], got %v", missing)
	}
}

func TestPackInstallMissing_AllPresent(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta"})
	writePackManifest(t, filepath.Join(configDir, "packs", "alpha"), "alpha")
	writePackManifest(t, filepath.Join(configDir, "packs", "beta"), "beta")

	var out bytes.Buffer
	results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
		ConfigDir:   configDir,
		ProfileName: "test",
	}, &out)
	if err != nil {
		t.Fatalf("PackInstallMissing: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "present" {
			t.Errorf("pack %q: expected present, got %q", r.Pack, r.Status)
		}
	}
}

func TestPackInstallMissing_NotInRegistry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta"})
	writePackManifest(t, filepath.Join(configDir, "packs", "alpha"), "alpha")
	// beta is missing and no registry entry for it.

	var out bytes.Buffer
	results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
		ConfigDir:   configDir,
		ProfileName: "test",
	}, &out)
	if err != nil {
		t.Fatalf("PackInstallMissing: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status != "present" {
		t.Errorf("alpha: expected present, got %q", results[0].Status)
	}
	if results[1].Status != "not-in-registry" {
		t.Errorf("beta: expected not-in-registry, got %q", results[1].Status)
	}
}

func TestPackInstallMissing_InstallsFromRegistry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta"})
	writePackManifest(t, filepath.Join(configDir, "packs", "alpha"), "alpha")

	writeTestRegistry(t, configDir, map[string]config.RegistryEntry{
		"beta": {Repo: "https://example.com/repo.git", Ref: "main", Path: "packs/beta"},
	})

	var capturedReq PackAddRequest
	fakePack := func(_ context.Context, req PackAddRequest, w io.Writer) error {
		capturedReq = req
		// Simulate install by creating the pack dir.
		writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
		return nil
	}

	var out bytes.Buffer
	results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
		ConfigDir:   configDir,
		ProfileName: "test",
		PackAddFn:   fakePack,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstallMissing: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[1].Status != "installed" {
		t.Errorf("beta: expected installed, got %q", results[1].Status)
	}

	// Verify PackAdd was called with correct fields.
	if capturedReq.URL != "https://example.com/repo.git" {
		t.Errorf("URL = %q, want https://example.com/repo.git", capturedReq.URL)
	}
	if capturedReq.Ref != "main" {
		t.Errorf("Ref = %q, want main", capturedReq.Ref)
	}
	if capturedReq.SubPath != "packs/beta" {
		t.Errorf("SubPath = %q, want packs/beta", capturedReq.SubPath)
	}
	if capturedReq.Name != "beta" {
		t.Errorf("Name = %q, want beta", capturedReq.Name)
	}
	if capturedReq.Register {
		t.Error("Register should be false")
	}
}

func TestPackInstallMissing_PackAddError(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestProfile(t, configDir, "test", []string{"alpha", "beta", "gamma"})

	writeTestRegistry(t, configDir, map[string]config.RegistryEntry{
		"alpha": {Repo: "https://example.com/a.git", Ref: "main"},
		"beta":  {Repo: "https://example.com/b.git", Ref: "main"},
		"gamma": {Repo: "https://example.com/c.git", Ref: "main"},
	})

	calls := 0
	failOnBeta := func(_ context.Context, req PackAddRequest, w io.Writer) error {
		calls++
		if req.Name == "beta" {
			return fmt.Errorf("simulated failure")
		}
		writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
		return nil
	}

	var out bytes.Buffer
	results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
		ConfigDir:   configDir,
		ProfileName: "test",
		PackAddFn:   failOnBeta,
	}, &out)
	if err != nil {
		t.Fatalf("PackInstallMissing: %v", err)
	}

	// All three packs should be attempted.
	if calls != 3 {
		t.Errorf("expected 3 PackAdd calls, got %d", calls)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Status != "installed" {
		t.Errorf("alpha: expected installed, got %q", results[0].Status)
	}
	if results[1].Status != "error" {
		t.Errorf("beta: expected error, got %q", results[1].Status)
	}
	if results[2].Status != "installed" {
		t.Errorf("gamma: expected installed, got %q", results[2].Status)
	}
}

// setupSeedConflictTest creates a pack with a "team" profile and a configDir
// with a stale version of that profile already in place. Returns packDir, configDir.
func setupSeedConflictTest(t *testing.T, staleContent string) (string, string) {
	t.Helper()
	packDir := t.TempDir()
	configDir := t.TempDir()

	m := map[string]any{
		"schema_version": 1,
		"name":           "test-pack",
		"version":        "2.0.0",
		"root":           ".",
		"profiles":       []string{"profiles/team.yaml"},
	}
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600)
	os.MkdirAll(filepath.Join(packDir, "profiles"), 0o700)
	os.WriteFile(filepath.Join(packDir, "profiles", "team.yaml"),
		[]byte("schema_version: 1\npacks:\n  - updated-pack\n"), 0o600)

	writeSeedProfile(t, configDir, "default")
	writeSeedSyncConfig(t, configDir)
	os.WriteFile(filepath.Join(configDir, "profiles", "team.yaml"),
		[]byte(staleContent), 0o600)

	return packDir, configDir
}

func TestPackSeedProfiles_SeedOverwritesExisting(t *testing.T) {
	t.Parallel()
	packDir, configDir := setupSeedConflictTest(t, "schema_version: 1\npacks:\n  - stale-pack\n")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		Seed:      true,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(configDir, "profiles", "team.yaml"))
	if err != nil {
		t.Fatalf("failed to read profile: %v", err)
	}
	if strings.Contains(string(got), "stale-pack") {
		t.Fatalf("expected profile to be overwritten with --seed, but still contains stale content:\n%s", got)
	}
	if !strings.Contains(string(got), "updated-pack") {
		t.Fatalf("expected profile to contain updated content, got:\n%s", got)
	}
}

func TestPackSeedProfiles_ConfirmFnPromptsOnConflict(t *testing.T) {
	t.Parallel()
	packDir, configDir := setupSeedConflictTest(t, "schema_version: 1\npacks:\n  - stale-pack\n")

	confirmed := false
	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		SeedConfirmFn: func(name string) bool {
			confirmed = true
			return true
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	if !confirmed {
		t.Fatal("expected SeedConfirmFn to be called for conflicting profile")
	}

	got, err := os.ReadFile(filepath.Join(configDir, "profiles", "team.yaml"))
	if err != nil {
		t.Fatalf("failed to read profile: %v", err)
	}
	if !strings.Contains(string(got), "updated-pack") {
		t.Fatalf("expected profile to be replaced after confirmation, got:\n%s", got)
	}
}

func TestPackSeedProfiles_ConfirmFnDeclinesKeepsExisting(t *testing.T) {
	t.Parallel()
	packDir, configDir := setupSeedConflictTest(t, "schema_version: 1\npacks:\n  - existing-pack\n")

	var out bytes.Buffer
	err := PackAdd(context.Background(), PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  true,
		Profile:   "default",
		SeedConfirmFn: func(name string) bool {
			return false
		},
		NowFn: func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(configDir, "profiles", "team.yaml"))
	if err != nil {
		t.Fatalf("failed to read profile: %v", err)
	}
	if !strings.Contains(string(got), "existing-pack") {
		t.Fatalf("expected profile to be preserved after declined confirmation, got:\n%s", got)
	}
}
