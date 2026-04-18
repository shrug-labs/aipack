package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEnsureCloneWith_FreshClone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	var calls []string
	mock := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		// Simulate git clone creating .git dir.
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 git call, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], "clone") {
		t.Fatalf("expected clone call, got: %s", calls[0])
	}
}

func TestEnsureCloneWith_FreshCloneWithRef(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	var calls []string
	mock := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "v1.0", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	// --branch succeeds: single clone call.
	if len(calls) != 1 {
		t.Fatalf("expected 1 git call, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], "--branch v1.0") {
		t.Fatalf("expected --branch clone, got: %s", calls[0])
	}
}

func TestEnsureCloneWith_FreshCloneWithRef_Fallback(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	var calls []string
	mock := func(_ context.Context, args ...string) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		// --branch fails (e.g. deleted ref, unusual refname), plain clone succeeds.
		if strings.Contains(call, "--branch") {
			return fmt.Errorf("remote branch not found")
		}
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	// Non-hash ref: exercises the --branch-then-fetch fallback path. (Commit
	// hashes take a different path — see TestEnsureCloneWith_CommitHashFullClone.)
	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "some-branch", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	// --branch fails + clone + fetch + checkout = 4 calls.
	if len(calls) != 4 {
		t.Fatalf("expected 4 git calls, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], "--branch") {
		t.Fatalf("expected --branch attempt first, got: %s", calls[0])
	}
	if !strings.Contains(calls[2], "fetch") {
		t.Fatalf("expected fetch call, got: %s", calls[2])
	}
}

func TestEnsureCloneWith_CommitHashFullClone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	var calls []string
	mock := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	// Commit hash refs skip the --branch/--depth path entirely and use a full
	// clone + checkout. Shallow single-ref fetch doesn't work for arbitrary
	// commits — short hashes aren't ref names, and full-hash fetch requires
	// server-side uploadpack.allowReachableSHA1InWant.
	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "abc1234", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	// Exactly 2 calls: clone (full, no --depth, no --branch) + checkout.
	if len(calls) != 2 {
		t.Fatalf("expected 2 git calls, got %d: %v", len(calls), calls)
	}
	if strings.Contains(calls[0], "--branch") {
		t.Fatalf("should not use --branch for commit hash, got: %s", calls[0])
	}
	if strings.Contains(calls[0], "--depth") {
		t.Fatalf("should not use --depth for commit hash (full history needed), got: %s", calls[0])
	}
	if !strings.Contains(calls[1], "checkout") {
		t.Fatalf("expected checkout call, got: %s", calls[1])
	}
	if !strings.Contains(calls[1], "abc1234") {
		t.Fatalf("expected checkout with hash, got: %s", calls[1])
	}
}

// TestEnsureCloneWith_BrokenReferenceCacheRetries asserts that when a
// commit-hash clone that uses a --reference cache fails with the
// "unresolved deltas" pattern (indicating the bare cache was seeded from a
// shallow local before the preventive fix shipped), ensureClone invalidates
// the cache, clears the destination, and retries the clone without
// --reference. Belt-and-suspenders for users with poisoned on-disk caches.
// Regression guard for finding #1 in
// projects/aipack-pack-update-bugs-2026-04-14.md.
func TestEnsureCloneWith_BrokenReferenceCacheRetries(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")

	// Create a bare-repo-shaped reference cache so ensureClone adds the
	// --reference flag. Contents are irrelevant — the mock fails the
	// first clone and we verify the retry.
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []string
	mock := func(_ context.Context, args ...string) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if strings.HasPrefix(call, "clone") {
			if strings.Contains(call, "--reference") {
				// First attempt: broken cache surfaces as this exact git
				// stderr. Keep the substring realistic so classifyCloneError
				// sees what it sees in prod.
				return fmt.Errorf("fatal: pack has 22 unresolved deltas")
			}
			// Retry without --reference: create .git so the directory
			// existence check below passes.
			return os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		}
		return nil
	}

	// Commit hash exercises the full-clone (not --depth 1) code path where
	// the original rollback bug surfaced.
	if err := EnsureCloneWithRef(context.Background(), "https://example.com/repo.git", dir, "abc1234", cacheDir, mock); err != nil {
		t.Fatalf("EnsureCloneWithRef: %v", err)
	}

	var cloneCalls []string
	for _, c := range calls {
		if strings.HasPrefix(c, "clone") {
			cloneCalls = append(cloneCalls, c)
		}
	}
	if len(cloneCalls) != 2 {
		t.Fatalf("expected 2 clone calls (fail-with-reference + retry-without), got %d: %v", len(cloneCalls), calls)
	}
	if !strings.Contains(cloneCalls[0], "--reference") {
		t.Fatalf("first clone should have --reference, got: %s", cloneCalls[0])
	}
	if strings.Contains(cloneCalls[1], "--reference") {
		t.Fatalf("retry clone should NOT have --reference, got: %s", cloneCalls[1])
	}
	// Checkout must still run after the retry succeeds.
	sawCheckout := false
	for _, c := range calls {
		if strings.Contains(c, "checkout") {
			sawCheckout = true
			break
		}
	}
	if !sawCheckout {
		t.Fatalf("expected checkout after successful retry clone, got: %v", calls)
	}
}

