package index_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/index"
)

func openTestDB(t *testing.T) *index.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- db.go tests ---

func TestOpenDB_CreatesTablesAndFTS(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	tables := []string{"packs", "resources", "tags", "roles", "requires", "resources_fts"}
	for _, tbl := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", tbl, err)
		}
	}
}

func TestOpenDB_IdempotentReopen(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db1, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db1.Close()

	db2, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}

// --- write.go tests ---

func TestUpdate_InsertsAndReplaces(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	pack := index.PackInfo{Name: "test-pack", Version: "1.0"}
	resources := []index.Resource{
		{Kind: "skill", Name: "find-5xx", Description: "Find 5xx windows",
			Owner: "alice", Tags: []string{"observability", "5xx"},
			Roles: []string{"oncall-operator"}, Requires: []string{"mcp:monitoring"}},
	}
	if err := db.Update(pack, resources); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&count)
	if count != 1 {
		t.Errorf("resources count = %d, want 1", count)
	}

	var tagCount int
	db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	if tagCount != 2 {
		t.Errorf("tags count = %d, want 2", tagCount)
	}

	var roleCount int
	db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&roleCount)
	if roleCount != 1 {
		t.Errorf("roles count = %d, want 1", roleCount)
	}

	var reqCount int
	db.QueryRow("SELECT COUNT(*) FROM requires").Scan(&reqCount)
	if reqCount != 1 {
		t.Errorf("requires count = %d, want 1", reqCount)
	}

	// Verify requires was parsed correctly.
	var reqKind, reqTarget string
	db.QueryRow("SELECT kind, target FROM requires").Scan(&reqKind, &reqTarget)
	if reqKind != "mcp" || reqTarget != "monitoring" {
		t.Errorf("requires = (%s, %s), want (mcp, monitoring)", reqKind, reqTarget)
	}

	// Update with different resources — old ones should be gone.
	resources2 := []index.Resource{
		{Kind: "rule", Name: "safety", Description: "Safety rule"},
	}
	if err := db.Update(pack, resources2); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&count)
	if count != 1 {
		t.Errorf("after re-update: resources count = %d, want 1", count)
	}
	db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	if tagCount != 0 {
		t.Errorf("after re-update: tags count = %d, want 0", tagCount)
	}
}

func TestUpdate_MultiplePacks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "pack-a", Version: "1.0"}, []index.Resource{
		{Kind: "rule", Name: "r1"},
	})
	db.Update(index.PackInfo{Name: "pack-b", Version: "2.0"}, []index.Resource{
		{Kind: "skill", Name: "s1"},
		{Kind: "skill", Name: "s2"},
	})

	var count int
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&count)
	if count != 3 {
		t.Errorf("total resources = %d, want 3", count)
	}

	// Re-update pack-a should not affect pack-b.
	db.Update(index.PackInfo{Name: "pack-a", Version: "1.1"}, []index.Resource{})
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&count)
	if count != 2 {
		t.Errorf("after re-update pack-a: total resources = %d, want 2", count)
	}
}

// --- query.go tests ---

func TestSearch_FTS(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "find-5xx", Description: "Find elevated 5xx error windows"},
		{Kind: "workflow", Name: "deploy", Description: "Deploy to production"},
	})

	results, err := db.Search("5xx", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "find-5xx" {
		t.Errorf("search '5xx' got %v, want [find-5xx]", results)
	}
}

