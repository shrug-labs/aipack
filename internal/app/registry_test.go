package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"gopkg.in/yaml.v3"
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

func TestRegistryFetch_NewRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.yaml")

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir:    dir,
		RegistryPath: regPath,
		URL:          "https://example.com/registry.yaml",
		FetchFn:      fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the file was created with the right entries.
	reg, err := config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("loading created registry: %v", err)
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
	if !strings.Contains(output, "2 new pack(s)") {
		t.Errorf("expected '2 new pack(s)' in output, got: %s", output)
	}
	if !strings.Contains(output, "2 total") {
		t.Errorf("expected '2 total' in output, got: %s", output)
	}
}

func TestRegistryFetch_MergesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.yaml")

	// Write an existing registry with one overlapping and one unique entry.
	existing := config.Registry{
		SchemaVersion: 1,
		Packs: map[string]config.RegistryEntry{
			"delta-pack": {
				Repo:        "https://github.com/org/delta-OLD",
				Description: "Old delta",
			},
			"zeta-pack": {
				Repo:        "https://github.com/org/zeta",
				Description: "Zeta tools",
			},
		},
	}
	b, _ := yaml.Marshal(&existing)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(regPath, b, 0o600)

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir:    dir,
		RegistryPath: regPath,
		URL:          "https://example.com/registry.yaml",
		FetchFn:      fakeFetchFn(testRemoteRegistryYAML),
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reg, err := config.LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("loading merged registry: %v", err)
	}

	// Should have 3 packs: delta-pack (existing, not overwritten), zeta-pack, epsilon-pack (new).
	if len(reg.Packs) != 3 {
		t.Fatalf("expected 3 packs, got %d", len(reg.Packs))
	}

	// delta-pack should retain the OLD URL (not overwritten).
	if reg.Packs["delta-pack"].Repo != "https://github.com/org/delta-OLD" {
		t.Errorf("delta-pack Repo was overwritten: got %q", reg.Packs["delta-pack"].Repo)
	}

	output := buf.String()
	if !strings.Contains(output, "1 new pack(s)") {
		t.Errorf("expected '1 new pack(s)' in output, got: %s", output)
	}
	if !strings.Contains(output, "3 total") {
		t.Errorf("expected '3 total' in output, got: %s", output)
	}
}

func TestRegistryFetch_URLFromSyncConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a sync-config with registry_url set.
	sc := config.SyncConfig{SchemaVersion: 1}
	sc.Defaults.RegistryURL = "https://example.com/from-config.yaml"
	config.SaveSyncConfig(config.SyncConfigPath(dir), sc)

	regPath := filepath.Join(dir, "registry.yaml")
	var capturedURL string
	fetchFn := func(url string) ([]byte, error) {
		capturedURL = url
		return []byte(testRemoteRegistryYAML), nil
	}

	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir:    dir,
		RegistryPath: regPath,
		URL:          "", // no explicit URL — should resolve from sync-config
		FetchFn:      fetchFn,
	}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedURL != "https://example.com/from-config.yaml" {
		t.Errorf("expected URL from sync-config, got %q", capturedURL)
	}
}

// ---------------------------------------------------------------------------
// Finding #3: Deep index ignores pack.json root
// ---------------------------------------------------------------------------

func TestDeepIndexOnePack_RespectsPackJsonRoot(t *testing.T) {
	t.Parallel()

	// Set up a fake cloned repo where pack.json specifies root: content.
	cloneFn := func(repo, dir, ref string) error {
		// Create pack.json at the pack path with root: content.
		packJSON := `{"schema_version": 1, "name": "test-pack", "version": "1.0", "root": "content", "rules": ["safety"]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(packJSON), 0o644); err != nil {
			return err
		}

		// Content lives under content/ subdirectory.
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
		Path: "", // pack.json is at the repo root
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

func TestRegistryFetch_UsesDefaultGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var capturedRepo, capturedRef, capturedPath string
	var buf bytes.Buffer
	err := RegistryFetch(RegistryFetchRequest{
		ConfigDir:    dir,
		RegistryPath: filepath.Join(dir, "registry.yaml"),
		URL:          "",
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