// TestEnsureCloneWith_BrokenCacheErrorPropagatesWithoutReference asserts
// that a clone failure unrelated to cache poisoning does NOT swallow the
// error under the retry path — if there's no reference cache in play, the
// original error must propagate as-is (wrapped as ErrBrokenReferenceCache
// for typed matching downstream). Locks in the narrow scope of the
// broken-cache fallback.
func TestEnsureCloneWith_BrokenCacheErrorPropagatesWithoutReference(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")

	mock := func(_ context.Context, args ...string) error {
		if len(args) > 0 && args[0] == "clone" {
			return fmt.Errorf("fatal: pack has 22 unresolved deltas")
		}
		return nil
	}

	err := EnsureCloneWithRef(context.Background(), "https://example.com/repo.git", dir, "abc1234", "", mock)
	if err == nil {
		t.Fatal("expected error when clone fails without a reference cache")
	}
	if !errors.Is(err, ErrBrokenReferenceCache) {
		t.Fatalf("error should wrap ErrBrokenReferenceCache, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unresolved deltas") {
		t.Fatalf("expected original stderr to be preserved in error, got: %v", err)
	}
}

// TestClassifyCloneError_NonMatchingErrorsPassThrough asserts that errors
// with unrelated patterns — auth failures, network timeouts, missing
// refs — are NOT wrapped as ErrBrokenReferenceCache. Prevents collateral
// cache nuking on unrelated git clone failures.
func TestClassifyCloneError_NonMatchingErrorsPassThrough(t *testing.T) {
	t.Parallel()
	nonMatching := []string{
		"fatal: could not read Username for 'https://example.com': terminal prompts disabled",
		"fatal: Could not read from remote repository.",
		"fatal: Authentication failed for 'https://example.com/repo.git'",
		"fatal: Remote branch not-a-real-ref not found in upstream origin",
		"ssh: connect to host bitbucket.example.com port 22: Operation timed out",
	}
	for _, msg := range nonMatching {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			in := fmt.Errorf("%s", msg)
			out := classifyCloneError(in)
			if errors.Is(out, ErrBrokenReferenceCache) {
				t.Errorf("classifyCloneError(%q) wrapped as broken cache; should pass through", msg)
			}
		})
	}
}

// TestClassifyCloneError_MatchingErrorsWrap asserts that the two exact
// patterns we do match wrap as ErrBrokenReferenceCache while preserving
// the original error chain via %w.
func TestClassifyCloneError_MatchingErrorsWrap(t *testing.T) {
	t.Parallel()
	matching := []string{
		"fatal: pack has 22 unresolved deltas",
		"error: invalid index-pack output",
	}
	for _, msg := range matching {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			in := fmt.Errorf("%s", msg)
			out := classifyCloneError(in)
			if !errors.Is(out, ErrBrokenReferenceCache) {
				t.Errorf("classifyCloneError(%q) should wrap as broken cache, got: %v", msg, out)
			}
			if !strings.Contains(out.Error(), msg) {
				t.Errorf("wrapped error should preserve original stderr, got: %v", out)
			}
		})
	}
}

