package config

import (
	"slices"
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
		MCP: MCPPack{
			Servers: map[string]MCPDefaults{
				"jira": {DefaultAllowedTools: []string{"search"}},
			},
		},
		Profiles:   []string{"default"},
		Registries: []string{"team"},
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
		"mcp/jira.json":                    false,
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
		MCP: MCPPack{
			Servers: map[string]MCPDefaults{
				"jira":      {DefaultAllowedTools: []string{"get_issue"}},
				"bitbucket": {DefaultAllowedTools: []string{"list_repos"}},
			},
		},
		Profiles:   []string{"default"},
		Registries: []string{"team"},
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
		"mcp/jira.json",
		"mcp/bitbucket.json",
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
		Extras:        []string{"wrappers", "mcp-servers/oci-api", "bootstrap.sh"},
	}

	paths := m.ContentPaths()
	slices.Sort(paths)

	expected := []string{
		"bootstrap.sh",
		"mcp-servers/oci-api",
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

func TestExtrasStagingName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"wrappers", "wrappers"},
		{"mcp-servers/oci-api", "mcp-servers/oci-api"},
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
