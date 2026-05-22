package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestLoadRegistry_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/repo
    path: subdir
    description: A test pack
    ref: v1.0.0
    owner: Team A
    contact: "#team-a"
  root-pack:
    repo: https://github.com/org/other
    description: Pack at repo root
collections:
  starter:
    description: Starter collection
    packs:
      - root-pack
      - name: my-pack
        ref: v1.1.0
        with: all
`), 0o600)

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(reg.Packs))
	}
	entry := reg.Packs["my-pack"]
	if entry.Repo != "https://github.com/org/repo" {
		t.Errorf("Repo = %q, want %q", entry.Repo, "https://github.com/org/repo")
	}
	if entry.Path != "subdir" {
		t.Errorf("Path = %q, want %q", entry.Path, "subdir")
	}
	if entry.Ref != "v1.0.0" {
		t.Errorf("Ref = %q, want %q", entry.Ref, "v1.0.0")
	}
	if entry.Owner != "Team A" {
		t.Errorf("Owner = %q, want %q", entry.Owner, "Team A")
	}

	root := reg.Packs["root-pack"]
	if root.Path != "" {
		t.Errorf("root-pack Path = %q, want empty", root.Path)
	}

	collection := reg.Collections["starter"]
	if collection.Description != "Starter collection" {
		t.Errorf("collection Description = %q", collection.Description)
	}
	if len(collection.Packs) != 2 {
		t.Fatalf("collection packs = %d, want 2", len(collection.Packs))
	}
	if collection.Packs[0].Name != "root-pack" {
		t.Errorf("first collection pack = %q, want root-pack", collection.Packs[0].Name)
	}
	if collection.Packs[1].Name != "my-pack" || collection.Packs[1].Ref != "v1.1.0" {
		t.Errorf("second collection pack = %+v, want my-pack ref v1.1.0", collection.Packs[1])
	}
	if got := []string(collection.Packs[1].With); len(got) != 1 || got[0] != "all" {
		t.Errorf("second collection pack With = %v, want [all]", got)
	}
}

func TestLoadRegistry_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadRegistry("/nonexistent/registry.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadRegistry_BadSchemaVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 99
packs: {}
`), 0o600)

	_, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("expected error for bad schema version")
	}
}

func TestLoadRegistry_EmptyPacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	os.WriteFile(path, []byte(`
schema_version: 1
`), 0o600)

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Packs == nil {
		t.Fatal("Packs map should be initialized, not nil")
	}
	if reg.Collections == nil {
		t.Fatal("Collections map should be initialized, not nil")
	}
	if len(reg.Packs) != 0 {
		t.Errorf("expected 0 packs, got %d", len(reg.Packs))
	}
}

func TestDefaultRegistrySources_PublicOnly(t *testing.T) {
	t.Parallel()

	sources := DefaultRegistrySources()
	if len(sources) != 1 {
		t.Fatalf("expected 1 default source, got %d", len(sources))
	}
	got := sources[0]
	if got.URL != DefaultRegistryRepo {
		t.Fatalf("default URL = %q, want %q", got.URL, DefaultRegistryRepo)
	}
	if got.Path != DefaultRegistryPath {
		t.Fatalf("default path = %q, want %q", got.Path, DefaultRegistryPath)
	}
}

func TestDefaultRegistrySources_AdditionalBeforePublic(t *testing.T) {
	oldName := AdditionalDefaultRegistryName
	oldURL := AdditionalDefaultRegistryURL
	AdditionalDefaultRegistryName = "team-packs"
	AdditionalDefaultRegistryURL = "https://example.com/team/registry.yaml"
	t.Cleanup(func() {
		AdditionalDefaultRegistryName = oldName
		AdditionalDefaultRegistryURL = oldURL
	})

	sources := DefaultRegistrySources()
	if len(sources) != 2 {
		t.Fatalf("expected 2 default sources, got %d", len(sources))
	}
	if sources[0].Name != "team-packs" {
		t.Fatalf("first source name = %q, want team-packs", sources[0].Name)
	}
	if sources[0].URL != "https://example.com/team/registry.yaml" {
		t.Fatalf("first source URL = %q", sources[0].URL)
	}
	if sources[1].URL != DefaultRegistryRepo {
		t.Fatalf("second source URL = %q, want %q", sources[1].URL, DefaultRegistryRepo)
	}
}

// --- DeriveSourceName tests ---

func TestDeriveSourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		path string
		want string
	}{
		// Default or empty path — derive from URL.
		{"https://github.com/shrug-labs/packs.git", "", "packs"},
		{"https://github.com/shrug-labs/packs.git", "registry.yaml", "packs"},
		{"https://bitbucket.example.com/scm/TEAM/my-tools.git", "", "my-tools"},
		{"https://example.com/my-team/registry.yaml", "", "my-team"},
		{"https://example.com/registry.yaml", "", "example"},
		{"https://example.com/tools.git/", "", "tools"},
		{"https://registry.example.com/registry.yaml", "", "example"}, // hostname starting with "registry" skips that label
		{"git@bitbucket.example.com:TEAM/my-tools.git", "", "my-tools"},
		{"git@github.com:org/repo.git", "", "repo"},
		{"ssh://git@github.com/org/repo.git", "", "repo"},
		// Non-default path — derive from path stem.
		{"git@github.com:org/repo.git", "non-pack-registry.yaml", "non-pack-registry"},
		{"git@github.com:org/repo.git", "tools/catalog.yaml", "catalog"},
		{"git@github.com:org/repo.git", "ops-tools/custom.yml", "custom"},
		// Path stem resolves to "registry" — falls through to URL derivation.
		{"git@github.com:org/repo.git", "registry.yml", "repo"},
	}
	for _, tt := range tests {
		if got := DeriveSourceName(tt.url, tt.path); got != tt.want {
			t.Errorf("DeriveSourceName(%q, %q) = %q, want %q", tt.url, tt.path, got, tt.want)
		}
	}
}

func TestUniqueSourceName_NoCollision(t *testing.T) {
	t.Parallel()
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", "", nil)
	if got != "aipack" {
		t.Errorf("got %q, want aipack", got)
	}
}

func TestUniqueSourceName_SameURLAndPath(t *testing.T) {
	t.Parallel()
	existing := []RegistrySourceEntry{
		{Name: "aipack", URL: "https://github.com/org/aipack.git", Path: "registry.yaml"},
	}
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", "registry.yaml", existing)
	if got != "aipack" {
		t.Errorf("got %q, want aipack (same URL+path should reuse name)", got)
	}
}

func TestUniqueSourceName_SameURLDifferentPath(t *testing.T) {
	t.Parallel()
	existing := []RegistrySourceEntry{
		{Name: "aipack", URL: "https://github.com/org/aipack.git", Path: "registry.yaml"},
	}
	got := UniqueSourceName("other-reg", "https://github.com/org/aipack.git", "other-reg.yaml", existing)
	if got != "other-reg" {
		t.Errorf("got %q, want other-reg (same URL but different path is a new source)", got)
	}
}

func TestUniqueSourceName_Collision(t *testing.T) {
	t.Parallel()
	existing := []RegistrySourceEntry{
		{Name: "aipack", URL: "https://other.com/aipack.git"},
	}
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", "", existing)
	if got != "aipack-2" {
		t.Errorf("got %q, want aipack-2", got)
	}
}

func TestIsGitURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		ref  string
		want bool
	}{
		{"https://github.com/org/repo.git", "", true},
		{"https://example.com/registry.yaml", "", false},
		{"https://example.com/registry.yaml", "main", true},
		{"https://example.com/repo", "v1.0", true},
		{"git@bitbucket.example.com:TEAM/tools.git", "", true},
		{"git@github.com:org/repo.git", "", true},
		{"ssh://git@bitbucket.example.com/TEAM/tools.git", "", true},
		{"ssh://git@github.com/org/repo", "", true},
	}
	for _, tt := range tests {
		if got := IsGitURL(tt.url, tt.ref); got != tt.want {
			t.Errorf("IsGitURL(%q, %q) = %v, want %v", tt.url, tt.ref, got, tt.want)
		}
	}
}

// --- LoadMergedRegistry tests ---

func TestLoadMergedRegistry_NoSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 0 {
		t.Fatalf("expected 0 packs, got %d", len(reg.Packs))
	}
}

func TestLoadMergedRegistry_MultipleSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	source1YAML := `schema_version: 1
packs:
  pack-a:
    repo: https://github.com/org/a
collections:
  starter:
    packs: [pack-a]
`
	source2YAML := `schema_version: 1
packs:
  pack-b:
    repo: https://github.com/org/b
collections:
  ops:
    packs: [pack-b]
`
	os.MkdirAll(RegistriesCacheDir(dir), 0o700)
	os.WriteFile(SourceCachePath(dir, "source-1"), []byte(source1YAML), 0o600)
	os.WriteFile(SourceCachePath(dir, "source-2"), []byte(source2YAML), 0o600)

	sc := SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []RegistrySourceEntry{
		{Name: "source-1", URL: "https://example.com/s1"},
		{Name: "source-2", URL: "https://example.com/s2"},
	}
	SaveSyncConfig(SyncConfigPath(dir), sc)

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(reg.Packs))
	}
	if len(reg.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(reg.Collections))
	}
}

