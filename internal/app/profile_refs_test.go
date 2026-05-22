package app

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

func TestProfileRefsReportsParamsEnvDotEnvAndDefaults(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, ".env"), "API_TOKEN=from-dotenv\n")
	packDir := filepath.Join(configDir, "packs", "refs-pack")
	writePackManifest(t, packDir, "refs-pack")
	writeFile(t, filepath.Join(packDir, "mcp", "srv.json"), `{
  "name": "srv",
  "transport": "stdio",
  "command": ["tool", "--workspace", "{params.workspace}", "--mode", "{params.mode:-prod}", "--token", "{env:API_TOKEN}", "--api-url", "{env:API_BASE_URL:-https://api.example.com}", "--missing", "{env:MISSING_TOKEN}"],
  "available_tools": ["ping"]
}`)
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.MCP = []string{"srv"}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, `schema_version: 2
params:
  workspace: default
packs:
  - name: refs-pack
`)

	result, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err != nil {
		t.Fatalf("ProfileRefs: %v", err)
	}
	status := map[string]string{}
	defaults := map[string]bool{}
	for _, ref := range result.Refs {
		status[ref.Display] = ref.Status
		defaults[ref.Display] = ref.HasDefault
	}
	if status["{params.workspace}"] != "set" {
		t.Fatalf("params.workspace status = %q, want set; refs=%+v", status["{params.workspace}"], result.Refs)
	}
	if status["{params.mode:-prod}"] != "defaulted" {
		t.Fatalf("params.mode default status = %q, want defaulted; refs=%+v", status["{params.mode:-prod}"], result.Refs)
	}
	if !defaults["{params.mode:-prod}"] {
		t.Fatalf("params.mode ref should retain structured default metadata; refs=%+v", result.Refs)
	}
	if status["{env:API_TOKEN}"] != "dotenv" {
		t.Fatalf("API_TOKEN status = %q, want dotenv; refs=%+v", status["{env:API_TOKEN}"], result.Refs)
	}
	if status["{env:API_BASE_URL:-https://api.example.com}"] != "defaulted" {
		t.Fatalf("API_BASE_URL default status = %q, want defaulted; refs=%+v", status["{env:API_BASE_URL:-https://api.example.com}"], result.Refs)
	}
	if !defaults["{env:API_BASE_URL:-https://api.example.com}"] {
		t.Fatalf("API_BASE_URL ref should retain structured default metadata; refs=%+v", result.Refs)
	}
	if status["{env:MISSING_TOKEN}"] != "missing" {
		t.Fatalf("MISSING_TOKEN status = %q, want missing; refs=%+v", status["{env:MISSING_TOKEN}"], result.Refs)
	}
}

func TestProfileRefsTreatsEmptyDotEnvValueAsPresent(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, ".env"), "API_TOKEN=\n")
	packDir := filepath.Join(configDir, "packs", "refs-pack")
	writePackManifest(t, packDir, "refs-pack")
	writeFile(t, filepath.Join(packDir, "mcp", "srv.json"), `{
  "name": "srv",
  "transport": "stdio",
  "command": ["tool", "--token", "{env:API_TOKEN}"],
  "available_tools": ["ping"]
}`)
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.MCP = []string{"srv"}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, `schema_version: 2
packs:
  - name: refs-pack
`)

	result, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err != nil {
		t.Fatalf("ProfileRefs: %v", err)
	}
	for _, ref := range result.Refs {
		if ref.Display == "{env:API_TOKEN}" && ref.Status == "dotenv" {
			return
		}
	}
	t.Fatalf("API_TOKEN should be reported as dotenv; refs=%+v", result.Refs)
}

func TestProfileRefsReturnsMalformedEnvRefError(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(configDir, "packs", "refs-pack")
	writePackManifest(t, packDir, "refs-pack")
	writeFile(t, filepath.Join(packDir, "mcp", "srv.json"), `{
  "name": "srv",
  "transport": "stdio",
  "command": ["tool", "{env:BROKEN"],
  "available_tools": ["ping"]
}`)
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.MCP = []string{"srv"}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, `schema_version: 2
packs:
  - name: refs-pack
`)

	_, err = ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err == nil || !strings.Contains(err.Error(), "unterminated env reference") {
		t.Fatalf("ProfileRefs error = %v, want unterminated env reference", err)
	}
}