func TestSearch_WithTagFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "find-5xx", Description: "Find 5xx", Tags: []string{"observability"}},
		{Kind: "skill", Name: "other", Description: "Other 5xx thing", Tags: []string{"testing"}},
	})

	results, err := db.Search("5xx", index.SearchFilters{Tags: []string{"observability"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "find-5xx" {
		t.Errorf("got %v, want [find-5xx]", results)
	}
}

func TestSearch_WithRoleFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "ops-skill", Description: "For operators", Roles: []string{"oncall-operator"}},
		{Kind: "skill", Name: "dev-skill", Description: "For developers", Roles: []string{"developer"}},
	})

	results, err := db.Search("", index.SearchFilters{Role: "oncall-operator"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "ops-skill" {
		t.Errorf("got %v, want [ops-skill]", results)
	}
}

func TestSearch_WithKindFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "s1", Description: "A skill"},
		{Kind: "rule", Name: "r1", Description: "A rule"},
	})

	results, err := db.Search("", index.SearchFilters{Kind: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "s1" {
		t.Errorf("got %v, want [s1]", results)
	}
}

func TestSearch_NoFilters(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "rule", Name: "r1"},
		{Kind: "skill", Name: "s1"},
	})

	results, err := db.Search("", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestRawQuery(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "find-5xx", Description: "Find 5xx"},
	})

	rows, cols, err := db.RawQuery("SELECT name, kind FROM resources")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || len(rows) != 1 {
		t.Errorf("cols=%v rows=%d, want 2 cols 1 row", cols, len(rows))
	}
}

func TestSchema(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	schema, err := db.Schema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "CREATE TABLE") {
		t.Errorf("schema missing CREATE TABLE: %s", schema[:100])
	}
}

// --- extract.go tests ---

func TestExtractFromPack(t *testing.T) {
	t.Parallel()
	pack := domain.Pack{
		Name:    "test",
		Version: "1.0",
		Rules: []domain.Rule{
			{Name: "safety", Frontmatter: domain.RuleFrontmatter{
				Description: "Safety checks",
				Metadata: map[string]any{
					"owner": "alice", "tags": []any{"safety", "gate"},
					"role": []any{"operator"}, "requires": []any{"mcp:monitoring"},
				},
			}, SourcePath: "rules/safety.md"},
		},
		Skills: []domain.Skill{
			{Name: "find-5xx", Frontmatter: domain.SkillFrontmatter{
				Description: "Find 5xx windows",
				Metadata:    map[string]any{"owner": "bob"},
			}},
		},
	}

	info, resources := index.ExtractFromPack(pack)
	if info.Name != "test" {
		t.Errorf("pack name = %s, want test", info.Name)
	}
	if len(resources) != 2 {
		t.Fatalf("resources count = %d, want 2", len(resources))
	}

	r := resources[0]
	if r.Kind != "rule" || r.Name != "safety" || r.Owner != "alice" {
		t.Errorf("rule = %+v", r)
	}
	if len(r.Tags) != 2 || r.Tags[0] != "safety" {
		t.Errorf("tags = %v", r.Tags)
	}
	if len(r.Requires) != 1 || r.Requires[0] != "mcp:monitoring" {
		t.Errorf("requires = %v", r.Requires)
	}

	s := resources[1]
	if s.Kind != "skill" || s.Name != "find-5xx" || s.Owner != "bob" {
		t.Errorf("skill = %+v", s)
	}
}

func TestExtractFromPack_SetsInstalled(t *testing.T) {
	t.Parallel()
	pack := domain.Pack{Name: "test", Version: "1.0"}
	info, _ := index.ExtractFromPack(pack)
	if !info.Installed {
		t.Error("ExtractFromPack should set Installed=true")
	}
	if info.Source != "sync" {
		t.Errorf("source = %q, want sync", info.Source)
	}
}

// --- discoverability tests ---

func TestUpdateRegistryPacks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	packs := []index.PackInfo{
		{Name: "remote-a", Description: "Remote pack A", Repo: "https://example.com/a.git", Owner: "team-a"},
		{Name: "remote-b", Description: "Remote pack B", Repo: "https://example.com/b.git", Ref: "main"},
	}
	if err := db.UpdateRegistryPacks(packs); err != nil {
		t.Fatal(err)
	}

	// Verify packs were inserted as uninstalled.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM packs WHERE installed = 0").Scan(&count)
	if count != 2 {
		t.Errorf("uninstalled packs = %d, want 2", count)
	}

	// Verify no resources were created.
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&count)
	if count != 0 {
		t.Errorf("resources = %d, want 0", count)
	}
}

