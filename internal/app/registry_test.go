package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
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
collections:
  starter:
    description: Starter pack set
    packs:
      - alpha-pack
      - name: beta-tools
        ref: v1.2.3
        with: [profiles]
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

func TestRegistryCollectionList(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	results, err := RegistryCollectionList(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "starter" {
		t.Fatalf("collection name = %q, want starter", results[0].Name)
	}
	if len(results[0].Packs) != 2 {
		t.Fatalf("collection packs = %d, want 2", len(results[0].Packs))
	}
}

func TestRegistryCollectionLookup_NotFound(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	_, err := RegistryCollectionLookup(req, "missing")
	if err == nil {
		t.Fatal("expected error for missing collection")
	}
	var notFound RegistryCollectionNotFoundError
	if !errors.As(err, &notFound) || notFound.Name != "missing" {
		t.Fatalf("error = %v, want RegistryCollectionNotFoundError for missing", err)
	}
}

func TestCollectionInstall_InstallsRegistryPacks(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)

	var installs []PackInstallRequest
	result, err := CollectionInstall(context.Background(), CollectionInstallRequest{
		ConfigDir:    req.ConfigDir,
		RegistryPath: req.RegistryPath,
		Name:         "starter",
		Add:          true,
		Profile:      "dev",
		PackInstallFn: func(_ context.Context, installReq PackInstallRequest, _ io.Writer) error {
			installs = append(installs, installReq)
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("CollectionInstall: %v", err)
	}
	if len(result.Packs) != 2 || len(installs) != 2 {
		t.Fatalf("installed result=%d requests=%d, want 2", len(result.Packs), len(installs))
	}
	if installs[0].Name != "alpha-pack" || installs[0].SubPath != "packs/alpha" {
		t.Fatalf("first install = %+v, want alpha-pack from packs/alpha", installs[0])
	}
	if installs[1].Name != "beta-tools" || installs[1].Ref != "v1.2.3" {
		t.Fatalf("second install = %+v, want beta-tools ref v1.2.3", installs[1])
	}
	if !installs[1].With.Has(domain.BundledProfiles) {
		t.Fatalf("second install With = %v, want profiles", installs[1].With)
	}
	if installs[0].Profile != "dev" || !installs[0].Add {
		t.Fatalf("collection install did not propagate add/profile: %+v", installs[0])
	}
}

func TestCollectionInstall_WithOverrideAppliesToEveryPack(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)

	override := domain.BundledAll()
	var installs []PackInstallRequest
	_, err := CollectionInstall(context.Background(), CollectionInstallRequest{
		ConfigDir:    req.ConfigDir,
		RegistryPath: req.RegistryPath,
		Name:         "starter",
		With:         override,
		PackInstallFn: func(_ context.Context, installReq PackInstallRequest, _ io.Writer) error {
			installs = append(installs, installReq)
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("CollectionInstall: %v", err)
	}
	for _, installReq := range installs {
		for _, cat := range domain.AllBundledCategories {
			if !installReq.With.Has(cat) {
				t.Fatalf("install %s missing override category %s", installReq.Name, cat)
			}
		}
	}
}

func TestCollectionInstall_RejectsArchiveRef(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(regPath, []byte(`schema_version: 1
packs:
  archive-pack:
    method: archive
    url: https://example.com/archive.zip
collections:
  starter:
    packs:
      - name: archive-pack
        ref: v1.0.0
`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := CollectionInstall(context.Background(), CollectionInstallRequest{
		ConfigDir:    dir,
		RegistryPath: regPath,
		Name:         "starter",
		PackInstallFn: func(_ context.Context, _ PackInstallRequest, _ io.Writer) error {
			t.Fatal("pack install should not be called")
			return nil
		},
	}, nil)
	if err == nil {
		t.Fatal("expected archive ref to fail")
	}
	if len(result.Packs) != 1 || result.Packs[0].Status != "error" {
		t.Fatalf("result = %+v, want one error", result)
	}
}

func TestRegistryLookup_FoundationalPackHint(t *testing.T) {
	t.Parallel()
	req := RegistryListRequest{ConfigDir: t.TempDir()}
	_, err := RegistryLookup(req, "aipack-core")
	if err == nil {
		t.Fatal("expected error for missing pack")
	}
	if !strings.Contains(err.Error(), "aipack registry fetch "+config.DefaultRegistryRepo) {
		t.Fatalf("expected default registry fetch hint, got: %v", err)
	}
}

func TestRegistryLookup_ExplicitRegistrySuppressesFoundationalHint(t *testing.T) {
	t.Parallel()
	req := setupTestRegistry(t)
	_, err := RegistryLookup(req, "aipack-core")
	if err == nil {
		t.Fatal("expected error for missing pack")
	}
	if strings.Contains(err.Error(), "registry fetch") {
		t.Fatalf("explicit registry lookup should not suggest default fetch, got: %v", err)
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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
	err = RegistryFetch(context.Background(), RegistryFetchRequest{
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		URL:       "https://bitbucket.example.com/scm/TEAM/my-tools.git",
		Ref:       "team/ops-tools",
		Path:      "ops-tools/registry.yaml",
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
	if capturedRef != "team/ops-tools" {
		t.Errorf("ref = %q", capturedRef)
	}
	if capturedPath != "ops-tools/registry.yaml" {
		t.Errorf("path = %q", capturedPath)
	}

	// Verify source saved with git coordinates.
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if len(sc.RegistrySources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.RegistrySources))
	}
	src := sc.RegistrySources[0]
	if src.Ref != "team/ops-tools" {
		t.Errorf("source Ref = %q", src.Ref)
	}
	if src.Path != "ops-tools/registry.yaml" {
		t.Errorf("source Path = %q", src.Path)
	}
}

func TestRegistryFetch_TwoFilesFromSameRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	registryA := `schema_version: 1
packs:
  pack-a:
    repo: https://github.com/org/a
    description: Pack A
`
	registryB := `schema_version: 1
packs:
  pack-b:
    repo: https://github.com/org/b
    description: Pack B
`
	repoURL := "git@bitbucket.example.com:TEAM/my-tools.git"
	gitFetchFn := func(repo, ref, path string) ([]byte, error) {
		switch path {
		case "registry.yaml":
			return []byte(registryA), nil
		case "non-pack-registry.yaml":
			return []byte(registryB), nil
		default:
			return nil, fmt.Errorf("unexpected path: %s", path)
		}
	}

	// Fetch first file.
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir:  dir,
		URL:        repoURL,
		Path:       "registry.yaml",
		GitFetchFn: gitFetchFn,
	}, &buf)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Fetch second file from the same repo.
	buf.Reset()
	err = RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir:  dir,
		URL:        repoURL,
		Path:       "non-pack-registry.yaml",
		GitFetchFn: gitFetchFn,
	}, &buf)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	// Both sources should exist in sync-config.
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(sc.RegistrySources) != 2 {
		t.Fatalf("expected 2 sources, got %d: %+v", len(sc.RegistrySources), sc.RegistrySources)
	}

	// Source names should be distinct.
	if sc.RegistrySources[0].Name == sc.RegistrySources[1].Name {
		t.Errorf("sources have the same name: %q", sc.RegistrySources[0].Name)
	}

	// Both cache files should exist with distinct content.
	cacheA := config.SourceCachePath(dir, sc.RegistrySources[0].Name)
	regA, err := config.LoadRegistry(cacheA)
	if err != nil {
		t.Fatalf("loading first cache: %v", err)
	}
	cacheB := config.SourceCachePath(dir, sc.RegistrySources[1].Name)
	regB, err := config.LoadRegistry(cacheB)
	if err != nil {
		t.Fatalf("loading second cache: %v", err)
	}

	// Merged view should contain packs from both registries.
	if len(regA.Packs)+len(regB.Packs) != 2 {
		t.Errorf("expected 2 total packs across caches, got %d + %d", len(regA.Packs), len(regB.Packs))
	}
}

func TestRegistryFetch_GitAutoDetect(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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

	if capturedRef != "" {
		t.Errorf("ref = %q, want empty (default branch)", capturedRef)
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			data, ok := fetchCalls[url]
			if !ok {
				t.Errorf("unexpected fetch URL: %q", url)
			}
			return []byte(data), nil
		},
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			// Default registry is always appended; return valid data.
			return []byte(testRemoteRegistryYAML), nil
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

	// Default registry should also be cached.
	defaultName := config.DeriveSourceName(config.DefaultRegistryRepo, config.DefaultRegistryPath)
	regDefault, err := config.LoadRegistry(config.SourceCachePath(dir, defaultName))
	if err != nil {
		t.Fatalf("loading default cache: %v", err)
	}
	if len(regDefault.Packs) == 0 {
		t.Error("default registry cache is empty")
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			return nil, fmt.Errorf("network error")
		},
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			return nil, fmt.Errorf("network error")
		},
	}, &buf)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
	if !strings.Contains(err.Error(), "registry source(s) failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRegistryFetch_UsesDefaultGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRepo, capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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
	if capturedRef != "" {
		t.Errorf("ref = %q, want empty (default branch)", capturedRef)
	}
	if capturedPath != config.DefaultRegistryPath {
		t.Errorf("path = %q, want %q", capturedPath, config.DefaultRegistryPath)
	}
}

