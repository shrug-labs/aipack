package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

const testRegistryYAML = `
schema_version: 1
packs:
  alpha-pack:
    repo: https://github.com/org/alpha
    path: packs/alpha
    description: Alpha operational runbooks
    owner: Team Alpha
  beta-tools:
    repo: https://github.com/org/beta
    description: Beta API review tools
    owner: Team Beta
  gamma-kb:
    repo: https://github.com/org/gamma
    description: Gamma knowledge base
`

func setupTestRegistry(t *testing.T) RegistryListRequest {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.yaml")
	os.WriteFile(regPath, []byte(testRegistryYAML), 0o600)
	return RegistryListRequest{ConfigDir: dir, RegistryPath: regPath}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistryList(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Should be sorted by name.
	if results[0].Name != "alpha-pack" {
		t.Errorf("first result = %q, want alpha-pack", results[0].Name)
	}
	if results[0].Path != "packs/alpha" {
		t.Errorf("alpha-pack Path = %q, want packs/alpha", results[0].Path)
	}
}

func TestRegistrySearch_SubstringMatch(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistrySearch(req, "api review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "beta-tools" {
		t.Errorf("got %q, want beta-tools", results[0].Name)
	}
}

func TestRegistrySearch_CaseInsensitive(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistrySearch(req, "ALPHA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "alpha-pack" {
		t.Errorf("got %q, want alpha-pack", results[0].Name)
	}
}

func TestRegistrySearch_MatchesName(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistrySearch(req, "gamma")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestRegistrySearch_NoMatch(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistrySearch(req, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestRegistryLookup_Found(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	entry, err := RegistryLookup(req, "alpha-pack")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Repo != "https://github.com/org/alpha" {
		t.Errorf("Repo = %q", entry.Repo)
	}
	if entry.Path != "packs/alpha" {
		t.Errorf("Path = %q", entry.Path)
	}
}

func TestRegistryLookup_NotFound(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	_, err := RegistryLookup(req, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing pack")
	}
}

// --- RegistryFetch tests ---

const testRemoteRegistryYAML = `schema_version: 1
packs:
  delta-pack:
    repo: https://github.com/org/delta
    description: Delta tools
  epsilon-pack:
    repo: https://github.com/org/epsilon
    description: Epsilon runbooks
`

func fakeFetchFn(data string) func(string) ([]byte, error) {
	return func(url string) ([]byte, error) {
		return []byte(data), nil
	}
}

func TestRegistryFetch_CachesRemoteRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		FetchFn:   fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cache file was created.
	cachePath := config.SourceCachePath(dir, "example")
	reg, err := config.LoadRegistry(cachePath)
	if err != nil {
		t.Fatalf("loading cached registry: %v", err)
	}
	if len(reg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(reg.Packs))
	}
	if _, ok := reg.Packs["delta-pack"]; !ok {
		t.Error("missing delta-pack")
	}
	if _, ok := reg.Packs["epsilon-pack"]; !ok {
		t.Error("missing epsilon-pack")
	}

	output := buf.String()
	if !strings.Contains(output, "2 pack(s)") {
		t.Errorf("expected '2 pack(s)' in output, got: %s", output)
	}
}

func TestRegistryFetch_SavesSourceToSyncConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		FetchFn:   fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(sc.RegistrySources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.RegistrySources))
	}
	src := sc.RegistrySources[0]
	if src.URL != "https://example.com/registry.yaml" {
		t.Errorf("source URL = %q", src.URL)
	}
	if src.Name != "example" {
		t.Errorf("source Name = %q, want example", src.Name)
	}
}

