package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestDoctorRequiredMCPRefs_EnvAndParams(t *testing.T) {
	t.Parallel()
	params := map[string]string{"base": "/usr/local"}
	inv := map[string]domain.MCPServer{
		"myserver": {
			Command: []string{"{params.base}/bin/server", "--token={env:TOKEN}"},
			Env:     map[string]string{"HOME": "{env:HOME}"},
		},
	}
	missing, requiredBy := doctorRequiredMCPRefs(params, inv, []string{"myserver"})

	// TOKEN and HOME should be listed as required env refs.
	if _, ok := requiredBy["env:TOKEN"]; !ok {
		t.Error("expected env:TOKEN in requiredBy")
	}
	if _, ok := requiredBy["env:HOME"]; !ok {
		t.Error("expected env:HOME in requiredBy")
	}
	// params.base is available, so should NOT appear.
	if _, ok := requiredBy["param:base"]; ok {
		t.Error("param:base should not appear (it's available)")
	}

	// Both env vars are likely unset in test, so should be missing.
	hasMissing := false
	for _, m := range missing {
		if m == "env:TOKEN" || m == "env:HOME" {
			hasMissing = true
		}
	}
	if !hasMissing {
		t.Errorf("expected at least one missing env ref, got %v", missing)
	}
}

func TestDoctorRequiredMCPRefs_MissingParam(t *testing.T) {
	t.Parallel()
	params := map[string]string{} // no params
	inv := map[string]domain.MCPServer{
		"myserver": {
			Command: []string{"{params.missing_key}/bin/server"},
		},
	}
	missing, requiredBy := doctorRequiredMCPRefs(params, inv, []string{"myserver"})

	if _, ok := requiredBy["param:missing_key"]; !ok {
		t.Error("expected param:missing_key in requiredBy")
	}
	found := false
	for _, m := range missing {
		if m == "param:missing_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected param:missing_key in missing, got %v", missing)
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

	packs := map[string]config.InstalledPackMeta{
		"alpha": {},
		"beta":  {},
	}
	cr := doctorCheckUnregisteredPacks(dir, packs)
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

	packs := map[string]config.InstalledPackMeta{
		"alpha": {},
	}
	cr := doctorCheckUnregisteredPacks(dir, packs)
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
	cr := doctorCheckUnregisteredPacks(dir, nil)
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

	cr := doctorCheckUnregisteredPacks(dir, nil)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
}

func TestDoctorCheckOrphanedInstallEntries_None(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, "my-pack"), 0o755)

	lf := &config.Lockfile{Packs: map[string]config.InstalledPackMeta{
		"my-pack": {Method: config.MethodCopy},
	}}
	cr := doctorCheckOrphanedInstallEntries(dir, lf, false)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
}

func TestDoctorCheckOrphanedInstallEntries_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "packs"), 0o755)

	lf := &config.Lockfile{Packs: map[string]config.InstalledPackMeta{
		"gone-pack": {Method: config.MethodCopy},
	}}
	cr := doctorCheckOrphanedInstallEntries(dir, lf, false)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	orphaned := cr.Details["orphaned"].([]string)
	if len(orphaned) != 1 || orphaned[0] != "gone-pack" {
		t.Errorf("orphaned = %v, want [gone-pack]", orphaned)
	}
}

func TestDoctorCheckOrphanedInstallEntries_Fix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "packs"), 0o755)

	lf := &config.Lockfile{
		LockVersion: 1,
		Packs: map[string]config.InstalledPackMeta{
			"gone-pack": {Method: config.MethodCopy},
		},
	}

	cr := doctorCheckOrphanedInstallEntries(dir, lf, true)
	if cr.Status != "fixed" {
		t.Errorf("Status = %q, want %q", cr.Status, "fixed")
	}
	// Verify entry was removed from persisted lockfile and LockVersion preserved.
	persisted, err := config.LoadLockfile(config.LockfilePath(dir))
	if err != nil {
		t.Fatalf("reload lockfile: %v", err)
	}
	if _, ok := persisted.Packs["gone-pack"]; ok {
		t.Error("gone-pack should be removed from lockfile after fix")
	}
	if persisted.LockVersion != 1 {
		t.Errorf("LockVersion = %d, want 1 (must be preserved through fix)", persisted.LockVersion)
	}
}

