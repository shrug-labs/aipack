package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

// resolveStrict calls ResolveProfile with CollisionError strategy and discards warnings.
func resolveStrict(t *testing.T, cfg ProfileConfig, profilePath, configDir string) ([]ResolvedPack, []string, error) {
	t.Helper()
	r, err := ResolveProfile(cfg, profilePath, configDir, CollisionError, nil)
	return r.Packs, r.SettingsPacks, err
}

func TestResolveProfile_InstalledPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Set up installed pack at configDir/packs/snap/pack.json
	packRoot := filepath.Join(root, "packs", "snap")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatalf("mkdir pack root: %v", err)
	}
	manifest := PackManifest{
		SchemaVersion: 2,
		Name:          "snap",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o600); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	profilePath := filepath.Join(root, "profiles", "test.yaml")
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "snap", Enabled: BoolPtr(true)}},
	}

	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if packs[0].Root != packRoot {
		t.Fatalf("resolved pack root = %q, want %q", packs[0].Root, packRoot)
	}
}

func TestResolveProfile_PackNotInstalled_Error(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "nonexistent"}},
	}
	_, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err == nil {
		t.Fatal("expected error for missing pack, got nil")
	}
}

func TestResolveProfile_VectorSelectorErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha"},
		MCP:           []string{},
	}, map[string]string{"rules/alpha.md": "---\nname: alpha\n---\nbody\n"})

	include := []string{"alpha"}
	exclude := []string{"alpha"}
	_, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Include: &include, Exclude: &exclude},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules cannot set both include and exclude`) {
		t.Fatalf("expected include/exclude error, got %v", err)
	}

	unknown := []string{"missing"}
	_, _, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Include: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules include references unknown id "missing"`) {
		t.Fatalf("expected unknown include error, got %v", err)
	}

	_, _, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Exclude: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules exclude references unknown id "missing"`) {
		t.Fatalf("expected unknown exclude error, got %v", err)
	}
}

func TestResolveProfile_PluginSelectors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		Plugins:       []string{"linear", "superpowers"},
	}, map[string]string{
		"plugins/linear.json":      `{"source":"github:linear/linear-codex-plugin"}`,
		"plugins/superpowers.json": `{"source":"github:obra/superpowers"}`,
	})

	include := []string{"superpowers"}
	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "base",
			Plugins: VectorSelector{Include: &include},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if got := packs[0].Plugins; len(got) != 1 || got[0] != "superpowers" {
		t.Fatalf("Plugins = %v, want [superpowers]", got)
	}

	unknown := []string{"missing"}
	_, _, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "base",
			Plugins: VectorSelector{Include: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" plugins include references unknown id "missing"`) {
		t.Fatalf("expected unknown plugin include error, got %v", err)
	}
}

func TestResolveProfile_OverrideAndDuplicateErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 2,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"jira"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 2,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"jira"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})

	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil || !strings.Contains(err.Error(), `collision(s)`) {
		t.Fatalf("expected collision error, got %v", err)
	}

	// Override referencing a non-existent resource is an error (catches typos).
	_, _, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Overrides: Overrides{Rules: []string{"typo-rule"}}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `overrides.rules references "typo-rule"`) {
		t.Fatalf("expected unknown override error, got %v", err)
	}

	// Same for MCP overrides.
	_, _, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Overrides: Overrides{MCP: []string{"nonexistent-server"}}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `overrides.mcp references "nonexistent-server"`) {
		t.Fatalf("expected unknown MCP override error, got %v", err)
	}
}

func TestResolveProfile_AllowsDeclaredOverrideAndSettingsPackValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 2,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 2,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})

	// Neither pack has config files, so settingsPacks should be empty
	// even with settings.enabled: true (no configs to contribute).
	packs, settingsPacks, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second", Overrides: Overrides{Rules: []string{"shared"}}, Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("packs = %d, want 2", len(packs))
	}
	if len(settingsPacks) != 0 {
		t.Fatalf("settingsPacks = %v, want empty (no pack has config files)", settingsPacks)
	}

	// Install a pack WITH config files — it should auto-contribute.
	installPackForResolveTest(t, root, "third", PackManifest{
		SchemaVersion: 2,
		Name:          "third",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
		Configs:       PackConfigs{HarnessSettings: map[string][]string{"claudecode": {"settings.local.json"}}},
	}, map[string]string{
		"configs/claudecode/settings.local.json": `{"theme": "dark"}`,
	})
	// Two packs with settings.enabled: true is no longer an error.
	_, settingsPacks, err = resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}, {Name: "third"}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile with multi-pack settings: %v", err)
	}
	// Only "third" has config files, so only it contributes.
	if len(settingsPacks) != 1 || settingsPacks[0] != "third" {
		t.Fatalf("settingsPacks = %v, want [third]", settingsPacks)
	}
}

func TestResolveProfile_OverrideStripsEarlierPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both provide rule "shared" and MCP server "srv-a".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared", "team-only"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md":    "---\nname: shared\n---\nteam version\n",
		"rules/team-only.md": "---\nname: team-only\n---\nbody\n",
		"mcp/srv-a.json":     `{"name":"srv-a"}`,
	})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 2,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared", "org-only"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md":   "---\nname: shared\n---\norg version\n",
		"rules/org-only.md": "---\nname: org-only\n---\nbody\n",
		"mcp/srv-a.json":    `{"name":"srv-a"}`,
	})

	// org declares overrides — its version of "shared" and "srv-a" should win.
	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team"},
			{Name: "org", Overrides: Overrides{Rules: []string{"shared"}, MCP: []string{"srv-a"}}},
		},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	// team pack should have "shared" stripped, keeping only "team-only".
	teamPack := packs[0]
	if teamPack.Name != "team" {
		t.Fatalf("expected pack[0] = team, got %s", teamPack.Name)
	}
	for _, r := range teamPack.Rules {
		if r == "shared" {
			t.Error("team pack should not have 'shared' rule after override")
		}
	}
	if len(teamPack.Rules) != 1 || teamPack.Rules[0] != "team-only" {
		t.Errorf("team rules = %v, want [team-only]", teamPack.Rules)
	}
	if _, ok := teamPack.MCP["srv-a"]; ok {
		t.Error("team pack should not have 'tracker' MCP after override")
	}

	// org pack should have "shared" and "srv-a".
	orgPack := packs[1]
	if orgPack.Name != "org" {
		t.Fatalf("expected pack[1] = org, got %s", orgPack.Name)
	}
	hasShared := false
	for _, r := range orgPack.Rules {
		if r == "shared" {
			hasShared = true
		}
	}
	if !hasShared {
		t.Error("org pack should have 'shared' rule")
	}
	if _, ok := orgPack.MCP["srv-a"]; !ok {
		t.Error("org pack should have 'tracker' MCP")
	}
}

func TestResolveProfile_OverrideWinsRegardlessOfOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both provide rule "shared" and MCP server "srv-a".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\nteam version\n",
		"mcp/srv-a.json":  `{"name":"srv-a"}`,
	})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 2,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\norg version\n",
		"mcp/srv-a.json":  `{"name":"srv-a"}`,
	})

	// team declares override but is listed FIRST — should still win.
	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team", Overrides: Overrides{Rules: []string{"shared"}, MCP: []string{"srv-a"}}},
			{Name: "org"},
		},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	// team (first, override owner) keeps "shared" and "srv-a".
	if len(packs[0].Rules) != 1 || packs[0].Rules[0] != "shared" {
		t.Errorf("team rules = %v, want [shared]", packs[0].Rules)
	}
	if _, ok := packs[0].MCP["srv-a"]; !ok {
		t.Error("team should have 'tracker' MCP")
	}
	// org (second, not owner) has them stripped.
	for _, r := range packs[1].Rules {
		if r == "shared" {
			t.Error("org should not have 'shared' rule")
		}
	}
	if _, ok := packs[1].MCP["srv-a"]; ok {
		t.Error("org should not have 'tracker' MCP")
	}
}

