package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestRunSync_DryRunVerboseDoesNotAppendZeroSummary(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run", "--verbose")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "plan: 1 changes") {
		t.Fatalf("expected verbose dry-run plan in stdout, got: %s", stdout)
	}
	if strings.Contains(stdout, "dry-run: 0 content, 0 settings") {
		t.Fatalf("unexpected zero-summary appended to verbose dry-run output: %s", stdout)
	}
}

func TestRunSync_DryRunJSONIsValidJSONOnly(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run", "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if got["rules"] != float64(1) {
		t.Fatalf("rules = %#v, want 1", got["rules"])
	}
	if got["mcp"] != float64(0) {
		t.Fatalf("mcp = %#v, want 0", got["mcp"])
	}
}

func TestResolveWatchDirs_ReturnsPackRootsWhenProfileContentIsInvalid(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	packDir := filepath.Join(configDir, "packs", "demo")
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := resolveWatchDirs("", "", configDir)
	if err != nil {
		t.Fatalf("resolveWatchDirs returned error: %v", err)
	}

	want := packDir
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("resolveWatchDirs = %v, want [%s]", dirs, want)
	}
}

func writeSyncFixture(t *testing.T) (home, configDir, projectDir string) {
	t.Helper()

	home = t.TempDir()
	projectDir = filepath.Join(home, "project")
	configDir = filepath.Join(home, ".config", "aipack")
	packDir := filepath.Join(configDir, "packs", "demo")

	for _, dir := range []string{
		projectDir,
		filepath.Join(configDir, "profiles"),
		filepath.Join(packDir, "rules"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schema_version":1,"name":"demo","root":"."}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	rule := "---\nname: sample\ndescription: sample rule\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(packDir, "rules", "sample.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	return home, configDir, projectDir
}
