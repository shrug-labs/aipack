package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestDoctor_JSON_HappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "aipack")
	configPath := filepath.Join(configDir, "sync-config.yaml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write sync-config: %v", err)
	}

	packDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755); err != nil {
		t.Fatalf("mkdir pack mcp dir: %v", err)
	}
	packManifest := []byte(`{
  "schema_version": 1,
  "name": "test-pack",
  "version": "0",
  "root": ".",
  "rules": [],
  "agents": [],
  "workflows": [],
  "skills": [],
  "mcp": {
    "servers": {
      "bitbucket": {"default_allowed_tools": []},
      "atlassian": {"default_allowed_tools": []}
    }
  },
  "configs": {"harness_settings": {}}
}`)
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), packManifest, 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	nodePath := filepath.Join(packDir, "bin", "node")
	uvxPath := filepath.Join(packDir, "bin", "uvx")
	serverPath := filepath.Join(packDir, "bitbucket-mcp", "build", "index.js")
	for _, p := range []string{nodePath, uvxPath, serverPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	if err := os.WriteFile(filepath.Join(packDir, "mcp", "bitbucket.json"), []byte(`{
  "name": "bitbucket",
  "transport": "stdio",
  "timeout": 300,
  "command": [
    "`+escapeJSON(nodePath)+`",
    "`+escapeJSON(serverPath)+`"
  ],
  "env": {
    "BITBUCKET_URL": "{global.bitbucket_url}",
    "BITBUCKET_TOKEN": "{env:BITBUCKET_TOKEN}"
  },
  "available_tools": []
}`), 0o644); err != nil {
		t.Fatalf("write bitbucket inventory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "atlassian.json"), []byte(`{
  "name": "atlassian",
  "transport": "stdio",
  "timeout": 300,
  "command": [
    "`+escapeJSON(uvxPath)+`",
    "--version"
  ],
  "env": {},
  "available_tools": []
}`), 0o644); err != nil {
		t.Fatalf("write atlassian inventory: %v", err)
	}

	// Install pack at configDir/packs/local/
	installedPackDir := filepath.Join(configDir, "packs", "local")
	if err := os.MkdirAll(filepath.Dir(installedPackDir), 0o755); err != nil {
		t.Fatalf("mkdir installed packs: %v", err)
	}
	if err := os.Symlink(packDir, installedPackDir); err != nil {
		t.Fatalf("symlink pack: %v", err)
	}

	profilePath := filepath.Join(t.TempDir(), "profile.yaml")
	profile := []byte("" +
		"schema_version: 6\n" +
		"globals:\n" +
		"  bitbucket_url: https://example.invalid\n" +
		"  jira_url: https://jira.invalid\n" +
		"  confluence_url: https://confluence.invalid\n" +
		"  artifactory_dev_pypi: https://pypi.invalid/dev\n" +
		"  artifactory_release_pypi: https://pypi.invalid/release\n" +
		"  mcp_servers_dir_rel: .local/share/mcp-servers\n" +
		"packs:\n" +
		"  - name: local\n" +
		"    enabled: true\n" +
		"    settings:\n" +
		"      enabled: true\n" +
		"    mcp:\n" +
		"      bitbucket: { enabled: true }\n" +
		"      atlassian: { enabled: true }\n")
	if err := os.WriteFile(profilePath, profile, 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	t.Setenv("BITBUCKET_TOKEN", "x")

	stdout, _, exit := runApp(t, "doctor", "--config-dir", configDir, "--profile-path", profilePath, "--json")
	if exit != 0 {
		t.Fatalf("doctor exit=%d, want 0; stdout=%s", exit, stdout)
	}
	var rep app.DoctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal doctor JSON: %v\njson=%s", err, stdout)
	}
	if !rep.OK || rep.Status != "ok" {
		t.Fatalf("doctor rep ok=%v status=%q, want ok=true status=ok", rep.OK, rep.Status)
	}
}

func TestDoctor_FailsWithMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()

	stdout, _, exit := runApp(t, "doctor", "--config-dir", tmpDir, "--json")
	if exit != 1 {
		t.Fatalf("doctor exit=%d, want 1", exit)
	}
	var rep app.DoctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal doctor JSON: %v\njson=%s", err, stdout)
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "sync_config_loaded" && c.Status == "fail" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sync_config_loaded fail check")
	}
}

func TestDoctor_HelpReturnsOK(t *testing.T) {
	_, _, exit := runApp(t, "doctor", "--help")
	if exit != cmdutil.ExitOK {
		t.Fatalf("doctor --help exit=%d, want %d", exit, cmdutil.ExitOK)
	}
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}

func quoteYAML(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
