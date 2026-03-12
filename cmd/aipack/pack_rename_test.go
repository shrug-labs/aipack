package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestPackRename_FullLifecycle(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Install a pack.
	packSrc := t.TempDir()
	writePackManifestCmd(t, packSrc, "old-name")
	_, _, code := runApp(t, "pack", "install", packSrc, "--config-dir", configDir, "--no-register")
	if code != cmdutil.ExitOK {
		t.Fatalf("install exit=%d", code)
	}

	// Create a profile referencing the pack.
	profDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profContent := "schema_version: 2\npacks:\n  - name: old-name\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(profDir, "default.yaml"), []byte(profContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a ledger with SourcePack=old-name.
	ledgerDir := filepath.Join(configDir, "ledger")
	if err := os.MkdirAll(ledgerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Managed["/some/path/rule.md"] = domain.Entry{SourcePack: "old-name", Digest: "abc"}
	if err := engine.SaveLedger(filepath.Join(ledgerDir, "claudecode.json"), lg, false); err != nil {
		t.Fatal(err)
	}

	// Rename.
	_, stderr, code := runApp(t, "pack", "rename", "old-name", "new-name", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("rename exit=%d; stderr=%s", code, stderr)
	}

	// Verify directory moved.
	if _, err := os.Stat(filepath.Join(configDir, "packs", "old-name")); !os.IsNotExist(err) {
		t.Error("old directory still exists")
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "new-name", "pack.json")); err != nil {
		t.Error("new directory missing pack.json")
	}

	// Verify manifest name updated.
	m, err := config.LoadPackManifest(filepath.Join(configDir, "packs", "new-name", "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "new-name" {
		t.Errorf("manifest name = %q, want new-name", m.Name)
	}

	// Verify sync-config updated.
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sc.InstalledPacks["old-name"]; ok {
		t.Error("sync-config still has old-name")
	}
	if _, ok := sc.InstalledPacks["new-name"]; !ok {
		t.Error("sync-config missing new-name")
	}

	// Verify profile updated.
	prof, err := config.LoadProfile(filepath.Join(profDir, "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prof.Packs {
		if p.Name == "old-name" {
			t.Error("profile still references old-name")
		}
	}
	found := false
	for _, p := range prof.Packs {
		if p.Name == "new-name" {
			found = true
		}
	}
	if !found {
		t.Error("profile missing new-name")
	}

	// Verify ledger updated.
	lg2, _, err := engine.LoadLedger(filepath.Join(ledgerDir, "claudecode.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range lg2.Managed {
		if entry.SourcePack == "old-name" {
			t.Error("ledger still has SourcePack=old-name")
		}
		if entry.SourcePack != "new-name" {
			t.Errorf("ledger SourcePack=%q, want new-name", entry.SourcePack)
		}
	}
}

func TestPackRename_NonexistentPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "packs"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, code := runApp(t, "pack", "rename", "ghost", "new", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("rename of nonexistent pack should fail")
	}
}

func TestPackRename_TargetExists(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	packSrc1 := t.TempDir()
	writePackManifestCmd(t, packSrc1, "pack-a")
	packSrc2 := t.TempDir()
	writePackManifestCmd(t, packSrc2, "pack-b")

	_, _, _ = runApp(t, "pack", "install", packSrc1, "--config-dir", configDir, "--no-register")
	_, _, _ = runApp(t, "pack", "install", packSrc2, "--config-dir", configDir, "--no-register")

	_, _, code := runApp(t, "pack", "rename", "pack-a", "pack-b", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("rename to existing name should fail")
	}
}

func TestPackRename_SameName(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "pack", "rename", "same", "same", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("rename to same name should fail")
	}
}

func TestPackRename_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "rename", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("rename --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}