func TestDoctorCheckStaleBackups_None(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, "my-pack"), 0o755)

	cr := doctorCheckStaleBackups(dir, false)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
	if cr.Status != "pass" {
		t.Errorf("Status = %q, want %q", cr.Status, "pass")
	}
}

func TestDoctorCheckStaleBackups_BackupInPacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	os.MkdirAll(filepath.Join(packsDir, ".my-pack.bak-20260405T120000Z-deadbeef"), 0o755)

	cr := doctorCheckStaleBackups(dir, false)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	stale := cr.Details["stale"].([]string)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale item, got %d", len(stale))
	}
}

func TestDoctorCheckStaleBackups_StagingLeftover(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "packs"), 0o755)
	stagingDir := filepath.Join(dir, ".tmp", "pack-staging")
	os.MkdirAll(filepath.Join(stagingDir, "clone-12345"), 0o755)

	cr := doctorCheckStaleBackups(dir, false)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	stale := cr.Details["stale"].([]string)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale item, got %d", len(stale))
	}
}

func TestDoctorCheckStaleBackups_Fix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packsDir := filepath.Join(dir, "packs")
	bakDir := filepath.Join(packsDir, ".my-pack.bak-20260405T120000Z-deadbeef")
	os.MkdirAll(bakDir, 0o755)
	stagingDir := filepath.Join(dir, ".tmp", "pack-staging")
	cloneDir := filepath.Join(stagingDir, "clone-99999")
	os.MkdirAll(cloneDir, 0o755)

	cr := doctorCheckStaleBackups(dir, true)
	if cr.Status != "fixed" {
		t.Errorf("Status = %q, want %q", cr.Status, "fixed")
	}
	if !cr.Fixed {
		t.Error("Fixed = false, want true")
	}
	if _, err := os.Stat(bakDir); !os.IsNotExist(err) {
		t.Error("backup directory should be removed after fix")
	}
	if _, err := os.Stat(cloneDir); !os.IsNotExist(err) {
		t.Error("staging clone dir should be removed after fix")
	}
}

// nilLsRemote is a no-op ls-remote injection used by drift tests that exercise
// non-clone code paths. Returning ("", nil) signals "no drift signal" — the
// drift check treats this the same as a real network failure and skips the
// pack rather than reporting it as drifted.
func nilLsRemote(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func TestDoctorCheckPackDrift_NoDrift(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"alpha": {Method: config.MethodLink},
	}
	// link packs never drift
	cr := doctorCheckPackDrift(context.Background(), dir, packs, nilLsRemote)
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

	packs := map[string]config.InstalledPackMeta{
		"mypack": {Method: config.MethodCopy, Origin: originDir},
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, nilLsRemote)
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

	packs := map[string]config.InstalledPackMeta{
		"mypack": {Method: config.MethodCopy, Origin: "/nonexistent/path"},
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, nilLsRemote)
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

	packs := map[string]config.InstalledPackMeta{
		"mypack": {Method: config.MethodCopy, Origin: originDir},
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, nilLsRemote)
	if !cr.OK {
		t.Errorf("OK = false, want true (same version = no drift)")
	}
}

