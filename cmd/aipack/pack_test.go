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
)

func TestPackList_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "list", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackList_JSON_Empty(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(entries))
	}
}

func TestPackList_JSON_WithPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "test-pack")

	_ = app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       false,
	}, os.NewFile(0, os.DevNull))

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["name"] != "test-pack" {
		t.Fatalf("name = %v, want test-pack", entries[0]["name"])
	}
}

func TestPackUpdate_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack update --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackUpdate_MutualExclusion(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--all", "my-pack")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update --all+name should fail, got exit=%d", code)
	}
}

// TestPackInstall_NonSemverVersionTreatedAsLiteralRef verifies that
// `pack install --version main` is dispatched as a literal ref through
// the clone path rather than rejected at the CLI validation gate. The
// install still fails downstream (fake URL), but the failure must NOT
// be an "invalid version" short-circuit.
func TestPackInstall_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "pack", "install", "--url", "https://example.com/repo", "--version", "main")
	if code == cmdutil.ExitOK {
		t.Fatalf("install with fake URL should still fail downstream, got exit=%d", code)
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
}

func TestPackUpdate_VersionAndAllMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--all", "--version", "1.0.0")
	if code == cmdutil.ExitOK {
		t.Fatalf("--version + --all should fail, got exit=%d", code)
	}
}

func TestPackVersions_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "versions", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack versions --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackVersions_MissingName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "versions")
	if code == cmdutil.ExitOK {
		t.Fatal("pack versions (no args) should fail")
	}
}

// TestPackVersions_NotInstalledOrRegistered exercises the full Kong → service
// → error wiring for `pack versions`. With an empty config dir the lockfile
// has no entry and the registry has nothing to fall back to, so the service
// returns the "not installed and not found in registry" error. The test
// asserts the error reaches stderr in the expected shape.
func TestPackVersions_NotInstalledOrRegistered(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "versions", "ghost-pack", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("pack versions on missing pack should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "ghost-pack") || !strings.Contains(stderr, "not installed") {
		t.Errorf("stderr should mention name and 'not installed', got: %s", stderr)
	}
}

// TestPackVersions_CopyPackRejects verifies that running `pack versions`
// against a copy-method install (no remote git origin) surfaces the
// service-layer error through the CLI. Proves the lockfile lookup wiring
// works without needing real network access.
func TestPackVersions_CopyPackRejects(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "local-pack")

	if err := app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
	}, os.NewFile(0, os.DevNull)); err != nil {
		t.Fatalf("seeding pack install: %v", err)
	}

	_, stderr, code := runApp(t, "pack", "versions", "local-pack", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("pack versions on copy install should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "remote clone install") {
		t.Errorf("stderr should mention 'remote clone install', got: %s", stderr)
	}
}

// TestPackInstall_VersionInNameParses verifies the name@version positional
// parser strips the version before registry lookup. With no registry and an
// empty config the install fails, but the failure must reference the parsed
// pack name ("ghost-pack") and NOT the raw input ("ghost-pack@1.2.3"). That
// difference is the only end-to-end proof the parser ran.
func TestPackInstall_VersionInNameParses(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@1.2.3", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("install of nonexistent registry pack should fail, got exit=%d", code)
	}
	if strings.Contains(stderr, "ghost-pack@1.2.3") {
		t.Errorf("stderr leaked raw name@version (parser did not strip): %s", stderr)
	}
	if !strings.Contains(stderr, "ghost-pack") {
		t.Errorf("stderr should reference parsed name 'ghost-pack', got: %s", stderr)
	}
}

// TestPackInstall_VersionInNameAndFlagCollide verifies the @<ref> + --ref
// collision check at the install entry point. Both forms of intent are
// rejected with a clear error rather than silently letting one win.
// Covers both flag spellings since --version is a Kong alias for --ref.
func TestPackInstall_VersionInNameAndFlagCollide(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@1.2.3", "--version", "2.0.0", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("expected collision error, got exit=%d", code)
	}
	if !strings.Contains(stderr, "@<ref>") {
		t.Errorf("stderr should explain the @<ref> collision, got: %s", stderr)
	}
}

