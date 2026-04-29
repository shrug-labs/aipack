package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContentPaths(t *testing.T) {
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "test-pack",
		Root:          "bb:org/repo",
		Rules:         []string{"base", "team"},
		Agents:        []string{"reviewer"},
		Workflows:     []string{"deploy"},
		Skills:        []string{"oncall"},
		MCP:           []string{"srv-a"},
		Profiles:      []string{"default"},
		Registries:    []string{"team"},
		Configs: PackConfigs{
			HarnessSettings: map[string][]string{
				"claudecode": {"settings.json"},
			},
		},
	}

	got := m.ContentPaths()

	want := map[string]bool{
		"pack.json":                        false,
		"rules/base.md":                    false,
		"rules/team.md":                    false,
		"agents/reviewer.md":               false,
		"workflows/deploy.md":              false,
		"skills/oncall/":                   false,
		"mcp/srv-a.json":                   false,
		"profiles/default.yaml":            false,
		"registries/team.yaml":             false,
		"configs/claudecode/settings.json": false,
	}

	for _, p := range got {
		if _, ok := want[p]; ok {
			want[p] = true
		} else {
			t.Errorf("unexpected path: %q", p)
		}
	}

	for p, found := range want {
		if !found {
			t.Errorf("missing expected path: %q", p)
		}
	}

	if len(got) != len(want) {
		t.Errorf("got %d paths, want %d", len(got), len(want))
	}
}

func TestContentPaths_AlwaysIncludesPackJSON(t *testing.T) {
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "empty",
		Root:          "bb:org/repo",
	}

	got := m.ContentPaths()
	if len(got) != 1 || got[0] != "pack.json" {
		t.Errorf("empty manifest: got %v, want [pack.json]", got)
	}
}