func TestUpdateRegistryPacks_DoesNotDowngradeInstalled(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Install a pack via Update (which sets installed=1).
	db.Update(index.PackInfo{Name: "my-pack", Version: "1.0"}, []index.Resource{
		{Kind: "rule", Name: "r1"},
	})

	// Upsert same pack from registry — should not downgrade to uninstalled.
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "my-pack", Description: "From registry", Repo: "https://example.com/pack.git"},
	})

	var installed int
	db.QueryRow("SELECT installed FROM packs WHERE name = 'my-pack'").Scan(&installed)
	if installed != 1 {
		t.Errorf("installed = %d, want 1 (should not downgrade)", installed)
	}

	// But repo should be updated (registry provides source coordinates).
	var repo string
	db.QueryRow("SELECT repo FROM packs WHERE name = 'my-pack'").Scan(&repo)
	if repo != "https://example.com/pack.git" {
		t.Errorf("repo = %q, want updated from registry", repo)
	}

	// Resources should still be there.
	var resCount int
	db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&resCount)
	if resCount != 1 {
		t.Errorf("resources = %d, want 1 (should be preserved)", resCount)
	}
}

func TestSearch_IncludesUninstalledPacks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Insert installed pack with resources.
	db.Update(index.PackInfo{Name: "installed-pack", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "find-5xx", Description: "Find 5xx errors"},
	})

	// Insert uninstalled pack from registry.
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "remote-pack", Description: "Remote 5xx analysis tools"},
	})

	// Search should return both the resource and the uninstalled pack.
	results, err := db.Search("5xx", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (1 resource + 1 pack)", len(results))
	}

	// First result should be the installed resource.
	if results[0].Kind != "skill" || !results[0].Installed {
		t.Errorf("result[0] = %+v, want installed skill", results[0])
	}
	// Second result should be the uninstalled pack.
	if results[1].Kind != "pack" || results[1].Installed {
		t.Errorf("result[1] = %+v, want uninstalled pack", results[1])
	}
}

func TestSearch_InstalledFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "installed-pack", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "s1", Description: "Installed skill"},
	})
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "available-pack", Description: "Available pack"},
	})

	// Only installed.
	yes := true
	results, err := db.Search("", index.SearchFilters{Installed: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "s1" {
		t.Errorf("installed filter: got %v, want [s1]", results)
	}

	// Only available (uninstalled).
	no := false
	results, err = db.Search("", index.SearchFilters{Installed: &no})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Kind != "pack" {
		t.Errorf("available filter: got %v, want [pack]", results)
	}
}

func TestSearch_KindFilterExcludesPacks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "s1", Description: "A skill"},
	})
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "remote", Description: "A remote pack"},
	})

	// Filtering by kind=skill should exclude pack-level results.
	results, err := db.Search("", index.SearchFilters{Kind: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1 (pack-level should be excluded)", len(results))
	}
}

func TestOpenDB_SchemaV3HasBodyAndCategory(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Verify body and category columns exist on resources.
	_, err := db.Exec(`INSERT INTO packs (name) VALUES ('test-v3')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO resources (pack_id, kind, name, body, category)
		VALUES (1, 'skill', 'test', 'some body text', 'ops')`)
	if err != nil {
		t.Fatalf("body/category columns not present: %v", err)
	}

	var body, category string
	db.QueryRow("SELECT body, category FROM resources WHERE name='test'").Scan(&body, &category)
	if body != "some body text" || category != "ops" {
		t.Errorf("got body=%q category=%q", body, category)
	}
}

func TestSearch_BodyText(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "workflow", Name: "deploy", Description: "Deploy workflow",
			Body: "Check deployment pipeline execution targets for errors before proceeding"},
		{Kind: "skill", Name: "triage", Description: "Triage skill",
			Body: "Query Grafana dashboards for anomalies"},
	})

	// Search for a term only in the body, not in name/description.
	results, err := db.Search("pipeline", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("body search for 'pipeline': got %v, want [deploy]", results)
	}
}

