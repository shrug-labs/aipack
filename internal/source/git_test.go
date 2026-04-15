package source

import (
	"context"
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
// downstream (e.g. the packs-tab recovery hint) would never see the ssh/git
// message text they need to match on.
//
// Uses ssh://127.0.0.1:1/ — port 1 is IANA-assigned tcpmux but in practice
// never listens, so the TCP connect attempt fails in ~60 ms via loopback
// RST with no DNS dependency, no external network, and no flake risk on
// machines with misconfigured resolvers.
func TestListRemoteTags_ErrorSurfacesStderr(t *testing.T) {
	t.Parallel()
	_, err := ListRemoteTags(context.Background(), "ssh://127.0.0.1:1/foo.git")
	if err == nil {
		t.Fatal("expected error connecting to a port with no listener")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Connection refused") {
		t.Errorf("error message should include ssh stderr (looking for \"Connection refused\"), got %q", msg)
	}
	if !strings.Contains(msg, "port 1") {
		t.Errorf("error message should include the target port, got %q", msg)
	}
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
