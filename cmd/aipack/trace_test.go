package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestTrace_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("trace --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestTrace_InvalidScope_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "rule", "test-rule", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("trace --scope badscope should fail, got exit=%d", code)
	}
}

func TestTrace_GlobalWithProjectDir_ReturnsError(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "trace", "rule", "test-rule", "--scope", "global", "--project-dir", "/tmp")
	if code == cmdutil.ExitOK {
		t.Fatal("trace --scope global --project-dir should fail")
	}
}

func TestTrace_WithoutHarnessDefaults_UsesAllHarnesses(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "sample", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, harness := range []string{"claudecode:", "cline:", "codex:", "opencode:"} {
		if !strings.Contains(stdout, harness) {
			t.Fatalf("expected trace output to include %s, got: %s", harness, stdout)
		}
	}
}

func TestTrace_SmartNameInfersUniqueType(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)

	stdout, stderr, code := runApp(t, "trace", "sample", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace smart exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"rule: sample",
		"pack: demo",
		"destinations:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("trace smart output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrace_SmartNameAmbiguityListsExplicitCommands(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)

	writeSkillFixture(t, filepath.Join(configDir, "packs", "demo"), "sample")

	stdout, stderr, code := runApp(t, "trace", "sample", "--config-dir", configDir, "--project-dir", projectDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("trace smart ambiguous should fail; stdout=%s stderr=%s", stdout, stderr)
	}
	for _, want := range []string{
		`Multiple resources named "sample"`,
		"aipack trace rule sample",
		"aipack trace skill sample",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("trace ambiguity output missing %q:\n%s", want, stderr)
		}
	}
}

func TestTrace_DefaultGlobalScopeRejectsProjectDir(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: global\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "trace", "rule", "sample", "--config-dir", configDir, "--project-dir", filepath.Join(home, "project"))
	if code == cmdutil.ExitOK {
		t.Fatal("trace should fail when defaults.scope=global and --project-dir is set")
	}
	if !strings.Contains(stderr, "effective scope global") {
		t.Fatalf("expected effective scope error, got: %s", stderr)
	}
}

func writeSkillFixture(t *testing.T, packDir, name string) {
	t.Helper()
	dir := filepath.Join(packDir, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: sample skill\nmetadata:\n  owner: test\n  last_updated: 2026-05-05\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