func TestRegistryFetch_CacheOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// First fetch: 2 packs.
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		FetchFn:   fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Second fetch: remote now has only 1 pack (delta removed).
	updatedYAML := `schema_version: 1
packs:
  epsilon-pack:
    repo: https://github.com/org/epsilon
    description: Epsilon runbooks v2
`
	buf.Reset()
	err = RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		FetchFn:   fakeFetchFn(updatedYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	// Cache should reflect the updated remote — delta-pack gone.
	cachePath := config.SourceCachePath(dir, "example")
	reg, err := config.LoadRegistry(cachePath)
	if err != nil {
		t.Fatalf("loading cached registry: %v", err)
	}
	if len(reg.Packs) != 1 {
		t.Fatalf("expected 1 pack after update, got %d", len(reg.Packs))
	}
	if reg.Packs["epsilon-pack"].Description != "Epsilon runbooks v2" {
		t.Errorf("description not updated: %q", reg.Packs["epsilon-pack"].Description)
	}
}

func TestRegistryFetch_GitArbitraryRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRepo, capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://bitbucket.example.com/scm/TEAM/my-tools.git",
		Ref:       "team/ai-runbooks",
		Path:      "ai-runbooks/registry.yaml",
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			capturedRepo = repo
			capturedRef = ref
			capturedPath = path
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedRepo != "https://bitbucket.example.com/scm/TEAM/my-tools.git" {
		t.Errorf("repo = %q", capturedRepo)
	}
	if capturedRef != "team/ai-runbooks" {
		t.Errorf("ref = %q", capturedRef)
	}
	if capturedPath != "ai-runbooks/registry.yaml" {
		t.Errorf("path = %q", capturedPath)
	}

	// Verify source saved with git coordinates.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if len(sc.RegistrySources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.RegistrySources))
	}
	src := sc.RegistrySources[0]
	if src.Ref != "team/ai-runbooks" {
		t.Errorf("source Ref = %q", src.Ref)
	}
	if src.Path != "ai-runbooks/registry.yaml" {
		t.Errorf("source Path = %q", src.Path)
	}
}

func TestRegistryFetch_GitAutoDetect(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://github.com/org/my-packs.git",
		// No --ref or --path: should auto-detect git and use defaults.
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			capturedRef = ref
			capturedPath = path
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedRef != "main" {
		t.Errorf("ref = %q, want main", capturedRef)
	}
	if capturedPath != "registry.yaml" {
		t.Errorf("path = %q, want registry.yaml", capturedPath)
	}
}

func TestRegistryFetch_MultiSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-configure two sources in sync-config.
	sourceA := `schema_version: 1
packs:
  pack-a:
    repo: https://github.com/org/a
    description: Pack A
`
	sourceB := `schema_version: 1
packs:
  pack-b:
    repo: https://github.com/org/b
    description: Pack B
`

	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "source-a", URL: "https://example.com/a.yaml"},
		{Name: "source-b", URL: "https://example.com/b.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	fetchCalls := map[string]string{
		"https://example.com/a.yaml": sourceA,
		"https://example.com/b.yaml": sourceB,
	}

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			data, ok := fetchCalls[url]
			if !ok {
				t.Errorf("unexpected fetch URL: %q", url)
			}
			return []byte(data), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both caches should exist.
	regA, err := config.LoadRegistry(config.SourceCachePath(dir, "source-a"))
	if err != nil {
		t.Fatalf("loading source-a cache: %v", err)
	}
	if _, ok := regA.Packs["pack-a"]; !ok {
		t.Error("missing pack-a in source-a cache")
	}

	regB, err := config.LoadRegistry(config.SourceCachePath(dir, "source-b"))
	if err != nil {
		t.Fatalf("loading source-b cache: %v", err)
	}
	if _, ok := regB.Packs["pack-b"]; !ok {
		t.Error("missing pack-b in source-b cache")
	}
}

func TestRegistryFetch_AllSourcesFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "bad-a", URL: "https://example.com/a.yaml"},
		{Name: "bad-b", URL: "https://example.com/b.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			return nil, fmt.Errorf("network error")
		},
	}, &buf)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
	if !strings.Contains(err.Error(), "all 2 registry source(s) failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRegistryFetch_UsesDefaultGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRepo, capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			capturedRepo = repo
			capturedRef = ref
			capturedPath = path
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedRepo != config.DefaultRegistryRepo {
		t.Errorf("repo = %q, want %q", capturedRepo, config.DefaultRegistryRepo)
	}
	if capturedRef != config.DefaultRegistryRef {
		t.Errorf("ref = %q, want %q", capturedRef, config.DefaultRegistryRef)
	}
	if capturedPath != config.DefaultRegistryPath {
		t.Errorf("path = %q, want %q", capturedPath, config.DefaultRegistryPath)
	}
}