func TestProfileRefsExplicitProfileHonorsSyncConfigCollisionStrategy(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	syncCfg := config.SyncConfig{SchemaVersion: 1}
	syncCfg.Defaults.CollisionStrategy = config.CollisionError
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), syncCfg); err != nil {
		t.Fatal(err)
	}

	for _, pack := range []string{"pack-a", "pack-b"} {
		packDir := filepath.Join(configDir, "packs", pack)
		writePackManifest(t, packDir, pack)
		writeFile(t, filepath.Join(packDir, "rules", "shared.md"), "---\nname: shared\ndescription: shared rule\n---\n")
	}
	writeFile(t, filepath.Join(configDir, "profiles", "prod.yaml"), `schema_version: 2
packs:
  - name: pack-a
  - name: pack-b
`)

	_, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "prod"})
	if err == nil || !strings.Contains(err.Error(), "collision(s)") {
		t.Fatalf("ProfileRefs error = %v, want collision error from sync-config defaults", err)
	}
}

func TestProfileSetAndUnsetParam(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, "schema_version: 2\npacks: []\n")

	if err := ProfileSetParam(ProfileParamRequest{ConfigDir: configDir, ProfileName: "default", Key: "workspace", Value: "default"}); err != nil {
		t.Fatalf("ProfileSetParam: %v", err)
	}
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Params["workspace"] != "default" {
		t.Fatalf("workspace = %q", cfg.Params["workspace"])
	}
	if err := ProfileUnsetParam(ProfileParamRequest{ConfigDir: configDir, ProfileName: "default", Key: "workspace"}); err != nil {
		t.Fatalf("ProfileUnsetParam: %v", err)
	}
	cfg, err = config.LoadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Params["workspace"]; ok {
		t.Fatalf("workspace should be unset: %+v", cfg.Params)
	}
}

// TestProfileSetParam_WarnsForBundledProfile verifies that mutating a profile
// installed from a pack surfaces the warnBundledProfileEdit hint to stdout, so
// the CHANGELOG claim "CLI profile mutations and the TUI warn before editing
// profiles installed from bundled pack content" holds for param edits.
func TestProfileSetParam_WarnsForBundledProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilePath := filepath.Join(configDir, "profiles", "team.yaml")
	writeFile(t, profilePath, "schema_version: 2\npacks: []\n")

	// Install a pack that declares this profile as bundled content.
	packDir := filepath.Join(configDir, "packs", "owner-pack")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"schema_version":2,"name":"owner-pack","version":"1.0.0","root":".","profiles":["team"]}`)

	var out bytes.Buffer
	if err := ProfileSetParam(ProfileParamRequest{
		ConfigDir: configDir, ProfileName: "team", Key: "workspace", Value: "default", Stdout: &out,
	}); err != nil {
		t.Fatalf("ProfileSetParam: %v", err)
	}
	if !strings.Contains(out.String(), "owner-pack") {
		t.Fatalf("expected bundled-profile warning citing owner-pack, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "duplicate") {
		t.Fatalf("expected warning to mention duplicate, got: %q", out.String())
	}

	out.Reset()
	if err := ProfileUnsetParam(ProfileParamRequest{
		ConfigDir: configDir, ProfileName: "team", Key: "workspace", Stdout: &out,
	}); err != nil {
		t.Fatalf("ProfileUnsetParam: %v", err)
	}
	if !strings.Contains(out.String(), "owner-pack") {
		t.Fatalf("expected bundled-profile warning on unset, got: %q", out.String())
	}
}

// TestProfileRefs_ScansContributingSettingsFiles verifies that {params.*} and
// {env:*} refs inside harness settings files (configs/<harness>/<file>) are
// reported alongside MCP-server refs. Without this coverage the settings-file
// scanning code path in ProfileRefs is dead — only the MCP path was exercised
// before.
func TestProfileRefs_ScansContributingSettingsFiles(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, ".env"), "PROXY_URL=https://proxy.example.com\n")

	packDir := filepath.Join(configDir, "packs", "settings-pack")
	writePackManifest(t, packDir, "settings-pack")
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Configs.HarnessSettings = map[string][]string{
		"codex": {"config.toml"},
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(packDir, "configs", "codex", "config.toml"),
		`workspace = "{params.workspace}"
