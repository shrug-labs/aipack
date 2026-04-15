package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestExitCodeContract_PackUpdateMissingCommit(t *testing.T) {
	configDir := t.TempDir()
	packsDir := filepath.Join(configDir, "packs")
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	packDir := filepath.Join(packsDir, "demo")
	if err := os.MkdirAll(filepath.Join(packDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":1,"name":"demo","version":"1.0.0","root":"."}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	lockfile := `schema_version: 1
packs:
  demo:
    method: clone
    origin: file:///nonexistent/repo.git
    ref: main
    version: 1.0.0
`
	if err := os.WriteFile(filepath.Join(configDir, "aipack.lock"), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	_, _, code := runApp(t, "pack", "update", "demo",
		"--config-dir", configDir,
		"--version", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update with bad commit must exit non-zero, got %d", code)
	}
}

func TestExitCodeContract_DoctorMissingCriticalEnv(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	packDir := filepath.Join(configDir, "packs", "demo")
	for _, dir := range []string{
		filepath.Join(configDir, "profiles"),
		filepath.Join(packDir, "mcp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schema_version":1,"name":"demo","version":"1.0.0","root":".","mcp":{"servers":{"needsenv":{}}}}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	mcpServer := `{"name":"needsenv","transport":"stdio","command":["echo"],"env":{"REQUIRED_TOKEN":"{env:REQUIRED_TOKEN_NEVER_SET}"}}`
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "needsenv.json"), []byte(mcpServer), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n    mcp:\n      needsenv:\n        enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	// t.Setenv registers cleanup; os.Unsetenv actually removes the var for the test.
	t.Setenv("REQUIRED_TOKEN_NEVER_SET", "")
	os.Unsetenv("REQUIRED_TOKEN_NEVER_SET")

	_, _, code := runApp(t, "doctor", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("doctor with unset critical env must exit non-zero, got %d", code)
	}
}

func TestExitCodeContract_SyncMissingSkill(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	configDir := filepath.Join(home, ".config", "aipack")
	packDir := filepath.Join(configDir, "packs", "demo")
	for _, dir := range []string{
		projectDir,
		filepath.Join(configDir, "profiles"),
		packDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schema_version":1,"name":"demo","version":"1.0.0","root":".","skills":["ghost-skill"]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	_, _, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --dry-run with missing manifest skill must exit non-zero, got %d", code)
	}
}

func TestExitCodeContract_PackInstallReconcileError(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	writeReconcileFixture(t, configDir)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")

	_, _, code := runApp(t, "pack", "install", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("pack install reconciliation against unknown pack must exit non-zero, got %d", code)
	}
}

func TestExitCodeContract_ProfileSetInstallReconcileError(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	writeReconcileFixture(t, configDir)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")

	_, _, code := runApp(t, "profile", "set", "default", "--install", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("profile set --install against unknown pack must exit non-zero, got %d", code)
	}
}

func writeReconcileFixture(t *testing.T, configDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(configDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := "schema_version: 2\npacks:\n  - name: nowhere-pack\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
}
