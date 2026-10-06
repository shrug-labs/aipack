package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/testutil"
)

func TestDoctorOmitsPluginDelivery(t *testing.T) {
	home, dir, source := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), []byte(`{"name":"probe","version":"1.0.0"}`))
	writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), []byte("---\nname: probe\ndescription: Owned plugin fixture\n---\nRead only.\n"))
	writeFile(t, filepath.Join(source, ".mcp.json"), []byte(`{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`))
	writeFile(t, filepath.Join(source, "hooks/hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`))
	if err := app.PackInstall(context.Background(), app.PackInstallRequest{ConfigDir: dir, PackPath: source, Name: "alias", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: default\n  scope: global\n  harnesses: [cline]\n"))
	writeFile(t, filepath.Join(dir, "profiles/default.yaml"), []byte("schema_version: 2\npacks:\n  - name: alias\n    hooks:\n      enabled: false\n"))
	args := []string{"doctor", "--config-dir", dir}
	out, diagnostics, code := runApp(t, append(args, "--json")...)
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &report); err != nil || code != 0 {
		t.Fatalf("doctor JSON: %s %s %d %v", out, diagnostics, code, err)
	}
	if _, exists := report["plugin_readiness"]; exists {
		t.Fatalf("doctor JSON includes plugin delivery: %s", out)
	}
	out, diagnostics, code = runApp(t, args...)
	if code != 0 || !strings.Contains(out, "doctor OK") {
		t.Fatalf("doctor text: %s %s %d", out, diagnostics, code)
	}
	for _, unwanted := range []string{"Plugin delivery:", "alias / cline", "available:", "selected:", "execution and login are not tested"} {
		if strings.Contains(out+diagnostics, unwanted) {
			t.Fatalf("doctor output includes %q: %s %s", unwanted, out, diagnostics)
		}
	}
}

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
  "schema_version": 2,
  "name": "test-pack",
  "version": "0",
  "root": ".",
  "rules": [],
  "agents": [],
  "workflows": [],
  "skills": [],
  "mcp":["srv-a","srv-b"],
  "configs": {"harness_settings": {}}
}`)
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), packManifest, 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	nodePath := filepath.Join(packDir, "bin", "node")
	uvxPath := filepath.Join(packDir, "bin", "uvx")
	serverPath := filepath.Join(packDir, "srv-a-mcp", "build", "index.js")
	for _, p := range []string{nodePath, uvxPath, serverPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	if err := os.WriteFile(filepath.Join(packDir, "mcp", "srv-a.json"), []byte(`{
  "name": "srv-a",
  "transport": "stdio",
  "timeout": 300,
  "command": [
    "`+escapeJSON(nodePath)+`",
    "`+escapeJSON(serverPath)+`"
  ],
  "env": {
    "SRV_A_URL": "{global.srv_a_url}",
    "SRV_A_TOKEN": "{env:SRV_A_TOKEN}"
  },
  "available_tools": []
}`), 0o644); err != nil {
		t.Fatalf("write srv-a inventory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "srv-b.json"), []byte(`{
  "name": "srv-b",
  "transport": "stdio",
  "timeout": 300,
  "command": [
    "`+escapeJSON(uvxPath)+`",
    "--version"
  ],
  "env": {},
  "available_tools": []
}`), 0o644); err != nil {
		t.Fatalf("write srv-b inventory: %v", err)
	}

	// Install pack at configDir/packs/local/
	installedPackDir := filepath.Join(configDir, "packs", "local")
	if err := os.MkdirAll(filepath.Dir(installedPackDir), 0o755); err != nil {
		t.Fatalf("mkdir installed packs: %v", err)
	}
	testutil.Symlink(t, packDir, installedPackDir)

	profilePath := filepath.Join(t.TempDir(), "profile.yaml")
	profile := []byte("" +
		"schema_version: 6\n" +
		"globals:\n" +
		"  srv_a_url: https://example.invalid\n" +
		"packs:\n" +
		"  - name: local\n" +
		"    enabled: true\n" +
		"    settings:\n" +
		"      enabled: true\n" +
		"    mcp:\n" +
		"      srv-a: { enabled: true }\n" +
		"      srv-b: { enabled: true }\n")
	if err := os.WriteFile(profilePath, profile, 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	t.Setenv("SRV_A_TOKEN", "x")

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
