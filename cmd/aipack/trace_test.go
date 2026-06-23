package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
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

func TestTrace_DisabledPackReportsDiagnostic(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	writeRulePackFixture(t, configDir, "disabled", "hidden", "1.0.0")
	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n  - name: disabled\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "hidden", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"profile state: pack_disabled",
		"destinations: (none planned)",
		"aipack pack enable disabled --profile default",
		"aipack sync --profile default",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("trace output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrace_AllDisabledProfileReportsDiagnostic(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	writeRulePackFixture(t, configDir, "disabled", "hidden", "1.0.0")
	profile := "schema_version: 2\npacks:\n  - name: disabled\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "hidden", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"profile state: pack_disabled",
		"aipack pack enable disabled --profile default",
		"aipack sync --profile default",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("trace output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrace_JSONContentExcludedDiagnostic(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n    rules:\n      exclude:\n        - sample\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "sample", "--json", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var result app.TraceResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !result.Found || result.ProfileState != "content_excluded" {
		t.Fatalf("result found/state = %v/%q, want true/content_excluded: %+v", result.Found, result.ProfileState, result)
	}
	if len(result.Destinations) != 0 {
		t.Fatalf("inactive resource should have no destinations: %+v", result.Destinations)
	}
	if len(result.Remediation) == 0 || !strings.Contains(strings.Join(result.Remediation, "\n"), "aipack profile include sample --kind rule --pack demo --profile default") {
		t.Fatalf("missing include remediation: %+v", result.Remediation)
	}
}

func TestTrace_InstalledNotInProfileReportsDiagnostic(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	writeRulePackFixture(t, configDir, "outside", "orphan", "1.0.0")

	stdout, stderr, code := runApp(t, "trace", "rule", "orphan", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"profile state: installed_not_in_profile",
		"aipack pack add outside --profile default",
		"aipack sync --profile default",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("trace output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrace_QuietInstalledPackQuotesSelectiveRemediation(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	writeRulePackFixture(t, configDir, "outside pack", "space rule", "1.0.0")
	if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{
		Packs: map[string]config.InstalledPackMeta{
			"outside pack": {InstallQuiet: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "rule", "space rule", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("trace exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"aipack pack add 'outside pack' --profile default",
		"aipack profile include 'space rule' --kind rule --pack 'outside pack' --profile default",
		"aipack sync --profile default",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("trace output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrace_MissingResourceExitsFailWithJSONDiagnostic(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)

	stdout, stderr, code := runApp(t, "trace", "rule", "absent", "--json", "--config-dir", configDir, "--project-dir", projectDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("trace missing exit=%d, want %d; stdout=%s stderr=%s", code, cmdutil.ExitFail, stdout, stderr)
	}
	var result app.TraceResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if result.Found || result.ProfileState != "not_installed" {
		t.Fatalf("result found/state = %v/%q, want false/not_installed", result.Found, result.ProfileState)
	}
}

func TestTrace_SmartNameInactiveAmbiguityListsStates(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)
	t.Setenv("HOME", home)
	writeRulePackFixture(t, configDir, "disabled", "hidden", "1.0.0")
	writeSkillFixture(t, filepath.Join(configDir, "packs", "disabled"), "hidden")
	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n  - name: disabled\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "trace", "hidden", "--config-dir", configDir, "--project-dir", projectDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("trace smart inactive ambiguous should fail; stdout=%s stderr=%s", stdout, stderr)
	}
	for _, want := range []string{
		`Multiple resources named "hidden"`,
		"state=pack_disabled",
		"aipack trace rule hidden",
		"aipack trace skill hidden",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("trace ambiguity output missing %q:\n%s", want, stderr)
		}
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