func TestResolveProfile_ExcludePreventsDuplicate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both define MCP servers "alpha" and "beta".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		MCP:           []string{"alpha", "beta"},
	}, map[string]string{"mcp/alpha.json": `{"name":"alpha"}`, "mcp/beta.json": `{"name":"beta"}`})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 2,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		MCP:           []string{"alpha", "beta"},
	}, map[string]string{"mcp/alpha.json": `{"name":"alpha"}`, "mcp/beta.json": `{"name":"beta"}`})

	// org disables both via enabled:false — no conflict.
	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team"},
			{Name: "org", MCP: map[string]MCPServerConfig{
				"alpha": {Enabled: BoolPtr(false)},
				"beta":  {Enabled: BoolPtr(false)},
			}},
		},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	// team should have both servers.
	if _, ok := packs[0].MCP["alpha"]; !ok {
		t.Error("team pack should have 'alpha'")
	}
	if _, ok := packs[0].MCP["beta"]; !ok {
		t.Error("team pack should have 'beta'")
	}
	// org should have neither.
	if _, ok := packs[1].MCP["alpha"]; ok {
		t.Error("org pack should not have 'alpha' (excluded)")
	}
	if _, ok := packs[1].MCP["beta"]; ok {
		t.Error("org pack should not have 'beta' (excluded)")
	}
}

func TestResolveProfile_MCPSelectionErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		MCP:           []string{"jira"},
	}, map[string]string{"mcp/jira.json": `{"name":"jira"}`})

	_, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name: "base",
			MCP:  map[string]MCPServerConfig{"missing": {Enabled: BoolPtr(true)}},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" references unknown mcp server "missing"`) {
		t.Fatalf("expected unknown mcp server error, got %v", err)
	}
}

func TestResolveProfile_AutoDiscover(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Install a pack with NO content fields in the manifest (nil),
	// but with content files on disk.
	manifest := PackManifest{
		SchemaVersion: 2,
		Name:          "disco",
		Version:       "1",
		Root:          ".",
		// Rules, Agents, Workflows, Skills are all nil
		MCP: []string{},
	}
	installPackForResolveTest(t, root, "disco", manifest, map[string]string{
		"rules/auto-rule.md":         "---\nname: auto-rule\n---\nbody\n",
		"agents/auto-agent.md":       "---\nname: auto-agent\n---\nbody\n",
		"skills/auto-skill/SKILL.md": "---\nname: auto-skill\n---\nbody\n",
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "disco", Enabled: BoolPtr(true)}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	p := packs[0]
	if len(p.Rules) != 1 || p.Rules[0] != "auto-rule" {
		t.Fatalf("Rules = %v, want [auto-rule]", p.Rules)
	}
	if len(p.Agents) != 1 || p.Agents[0] != "auto-agent" {
		t.Fatalf("Agents = %v, want [auto-agent]", p.Agents)
	}
	if len(p.Skills) != 1 || p.Skills[0] != "auto-skill" {
		t.Fatalf("Skills = %v, want [auto-skill]", p.Skills)
	}
}

func TestResolveProfile_AutoDiscoverWithExclude(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Pack with nil rules — auto-discover finds both, then profile excludes one.
	manifest := PackManifest{
		SchemaVersion: 2,
		Name:          "filtered",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
	}
	installPackForResolveTest(t, root, "filtered", manifest, map[string]string{
		"rules/keep.md": "---\nname: keep\n---\nbody\n",
		"rules/drop.md": "---\nname: drop\n---\nbody\n",
	})

	exclude := []string{"drop"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "filtered",
			Enabled: BoolPtr(true),
			Rules:   VectorSelector{Exclude: &exclude},
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs[0].Rules) != 1 || packs[0].Rules[0] != "keep" {
		t.Fatalf("Rules = %v, want [keep]", packs[0].Rules)
	}
}

func TestResolveProfile_GlobInclude(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	installPackForResolveTest(t, root, "globs", PackManifest{
		SchemaVersion: 2,
		Name:          "globs",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
	}, map[string]string{
		"rules/anti-slop.md":     "---\nname: anti-slop\n---\nbody\n",
		"rules/anti-bloat.md":    "---\nname: anti-bloat\n---\nbody\n",
		"rules/user-baseline.md": "---\nname: user-baseline\n---\nbody\n",
		"rules/verification.md":  "---\nname: verification\n---\nbody\n",
	})

	// Include using glob pattern: only anti-* rules
	include := []string{"anti-*"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "globs",
			Enabled: BoolPtr(true),
			Rules:   VectorSelector{Include: &include},
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	rules := packs[0].Rules
	if len(rules) != 2 || rules[0] != "anti-bloat" || rules[1] != "anti-slop" {
		t.Fatalf("Rules = %v, want [anti-bloat anti-slop]", rules)
	}
}

func TestResolveProfile_GlobExclude(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	installPackForResolveTest(t, root, "globex", PackManifest{
		SchemaVersion: 2,
		Name:          "globex",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
	}, map[string]string{
		"rules/anti-slop.md":     "---\nname: anti-slop\n---\nbody\n",
		"rules/anti-bloat.md":    "---\nname: anti-bloat\n---\nbody\n",
		"rules/user-baseline.md": "---\nname: user-baseline\n---\nbody\n",
	})

	// Exclude using glob pattern: drop all anti-* rules
	exclude := []string{"anti-*"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "globex",
			Enabled: BoolPtr(true),
			Rules:   VectorSelector{Exclude: &exclude},
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	rules := packs[0].Rules
	if len(rules) != 1 || rules[0] != "user-baseline" {
		t.Fatalf("Rules = %v, want [user-baseline]", rules)
	}
}

func TestResolveProfile_GlobMixedWithExact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	installPackForResolveTest(t, root, "mixed", PackManifest{
		SchemaVersion: 2,
		Name:          "mixed",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma", "anti-slop"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md":     "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":      "---\nname: beta\n---\nbody\n",
		"rules/gamma.md":     "---\nname: gamma\n---\nbody\n",
		"rules/anti-slop.md": "---\nname: anti-slop\n---\nbody\n",
	})

	// Mix exact ID and glob in include
	include := []string{"alpha", "anti-*"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "mixed",
			Rules: VectorSelector{Include: &include},
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	rules := packs[0].Rules
	if len(rules) != 2 || rules[0] != "alpha" || rules[1] != "anti-slop" {
		t.Fatalf("Rules = %v, want [alpha anti-slop]", rules)
	}
}

func TestResolveProfile_GlobNoMatch_NoError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	installPackForResolveTest(t, root, "nomatch", PackManifest{
		SchemaVersion: 2,
		Name:          "nomatch",
		Version:       "1",
		Root:          ".",
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbody\n",
	})

	// Glob that matches nothing — should not error
	exclude := []string{"experimental-*"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "nomatch",
			Rules: VectorSelector{Exclude: &exclude},
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("expected no error for zero-match glob, got %v", err)
	}
	if len(packs[0].Rules) != 1 || packs[0].Rules[0] != "alpha" {
		t.Fatalf("Rules = %v, want [alpha]", packs[0].Rules)
	}
}

func TestResolveProfile_ExactUnknown_StillErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	installPackForResolveTest(t, root, "exacterr", PackManifest{
		SchemaVersion: 2,
		Name:          "exacterr",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbody\n",
	})

	// Exact unknown ID should still error (backward compat)
	include := []string{"nonexistent"}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "exacterr",
			Rules: VectorSelector{Include: &include},
		}},
	}
	_, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `unknown id "nonexistent"`) {
		t.Fatalf("expected unknown id error, got %v", err)
	}
}