func TestEnsureCloneWith_AlreadyCloned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Pre-create .git to simulate existing clone.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls []string
	mock := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}

	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	// No git calls — already cloned, no ref.
	if len(calls) != 0 {
		t.Fatalf("expected 0 git calls, got %d: %v", len(calls), calls)
	}
}

func TestEnsureCloneWith_AlreadyClonedWithRef(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls []string
	mock := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}

	if err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "main", mock); err != nil {
		t.Fatalf("EnsureCloneWith: %v", err)
	}
	// fetch + checkout = 2 calls (skip clone).
	if len(calls) != 2 {
		t.Fatalf("expected 2 git calls, got %d: %v", len(calls), calls)
	}
}

func TestEnsureCloneWith_CloneFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	mock := func(_ context.Context, args ...string) error {
		return fmt.Errorf("git failed")
	}

	err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "", mock)
	if err == nil {
		t.Fatal("expected error when clone fails")
	}
	if !strings.Contains(err.Error(), "git failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureCloneWith_FetchFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	mock := func(_ context.Context, args ...string) error {
		call := strings.Join(args, " ")
		// --branch fails, plain clone succeeds, fetch fails.
		if strings.Contains(call, "--branch") {
			return fmt.Errorf("remote branch not found")
		}
		if len(args) >= 1 && args[0] == "clone" {
			return os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		}
		if strings.Contains(call, "fetch") {
			return fmt.Errorf("fetch failed")
		}
		return nil
	}

	err := EnsureCloneWith(context.Background(), "https://example.com/repo.git", dir, "v1.0", mock)
	if err == nil {
		t.Fatal("expected error when fetch fails")
	}
}

func TestCheckGit_Available(t *testing.T) {
	t.Parallel()
	// git should be available in the test environment.
	if err := CheckGit(); err != nil {
		t.Fatalf("CheckGit: %v", err)
	}
}

// TestListRemoteTags_ErrorSurfacesStderr locks in the end-to-end contract
// that TUI/app callers depend on: when gitLsRemoteRaw's subprocess fails,
// the returned error's Error() string must contain the actual stderr text,
// not just "exit status 128". Without this, pattern-based error classifiers
// downstream (e.g. the packs-tab recovery hint) would never see the git
// message text they need to match on.
//
// Triggers the failure via a nonexistent filesystem path. git treats a
// non-URL string as a local transport and fails with a stable error
// message ("does not appear to be a git repository" / "Could not read
// from remote repository") across every git version since ~2010. This
// avoids any dependency on the SSH client — earlier revisions of this
// test used ssh://127.0.0.1:1/ and asserted on "Connection refused",
// which broke on build environments with OpenSSH older than 7.6 that
// reject the StrictHostKeyChecking=accept-new option aipack sets in
// nonInteractiveGitEnv(). The filesystem trigger is faster, hermetic,
// and exercises the same error-surfacing code path in gitLsRemoteRaw.
func TestListRemoteTags_ErrorSurfacesStderr(t *testing.T) {
	t.Parallel()
	bogusPath := filepath.Join(t.TempDir(), "nonexistent-probe.git")
	_, err := ListRemoteTags(context.Background(), bogusPath)
	if err == nil {
		t.Fatal("expected error listing tags from a nonexistent path")
	}
	msg := err.Error()
	// Our error wrapper prefix must be present — proves the error
	// went through the formatting path, not just the raw *exec.ExitError.
	if !strings.Contains(msg, "git ls-remote") {
		t.Errorf("error message should include command context, got %q", msg)
	}
	// Real git stderr must be surfaced. Either phrase proves the subprocess
	// output was captured; git typically emits both.
	if !strings.Contains(msg, "does not appear to be a git repository") &&
		!strings.Contains(msg, "Could not read from remote repository") {
		t.Errorf("error message should include git stderr, got %q", msg)
	}
	// Must be more than the bare "exit status N" wrapper.
	if strings.HasSuffix(msg, "exit status 128") {
		t.Errorf("error message should contain stderr, not just the exit status suffix, got %q", msg)
	}
}

