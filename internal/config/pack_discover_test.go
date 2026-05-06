package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

// TestDiscoverIDs covers slashed-id discovery used for rules/prompts/mcp/
// profiles/registries: walk recursively, preserve the slashed relative path
// in the id.
func TestDiscoverIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"alpha.md":            "x",
		"beta.md":             "x",
		"README.txt":          "x", // wrong suffix
		"group-a/nested.md":   "x",
		"group-a/sub/deep.md": "x",
		"group-a/.hidden.md":  "x", // dotfile is matched
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := DiscoverIDs(dir, ".md")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "group-a/.hidden", "group-a/nested", "group-a/sub/deep"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Fatalf("ids[%d] = %q, want %q (full = %v)", i, ids[i], w, ids)
		}
	}
}

func TestDiscoverIDs_MissingDir(t *testing.T) {
	t.Parallel()
	ids, err := DiscoverIDs("/nonexistent/path", ".md")
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil {
		t.Fatalf("expected nil for missing dir, got %v", ids)
	}
}

func TestDiscoverIDs_EmptyDir(t *testing.T) {
	t.Parallel()
	ids, err := DiscoverIDs(t.TempDir(), ".md")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty slice, got %v", ids)
	}
}

// TestDiscoverIDsByLeaf covers leaf-id discovery used for agents/workflows:
// walk recursively, but the id is the file basename (subdirectory under the
// category root is authoring organization). Returns the id → relpath map
// and an error on within-pack same-leaf collisions.
func TestDiscoverIDsByLeaf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"flat.md":            "x",
		"team-a/nested.md":   "x",
		"team-b/sub/deep.md": "x",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids, paths, err := DiscoverIDsByLeaf(dir, "agents", ".md")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"deep", "flat", "nested"}
	if len(ids) != len(wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}
	for i, w := range wantIDs {
		if ids[i] != w {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], w)
		}
	}
	wantPaths := map[string]string{
		"flat":   "agents/flat.md",
		"nested": "agents/team-a/nested.md",
		"deep":   "agents/team-b/sub/deep.md",
	}
	for id, want := range wantPaths {
		if paths[id] != want {
			t.Errorf("paths[%q] = %q, want %q", id, paths[id], want)
		}
	}
}

func TestDiscoverIDsByLeaf_CollisionError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, rel := range []string{"team-a/foo.md", "team-b/foo.md"} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := DiscoverIDsByLeaf(dir, "agents", ".md")
	if err == nil {
		t.Fatal("expected duplicate-leaf error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `duplicate agents id "foo"`) {
		t.Errorf("error = %q, want substring %q", msg, `duplicate agents id "foo"`)
	}
	if !strings.Contains(msg, "team-a/foo.md") || !strings.Contains(msg, "team-b/foo.md") {
		t.Errorf("error %q should name both source paths", msg)
	}
}

