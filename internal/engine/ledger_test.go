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
	path := LedgerPathForScope(domain.ScopeProject, "/home/user/proj", "/home/user", "claudecode")
	want := "/home/user/.config/aipack/ledger/-home-user-proj/claudecode.json"
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestLedgerPathForScope_Global(t *testing.T) {
	t.Parallel()
	path := LedgerPathForScope(domain.ScopeGlobal, "/home/user/proj", "/home/user", "claudecode")
	want := "/home/user/.config/aipack/ledger/claudecode.json"
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestEncodeProjectPath(t *testing.T) {
	t.Parallel()
	got := EncodeProjectPath("/Users/foo/bar")
	want := "-Users-foo-bar"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMigrateOldLedgers_CombinedToPerHarness(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ledgerDir := filepath.Join(home, ".config", "aipack", "ledger")
	if err := os.MkdirAll(ledgerDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a combined ledger with entries for two harnesses.
	combined := domain.NewLedger()
	combined.Managed[filepath.Join(home, ".claude", "rules", "a.md")] = domain.Entry{Digest: "aaa", SourcePack: "p1"}
	combined.Managed[filepath.Join(home, ".opencode", "rules", "b.md")] = domain.Entry{Digest: "bbb", SourcePack: "p1"}
	if err := SaveLedger(filepath.Join(ledgerDir, "claudecode+opencode.json"), combined, false); err != nil {
		t.Fatal(err)
	}

	roots := map[string][]string{
		"claudecode": {filepath.Join(home, ".claude")},
		"opencode":   {filepath.Join(home, ".opencode")},
	}

	n, err := MigrateOldLedgers(domain.ScopeGlobal, "", home, []string{"claudecode", "opencode"}, roots)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("migrated = %d, want 2", n)
	}

	// Verify per-harness ledgers.
	cc, _, _ := LoadLedger(LedgerPathForScope(domain.ScopeGlobal, "", home, "claudecode"))
	if len(cc.Managed) != 1 {
		t.Errorf("claudecode entries = %d, want 1", len(cc.Managed))
	}
	oc, _, _ := LoadLedger(LedgerPathForScope(domain.ScopeGlobal, "", home, "opencode"))
	if len(oc.Managed) != 1 {
		t.Errorf("opencode entries = %d, want 1", len(oc.Managed))
	}
}

func TestMigrateOldLedgers_ProjectLocal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "myproject")

	// Write an old project-local ledger.
	oldPath := filepath.Join(projectDir, ".aipack", "ledger.json")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	old := domain.NewLedger()
	old.Managed[filepath.Join(projectDir, ".claude", "rules", "x.md")] = domain.Entry{Digest: "xxx", SourcePack: "p1"}
	if err := SaveLedger(oldPath, old, false); err != nil {
		t.Fatal(err)
	}

	roots := map[string][]string{
		"claudecode": {filepath.Join(projectDir, ".claude")},
	}

	n, err := MigrateOldLedgers(domain.ScopeProject, projectDir, home, []string{"claudecode"}, roots)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("migrated = %d, want 1", n)
	}

	cc, _, _ := LoadLedger(LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode"))
	if len(cc.Managed) != 1 {
		t.Errorf("claudecode entries = %d, want 1", len(cc.Managed))
	}
}

func TestMigrateOldLedgers_NoOldFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	roots := map[string][]string{
		"claudecode": {filepath.Join(home, ".claude")},
	}

	n, err := MigrateOldLedgers(domain.ScopeGlobal, "", home, []string{"claudecode"}, roots)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("migrated = %d, want 0", n)
	}
}
