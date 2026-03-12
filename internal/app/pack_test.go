package app

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"

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
	err := PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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

func TestPackAdd_NoRegister(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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
	_ = PackAdd(PackAddRequest{
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
	_ = PackAdd(PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
	}, &out)

	out.Reset()
	if err := PackRemove(configDir, "test-pack", &out); err != nil {
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
	err := PackRemove(configDir, "nonexistent", &out)
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
	_ = PackAdd(PackAddRequest{PackPath: packDir1, ConfigDir: configDir, Link: true, Register: false}, &out)
	out.Reset()
	err := PackAdd(PackAddRequest{PackPath: packDir2, ConfigDir: configDir, Link: true, Register: false}, &out)
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
	if err := PackAdd(req, &out); err != nil {
		t.Fatalf("first PackAdd: %v", err)
	}
	out.Reset()
	if err := PackAdd(req, &out); err != nil {
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
func fakeCloneGitFn(t *testing.T, packName string) func(args ...string) error {
	t.Helper()
	return func(args ...string) error {
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
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
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

func TestPackAdd_URL_GenericRepository_SkipsPackURLProbe(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	urlChecks := 0
	err := PackAdd(PackAddRequest{
		URL:       "https://example.com/team/my-pack.git",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn: func(string) (bool, error) {
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
	gitFn := func(args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			cloneURL = args[3]
			writePackManifest(t, args[len(args)-1], "my-pack")
		}
		return nil
	}
	err := PackAdd(PackAddRequest{
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

func TestPackAdd_URL_GitHubBlobSubdir_InstallsExtractedPackAndRecordsSubPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	urlChecks := 0
	gitFn := func(args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, filepath.Join(dir, "packs", "team"), "team-pack")
			if err := os.WriteFile(filepath.Join(dir, "packs", "team", "marker.txt"), []byte("v1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  gitFn,
		URLOKFn: func(raw string) (bool, error) {
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
	installGit := func(args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, filepath.Join(dir, "packs", "team"), "team-pack")
			if err := os.WriteFile(filepath.Join(dir, "packs", "team", "marker.txt"), []byte("v1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/repo/blob/main/packs/team/pack.json",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  installGit,
		URLOKFn:   func(string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}

	cloneCalls := 0
	pullCalled := false
	updateGit := func(args ...string) error {
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
	results, err := PackUpdate(PackUpdateRequest{
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
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  true,
		Profile:   "default",
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
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
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
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
	err := PackAdd(PackAddRequest{
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
	_ = PackAdd(PackAddRequest{
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
	if err := PackRemove(configDir, "test-pack", &out); err != nil {
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
	err := PackAdd(PackAddRequest{
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
	if err := PackRemove(configDir, "test-pack", &out); err != nil {
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

func TestPackList_ShowsOriginInfo(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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

// --- Update tests ---

func TestPackUpdate_Link_VerifiesTarget(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	_ = PackAdd(PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{
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
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)

	pullCalled := false
	fakeGit := func(args ...string) error {
		if len(args) >= 3 && args[2] == "pull" {
			pullCalled = true
		}
		return nil
	}

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{PackPath: pack1, ConfigDir: configDir, Link: true, Register: false, NowFn: func() time.Time { return fixedNow }}, &out)
	_ = PackAdd(PackAddRequest{PackPath: pack2, ConfigDir: configDir, Link: true, Register: false, NowFn: func() time.Time { return fixedNow }}, &out)

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
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
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{
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
	_ = PackAdd(PackAddRequest{
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
	err := PackAdd(PackAddRequest{
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
	results, err := PackUpdate(PackUpdateRequest{
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
	err = PackRemove(configDir, "lifecycle-pack", &removeOut)
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

// writePackRegistry writes a registry.yaml into a pack directory.
func writePackRegistry(t *testing.T, packDir string, packs map[string]config.RegistryEntry) {
	t.Helper()
	reg := config.Registry{
		SchemaVersion: 1,
		Packs:         packs,
	}
	b, err := yaml.Marshal(&reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "registry.yaml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPackUpdate_Copy_MergesPackRegistry(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "test-pack")
	writeSeedSyncConfig(t, configDir)

	// Bundle a registry.yaml with the pack.
	writePackRegistry(t, packDir, map[string]config.RegistryEntry{
		"extra-pack": {Repo: "https://github.com/example/extra-pack", Description: "An extra pack"},
	})

	// Install the pack (copy mode).
	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      false,
		Register:  false,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Verify registry was seeded on initial add.
	regPath := filepath.Join(configDir, "registry.yaml")
	reg, err := config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry after add: %v", err)
	}
	if _, ok := reg.Packs["extra-pack"]; !ok {
		t.Fatal("expected extra-pack in registry after add")
	}

	// Now add a new entry to the source pack's registry.yaml (simulating
	// an upstream pack update that adds a new registry entry).
	writePackRegistry(t, packDir, map[string]config.RegistryEntry{
		"extra-pack":  {Repo: "https://github.com/example/extra-pack", Description: "An extra pack"},
		"second-pack": {Repo: "https://github.com/example/second-pack", Description: "A second pack"},
	})

	// Run PackUpdate — this should re-copy and re-merge the registry.
	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "test-pack",
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 || results[0].Status != "updated" {
		t.Fatalf("expected 1 updated result, got %+v", results)
	}

	// Verify the new registry entry was merged.
	reg, err = config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry after update: %v", err)
	}
	if _, ok := reg.Packs["extra-pack"]; !ok {
		t.Fatal("expected extra-pack to still be in registry after update")
	}
	if _, ok := reg.Packs["second-pack"]; !ok {
		t.Fatal("expected second-pack to be merged into registry after update")
	}
}

func TestPackUpdate_Clone_MergesPackRegistry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	// Custom git fn that writes both pack.json and registry.yaml.
	fakeGit := func(args ...string) error {
		if len(args) >= 4 && args[0] == "clone" {
			dir := args[len(args)-1]
			writePackManifest(t, dir, "my-pack")
			writePackRegistry(t, dir, map[string]config.RegistryEntry{
				"bundled-pack": {Repo: "https://github.com/example/bundled-pack", Description: "Bundled"},
			})
		}
		return nil
	}

	// Install via URL (clone) with --seed to enable registry seeding.
	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		Seed:      true,
		RunGitFn:  fakeGit,
		URLOKFn:   func(string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd URL: %v", err)
	}

	// Verify initial registry seed.
	regPath := filepath.Join(configDir, "registry.yaml")
	reg, err := config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry after add: reading registry: %v", err)
	}
	if _, ok := reg.Packs["bundled-pack"]; !ok {
		t.Fatal("expected bundled-pack in registry after add")
	}

	// Update git fn adds a new registry entry.
	updateGit := func(args ...string) error {
		// On pull, update the pack dir's registry.yaml with a new entry.
		if len(args) >= 3 && args[2] == "pull" {
			packDir := filepath.Join(configDir, "packs", "my-pack")
			writePackRegistry(t, packDir, map[string]config.RegistryEntry{
				"bundled-pack": {Repo: "https://github.com/example/bundled-pack", Description: "Bundled"},
				"new-pack":     {Repo: "https://github.com/example/new-pack", Description: "New"},
			})
		}
		return nil
	}

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		RunGitFn:  updateGit,
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 || results[0].Status != "updated" {
		t.Fatalf("expected 1 updated result, got %+v", results)
	}

	// Verify the new entry was merged.
	reg, err = config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry after update: %v", err)
	}
	if _, ok := reg.Packs["bundled-pack"]; !ok {
		t.Fatal("expected bundled-pack to still be in registry after update")
	}
	if _, ok := reg.Packs["new-pack"]; !ok {
		t.Fatal("expected new-pack to be merged into registry after update")
	}
}

// --- Commit hash tracking tests ---

const fakeHash1 = "aabbccdd1122334455667788"
const fakeHash2 = "11223344556677889900aabb"

func fakeHashFn(hash string) func(string) (string, error) {
	return func(string) (string, error) { return hash, nil }
}

func TestPackAdd_URL_RecordsCommitHash(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSeedSyncConfig(t, configDir)

	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
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
	err := PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Update — new hash.
	fakeGit := func(args ...string) error { return nil }
	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
		NowFn:     func() time.Time { return fixedNow },
		GitHashFn: fakeHashFn(fakeHash1),
	}, &out)

	// Update with same hash — should be up-to-date.
	fakeGit := func(args ...string) error { return nil }
	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
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
	_ = PackAdd(PackAddRequest{
		URL:       "https://github.com/example/my-pack",
		ConfigDir: configDir,
		Register:  false,
		RunGitFn:  fakeCloneGitFn(t, "my-pack"),
		URLOKFn:   func(string) (bool, error) { return true, nil },
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
	if err := os.WriteFile(filepath.Join(configDir, "registry.yaml"), b, 0o600); err != nil {
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
	results, err := PackInstallMissing(PackInstallMissingRequest{
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
	results, err := PackInstallMissing(PackInstallMissingRequest{
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
	fakePack := func(req PackAddRequest, w io.Writer) error {
		capturedReq = req
		// Simulate install by creating the pack dir.
		writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
		return nil
	}

	var out bytes.Buffer
	results, err := PackInstallMissing(PackInstallMissingRequest{
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
	failOnBeta := func(req PackAddRequest, w io.Writer) error {
		calls++
		if req.Name == "beta" {
			return fmt.Errorf("simulated failure")
		}
		writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
		return nil
	}

	var out bytes.Buffer
	results, err := PackInstallMissing(PackInstallMissingRequest{
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

// --- archive test helpers ---

// buildTestTar creates a tar archive in memory from a map of filename -> content.
func buildTestTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeArchiveFn returns an ArchiveFn that serves different content based on
// the requested ref. manifestJSON is the pack.json content; extraFiles is
// extra content keyed by filename.
func fakeArchiveFn(t *testing.T, manifestJSON string, extraFiles map[string]string) func(repoURL, ref string, paths []string) ([]byte, error) {
	t.Helper()
	return func(repoURL, ref string, paths []string) ([]byte, error) {
		files := make(map[string]string)
		for _, p := range paths {
			if strings.HasSuffix(p, "pack.json") {
				files[p] = manifestJSON
			} else {
				// Serve extra files that match requested paths (prefix match for dirs).
				for name, content := range extraFiles {
					if strings.HasPrefix(name, p) || name == p {
						files[name] = content
					}
				}
			}
		}
		return buildTestTar(t, files), nil
	}
}

// writeRegistryCache writes a cached registry YAML into the config dir with a
// single pack entry. The sync-config must have a matching registry source.
func writeRegistryCache(t *testing.T, configDir, sourceName, packName string, entry config.RegistryEntry) {
	t.Helper()
	reg := config.Registry{
		SchemaVersion: 1,
		Packs:         map[string]config.RegistryEntry{packName: entry},
	}
	data, err := yaml.Marshal(&reg)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := config.RegistriesCacheDir(configDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SourceCachePath(configDir, sourceName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeSyncConfigWithSource writes a sync-config that includes a registry source.
func writeSyncConfigWithSource(t *testing.T, configDir, sourceName, sourceURL string) {
	t.Helper()
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.Defaults.Profile = "default"
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: sourceName, URL: sourceURL},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
}

func TestPackUpdate_Archive_ReResolvesRegistryRef(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	manifest := `{"schema_version":1,"name":"my-pack","version":"1.0.0","root":".","rules":["old"]}`
	oldFiles := map[string]string{"rules/old.md": "old content"}
	newFiles := map[string]string{"rules/old.md": "new content"}

	// Install with ref "old-branch" via archive.
	writeSyncConfigWithSource(t, configDir, "test-source", "ssh://git@example.com/repo.git")
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Ref:       "old-branch",
		Name:      "my-pack",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, manifest, oldFiles),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Verify it was recorded with old-branch ref.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if sc.InstalledPacks["my-pack"].Ref != "old-branch" {
		t.Fatalf("stored ref = %q, want old-branch", sc.InstalledPacks["my-pack"].Ref)
	}

	// Update registry cache to point to "new-branch".
	writeRegistryCache(t, configDir, "test-source", "my-pack", config.RegistryEntry{
		Repo: "ssh://git@example.com/repo.git",
		Ref:  "new-branch",
	})

	// Track which ref the archive function receives during update.
	var capturedRefs []string
	trackingArchive := func(repoURL, ref string, paths []string) ([]byte, error) {
		capturedRefs = append(capturedRefs, ref)
		return fakeArchiveFn(t, manifest, newFiles)(repoURL, ref, paths)
	}

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		NowFn:     func() time.Time { return fixedNow },
		ArchiveFn: trackingArchive,
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "updated" {
		t.Fatalf("status = %q, want updated; output:\n%s", results[0].Status, out.String())
	}

	// Verify the archive was fetched with the registry's new ref, not the stored old one.
	foundNewRef := false
	for _, r := range capturedRefs {
		if r == "new-branch" {
			foundNewRef = true
		}
		if r == "old-branch" {
			t.Fatal("archive fetch used stale ref 'old-branch' instead of registry's 'new-branch'")
		}
	}
	if !foundNewRef {
		t.Fatalf("expected archive fetch with ref 'new-branch', captured refs: %v", capturedRefs)
	}

	// Verify the updated metadata records the new ref.
	sc, _ = config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if sc.InstalledPacks["my-pack"].Ref != "new-branch" {
		t.Fatalf("updated ref = %q, want new-branch", sc.InstalledPacks["my-pack"].Ref)
	}
}

func TestPackUpdate_Archive_FallsBackWhenNotInRegistry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	manifest := `{"schema_version":1,"name":"my-pack","version":"1.0.0","root":".","rules":["example"]}`
	files := map[string]string{"rules/example.md": "content"}

	// Install with ref "main" but no registry entry.
	writeSeedSyncConfig(t, configDir)
	writeSeedProfile(t, configDir, "default")

	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Ref:       "main",
		Name:      "my-pack",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, manifest, files),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	// Update with no registry — should use stored metadata ref.
	var capturedRefs []string
	trackingArchive := func(repoURL, ref string, paths []string) ([]byte, error) {
		capturedRefs = append(capturedRefs, ref)
		return fakeArchiveFn(t, manifest, files)(repoURL, ref, paths)
	}

	out.Reset()
	results, err := PackUpdate(PackUpdateRequest{
		ConfigDir: configDir,
		Name:      "my-pack",
		NowFn:     func() time.Time { return fixedNow },
		ArchiveFn: trackingArchive,
	}, &out)
	if err != nil {
		t.Fatalf("PackUpdate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Content is the same, so should be up-to-date.
	if results[0].Status != "up-to-date" {
		t.Fatalf("status = %q, want up-to-date", results[0].Status)
	}

	// Verify the stored ref "main" was used (no registry override).
	for _, r := range capturedRefs {
		if r != "main" {
			t.Fatalf("expected ref 'main', got %q", r)
		}
	}
}

func TestPackAdd_URL_Install_ShowsContentUnchanged(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSyncConfigWithSource(t, configDir, "test-source", "ssh://git@example.com/repo.git")
	writeSeedProfile(t, configDir, "default")

	manifest := `{"schema_version":1,"name":"my-pack","version":"1.0.0","root":".","rules":["example"]}`
	files := map[string]string{"rules/example.md": "# Example\ncontent"}

	// First install.
	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Name:      "my-pack",
		Ref:       "main",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, manifest, files),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install with same content.
	out.Reset()
	err = PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Name:      "my-pack",
		Ref:       "main",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, manifest, files),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}

	if !strings.Contains(out.String(), "Content unchanged") {
		t.Fatalf("expected 'Content unchanged' in output, got:\n%s", out.String())
	}
}

func TestPackAdd_URL_Install_ShowsChanges(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeSyncConfigWithSource(t, configDir, "test-source", "ssh://git@example.com/repo.git")
	writeSeedProfile(t, configDir, "default")

	oldManifest := `{"schema_version":1,"name":"my-pack","version":"1.0.0","root":".","rules":["example"]}`
	newManifest := `{"schema_version":1,"name":"my-pack","version":"1.0.0","root":".","rules":["example","added"]}`
	oldFiles := map[string]string{"rules/example.md": "# Old\ncontent"}
	newFiles := map[string]string{"rules/example.md": "# New\ncontent", "rules/added.md": "# Added"}

	// First install.
	var out bytes.Buffer
	err := PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Name:      "my-pack",
		Ref:       "main",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, oldManifest, oldFiles),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install with different content.
	out.Reset()
	err = PackAdd(PackAddRequest{
		URL:       "ssh://git@example.com/repo.git",
		ConfigDir: configDir,
		Name:      "my-pack",
		Ref:       "main",
		Register:  false,
		ArchiveFn: fakeArchiveFn(t, newManifest, newFiles),
		NowFn:     func() time.Time { return fixedNow },
	}, &out)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}

	output := out.String()
	if strings.Contains(output, "Content unchanged") {
		t.Fatal("should NOT say 'Content unchanged' when content differs")
	}
	if !strings.Contains(output, "Changes:") {
		t.Fatalf("expected 'Changes:' in output, got:\n%s", output)
	}
}
