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

func TestStatus_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "status", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("status --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestStatus_InvalidProfile_ReturnsError(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "aipack")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	_, _, code := runApp(t, "status", "--profile", "nonexistent", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("status with nonexistent profile should fail")
	}
}

func TestStatus_TextOutput(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	stdout, stderr, code := runApp(t, "status", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("status exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	for _, want := range []string{
		"profile: default",
		"packs (1):",
		"demo",
		"rules: 1",
		"totals: 1 rules",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestStatus_JSONOutput(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	stdout, stderr, code := runApp(t, "status", "--json", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("status --json exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	var es app.EcosystemStatus
	if err := json.Unmarshal([]byte(stdout), &es); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	if es.Profile != "default" {
		t.Errorf("profile=%q, want %q", es.Profile, "default")
	}
	if len(es.Packs) != 1 {
		t.Fatalf("packs=%d, want 1", len(es.Packs))
	}
	if es.Packs[0].Name != "demo" {
		t.Errorf("pack name=%q, want %q", es.Packs[0].Name, "demo")
	}
	if es.TotalRules != 1 {
		t.Errorf("total_rules=%d, want 1", es.TotalRules)
	}
}

func TestStatus_SpecificProfile(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	// Create a second profile pointing at the same pack.
	altProfile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "alt.yaml"), []byte(altProfile), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "status", "--profile", "alt", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("status --profile alt exit=%d", code)
	}
	if !strings.Contains(stdout, "profile: alt") {
		t.Errorf("expected 'profile: alt' in output, got:\n%s", stdout)
	}
}
