package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvSetAndUnset_RoundTrip(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if err := EnvSet(configDir, "API_TOKEN", "abc123"); err != nil {
		t.Fatalf("EnvSet: %v", err)
	}
	value, ok, err := EnvGet(configDir, "API_TOKEN")
	if err != nil {
		t.Fatalf("EnvGet: %v", err)
	}
	if !ok || value != "abc123" {
		t.Fatalf("EnvGet = (%q, %v), want (abc123, true)", value, ok)
	}

	if err := EnvUnset(configDir, "API_TOKEN"); err != nil {
		t.Fatalf("EnvUnset: %v", err)
	}
	_, ok, _ = EnvGet(configDir, "API_TOKEN")
	if ok {
		t.Fatalf("EnvGet after unset should be false")
	}
}

func TestEnvList_MasksValuesUnlessRequested(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("ALPHA=one\nBRAVO=longer-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	masked, err := EnvList(configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(masked.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(masked.Entries))
	}
	for _, e := range masked.Entries {
		if e.Value != "" {
			t.Fatalf("masked entry %s leaked value %q", e.Key, e.Value)
		}
		if e.Length == 0 {
			t.Fatalf("masked entry %s should report length", e.Key)
		}
	}
	// Sort order is alphabetical so output is stable for screenshots/help text.
	if masked.Entries[0].Key != "ALPHA" || masked.Entries[1].Key != "BRAVO" {
		t.Fatalf("entries should be sorted alphabetically, got %+v", masked.Entries)
	}

	revealed, err := EnvList(configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Entries[0].Value != "one" || revealed.Entries[1].Value != "longer-value" {
		t.Fatalf("revealed entries = %+v", revealed.Entries)
	}
}

func TestEnvSet_RejectsInvalidKey(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	if err := EnvSet(configDir, "1bad", "x"); err == nil {
		t.Fatal("expected invalid key error")
	}
	if err := EnvSet(configDir, "WITH SPACE", "x"); err == nil {
		t.Fatal("expected invalid key error for space-bearing key")
	}
}

func TestEnvList_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	result, err := EnvList(configDir, false)
	if err != nil {
		t.Fatalf("EnvList: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("missing .env should produce empty entries, got %+v", result.Entries)
	}
	if result.Path == "" {
		t.Fatalf("Path should always populate, even when file missing")
	}
}
