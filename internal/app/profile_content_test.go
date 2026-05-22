package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestProfileContentExcludeThenIncludeNormalRule(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "core", config.PackManifest{Rules: []string{"anti-slop", "show-work"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "core"}},
	})

	result, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"anti-slop"}})
	if err != nil {
		t.Fatalf("ProfileContentExclude: %v", err)
	}
	if !result.Changed {
		t.Fatal("exclude should report a profile change")
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Rules.Exclude == nil || !config.StringSlicesEqual(*cfg.Packs[0].Rules.Exclude, []string{"anti-slop"}) {
		t.Fatalf("rules.exclude = %+v, want [anti-slop]", cfg.Packs[0].Rules.Exclude)
	}

	result, err = ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"anti-slop"}})
	if err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	if !result.Changed {
		t.Fatal("include should report a profile change")
	}
	cfg = loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Rules.Include != nil || cfg.Packs[0].Rules.Exclude != nil {
		t.Fatalf("rules selector = %+v, want default selector", cfg.Packs[0].Rules)
	}
}

func TestProfileContentQuietPackIncludeAndExclude(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "catalog", config.PackManifest{Skills: []string{"jira", "triage"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "catalog", Quiet: true}},
	})

	if _, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Skills.Include == nil || !config.StringSlicesEqual(*cfg.Packs[0].Skills.Include, []string{"jira"}) {
		t.Fatalf("skills.include = %+v, want [jira]", cfg.Packs[0].Skills.Include)
	}

	if _, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentExclude: %v", err)
	}
	cfg = loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Skills.Include != nil || cfg.Packs[0].Skills.Exclude != nil {
		t.Fatalf("quiet skills selector = %+v, want default quiet selector", cfg.Packs[0].Skills)
	}
}

func TestProfileContentEmptyIncludeUsesProfileDefaults(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "core", config.PackManifest{Rules: []string{"anti-slop", "show-work"}})
	empty := []string{}
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name:  "core",
			Rules: config.VectorSelector{Include: &empty},
		}},
	})

	if _, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"anti-slop"}}); err != nil {
		t.Fatalf("ProfileContentExclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Rules.Exclude == nil || !config.StringSlicesEqual(*cfg.Packs[0].Rules.Exclude, []string{"anti-slop"}) {
		t.Fatalf("rules.exclude = %+v, want [anti-slop]", cfg.Packs[0].Rules.Exclude)
	}
}

func TestProfileContentTogglesMCPServer(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{MCP: []string{"jira", "slack"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{{Name: "tools"}},
	})

	if _, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentExclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	jira, ok := cfg.Packs[0].MCP["jira"]
	if !ok || jira.Enabled == nil || *jira.Enabled {
		t.Fatalf("mcp.jira = %+v, want enabled false", cfg.Packs[0].MCP["jira"])
	}

	if _, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	cfg = loadProfileContentProfile(t, configDir, "default")
	if len(cfg.Packs[0].MCP) != 0 {
		t.Fatalf("mcp map = %+v, want cleared default selection", cfg.Packs[0].MCP)
	}
}

func TestProfileContentMCPExcludesPreserveToolPolicy(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{MCP: []string{"jira"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name: "tools",
			MCP: map[string]config.MCPServerConfig{
				"jira": {AllowedTools: []string{"get_issue"}},
			},
		}},
	})

	if _, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentExclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	jira := cfg.Packs[0].MCP["jira"]
	if jira.Enabled == nil || *jira.Enabled {
		t.Fatalf("mcp.jira.enabled = %+v, want false", jira.Enabled)
	}
	if !config.StringSlicesEqual(jira.AllowedTools, []string{"get_issue"}) {
		t.Fatalf("mcp.jira.allowed_tools = %v, want [get_issue]", jira.AllowedTools)
	}
}

func TestProfileContentMCPIncludeAddsMissingServerToInclusiveMap(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{MCP: []string{"jira", "slack"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name: "tools",
			MCP: map[string]config.MCPServerConfig{
				"slack": {Enabled: config.BoolPtr(true)},
			},
		}},
	})

	result, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}})
	if err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	if !result.Changed {
		t.Fatal("include should report a profile change")
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	jira, ok := cfg.Packs[0].MCP["jira"]
	if !ok || jira.Enabled == nil || !*jira.Enabled {
		t.Fatalf("mcp.jira = %+v, want enabled true", cfg.Packs[0].MCP["jira"])
	}
	slack, ok := cfg.Packs[0].MCP["slack"]
	if !ok || slack.Enabled == nil || !*slack.Enabled {
		t.Fatalf("mcp.slack = %+v, want preserved enabled true", cfg.Packs[0].MCP["slack"])
	}
}