func TestGitErrorHint_AuthFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		output string
		args   []string
		want   string
	}{
		{
			output: "fatal: could not read Username for 'https://example.com': terminal prompts disabled",
			args:   []string{"clone", "--depth", "1", "https://example.com/repo.git", "/tmp/x"},
			want:   "HTTPS git requires credentials",
		},
		{
			output: "fatal: Authentication failed for 'https://example.com/repo.git'",
			args:   []string{"clone", "https://example.com/repo.git", "/tmp/x"},
			want:   "HTTPS git requires credentials",
		},
		{
			output: "fatal: Authentication failed",
			args:   []string{"clone", "git@example.com:org/repo.git", "/tmp/x"},
			want:   "SSH key or credential",
		},
		{
			output: "xcrun: error: invalid active developer path",
			args:   []string{"clone", "https://example.com/repo.git"},
			want:   "Xcode Command Line Tools",
		},
		{
			output: "ssh: connect to host bitbucket.example.com port 22: Operation timed out\nfatal: Could not read from remote repository.",
			args:   []string{"clone", "--depth", "1", "git@bitbucket.example.com:proj/repo.git", "/tmp/x"},
			want:   "port 7999",
		},
		{
			output: "ssh: connect to host bitbucket.example.com port 22: Connection timed out",
			args:   []string{"clone", "git@bitbucket.example.com:proj/repo.git", "/tmp/x"},
			want:   "ssh://git@bitbucket.example.com:7999",
		},
		{
			output: "normal git error: repository not found",
			args:   []string{"clone", "https://example.com/repo.git"},
			want:   "",
		},
	}
	for _, tt := range tests {
		got := gitErrorHint(tt.output, tt.args)
		if tt.want == "" && got != "" {
			t.Errorf("gitErrorHint(%q, ...) = %q, want empty", tt.output, got)
		}
		if tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("gitErrorHint(%q, ...) = %q, want substring %q", tt.output, got, tt.want)
		}
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/Org/Repo.git", "https://github.com/org/repo"},
		{"git@bitbucket.example.com:PROJ/repo.git", "ssh://git@bitbucket.example.com/proj/repo"},
		{"ssh://git@Host.COM:7999/team/tools.git", "ssh://git@host.com:7999/team/tools"},
		{"https://GitHub.COM/owner/REPO", "https://github.com/owner/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := normalizeRepoURL(tt.input)
			if got != tt.want {
				t.Errorf("normalizeRepoURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCacheKeyForURL_Stable(t *testing.T) {
	t.Parallel()
	// Same URL produces same key.
	a := CacheKeyForURL("https://github.com/org/repo.git")
	b := CacheKeyForURL("https://github.com/org/repo.git")
	if a != b {
		t.Fatalf("unstable: %q != %q", a, b)
	}
	// Normalization: trailing .git stripped, host lowercased.
	c := CacheKeyForURL("https://GitHub.com/org/repo")
	if a != c {
		t.Fatalf("normalization failed: %q != %q", a, c)
	}
	// Different repo → different key.
	d := CacheKeyForURL("https://github.com/org/other-repo")
	if a == d {
		t.Fatal("different repos should have different keys")
	}
}

func TestEnsureCloneWithRef_UsesReference(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")

	// Create a fake bare cache with a HEAD file.
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("writing HEAD: %v", err)
	}

	var cloneArgs string
	mock := func(_ context.Context, args ...string) error {
		call := strings.Join(args, " ")
		if strings.HasPrefix(call, "clone") {
			cloneArgs = call
			os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		}
		return nil
	}

	if err := EnsureCloneWithRef(context.Background(), "https://example.com/repo.git", dir, "", cacheDir, mock); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cloneArgs, "--reference") {
		t.Fatalf("expected --reference in clone args, got: %s", cloneArgs)
	}
	if !strings.Contains(cloneArgs, cacheDir) {
		t.Fatalf("expected cache dir in clone args, got: %s", cloneArgs)
	}
}