func TestSearch_BodySnippet(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "workflow", Name: "deploy", Description: "Deploy workflow",
			Body: "Step 1: Check deployment pipeline execution targets. Step 2: Verify no alarms."},
	})

	results, err := db.Search("pipeline", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet for body match")
	}
	if !strings.Contains(results[0].Snippet, "pipeline") {
		t.Errorf("snippet should contain search term, got: %s", results[0].Snippet)
	}
}

func TestSearch_BM25Ranking(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		// Match in body only.
		{Kind: "skill", Name: "other", Description: "Something else",
			Body: "This mentions triage in the body text"},
		// Match in name.
		{Kind: "skill", Name: "triage", Description: "Something else",
			Body: "Unrelated body text"},
	})

	results, err := db.Search("triage", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Name match should rank higher than body match (name weight=10 > body weight=1).
	if results[0].Name != "triage" {
		t.Errorf("expected name match to rank first, got %s", results[0].Name)
	}
}

func TestSearch_WithCategoryFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "ops-skill", Description: "Ops skill", Category: "ops"},
		{Kind: "skill", Name: "dev-skill", Description: "Dev skill", Category: "dev"},
		{Kind: "skill", Name: "no-cat", Description: "No category"},
	})

	results, err := db.Search("", index.SearchFilters{Category: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "ops-skill" {
		t.Errorf("category filter: got %v, want [ops-skill]", results)
	}
}

func TestSearch_CategoryInResult(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "rule", Name: "safety", Description: "Safety rule", Category: "governance"},
	})

	results, err := db.Search("safety", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Category != "governance" {
		t.Errorf("category = %q, want governance", results[0].Category)
	}
}

func TestSearch_CategoryExcludesPacks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	db.Update(index.PackInfo{Name: "p1", Version: "1.0"}, []index.Resource{
		{Kind: "skill", Name: "s1", Description: "An ops skill", Category: "ops"},
	})
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "remote", Description: "A remote pack"},
	})

	// Category filter should exclude pack-level results.
	results, err := db.Search("", index.SearchFilters{Category: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Kind != "skill" {
		t.Errorf("got %v, want [skill s1]", results)
	}
}

func TestExtractFromPack_BodyAndCategory(t *testing.T) {
	t.Parallel()
	pack := domain.Pack{
		Name:    "test",
		Version: "1.0",
		Rules: []domain.Rule{
			{Name: "safety", Body: []byte("rule body text"), Frontmatter: domain.RuleFrontmatter{
				Description: "Safety checks",
				Metadata:    map[string]any{"category": "governance"},
			}},
		},
		Skills: []domain.Skill{
			{Name: "triage", Body: []byte("skill body text"), Frontmatter: domain.SkillFrontmatter{
				Description: "Triage ops",
				Metadata:    map[string]any{"category": "ops"},
			}},
		},
		Workflows: []domain.Workflow{
			{Name: "deploy", Body: []byte("workflow body text"), Frontmatter: domain.WorkflowFrontmatter{
				Description: "Deploy things",
			}},
		},
	}

	_, resources := index.ExtractFromPack(pack)
	if len(resources) != 3 {
		t.Fatalf("resources = %d, want 3", len(resources))
	}

	// Rule (index 0).
	if resources[0].Body != "rule body text" {
		t.Errorf("rule body = %q", resources[0].Body)
	}
	if resources[0].Category != "governance" {
		t.Errorf("rule category = %q", resources[0].Category)
	}

	// Workflow (index 1 — workflows are extracted before skills).
	if resources[1].Body != "workflow body text" {
		t.Errorf("workflow body = %q", resources[1].Body)
	}
	if resources[1].Category != "" {
		t.Errorf("workflow category = %q, want empty", resources[1].Category)
	}

	// Skill (index 2).
	if resources[2].Body != "skill body text" {
		t.Errorf("skill body = %q", resources[2].Body)
	}
	if resources[2].Category != "ops" {
		t.Errorf("skill category = %q", resources[2].Category)
	}
}

func TestOpenDB_SchemaV2HasPacksColumns(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Verify the new columns exist by writing to them.
	_, err := db.Exec(`INSERT INTO packs (name, installed, source, ref, path, contact)
		VALUES ('test', 0, 'registry', 'main', 'packs/test', '#test-channel')`)
	if err != nil {
		t.Fatalf("new columns not present: %v", err)
	}

	var installed int
	var source, ref, path, contact string
	db.QueryRow("SELECT installed, source, ref, path, contact FROM packs WHERE name='test'").
		Scan(&installed, &source, &ref, &path, &contact)
	if installed != 0 || source != "registry" || ref != "main" || path != "packs/test" || contact != "#test-channel" {
		t.Errorf("got installed=%d source=%s ref=%s path=%s contact=%s", installed, source, ref, path, contact)
	}
}

func TestOpenDB_PacksFTS(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Insert an uninstalled pack and search via FTS.
	db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "openshift-runbooks", Description: "OpenShift operational runbooks for incident response"},
	})

	// FTS should find it by description.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM packs_fts WHERE packs_fts MATCH 'incident'").Scan(&count)
	if count != 1 {
		t.Errorf("packs_fts match for 'incident' = %d, want 1", count)
	}
}

