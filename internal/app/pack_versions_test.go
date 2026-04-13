package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

func TestPackListVersions_RequiresName(t *testing.T) {
	t.Parallel()
	_, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestPackListVersions_FromLockfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Seed lockfile with an installed pack pinned to 1.2.3.
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"my-pack": {
				Origin: "https://github.com/example/my-pack",
				Method: config.MethodClone,
				Ref:    "v1.2.3",
			},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	result, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "my-pack",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v1.2.3", "v2.0.0", "release-x"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Origin != "https://github.com/example/my-pack" {
		t.Errorf("origin = %q", result.Origin)
	}
	if result.InstalledVersion != "1.2.3" {
		t.Errorf("installed version = %q", result.InstalledVersion)
	}
	// Should be 3 semver tags, sorted descending, "release-x" filtered out.
	want := []string{"v2.0.0", "v1.2.3", "v1.0.0"}
	if len(result.Versions) != len(want) {
		t.Fatalf("got %d versions, want %d: %+v", len(result.Versions), len(want), result.Versions)
	}
	for i, v := range result.Versions {
		if v.Version != want[i] {
			t.Errorf("[%d] = %q, want %q", i, v.Version, want[i])
		}
	}
	// v1.2.3 should be marked installed.
	if !result.Versions[1].Installed {
		t.Errorf("v1.2.3 should be marked installed")
	}
	// Others should not be marked.
	if result.Versions[0].Installed || result.Versions[2].Installed {
		t.Errorf("only the pinned version should be marked installed")
	}
}

func TestPackListVersions_FromRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a cached registry source. LoadMergedRegistry reads sources from
	// sync-config and loads each from the registries cache directory.
	regYAML := `schema_version: 1
packs:
  some-pack:
    repo: https://github.com/example/some-pack
    description: example
`
	cacheDir := config.RegistriesCacheDir(dir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "test-source.yaml")
	if err := os.WriteFile(cachePath, []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "test-source", URL: "https://example.com/registry.yaml"},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}

	result, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "some-pack",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v0.1.0", "v0.2.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Origin != "https://github.com/example/some-pack" {
		t.Errorf("origin = %q", result.Origin)
	}
	if result.InstalledVersion != "" {
		t.Errorf("installed version = %q, want empty (not installed)", result.InstalledVersion)
	}
	if len(result.Versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(result.Versions))
	}
	// None should be marked installed.
	for _, v := range result.Versions {
		if v.Installed {
			t.Errorf("no version should be marked installed: %v", v)
		}
	}
}

func TestPackListVersions_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "nonexistent",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			t.Fatal("ListRemoteTagsFn should not be called when pack is not resolvable")
			return nil, nil
		},
	})
	if err == nil {
		t.Fatal("expected error for unresolvable pack")
	}
}

func TestPackListVersions_NoSemverTags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"untagged": {Origin: "https://example.com/repo", Method: config.MethodClone},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	result, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "untagged",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"release-1", "milestone-2"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Versions) != 0 {
		t.Errorf("got %d versions, want 0 (no semver tags)", len(result.Versions))
	}
}

// TestPackListVersions_RejectsNonCloneMethod ensures that packs installed via
// link/copy/local return a clear error instead of running git ls-remote against
// a local filesystem path (which could silently match an unrelated local repo).
func TestPackListVersions_RejectsNonCloneMethod(t *testing.T) {
	t.Parallel()
	methods := []string{
		config.MethodLink,
		config.MethodCopy,
		config.MethodLocal,
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			lf := config.Lockfile{
				LockVersion: config.LockfileVersion,
				Packs: map[string]config.InstalledPackMeta{
					"local-pack": {
						Origin: "/tmp/some/path",
						Method: method,
					},
				},
			}
			if err := config.SaveLockfile(config.LockfilePath(dir), lf); err != nil {
				t.Fatal(err)
			}

			_, err := PackListVersions(context.Background(), PackListVersionsRequest{
				ConfigDir: dir,
				Name:      "local-pack",
				ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
					t.Fatalf("ListRemoteTagsFn should not be called for %s installs", method)
					return nil, nil
				},
			})
			if err == nil {
				t.Fatalf("expected error for %s-method pack", method)
			}
			if !strings.Contains(err.Error(), method) {
				t.Errorf("error should mention install method %q: %v", method, err)
			}
		})
	}
}

func TestPackListVersions_CommitHashPinNoMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"my-pack": {
				Origin: "https://github.com/example/my-pack",
				Method: config.MethodClone,
				Ref:    "abc1234", // commit hash, not semver
			},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	result, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "my-pack",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"v1.0.0", "v2.0.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InstalledVersion != "abc1234" {
		t.Errorf("installed version = %q, want abc1234", result.InstalledVersion)
	}
	// No tag should be marked installed (commit hash doesn't match any tag).
	for _, v := range result.Versions {
		if v.Installed {
			t.Errorf("commit hash pin should not mark any tag: %v", v)
		}
	}
}

func TestPackListVersions_PreservesBareRemoteTagSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"my-pack": {
				Origin: "https://github.com/example/my-pack",
				Method: config.MethodClone,
				Ref:    "v1.2.3",
			},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	result, err := PackListVersions(context.Background(), PackListVersionsRequest{
		ConfigDir: dir,
		Name:      "my-pack",
		ListRemoteTagsFn: func(context.Context, string) ([]string, error) {
			return []string{"1.2.3", "2.0.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Versions[1].Version; got != "1.2.3" {
		t.Fatalf("version = %q, want raw remote tag", got)
	}
	if !result.Versions[1].Installed {
		t.Fatalf("bare remote tag should still be marked installed")
	}
}