// TestDoctorCheckPackDrift_CloneRemoteMoved exercises the clone-method ls-remote
// path: a pack whose recorded commit_hash differs from what ls-remote reports
// for the same ref should produce a drift entry. This is the case the previous
// implementation was unable to detect — clone installs strip .git, so any
// drift signal has to come from the network.
func TestDoctorCheckPackDrift_CloneRemoteMoved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"my-pack": {
			Method:     config.MethodClone,
			Origin:     "https://github.com/example/my-pack",
			Ref:        "main",
			CommitHash: "aaaaaaa1",
		},
	}
	calls := 0
	lsRemote := func(_ context.Context, _, ref string) (string, error) {
		calls++
		if ref != "main" {
			t.Errorf("ls-remote ref = %q, want %q", ref, "main")
		}
		return "bbbbbbb2", nil // remote moved
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, lsRemote)
	if calls != 1 {
		t.Errorf("ls-remote calls = %d, want 1", calls)
	}
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	drifted, ok := cr.Details["drifted"].([]PackDrift)
	if !ok || len(drifted) != 1 {
		t.Fatalf("expected 1 drift entry, got %v", cr.Details["drifted"])
	}
	// Hashes are truncated via util.ShortHash (7 chars, matching git's
	// standard short-SHA convention).
	if drifted[0].InstalledHash != "aaaaaaa" || drifted[0].CurrentHash != "bbbbbbb" {
		t.Errorf("drift hashes = %s -> %s, want aaaaaaa -> bbbbbbb",
			drifted[0].InstalledHash, drifted[0].CurrentHash)
	}
	if drifted[0].Method != config.MethodClone {
		t.Errorf("Method = %q, want clone", drifted[0].Method)
	}
}

func TestDoctorCheckPackDrift_ClonePinnedCommitHashUsesHEAD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"my-pack": {
			Method:     config.MethodClone,
			Origin:     "https://github.com/example/my-pack",
			Ref:        "aabbccdd",
			CommitHash: "aabbccdd",
		},
	}
	calls := 0
	lsRemote := func(_ context.Context, _, ref string) (string, error) {
		calls++
		if ref != "" {
			t.Fatalf("ls-remote ref = %q, want default-branch HEAD lookup", ref)
		}
		return "bbbbbbb2", nil
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, lsRemote)
	if calls != 1 {
		t.Fatalf("ls-remote calls = %d, want 1", calls)
	}
	if cr.OK {
		t.Fatalf("OK = true, want false")
	}
}

// TestDoctorCheckPackDrift_CloneRemoteMatch verifies that a clone pack whose
// recorded commit_hash matches the ls-remote response is not flagged as drift.
func TestDoctorCheckPackDrift_CloneRemoteMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"my-pack": {
			Method:     config.MethodClone,
			Origin:     "https://github.com/example/my-pack",
			Ref:        "v1.2.3",
			CommitHash: "abcdef12",
		},
	}
	lsRemote := func(_ context.Context, _, _ string) (string, error) {
		return "abcdef12", nil
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, lsRemote)
	if !cr.OK {
		t.Errorf("OK = false, want true")
	}
}

// TestDoctorCheckPackDrift_CloneNetworkFailureSkips verifies that an
// ls-remote failure (offline, auth error, timeout) is treated as "no drift
// signal" rather than as drift — offline runs of `aipack doctor` should not
// produce false positives, only the absence of one of the checks.
func TestDoctorCheckPackDrift_CloneNetworkFailureSkips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"my-pack": {
			Method:     config.MethodClone,
			Origin:     "https://example.invalid/my-pack",
			Ref:        "main",
			CommitHash: "aaaaaaa1",
		},
	}
	lsRemote := func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("dial tcp: no such host")
	}

	cr := doctorCheckPackDrift(context.Background(), dir, packs, lsRemote)
	if !cr.OK {
		t.Errorf("OK = false, want true (network failure should not trigger drift)")
	}
}