fallback_workspace = "{params.default_workspace:-default}"
proxy = "{env:PROXY_URL}"
trace_endpoint = "{env:UNSET_TRACE_ENDPOINT}"
`)

	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, `schema_version: 2
params:
  workspace: default
packs:
  - name: settings-pack
`)

	result, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err != nil {
		t.Fatalf("ProfileRefs: %v", err)
	}
	want := map[string]string{
		"{params.workspace}":                  "set",
		"{params.default_workspace:-default}": "defaulted",
		"{env:PROXY_URL}":                     "dotenv",
		"{env:UNSET_TRACE_ENDPOINT}":          "missing",
	}
	got := map[string]string{}
	settingsLocations := map[string]string{}
	for _, ref := range result.Refs {
		got[ref.Display] = ref.Status
		if strings.HasPrefix(ref.Location, "configs.") {
			settingsLocations[ref.Display] = ref.Location
		}
	}
	for display, wantStatus := range want {
		if got[display] != wantStatus {
			t.Fatalf("%s status = %q, want %q; refs=%+v", display, got[display], wantStatus, result.Refs)
		}
	}
	for display := range want {
		if _, ok := settingsLocations[display]; !ok {
			t.Fatalf("%s should be reported with a configs.* location, got %q", display, settingsLocations[display])
		}
	}
}

// TestProfileRefs_SkipsDisabledSettings confirms that a pack opting out of
// settings (settings.enabled: false) does NOT contribute settings-file refs.
func TestProfileRefs_SkipsDisabledSettings(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(configDir, "packs", "off-pack")
	writePackManifest(t, packDir, "off-pack")
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Configs.HarnessSettings = map[string][]string{"codex": {"config.toml"}}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(packDir, "configs", "codex", "config.toml"),
		`workspace = "{params.never_scanned}"`)

	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, `schema_version: 2
packs:
  - name: off-pack
    settings:
      enabled: false
`)

	result, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err != nil {
		t.Fatalf("ProfileRefs: %v", err)
	}
	for _, ref := range result.Refs {
		if ref.Display == "{params.never_scanned}" {
			t.Fatalf("ref from disabled settings pack should not appear: %+v", ref)
		}
	}
}

func TestProfileRefs_ScansHookDescriptorOnly(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(configDir, "packs", "hook-pack")
	writePackManifest(t, packDir, "hook-pack")
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Hooks = []string{"tool-audit"}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(packDir, "hooks", "tool-audit", "HOOK.yaml"), `name: tool-audit
events:
  - on: tool.after
    handler:
      command: "python3 {hook:root}/handler.py --token {env:HOOK_TOKEN}"
`)
	writeFile(t, filepath.Join(packDir, "hooks", "tool-audit", "handler.py"), `{env:SHOULD_NOT_SCAN_ASSET}`)
	writeFile(t, filepath.Join(configDir, "profiles", "default.yaml"), `schema_version: 2
packs:
  - name: hook-pack
`)

	result, err := ProfileRefs(ProfileRefsRequest{ConfigDir: configDir, ProfileName: "default"})
	if err != nil {
		t.Fatalf("ProfileRefs: %v", err)
	}
	seen := map[string]ProfileRef{}
	for _, ref := range result.Refs {
		seen[ref.Display] = ref
	}
	if seen["{env:HOOK_TOKEN}"].Location != "hooks.tool-audit" {
		t.Fatalf("HOOK_TOKEN ref = %+v, want hooks.tool-audit location; refs=%+v", seen["{env:HOOK_TOKEN}"], result.Refs)
	}
	if _, ok := seen["{env:SHOULD_NOT_SCAN_ASSET}"]; ok {
		t.Fatalf("hook asset refs should not be scanned: %+v", result.Refs)
	}
}

// TestProfileSetParam_NoWarnForLocalProfile confirms the warning fires only
// for bundled profiles, so users editing their own profiles aren't nagged.
func TestProfileSetParam_NoWarnForLocalProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	writeFile(t, profilePath, "schema_version: 2\npacks: []\n")

	var out bytes.Buffer
	if err := ProfileSetParam(ProfileParamRequest{
		ConfigDir: configDir, ProfileName: "default", Key: "workspace", Value: "default", Stdout: &out,
	}); err != nil {
		t.Fatalf("ProfileSetParam: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no warning for non-bundled profile, got: %q", out.String())
	}
}