func TestResolveProfile_DisabledPackOverridesIgnored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both provide rule "shared" and MCP server "srv-a".
	// The disabled pack declares overrides — these should be ignored.
	installPackForResolveTest(t, root, "personal", PackManifest{
		SchemaVersion: 2,
		Name:          "personal",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\npersonal version\n",
		"mcp/srv-a.json":  `{"name":"srv-a"}`,
	})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\nteam version\n",
		"mcp/srv-a.json":  `{"name":"srv-a"}`,
	})

	// personal is disabled but has overrides — should not cause errors
	// and should not participate in conflict resolution.
	// team declares overrides so the two enabled-pack collision is resolved.
	_, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{
				Name:      "personal",
				Enabled:   BoolPtr(false),
				Overrides: Overrides{Rules: []string{"shared"}, MCP: []string{"srv-a"}},
			},
			{
				Name: "team",
			},
		},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("disabled pack with overrides should not error, got: %v", err)
	}
}

func TestResolveProfile_DisabledPackOverrideShouldNotResolveEnabledConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Three packs: two enabled provide "shared", one disabled declares the override.
	// The disabled pack's override must not silently resolve the conflict
	// between the two enabled packs.
	installPackForResolveTest(t, root, "alpha", PackManifest{
		SchemaVersion: 2,
		Name:          "alpha",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\nalpha version\n",
	})
	installPackForResolveTest(t, root, "beta", PackManifest{
		SchemaVersion: 2,
		Name:          "beta",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\nbeta version\n",
	})
	installPackForResolveTest(t, root, "disabled-resolver", PackManifest{
		SchemaVersion: 2,
		Name:          "disabled-resolver",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
	}, map[string]string{
		"rules/shared.md": "---\nname: shared\n---\ndisabled version\n",
	})

	// disabled-resolver declares override for "shared" but is disabled.
	// alpha and beta both provide "shared" without their own override.
	// This should error — the disabled pack must not silently resolve the conflict.
	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "alpha"},
			{Name: "beta"},
			{
				Name:      "disabled-resolver",
				Enabled:   BoolPtr(false),
				Overrides: Overrides{Rules: []string{"shared"}},
			},
		},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil {
		t.Fatal("expected duplicate error when disabled pack's override should not resolve enabled-pack conflict")
	}
	if !strings.Contains(err.Error(), "collision(s)") {
		t.Fatalf("expected 'collision(s)' error, got: %v", err)
	}
}

func TestResolveProfile_CollisionStrategy_FirstWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2, Name: "base", Version: "1", Root: ".",
		Rules: []string{"shared"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbase version\n"})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2, Name: "team", Version: "1", Root: ".",
		Rules: []string{"shared"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nteam version\n"})

	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "base"}, {Name: "team"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionFirstWins, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.CollisionWarnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(r.CollisionWarnings))
	}
	if !strings.Contains(r.CollisionWarnings[0].Message, "first-wins") {
		t.Errorf("expected first-wins in warning, got %q", r.CollisionWarnings[0].Message)
	}
	// First pack (base) should keep the rule.
	if len(r.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(r.Packs))
	}
	if !slices.Contains(r.Packs[0].Rules, "shared") {
		t.Error("first pack should keep 'shared' rule")
	}
	if slices.Contains(r.Packs[1].Rules, "shared") {
		t.Error("second pack should NOT have 'shared' rule with first-wins")
	}
}

func TestResolveProfile_CollisionStrategy_FirstWins_MultiID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// base provides alpha, beta, gamma. team also provides alpha and beta.
	// Under first-wins, both colliding IDs must be stripped from team.
	// This exercises the deferred-deletion path: mutating the current pack's
	// slice during range iteration would skip the second colliding ID.
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2, Name: "base", Version: "1", Root: ".",
		Rules: []string{"alpha", "beta", "gamma"},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbase version\n",
		"rules/beta.md":  "---\nname: beta\n---\nbase version\n",
		"rules/gamma.md": "---\nname: gamma\n---\nbase version\n",
	})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2, Name: "team", Version: "1", Root: ".",
		Rules: []string{"alpha", "beta", "team-only"},
	}, map[string]string{
		"rules/alpha.md":     "---\nname: alpha\n---\nteam version\n",
		"rules/beta.md":      "---\nname: beta\n---\nteam version\n",
		"rules/team-only.md": "---\nname: team-only\n---\nbody\n",
	})

	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "base"}, {Name: "team"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionFirstWins, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.CollisionWarnings) != 2 {
		t.Fatalf("expected 2 warnings (alpha + beta), got %d", len(r.CollisionWarnings))
	}
	// base keeps both colliding rules.
	if !slices.Contains(r.Packs[0].Rules, "alpha") || !slices.Contains(r.Packs[0].Rules, "beta") {
		t.Errorf("base should keep both alpha and beta, got %v", r.Packs[0].Rules)
	}
	// team must have BOTH stripped — not just one.
	if slices.Contains(r.Packs[1].Rules, "alpha") {
		t.Error("team should NOT have 'alpha' with first-wins")
	}
	if slices.Contains(r.Packs[1].Rules, "beta") {
		t.Error("team should NOT have 'beta' with first-wins")
	}
	// team-only is unique, so it stays.
	if !slices.Contains(r.Packs[1].Rules, "team-only") {
		t.Error("team should keep 'team-only'")
	}
}