// TestDoctorCheckPackDrift_PreCancelledContextSkipsLsRemote verifies that the
// dispatch loop's fast-path ctx.Err() check fires on a pre-cancelled context
// and produces zero ls-remote calls. Without the fast-path check, the select
// would non-deterministically pick the semaphore branch when both cases are
// ready, dispatching workers that would each pay one network round-trip
// before failing.
func TestDoctorCheckPackDrift_PreCancelledContextSkipsLsRemote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packs := map[string]config.InstalledPackMeta{
		"pack-a": {Method: config.MethodClone, Origin: "https://example.com/a", Ref: "main", CommitHash: "abc12345"},
		"pack-b": {Method: config.MethodClone, Origin: "https://example.com/b", Ref: "main", CommitHash: "def67890"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	lsRemote := func(_ context.Context, _, _ string) (string, error) {
		calls++
		return "ffffffff", nil
	}

	cr := doctorCheckPackDrift(ctx, dir, packs, lsRemote)
	if calls != 0 {
		t.Errorf("ls-remote called %d times, want 0 (cancelled before dispatch)", calls)
	}
	if !cr.OK {
		t.Errorf("OK = false, want true (no drift collected on cancel)")
	}
}

// TestDoctorCheckPackDrift_CloneRunsConcurrently verifies that ls-remote calls
// for multiple clone packs are issued concurrently rather than sequentially.
// We do this by having each fake ls-remote block on a shared channel that's
// only closed after all goroutines have entered the call — a sequential
// implementation would deadlock at the first pack.
func TestDoctorCheckPackDrift_CloneRunsConcurrently(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const n = 4
	packs := make(map[string]config.InstalledPackMeta, n)
	for i := range n {
		name := string(rune('a'+i)) + "-pack"
		packs[name] = config.InstalledPackMeta{
			Method:     config.MethodClone,
			Origin:     "https://github.com/example/" + name,
			Ref:        "main",
			CommitHash: "abcdef00",
		}
	}

	entered := make(chan struct{}, n)
	release := make(chan struct{})
	lsRemote := func(_ context.Context, _, _ string) (string, error) {
		entered <- struct{}{}
		<-release
		return "abcdef00", nil
	}

	done := make(chan CheckResult, 1)
	go func() {
		done <- doctorCheckPackDrift(context.Background(), dir, packs, lsRemote)
	}()

	// All n goroutines should enter ls-remote before any returns. If the
	// implementation runs sequentially, only one will enter and we deadlock
	// on the channel send (caught by the test timeout).
	for range n {
		<-entered
	}
	close(release)
	cr := <-done
	if !cr.OK {
		t.Errorf("OK = false, want true (all hashes match)")
	}
}

// ---------------------------------------------------------------------------
// Ledger health tests
// ---------------------------------------------------------------------------

func TestDoctorCheckLedgerHealth_NoLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, nil, false)
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
	eng := engine.New(nil, nil)

	// Create a real file for one entry, leave the other orphaned.
	realFile := filepath.Join(dir, "exists.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "mypack", Digest: "abc"}
	lg.Managed["/nonexistent/path/rule.md"] = domain.Entry{SourcePack: "mypack", Digest: "def"}
	eng.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	// Without fix: reports but doesn't change.
	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, nil, false)
	if cr.OK {
		t.Errorf("OK = true, want false (has orphans)")
	}
	if cr.Details["orphaned"] != 1 {
		t.Errorf("orphaned = %v, want 1", cr.Details["orphaned"])
	}

	// With fix: removes orphan.
	cr = doctorCheckLedgerHealth(engine.New(nil, nil), dir, nil, true)
	if !cr.OK {
		t.Errorf("OK = false, want true after fix")
	}
	if !cr.Fixed {
		t.Error("Fixed = false, want true")
	}

	// Verify ledger was actually modified.
	lg2, _, _ := eng.LoadLedger(filepath.Join(ledgerDir, "test.json"))
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
	eng := engine.New(nil, nil)

	realFile := filepath.Join(dir, "rule.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "", Digest: "abc"}
	eng.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	singlePack := []config.ResolvedPack{{Name: "only-pack"}}

	// Without fix: reports.
	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, singlePack, false)
	if cr.OK {
		t.Errorf("OK = true, want false")
	}
	if cr.Details["missing_source_pack"] != 1 {
		t.Errorf("missing_source_pack = %v, want 1", cr.Details["missing_source_pack"])
	}

	// With fix and single pack: fills it in.
	cr = doctorCheckLedgerHealth(engine.New(nil, nil), dir, singlePack, true)
	if !cr.Fixed {
		t.Error("Fixed = false, want true")
	}

	lg2, _, _ := eng.LoadLedger(filepath.Join(ledgerDir, "test.json"))
	if lg2.Managed[realFile].SourcePack != "only-pack" {
		t.Errorf("SourcePack = %q, want only-pack", lg2.Managed[realFile].SourcePack)
	}
}

