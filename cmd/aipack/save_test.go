package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestResolveSaveHarness_ExplicitFlagWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(cmdutil.DefaultHarnessEnv, "codex")

	hs, err := resolveSaveHarnesses("opencode")
	if err != nil {
		t.Fatalf("resolveSaveHarnesses returned error: %v", err)
	}
	if len(hs) != 1 || hs[0] != "opencode" {
		t.Fatalf("resolveSaveHarnesses explicit harness = %#v, want [%q]", hs, "opencode")
	}
}

func TestResolveSaveHarness_UsesSyncConfigDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(cmdutil.DefaultHarnessEnv, "codex")

	configPath := filepath.Join(home, ".config", "aipack", "sync-config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := "schema_version: 1\ndefaults:\n  harnesses:\n    - opencode\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write sync-config: %v", err)
	}

	hs, err := resolveSaveHarnesses("")
	if err != nil {
		t.Fatalf("resolveSaveHarnesses returned error: %v", err)
	}
	if len(hs) != 1 || hs[0] != "opencode" {
		t.Fatalf("resolveSaveHarnesses config default = %#v, want [%q]", hs, "opencode")
	}
}

func TestResolveSaveHarness_UsesEnvFallbackWhenNoConfigDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(cmdutil.DefaultHarnessEnv, "codex,opencode")

	hs, err := resolveSaveHarnesses("")
	if err != nil {
		t.Fatalf("resolveSaveHarnesses returned error: %v", err)
	}
	if len(hs) != 2 || hs[0] != "codex" || hs[1] != "opencode" {
		t.Fatalf("resolveSaveHarnesses env fallback = %#v, want [codex opencode]", hs)
	}
}

func TestResolveSaveHarness_ErrorsWhenNoDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(cmdutil.DefaultHarnessEnv, "")

	if _, err := resolveSaveHarnesses(""); err == nil {
		t.Fatalf("resolveSaveHarnesses expected error when no defaults are configured")
	}
}

func TestResolveSaveHarness_All(t *testing.T) {
	hs, err := resolveSaveHarnesses("all")
	if err != nil {
		t.Fatalf("resolveSaveHarnesses returned error: %v", err)
	}
	want := domain.AllHarnesses()
	if len(hs) != len(want) {
		t.Fatalf("resolveSaveHarnesses(all) = %#v, want %d harnesses", hs, len(want))
	}
	for i := range want {
		if hs[i] != want[i] {
			t.Fatalf("resolveSaveHarnesses(all) = %#v, want %#v", hs, want)
		}
	}
}

func TestRunSave_SnapshotAndToPackMutuallyExclusive(t *testing.T) {
	_, stderr, code := runApp(t, "save", "--snapshot", "--to-pack", "foo")
	if code == cmdutil.ExitOK {
		t.Fatalf("save --snapshot --to-pack should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("expected 'mutually exclusive' in stderr, got: %s", stderr)
	}
}

func TestRunSave_HelpReturnsOK(t *testing.T) {
	_, _, code := runApp(t, "save", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("save --help exit=%d want %d", code, cmdutil.ExitOK)
	}
}