func TestResolveProfile_CollisionStrategy_LastWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Four-pack layering: essentials → dev-starter → team → personal.
	// Overlaps at every layer to exercise the cascade.
	installPackForResolveTest(t, root, "essentials", PackManifest{
		SchemaVersion: 2, Name: "essentials", Version: "1", Root: ".",
		Rules:     []string{"anti-slop", "show-your-work", "base-only"},
		Skills:    []string{"tdd"},
		Workflows: []string{"deploy"},
		MCP:       []string{"jira"},
	}, map[string]string{
		"rules/anti-slop.md":      "---\nname: anti-slop\n---\nessentials version\n",
		"rules/show-your-work.md": "---\nname: show-your-work\n---\nessentials version\n",
		"rules/base-only.md":      "---\nname: base-only\n---\nunique to essentials\n",
		"skills/tdd/SKILL.md":     "---\nname: tdd\n---\nessentials tdd\n",
		"workflows/deploy.md":     "---\nname: deploy\n---\nessentials deploy\n",
		"mcp/jira.json":           `{"name":"jira"}`,
	})
	installPackForResolveTest(t, root, "dev-starter", PackManifest{
		SchemaVersion: 2, Name: "dev-starter", Version: "1", Root: ".",
		Rules:  []string{"anti-slop"},
		Skills: []string{"tdd", "oncall"},
		MCP:    []string{"deploy-tool", "jira"},
	}, map[string]string{
		"rules/anti-slop.md":     "---\nname: anti-slop\n---\ndev-starter version\n",
		"skills/tdd/SKILL.md":    "---\nname: tdd\n---\ndev-starter tdd\n",
		"skills/oncall/SKILL.md": "---\nname: oncall\n---\ndev-starter oncall\n",
		"mcp/jira.json":          `{"name":"jira"}`,
		"mcp/deploy-tool.json":   `{"name":"deploy-tool"}`,
	})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2, Name: "team", Version: "1", Root: ".",
		Rules:     []string{"anti-slop"},
		Workflows: []string{"deploy"},
	}, map[string]string{
		"rules/anti-slop.md":  "---\nname: anti-slop\n---\nteam version\n",
		"workflows/deploy.md": "---\nname: deploy\n---\nteam deploy\n",
	})
	installPackForResolveTest(t, root, "personal", PackManifest{
		SchemaVersion: 2, Name: "personal", Version: "1", Root: ".",
		Rules: []string{"show-your-work"},
	}, map[string]string{
		"rules/show-your-work.md": "---\nname: show-your-work\n---\npersonal version\n",
	})

	profilePath := filepath.Join(root, "profile.yaml")

	t.Run("four_pack_cascade", func(t *testing.T) {
		r, err := ResolveProfile(ProfileConfig{
			SchemaVersion: ProfileSchemaVersion,
			Packs: []PackEntry{
				{Name: "essentials"},
				{Name: "dev-starter"},
				{Name: "team"},
				{Name: "personal"},
			},
		}, profilePath, root, CollisionLastWins, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		packs, warnings := r.Packs, r.CollisionWarnings

		// Expected collisions (pair-wise, each shadowing is a separate warning):
		//   anti-slop: essentials→dev-starter, dev-starter→team (2)
		//   tdd:       essentials→dev-starter (1)
		//   jira:      essentials→dev-starter (1)
		//   deploy:    essentials→team (1)
		//   show-your-work: essentials→personal (1)
		// Total: 6 warnings.
		if len(warnings) != 6 {
			t.Fatalf("expected 6 warnings, got %d:", len(warnings))
		}
		for _, w := range warnings {
			if !strings.Contains(w.Message, "last-wins") {
				t.Errorf("expected last-wins in warning, got %q", w.Message)
			}
		}

		// anti-slop: team wins (last pack that has it).
		if slices.Contains(packs[0].Rules, "anti-slop") {
			t.Error("essentials should NOT have anti-slop")
		}
		if slices.Contains(packs[1].Rules, "anti-slop") {
			t.Error("dev-starter should NOT have anti-slop")
		}
		if !slices.Contains(packs[2].Rules, "anti-slop") {
			t.Error("team should have anti-slop (last-wins)")
		}

		// show-your-work: personal wins.
		if slices.Contains(packs[0].Rules, "show-your-work") {
			t.Error("essentials should NOT have show-your-work")
		}
		if !slices.Contains(packs[3].Rules, "show-your-work") {
			t.Error("personal should have show-your-work")
		}

		// base-only: no collision, essentials keeps it.
		if !slices.Contains(packs[0].Rules, "base-only") {
			t.Error("essentials should keep base-only (no collision)")
		}

		// tdd skill: dev-starter wins over essentials.
		if slices.Contains(packs[0].Skills, "tdd") {
			t.Error("essentials should NOT have tdd skill")
		}
		if !slices.Contains(packs[1].Skills, "tdd") {
			t.Error("dev-starter should have tdd skill")
		}

		// oncall skill: unique to dev-starter, no collision.
		if !slices.Contains(packs[1].Skills, "oncall") {
			t.Error("dev-starter should keep oncall (no collision)")
		}

		// deploy workflow: team wins over essentials.
		if slices.Contains(packs[0].Workflows, "deploy") {
			t.Error("essentials should NOT have deploy workflow")
		}
		if !slices.Contains(packs[2].Workflows, "deploy") {
			t.Error("team should have deploy workflow")
		}

		// jira MCP: dev-starter wins over essentials.
		if _, ok := packs[0].MCP["jira"]; ok {
			t.Error("essentials should NOT have jira MCP")
		}
		if _, ok := packs[1].MCP["jira"]; !ok {
			t.Error("dev-starter should have jira MCP")
		}

		// deploy-tool MCP: unique to dev-starter, no collision.
		if _, ok := packs[1].MCP["deploy-tool"]; !ok {
			t.Error("dev-starter should keep deploy-tool MCP (no collision)")
		}
	})

	t.Run("no_collisions_no_warnings", func(t *testing.T) {
		// When packs don't overlap, no warnings should be emitted.
		r, err := ResolveProfile(ProfileConfig{
			SchemaVersion: ProfileSchemaVersion,
			Packs:         []PackEntry{{Name: "team"}, {Name: "personal"}},
		}, profilePath, root, CollisionLastWins, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.CollisionWarnings) != 0 {
			t.Errorf("expected 0 warnings for non-overlapping packs, got %d: %v", len(r.CollisionWarnings), r.CollisionWarnings)
		}
		if !slices.Contains(r.Packs[0].Rules, "anti-slop") {
			t.Error("team should keep anti-slop")
		}
		if !slices.Contains(r.Packs[1].Rules, "show-your-work") {
			t.Error("personal should keep show-your-work")
		}
	})

	t.Run("disabled_pack_skipped", func(t *testing.T) {
		// A disabled pack shouldn't trigger collision warnings.
		r, err := ResolveProfile(ProfileConfig{
			SchemaVersion: ProfileSchemaVersion,
			Packs: []PackEntry{
				{Name: "essentials"},
				{Name: "dev-starter", Enabled: BoolPtr(false)},
				{Name: "team"},
			},
		}, profilePath, root, CollisionLastWins, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Only essentials→team collisions: anti-slop and deploy = 2 warnings.
		if len(r.CollisionWarnings) != 2 {
			t.Fatalf("expected 2 warnings (disabled pack excluded), got %d: %v", len(r.CollisionWarnings), r.CollisionWarnings)
		}
	})

	t.Run("override_suppresses_warning", func(t *testing.T) {
		// Explicit override should NOT produce a collision warning.
		r, err := ResolveProfile(ProfileConfig{
			SchemaVersion: ProfileSchemaVersion,
			Packs: []PackEntry{
				{Name: "essentials"},
				{Name: "team", Overrides: Overrides{Rules: []string{"anti-slop"}}},
			},
		}, profilePath, root, CollisionLastWins, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// deploy still collides (no override) = 1 warning. anti-slop has override = no warning.
		if len(r.CollisionWarnings) != 1 {
			t.Fatalf("expected 1 warning (override suppresses anti-slop), got %d: %v", len(r.CollisionWarnings), r.CollisionWarnings)
		}
		if !strings.Contains(r.CollisionWarnings[0].Message, "deploy") {
			t.Errorf("expected deploy warning, got %q", r.CollisionWarnings[0].Message)
		}
	})
}

func TestResolveProfile_CollisionStrategy_ErrorCollectsAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 2, Name: "first", Version: "1", Root: ".",
		Rules: []string{"rule-a", "rule-b"},
		MCP:   []string{"srv"},
	}, map[string]string{
		"rules/rule-a.md": "---\nname: rule-a\n---\nbody\n",
		"rules/rule-b.md": "---\nname: rule-b\n---\nbody\n",
		"mcp/srv.json":    `{"name":"srv"}`,
	})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 2, Name: "second", Version: "1", Root: ".",
		Rules: []string{"rule-a", "rule-b"},
		MCP:   []string{"srv"},
	}, map[string]string{
		"rules/rule-a.md": "---\nname: rule-a\n---\nbody\n",
		"rules/rule-b.md": "---\nname: rule-b\n---\nbody\n",
		"mcp/srv.json":    `{"name":"srv"}`,
	})

	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil {
		t.Fatal("expected error for collisions")
	}
	msg := err.Error()
	// Should mention all 3 collisions.
	if !strings.Contains(msg, "3 content collision(s)") {
		t.Errorf("expected '3 content collision(s)', got:\n%s", msg)
	}
	if !strings.Contains(msg, `rules "rule-a"`) {
		t.Errorf("expected rule-a collision in error, got:\n%s", msg)
	}
	if !strings.Contains(msg, `rules "rule-b"`) {
		t.Errorf("expected rule-b collision in error, got:\n%s", msg)
	}
	if !strings.Contains(msg, `mcp "srv"`) {
		t.Errorf("expected mcp srv collision in error, got:\n%s", msg)
	}
	// Should contain remediation YAML.
	if !strings.Contains(msg, "overrides:") {
		t.Errorf("expected remediation YAML in error, got:\n%s", msg)
	}
	if !strings.Contains(msg, "collision_strategy") {
		t.Errorf("expected collision_strategy hint in error, got:\n%s", msg)
	}
}