func TestDoctorCheckLedgerHealth_MissingSourcePackMultiplePacks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	os.MkdirAll(ledgerDir, 0o755)
	eng := engine.New(nil, nil)

	realFile := filepath.Join(dir, "rule.md")
	os.WriteFile(realFile, []byte("x"), 0o600)

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "", Digest: "abc"}
	eng.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false)

	multiplePacks := []config.ResolvedPack{{Name: "a"}, {Name: "b"}}

	// With fix and multiple packs: can't auto-fill, reports but doesn't fix.
	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, multiplePacks, true)
	if cr.Fixed {
		t.Error("Fixed = true, want false (ambiguous)")
	}
}

func TestDoctorCheckLedgerHealth_MCPMissingSourcePack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	if err := os.MkdirAll(ledgerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)

	mcpKey := filepath.Join(dir, ".claude.json") + "#mcp:atlassian"
	lg := domain.NewLedger()
	lg.Managed[mcpKey] = domain.Entry{SourcePack: "", Digest: "abc"}
	if err := eng.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false); err != nil {
		t.Fatal(err)
	}

	singlePack := []config.ResolvedPack{{Name: "only-pack"}}

	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, singlePack, false)
	if cr.OK {
		t.Fatalf("OK = true, want false")
	}
	if got := cr.Details["missing_source_pack"]; got != 1 {
		t.Fatalf("missing_source_pack = %v, want 1", got)
	}

	cr = doctorCheckLedgerHealth(engine.New(nil, nil), dir, singlePack, true)
	if !cr.Fixed {
		t.Fatal("Fixed = false, want true")
	}

	lg2, _, err := eng.LoadLedger(filepath.Join(ledgerDir, "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := lg2.Managed[mcpKey].SourcePack; got != "only-pack" {
		t.Fatalf("SourcePack = %q, want only-pack", got)
	}
}

func TestDoctorCheckLedgerHealth_IgnoresSyntheticMCPKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledgerDir := filepath.Join(dir, "ledger")
	if err := os.MkdirAll(ledgerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)

	realFile := filepath.Join(dir, "rule.md")
	if err := os.WriteFile(realFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	lg := domain.NewLedger()
	lg.Managed[realFile] = domain.Entry{SourcePack: "mypack", Digest: "abc"}
	lg.Managed[realFile+"#mcp:atlassian"] = domain.Entry{SourcePack: "mypack", Digest: "def"}
	if err := eng.SaveLedger(filepath.Join(ledgerDir, "test.json"), lg, false); err != nil {
		t.Fatal(err)
	}

	cr := doctorCheckLedgerHealth(engine.New(nil, nil), dir, nil, false)
	if !cr.OK {
		t.Fatalf("OK = false, want true (synthetic mcp keys should be ignored), message=%q", cr.Message)
	}
}