func TestRegistryFetch_UsesAdditionalDefaultBeforePublic(t *testing.T) {
	oldName := config.AdditionalDefaultRegistryName
	oldURL := config.AdditionalDefaultRegistryURL
	config.AdditionalDefaultRegistryName = "team-packs"
	config.AdditionalDefaultRegistryURL = "https://example.com/team/registry.yaml"
	t.Cleanup(func() {
		config.AdditionalDefaultRegistryName = oldName
		config.AdditionalDefaultRegistryURL = oldURL
	})

	dir := t.TempDir()
	var fetchOrder []string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			fetchOrder = append(fetchOrder, url)
			if url != "https://example.com/team/registry.yaml" {
				t.Fatalf("unexpected HTTP fetch URL: %q", url)
			}
			return []byte(testRemoteRegistryYAML), nil
		},
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			fetchOrder = append(fetchOrder, repo)
			if repo != config.DefaultRegistryRepo {
				t.Fatalf("unexpected git fetch repo: %q", repo)
			}
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantOrder := []string{"https://example.com/team/registry.yaml", config.DefaultRegistryRepo}
	if len(fetchOrder) != len(wantOrder) {
		t.Fatalf("fetch order = %v, want %v", fetchOrder, wantOrder)
	}
	for i := range wantOrder {
		if fetchOrder[i] != wantOrder[i] {
			t.Fatalf("fetch order = %v, want %v", fetchOrder, wantOrder)
		}
	}

	sc, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(sc.RegistrySources) != 2 {
		t.Fatalf("expected 2 registry sources, got %d", len(sc.RegistrySources))
	}
	if sc.RegistrySources[0].Name != "team-packs" {
		t.Fatalf("first source name = %q, want team-packs", sc.RegistrySources[0].Name)
	}
	if sc.RegistrySources[1].URL != config.DefaultRegistryRepo {
		t.Fatalf("second source URL = %q, want %q", sc.RegistrySources[1].URL, config.DefaultRegistryRepo)
	}
}

