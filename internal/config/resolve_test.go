package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProfile_InstalledPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Set up installed pack at configDir/packs/snap/pack.json
	packRoot := filepath.Join(root, "packs", "snap")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatalf("mkdir pack root: %v", err)
	}
	manifest := PackManifest{
		SchemaVersion: 1,
		Name:          "snap",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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

	packs, _, err := ResolveProfile(cfg, profilePath, root)
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
	_, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
	if err == nil {
		t.Fatal("expected error for missing pack, got nil")
	}
}

func TestResolveProfile_VectorSelectorErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "base", PackManifest{
		SchemaVersion: 1,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/alpha.md": "---\nname: alpha\n---\nbody\n"})

	include := []string{"alpha"}
	exclude := []string{"alpha"}
	_, _, err := ResolveProfile(ProfileConfig{
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
	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{{
			Name:  "base",
			Rules: VectorSelector{Include: &unknown},
		}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `pack "base" rules include references unknown id "missing"`) {
		t.Fatalf("expected unknown include error, got %v", err)
	}

	_, _, err = ResolveProfile(ProfileConfig{
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

func TestResolveProfile_OverrideAndDuplicateErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installPackForResolveTest(t, root, "first", PackManifest{
		SchemaVersion: 1,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"jira": {}}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 1,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"jira": {}}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n", "mcp/jira.json": `{"name":"jira"}`})

	_, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second"}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `rules id "shared" appears in both`) {
		t.Fatalf("expected duplicate rules error, got %v", err)
	}

	// Override referencing a non-existent resource is an error (catches typos).
	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Overrides: Overrides{Rules: []string{"typo-rule"}}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `overrides.rules references "typo-rule"`) {
		t.Fatalf("expected unknown override error, got %v", err)
	}

	// Same for MCP overrides.
	_, _, err = ResolveProfile(ProfileConfig{
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
		SchemaVersion: 1,
		Name:          "first",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})
	installPackForResolveTest(t, root, "second", PackManifest{
		SchemaVersion: 1,
		Name:          "second",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, map[string]string{"rules/shared.md": "---\nname: shared\n---\nbody\n"})

	packs, settingsPack, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first"}, {Name: "second", Overrides: Overrides{Rules: []string{"shared"}}, Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("packs = %d, want 2", len(packs))
	}
	if settingsPack != "second" {
		t.Fatalf("settingsPack = %q, want %q", settingsPack, "second")
	}

	installPackForResolveTest(t, root, "third", PackManifest{
		SchemaVersion: 1,
		Name:          "third",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
	}, nil)
	_, _, err = ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs:         []PackEntry{{Name: "first", Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}, {Name: "third", Settings: PackSettingsConfig{Enabled: BoolPtr(true)}}},
	}, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `multiple packs have settings.enabled`) {
		t.Fatalf("expected multiple settings pack error, got %v", err)
	}
}

func TestResolveProfile_OverrideStripsEarlierPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both provide rule "shared" and MCP server "tracker".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 1,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared", "team-only"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"tracker": {}}},
	}, map[string]string{
		"rules/shared.md":    "---\nname: shared\n---\nteam version\n",
		"rules/team-only.md": "---\nname: team-only\n---\nbody\n",
		"mcp/tracker.json":   `{"name":"tracker"}`,
	})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 1,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared", "org-only"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"tracker": {}}},
	}, map[string]string{
		"rules/shared.md":   "---\nname: shared\n---\norg version\n",
		"rules/org-only.md": "---\nname: org-only\n---\nbody\n",
		"mcp/tracker.json":  `{"name":"tracker"}`,
	})

	// org declares overrides — its version of "shared" and "tracker" should win.
	packs, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team"},
			{Name: "org", Overrides: Overrides{Rules: []string{"shared"}, MCP: []string{"tracker"}}},
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
	if _, ok := teamPack.MCP["tracker"]; ok {
		t.Error("team pack should not have 'tracker' MCP after override")
	}

	// org pack should have "shared" and "tracker".
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
	if _, ok := orgPack.MCP["tracker"]; !ok {
		t.Error("org pack should have 'tracker' MCP")
	}
}

