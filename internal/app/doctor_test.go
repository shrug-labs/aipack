package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestDoctorExtractEnvRefNames_Single(t *testing.T) {
	t.Parallel()
	names := doctorExtractEnvRefNames("{env:HOME}/bin")
	if len(names) != 1 {
		t.Fatalf("expected 1 name, got %d: %v", len(names), names)
	}
	if names[0] != "HOME" {
		t.Errorf("names[0] = %q, want %q", names[0], "HOME")
	}
}

func TestDoctorExtractEnvRefNames_Multiple(t *testing.T) {
	t.Parallel()
	names := doctorExtractEnvRefNames("{env:A}{env:B}")
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d: %v", len(names), names)
	}
	// Result is sorted.
	if names[0] != "A" {
		t.Errorf("names[0] = %q, want %q", names[0], "A")
	}
	if names[1] != "B" {
		t.Errorf("names[1] = %q, want %q", names[1], "B")
	}
}

func TestDoctorExtractEnvRefNames_None(t *testing.T) {
	t.Parallel()
	names := doctorExtractEnvRefNames("/usr/bin")
	if len(names) != 0 {
		t.Errorf("expected 0 names, got %d: %v", len(names), names)
	}
}

func TestDoctorSkippedCheck(t *testing.T) {
	t.Parallel()
	cr := doctorSkippedCheck("test-check", "reason")

	if cr.Name != "test-check" {
		t.Errorf("Name = %q, want %q", cr.Name, "test-check")
	}
	if cr.OK != false {
		t.Errorf("OK = %v, want false", cr.OK)
	}
	if cr.Status != "skip" {
		t.Errorf("Status = %q, want %q", cr.Status, "skip")
	}
	if cr.Severity != "critical" {
		t.Errorf("Severity = %q, want %q", cr.Severity, "critical")
	}
	if cr.Message != "skipped: reason" {
		t.Errorf("Message = %q, want %q", cr.Message, "skipped: reason")
	}
}

func TestDoctorCheckGit_Available(t *testing.T) {
	t.Parallel()
	// On any CI or dev machine with git installed, this should pass.
	cr := doctorCheckGit()
	if !cr.OK {
		t.Skipf("git not available in this environment: %s", cr.Message)
	}
	if cr.Name != "git_available" {
		t.Errorf("Name = %q, want %q", cr.Name, "git_available")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
	if cr.Severity != "warning" {
		t.Errorf("Severity = %q, want %q", cr.Severity, "warning")
	}
}

func TestDoctorCheckUnregisteredPacks_AllRegistered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, "alpha"), 0o755)
	os.MkdirAll(filepath.Join(packsDir, "beta"), 0o755)

	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"alpha": {},
			"beta":  {},
		},
	}
	cr := doctorCheckUnregisteredPacks(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
}

func TestDoctorCheckUnregisteredPacks_SomeUnregistered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, "alpha"), 0o755)
	os.MkdirAll(filepath.Join(packsDir, "beta"), 0o755)
	os.MkdirAll(filepath.Join(packsDir, "gamma"), 0o755)

	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"alpha": {},
		},
	}
	cr := doctorCheckUnregisteredPacks(dir, syncCfg)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	if cr.Status != "warn" {
		t.Errorf("Status = %q, want %q", cr.Status, "warn")
	}
	unreg, ok := cr.Details["unregistered"].([]string)
	if !ok {
		t.Fatal("missing unregistered details")
	}
	if len(unreg) != 2 {
		t.Fatalf("expected 2 unregistered, got %d: %v", len(unreg), unreg)
	}
	if unreg[0] != "beta" || unreg[1] != "gamma" {
		t.Errorf("unregistered = %v, want [beta gamma]", unreg)
	}
}

func TestDoctorCheckUnregisteredPacks_NoPacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No packs/ directory at all.
	syncCfg := config.SyncConfig{}
	cr := doctorCheckUnregisteredPacks(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Message != "no packs directory" {
		t.Errorf("Message = %q, want %q", cr.Message, "no packs directory")
	}
}