func TestUpdateDeepIndex_Basic(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Deep-index a pack with resources.
	pack := index.PackInfo{
		Name:        "remote-pack",
		Description: "A remotely indexed pack",
		Repo:        "https://example.com/repo.git",
		Ref:         "main",
		Source:      "deep-index",
	}
	resources := []index.Resource{
		{Kind: "rule", Name: "alpha", Description: "Alpha rule", Tags: []string{"ops"}, Category: "ops"},
		{Kind: "skill", Name: "beta", Description: "Beta skill", Body: "Learn to beta"},
	}

	if err := db.UpdateDeepIndex(pack, resources); err != nil {
		t.Fatalf("UpdateDeepIndex: %v", err)
	}

	// Verify pack is uninstalled with deep-index source.
	var installed int
	var source string
	db.QueryRow("SELECT installed, source FROM packs WHERE name='remote-pack'").Scan(&installed, &source)
	if installed != 0 || source != "deep-index" {
		t.Errorf("pack: installed=%d source=%s, want 0 deep-index", installed, source)
	}

	// Verify resources were indexed.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM resources r JOIN packs p ON r.pack_id=p.id WHERE p.name='remote-pack'").Scan(&count)
	if count != 2 {
		t.Errorf("resource count = %d, want 2", count)
	}

	// Verify tags were indexed.
	var tagCount int
	db.QueryRow("SELECT COUNT(*) FROM tags t JOIN resources r ON t.resource_id=r.id JOIN packs p ON r.pack_id=p.id WHERE p.name='remote-pack'").Scan(&tagCount)
	if tagCount != 1 {
		t.Errorf("tag count = %d, want 1", tagCount)
	}

	// Search should find the resources.
	results, err := db.Search("alpha", index.SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Name == "alpha" && r.Pack == "remote-pack" && !r.Installed {
			found = true
		}
	}
	if !found {
		t.Errorf("search for 'alpha' did not find deep-indexed resource; got %v", results)
	}
}

