package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestLoadLedger_NonExistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	lg, warn, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if lg.Managed == nil {
		t.Error("Managed should be initialized")
	}
	if len(lg.Managed) != 0 {
		t.Errorf("Managed = %d, want 0", len(lg.Managed))
	}
}

func TestSaveLedger_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	lg := domain.NewLedger()
	lg.Record("/tmp/test-file", []byte("hello"), "pack1", nil, time.Now())

	if err := SaveLedger(path, lg, false); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Managed) != 1 {
		t.Fatalf("Managed = %d, want 1", len(loaded.Managed))
	}

	e, ok := loaded.Managed["/tmp/test-file"]
	if !ok {
		t.Fatal("expected /tmp/test-file in ledger")
	}
	if e.Digest == "" {
		t.Error("Digest should be set")
	}
	if e.SourcePack != "pack1" {
		t.Errorf("SourcePack = %q", e.SourcePack)
	}
}

func TestSaveLedger_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	lg := domain.NewLedger()
	lg.Record("/tmp/test-file", []byte("hello"), "pack1", nil, time.Now())

	if err := SaveLedger(path, lg, true); err != nil {
		t.Fatal(err)
	}

	// File should NOT exist after dry run.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist after dry run")
	}
}

func TestSaveLedger_SortedKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	lg := domain.NewLedger()
	lg.Record("/z/file", []byte("z"), "pack1", nil, time.Now())
	lg.Record("/a/file", []byte("a"), "pack1", nil, time.Now())
	lg.Record("/m/file", []byte("m"), "pack1", nil, time.Now())

	if err := SaveLedger(path, lg, false); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}

	// Verify schema_version and tool fields.
	var sv int
	if err := json.Unmarshal(raw["schema_version"], &sv); err != nil {
		t.Fatal(err)
	}
	if sv != 1 {
		t.Errorf("schema_version = %d, want 1", sv)
	}
}

func TestSaveLedger_V1Compat(t *testing.T) {
	t.Parallel()
	// Simulate a v1 ledger JSON and verify v2 can read it.
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	v1JSON := `{
  "schema_version": 1,
  "updated_at_epoch_s": 1709500000,
  "tool": "aipack",
  "managed": {
    "/home/user/.claude/rules/alpha.md": {
      "digest": "abc123",
      "synced_at_epoch_s": 1709500000,
      "mtime_epoch_s": 1709500000.5,
      "source_pack": "example-pack"
    }
  }
}`
	if err := os.WriteFile(path, []byte(v1JSON), 0o644); err != nil {
		t.Fatal(err)
	}

	lg, _, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lg.Managed) != 1 {
		t.Fatalf("Managed = %d, want 1", len(lg.Managed))
	}
	e := lg.Managed["/home/user/.claude/rules/alpha.md"]
	if e.Digest != "abc123" {
		t.Errorf("Digest = %q", e.Digest)
	}
	if e.SourcePack != "example-pack" {
		t.Errorf("SourcePack = %q", e.SourcePack)
	}
}

func TestLoadLedger_CorruptJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json at all {{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	lg, warn, err := LoadLedger(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn == "" {
		t.Error("expected warning for corrupt JSON, got empty")
	}
	if lg.Managed == nil {
		t.Error("Managed should be initialized even for corrupt ledger")
	}
	if len(lg.Managed) != 0 {
		t.Errorf("Managed = %d, want 0 for corrupt ledger", len(lg.Managed))
	}
}

func TestLedgerPathForScope_Project(t *testing.T) {
	t.Parallel()
	path := LedgerPathForScope(domain.ScopeProject, "/home/user/proj", "/home/user", []string{"claudecode"})
	if path != "/home/user/proj/.aipack/ledger.json" {
		t.Errorf("path = %q", path)
	}
}

func TestLedgerPathForScope_Global(t *testing.T) {
	t.Parallel()
	path := LedgerPathForScope(domain.ScopeGlobal, "/home/user/proj", "/home/user", []string{"claudecode", "opencode"})
	expected := "/home/user/.config/aipack/ledger/claudecode+opencode.json"
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}