func TestRegistryFetch_AdditionalDefaultGitIsIdempotent(t *testing.T) {
	oldName := config.AdditionalDefaultRegistryName
	oldURL := config.AdditionalDefaultRegistryURL
	config.AdditionalDefaultRegistryName = "team-packs"
	config.AdditionalDefaultRegistryURL = "https://example.com/team/packs.git"
	t.Cleanup(func() {
		config.AdditionalDefaultRegistryName = oldName
		config.AdditionalDefaultRegistryURL = oldURL
	})

	dir := t.TempDir()
	fetch := func(orders *[][]string) func(repo, ref, path string) ([]byte, error) {
		return func(repo, ref, path string) ([]byte, error) {
			*orders = append(*orders, []string{repo, ref, path})
			if path != config.DefaultRegistryPath {
				t.Fatalf("git fetch path = %q, want %q", path, config.DefaultRegistryPath)
			}
			return []byte(testRemoteRegistryYAML), nil
		}
	}

	var firstOrder [][]string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir:  dir,
		GitFetchFn: fetch(&firstOrder),
	}, &buf)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if len(firstOrder) != 2 {
		t.Fatalf("first fetch order = %v, want 2 default sources", firstOrder)
	}

	var secondOrder [][]string
	buf.Reset()
	err = RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir:  dir,
		GitFetchFn: fetch(&secondOrder),
	}, &buf)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(secondOrder) != 2 {
		t.Fatalf("second fetch order = %v, want same 2 default sources", secondOrder)
	}

	got, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(got.RegistrySources) != 2 {
		t.Fatalf("registry sources = %+v, want 2 sources", got.RegistrySources)
	}
	if got.RegistrySources[0].Name != "team-packs" {
		t.Fatalf("additional default name = %q, want team-packs", got.RegistrySources[0].Name)
	}
	if got.RegistrySources[0].Path != config.DefaultRegistryPath {
		t.Fatalf("additional default path = %q, want %q", got.RegistrySources[0].Path, config.DefaultRegistryPath)
	}
}

