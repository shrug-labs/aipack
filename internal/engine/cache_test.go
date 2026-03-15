package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCacheKey(t *testing.T) {
	t.Parallel()
	key := settingsCacheKey(domain.HarnessClaudeCode, "/home/user/.claude/settings.local.json")
	want := "claudecode--.claude--settings.local.json"
	if key != want {
		t.Errorf("key = %q, want %q", key, want)
	}
}

func TestPresyncDirUsesLedgerSpecificNamespace(t *testing.T) {
	t.Parallel()

	ledgerPath := filepath.Join("/tmp", "ledger", "codex.json")
	got := presyncDir(ledgerPath)
	want := filepath.Join("/tmp", "ledger", "codex-"+cacheSubdirName)
	if got != want {
		t.Fatalf("presyncDir = %q, want %q", got, want)
	}
}

func TestSnapshotSettingsFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	// Create existing settings file on disk.
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.local.json")
	original := []byte(`{"user_pref": true}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	settings := []domain.SettingsAction{{
		Dst:     settingsPath,
		Desired: []byte(`{"managed": true}`),
		Harness: domain.HarnessClaudeCode,
		Label:   "settings.local.json",
	}}

	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}

	// Verify presync cache written.
	cacheDir := presyncDir(ledgerPath)
	cached, err := os.ReadFile(filepath.Join(cacheDir, "claudecode--.claude--settings.local.json"))
	if err != nil {
		t.Fatalf("presync cache not written: %v", err)
	}
	if string(cached) != string(original) {
		t.Errorf("presync = %q, want %q", cached, original)
	}

	// Verify index written.
	idx, _, err := loadPresyncIndex(cacheDir)
	if err != nil {
		t.Fatalf("loading index: %v", err)
	}
	origPath, ok := idx["claudecode--.claude--settings.local.json"]
	if !ok {
		t.Fatal("index missing key")
	}
	if origPath != settingsPath {
		t.Errorf("index path = %q, want %q", origPath, settingsPath)
	}
}

func TestSnapshotSettingsFiles_DryRunNoWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := []domain.SettingsAction{{
		Dst:     settingsPath,
		Desired: []byte(`{}`),
		Harness: domain.HarnessClaudeCode,
		Label:   "settings.json",
	}}

	if _, err := SnapshotSettingsFiles(settings, ledgerPath, true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(presyncDir(ledgerPath)); !os.IsNotExist(err) {
		t.Error("presync dir should not exist after dry run")
	}
}

func TestSnapshotSettingsFiles_NoFileOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	settings := []domain.SettingsAction{{
		Dst:     filepath.Join(dir, "nonexistent.json"),
		Desired: []byte(`{}`),
		Harness: domain.HarnessClaudeCode,
		Label:   "nonexistent.json",
	}}

	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFromCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	// Set up a settings file and snapshot it.
	settingsPath := filepath.Join(dir, "settings.json")
	original := []byte(`{"original": true}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	settings := []domain.SettingsAction{{
		Dst:     settingsPath,
		Desired: []byte(`{"managed": true}`),
		Harness: domain.HarnessClaudeCode,
		Label:   "settings.json",
	}}
	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}

	// Overwrite the settings file (simulates sync writing to it).
	if err := os.WriteFile(settingsPath, []byte(`{"managed": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore from presync cache.
	restored, err := RestoreFromCache(ledgerPath, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored = %d, want 1", len(restored))
	}

	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("restored content = %q, want %q", got, original)
	}
}

func TestRestoreFromCache_DryRunNoWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := []domain.SettingsAction{{
		Dst:     settingsPath,
		Desired: []byte(`{}`),
		Harness: domain.HarnessClaudeCode,
		Label:   "settings.json",
	}}
	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}

	// Overwrite.
	if err := os.WriteFile(settingsPath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry-run restore should NOT change the file.
	restored, err := RestoreFromCache(ledgerPath, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 {
		t.Fatalf("dry-run should still report files: got %d", len(restored))
	}
	got, _ := os.ReadFile(settingsPath)
	if string(got) != "changed" {
		t.Errorf("dry-run should not modify file: got %q", got)
	}
}

func TestRestoreFromCache_EmptyCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	restored, err := RestoreFromCache(ledgerPath, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 0 {
		t.Errorf("expected empty, got %d", len(restored))
	}
}

func TestSettingsCache_EndToEnd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	settingsPath := filepath.Join(dir, "settings.json")
	original := []byte(`{"user_only": "keep_me"}` + "\n")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")
	settings := []domain.SettingsAction{{
		Dst:     settingsPath,
		Desired: []byte(`{"managed": true}` + "\n"),
		Harness: domain.HarnessClaudeCode,
		Label:   "settings.json",
	}}

	// Snapshot original, then simulate sync overwrite.
	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, settings[0].Desired, 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify settings changed.
	got, _ := os.ReadFile(settingsPath)
	if string(got) == string(original) {
		t.Fatal("settings should have changed after sync")
	}

	// Restore gets back pre-sync state.
	restored, err := RestoreFromCache(ledgerPath, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored count = %d", len(restored))
	}
	got, _ = os.ReadFile(settingsPath)
	if string(got) != string(original) {
		t.Errorf("after restore = %q, want %q", got, original)
	}
}

func TestRestoreFromCache_HarnessFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, ".aipack", "ledger.json")

	// Create two settings files for different harnesses.
	ccPath := filepath.Join(dir, "cc-settings.json")
	ocPath := filepath.Join(dir, "oc-settings.json")
	if err := os.WriteFile(ccPath, []byte("cc-original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ocPath, []byte("oc-original"), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := []domain.SettingsAction{
		{Dst: ccPath, Desired: []byte("{}"), Harness: domain.HarnessClaudeCode, Label: "cc-settings.json"},
		{Dst: ocPath, Desired: []byte("{}"), Harness: domain.HarnessOpenCode, Label: "oc-settings.json"},
	}
	if _, err := SnapshotSettingsFiles(settings, ledgerPath, false); err != nil {
		t.Fatal(err)
	}

	// Overwrite both.
	if err := os.WriteFile(ccPath, []byte("cc-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ocPath, []byte("oc-changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore only claudecode.
	restored, err := RestoreFromCache(ledgerPath, "claudecode", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 {
		t.Fatalf("filtered restore = %d, want 1", len(restored))
	}

	// cc should be restored, oc should remain changed.
	ccGot, _ := os.ReadFile(ccPath)
	if string(ccGot) != "cc-original" {
		t.Errorf("cc = %q, want %q", ccGot, "cc-original")
	}
	ocGot, _ := os.ReadFile(ocPath)
	if string(ocGot) != "oc-changed" {
		t.Errorf("oc should remain changed: %q", ocGot)
	}
}