func TestEnsureCloneWithRef_NoCacheDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")

	var cloneArgs string
	mock := func(_ context.Context, args ...string) error {
		call := strings.Join(args, " ")
		if strings.HasPrefix(call, "clone") {
			cloneArgs = call
			os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		}
		return nil
	}

	// Non-existent cache — should clone normally without --reference.
	if err := EnsureCloneWithRef(context.Background(), "https://example.com/repo.git", dir, "", "/nonexistent", mock); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cloneArgs, "--reference") {
		t.Fatalf("should not use --reference for missing cache, got: %s", cloneArgs)
	}
}

// TestUpdateBareCache_ConcurrentSameKey verifies that concurrent callers for
// the same origin URL serialize on the per-key mutex, so only one bare clone
// actually runs. Without the mutex, the TOCTOU window between the HEAD stat
// and the clone --bare invocation lets every goroutine fall through to
// clone, which both wastes work and can corrupt partial state.
func TestUpdateBareCache_ConcurrentSameKey(t *testing.T) {
	// Not t.Parallel: manipulates the process-global bareCacheMus via a
	// unique URL, and we want deterministic counts.
	cacheDir := filepath.Join(t.TempDir(), "cache")
	// Use a unique URL so this test doesn't share the key mutex with
	// anything else in the process (the map is process-wide).
	url := "https://example.com/concurrent-same-key-test.git"

	var bareClones int64
	var creating sync.Mutex // guards the in-progress HEAD write
	mock := func(_ context.Context, args ...string) error {
		if len(args) >= 2 && args[0] == "clone" && args[1] == "--bare" {
			atomic.AddInt64(&bareClones, 1)
			// Materialize the bare repo layout so the next caller's
			// stat-of-HEAD hits an existing cache and returns early.
			dest := args[len(args)-1]
			creating.Lock()
			defer creating.Unlock()
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
		return nil
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			// localSource is empty → mock will actually be invoked with
			// the URL as the source (we don't care, we only inspect args).
			errs <- UpdateBareCache(context.Background(), url, "", cacheDir, mock)
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("UpdateBareCache returned error under concurrency: %v", err)
		}
	}
	if got := atomic.LoadInt64(&bareClones); got != 1 {
		t.Fatalf("expected exactly 1 bare clone under concurrent same-key access, got %d", got)
	}

	// Cache should be populated and reusable by a subsequent caller.
	if _, err := os.Stat(filepath.Join(cacheDir, CacheKeyForURL(url), "HEAD")); err != nil {
		t.Fatalf("expected cache HEAD to exist after concurrent init: %v", err)
	}
}