func TestRegistryFetch_ReplacesStaleAdditionalDefaultSourceByName(t *testing.T) {
	oldName := config.AdditionalDefaultRegistryName
	oldURL := config.AdditionalDefaultRegistryURL
	config.AdditionalDefaultRegistryName = "team-packs"
	config.AdditionalDefaultRegistryURL = "https://registry.example.com/team/latest/registry.yaml"
	t.Cleanup(func() {
		config.AdditionalDefaultRegistryName = oldName
		config.AdditionalDefaultRegistryURL = oldURL
	})

	dir := t.TempDir()
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{
			Name: config.DeriveSourceName(config.DefaultRegistryRepo, config.DefaultRegistryPath),
			URL:  config.DefaultRegistryRepo,
			Path: config.DefaultRegistryPath,
		},
		{
			Name: "team-packs",
			URL:  "ssh://git@example.com/old/team-packs.git",
			Ref:  "main",
			Path: config.DefaultRegistryPath,
		},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatalf("saving sync-config: %v", err)
	}

	var fetchOrder []string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			fetchOrder = append(fetchOrder, url)
			return []byte(testRemoteRegistryYAML), nil
		},
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			fetchOrder = append(fetchOrder, repo)
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("registry fetch: %v", err)
	}

	if len(fetchOrder) < 2 || fetchOrder[0] != config.AdditionalDefaultRegistryURL {
		t.Fatalf("fetch order = %v, want additional default first", fetchOrder)
	}
	for _, gotURL := range fetchOrder {
		if strings.Contains(gotURL, "old/team-packs") {
			t.Fatalf("stale source should not be fetched after name replacement: %v", fetchOrder)
		}
	}

	got, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if got.RegistrySources[0].Name != "team-packs" || got.RegistrySources[0].URL != config.AdditionalDefaultRegistryURL {
		t.Fatalf("first source = %+v, want team-packs at additional default URL", got.RegistrySources[0])
	}
	if len(got.RegistrySources) != 2 {
		t.Fatalf("registry sources = %+v, want replacement plus public default", got.RegistrySources)
	}
}