func TestLoadMergedRegistry_SourceOrderRespected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// No local registry — test source priority.
	cacheA := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/a
    description: source A wins
collections:
  team:
    description: collection A wins
    packs: [my-pack]
`
	cacheB := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/b
    description: source B loses
collections:
  team:
    description: collection B loses
    packs: [my-pack]
`
	os.MkdirAll(RegistriesCacheDir(dir), 0o700)
	os.WriteFile(SourceCachePath(dir, "source-a"), []byte(cacheA), 0o600)
	os.WriteFile(SourceCachePath(dir, "source-b"), []byte(cacheB), 0o600)

	sc := SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []RegistrySourceEntry{
		{Name: "source-a", URL: "https://a.example.com"},
		{Name: "source-b", URL: "https://b.example.com"},
	}
	SaveSyncConfig(SyncConfigPath(dir), sc)

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Packs["my-pack"].Description != "source A wins" {
		t.Errorf("expected source A to win, got %q", reg.Packs["my-pack"].Description)
	}
	if reg.Collections["team"].Description != "collection A wins" {
		t.Errorf("expected source A collection to win, got %q", reg.Collections["team"].Description)
	}
}

func TestParseRegistryCollectionWith(t *testing.T) {
	t.Parallel()

	all, err := ParseRegistryCollectionWith([]string{"all"})
	if err != nil {
		t.Fatalf("ParseRegistryCollectionWith(all): %v", err)
	}
	for _, cat := range domain.AllBundledCategories {
		if !all.Has(cat) {
			t.Fatalf("all missing category %s", cat)
		}
	}

	partial, err := ParseRegistryCollectionWith([]string{"profiles", "extras"})
	if err != nil {
		t.Fatalf("ParseRegistryCollectionWith(partial): %v", err)
	}
	if !partial.Has(domain.BundledProfiles) || !partial.Has(domain.BundledExtras) {
		t.Fatalf("partial set missing profiles/extras: %v", partial)
	}
	if partial.Has(domain.BundledRegistries) {
		t.Fatalf("partial set unexpectedly includes registries: %v", partial)
	}

	if _, err := ParseRegistryCollectionWith([]string{"bad"}); err == nil {
		t.Fatal("expected invalid with value to fail")
	}
}

func TestRegistryEntry_QuietAndContentPaths(t *testing.T) {
	t.Parallel()
	input := `schema_version: 1
packs:
  ext-skills:
    repo: ssh://git@example.com:7999/team/repo.git
    quiet: true
    content_paths:
      skills: nested/custom-skills
  normal-pack:
    repo: https://github.com/org/pack.git
`
	reg, err := ParseRegistry([]byte(input))
	if err != nil {
		t.Fatalf("ParseRegistry: %v", err)
	}
	ext := reg.Packs["ext-skills"]
	if !ext.Quiet {
		t.Fatal("expected Quiet=true")
	}
	if ext.ContentPaths[domain.CategorySkills] != "nested/custom-skills" {
		t.Fatalf("expected content_paths.skills = nested/custom-skills, got %q", ext.ContentPaths[domain.CategorySkills])
	}
	normal := reg.Packs["normal-pack"]
	if normal.Quiet {
		t.Fatal("expected Quiet=false for normal pack")
	}
	if len(normal.ContentPaths) != 0 {
		t.Fatalf("expected no content_paths for normal pack, got %v", normal.ContentPaths)
	}
}

// --- FetchFileViaGit tests ---

func TestFetchFileViaGitWith_ClonesSuccessfully(t *testing.T) {
	t.Parallel()
	wantContent := "clone-only"
	var cloneDir string
	cloneMock := func(_ context.Context, args ...string) error {
		for i, a := range args {
			if a == "clone" && i == 0 {
				cloneDir = args[len(args)-1]
			}
		}
		if cloneDir == "" {
			return nil
		}
		return os.WriteFile(filepath.Join(cloneDir, "file.txt"), []byte(wantContent), 0o644)
	}

	data, err := FetchFileViaGitWith(context.Background(), "https://example.com/repo.git", "v1.0", "file.txt", cloneMock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("got %q, want %q", string(data), wantContent)
	}
}