func TestDoctorCheckUnregisteredPacks_SkipsDotDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, ".hidden"), 0o755)

	syncCfg := config.SyncConfig{}
	cr := doctorCheckUnregisteredPacks(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
}

func TestDoctorCheckPackDrift_NoDrift(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"alpha": {Method: config.MethodLink},
		},
	}
	// link packs never drift
	cr := doctorCheckPackDrift(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
}

func TestDoctorCheckPackDrift_CopyVersionDrift(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create installed pack with version 1.0.0
	packDir := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(packDir, 0o755)
	writePackJSON(t, packDir, "1.0.0")

	// Create origin with version 2.0.0
	originDir := t.TempDir()
	writePackJSON(t, originDir, "2.0.0")

	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"mypack": {Method: config.MethodCopy, Origin: originDir},
		},
	}

	cr := doctorCheckPackDrift(dir, syncCfg)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	if cr.Status != "warn" {
		t.Errorf("Status = %q, want %q", cr.Status, "warn")
	}
	drifted, ok := cr.Details["drifted"].([]PackDrift)
	if !ok {
		t.Fatal("missing drifted details")
	}
	if len(drifted) != 1 {
		t.Fatalf("expected 1 drifted, got %d", len(drifted))
	}
	if drifted[0].InstalledVersion != "1.0.0" || drifted[0].OriginVersion != "2.0.0" {
		t.Errorf("drift = %s -> %s, want 1.0.0 -> 2.0.0", drifted[0].InstalledVersion, drifted[0].OriginVersion)
	}
}

func TestDoctorCheckPackDrift_CopyNoOrigin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	packDir := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(packDir, 0o755)
	writePackJSON(t, packDir, "1.0.0")

	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"mypack": {Method: config.MethodCopy, Origin: "/nonexistent/path"},
		},
	}

	cr := doctorCheckPackDrift(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true (inaccessible origin should be skipped)")
	}
}

func TestDoctorCheckPackDrift_CopySameVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	packDir := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(packDir, 0o755)
	writePackJSON(t, packDir, "1.0.0")

	originDir := t.TempDir()
	writePackJSON(t, originDir, "1.0.0")

	syncCfg := config.SyncConfig{
		InstalledPacks: map[string]config.InstalledPackMeta{
			"mypack": {Method: config.MethodCopy, Origin: originDir},
		},
	}

	cr := doctorCheckPackDrift(dir, syncCfg)
	if !cr.OK {
		t.Errorf("OK = false, want true (same version = no drift)")
	}
}

// ---------------------------------------------------------------------------
// Ledger health tests
// ---------------------------------------------------------------------------

func TestDoctorCheckLedgerHealth_NoLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cr := doctorCheckLedgerHealth(dir, nil, false)
	if !cr.OK {
		t.Errorf("OK = false, want true (no ledger dir)")
	}
	if cr.Message != "no ledger directory" {
		t.Errorf("Message = %q", cr.Message)
	}
}

func TestDoctorCheckLedgerHealth_OrphanedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	os.MkdirAll(ledgerDir, 0o755)

	// Create a real file for one entry, leave the other orphaned.
	realFile := filepath.Join(dir, "exists.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "mypack", Digest: "abc"}
	lg.Managed["/nonexistent/path/rule.md"] = domain.Entry{SourcePack: "mypack", Digest: "def"}
	engine.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	// Without fix: reports but doesn't change.
	cr := doctorCheckLedgerHealth(dir, nil, false)
	if cr.OK {
		t.Errorf("OK = true, want false (has orphans)")
	}
	if cr.Details["orphaned"] != 1 {
		t.Errorf("orphaned = %v, want 1", cr.Details["orphaned"])
	}

	// With fix: prunes orphan.
	cr = doctorCheckLedgerHealth(dir, nil, true)
	if !cr.OK {
		t.Errorf("OK = false, want true after fix")
	}
	if !cr.Fixed {
		t.Error("Fixed = false, want true")
	}

	// Verify ledger was actually modified.
	lg2, _, _ := engine.LoadLedger(filepath.Join(ledgerDir, "test.json"))
	if _, ok := lg2.Managed["/nonexistent/path/rule.md"]; ok {
		t.Error("orphaned entry still in ledger after fix")
	}
	if _, ok := lg2.Managed[realFile]; !ok {
		t.Error("valid entry was incorrectly removed")
	}
}