// TestPackInstall_NonSemverInNameTreatedAsLiteralRef verifies that the
// @<spec> shorthand parses non-semver specs (like "ghost-pack@main") as
// literal refs instead of rejecting them. The install still fails
// downstream (pack not in registry), but the error path must go through
// registry lookup, not an "invalid version" short-circuit.
func TestPackInstall_NonSemverInNameTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@main", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("install of nonexistent registry pack should fail, got exit=%d", code)
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
	// The error must come from registry lookup (not validation), proving
	// the @version parser passed "main" through to the install dispatch.
	if !strings.Contains(stderr, "ghost-pack") {
		t.Errorf("stderr should reference parsed name 'ghost-pack', got: %s", stderr)
	}
}

func TestPackUpdate_BareUpdatesAll(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "update", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("bare pack update should succeed on empty config, got exit=%d stderr=%s", code, stderr)
	}
}

// TestPackInstall_BareReconcilesProfile verifies the new default: `pack
// install` with no arguments routes to profile reconciliation (equivalent to
// -m/--missing), succeeding on an empty config instead of erroring.
func TestPackInstall_BareReconcilesProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("bare pack install should succeed on empty config, got exit=%d stderr=%s", code, stderr)
	}
}

// TestPackInstall_BareWithWith verifies that combining bare install with
// --with still triggers the profile-reconciliation + --with error — implicit
// and explicit missing should behave identically.
func TestPackInstall_BareWithWith(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "install", "-w", "all")
	if code == cmdutil.ExitOK {
		t.Fatal("bare pack install with --with should error (reconciliation + --with conflict)")
	}
}

func TestPackUpdate_VersionWithoutName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--version", "1.0.0")
	if code == cmdutil.ExitOK {
		t.Fatal("pack update --version without a name should fail")
	}
}

// TestPackUpdate_NonSemverVersionTreatedAsLiteralRef verifies that
// `pack update my-pack --version main` no longer short-circuits on
// "invalid version". Under the unified ref model, the update dispatches
// through the literal-ref path. The command still fails (pack not
// installed in the empty config), but the error must not be a validation
// rejection.
func TestPackUpdate_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "pack", "update", "my-pack", "--version", "main")
	if code == cmdutil.ExitOK {
		t.Fatal("pack update against missing pack should fail")
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
}

func TestPackShow_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack show --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackShow_MissingName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack show (no args) should fail, got exit=%d", code)
	}
}

func TestPackShow_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "pack", "show", "nonexistent", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("pack show nonexistent exit=%d, want %d", code, cmdutil.ExitFail)
	}
}

// ---------------------------------------------------------------------------
// parseWithFlag
// ---------------------------------------------------------------------------

func TestParseWithFlag_Empty(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
	got2, err := parseWithFlag([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != nil {
		t.Errorf("empty input should return nil, got %v", got2)
	}
}

func TestParseWithFlag_All(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range domain.AllBundledCategories {
		if !got.Has(cat) {
			t.Errorf("all should include %q", cat)
		}
	}
}

func TestParseWithFlag_AllMixedCase(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"ALL"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) {
		t.Error("ALL (uppercase) should expand to all categories")
	}
}

func TestParseWithFlag_AllShortCircuits(t *testing.T) {
	t.Parallel()
	// "all" combined with other values should still return all.
	got, err := parseWithFlag([]string{"profiles", "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range domain.AllBundledCategories {
		if !got.Has(cat) {
			t.Errorf("all should include %q even when combined", cat)
		}
	}
}

func TestParseWithFlag_Aliases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		alias string
		want  domain.BundledCategory
	}{
		{"p", domain.BundledProfiles},
		{"r", domain.BundledRegistries},
		{"e", domain.BundledExtras},
		{"P", domain.BundledProfiles}, // case-insensitive
	}
	for _, tc := range cases {
		got, err := parseWithFlag([]string{tc.alias})
		if err != nil {
			t.Fatalf("alias %q: %v", tc.alias, err)
		}
		if !got.Has(tc.want) {
			t.Errorf("alias %q should expand to %q", tc.alias, tc.want)
		}
	}
}

func TestParseWithFlag_Invalid(t *testing.T) {
	t.Parallel()
	_, err := parseWithFlag([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestParseWithFlag_Whitespace(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{" profiles ", " extras"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) || !got.Has(domain.BundledExtras) {
		t.Errorf("should trim whitespace, got %v", got)
	}
}

func TestParseWithFlag_Duplicates(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"profiles", "profiles", "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) {
		t.Error("duplicates should not cause errors")
	}
}

func writePackManifestCmd(t *testing.T, dir string, name string) {
	t.Helper()
	m := map[string]any{
		"schema_version": 2,
		"name":           name,
		"version":        "1.0.0",
		"root":           ".",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