func TestRunDoctor_UsesStableMCPRefsCheckNameWhenSkipped(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDir, err := config.DefaultConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: missing\n  harnesses: [claudecode]\n  scope: global\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep := RunDoctor(t.Context(), engine.New(nil, nil), DoctorRequest{
		ConfigDir: configDir,
		Home:      home,
	})

	foundRefs := false
	for _, check := range rep.Checks {
		if check.Name == "mcp_refs_present" {
			foundRefs = true
		}
		if check.Name == "mcp_env_vars_present" {
			t.Fatalf("unexpected legacy check name in report: %+v", check)
		}
	}
	if !foundRefs {
		t.Fatal("expected skipped mcp_refs_present check")
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
	cr := doctorCheckManifestDrift(dir, packs, false)
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
	cr := doctorCheckManifestDrift(dir, packs, false)
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
	cr := doctorCheckManifestDrift(dir, packs, false)
	if cr.OK {
		t.Errorf("OK = true, want false (missing content)")
	}
}

func TestDoctorCheckManifestDrift_FixAddsUndeclaredAndRemovesMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	packRoot := filepath.Join(dir, "packs", "mypack")
	os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755)
	os.WriteFile(filepath.Join(packRoot, "rules", "alpha.md"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(packRoot, "rules", "beta.md"), []byte("x"), 0o600)

	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "mypack",
		Root:          ".",
		Rules:         []string{"alpha", "ghost"}, // ghost is missing; beta is undeclared
	}
	// Write the initial manifest so SavePackManifest can overwrite it.
	if err := config.SavePackManifest(filepath.Join(packRoot, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}

	packs := []config.ResolvedPack{{
		Name:     "mypack",
		Root:     packRoot,
		Manifest: manifest,
	}}
	cr := doctorCheckManifestDrift(dir, packs, true)
	if !cr.Fixed {
		t.Fatalf("Fixed = false, want true")
	}
	if cr.Status != "fixed" {
		t.Errorf("Status = %q, want fixed", cr.Status)
	}

	// Re-read manifest and verify.
	updated, err := config.LoadPackManifest(filepath.Join(packRoot, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta"}
	if len(updated.Rules) != len(want) {
		t.Fatalf("Rules = %v, want %v", updated.Rules, want)
	}
	for i, id := range want {
		if updated.Rules[i] != id {
			t.Errorf("Rules[%d] = %q, want %q", i, updated.Rules[i], id)
		}
	}
}

func TestDoctorCheckLedgerHealth_FixesNestedProjectLedgerEntries(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	eng := engine.New(nil, nil)
	configDir, _ := config.DefaultConfigDir(home)
	projectDir := filepath.Join(home, "project")
	trackedFile := filepath.Join(projectDir, ".claude", "settings.local.json")

	if err := os.MkdirAll(filepath.Dir(trackedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackedFile, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	if err := eng.SaveLedger(ledgerPath, domain.Ledger{
		Managed: map[string]domain.Entry{
			trackedFile: {Digest: domain.SingleFileDigest([]byte(`{"ok":true}`))},
		},
	}, false); err != nil {
		t.Fatal(err)
	}

	cr := doctorCheckLedgerHealth(engine.New(nil, nil), configDir, []config.ResolvedPack{{Name: "alpha"}}, true)
	if !cr.Fixed {
		t.Fatalf("Fixed = false, want true")
	}

	lg, _, err := eng.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lg.Managed[trackedFile].SourcePack; got != "alpha" {
		t.Fatalf("SourcePack = %q, want alpha", got)
	}
}

func TestDoctorCheckStaleLedgers_FindsNestedProjectLedgerDirs(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	eng := engine.New(nil, nil)
	configDir, _ := config.DefaultConfigDir(home)
	projectDir := filepath.Join(home, "project")
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)

	if err := eng.SaveLedger(ledgerPath, domain.Ledger{Managed: map[string]domain.Entry{}}, false); err != nil {
		t.Fatal(err)
	}

	var syncCfg config.SyncConfig
	syncCfg.Defaults.Harnesses = []string{"claudecode"}
	cr := doctorCheckStaleLedgers(configDir, syncCfg)
	if cr.OK {
		t.Fatalf("OK = true, want false")
	}
	if cr.Status != "warn" {
		t.Fatalf("Status = %q, want warn", cr.Status)
	}
	stale, ok := cr.Details["stale"].([]string)
	if !ok || len(stale) == 0 {
		t.Fatalf("stale details = %#v, want non-empty []string", cr.Details["stale"])
	}
}

func TestDoctorCheckStaleLedgers_IgnoresPresyncCacheJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	presyncDir := filepath.Join(configDir, "ledger", "claudecode-presync")
	if err := os.MkdirAll(presyncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presyncDir, "index.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presyncDir, "claudecode--.claude--settings.local.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	var syncCfg config.SyncConfig
	syncCfg.Defaults.Scope = string(domain.ScopeGlobal)
	syncCfg.Defaults.Harnesses = []string{"claudecode"}
	cr := doctorCheckStaleLedgers(configDir, syncCfg)
	if !cr.OK {
		t.Fatalf("OK = false, want true when only presync cache exists; details=%#v", cr.Details)
	}
	if cr.Status != "pass" {
		t.Fatalf("Status = %q, want pass", cr.Status)
	}
	if strings.Contains(cr.Message, "not matching current harness config") {
		t.Fatalf("unexpected stale warning message: %q", cr.Message)
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