func TestDoctorCheckLedgerHealth_MissingSourcePack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	os.MkdirAll(ledgerDir, 0o755)

	realFile := filepath.Join(dir, "rule.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "", Digest: "abc"}
	engine.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	singlePack := []config.ResolvedPack{{Name: "only-pack"}}

	// Without fix: reports.
	cr := doctorCheckLedgerHealth(dir, singlePack, false)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	if cr.Details["missing_source_pack"] != 1 {
		t.Errorf("missing_source_pack = %v, want 1", cr.Details["missing_source_pack"])
	}

	// With fix and single pack: fills it in.
	cr = doctorCheckLedgerHealth(dir, singlePack, true)
	if !cr.Fixed {
		t.Error("Fixed = false, want true")
	}

	lg2, _, _ := engine.LoadLedger(filepath.Join(ledgerDir, "test.json"))
	if lg2.Managed[realFile].SourcePack != "only-pack" {
		t.Errorf("SourcePack = %q, want only-pack", lg2.Managed[realFile].SourcePack)
	}
}

func TestDoctorCheckLedgerHealth_MissingSourcePackMultiplePacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	os.MkdirAll(ledgerDir, 0o755)

	realFile := filepath.Join(dir, "rule.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "", Digest: "abc"}
	engine.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	multiplePacks := []config.ResolvedPack{{Name: "a"}, {Name: "b"}}

	// With fix and multiple packs: can't auto-fill, reports but doesn't fix.
	cr := doctorCheckLedgerHealth(dir, multiplePacks, true)
	if cr.Fixed {
		t.Error("Fixed = true, want false (ambiguous)")
	}
}

// ---------------------------------------------------------------------------
// Manifest drift tests
// ---------------------------------------------------------------------------

func TestDoctorCheckManifestDrift_NoDrift(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packRoot := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "alpha.md"), []byte("x"), 0o600)

	packs := []config.ResolvedPack{{
		Name: "mypack",
		Root: packRoot,
		Manifest: config.PackManifest{
			Rules: []string{"alpha"},
		},
	}}
	cr := doctorCheckManifestDrift(dir, packs)
	if !cr.OK {
		t.Errorf("OK = false, want true (no drift)")
	}
}

func TestDoctorCheckManifestDrift_Undeclared(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packRoot := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "alpha.md"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(packRoot, "rules", "beta.md"), []byte("x"), 0o600)

	packs := []config.ResolvedPack{{
		Name: "mypack",
		Root: packRoot,
		Manifest: config.PackManifest{
			Rules: []string{"alpha"}, // beta is on disk but not declared
		},
	}}
	cr := doctorCheckManifestDrift(dir, packs)
	if cr.OK {
		t.Errorf("OK = true, want false (undeclared content)")
	}
	if cr.Status != "warn" {
		t.Errorf("Status = %q, want warn", cr.Status)
	}
}

func TestDoctorCheckManifestDrift_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packRoot := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)

	packs := []config.ResolvedPack{{
		Name: "mypack",
		Root: packRoot,
		Manifest: config.PackManifest{
			Rules: []string{"ghost"}, // declared but not on disk
		},
	}}
	cr := doctorCheckManifestDrift(dir, packs)
	if cr.OK {
		t.Errorf("OK = true, want false (missing content)")
	}
}

func writePackJSON(t *testing.T, dir, version string) {
	t.Helper()
	m := map[string]any{
		"schema_version": 1,
		"name":           filepath.Base(dir),
		"version":        version,
		"root":           ".",
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