// TestDiscoverSkills covers skill discovery: walk recursively, leaf as id,
// SkipDir at first SKILL.md so bundled assets stay assets, error on
// within-pack same-leaf collisions.
func TestDiscoverSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Top-level skill
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nested skill — captured with leaf id, nested path tracked in map
	if err := os.MkdirAll(filepath.Join(dir, "team-a", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team-a", "beta", "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Bundled-asset SKILL.md inside alpha — must be ignored (SkipDir at outer)
	if err := os.MkdirAll(filepath.Join(dir, "alpha", "fixtures", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "fixtures", "inner", "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Bare dir without SKILL.md — silently skipped
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	// File at root, not a skill
	if err := os.WriteFile(filepath.Join(dir, "notadir.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ids, paths, err := DiscoverSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"alpha", "beta"}
	if len(ids) != len(wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}
	for i, w := range wantIDs {
		if ids[i] != w {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], w)
		}
	}
	if paths["alpha"] != "skills/alpha/SKILL.md" {
		t.Errorf("paths[alpha] = %q, want %q", paths["alpha"], "skills/alpha/SKILL.md")
	}
	if paths["beta"] != "skills/team-a/beta/SKILL.md" {
		t.Errorf("paths[beta] = %q, want %q", paths["beta"], "skills/team-a/beta/SKILL.md")
	}
}

func TestDiscoverSkills_CollisionError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, rel := range []string{"team-a/oncall", "team-b/oncall"} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := DiscoverSkills(dir)
	if err == nil {
		t.Fatal("expected duplicate-skill error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `duplicate skill id "oncall"`) {
		t.Errorf("error = %q, want substring %q", msg, `duplicate skill id "oncall"`)
	}
}

func TestDiscoverSkills_MissingDir(t *testing.T) {
	t.Parallel()
	ids, paths, err := DiscoverSkills("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil || paths != nil {
		t.Fatalf("expected nil slices for missing dir, got %v / %v", ids, paths)
	}
}

func TestDiscoverContent_NilFieldsPopulated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	for _, dir := range []string{"rules", "agents", "workflows", "mcp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "a1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "s1")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp", "srv1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := PackManifest{SchemaVersion: 2, Name: "test", Root: "."}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}

	if len(m.Rules) != 1 || m.Rules[0] != "r1" {
		t.Fatalf("Rules = %v, want [r1]", m.Rules)
	}
	if len(m.Agents) != 1 || m.Agents[0] != "a1" {
		t.Fatalf("Agents = %v, want [a1]", m.Agents)
	}
	if len(m.Workflows) != 0 {
		t.Fatalf("Workflows = %v, want []", m.Workflows)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "s1" {
		t.Fatalf("Skills = %v, want [s1]", m.Skills)
	}
	if len(m.MCP) != 1 || m.MCP[0] != "srv1" {
		t.Fatalf("MCP = %v, want [srv1]", m.MCP)
	}
}

// TestDiscoverContent_NestedAuthoring confirms the asymmetric id model:
// rules carry the slashed path as id; agents/workflows/skills authored under
// subdirectories take the leaf as id, and the actual on-disk path is
// recorded in m.resolvedPaths so RelPath returns the nested location.
func TestDiscoverContent_NestedAuthoring(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, p := range []string{
		"rules/team-a/style.md",
		"rules/team-b/style.md",
		"agents/team-a/reviewer.md",
		"workflows/team/deploy.md",
		"skills/team-a/oncall/SKILL.md",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m := PackManifest{SchemaVersion: 2, Name: "test", Root: "."}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}

	wantRules := []string{"team-a/style", "team-b/style"}
	if len(m.Rules) != len(wantRules) {
		t.Fatalf("Rules = %v, want %v", m.Rules, wantRules)
	}
	if len(m.Agents) != 1 || m.Agents[0] != "reviewer" {
		t.Fatalf("Agents = %v, want [reviewer]", m.Agents)
	}
	if len(m.Workflows) != 1 || m.Workflows[0] != "deploy" {
		t.Fatalf("Workflows = %v, want [deploy]", m.Workflows)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "oncall" {
		t.Fatalf("Skills = %v, want [oncall]", m.Skills)
	}

	// Resolved-path map should point at the nested authored locations.
	cases := []struct {
		cat  string
		id   string
		want string
		got  string
	}{
		{"agents", "reviewer", "agents/team-a/reviewer.md", m.RelPath("agents", "reviewer")},
		{"workflows", "deploy", "workflows/team/deploy.md", m.RelPath("workflows", "deploy")},
		{"skills", "oncall", "skills/team-a/oncall/SKILL.md", m.RelPath("skills", "oncall")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("RelPath(%s, %s) = %q, want %q", c.cat, c.id, c.got, c.want)
		}
	}
}

func TestDiscoverContent_ExplicitFieldsPreserved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r2.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := PackManifest{
		SchemaVersion: 2, Name: "test", Root: ".",
		Rules: []string{"r1"},
	}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}
	if len(m.Rules) != 1 || m.Rules[0] != "r1" {
		t.Fatalf("Rules = %v, want [r1] (explicit preserved)", m.Rules)
	}
}

func TestDiscoverContent_EmptySliceTriggersDiscovery(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "r1.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := PackManifest{
		SchemaVersion: 2, Name: "test", Root: ".",
		Rules: []string{},
	}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatal(err)
	}
	if len(m.Rules) != 1 || m.Rules[0] != "r1" {
		t.Fatalf("Rules = %v, want [r1] (empty slice triggers discovery)", m.Rules)
	}
}

func TestDiscoverContent_PluginsByLeaf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins", "team"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "team", "superpowers.json"), []byte(`{"source":"github:obra/superpowers"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := PackManifest{SchemaVersion: 2, Name: "demo", Root: "."}
	if err := DiscoverContent(&m, root); err != nil {
		t.Fatalf("DiscoverContent: %v", err)
	}
	if len(m.Plugins) != 1 || m.Plugins[0] != "superpowers" {
		t.Fatalf("Plugins = %v, want [superpowers]", m.Plugins)
	}
	if got := m.RelPath(domain.CategoryPlugins, "superpowers"); got != "plugins/team/superpowers.json" {
		t.Fatalf("plugin rel path = %q, want plugins/team/superpowers.json", got)
	}
}
