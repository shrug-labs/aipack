package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestResolveHarnesses_All(t *testing.T) {
	hs, err := cmdutil.ResolveHarnesses([]string{"all"})
	if err != nil {
		t.Fatalf("ResolveHarnesses(all) returned error: %v", err)
	}
	want := domain.AllHarnesses()
	if len(hs) != len(want) {
		t.Fatalf("ResolveHarnesses(all) = %#v, want %d harnesses", hs, len(want))
	}
	for i := range want {
		if hs[i] != want[i] {
			t.Fatalf("ResolveHarnesses(all) = %#v, want %#v", hs, want)
		}
	}
}

func TestResolveHarnesses_ExplicitWins(t *testing.T) {
	t.Setenv(cmdutil.DefaultHarnessEnv, "codex")
	hs, err := cmdutil.ResolveHarnesses([]string{"opencode"})
	if err != nil {
		t.Fatalf("ResolveHarnesses returned error: %v", err)
	}
	if len(hs) != 1 || hs[0] != "opencode" {
		t.Fatalf("ResolveHarnesses explicit = %#v, want [opencode]", hs)
	}
}

func TestResolveHarnesses_EnvFallback(t *testing.T) {
	t.Setenv(cmdutil.DefaultHarnessEnv, "codex,opencode")
	hs, err := cmdutil.ResolveHarnesses(nil)
	if err != nil {
		t.Fatalf("ResolveHarnesses returned error: %v", err)
	}
	if len(hs) != 2 || hs[0] != "codex" || hs[1] != "opencode" {
		t.Fatalf("ResolveHarnesses env fallback = %#v, want [codex opencode]", hs)
	}
}

func TestResolveHarnesses_ErrorsWhenNoDefaults(t *testing.T) {
	t.Setenv(cmdutil.DefaultHarnessEnv, "")
	if _, err := cmdutil.ResolveHarnesses(nil); err == nil {
		t.Fatal("ResolveHarnesses expected error when no defaults are configured")
	}
}

func TestRunSave_TypesRequiresToPack(t *testing.T) {
	_, stderr, code := runApp(t, "save", "--types", "rules")
	if code == cmdutil.ExitOK {
		t.Fatalf("save --types without --to-pack should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "--types requires --to-pack") {
		t.Fatalf("expected '--types requires --to-pack' in stderr, got: %s", stderr)
	}
}

func TestRunSave_HelpReturnsOK(t *testing.T) {
	_, _, code := runApp(t, "save", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("save --help exit=%d want %d", code, cmdutil.ExitOK)
	}
}

func TestRunSave_ToPackUsesAllResolvedHarnesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "project")
	configDir := filepath.Join(home, ".config", "aipack")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rulePath := filepath.Join(projectDir, ".claude", "rules", "sample.md")
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
		t.Fatal(err)
	}
	rule := "---\nname: sample\ndescription: sample rule\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nbody\n"
	if err := os.WriteFile(rulePath, []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(projectDir, ".agents", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: demo\ndescription: Use when testing save flows.\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nDemo skill.\n"
	if err := os.WriteFile(skillPath, []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	syncCfg := "{\n  \"schema_version\": 1,\n  \"defaults\": {\n    \"profile\": \"default\",\n    \"scope\": \"project\"\n  },\n  \"installed_packs\": {}\n}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t,
		"save",
		"--to-pack", "captured",
		"--config-dir", configDir,
		"--project-dir", projectDir,
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("save exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	for _, path := range []string{
		filepath.Join(configDir, "packs", "captured", "rules", "sample.md"),
		filepath.Join(configDir, "packs", "captured", "skills", "demo", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected saved file %s: %v", path, err)
		}
	}
}

func TestRunSave_ToPackInitializesMissingConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "project")
	configDir := filepath.Join(home, ".config", "aipack")
	if err := os.MkdirAll(filepath.Join(projectDir, ".claude", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}

	rule := "---\nname: sample\ndescription: sample rule\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(projectDir, ".claude", "rules", "sample.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t,
		"save",
		"--to-pack", "captured",
		"--config-dir", configDir,
		"--project-dir", projectDir,
		"--harness", "claudecode",
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("save exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	if _, err := os.Stat(filepath.Join(configDir, "sync-config.yaml")); err != nil {
		t.Fatalf("expected sync-config.yaml to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "profiles", "default.yaml")); err != nil {
		t.Fatalf("expected default profile to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "captured", "pack.json")); err != nil {
		t.Fatalf("expected destination pack to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "captured", "rules", "sample.md")); err != nil {
		t.Fatalf("expected saved rule to exist: %v", err)
	}
}

func TestRunSave_ToPackDefaultGlobalScopeRejectsProjectDir(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	projectDir := filepath.Join(home, "project")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  scope: global\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "save", "--to-pack", "captured", "--config-dir", configDir, "--project-dir", projectDir)
	if code == cmdutil.ExitOK {
		t.Fatal("save --to-pack should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}

func TestRunSave_ToPackDryRunNewPackAcrossHarnesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "project")
	configDir := filepath.Join(home, ".config", "aipack")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rulePath := filepath.Join(projectDir, ".claude", "rules", "sample.md")
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
		t.Fatal(err)
	}
	rule := "---\nname: sample\ndescription: sample rule\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nbody\n"
	if err := os.WriteFile(rulePath, []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(projectDir, ".agents", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: demo\ndescription: Use when testing save flows.\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nDemo skill.\n"
	if err := os.WriteFile(skillPath, []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t,
		"save",
		"--to-pack", "captured",
		"--config-dir", configDir,
		"--project-dir", projectDir,
		"--dry-run",
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("save --dry-run exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "(dry-run)") {
		t.Fatalf("expected dry-run summary, got stdout=%s stderr=%s", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "captured")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create destination pack, stat err=%v", err)
	}
}