func TestResolveProfile_CollisionStrategy_MCP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "pack-a", PackManifest{
		SchemaVersion: 2, Name: "pack-a", Version: "1", Root: ".",
		MCP: []string{"shared-srv"},
	}, map[string]string{"mcp/shared-srv.json": `{"name":"shared-srv"}`})
	installPackForResolveTest(t, root, "pack-b", PackManifest{
		SchemaVersion: 2, Name: "pack-b", Version: "1", Root: ".",
		MCP: []string{"shared-srv"},
	}, map[string]string{"mcp/shared-srv.json": `{"name":"shared-srv"}`})

	// first-wins: pack-a keeps server
	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "pack-a"}, {Name: "pack-b"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionFirstWins, nil)
	if err != nil {
		t.Fatalf("first-wins: %v", err)
	}
	if len(r.CollisionWarnings) != 1 {
		t.Fatalf("first-wins: expected 1 warning, got %d", len(r.CollisionWarnings))
	}
	if _, ok := r.Packs[0].MCP["shared-srv"]; !ok {
		t.Error("first-wins: pack-a should keep shared-srv")
	}
	if _, ok := r.Packs[1].MCP["shared-srv"]; ok {
		t.Error("first-wins: pack-b should NOT have shared-srv")
	}

	// last-wins: pack-b keeps server
	r, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "pack-a"}, {Name: "pack-b"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionLastWins, nil)
	if err != nil {
		t.Fatalf("last-wins: %v", err)
	}
	if len(r.CollisionWarnings) != 1 {
		t.Fatalf("last-wins: expected 1 warning, got %d", len(r.CollisionWarnings))
	}
	if _, ok := r.Packs[0].MCP["shared-srv"]; ok {
		t.Error("last-wins: pack-a should NOT have shared-srv")
	}
	if _, ok := r.Packs[1].MCP["shared-srv"]; !ok {
		t.Error("last-wins: pack-b should keep shared-srv")
	}
}

func TestResolveProfile_OverridesOverrideStrategy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2, Name: "base", Version: "1", Root: ".",
		Rules: []string{"shared"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbase version\n"})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2, Name: "team", Version: "1", Root: ".",
		Rules: []string{"shared"},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nteam version\n"})

	// With first-wins, "base" would normally win. But override declares "team" wins.
	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "base"},
			{Name: "team", Overrides: Overrides{Rules: []string{"shared"}}},
		},
	}, filepath.Join(root, "profile.yaml"), root, CollisionFirstWins, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Override should produce no collision warnings — it's an explicit resolution.
	if len(r.CollisionWarnings) != 0 {
		t.Errorf("expected 0 warnings (override resolved it), got %d: %v", len(r.CollisionWarnings), r.CollisionWarnings)
	}
	if slices.Contains(r.Packs[0].Rules, "shared") {
		t.Error("base should NOT have 'shared' — override gives it to team")
	}
	if !slices.Contains(r.Packs[1].Rules, "shared") {
		t.Error("team should have 'shared' via override")
	}
}

func TestResolveProfile_CollisionError_PartialOverrideResolution(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Two packs, each providing two colliding rules.
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 2, Name: "base", Version: "1", Root: ".",
		Rules: []string{"rule-a", "rule-b"},
	}, map[string]string{
		"rules/rule-a.md": "---\nname: rule-a\n---\nbody\n",
		"rules/rule-b.md": "---\nname: rule-b\n---\nbody\n",
	})
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 2, Name: "team", Version: "1", Root: ".",
		Rules: []string{"rule-a", "rule-b"},
	}, map[string]string{
		"rules/rule-a.md": "---\nname: rule-a\n---\nbody\n",
		"rules/rule-b.md": "---\nname: rule-b\n---\nbody\n",
	})

	// No overrides: both collisions should cause an error.
	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "base"}, {Name: "team"}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil {
		t.Fatal("expected error for 2 unresolved collisions")
	}
	if !strings.Contains(err.Error(), "2 content collision(s)") {
		t.Errorf("expected '2 content collision(s)', got:\n%s", err.Error())
	}

	// Override resolves rule-a, but rule-b remains → exactly 1 collision.
	_, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "base"},
			{Name: "team", Overrides: Overrides{Rules: []string{"rule-a"}}},
		},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil {
		t.Fatal("expected error for 1 remaining collision")
	}
	msg := err.Error()
	if !strings.Contains(msg, "1 content collision(s)") {
		t.Errorf("expected '1 content collision(s)', got:\n%s", msg)
	}
	if strings.Contains(msg, `"rule-a"`) {
		t.Errorf("rule-a is overridden and should not appear in error, got:\n%s", msg)
	}
	if !strings.Contains(msg, `"rule-b"`) {
		t.Errorf("rule-b should still be reported as a collision, got:\n%s", msg)
	}

	// Override resolves both → no error.
	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "base"},
			{Name: "team", Overrides: Overrides{Rules: []string{"rule-a", "rule-b"}}},
		},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err != nil {
		t.Fatalf("expected no error when all collisions resolved by overrides, got: %v", err)
	}
	// Fully resolved → no collision warnings either.
	if len(r.CollisionWarnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(r.CollisionWarnings), r.CollisionWarnings)
	}
	// team should have both rules, base should have neither.
	if slices.Contains(r.Packs[0].Rules, "rule-a") || slices.Contains(r.Packs[0].Rules, "rule-b") {
		t.Errorf("base should have no rules, got %v", r.Packs[0].Rules)
	}
	if !slices.Contains(r.Packs[1].Rules, "rule-a") || !slices.Contains(r.Packs[1].Rules, "rule-b") {
		t.Errorf("team should have both rules via overrides, got %v", r.Packs[1].Rules)
	}
}

func TestNormalizeCollisionStrategy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  CollisionStrategy
		err   bool
	}{
		{"", CollisionLastWins, false},
		{"error", CollisionError, false},
		{"first-wins", CollisionFirstWins, false},
		{"last-wins", CollisionLastWins, false},
		{"FIRST-WINS", CollisionFirstWins, false},
		{"  last-wins  ", CollisionLastWins, false},
		{"invalid", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeCollisionStrategy(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("NormalizeCollisionStrategy(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeCollisionStrategy(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeCollisionStrategy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveProfile_DriftAware_RefInPrevInventoryBecomesBrokenRef(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Current pack inventory has only "kept". The profile still references
	// "removed", which was in the previous inventory — expect a BrokenRef,
	// not an error.
	installPackForResolveTest(t, root, "test-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"kept"},
		MCP:           []string{},
	}, map[string]string{"rules/kept.md": "---\nname: kept\n---\nbody\n"})

	include := []string{"kept", "removed"}
	prev := map[string]domain.PackInventory{
		"test-pack": {Rules: []string{"kept", "removed"}},
	}
	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "test-pack",
			Rules: VectorSelector{Include: &include},
		}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, prev)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(r.BrokenRefs) != 1 {
		t.Fatalf("expected 1 BrokenRef, got %d: %+v", len(r.BrokenRefs), r.BrokenRefs)
	}
	br := r.BrokenRefs[0]
	if br.PackName != "test-pack" || br.Category != domain.CategoryRules || br.ID != "removed" || br.Direction != "include" {
		t.Errorf("BrokenRef = %+v", br)
	}
	// The "kept" rule should still resolve.
	if len(r.Packs) != 1 || len(r.Packs[0].Rules) != 1 || r.Packs[0].Rules[0] != "kept" {
		t.Errorf("expected resolved rules = [kept], got %v", r.Packs)
	}
}

func TestResolveProfile_DriftAware_TypoStillErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "test-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"kept"},
		MCP:           []string{},
	}, map[string]string{"rules/kept.md": "---\nname: kept\n---\nbody\n"})

	// "typo" was never in the pack — not in current inventory, not in prev.
	// Expect a hard error, not a BrokenRef.
	include := []string{"typo"}
	prev := map[string]domain.PackInventory{
		"test-pack": {Rules: []string{"kept"}},
	}
	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "test-pack",
			Rules: VectorSelector{Include: &include},
		}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, prev)
	if err == nil || !strings.Contains(err.Error(), `references unknown id "typo"`) {
		t.Fatalf("expected typo error, got %v", err)
	}
}

func TestResolveProfile_DriftAware_OverrideDriftsOut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 2,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"kept"},
		MCP:           []string{},
	}, map[string]string{"rules/kept.md": "---\nname: kept\n---\nbody\n"})

	// Override targets "removed" — not in any current pack, but was in
	// first's previous inventory. Becomes a BrokenRef, not an error.
	prev := map[string]domain.PackInventory{
		"first": {Rules: []string{"kept", "removed"}},
	}
	r, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:      "first",
			Overrides: Overrides{Rules: []string{"removed"}},
		}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, prev)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(r.BrokenRefs) != 1 || r.BrokenRefs[0].Direction != "overrides" || r.BrokenRefs[0].ID != "removed" {
		t.Errorf("expected overrides BrokenRef for 'removed', got %+v", r.BrokenRefs)
	}
}

