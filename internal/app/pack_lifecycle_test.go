package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"

	"gopkg.in/yaml.v3"
)

func claudeCodeTestRegistry() *harness.Registry {
	return harness.NewRegistry(ccharness.Harness{})
}

func TestPackDelete(t *testing.T) {
	t.Parallel()

	t.Run("removes from disk", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
		}, &out)

		out.Reset()
		if _, err := PackDelete(configDir, "test-pack", &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(configDir, "packs", "test-pack")); !os.IsNotExist(err) {
			t.Fatal("pack should be removed")
		}
	})

	t.Run("not installed", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		_, err := PackDelete(t.TempDir(), "nonexistent", &out)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("clears origin from sync-config", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		lf, _ := config.LoadLockfile(config.LockfilePath(configDir))
		if _, ok := lf.Packs["test-pack"]; !ok {
			t.Fatal("origin should exist before remove")
		}

		out.Reset()
		PackDelete(configDir, "test-pack", &out)
		lf, _ = config.LoadLockfile(config.LockfilePath(configDir))
		if _, ok := lf.Packs["test-pack"]; ok {
			t.Fatal("origin should be cleared")
		}
	})

	t.Run("removes from profile", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeEmptyProfile(t, configDir, "default")
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			Add: true, Profile: "default",
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		out.Reset()
		PackDelete(configDir, "test-pack", &out)

		b, _ := os.ReadFile(filepath.Join(configDir, "profiles", "default.yaml"))
		var cfg config.ProfileConfig
		yaml.Unmarshal(b, &cfg)
		for _, p := range cfg.Packs {
			if p.Name == "test-pack" {
				t.Fatal("pack entry should be removed from profile")
			}
		}
		if !strings.Contains(out.String(), "Removed") {
			t.Fatalf("output = %s", out.String())
		}
	})

	t.Run("returns bundled profiles", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()

		m := map[string]any{
			"schema_version": 2, "name": "test-pack", "version": "1.0.0",
			"root": ".", "profiles": []string{"dev"},
		}
		b, _ := json.Marshal(m)
		os.MkdirAll(packDir, 0o700)
		os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600)
		os.MkdirAll(filepath.Join(packDir, "profiles"), 0o700)
		os.WriteFile(filepath.Join(packDir, "profiles", "dev.yaml"),
			[]byte("schema_version: 1\npacks: []\n"), 0o600)

		writeEmptyProfile(t, configDir, "default")
		writeTestSyncConfig(t, configDir)

		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			Add: true, Profile: "default",
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		teamProfile := filepath.Join(configDir, "profiles", "dev.yaml")
		if _, err := os.Stat(teamProfile); err != nil {
			t.Fatalf("bundled profile should exist: %v", err)
		}

		out.Reset()
		result, _ := PackDelete(configDir, "test-pack", &out)

		if len(result.BundledProfiles) != 1 || result.BundledProfiles[0] != "dev" {
			t.Fatalf("BundledProfiles = %v", result.BundledProfiles)
		}
		// Still on disk (caller must remove).
		if _, err := os.Stat(teamProfile); err != nil {
			t.Fatal("profile should still exist before explicit removal")
		}

		out.Reset()
		RemoveBundledProfiles(configDir, result.BundledProfiles, &out)
		if _, err := os.Stat(teamProfile); !os.IsNotExist(err) {
			t.Fatal("profile should be removed")
		}
		if !strings.Contains(out.String(), "Removed bundled profile") {
			t.Fatalf("output = %s", out.String())
		}
	})
}

func TestPackDelete_RemovesInstalledSearchIndexRows(t *testing.T) {
	t.Parallel()

	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "indexed-delete-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "delete-smoke.md"), []byte(`---
name: delete-smoke
description: smoke rule for pack delete index cleanup
metadata:
  owner: test
  last_updated: 2026-05-06
---

# Delete Smoke

Smoke body.
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	installed := true
	before, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "smoke",
		Installed: &installed,
	})
	if err != nil {
		t.Fatalf("RunIndexSearch before delete: %v", err)
	}
	if len(before) != 1 || before[0].Pack != "indexed-delete-pack" {
		t.Fatalf("expected installed index row before delete, got %+v", before)
	}

	out.Reset()
	if _, err := PackDelete(configDir, "indexed-delete-pack", &out); err != nil {
		t.Fatalf("PackDelete: %v", err)
	}

	after, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "smoke",
		Installed: &installed,
	})
	if err != nil {
		t.Fatalf("RunIndexSearch after delete: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("deleted pack leaked installed search rows: %+v", after)
	}
}