func TestUpdateDeepIndex_SkipsInstalled(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Install a pack via Update (simulates sync).
	err := db.Update(index.PackInfo{Name: "installed-pack", Version: "1.0"}, []index.Resource{
		{Kind: "rule", Name: "original", Description: "Original rule from sync"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Try to deep-index the same pack — should be skipped.
	err = db.UpdateDeepIndex(index.PackInfo{Name: "installed-pack"}, []index.Resource{
		{Kind: "rule", Name: "overridden", Description: "Should not appear"},
	})
	if err != nil {
		t.Fatalf("UpdateDeepIndex: %v", err)
	}

	// Verify the original resource is still there.
	var name string
	db.QueryRow("SELECT r.name FROM resources r JOIN packs p ON r.pack_id=p.id WHERE p.name='installed-pack'").Scan(&name)
	if name != "original" {
		t.Errorf("resource name = %q, want 'original' (deep-index should have been skipped)", name)
	}
}

func TestUpdateDeepIndex_Idempotent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	pack := index.PackInfo{Name: "idempotent-pack", Source: "deep-index"}
	resources := []index.Resource{
		{Kind: "rule", Name: "r1", Description: "First"},
	}

	// Index twice.
	if err := db.UpdateDeepIndex(pack, resources); err != nil {
		t.Fatalf("first UpdateDeepIndex: %v", err)
	}
	resources[0].Description = "Updated"
	if err := db.UpdateDeepIndex(pack, resources); err != nil {
		t.Fatalf("second UpdateDeepIndex: %v", err)
	}

	// Should have exactly 1 resource with updated description.
	var count int
	var desc string
	db.QueryRow("SELECT COUNT(*) FROM resources r JOIN packs p ON r.pack_id=p.id WHERE p.name='idempotent-pack'").Scan(&count)
	db.QueryRow("SELECT r.description FROM resources r JOIN packs p ON r.pack_id=p.id WHERE p.name='idempotent-pack'").Scan(&desc)
	if count != 1 {
		t.Errorf("resource count = %d, want 1", count)
	}
	if desc != "Updated" {
		t.Errorf("description = %q, want 'Updated'", desc)
	}
}

// ---------------------------------------------------------------------------
// Finding #6: Registry fetch --prune leaves stale search index rows
// ---------------------------------------------------------------------------

func TestDeletePack_RemovesFromIndex(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Index a pack via deep-index.
	pack := index.PackInfo{
		Name:        "prunable-pack",
		Description: "Will be pruned",
		Repo:        "https://example.com/repo.git",
		Source:      "deep-index",
	}
	resources := []index.Resource{
		{Kind: "rule", Name: "stale-rule", Description: "A stale rule", Tags: []string{"ops"}},
	}
	if err := db.UpdateDeepIndex(pack, resources); err != nil {
		t.Fatalf("UpdateDeepIndex: %v", err)
	}

	// Verify it's searchable.
	results, err := db.Search("stale", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results before prune")
	}

	// Delete the pack (simulates registry prune).
	if err := db.DeletePack("prunable-pack"); err != nil {
		t.Fatalf("DeletePack: %v", err)
	}

	// Search should no longer return it.
	results, err = db.Search("stale", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Pack == "prunable-pack" || r.Name == "stale-rule" {
			t.Errorf("search still returns pruned pack/resource: %+v", r)
		}
	}

	// Pack row should be gone.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM packs WHERE name='prunable-pack'").Scan(&count)
	if count != 0 {
		t.Errorf("pack row still exists after DeletePack")
	}

	// Resources and tags should be gone (CASCADE).
	db.QueryRow("SELECT COUNT(*) FROM resources r JOIN packs p ON r.pack_id=p.id WHERE p.name='prunable-pack'").Scan(&count)
	if count != 0 {
		t.Errorf("resources still exist after DeletePack")
	}
}

func TestExtractFromPack_NilMetadata(t *testing.T) {
	t.Parallel()
	pack := domain.Pack{
		Name: "minimal",
		Rules: []domain.Rule{
			{Name: "bare", Frontmatter: domain.RuleFrontmatter{Description: "No metadata"}},
		},
		Agents: []domain.Agent{
			{Name: "ag", Frontmatter: domain.AgentFrontmatter{Description: "Agent with no metadata"}},
		},
	}

	_, resources := index.ExtractFromPack(pack)
	if len(resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(resources))
	}
	for _, r := range resources {
		if r.Owner != "" || len(r.Tags) != 0 || len(r.Roles) != 0 || len(r.Requires) != 0 {
			t.Errorf("resource %s has unexpected metadata: %+v", r.Name, r)
		}
	}
}