func TestResolveProfile_DriftAware_NilPrevPreservesHardError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "test-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"kept"},
		MCP:           []string{},
	}, map[string]string{"rules/kept.md": "---\nname: kept\n---\nbody\n"})

	// No prevInventories map → the classic hard-error path must be preserved
	// for fat-finger typo detection.
	include := []string{"removed"}
	_, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "test-pack",
			Rules: VectorSelector{Include: &include},
		}},
	}, filepath.Join(root, "profile.yaml"), root, CollisionError, nil)
	if err == nil || !strings.Contains(err.Error(), `references unknown id "removed"`) {
		t.Fatalf("expected hard error with nil prev, got %v", err)
	}
}

func installPackForResolveTest(t *testing.T, configDir string, packName string, manifest PackManifest, files map[string]string) {
	t.Helper()
	packRoot := filepath.Join(configDir, "packs", packName)
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		path := filepath.Join(packRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveProfile_EmptyIncludeIncludesAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "test-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
		Skills:        []string{"skill-one", "skill-two"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md":            "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":             "---\nname: beta\n---\nbody\n",
		"skills/skill-one/SKILL.md": "---\nname: skill-one\n---\nbody\n",
		"skills/skill-two/SKILL.md": "---\nname: skill-two\n---\nbody\n",
	})

	empty := []string{}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "test-pack",
			Enabled: BoolPtr(true),
			Rules:   VectorSelector{Include: &empty},
			Skills:  VectorSelector{Include: &empty},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	// include: [] = include all (backward compat, same as nil)
	if len(packs[0].Rules) != 2 {
		t.Fatalf("expected 2 rules with empty include, got %d", len(packs[0].Rules))
	}
	if len(packs[0].Skills) != 2 {
		t.Fatalf("expected 2 skills with empty include, got %d", len(packs[0].Skills))
	}
}

func TestResolveProfile_QuietPackOmittedSelectorsExcludeAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
		Skills:        []string{"skill-one", "skill-two"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md":            "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":             "---\nname: beta\n---\nbody\n",
		"skills/skill-one/SKILL.md": "---\nname: skill-one\n---\nbody\n",
		"skills/skill-two/SKILL.md": "---\nname: skill-two\n---\nbody\n",
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-pack",
			Enabled: BoolPtr(true),
			Quiet:   true,
			// No selectors — quiet means nothing included by default.
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].Rules) != 0 {
		t.Fatalf("expected 0 rules for quiet pack with no selectors, got %d", len(packs[0].Rules))
	}
	if len(packs[0].Skills) != 0 {
		t.Fatalf("expected 0 skills for quiet pack with no selectors, got %d", len(packs[0].Skills))
	}
}

func TestResolveProfile_QuietPackWithExplicitInclude(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
		Skills:        []string{"skill-one", "skill-two"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md":            "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":             "---\nname: beta\n---\nbody\n",
		"skills/skill-one/SKILL.md": "---\nname: skill-one\n---\nbody\n",
		"skills/skill-two/SKILL.md": "---\nname: skill-two\n---\nbody\n",
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-pack",
			Enabled: BoolPtr(true),
			Quiet:   true,
			Rules:   VectorSelector{Include: &[]string{"alpha"}},
			Skills:  VectorSelector{Include: &[]string{"skill-two"}},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].Rules) != 1 || packs[0].Rules[0] != "alpha" {
		t.Fatalf("expected [alpha], got %v", packs[0].Rules)
	}
	if len(packs[0].Skills) != 1 || packs[0].Skills[0] != "skill-two" {
		t.Fatalf("expected [skill-two], got %v", packs[0].Skills)
	}
}

func TestResolveProfile_QuietPackEmptyIncludeExcludesAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":  "---\nname: beta\n---\nbody\n",
	})

	empty := []string{}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-pack",
			Enabled: BoolPtr(true),
			Quiet:   true,
			Rules:   VectorSelector{Include: &empty},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	// quiet + include: [] = nothing (quiet overrides backward-compat empty-means-all)
	if len(packs[0].Rules) != 0 {
		t.Fatalf("expected 0 rules for quiet pack with empty include, got %d", len(packs[0].Rules))
	}
}

func TestResolveProfile_ExtractedContentPathsPack(t *testing.T) {
	// After install-time extraction, a content_paths pack looks like a
	// standard pack: pack.json + content at standard paths. Verify the
	// standard resolution path handles it.
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "ext-lib", PackManifest{
		SchemaVersion: 2,
		Name:          "ext-lib",
		Version:       "0.1.0",
		Root:          ".",
		Skills:        []string{"skill-alpha", "skill-beta"},
		MCP:           []string{},
	}, map[string]string{
		"skills/skill-alpha/SKILL.md": "---\nname: skill-alpha\ndescription: test\n---\nbody\n",
		"skills/skill-beta/SKILL.md":  "---\nname: skill-beta\ndescription: test\n---\nbody\n",
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "ext-lib", Enabled: BoolPtr(true)}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].Skills) != 2 {
		t.Fatalf("expected 2 skills, got %v", packs[0].Skills)
	}
}

func TestResolveProfile_QuietPackExcludeReturnsNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":  "---\nname: beta\n---\nbody\n",
		"rules/gamma.md": "---\nname: gamma\n---\nbody\n",
	})

	// Quiet means opt-in only. Exclude on a quiet pack returns nothing —
	// you can't subtract from an empty default. Use include instead.
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-pack",
			Enabled: BoolPtr(true),
			Quiet:   true,
			Rules:   VectorSelector{Exclude: &[]string{"beta"}},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].Rules) != 0 {
		t.Fatalf("expected 0 rules (exclude on quiet = nothing), got %v", packs[0].Rules)
	}
}

func TestResolveProfile_NonQuietOmittedSelectorsIncludeAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "normal-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "normal-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
		Skills:        []string{"skill-one"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md":            "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":             "---\nname: beta\n---\nbody\n",
		"skills/skill-one/SKILL.md": "---\nname: skill-one\n---\nbody\n",
	})

	// No selectors at all on a non-quiet pack → everything included.
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "normal-pack",
			Enabled: BoolPtr(true),
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].Rules) != 2 {
		t.Fatalf("expected 2 rules with omitted selectors, got %d", len(packs[0].Rules))
	}
	if len(packs[0].Skills) != 1 {
		t.Fatalf("expected 1 skill with omitted selectors, got %d", len(packs[0].Skills))
	}
}

func TestResolveProfile_QuietPackMCPStillDelivered(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-mcp", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-mcp",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"some-rule"},
		MCP:           []string{"srv-a"},
	}, map[string]string{
		"rules/some-rule.md": "---\nname: some-rule\n---\nbody\n",
		"mcp/srv-a.json":     `{"name":"srv-a","command":["echo","hi"]}`,
	})

	// Quiet suppresses manifest-derived MCP defaults (same opt-in semantics
	// as content vectors), but an explicit profile entry overrides quiet:
	// a quiet pack with an MCP server explicitly enabled still delivers it.
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-mcp",
			Enabled: BoolPtr(true),
			Quiet:   true,
			MCP: map[string]MCPServerConfig{
				"srv-a": {Enabled: BoolPtr(true)},
			},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	// Content vectors: quiet + omitted = nothing.
	if len(packs[0].Rules) != 0 {
		t.Fatalf("expected 0 rules for quiet pack, got %d", len(packs[0].Rules))
	}
	// MCP: explicitly enabled, should be delivered. Tool lists are silent —
	// no manifest defaults exist; the harness default applies at render time.
	srv, ok := packs[0].MCP["srv-a"]
	if !ok {
		t.Fatal("expected my-server in MCP map")
	}
	if len(srv.AllowedTools) != 0 || len(srv.AlwaysAllowedTools) != 0 {
		t.Fatalf("expected silent tool lists, got allowed=%v always=%v", srv.AllowedTools, srv.AlwaysAllowedTools)
	}
}