func TestRegistryFetch_URLFromSyncConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write sync-config with legacy registry_url set (backward compat).
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.Defaults.RegistryURL = "https://example.com/from-config.yaml"
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	var capturedURL string
	fetchFn := func(url string) ([]byte, error) {
		capturedURL = url
		return []byte(testRemoteRegistryYAML), nil
	}

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn:   fetchFn,
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedURL != "https://example.com/from-config.yaml" {
		t.Errorf("expected URL from sync-config, got %q", capturedURL)
	}
}

func TestRegistryRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up a source via fetch.
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		FetchFn:   fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Verify source exists.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if len(sc.RegistrySources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.RegistrySources))
	}

	// Remove it.
	buf.Reset()
	err = RegistryRemove(RegistryRemoveRequest{
		ConfigDir: dir,
		Name:      "example",
	}, &buf)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Source should be gone from sync-config.
	sc, _ = config.LoadSyncConfig(config.SyncConfigPath(dir))
	if len(sc.RegistrySources) != 0 {
		t.Errorf("expected 0 sources after remove, got %d", len(sc.RegistrySources))
	}

	// Cache file should be gone.
	cachePath := config.SourceCachePath(dir, "example")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("cache file should be removed")
	}
}

func TestRegistryRemove_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := RegistryRemove(RegistryRemoveRequest{
		ConfigDir: dir,
		Name:      "nonexistent",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

// --- Merged view tests ---

func TestRegistryList_MergedView(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Two cached sources with different packs.
	source1YAML := `schema_version: 1
packs:
  pack-a:
    repo: https://github.com/org/a
    description: Pack A
`
	source2YAML := `schema_version: 1
packs:
  pack-b:
    repo: https://github.com/org/b
    description: Pack B
`
	os.MkdirAll(config.RegistriesCacheDir(dir), 0o700)
	os.WriteFile(config.SourceCachePath(dir, "source-1"), []byte(source1YAML), 0o600)
	os.WriteFile(config.SourceCachePath(dir, "source-2"), []byte(source2YAML), 0o600)

	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "source-1", URL: "https://example.com/s1.yaml"},
		{Name: "source-2", URL: "https://example.com/s2.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	results, err := RegistryList(RegistryListRequest{ConfigDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestRegistryList_FirstSourceWinsConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// First source has "my-pack" with description A.
	source1YAML := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/first
    description: First source version
`
	// Second source has "my-pack" with description B.
	source2YAML := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/second
    description: Second source version
`
	os.MkdirAll(config.RegistriesCacheDir(dir), 0o700)
	os.WriteFile(config.SourceCachePath(dir, "first"), []byte(source1YAML), 0o600)
	os.WriteFile(config.SourceCachePath(dir, "second"), []byte(source2YAML), 0o600)

	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "first", URL: "https://example.com/first.yaml"},
		{Name: "second", URL: "https://example.com/second.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	// Lookup should return the first source's version.
	entry, err := RegistryLookup(RegistryListRequest{ConfigDir: dir}, "my-pack")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Description != "First source version" {
		t.Errorf("expected first source to win, got %q", entry.Description)
	}
}

// ---------------------------------------------------------------------------
// RegistrySources tests
// ---------------------------------------------------------------------------

func TestRegistrySources_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	sources, err := RegistrySources(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

func TestRegistrySources_WithCachedAndUncached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Configure two sources, only cache one.
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "cached-src", URL: "https://example.com/a.yaml"},
		{Name: "uncached-src", URL: "https://example.com/b.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	cachedYAML := `schema_version: 1
packs:
  pack-a:
    repo: https://github.com/org/a
`
	os.MkdirAll(config.RegistriesCacheDir(dir), 0o700)
	os.WriteFile(config.SourceCachePath(dir, "cached-src"), []byte(cachedYAML), 0o600)

	sources, err := RegistrySources(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	if sources[0].Name != "cached-src" || !sources[0].Cached {
		t.Errorf("source 0: name=%q cached=%v, want cached-src/true", sources[0].Name, sources[0].Cached)
	}
	if sources[1].Name != "uncached-src" || sources[1].Cached {
		t.Errorf("source 1: name=%q cached=%v, want uncached-src/false", sources[1].Name, sources[1].Cached)
	}
}

// ---------------------------------------------------------------------------
// RegistryAddSource tests
// ---------------------------------------------------------------------------

func TestRegistryAddSource_AddsToSyncConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	err := RegistryAddSource(RegistryAddSourceRequest{
		ConfigDir: dir,
		URL:       "git@github.com:org/tools.git",
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(sc.RegistrySources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.RegistrySources))
	}
	src := sc.RegistrySources[0]
	if src.Name != "tools" {
		t.Errorf("source Name = %q, want tools", src.Name)
	}
	if src.URL != "git@github.com:org/tools.git" {
		t.Errorf("source URL = %q", src.URL)
	}
	if src.Ref != "main" {
		t.Errorf("source Ref = %q, want main (default for git URL)", src.Ref)
	}
}

func TestRegistryAddSource_CustomName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	err := RegistryAddSource(RegistryAddSourceRequest{
		ConfigDir: dir,
		URL:       "https://example.com/registry.yaml",
		Name:      "my-team",
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if sc.RegistrySources[0].Name != "my-team" {
		t.Errorf("source Name = %q, want my-team", sc.RegistrySources[0].Name)
	}
}

// ---------------------------------------------------------------------------
// Installed marker tests
// ---------------------------------------------------------------------------

func TestRegistryList_ShowsInstalledFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a cached registry source with two packs.
	cachedYAML := `schema_version: 1
packs:
  installed-pack:
    repo: https://github.com/org/installed
    description: An installed pack
  available-pack:
    repo: https://github.com/org/available
    description: Not installed
`
	os.MkdirAll(config.RegistriesCacheDir(dir), 0o700)
	os.WriteFile(config.SourceCachePath(dir, "test"), []byte(cachedYAML), 0o600)

	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "test", URL: "https://example.com/test.yaml"},
	}
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	// Simulate one pack being installed.
	os.MkdirAll(filepath.Join(dir, "packs", "installed-pack"), 0o755)

	results, err := RegistryList(RegistryListRequest{ConfigDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Name == "installed-pack" && !r.Installed {
			t.Errorf("installed-pack should be marked as installed")
		}
		if r.Name == "available-pack" && r.Installed {
			t.Errorf("available-pack should not be marked as installed")
		}
	}
}

// ---------------------------------------------------------------------------
// Deep index tests
// ---------------------------------------------------------------------------

func TestDeepIndexOnePack_RespectsPackJsonRoot(t *testing.T) {
	t.Parallel()

	cloneFn := func(repo, dir, ref string) error {
		packJSON := `{"schema_version": 1, "name": "test-pack", "version": "1.0", "root": "content", "rules": ["safety"]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(packJSON), 0o644); err != nil {
			return err
		}
		rulesDir := filepath.Join(dir, "content", "rules")
		if err := os.MkdirAll(rulesDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(rulesDir, "safety.md"), []byte("---\ndescription: Safety rule\n---\nBe safe\n"), 0o644); err != nil {
			return err
		}
		return nil
	}

	entry := config.RegistryEntry{
		Repo: "https://example.com/test.git",
		Ref:  "main",
		Path: "",
	}

	resources, err := deepIndexOnePack(entry, cloneFn)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) == 0 {
		t.Fatal("expected at least 1 resource from content/ subdirectory, got 0")
	}

	found := false
	for _, r := range resources {
		if r.Kind == "rule" && r.Name == "safety" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find rule 'safety' in resources, got: %v", resources)
	}
}