func TestProfileContentMCPIncludeDisabledServerInInclusiveMap(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{MCP: []string{"jira", "slack"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name: "tools",
			MCP: map[string]config.MCPServerConfig{
				"jira":  {Enabled: config.BoolPtr(false)},
				"slack": {Enabled: config.BoolPtr(true)},
			},
		}},
	})

	if _, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}}); err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	jira := cfg.Packs[0].MCP["jira"]
	if jira.Enabled == nil || !*jira.Enabled {
		t.Fatalf("mcp.jira = %+v, want enabled true", jira)
	}
}

func TestProfileContentIncludeHookClearsExplicitOptOut(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "hooks", config.PackManifest{Hooks: []string{"datetime-injector", "audit-log"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name:  "hooks",
			Hooks: config.HookSelector{Enabled: config.BoolPtr(false)},
		}},
	})

	if _, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"datetime-injector"}}); err != nil {
		t.Fatalf("ProfileContentInclude: %v", err)
	}
	cfg := loadProfileContentProfile(t, configDir, "default")
	if cfg.Packs[0].Hooks.Enabled != nil {
		t.Fatalf("hooks.enabled = %v, want nil", *cfg.Packs[0].Hooks.Enabled)
	}
	if cfg.Packs[0].Hooks.Include == nil || !config.StringSlicesEqual(*cfg.Packs[0].Hooks.Include, []string{"datetime-injector"}) {
		t.Fatalf("hooks.include = %+v, want [datetime-injector]", cfg.Packs[0].Hooks.Include)
	}
}

func TestProfileContentAmbiguousIDRequiresFilter(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "rules-pack", config.PackManifest{Rules: []string{"jira"}})
	writeProfileContentPack(t, configDir, "mcp-pack", config.PackManifest{MCP: []string{"jira"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{
			{Name: "rules-pack"},
			{Name: "mcp-pack"},
		},
	})

	_, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}})
	if err == nil {
		t.Fatal("ProfileContentInclude should reject ambiguous IDs")
	}
	msg := err.Error()
	for _, want := range []string{`"jira" matches multiple profile entries`, "rules/jira from rules-pack", "mcp/jira from mcp-pack", "Use --kind or --pack"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
}

func TestProfileContentSuggestsInstalledPackOutsideProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{MCP: []string{"jira"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{},
	})

	_, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}})
	if err == nil {
		t.Fatal("ProfileContentInclude should report missing profile content")
	}
	msg := err.Error()
	for _, want := range []string{"matching installed content", "mcp/jira from tools", "aipack pack add tools --profile default"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
}

func TestProfileContentRejectsDisabledPackMatches(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileContentPack(t, configDir, "tools", config.PackManifest{Skills: []string{"jira"}})
	writeProfileContentProfile(t, configDir, "default", config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs: []config.PackEntry{{
			Name:    "tools",
			Enabled: config.BoolPtr(false),
		}},
	})

	_, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: configDir, ProfileName: "default", IDs: []string{"jira"}})
	if err == nil {
		t.Fatal("ProfileContentInclude should reject disabled pack matches")
	}
	msg := err.Error()
	for _, want := range []string{"disabled pack entries", "skills/jira from tools", "aipack pack enable tools --profile default"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
}

func writeProfileContentPack(t *testing.T, configDir, name string, manifest config.PackManifest) {
	t.Helper()
	packDir := filepath.Join(configDir, "packs", name)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion = 2
	manifest.Name = name
	manifest.Root = "."
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	for _, id := range manifest.Rules {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryRules.PrimaryRelPath(id))), "# rule\n")
	}
	for _, id := range manifest.Agents {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryAgents.PrimaryRelPath(id))), "# agent\n")
	}
	for _, id := range manifest.Workflows {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryWorkflows.PrimaryRelPath(id))), "# workflow\n")
	}
	for _, id := range manifest.Skills {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategorySkills.PrimaryRelPath(id))), "---\nname: "+id+"\ndescription: Test skill\n---\n")
	}
	for _, id := range manifest.Hooks {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryHooks.PrimaryRelPath(id))), "events: []\n")
	}
	for _, id := range manifest.Plugins {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryPlugins.PrimaryRelPath(id))), `{"name":"`+id+`"}`)
	}
	for _, id := range manifest.MCP {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(domain.CategoryMCP.PrimaryRelPath(id))), `{"name":"`+id+`","transport":"stdio","command":["true"]}`)
	}
}

func writeProfileContentProfile(t *testing.T, configDir, name string, profile config.ProfileConfig) {
	t.Helper()
	if err := ProfileSave(ProfileSaveRequest{ConfigDir: configDir, Name: name, Config: profile}); err != nil {
		t.Fatal(err)
	}
}

func loadProfileContentProfile(t *testing.T, configDir, name string) config.ProfileConfig {
	t.Helper()
	cfg, err := config.LoadProfile(filepath.Join(configDir, "profiles", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