// TestResolveProfile_QuietPackEmptyMCPOmitsManifestServers pins the fix for
// the quiet-MCP gap: a quiet profile entry with no explicit mcp: map must
// NOT fall back to the manifest-derived default (all servers enabled). The
// observed regression was that `aipack pack add -q` on a pack declaring
// multiple MCP servers triggered last-wins collisions and {params.*}
// unresolved warnings against other packs' configs.
func TestResolveProfile_QuietPackEmptyMCPOmitsManifestServers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-mcp", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-mcp",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"srv-a", "srv-b"},
	}, map[string]string{
		"mcp/srv-a.json": `{"name":"srv-a","command":["echo","a"]}`,
		"mcp/srv-b.json": `{"name":"srv-b","command":["echo","b"]}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-mcp",
			Enabled: BoolPtr(true),
			Quiet:   true,
			// No explicit MCP map → must resolve to empty under quiet.
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	if len(packs[0].MCP) != 0 {
		t.Fatalf("quiet pack with no explicit mcp selection must deliver no servers, got %v", packs[0].MCP)
	}
}

// TestResolveProfile_NonQuietPackEmptyMCPInheritsManifestServers pins the
// non-quiet default: an empty mcp: map still defaults to manifest-declared
// servers, so packs that add new servers upstream are picked up without
// requiring the consumer to re-declare every server.
func TestResolveProfile_NonQuietPackEmptyMCPInheritsManifestServers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "open-mcp", PackManifest{
		SchemaVersion: 2,
		Name:          "open-mcp",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"srv-a", "srv-b"},
	}, map[string]string{
		"mcp/srv-a.json": `{"name":"srv-a","command":["echo","a"]}`,
		"mcp/srv-b.json": `{"name":"srv-b","command":["echo","b"]}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "open-mcp",
			Enabled: BoolPtr(true),
		}},
	}
	packs, _, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 || len(packs[0].MCP) != 2 {
		t.Fatalf("non-quiet pack with empty mcp: map should inherit both manifest servers, got %v", packs[0].MCP)
	}
}

// TestResolveProfile_QuietPackSettingsDefaultSkipped pins the quiet-settings
// gap fix: a quiet pack that has config files but no explicit settings
// opt-in must NOT be added to settingsPacks. The settings branch previously
// ignored quiet entirely — a quiet pack with a configs/ directory would
// contribute settings just like a non-quiet pack.
func TestResolveProfile_QuietPackSettingsDefaultSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-cfg", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-cfg",
		Version:       "1.0.0",
		Root:          ".",
		Configs:       PackConfigs{HarnessSettings: map[string][]string{"claudecode": {"settings.local.json"}}},
	}, map[string]string{
		"configs/claudecode/settings.local.json": `{"theme":"dark"}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-cfg",
			Enabled: BoolPtr(true),
			Quiet:   true,
			// Settings.Enabled is nil — under quiet this must NOT contribute.
		}},
	}
	_, settingsPacks, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(settingsPacks) != 0 {
		t.Fatalf("quiet pack without explicit Settings.Enabled must not contribute settings, got %v", settingsPacks)
	}
}

// TestResolveProfile_QuietPackSettingsExplicitOptIn verifies the explicit
// opt-in escape hatch: a quiet pack with Settings.Enabled: true still
// contributes settings. This matches the MCP escape hatch (explicit mcp:
// map wins over the quiet default).
func TestResolveProfile_QuietPackSettingsExplicitOptIn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-cfg-opt-in", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-cfg-opt-in",
		Version:       "1.0.0",
		Root:          ".",
		Configs:       PackConfigs{HarnessSettings: map[string][]string{"claudecode": {"settings.local.json"}}},
	}, map[string]string{
		"configs/claudecode/settings.local.json": `{"theme":"dark"}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:     "quiet-cfg-opt-in",
			Enabled:  BoolPtr(true),
			Quiet:    true,
			Settings: PackSettingsConfig{Enabled: BoolPtr(true)},
		}},
	}
	_, settingsPacks, err := resolveStrict(t, cfg, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(settingsPacks) != 1 || settingsPacks[0] != "quiet-cfg-opt-in" {
		t.Fatalf("quiet pack with Settings.Enabled: true must contribute settings, got %v", settingsPacks)
	}
}

// TestResolveProfile_SentinelSurvivesProfileAllowedTools verifies that the "*"
// sentinel in a profile's per-server allowed_tools passes through ResolveProfile
// and normalizeList unchanged. Expansion happens downstream in
// engine.buildMCPServers; the resolver must not filter or rewrite it.
func TestResolveProfile_SentinelSurvivesProfileAllowedTools(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "star", PackManifest{
		SchemaVersion: 2,
		Name:          "star",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"srv"},
	}, map[string]string{
		"mcp/srv.json": `{"name":"srv","command":["echo","hi"],"available_tools":["alpha","beta","gamma"]}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "star",
			Enabled: BoolPtr(true),
			MCP: map[string]MCPServerConfig{
				"srv": {Enabled: BoolPtr(true), AllowedTools: []string{"*"}},
			},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	srv, ok := packs[0].MCP["srv"]
	if !ok {
		t.Fatal("expected srv in MCP map")
	}
	if len(srv.AllowedTools) != 1 || srv.AllowedTools[0] != "*" {
		t.Fatalf("expected AllowedTools=[*] preserved through resolver, got %v", srv.AllowedTools)
	}
}

// TestResolveProfile_AlwaysAllowedSurvivesProfile verifies that an explicit
// always_allowed_tools list on a profile MCP server override flows through
// ResolveProfile and normalizeList into ResolvedMCPServer.AlwaysAllowedTools
// unchanged, and is independent of AllowedTools.
func TestResolveProfile_AlwaysAllowedSurvivesProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "always", PackManifest{
		SchemaVersion: 2,
		Name:          "always",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"srv"},
	}, map[string]string{
		"mcp/srv.json": `{"name":"srv","command":["echo","hi"],"available_tools":["alpha","beta","gamma"]}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "always",
			Enabled: BoolPtr(true),
			MCP: map[string]MCPServerConfig{
				"srv": {
					Enabled:            BoolPtr(true),
					AllowedTools:       []string{"alpha", "beta"},
					AlwaysAllowedTools: []string{"alpha"},
				},
			},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	srv := packs[0].MCP["srv"]
	if len(srv.AllowedTools) != 2 || srv.AllowedTools[0] != "alpha" || srv.AllowedTools[1] != "beta" {
		t.Fatalf("AllowedTools = %v, want [alpha beta]", srv.AllowedTools)
	}
	if len(srv.AlwaysAllowedTools) != 1 || srv.AlwaysAllowedTools[0] != "alpha" {
		t.Fatalf("AlwaysAllowedTools = %v, want [alpha]", srv.AlwaysAllowedTools)
	}
}

// TestResolveProfile_AlwaysAllowedSentinelSurvivesProfile verifies the "*"
// sentinel passes through unchanged on always_allowed_tools just like it does
// on allowed_tools — expansion happens downstream in engine.buildMCPServers.
func TestResolveProfile_AlwaysAllowedSentinelSurvivesProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "always-star", PackManifest{
		SchemaVersion: 2,
		Name:          "always-star",
		Version:       "1.0.0",
		Root:          ".",
		MCP:           []string{"srv"},
	}, map[string]string{
		"mcp/srv.json": `{"name":"srv","command":["echo","hi"],"available_tools":["read","write"]}`,
	})

	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "always-star",
			Enabled: BoolPtr(true),
			MCP: map[string]MCPServerConfig{
				"srv": {Enabled: BoolPtr(true), AlwaysAllowedTools: []string{"*"}},
			},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	srv := packs[0].MCP["srv"]
	if len(srv.AlwaysAllowedTools) != 1 || srv.AlwaysAllowedTools[0] != "*" {
		t.Fatalf("AlwaysAllowedTools = %v, want [*] preserved through resolver", srv.AlwaysAllowedTools)
	}
}