func TestRegistryFetch_PublicDefaultDoesNotReplaceSameNameCustomSource(t *testing.T) {
	oldName := config.AdditionalDefaultRegistryName
	oldURL := config.AdditionalDefaultRegistryURL
	config.AdditionalDefaultRegistryName = ""
	config.AdditionalDefaultRegistryURL = ""
	t.Cleanup(func() {
		config.AdditionalDefaultRegistryName = oldName
		config.AdditionalDefaultRegistryURL = oldURL
	})

	dir := t.TempDir()
	defaultName := config.DeriveSourceName(config.DefaultRegistryRepo, config.DefaultRegistryPath)
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{{
		Name: defaultName,
		URL:  "https://example.com/custom/registry.yaml",
	}}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatalf("saving sync-config: %v", err)
	}

	var fetchOrder []string
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn: func(url string) ([]byte, error) {
			fetchOrder = append(fetchOrder, url)
			return []byte(testRemoteRegistryYAML), nil
		},
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			fetchOrder = append(fetchOrder, repo)
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("registry fetch: %v", err)
	}

	wantOrder := []string{"https://example.com/custom/registry.yaml", config.DefaultRegistryRepo}
	if len(fetchOrder) != len(wantOrder) {
		t.Fatalf("fetch order = %v, want %v", fetchOrder, wantOrder)
	}
	for i := range wantOrder {
		if fetchOrder[i] != wantOrder[i] {
			t.Fatalf("fetch order = %v, want %v", fetchOrder, wantOrder)
		}
	}

	got, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(got.RegistrySources) != 2 {
		t.Fatalf("registry sources = %+v, want custom plus public default", got.RegistrySources)
	}
	if got.RegistrySources[0].URL != "https://example.com/custom/registry.yaml" {
		t.Fatalf("first source = %+v, want custom source preserved", got.RegistrySources[0])
	}
	if got.RegistrySources[1].URL != config.DefaultRegistryRepo {
		t.Fatalf("second source = %+v, want public default appended", got.RegistrySources[1])
	}
}