func TestPackDelete_RestoresRegistryIndexStatus(t *testing.T) {
	t.Parallel()

	packDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(`{
  "schema_version": 2,
  "name": "registered-pack",
  "version": "1.0.0",
  "root": ".",
  "rules": ["delete-smoke"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "delete-smoke.md"), []byte(`---
name: delete-smoke
description: installed registry delete smoke
metadata:
  owner: test
  last_updated: 2026-06-05
---

Installed registry delete smoke body.
`), 0o600); err != nil {
		t.Fatal(err)
	}

	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.RegistrySources = []config.RegistrySourceEntry{{Name: "test", URL: "https://example.com/registry.yaml"}}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.RegistriesCacheDir(configDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SourceCachePath(configDir, "test"), []byte(`schema_version: 1
packs:
  registered-pack:
    repo: https://example.com/registered-pack.git
    description: Registered delete smoke pack
    owner: Test Team
`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.LoadMergedRegistry(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := indexRegistryEntries(reg, configDir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		NowFn:     func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	installed, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "registry delete smoke",
		Status:    "installed",
	})
	if err != nil {
		t.Fatalf("RunIndexSearch installed: %v", err)
	}
	if len(installed) == 0 || installed[0].Pack != "registered-pack" {
		t.Fatalf("expected installed search result before delete, got %+v", installed)
	}

	out.Reset()
	if _, err := PackDelete(configDir, "registered-pack", &out); err != nil {
		t.Fatalf("PackDelete: %v", err)
	}

	registered, err := RunIndexSearch(IndexSearchRequest{
		ConfigDir: configDir,
		Terms:     "Registered delete smoke",
		Status:    "registered",
	})
	if err != nil {
		t.Fatalf("RunIndexSearch registered: %v", err)
	}
	if len(registered) != 1 || registered[0].Pack != "registered-pack" || registered[0].Status != "registered" {
		t.Fatalf("expected registered search result after delete, got %+v", registered)
	}
}

func TestPackDelete_RemovesTrackingAndRenderedFilesByDefault(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "deletable")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Seed a ledger entry that pretends a sync rendered a file from `deletable`,
	// plus an entry from a sibling pack that must be preserved.
	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	rendered := filepath.Join(t.TempDir(), ".claude", "rules", "rendered.md")
	if err := os.MkdirAll(filepath.Dir(rendered), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rendered, []byte("rendered body"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(t.TempDir(), ".claude", "rules", "sibling.md")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("sibling body"), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("rendered body"), "deletable", nil, fixedNow)
	lg.Record(sibling, []byte("sibling body"), "other-pack", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "deletable", Registry: claudeCodeTestRegistry()}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}

	if !result.SourceRemoved {
		t.Fatalf("expected source removed, result=%+v", result)
	}
	if !result.LockfileCleared {
		t.Fatalf("expected lockfile cleared, result=%+v", result)
	}
	if result.LedgerCleared != 1 {
		t.Fatalf("expected 1 ledger entry cleared, got %d", result.LedgerCleared)
	}
	if result.RenderedRemoved != 1 {
		t.Fatalf("expected 1 rendered file removed, got %d", result.RenderedRemoved)
	}
	if result.ProfilesEdited != 1 {
		t.Fatalf("expected 1 profile edited, got %d", result.ProfilesEdited)
	}

	// Pack source removed.
	if _, err := os.Stat(filepath.Join(configDir, "packs", "deletable")); !os.IsNotExist(err) {
		t.Fatalf("expected pack source gone")
	}
	// Lockfile entry gone.
	lf, _ := config.LoadLockfile(config.LockfilePath(configDir))
	if _, ok := lf.Packs["deletable"]; ok {
		t.Fatalf("lockfile still has deletable entry")
	}
	// Profile entry gone.
	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	for _, p := range cfg.Packs {
		if p.Name == "deletable" {
			t.Fatalf("profile still references deletable")
		}
	}
	// Rendered file removed by default.
	if _, err := os.Stat(rendered); !os.IsNotExist(err) {
		t.Fatalf("rendered file should be removed by default")
	}
	// Sibling rendered file PRESERVED.
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling rendered file should persist: %v", err)
	}
	// Sibling ledger entry PRESERVED.
	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[filepath.Clean(sibling)]; !ok {
		t.Fatalf("sibling ledger entry should be preserved, got %v", reloaded.Managed)
	}
	if _, ok := reloaded.Managed[filepath.Clean(rendered)]; ok {
		t.Fatalf("deleted pack ledger entry should be cleared, got %v", reloaded.Managed)
	}
}

func TestPackDelete_RemovesCodexSkillRenderedFilesByDefault(t *testing.T) {
	cases := []struct {
		name         string
		renderedPath func(*testing.T) string
		body         []byte
		wantMessage  string
	}{
		{
			name: "ledger path",
			renderedPath: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), ".codex", "skills", "deploy", "SKILL.md")
			},
			body:        []byte("rendered codex skill"),
			wantMessage: "codex rendered skill should be removed by default",
		},
		{
			name: "CODEX_HOME path",
			renderedPath: func(t *testing.T) string {
				codexHome := t.TempDir()
				t.Setenv("CODEX_HOME", codexHome)
				return filepath.Join(codexHome, "skills", "deploy", "SKILL.md")
			},
			body:        []byte("rendered codex home skill"),
			wantMessage: "CODEX_HOME rendered skill should be removed by default",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packDir := t.TempDir()
			configDir := t.TempDir()
			writePackManifest(t, packDir, "deletable")
			writeEmptyProfile(t, configDir, "default")
			writeTestSyncConfig(t, configDir)

			var out bytes.Buffer
			if err := PackInstall(context.Background(), PackInstallRequest{
				PackPath: packDir, ConfigDir: configDir, Link: true,
				Add: true, Profile: "default",
				NowFn: func() time.Time { return fixedNow },
			}, &out); err != nil {
				t.Fatalf("PackInstall: %v", err)
			}

			eng := engine.New(nil, nil)
			ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessCodex)
			if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
				t.Fatal(err)
			}
			rendered := tc.renderedPath(t)
			if err := os.MkdirAll(filepath.Dir(rendered), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rendered, tc.body, 0o600); err != nil {
				t.Fatal(err)
			}
			lg := domain.NewLedger()
			lg.Record(rendered, tc.body, "deletable", nil, fixedNow)
			if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
				t.Fatalf("SaveLedger: %v", err)
			}

			out.Reset()
			result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "deletable"}, &out)
			if err != nil {
				t.Fatalf("PackDeleteWithOptions: %v", err)
			}
			if result.RenderedRemoved != 1 {
				t.Fatalf("expected 1 rendered file removed, got %d", result.RenderedRemoved)
			}
			if result.LedgerCleared != 1 {
				t.Fatalf("expected 1 ledger entry cleared, got %d", result.LedgerCleared)
			}
			if _, err := os.Stat(rendered); !os.IsNotExist(err) {
				t.Fatal(tc.wantMessage)
			}
		})
	}
}

func TestPackDelete_PreservesModifiedRenderedFilesAsUnmanaged(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "deletable")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	rendered := filepath.Join(t.TempDir(), ".claude", "rules", "edited.md")
	if err := os.MkdirAll(filepath.Dir(rendered), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rendered, []byte("original body"), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("original body"), "deletable", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}
	if err := os.WriteFile(rendered, []byte("user edited body"), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "deletable", Registry: claudeCodeTestRegistry()}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}
	if result.RenderedRemoved != 0 {
		t.Fatalf("modified rendered file must not be removed, got %d removals", result.RenderedRemoved)
	}
	if result.RenderedPreserved != 1 {
		t.Fatalf("expected 1 preserved rendered file, got %d", result.RenderedPreserved)
	}
	got, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatalf("modified rendered file should remain: %v", err)
	}
	if string(got) != "user edited body" {
		t.Fatalf("modified rendered content changed: %q", got)
	}
	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[filepath.Clean(rendered)]; ok {
		t.Fatalf("preserved modified rendered file should be unmanaged, got %v", reloaded.Managed)
	}
}

func TestPackDelete_PreservesUnknownLedgerPathsAsUnmanaged(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "deletable")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	arbitrary := filepath.Join(t.TempDir(), "not-a-harness-rendered-file.md")
	if err := os.WriteFile(arbitrary, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(arbitrary, []byte("body"), "deletable", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "deletable", Registry: claudeCodeTestRegistry()}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}
	if result.RenderedRemoved != 0 {
		t.Fatalf("unknown ledger path must not be removed, got %d removals", result.RenderedRemoved)
	}
	if result.RenderedPreserved != 1 {
		t.Fatalf("expected 1 preserved rendered path, got %d", result.RenderedPreserved)
	}
	if _, err := os.Stat(arbitrary); err != nil {
		t.Fatalf("unknown ledger path should remain: %v", err)
	}
	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[filepath.Clean(arbitrary)]; ok {
		t.Fatalf("preserved unknown path should be unmanaged, got %v", reloaded.Managed)
	}
}

func TestPackDelete_StripsSharedSettingsWithoutRemovingUserKeys(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "deletable")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	onDisk := []byte(`{
  "permissions": {
    "allow": ["mcp__server__tool", "user_permission"],
    "deny": ["mcp__server__delete"]
  },
  "theme": "dark"
}
`)
	prevManaged := []byte(`{
  "permissions": {
    "allow": ["mcp__server__tool"],
    "deny": ["mcp__server__delete"]
  }
}
`)
	if err := os.WriteFile(settingsPath, onDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(settingsPath, onDisk, "deletable", prevManaged, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "deletable", Registry: claudeCodeTestRegistry()}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}
	if result.SharedSettingsStripped != 1 {
		t.Fatalf("expected 1 shared settings file stripped, got %d", result.SharedSettingsStripped)
	}
	if result.RenderedRemoved != 0 {
		t.Fatalf("shared settings file must not be removed wholesale, got %d removals", result.RenderedRemoved)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings file should remain: %v", err)
	}
	if strings.Contains(string(got), "mcp__server__tool") || strings.Contains(string(got), "mcp__server__delete") {
		t.Fatalf("managed permission entries should be stripped, got:\n%s", got)
	}
	if !strings.Contains(string(got), "user_permission") || !strings.Contains(string(got), `"theme": "dark"`) {
		t.Fatalf("user settings should be preserved, got:\n%s", got)
	}
	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[filepath.Clean(settingsPath)]; ok {
		t.Fatalf("stripped shared settings file should be unmanaged, got %v", reloaded.Managed)
	}
}

func TestPackDelete_StripsMCPFromSharedSettingsUsingSyntheticLedgerEntries(t *testing.T) {
	t.Parallel()
	eng := engine.New(nil, nil)
	configDir := t.TempDir()
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeProject, t.TempDir(), domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mcpPath := filepath.Join(root, ".mcp.json")
	settingsPath := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mcpOnDisk := []byte(`{
  "mcpServers": {
    "delete-server": {"command": "delete"},
    "keep-server": {"command": "keep"}
  },
  "userCustom": "keep-me"
}
`)
	mcpOverlay := []byte(`{
  "mcpServers": {
    "delete-server": {"command": "delete"},
    "keep-server": {"command": "keep"}
  }
}
`)
	settingsOnDisk := []byte(`{
  "permissions": {
    "allow": ["mcp__delete-server__tool", "mcp__keep-server__tool", "user_permission"]
  },
  "user_pref": "keep-me"
}
`)
	settingsOverlay := []byte(`{
  "permissions": {
    "allow": ["mcp__delete-server__tool", "mcp__keep-server__tool"]
  }
}
`)
	if err := os.WriteFile(mcpPath, mcpOnDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, settingsOnDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(mcpPath, mcpOnDisk, "", mcpOverlay, fixedNow)
	lg.Record(settingsPath, settingsOnDisk, "", settingsOverlay, fixedNow)
	lg.Record(domain.MCPLedgerKey(mcpPath, "delete-server"), []byte(`{}`), "deletable", nil, fixedNow)
	lg.Record(domain.MCPLedgerKey(mcpPath, "keep-server"), []byte(`{}`), "keeper", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	result, err := cleanOrClearPackInLedger(packDeleteLedgerCleanRequest{
		eng:      eng,
		registry: claudeCodeTestRegistry(),
		path:     ledgerPath,
		packName: "deletable",
		stdout:   io.Discard,
	})
	if err != nil {
		t.Fatalf("cleanOrClearPackInLedger: %v", err)
	}
	if result.SharedSettingsStripped != 2 {
		t.Fatalf("expected 2 shared settings files stripped, got %d", result.SharedSettingsStripped)
	}
	if result.LedgerCleared != 1 {
		t.Fatalf("expected 1 synthetic MCP ledger entry cleared, got %d", result.LedgerCleared)
	}

	gotMCP, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotMCP), "delete-server") {
		t.Fatalf("deleted pack MCP server should be stripped, got:\n%s", gotMCP)
	}
	if !strings.Contains(string(gotMCP), "keep-server") || !strings.Contains(string(gotMCP), `"userCustom": "keep-me"`) {
		t.Fatalf("remaining MCP server and user content should be preserved, got:\n%s", gotMCP)
	}

	gotSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotSettings), "mcp__delete-server__tool") {
		t.Fatalf("deleted pack MCP permission should be stripped, got:\n%s", gotSettings)
	}
	if !strings.Contains(string(gotSettings), "mcp__keep-server__tool") ||
		!strings.Contains(string(gotSettings), "user_permission") ||
		!strings.Contains(string(gotSettings), `"user_pref": "keep-me"`) {
		t.Fatalf("remaining MCP permission and user settings should be preserved, got:\n%s", gotSettings)
	}

	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[domain.MCPLedgerKey(mcpPath, "delete-server")]; ok {
		t.Fatalf("deleted pack synthetic MCP ledger entry should be cleared")
	}
	if _, ok := reloaded.Managed[domain.MCPLedgerKey(mcpPath, "keep-server")]; !ok {
		t.Fatalf("sibling pack synthetic MCP ledger entry should remain")
	}
	if strings.Contains(string(reloaded.Managed[filepath.Clean(mcpPath)].ManagedOverlay), "delete-server") {
		t.Fatalf("MCP managed overlay should no longer contain deleted server")
	}
	if strings.Contains(string(reloaded.Managed[filepath.Clean(settingsPath)].ManagedOverlay), "mcp__delete-server__tool") {
		t.Fatalf("settings managed overlay should no longer contain deleted server permission")
	}
}

func TestPackDelete_PreservesSiblingMCPWhenDeletingSettingsPack(t *testing.T) {
	t.Parallel()
	eng := engine.New(nil, nil)
	configDir := t.TempDir()
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeProject, t.TempDir(), domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mcpPath := filepath.Join(root, ".mcp.json")
	settingsPath := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mcpOnDisk := []byte(`{
  "mcpServers": {
    "keep-server": {"command": "keep"}
  }
}
`)
	settingsOnDisk := []byte(`{
  "permissions": {
    "allow": ["Bash(ls:*)", "mcp__keep-server__tool"]
  },
  "review_setting": "settings-pack"
}
`)
	if err := os.WriteFile(mcpPath, mcpOnDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, settingsOnDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(mcpPath, mcpOnDisk, "settings-pack", mcpOnDisk, fixedNow)
	lg.Record(settingsPath, settingsOnDisk, "settings-pack", settingsOnDisk, fixedNow)
	lg.Record(domain.MCPLedgerKey(mcpPath, "keep-server"), []byte(`{}`), "mcp-pack", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	result, err := cleanOrClearPackInLedger(packDeleteLedgerCleanRequest{
		eng:      eng,
		registry: claudeCodeTestRegistry(),
		path:     ledgerPath,
		packName: "settings-pack",
		stdout:   io.Discard,
	})
	if err != nil {
		t.Fatalf("cleanOrClearPackInLedger: %v", err)
	}
	if result.SharedSettingsStripped != 1 {
		t.Fatalf("expected settings file stripped while MCP file stayed intact, got %d stripped files", result.SharedSettingsStripped)
	}

	gotMCP, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotMCP), "keep-server") {
		t.Fatalf("sibling MCP server should remain in shared MCP file, got:\n%s", gotMCP)
	}

	gotSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotSettings), "mcp__keep-server__tool") {
		t.Fatalf("sibling MCP permission should remain in settings file, got:\n%s", gotSettings)
	}
	if strings.Contains(string(gotSettings), "Bash(ls:*)") || strings.Contains(string(gotSettings), "review_setting") {
		t.Fatalf("deleted settings pack keys should be stripped, got:\n%s", gotSettings)
	}

	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[domain.MCPLedgerKey(mcpPath, "keep-server")]; !ok {
		t.Fatalf("sibling MCP synthetic ledger entry should remain")
	}
	if entry, ok := reloaded.Managed[filepath.Clean(mcpPath)]; !ok {
		t.Fatalf("shared MCP file ledger entry should remain for sibling server")
	} else if entry.SourcePack != "" || !strings.Contains(string(entry.ManagedOverlay), "keep-server") {
		t.Fatalf("shared MCP ledger entry not rewritten to sibling-only managed overlay: %+v", entry)
	}
	if entry, ok := reloaded.Managed[filepath.Clean(settingsPath)]; !ok {
		t.Fatalf("shared settings ledger entry should remain for sibling MCP permission")
	} else if entry.SourcePack != "" || !strings.Contains(string(entry.ManagedOverlay), "mcp__keep-server__tool") {
		t.Fatalf("shared settings ledger entry not rewritten to sibling-only managed overlay: %+v", entry)
	}
}

func TestPackDelete_KeepRenderedClearsTrackingPreservesRenderedFiles(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "kept")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	rendered := filepath.Join(t.TempDir(), "rendered.md")
	if err := os.WriteFile(rendered, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("body"), "kept", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{
		ConfigDir: configDir, Name: "kept", KeepRendered: true,
	}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}
	if result.RenderedRemoved != 0 {
		t.Fatalf("keep-rendered should not remove rendered files, got %d", result.RenderedRemoved)
	}
	if _, err := os.Stat(rendered); err != nil {
		t.Fatalf("rendered file should persist with keep-rendered: %v", err)
	}
	reloaded, warnings, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("LoadLedger: %v (warnings: %v)", err, warnings)
	}
	if _, ok := reloaded.Managed[filepath.Clean(rendered)]; ok {
		t.Fatalf("keep-rendered should clear ledger entry, got %v", reloaded.Managed)
	}
}

func TestPackDelete_DryRunMakesNoMutations(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "deletable")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	rendered := filepath.Join(t.TempDir(), "rendered.md")
	if err := os.WriteFile(rendered, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("body"), "deletable", nil, fixedNow)
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{
		ConfigDir: configDir, Name: "deletable", DryRun: true,
	}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions dry-run: %v", err)
	}
	if !result.DryRun {
		t.Fatalf("expected DryRun=true, result=%+v", result)
	}
	if result.LedgerCleared != 1 || result.ProfilesEdited != 1 {
		t.Fatalf("dry-run should report 1/1, got %d/%d", result.LedgerCleared, result.ProfilesEdited)
	}

	// Nothing should have actually changed.
	if _, err := os.Stat(filepath.Join(configDir, "packs", "deletable")); err != nil {
		t.Fatalf("pack source should still exist: %v", err)
	}
	lf, _ := config.LoadLockfile(config.LockfilePath(configDir))
	if _, ok := lf.Packs["deletable"]; !ok {
		t.Fatalf("dry-run should not touch lockfile")
	}
	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	found := false
	for _, p := range cfg.Packs {
		if p.Name == "deletable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dry-run should not touch profile")
	}
	reloaded, _, _ := eng.LoadLedger(ledgerPath)
	if _, ok := reloaded.Managed[filepath.Clean(rendered)]; !ok {
		t.Fatalf("dry-run should not modify ledger")
	}
}

// TestPackDelete_ClearsProjectScopedLedgerEntries verifies that delete walks
// project-scoped ledger subdirectories (configDir/ledger/<encoded-project>/)
// and clears entries matching the pack, not just global ledger files.
func TestPackDelete_ClearsProjectScopedLedgerEntries(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()
	writePackManifest(t, packDir, "scoped")
	writeEmptyProfile(t, configDir, "default")
	writeTestSyncConfig(t, configDir)

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: true,
		Add: true, Profile: "default",
		NowFn: func() time.Time { return fixedNow },
	}, &out); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	// Seed both global AND project-scoped ledger entries for the pack.
	eng := engine.New(nil, nil)
	projectA := t.TempDir()
	projectB := t.TempDir()
	globalLedger := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	projectALedger := engine.LedgerPath(configDir, domain.ScopeProject, projectA, domain.HarnessClaudeCode)
	projectBLedger := engine.LedgerPath(configDir, domain.ScopeProject, projectB, domain.HarnessCodex)

	for _, lp := range []string{globalLedger, projectALedger, projectBLedger} {
		if err := os.MkdirAll(filepath.Dir(lp), 0o700); err != nil {
			t.Fatal(err)
		}
		rendered := filepath.Join(t.TempDir(), "rendered.md")
		if err := os.WriteFile(rendered, []byte("body"), 0o600); err != nil {
			t.Fatal(err)
		}
		lg := domain.NewLedger()
		lg.Record(rendered, []byte("body"), "scoped", nil, fixedNow)
		// Sibling pack entry that must persist.
		sibling := filepath.Join(t.TempDir(), "sibling.md")
		if err := os.WriteFile(sibling, []byte("sibling"), 0o600); err != nil {
			t.Fatal(err)
		}
		lg.Record(sibling, []byte("sibling"), "other-pack", nil, fixedNow)
		if err := eng.SaveLedger(lp, lg, false); err != nil {
			t.Fatalf("SaveLedger %s: %v", lp, err)
		}
	}

	out.Reset()
	result, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "scoped", KeepRendered: true}, &out)
	if err != nil {
		t.Fatalf("PackDeleteWithOptions: %v", err)
	}
	// Three ledgers, one matching entry each.
	if result.LedgerCleared != 3 {
		t.Fatalf("LedgerCleared = %d, want 3 (global + projectA + projectB); output:\n%s", result.LedgerCleared, out.String())
	}

	// Each ledger should have its 'scoped' entry removed but the sibling entry preserved.
	for _, lp := range []string{globalLedger, projectALedger, projectBLedger} {
		reloaded, warnings, err := eng.LoadLedger(lp)
		if err != nil {
			t.Fatalf("LoadLedger %s: %v (warnings %v)", lp, err, warnings)
		}
		hasOtherPack := false
		for _, entry := range reloaded.Managed {
			if entry.SourcePack == "scoped" {
				t.Fatalf("ledger %s still has 'scoped' entry: %+v", lp, entry)
			}
			if entry.SourcePack == "other-pack" {
				hasOtherPack = true
			}
		}
		if !hasOtherPack {
			t.Fatalf("ledger %s should still have other-pack sibling entry", lp)
		}
	}
}

func TestPackDelete_NotInstalledReturnsError(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	var out bytes.Buffer
	_, err := PackDeleteWithOptions(engine.New(nil, nil), PackDeleteRequest{
		ConfigDir: configDir, Name: "missing-pack",
	}, &out)
	if err == nil {
		t.Fatal("expected error for missing pack")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected 'not installed' error, got: %v", err)
	}
}

func TestPackAdd_QuietUpdatesExistingEntry(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	var out bytes.Buffer
	PackAdd(configDir, "default", "my-pack", nil, &out)

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	for _, p := range cfg.Packs {
		if p.Name == "my-pack" && p.Quiet {
			t.Fatal("should not be quiet initially")
		}
	}

	out.Reset()
	t2 := true
	PackAdd(configDir, "default", "my-pack", &t2, &out)

	cfg, _ = config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "my-pack" {
			found = &cfg.Packs[i]
		}
	}
	if found == nil || !found.Quiet {
		t.Fatal("expected Quiet=true after re-add")
	}
}

// TestPackAdd_InheritsInstallQuietFromLockfile pins the feature: once a
// pack is installed with InstallQuiet=true on its lockfile meta, every
// profile add of that pack defaults to quiet without needing `-q` again.
// This is the v0.25.0 fix for "I thought installing -q made the pack
// quiet-by-nature" — the quiet intent now persists on the lockfile and
// PackAdd consults it when creating a new profile entry.
func TestPackAdd_InheritsInstallQuietFromLockfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	// Simulate a pack installed with -q by writing its lockfile meta.
	if err := packRecordOrigin(configDir, "quiet-pack", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	// PackAdd without an explicit override — should inherit Quiet=true.
	var out bytes.Buffer
	if err := PackAdd(configDir, "default", "quiet-pack", nil, &out); err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	var found *config.PackEntry
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == "quiet-pack" {
			found = &cfg.Packs[i]
		}
	}
	if found == nil {
		t.Fatal("pack not added to profile")
	}
	if !found.Quiet {
		t.Error("new profile entry should inherit Quiet=true from lockfile InstallQuiet")
	}
}

func TestPackAdd_WarnsWhenProfileIsBundled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	writeEmptyProfile(t, configDir, "default")

	packDir := filepath.Join(configDir, "packs", "source-pack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), config.PackManifest{
		SchemaVersion: 2,
		Name:          "source-pack",
		Root:          ".",
		Profiles:      []string{"default"},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackAdd(configDir, "default", "my-pack", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pack-provided profile") || !strings.Contains(out.String(), "source-pack") {
		t.Fatalf("expected bundled profile warning, got:\n%s", out.String())
	}
}

// TestPackAdd_NoQuietOverrideDemotesQuietInstall verifies the explicit
// demote path: `pack add --no-quiet` (quietOverride = &false) forces the
// profile entry to non-quiet even when the lockfile says the pack was
// installed quietly.
func TestPackAdd_NoQuietOverrideDemotesQuietInstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	if err := packRecordOrigin(configDir, "pack-x", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	var out bytes.Buffer
	f := false
	if err := PackAdd(configDir, "default", "pack-x", &f, &out); err != nil {
		t.Fatalf("PackAdd: %v", err)
	}

	cfg, _ := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	for _, p := range cfg.Packs {
		if p.Name == "pack-x" && p.Quiet {
			t.Error("--no-quiet override should force Quiet=false even when InstallQuiet=true")
		}
	}
}

// TestResolveInstallQuiet_PreservesOnReinstall pins the re-install
// preservation rule: when no explicit flag is passed, a re-install keeps
// the existing InstallQuiet so `pack install --add` (no -q) doesn't
// silently demote a pack that was originally installed with -q.
func TestResolveInstallQuiet_PreservesOnReinstall(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	config.EnsureInit(configDir)

	if err := packRecordOrigin(configDir, "preserve-pack", config.InstalledPackMeta{
		Origin: "/tmp/src", Method: config.MethodLink, InstalledAt: "2026-04-21T00:00:00Z",
		InstallQuiet: true,
	}); err != nil {
		t.Fatalf("packRecordOrigin: %v", err)
	}

	if got := resolveInstallQuiet(configDir, "preserve-pack", nil); !got {
		t.Error("nil override on a pack with InstallQuiet=true should return true (preserve)")
	}
	f := false
	if got := resolveInstallQuiet(configDir, "preserve-pack", &f); got {
		t.Error("*false override should force InstallQuiet=false regardless of lockfile")
	}
	if got := resolveInstallQuiet(configDir, "unknown-pack", nil); got {
		t.Error("unknown pack with nil override should default to false")
	}
}

func TestProfileMissingPacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		packs    []string
		disabled []string
		install  []string // packs to install on disk
		wantN    int
		wantName string // first expected missing name (empty = skip check)
	}{
		{"mixed", []string{"alpha", "beta", "gamma"}, nil, []string{"alpha"}, 2, "beta"},
		{"all present", []string{"alpha", "beta"}, nil, []string{"alpha", "beta"}, 0, ""},
		{"disabled skipped", []string{"alpha"}, []string{"beta"}, nil, 1, "alpha"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			if len(tc.disabled) > 0 {
				writeShowTestProfileWithDisabled(t, configDir, "test", tc.packs, tc.disabled)
			} else {
				writeShowTestProfile(t, configDir, "test", tc.packs)
			}
			for _, name := range tc.install {
				installShowPack(t, configDir, name)
			}
			missing, err := ProfileMissingPacks(configDir, "test")
			if err != nil {
				t.Fatal(err)
			}
			if len(missing) != tc.wantN {
				t.Fatalf("got %d missing %v, want %d", len(missing), missing, tc.wantN)
			}
			if tc.wantName != "" && (len(missing) == 0 || missing[0] != tc.wantName) {
				t.Errorf("first missing = %v, want %q", missing, tc.wantName)
			}
		})
	}
}

func TestPackInstallMissing(t *testing.T) {
	t.Parallel()

	t.Run("all present", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")
		installShowPack(t, configDir, "beta")

		var out bytes.Buffer
		results, err := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test",
		}, &out)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if r.Status != MissingStatusPresent {
				t.Errorf("%s: got %q, want present", r.Pack, r.Status)
			}
		}
	})

	t.Run("not in registry", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")

		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test",
		}, &out)
		if results[1].Status != MissingStatusNotInRegistry {
			t.Errorf("beta: got %q", results[1].Status)
		}
	})

	t.Run("installs from registry", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta"})
		installShowPack(t, configDir, "alpha")
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"beta": {Repo: "https://example.com/repo.git", Ref: "main", Path: "packs/beta"},
		})

		var captured PackInstallRequest
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			captured = req
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if results[1].Status != MissingStatusInstalled {
			t.Errorf("beta: got %q", results[1].Status)
		}
		if captured.URL != "https://example.com/repo.git" || captured.Ref != "main" || captured.SubPath != "packs/beta" {
			t.Errorf("PackInstall called with URL=%q Ref=%q SubPath=%q", captured.URL, captured.Ref, captured.SubPath)
		}
		if captured.Add {
			t.Error("Add should be false")
		}
	})

	t.Run("installs archive from registry", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"archive-pack"})
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"archive-pack": {Method: config.MethodArchive, URL: "https://example.com/pack.zip", Path: "packs/archive-pack", Ref: "ignored"},
		})

		var captured PackInstallRequest
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			captured = req
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if results[0].Status != MissingStatusInstalled {
			t.Errorf("archive-pack: got %q", results[0].Status)
		}
		if !captured.Archive {
			t.Fatal("PackInstallRequest.Archive = false, want true")
		}
		if captured.URL != "https://example.com/pack.zip" || captured.Ref != "" || captured.SubPath != "packs/archive-pack" {
			t.Errorf("PackInstall called with URL=%q Ref=%q SubPath=%q", captured.URL, captured.Ref, captured.SubPath)
		}
	})

	t.Run("propagates content paths and quiet", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"ext-catalog"})
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"ext-catalog": {
				Repo: "https://example.com/mono.git", Quiet: true,
				ContentPaths: map[domain.PackCategory]string{
					domain.CategorySkills: "tools/agent/skills",
					domain.CategoryRules:  "tools/agent/rules",
				},
			},
		})
		var captured PackInstallRequest
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			captured = req
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if captured.Quiet == nil || !*captured.Quiet {
			t.Error("Quiet not propagated")
		}
		if captured.ContentPaths[domain.CategorySkills] != "tools/agent/skills" {
			t.Errorf("skills path = %q", captured.ContentPaths[domain.CategorySkills])
		}
	})

	t.Run("error does not stop others", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeShowTestProfile(t, configDir, "test", []string{"alpha", "beta", "gamma"})
		writeShowTestRegistry(t, configDir, map[string]config.RegistryEntry{
			"alpha": {Repo: "https://example.com/a.git", Ref: "main"},
			"beta":  {Repo: "https://example.com/b.git", Ref: "main"},
			"gamma": {Repo: "https://example.com/c.git", Ref: "main"},
		})
		calls := 0
		fake := func(_ context.Context, req PackInstallRequest, _ io.Writer) error {
			calls++
			if req.Name == "beta" {
				return fmt.Errorf("simulated failure")
			}
			writePackManifest(t, filepath.Join(req.ConfigDir, "packs", req.Name), req.Name)
			return nil
		}
		var out bytes.Buffer
		results, _ := PackInstallMissing(context.Background(), PackInstallMissingRequest{
			ConfigDir: configDir, ProfileName: "test", PackInstallFn: fake,
		}, &out)
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
		want := []MissingStatus{MissingStatusInstalled, MissingStatusError, MissingStatusInstalled}
		for i, r := range results {
			if r.Status != want[i] {
				t.Errorf("[%d] %s: got %q, want %q", i, r.Pack, r.Status, want[i])
			}
		}
	})
}

// TestPackRename_RebuildsResolvedInventory asserts that after renaming a
// pack, the lockfile entry under the new name has a non-nil Resolved
// inventory. Also verifies the backfill case: a rename of a pre-v0.22
// install (whose lockfile entry lacks Resolved) produces a populated
// baseline under the new name. Regression guard for finding #3 in
// projects/aipack-pack-update-bugs-2026-04-14.md.
func TestPackRename_RebuildsResolvedInventory(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	configDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePackManifest(t, packDir, "rename-src")
	if err := os.WriteFile(filepath.Join(packDir, "rules", "rule-a.md"), []byte("# rule-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PackInstall(context.Background(), PackInstallRequest{
		PackPath: packDir, ConfigDir: configDir, Link: false,
	}, &out); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Simulate pre-v0.22: clear Resolved so the rename path has to
	// backfill rather than carry forward.
	lfPath := config.LockfilePath(configDir)
	lf, err := config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["rename-src"]
	meta.Resolved = nil
	lf.Packs["rename-src"] = meta
	if err := config.SaveLockfile(lfPath, lf); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(nil, nil)
	if err := PackRename(eng, configDir, "rename-src", "rename-dst", &out); err != nil {
		t.Fatalf("PackRename: %v", err)
	}

	lf, err = config.LoadLockfile(lfPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lf.Packs["rename-src"]; ok {
		t.Error("old name still in lockfile")
	}
	got, ok := lf.Packs["rename-dst"]
	if !ok {
		t.Fatal("new name missing from lockfile")
	}
	if got.Resolved == nil {
		t.Fatal("Resolved is nil after rename — rebuild did not fire")
	}
	if !slices.Contains(got.Resolved.Rules, "rule-a") {
		t.Errorf("Resolved.Rules = %v, want to contain rule-a", got.Resolved.Rules)
	}
}
