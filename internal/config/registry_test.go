package config

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTarBytes creates a tar archive in memory and returns the raw bytes.
func buildTarBytes(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	buf := buildTar(t, entries)
	return buf.Bytes()
}

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
	if len(reg.Packs) != 0 {
		t.Errorf("expected 0 packs, got %d", len(reg.Packs))
	}
}

func TestResolveRegistryPath_FlagOverride(t *testing.T) {
	t.Parallel()
	got := ResolveRegistryPath("/explicit/path.yaml", "", "/config")
	if got != "/explicit/path.yaml" {
		t.Errorf("got %q, want /explicit/path.yaml", got)
	}
}

func TestResolveRegistryPath_SyncConfigDefault(t *testing.T) {
	t.Parallel()
	got := ResolveRegistryPath("", "/from/sync-config.yaml", "/config")
	if got != "/from/sync-config.yaml" {
		t.Errorf("got %q, want /from/sync-config.yaml", got)
	}
}

func TestResolveRegistryPath_DefaultFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := ResolveRegistryPath("", "", dir)
	want := filepath.Join(dir, "registry.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- DeriveSourceName tests ---

func TestDeriveSourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/shrug-labs/aipack.git", "aipack"},
		{"https://bitbucket.example.com/scm/TEAM/my-tools.git", "my-tools"},
		{"https://example.com/my-team/registry.yaml", "my-team"},
		{"https://example.com/registry.yaml", "example"},
		{"https://example.com/tools.git/", "tools"},
		{"https://registry.example.com/registry.yaml", "example"}, // hostname starting with "registry" skips that label
		{"git@bitbucket.example.com:TEAM/my-tools.git", "my-tools"},
		{"git@github.com:org/repo.git", "repo"},
		{"ssh://git@github.com/org/repo.git", "repo"},
	}
	for _, tt := range tests {
		if got := DeriveSourceName(tt.url); got != tt.want {
			t.Errorf("DeriveSourceName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestUniqueSourceName_NoCollision(t *testing.T) {
	t.Parallel()
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", nil)
	if got != "aipack" {
		t.Errorf("got %q, want aipack", got)
	}
}

func TestUniqueSourceName_SameURL(t *testing.T) {
	t.Parallel()
	existing := []RegistrySourceEntry{
		{Name: "aipack", URL: "https://github.com/org/aipack.git"},
	}
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", existing)
	if got != "aipack" {
		t.Errorf("got %q, want aipack (same URL should reuse name)", got)
	}
}

func TestUniqueSourceName_Collision(t *testing.T) {
	t.Parallel()
	existing := []RegistrySourceEntry{
		{Name: "aipack", URL: "https://other.com/aipack.git"},
	}
	got := UniqueSourceName("aipack", "https://github.com/org/aipack.git", existing)
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

func TestLoadMergedRegistry_LocalOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	localYAML := `schema_version: 1
packs:
  local-pack:
    repo: https://github.com/org/local
`
	os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte(localYAML), 0o600)

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(reg.Packs))
	}
	if _, ok := reg.Packs["local-pack"]; !ok {
		t.Error("missing local-pack")
	}
}

func TestLoadMergedRegistry_LocalPlusCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	localYAML := `schema_version: 1
packs:
  local-pack:
    repo: https://github.com/org/local
`
	os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte(localYAML), 0o600)

	cachedYAML := `schema_version: 1
packs:
  remote-pack:
    repo: https://github.com/org/remote
`
	os.MkdirAll(RegistriesCacheDir(dir), 0o700)
	os.WriteFile(SourceCachePath(dir, "my-source"), []byte(cachedYAML), 0o600)

	sc := SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []RegistrySourceEntry{
		{Name: "my-source", URL: "https://example.com"},
	}
	SaveSyncConfig(SyncConfigPath(dir), sc)

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(reg.Packs))
	}
}

func TestLoadMergedRegistry_LocalWinsConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	localYAML := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/local
    description: local wins
`
	os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte(localYAML), 0o600)

	cachedYAML := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/remote
    description: remote loses
`
	os.MkdirAll(RegistriesCacheDir(dir), 0o700)
	os.WriteFile(SourceCachePath(dir, "remote"), []byte(cachedYAML), 0o600)

	sc := SyncConfig{SchemaVersion: 1}
	sc.RegistrySources = []RegistrySourceEntry{
		{Name: "remote", URL: "https://example.com"},
	}
	SaveSyncConfig(SyncConfigPath(dir), sc)

	reg, err := LoadMergedRegistry(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Packs["my-pack"].Description != "local wins" {
		t.Errorf("expected local to win conflict, got %q", reg.Packs["my-pack"].Description)
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
`
	cacheB := `schema_version: 1
packs:
  my-pack:
    repo: https://github.com/org/b
    description: source B loses
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
}

// --- FetchFileViaGit tests ---

func TestFetchFileViaGitWith_ArchiveSuccess(t *testing.T) {
	t.Parallel()
	wantContent := "registry: data"
	archiveMock := func(repoURL, ref string, paths []string) ([]byte, error) {
		// Build a tar containing the requested file.
		return buildTarBytes(t, []tarEntry{
			{Name: paths[0], Type: tar.TypeReg, Body: wantContent},
		}), nil
	}
	cloneMock := func(args ...string) error {
		t.Fatal("clone should not be called when archive succeeds")
		return nil
	}

	data, err := FetchFileViaGitWith("git@example.com:org/repo.git", "main", "registry.yaml", cloneMock, archiveMock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("got %q, want %q", string(data), wantContent)
	}
}

func TestFetchFileViaGitWith_ArchiveNotSupported_FallsBackToClone(t *testing.T) {
	t.Parallel()
	wantContent := "cloned-data"
	archiveMock := func(repoURL, ref string, paths []string) ([]byte, error) {
		return nil, ErrArchiveNotSupported
	}
	var cloneDir string
	cloneMock := func(args ...string) error {
		// Capture the clone destination and write the expected file.
		for i, a := range args {
			if a == "clone" && i == 0 {
				cloneDir = args[len(args)-1]
			}
		}
		if cloneDir == "" {
			return nil
		}
		return os.WriteFile(filepath.Join(cloneDir, "registry.yaml"), []byte(wantContent), 0o644)
	}

	data, err := FetchFileViaGitWith("https://github.com/org/repo.git", "", "registry.yaml", cloneMock, archiveMock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("got %q, want %q", string(data), wantContent)
	}
}

func TestFetchFileViaGitWith_ArchiveGenericError_DoesNotFallback(t *testing.T) {
	t.Parallel()
	archiveMock := func(repoURL, ref string, paths []string) ([]byte, error) {
		return nil, fmt.Errorf("permission denied")
	}
	cloneMock := func(args ...string) error {
		t.Fatal("clone should not be called on non-archive-not-supported errors")
		return nil
	}

	_, err := FetchFileViaGitWith("git@example.com:org/repo.git", "main", "registry.yaml", cloneMock, archiveMock)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected 'permission denied' in error, got: %v", err)
	}
}

func TestFetchFileViaGitWith_NilArchive_UsesClone(t *testing.T) {
	t.Parallel()
	wantContent := "clone-only"
	var cloneDir string
	cloneMock := func(args ...string) error {
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

	data, err := FetchFileViaGitWith("https://example.com/repo.git", "v1.0", "file.txt", cloneMock, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != wantContent {
		t.Errorf("got %q, want %q", string(data), wantContent)
	}
}