// TestUpdateBareCache_ShallowLocalSeedSkipped asserts that UpdateBareCache
// clones from the remote URL (not the local source) when the local source
// has a .git/shallow file. Seeding from a shallow local produces a bare
// cache with an incomplete object database that later `clone --reference`
// calls can't resolve, surfacing as "pack has N unresolved deltas" during
// a `pack update --version <older-commit>` rollback. Regression guard for
// finding #1 in projects/aipack-pack-update-bugs-2026-04-14.md.
func TestUpdateBareCache_ShallowLocalSeedSkipped(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	localSource := filepath.Join(tmp, "shallow")
	if err := os.MkdirAll(filepath.Join(localSource, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Presence of .git/shallow is what marks a clone as shallow. Git
	// writes it during `clone --depth <n>` and reads it to short-circuit
	// operations that need the full object graph.
	if err := os.WriteFile(filepath.Join(localSource, ".git", "shallow"),
		[]byte("0000000000000000000000000000000000000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(tmp, "cache")
	// Unique URL per test so the process-global bareCacheMus doesn't
	// cross-contaminate with parallel tests.
	url := "https://example.com/shallow-skip-test.git"

	var cloneSrc string
	mock := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" && args[1] == "--bare" {
			cloneSrc = args[2]
			dest := args[3]
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
		return nil
	}

	if err := UpdateBareCache(context.Background(), url, localSource, cacheDir, mock); err != nil {
		t.Fatalf("UpdateBareCache: %v", err)
	}
	if cloneSrc != url {
		t.Errorf("clone source = %q, want %q (remote URL, not shallow local)", cloneSrc, url)
	}
}

// TestUpdateBareCache_NonShallowLocalSeedUsed asserts the inverse: when the
// local source is a full (non-shallow) clone, it IS used as the seed to
// avoid the redundant network round-trip. Pairs with the shallow-skip test
// to lock in the exact branch condition.
func TestUpdateBareCache_NonShallowLocalSeedUsed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	localSource := filepath.Join(tmp, "full")
	if err := os.MkdirAll(filepath.Join(localSource, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No .git/shallow file — a non-shallow clone.

	cacheDir := filepath.Join(tmp, "cache")
	url := "https://example.com/full-seed-test.git"

	var cloneSrc string
	mock := func(_ context.Context, args ...string) error {
		if len(args) >= 4 && args[0] == "clone" && args[1] == "--bare" {
			cloneSrc = args[2]
			dest := args[3]
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
		return nil
	}

	if err := UpdateBareCache(context.Background(), url, localSource, cacheDir, mock); err != nil {
		t.Fatalf("UpdateBareCache: %v", err)
	}
	if cloneSrc != localSource {
		t.Errorf("clone source = %q, want %q (non-shallow local)", cloneSrc, localSource)
	}
}

// TestUpdateBareCache_ConcurrentDistinctKeys verifies that concurrent callers
// for different origins do NOT serialize on each other — each origin gets its
// own mutex and can clone independently.
func TestUpdateBareCache_ConcurrentDistinctKeys(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	var bareClones int64
	mock := func(_ context.Context, args ...string) error {
		if len(args) >= 2 && args[0] == "clone" && args[1] == "--bare" {
			atomic.AddInt64(&bareClones, 1)
			dest := args[len(args)-1]
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
		return nil
	}

	const n = 5
	var wg sync.WaitGroup
	for i := range n {
		url := fmt.Sprintf("https://example.com/distinct-%d.git", i)
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			_ = UpdateBareCache(context.Background(), u, "", cacheDir, mock)
		}(url)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&bareClones); got != n {
		t.Fatalf("expected %d bare clones for %d distinct origins, got %d", n, n, got)
	}
}

func TestParseLsRemoteHash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"normal", "abc123def456\trefs/heads/main\n", "abc123def456"},
		{"multi-line", "abc123\trefs/heads/main\ndef456\trefs/tags/v1\n", "abc123"},
		{"empty", "", ""},
		{"no-tab", "abc123", ""},
		{"trailing-whitespace", "  abc123\trefs/heads/main  \n", "abc123"},
		// Annotated tag: two lines, the ^{} line has the commit SHA.
		{"annotated-tag", "aaa111\trefs/tags/v1.0\nbbb222\trefs/tags/v1.0^{}\n", "bbb222"},
		// Annotated tag only (no branch match): still prefer ^{}.
		{"annotated-tag-only-deref", "ccc333\trefs/tags/v2.0\nddd444\trefs/tags/v2.0^{}\n", "ddd444"},
		// Branch + annotated tag mixed output: ^{} wins.
		{"branch-and-annotated-tag", "eee555\trefs/heads/main\nfff666\trefs/tags/v1.0\nggg777\trefs/tags/v1.0^{}\n", "ggg777"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseLsRemoteHash([]byte(tt.out))
			if got != tt.want {
				t.Errorf("parseLsRemoteHash(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}
