package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
		Register:  false,
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

func TestPackUpdate_NeitherNameNorAll(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update (no args) should fail, got exit=%d", code)
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
		"schema_version": 1,
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