func TestRegistryFetch_InsertsAdditionalDefaultBeforeExistingPublic(t *testing.T) {
	oldName := config.AdditionalDefaultRegistryName
	oldURL := config.AdditionalDefaultRegistryURL
	config.AdditionalDefaultRegistryName = "team-packs"
	config.AdditionalDefaultRegistryURL = "https://example.com/team/registry.yaml"
	t.Cleanup(func() {
		config.AdditionalDefaultRegistryName = oldName
		config.AdditionalDefaultRegistryURL = oldURL
	})

	dir := t.TempDir()
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []config.RegistrySourceEntry{{
		Name: config.DeriveSourceName(config.DefaultRegistryRepo, config.DefaultRegistryPath),
		URL:  config.DefaultRegistryRepo,
		Path: config.DefaultRegistryPath,
	}}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatalf("saving sync-config: %v", err)
	}

	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
		ConfigDir: dir,
		FetchFn:   fakeFetchFn(testRemoteRegistryYAML),
		GitFetchFn: func(repo, ref, path string) ([]byte, error) {
			return []byte(testRemoteRegistryYAML), nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := config.LoadSyncConfig(config.SyncConfigPath(dir))
	if err != nil {
		t.Fatalf("loading sync-config: %v", err)
	}
	if len(got.RegistrySources) != 2 {
		t.Fatalf("expected 2 registry sources, got %d", len(got.RegistrySources))
	}
	if got.RegistrySources[0].Name != "team-packs" {
		t.Fatalf("first source name = %q, want team-packs", got.RegistrySources[0].Name)
	}
	if got.RegistrySources[1].URL != config.DefaultRegistryRepo {
		t.Fatalf("second source URL = %q, want %q", got.RegistrySources[1].URL, config.DefaultRegistryRepo)
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
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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

func TestRegistryDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up a source via fetch.
	var buf bytes.Buffer
	err := RegistryFetch(context.Background(), RegistryFetchRequest{
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
	err = RegistryDelete(RegistryDeleteRequest{
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

func TestRegistryDelete_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := RegistryDelete(RegistryDeleteRequest{
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
	if src.Ref != "" {
		t.Errorf("source Ref = %q, want empty (default branch)", src.Ref)
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

func TestIndexRegistryEntriesMarksInstalledFromLockfile(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	packRoot := filepath.Join(PacksDir(configDir), "installed-pack")
	if err := writeTestFile(filepath.Join(packRoot, "pack.json"), `{"schema_version":2,"name":"installed-pack","root":"."}`); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{
		Packs: map[string]config.InstalledPackMeta{
			"installed-pack": {Method: config.MethodLink, Origin: packRoot},
		},
	}); err != nil {
		t.Fatal(err)
	}

	reg := config.Registry{
		SchemaVersion: config.RegistrySchemaVersion,
		Packs: map[string]config.RegistryEntry{
			"installed-pack": {Repo: "https://example.com/installed.git"},
		},
	}
	if err := indexRegistryEntries(reg, configDir); err != nil {
		t.Fatal(err)
	}

	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var installed int
	if err := db.QueryRow("SELECT installed FROM packs WHERE name='installed-pack'").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if installed != 1 {
		t.Fatalf("installed = %d, want 1", installed)
	}
}

func TestRegistryDeepIndexSkipsInstalledFromLockfile(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	packRoot := filepath.Join(PacksDir(configDir), "installed-pack")
	if err := writeTestFile(filepath.Join(packRoot, "pack.json"), `{"schema_version":2,"name":"installed-pack","root":"."}`); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{
		Packs: map[string]config.InstalledPackMeta{
			"installed-pack": {Method: config.MethodLink, Origin: packRoot},
		},
	}); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(configDir, "registry.yaml")
	if err := os.WriteFile(registryPath, []byte(`schema_version: 1
packs:
  installed-pack:
    repo: https://example.com/installed.git
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RegistryDeepIndex(context.Background(), RegistryDeepIndexRequest{
		ConfigDir:    configDir,
		RegistryPath: registryPath,
		GitCloneFn: func(repoURL, dir, ref string) error {
			t.Fatalf("deep-index should not clone installed pack %s", repoURL)
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeepIndexOnePack_RespectsPackJsonRoot(t *testing.T) {
	t.Parallel()

	cloneFn := func(repo, dir, ref string) error {
		packJSON := `{"schema_version": 2, "name": "test-pack", "version": "1.0", "root": "content", "rules": ["safety"]}`
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

	resources, err := deepIndexOnePack(context.Background(), t.TempDir(), "test-pack", entry, cloneFn)
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

func TestDeepIndexOnePack_IndexesPromptsPluginsAndMCPServers(t *testing.T) {
	t.Parallel()

	cloneFn := func(repo, dir, ref string) error {
		writePackJSON := func(p, body string) {
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
		}
		mkdir := func(p string) {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", p, err)
			}
		}

		mkdir(dir)
		writePackJSON(filepath.Join(dir, "pack.json"),
			`{"schema_version": 2, "name": "deep-pack", "version": "1.0", "root": "."}`)

		mkdir(filepath.Join(dir, "prompts"))
		writePackJSON(filepath.Join(dir, "prompts", "smoke.md"),
			"---\ndescription: Quick smoke check\n---\nSmoke prompt body.\n")

		mkdir(filepath.Join(dir, "plugins"))
		writePackJSON(filepath.Join(dir, "plugins", "linear.json"),
			`{"source": "github:linear/linear-codex-plugin", "marketplace": "openai-curated"}`)

		mkdir(filepath.Join(dir, "mcp"))
		writePackJSON(filepath.Join(dir, "mcp", "example-server.json"),
			`{"name": "example-server", "transport": "stdio", "command": ["npx", "example-server-mcp"], "available_tools": ["search_issues", "get_issue"], "auth": "PAT", "notes": "Example MCP server"}`)

		return nil
	}

	entry := config.RegistryEntry{
		Repo: "https://example.com/deep.git",
		Ref:  "main",
	}

	resources, err := deepIndexOnePack(context.Background(), t.TempDir(), "deep-pack", entry, cloneFn)
	if err != nil {
		t.Fatal(err)
	}

	byKindName := map[string]index.Resource{}
	for _, r := range resources {
		byKindName[r.Kind+"/"+r.Name] = r
	}
	if r, ok := byKindName["prompt/smoke"]; !ok || !strings.Contains(r.Description, "Quick smoke check") {
		t.Fatalf("expected prompt 'smoke' with description, got %+v", byKindName)
	}
	if r, ok := byKindName["plugin/linear"]; !ok || !strings.Contains(r.Body, "openai-curated") {
		t.Fatalf("expected plugin 'linear' with marketplace in body, got %+v", byKindName)
	}
	if r, ok := byKindName["mcp/example-server"]; !ok {
		t.Fatalf("expected mcp 'example-server', got %+v", byKindName)
	} else {
		if !strings.Contains(r.Body, "search_issues") {
			t.Fatalf("mcp body should include tools: %s", r.Body)
		}
		if !strings.Contains(r.Body, "transport: stdio") {
			t.Fatalf("mcp body should include transport: %s", r.Body)
		}
	}
}

func TestDeepIndexOnePack_IndexesNestedResources(t *testing.T) {
	t.Parallel()

	cloneFn := func(repo, dir, ref string) error {
		writeFile(t, filepath.Join(dir, "pack.json"),
			`{"schema_version": 2, "name": "nested-pack", "version": "1.0", "root": "."}`)
		writeFile(t, filepath.Join(dir, "rules", "team", "safety.md"),
			"---\ndescription: Nested safety rule\n---\nNested rule body.\n")
		return nil
	}

	resources, err := deepIndexOnePack(context.Background(), t.TempDir(), "nested-pack", config.RegistryEntry{
		Repo: "https://example.com/nested.git",
		Ref:  "main",
	}, cloneFn)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resources {
		if r.Kind == "rule" && r.Name == "team/safety" {
			return
		}
	}
	t.Fatalf("expected nested rule team/safety, got %+v", resources)
}

func TestRegistryDeepIndex_IndexesArchiveEntries(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	archive := buildPackZip(t, map[string]string{
		"repo-main/pack.json":      `{"schema_version":2,"name":"archive-index","version":"1.0.0","root":"."}`,
		"repo-main/prompts/run.md": "Archive prompt body.\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
		"archive-index": {
			Method: config.MethodArchive,
			URL:    srv.URL + "/pack.zip",
		},
	})

	if err := RegistryDeepIndex(context.Background(), RegistryDeepIndexRequest{ConfigDir: configDir}, nil); err != nil {
		t.Fatalf("RegistryDeepIndex: %v", err)
	}
	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("Archive", index.SearchFilters{Status: "registered", Kind: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "archive-index" || results[0].Kind != "prompt" {
		t.Fatalf("archive deep-index result = %+v", results)
	}
}

func TestRegistryDeepIndex_ArchiveContentPathsWithoutPackJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	archive := buildPackZip(t, map[string]string{
		"repo-main/material/prompts/run.md": "Archive content_paths prompt body.\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
		"archive-catalog": {
			Method: config.MethodArchive,
			URL:    srv.URL + "/pack.zip",
			ContentPaths: map[domain.PackCategory]string{
				domain.CategoryPrompts: "material/prompts",
			},
		},
	})

	if err := RegistryDeepIndex(context.Background(), RegistryDeepIndexRequest{ConfigDir: configDir}, nil); err != nil {
		t.Fatalf("RegistryDeepIndex: %v", err)
	}
	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	results, err := db.Search("content_paths", index.SearchFilters{Status: "registered", Kind: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "archive-catalog" || results[0].Kind != "prompt" {
		t.Fatalf("archive content_paths deep-index result = %+v", results)
	}
}