// TestResolveProfile_QuietPackEmptyExcludeExcludesAll verifies that quiet packs
// treat empty exclude: [] the same as omitted exclude (nothing included).
// Quiet packs only respond to explicit include — exclude is ignored.
func TestResolveProfile_QuietPackEmptyExcludeExcludesAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "quiet-pack", PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma"},
		MCP:           []string{},
	}, map[string]string{
		"rules/alpha.md": "---\nname: alpha\n---\nbody\n",
		"rules/beta.md":  "---\nname: beta\n---\nbody\n",
		"rules/gamma.md": "---\nname: gamma\n---\nbody\n",
	})

	empty := []string{}
	cfg := ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:    "quiet-pack",
			Enabled: BoolPtr(true),
			Quiet:   true,
			Rules:   VectorSelector{Exclude: &empty},
		}},
	}
	profilePath := filepath.Join(root, "profile.yaml")
	packs, _, err := resolveStrict(t, cfg, profilePath, root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(packs))
	}
	// quiet + exclude: [] should behave like quiet + exclude: nil → nothing.
	// The empty exclude list says "exclude nothing" — but on a quiet pack the
	// default is already "include nothing", so there's nothing to exclude from.
	// If this returns 3, the quiet semantics are broken for empty exclude.
	if len(packs[0].Rules) != 0 {
		t.Fatalf("expected 0 rules for quiet pack with empty exclude, got %d — "+
			"empty exclude bypasses quiet default and returns full inventory",
			len(packs[0].Rules))
	}
}

// TestResolveProfile_ContentPathsPackYAMLRoundTrip verifies that a pack
// installed with content_paths has its metadata survive a sync-config
// save/load cycle. The map[PackCategory]string type must serialize and
// deserialize correctly through YAML.
func TestResolveProfile_ContentPathsPackYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scPath := filepath.Join(dir, "sync-config.yaml")

	original := SyncConfig{
		SchemaVersion: SyncConfigSchemaVersion,
		Defaults: struct {
			Profile           string            `yaml:"profile"`
			Harnesses         []string          `yaml:"harnesses"`
			Scope             string            `yaml:"scope"`
			Registry          string            `yaml:"registry,omitempty"`
			RegistryURL       string            `yaml:"registry_url,omitempty"`
			CollisionStrategy CollisionStrategy `yaml:"collision_strategy,omitempty"`
		}{Profile: "default", Harnesses: []string{"claudecode"}, Scope: "global"},
		InstalledPacks: map[string]InstalledPackMeta{
			"ext-skills": {
				Origin:      "https://github.com/example/mono.git",
				Method:      MethodClone,
				InstalledAt: "2026-04-02T00:00:00Z",
				ContentPaths: map[domain.PackCategory]string{
					domain.CategorySkills: "tools/agent/skills",
					domain.CategoryRules:  "config/rules",
				},
			},
		},
	}

	if err := SaveSyncConfig(scPath, original); err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}

	loaded, err := LoadSyncConfig(scPath)
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}

	meta, ok := loaded.InstalledPacks["ext-skills"]
	if !ok {
		t.Fatal("ext-skills missing from round-tripped InstalledPacks")
	}
	if meta.ContentPaths[domain.CategorySkills] != "tools/agent/skills" {
		t.Fatalf("skills content path = %q, want tools/agent/skills",
			meta.ContentPaths[domain.CategorySkills])
	}
	if meta.ContentPaths[domain.CategoryRules] != "config/rules" {
		t.Fatalf("rules content path = %q, want config/rules",
			meta.ContentPaths[domain.CategoryRules])
	}
}

// ---------------------------------------------------------------------------
// User story tests
// ---------------------------------------------------------------------------

// "I installed two packs from different teams. My profile lists both.
// I expect rules from both to appear in the resolved profile."
func TestResolveProfile_TwoPacksComposeContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "team-ops", PackManifest{
		SchemaVersion: 2, Name: "team-ops", Version: "1", Root: ".",
		MCP: []string{},
	}, map[string]string{
		"rules/no-force-push.md":  "---\nname: no-force-push\n---\nbody\n",
		"rules/require-review.md": "---\nname: require-review\n---\nbody\n",
	})
	installPackForResolveTest(t, root, "team-sec", PackManifest{
		SchemaVersion: 2, Name: "team-sec", Version: "1", Root: ".",
		MCP: []string{},
	}, map[string]string{
		"rules/no-secrets.md": "---\nname: no-secrets\n---\nbody\n",
	})

	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team-ops"},
			{Name: "team-sec"},
		},
	}, filepath.Join(root, "p.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d packs, want 2", len(packs))
	}
	// team-ops contributes 2 rules, team-sec contributes 1.
	if len(packs[0].Rules) != 2 {
		t.Fatalf("team-ops rules = %d, want 2", len(packs[0].Rules))
	}
	if len(packs[1].Rules) != 1 {
		t.Fatalf("team-sec rules = %d, want 1", len(packs[1].Rules))
	}
}

// "I added a large catalog pack but marked it quiet. I only want 2 of its
// 5 skills. The other 3 should not appear, and its rules should be empty."
func TestResolveProfile_QuietCatalogSelectiveInclude(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "catalog", PackManifest{
		SchemaVersion: 2, Name: "catalog", Version: "2.0.0", Root: ".",
		Rules:  []string{"verbose-logging", "change-log"},
		Skills: []string{"deploy", "triage", "rollback", "debug", "migrate"},
		MCP:    []string{},
	}, map[string]string{
		"rules/verbose-logging.md": "---\nname: verbose-logging\n---\nb\n",
		"rules/change-log.md":      "---\nname: change-log\n---\nb\n",
		"skills/deploy/SKILL.md":   "---\nname: deploy\ndescription: d\n---\nb\n",
		"skills/triage/SKILL.md":   "---\nname: triage\ndescription: d\n---\nb\n",
		"skills/rollback/SKILL.md": "---\nname: rollback\ndescription: d\n---\nb\n",
		"skills/debug/SKILL.md":    "---\nname: debug\ndescription: d\n---\nb\n",
		"skills/migrate/SKILL.md":  "---\nname: migrate\ndescription: d\n---\nb\n",
	})

	include := []string{"deploy", "triage"}
	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:   "catalog",
			Quiet:  true,
			Skills: VectorSelector{Include: &include},
			// Rules: no selector → quiet means nothing.
		}},
	}, filepath.Join(root, "p.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs[0].Skills) != 2 {
		t.Fatalf("skills = %v, want [deploy triage]", packs[0].Skills)
	}
	if len(packs[0].Rules) != 0 {
		t.Fatalf("quiet pack with no rules selector should produce 0 rules, got %d", len(packs[0].Rules))
	}
}

// "I have two packs. I disabled one. Only the enabled pack's content appears —
// the disabled pack contributes no rules, no skills, no MCP servers."
func TestResolveProfile_DisabledPackContributesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "active", PackManifest{
		SchemaVersion: 2, Name: "active", Version: "1", Root: ".",
		Rules: []string{"keep-this"},
		MCP:   []string{},
	}, map[string]string{
		"rules/keep-this.md": "---\nname: keep-this\n---\nb\n",
	})
	installPackForResolveTest(t, root, "dormant", PackManifest{
		SchemaVersion: 2, Name: "dormant", Version: "1", Root: ".",
		Rules:  []string{"important-rule"},
		Skills: []string{"critical-skill"},
		MCP:    []string{"jira"},
	}, map[string]string{
		"rules/important-rule.md":        "---\nname: important-rule\n---\nb\n",
		"skills/critical-skill/SKILL.md": "---\nname: critical-skill\ndescription: d\n---\nb\n",
		"mcp/my-server.json":             `{"name":"jira","command":["echo"]}`,
	})

	packs, _, err := resolveStrict(t, ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "active"},
			{Name: "dormant", Enabled: BoolPtr(false)},
		},
	}, filepath.Join(root, "p.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 1 {
		t.Fatalf("expected 1 pack (active only), got %d", len(packs))
	}
	if packs[0].Name != "active" {
		t.Fatalf("surviving pack = %q, want active", packs[0].Name)
	}
}
