package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestProfileList_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "profile", "list", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestProfileList_FreshConfigDir_ShowsDefault(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// EnsureInit now auto-creates the default profile for any config dir,
	// so a fresh directory should show the default profile, not "No profiles found".
	stdout, stderr, code := runApp(t, "profile", "list", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "default") {
		t.Fatalf("expected default profile in output, got: %s", stdout)
	}
}

func TestProfileList_WithDefault(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "default.yaml"), []byte("schema_version: 6\npacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "dev.yaml"), []byte("schema_version: 6\npacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "profile", "list", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile list exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	output := stdout
	if !strings.Contains(output, "default (active)") {
		t.Fatalf("expected 'default (active)' in output, got: %s", output)
	}
	if !strings.Contains(output, "dev") {
		t.Fatalf("expected 'dev' in output, got: %s", output)
	}
}

func TestProfileShow_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "profile", "show", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("profile show --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestProfileShow_MissingProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "profile", "show", "nonexistent", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("profile show nonexistent exit=%d, want %d", code, cmdutil.ExitFail)
	}
}

func TestProfileIncludeExclude_CLI(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeProfileCLIPack(t, configDir)
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("schema_version: 2\npacks:\n  - name: core\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "profile", "exclude", "anti-slop", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile exclude exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Excluded rules/anti-slop from core in profile default") {
		t.Fatalf("stdout = %q", stdout)
	}
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Packs[0].Rules.Exclude == nil || !config.StringSlicesEqual(*cfg.Packs[0].Rules.Exclude, []string{"anti-slop"}) {
		t.Fatalf("rules.exclude = %+v, want [anti-slop]", cfg.Packs[0].Rules.Exclude)
	}

	_, stderr, code = runApp(t, "profile", "include", "anti-slop", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile include exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	cfg, err = config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Packs[0].Rules.Include != nil || cfg.Packs[0].Rules.Exclude != nil {
		t.Fatalf("rules selector = %+v, want default selector", cfg.Packs[0].Rules)
	}

	_, stderr, code = runApp(t, "profile", "exclude", "jira", "--kind", "mcp", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile exclude mcp exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	cfg, err = config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	jira, ok := cfg.Packs[0].MCP["jira"]
	if !ok || jira.Enabled == nil || *jira.Enabled {
		t.Fatalf("mcp.jira = %+v, want enabled false", cfg.Packs[0].MCP["jira"])
	}
}

func TestProfileSetAndUnsetParam_CLI(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profilesDir, "default.yaml")
	if err := os.WriteFile(profilePath, []byte("schema_version: 2\npacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "profile", "set-param", "default", "workspace", "team-alpha", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile set-param exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Params["workspace"] != "team-alpha" {
		t.Fatalf("workspace = %q", cfg.Params["workspace"])
	}

	_, stderr, code = runApp(t, "profile", "unset-param", "default", "workspace", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("profile unset-param exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	cfg, err = config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Params["workspace"]; ok {
		t.Fatalf("workspace should be unset: %+v", cfg.Params)
	}
}

func TestProfileRefs_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runApp(t, "profile", "refs", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("profile refs --help exit=%d, want %d; stderr=%s stdout=%s", code, cmdutil.ExitOK, stderr, stdout)
	}
}

func writeProfileCLIPack(t *testing.T, configDir string) {
	t.Helper()
	packDir := filepath.Join(configDir, "packs", "core")
	for _, path := range []string{
		filepath.Join(packDir, "rules", "anti-slop.md"),
		filepath.Join(packDir, "mcp", "jira.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "anti-slop.md"), []byte("# rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "jira.json"), []byte(`{"name":"jira","transport":"stdio","command":["true"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), config.PackManifest{
		SchemaVersion: 2,
		Name:          "core",
		Root:          ".",
		Rules:         []string{"anti-slop"},
		MCP:           []string{"jira"},
	}); err != nil {
		t.Fatal(err)
	}
}