func TestResolveProfile_OverrideWinsRegardlessOfOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both provide rule "shared" and MCP server "tracker".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 1,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"tracker": {}}},
	}, map[string]string{
		"rules/shared.md":  "---\nname: shared\n---\nteam version\n",
		"mcp/tracker.json": `{"name":"tracker"}`,
	})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 1,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"shared"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"tracker": {}}},
	}, map[string]string{
		"rules/shared.md":  "---\nname: shared\n---\norg version\n",
		"mcp/tracker.json": `{"name":"tracker"}`,
	})

	// team declares override but is listed FIRST — should still win.
	packs, _, err := ResolveProfile(ProfileConfig{
		SchemaVersion: ProfileSchemaVersion,
		Packs: []PackEntry{
			{Name: "team", Overrides: Overrides{Rules: []string{"shared"}, MCP: []string{"tracker"}}},
			{Name: "org"},
		},
	}, filepath.Join(root, "profile.yaml"), root)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	// team (first, override owner) keeps "shared" and "tracker".
	if len(packs[0].Rules) != 1 || packs[0].Rules[0] != "shared" {
		t.Errorf("team rules = %v, want [shared]", packs[0].Rules)
	}
	if _, ok := packs[0].MCP["tracker"]; !ok {
		t.Error("team should have 'tracker' MCP")
	}
	// org (second, not owner) has them stripped.
	for _, r := range packs[1].Rules {
		if r == "shared" {
			t.Error("org should not have 'shared' rule")
		}
	}
	if _, ok := packs[1].MCP["tracker"]; ok {
		t.Error("org should not have 'tracker' MCP")
	}
}

func TestResolveProfile_ExcludePreventsDuplicate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Two packs both define MCP servers "alpha" and "beta".
	installPackForResolveTest(t, root, "team", PackManifest{
		SchemaVersion: 1,
		Name:          "team",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"alpha": {}, "beta": {}}},
	}, map[string]string{"mcp/alpha.json": `{"name":"alpha"}`, "mcp/beta.json": `{"name":"beta"}`})
	installPackForResolveTest(t, root, "org", PackManifest{
		SchemaVersion: 1,
		Name:          "org",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{"alpha": {}, "beta": {}}},
	}, map[string]string{"mcp/alpha.json": `{"name":"alpha"}`, "mcp/beta.json": `{"name":"beta"}`})

	// org disables both via enabled:false — no conflict.
	packs, _, err := ResolveProfile(ProfileConfig{
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
		SchemaVersion: 1,
		Name:          "base",
		Version:       "1",
		Root:          ".",
		MCP: MCPPack{Servers: map[string]MCPDefaults{
			"jira": {DefaultAllowedTools: []string{"get_issue"}},
		}},
	}, map[string]string{"mcp/jira.json": `{"name":"jira"}`})

	_, _, err := ResolveProfile(ProfileConfig{
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
		SchemaVersion: 1,
		Name:          "disco",
		Version:       "1",
		Root:          ".",
		// Rules, Agents, Workflows, Skills are all nil
		MCP: MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "filtered",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "globs",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "globex",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "mixed",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma", "anti-slop"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "nomatch",
		Version:       "1",
		Root:          ".",
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	packs, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
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
		SchemaVersion: 1,
		Name:          "exacterr",
		Version:       "1",
		Root:          ".",
		Rules:         []string{"alpha"},
		MCP:           MCPPack{Servers: map[string]MCPDefaults{}},
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
	_, _, err := ResolveProfile(cfg, filepath.Join(root, "profile.yaml"), root)
	if err == nil || !strings.Contains(err.Error(), `unknown id "nonexistent"`) {
		t.Fatalf("expected unknown id error, got %v", err)
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
