package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestConfigEnvSetGet(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if _, stderr, code := runApp(t, "config", "env", "set", "API_TOKEN", "secret", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("config env set exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	stdout, stderr, code := runApp(t, "config", "env", "get", "API_TOKEN", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env get exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "secret" {
		t.Fatalf("config env get stdout = %q, want secret", stdout)
	}
}

func TestConfigEnvPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "env", "path", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env path exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	want := filepath.Join(configDir, ".env")
	if strings.TrimSpace(stdout) != want {
		t.Fatalf("config env path stdout = %q, want %q", stdout, want)
	}
}

func TestConfigEnvListJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if _, stderr, code := runApp(t, "config", "env", "set", "API_TOKEN", "secret", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("config env set exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	stdout, stderr, code := runApp(t, "config", "env", "list", "--json", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got struct {
		Path    string `json:"path"`
		Entries []struct {
			Key    string `json:"key"`
			Value  string `json:"value,omitempty"`
			Length int    `json:"length"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal config env list JSON: %v\nstdout=%s", err, stdout)
	}
	if got.Path != filepath.Join(configDir, ".env") {
		t.Fatalf("path = %q, want config .env", got.Path)
	}
	if len(got.Entries) != 1 || got.Entries[0].Key != "API_TOKEN" || got.Entries[0].Value != "" || got.Entries[0].Length != len("secret") {
		t.Fatalf("entries = %+v, want masked API_TOKEN with length", got.Entries)
	}
}

func TestConfigSetAutoSyncTrue(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "auto_sync", "true", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set auto_sync exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.auto_sync to true") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if !syncCfg.Defaults.AutoSync {
		t.Fatal("defaults.auto_sync = false, want true")
	}

	stdout, stderr, code = runApp(t, "config", "defaults", "get", "auto_sync", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get auto_sync exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "true" {
		t.Fatalf("config get auto_sync stdout = %q, want true", stdout)
	}
}

func TestConfigSetAutoSyncFalseAlias(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if _, stderr, code := runApp(t, "config", "defaults", "set", "auto_sync", "true", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("config defaults set auto_sync exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	stdout, stderr, code := runApp(t, "config", "defaults", "set", "auto-sync", "false", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set auto-sync exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.auto_sync to false") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if syncCfg.Defaults.AutoSync {
		t.Fatal("defaults.auto_sync = true, want false")
	}

	content, err := os.ReadFile(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("read sync-config: %v", err)
	}
	if !strings.Contains(string(content), "auto_sync: false") {
		t.Fatalf("sync-config = %q, want visible auto_sync: false", string(content))
	}
}

func TestConfigSetUnknownSettingReturnsUsage(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "unknown", "true", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set unknown exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "unknown config setting") {
		t.Fatalf("stderr = %q, want unknown setting error", stderr)
	}
}

func TestConfigSetInvalidBoolReturnsUsage(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "auto_sync", "maybe", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set invalid bool exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "invalid boolean value") {
		t.Fatalf("stderr = %q, want invalid boolean error", stderr)
	}
}

func TestConfigSetBoolRejectsYesNoAliases(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "auto_sync", "yes", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set yes exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "invalid boolean value") {
		t.Fatalf("stderr = %q, want invalid boolean error", stderr)
	}
}

func TestConfigGetAutoSyncDefaultsFalse(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "get", "defaults.auto_sync", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get auto_sync exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "false" {
		t.Fatalf("config get auto_sync stdout = %q, want false", stdout)
	}
}

func TestConfigDefaultsSetAndGetProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	if _, stderr, code := runApp(t, "profile", "create", "team", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("profile create exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "profile", "team", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set profile exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.profile to team") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	stdout, stderr, code = runApp(t, "config", "defaults", "get", "profile", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get profile exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "team" {
		t.Fatalf("config defaults get profile stdout = %q, want team", stdout)
	}
}

func TestConfigDefaultsSetProfileRequiresExistingProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "profile", "missing", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("config defaults set missing profile exit=%d, want %d; stderr=%s", code, cmdutil.ExitFail, stderr)
	}
	if !strings.Contains(stderr, "profile \"missing\" does not exist") {
		t.Fatalf("stderr = %q, want missing profile error", stderr)
	}
}

func TestConfigDefaultsSetProfileReportsMissingPacksWithoutAutoSync(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	syncCfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	syncCfg.Defaults.AutoSync = true
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), syncCfg); err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	profilePath := filepath.Join(profilesDir, "team.yaml")
	if err := os.WriteFile(profilePath, []byte("schema_version: 2\npacks:\n  - name: missing-pack\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "profile", "team", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set profile exit=%d, want %d; stdout=%s stderr=%s", code, cmdutil.ExitOK, stdout, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.profile to team") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}
	if !strings.Contains(stdout, "1 pack(s) not installed: missing-pack") {
		t.Fatalf("stdout = %q, want missing-pack warning", stdout)
	}
	if strings.Contains(stdout, "auto-sync:") {
		t.Fatalf("stdout = %q, auto-sync should be suppressed while packs are missing", stdout)
	}
}

func TestConfigDefaultsSetAndGetHarnesses(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "harnesses", "codex,opencode", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set harnesses exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.harnesses to codex,opencode") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	stdout, stderr, code = runApp(t, "config", "defaults", "get", "harnesses", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get harnesses exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "codex,opencode" {
		t.Fatalf("config defaults get harnesses stdout = %q, want codex,opencode", stdout)
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatalf("LoadSyncConfig: %v", err)
	}
	if strings.Join(syncCfg.Defaults.Harnesses, ",") != "codex,opencode" {
		t.Fatalf("defaults.harnesses = %v, want codex,opencode", syncCfg.Defaults.Harnesses)
	}
}

func TestConfigDefaultsSetHarnessesAllExpands(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "harnesses", "all", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set harnesses all exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.harnesses to cline,claudecode,codex,opencode") {
		t.Fatalf("stdout = %q, want expanded all harnesses", stdout)
	}
}

func TestConfigDefaultsSetHarnessesRejectsUnknown(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "harnesses", "codex,ghost", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set bad harness exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "unknown harness") {
		t.Fatalf("stderr = %q, want unknown harness error", stderr)
	}
}

func TestConfigDefaultsSetAndGetScope(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "scope", "project", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set scope exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.scope to project") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	stdout, stderr, code = runApp(t, "config", "defaults", "get", "defaults.scope", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get scope exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "project" {
		t.Fatalf("config defaults get scope stdout = %q, want project", stdout)
	}
}

func TestConfigDefaultsSetScopeRejectsUnknown(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "scope", "workspace", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set bad scope exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "unknown scope") {
		t.Fatalf("stderr = %q, want unknown scope error", stderr)
	}
}

func TestConfigDefaultsSetAndGetCollisionStrategy(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "defaults", "set", "collision_strategy", "first-wins", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults set collision_strategy exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, "Set defaults.collision_strategy to first-wins") {
		t.Fatalf("stdout = %q, want confirmation", stdout)
	}

	stdout, stderr, code = runApp(t, "config", "defaults", "get", "collision-strategy", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config defaults get collision_strategy exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "first-wins" {
		t.Fatalf("config defaults get collision_strategy stdout = %q, want first-wins", stdout)
	}
}

func TestConfigDefaultsSetCollisionStrategyRejectsUnknown(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "config", "defaults", "set", "collision_strategy", "merge", "--config-dir", configDir)
	if code != cmdutil.ExitUsage {
		t.Fatalf("config defaults set bad collision_strategy exit=%d, want %d; stderr=%s", code, cmdutil.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "unknown collision_strategy") {
		t.Fatalf("stderr = %q, want unknown collision_strategy error", stderr)
	}
}