func TestContentPaths_FullManifest(t *testing.T) {
	t.Parallel()
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "test",
		Root:          ".",
		Rules:         []string{"team", "safety"},
		Agents:        []string{"reviewer"},
		Workflows:     []string{"deploy", "triage"},
		Skills:        []string{"oncall", "debug"},
		MCP:           []string{"srv-a", "srv-b"},
		Profiles:      []string{"default"},
		Registries:    []string{"team"},
		Configs: PackConfigs{
			HarnessSettings: map[string][]string{
				"claudecode": {"settings.json"},
			},
			HarnessPlugins: map[string][]string{
				"opencode": {"plugin.json"},
			},
		},
	}

	paths := m.ContentPaths()

	expected := []string{
		"pack.json",
		"rules/team.md",
		"rules/safety.md",
		"agents/reviewer.md",
		"workflows/deploy.md",
		"workflows/triage.md",
		"skills/oncall/",
		"skills/debug/",
		"mcp/srv-a.json",
		"mcp/srv-b.json",
		"profiles/default.yaml",
		"registries/team.yaml",
		"configs/claudecode/settings.json",
		"configs/opencode/plugin.json",
	}

	slices.Sort(paths)
	slices.Sort(expected)

	if len(paths) != len(expected) {
		t.Fatalf("got %d paths, want %d:\n  got:  %v\n  want: %v", len(paths), len(expected), paths, expected)
	}
	for i := range expected {
		if paths[i] != expected[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

func TestContentPaths_Empty(t *testing.T) {
	t.Parallel()
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "minimal",
		Root:          ".",
	}
	paths := m.ContentPaths()
	if len(paths) != 1 || paths[0] != "pack.json" {
		t.Fatalf("got %v, want [pack.json]", paths)
	}
}

func TestContentPaths_IncludesPlugins(t *testing.T) {
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "plugins-test",
		Root:          "bb:org/repo",
		Configs: PackConfigs{
			HarnessSettings: map[string][]string{
				"opencode": {"opencode.json"},
			},
			HarnessPlugins: map[string][]string{
				"opencode": {"oh-my-opencode.json"},
			},
		},
	}

	got := m.ContentPaths()
	slices.Sort(got)

	want := []string{
		"configs/opencode/oh-my-opencode.json",
		"configs/opencode/opencode.json",
		"pack.json",
	}
	slices.Sort(want)

	if len(got) != len(want) {
		t.Fatalf("got %d paths %v, want %d paths %v", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestContentPaths_IncludesExtrasDirs(t *testing.T) {
	t.Parallel()
	m := PackManifest{
		SchemaVersion: 1,
		Name:          "extras-test",
		Root:          ".",
		Extras:        []string{"wrappers", "mcp-servers/cloud-api", "bootstrap.sh"},
	}

	paths := m.ContentPaths()
	slices.Sort(paths)

	expected := []string{
		"bootstrap.sh",
		"mcp-servers/cloud-api",
		"pack.json",
		"wrappers",
	}
	slices.Sort(expected)

	if len(paths) != len(expected) {
		t.Fatalf("got %d paths %v, want %d paths %v", len(paths), paths, len(expected), expected)
	}
	for i := range expected {
		if paths[i] != expected[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

func TestNormalizeIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []string
		ext     string
		want    []string
	}{
		{"nil input", nil, ".yaml", nil},
		{"empty slice", []string{}, ".yaml", []string{}},
		{"bare IDs pass through", []string{"team", "ops"}, ".yaml", []string{"team", "ops"}},
		{"strips path prefix and ext", []string{"profiles/team.yaml"}, ".yaml", []string{"team"}},
		{"nested path stripped", []string{"a/b/c.yaml"}, ".yaml", []string{"c"}},
		{"extension only stripped", []string{"team.yaml"}, ".yaml", []string{"team"}},
		{"non-matching ext unchanged", []string{"team.json"}, ".yaml", []string{"team.json"}},
		{"multiple dots", []string{"my.config.yaml"}, ".yaml", []string{"my.config"}},
		{"path traversal sanitized", []string{"../escape.yaml"}, ".yaml", []string{"escape"}},
		{"mixed entries", []string{"registries/popular.yaml", "local", "old/path.yaml"}, ".yaml", []string{"popular", "local", "path"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy input to avoid mutating test table.
			var input []string
			if tt.entries != nil {
				input = make([]string, len(tt.entries))
				copy(input, tt.entries)
			}
			got := normalizeIDs(input, tt.ext)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtrasStagingName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"wrappers", "wrappers"},
		{"mcp-servers/cloud-api", "mcp-servers/cloud-api"},
		{"../shared-scripts", "shared-scripts"},
		{"../../shared/lib", "shared/lib"},
		{"../scripts/auth.sh", "scripts/auth.sh"},
		{"..", "."}, // degenerate — validation catches this
	}
	for _, tt := range tests {
		got := ExtrasStagingName(tt.input)
		if got != tt.want {
			t.Errorf("ExtrasStagingName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParsePackManifest_V1NestedMCPExtractsServerIDs(t *testing.T) {
	// schema_version: 1 is the legacy shape where `mcp` is an object with
	// `servers` and optional pack-level tool-policy defaults. The parser
	// extracts sorted server IDs into m.MCP so the runtime sees a uniform
	// shape; pack-level policy fields parse without error but are inert —
	// v0.23+ expresses tool policy in profiles, not manifests.
	data := []byte(`{
		"schema_version": 1,
		"name": "legacy-pack",
		"root": "bb:org/repo",
		"mcp": {
			"servers": {
				"srv-b": {"allowed_tools": ["x"]},
				"srv-a": {}
			},
			"default_allowed_tools": ["tool-a"],
			"default_always_allowed_tools": ["tool-auto"]
		}
	}`)
	m, err := ParsePackManifest(data)
	if err != nil {
		t.Fatalf("ParsePackManifest returned error on valid v1 form: %v", err)
	}
	if !slices.Equal(m.MCP, []string{"srv-a", "srv-b"}) {
		t.Errorf("m.MCP = %v, want [srv-a srv-b] (sorted extraction from v1 servers map)", m.MCP)
	}
	if m.Name != "legacy-pack" || m.Root != "bb:org/repo" || m.SchemaVersion != 1 {
		t.Errorf("non-mcp fields dropped; got %+v", m)
	}
}

func TestParsePackManifest_V2FlatMCPArrayParsesCleanly(t *testing.T) {
	data := []byte(`{
		"schema_version": 2,
		"name": "flat-pack",
		"root": "bb:org/repo",
		"mcp": ["srv-a", "srv-b"]
	}`)
	m, err := ParsePackManifest(data)
	if err != nil {
		t.Fatalf("ParsePackManifest returned error on valid v2 flat mcp: %v", err)
	}
	if !slices.Equal(m.MCP, []string{"srv-a", "srv-b"}) {
		t.Errorf("m.MCP = %v, want [srv-a srv-b]", m.MCP)
	}
	if m.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", m.SchemaVersion)
	}
}

func TestParsePackManifest_V1WithFlatMCPIsRejected(t *testing.T) {
	// Shape is strictly tied to schema_version. A pack that advertises v1
	// but uses the v2 flat form is malformed — the error must point at the
	// fix (bump to schema_version: 2) rather than silently coercing.
	data := []byte(`{
		"schema_version": 1,
		"name": "mismatched",
		"root": "bb:org/repo",
		"mcp": ["srv-a"]
	}`)
	_, err := ParsePackManifest(data)
	if err == nil {
		t.Fatal("expected error for v1 + flat mcp")
	}
	msg := err.Error()
	for _, want := range []string{"schema_version: 1", "flat array", "schema_version: 2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

func TestParsePackManifest_V2WithNestedMCPIsRejected(t *testing.T) {
	// Symmetric case: v2 must use the flat form. The error points at both
	// fixes (convert mcp to an array, or downgrade to v1).
	data := []byte(`{
		"schema_version": 2,
		"name": "mismatched",
		"root": "bb:org/repo",
		"mcp": {"servers": {"srv-a": {}}}
	}`)
	_, err := ParsePackManifest(data)
	if err == nil {
		t.Fatal("expected error for v2 + nested mcp")
	}
	msg := err.Error()
	for _, want := range []string{"schema_version: 2", "nested object", "schema_version: 1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

func TestParsePackManifest_UnsupportedSchemaVersionRejected(t *testing.T) {
	data := []byte(`{"schema_version":99,"name":"future","root":"r","mcp":[]}`)
	_, err := ParsePackManifest(data)
	if err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
	if !strings.Contains(err.Error(), "unsupported") || !strings.Contains(err.Error(), "99") {
		t.Errorf("error should mention the unsupported version; got: %v", err)
	}
}

func TestParsePackManifest_MissingSchemaVersionRejected(t *testing.T) {
	data := []byte(`{"name":"noversion","root":"r"}`)
	_, err := ParsePackManifest(data)
	if err == nil {
		t.Fatal("expected error when schema_version is missing")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error should mention schema_version; got: %v", err)
	}
}

func TestSavePackManifest_NormalizesV1ToV2(t *testing.T) {
	// Regression: a valid v1 pack.json (schema_version: 1 + nested
	// mcp object) used to round-trip through Load → Save as
	// schema_version: 1 + flat mcp array, because SavePackManifest
	// serialized the in-memory PackManifest.MCP []string as an array
	// without updating SchemaVersion. The next read of the saved file
	// then failed rejectMCPShapeMismatch with "v1 expects nested
	// object; got flat array." Install, update, and extract all
	// re-save the manifest, so consumers of v1 packs hit this on the
	// first sync after install.
	t.Parallel()

	v1 := []byte(`{
		"schema_version": 1,
		"name": "my-pack",
		"root": ".",
		"mcp": {
			"servers": {
				"alpha": {"default_allowed_tools": ["one"]},
				"beta": {}
			}
		}
	}`)

	m, err := ParsePackManifest(v1)
	if err != nil {
		t.Fatalf("ParsePackManifest(v1): %v", err)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "pack.json")
	if err := SavePackManifest(out, m); err != nil {
		t.Fatalf("SavePackManifest: %v", err)
	}

	saved, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	if !strings.Contains(string(saved), `"schema_version": 2`) {
		t.Errorf("saved manifest still declares legacy schema_version; got:\n%s", saved)
	}

	reparsed, err := ParsePackManifest(saved)
	if err != nil {
		t.Fatalf("ParsePackManifest(saved): %v — round-trip produced invalid manifest:\n%s", err, saved)
	}
	if reparsed.SchemaVersion != PackSchemaVersion {
		t.Errorf("reparsed SchemaVersion = %d, want %d", reparsed.SchemaVersion, PackSchemaVersion)
	}
	if !slices.Equal(reparsed.MCP, []string{"alpha", "beta"}) {
		t.Errorf("reparsed.MCP = %v, want [alpha beta]", reparsed.MCP)
	}
}

func TestParsePackManifest_EmptyMCPAcceptedUnderEitherVersion(t *testing.T) {
	// Manifests with no mcp field at all pass both versions (auto-discovery
	// from mcp/*.json populates it downstream). The shape check only fires
	// when mcp is present and non-null.
	for _, version := range []int{1, 2} {
		data := fmt.Appendf(nil, `{"schema_version":%d,"name":"empty","root":"r"}`, version)
		if _, err := ParsePackManifest(data); err != nil {
			t.Errorf("schema %d without mcp should parse; got error: %v", version, err)
		}
	}
}
