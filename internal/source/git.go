package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var checkGitOnce sync.Once
var checkGitErr error

// bareCacheMus serializes UpdateBareCache writes per cache key so two
// concurrent pack updates sharing the same origin URL don't race on the
// stat-then-clone sequence. Keyed by CacheKeyForURL(repoURL).
var bareCacheMus sync.Map // map[string]*sync.Mutex

func bareCacheMutexFor(key string) *sync.Mutex {
	if v, ok := bareCacheMus.Load(key); ok {
		return v.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := bareCacheMus.LoadOrStore(key, m)
	return actual.(*sync.Mutex)
}

// CheckGit verifies that the git binary is available and returns an actionable
// error when it is missing (e.g. Xcode CLT not installed on macOS).
// The result is cached after the first call.
func CheckGit() error {
	checkGitOnce.Do(func() {
		_, err := exec.LookPath("git")
		if err != nil {
			switch runtime.GOOS {
			case "darwin":
				checkGitErr = fmt.Errorf("git not found: install Xcode Command Line Tools with: xcode-select --install")
			case "windows":
				checkGitErr = fmt.Errorf("git not found: install Git for Windows with: winget install Git.Git")
			default:
				checkGitErr = fmt.Errorf("git not found: install git from https://git-scm.com/downloads")
			}
		}
	})
	return checkGitErr
}

// EnsureClone clones a repo into dir (using the real git binary) if .git is not already present.
func EnsureClone(ctx context.Context, repoURL, dir, ref string) error {
	return ensureClone(ctx, repoURL, dir, ref, "", runGit)
}

// EnsureCloneWith is like EnsureClone but accepts a custom git runner for testing.
func EnsureCloneWith(ctx context.Context, repoURL, dir, ref string, runGitFn func(ctx context.Context, args ...string) error) error {
	return ensureClone(ctx, repoURL, dir, ref, "", runGitFn)
}

// EnsureCloneWithRef is like EnsureCloneWith but uses referenceDir as a local
// object cache (git --reference) to speed up the network transfer. If
// referenceDir is empty or doesn't contain a valid bare repo, it is ignored.
func EnsureCloneWithRef(ctx context.Context, repoURL, dir, ref, referenceDir string, runGitFn func(ctx context.Context, args ...string) error) error {
	return ensureClone(ctx, repoURL, dir, ref, referenceDir, runGitFn)
}

func ensureClone(ctx context.Context, repoURL string, dir string, ref string, referenceDir string, runGitFn func(ctx context.Context, args ...string) error) error {
	if err := CheckGit(); err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		if ref != "" {
			return checkoutRef(ctx, dir, ref, runGitFn)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// Check if the reference dir contains a usable bare repo.
	var refArgs []string
	if referenceDir != "" {
		if _, err := os.Stat(filepath.Join(referenceDir, "HEAD")); err == nil {
			refArgs = []string{"--reference", referenceDir}
		}
	}

	// Commit hashes need full history — shallow clone + single-ref fetch
	// doesn't work for arbitrary commits (short hashes aren't ref names, and
	// full-hash fetch requires server-side uploadpack.allowReachableSHA1InWant).
	// Do a full clone and then checkout the commit locally.
	if ref != "" && IsCommitHash(ref) {
		args := append([]string{"clone"}, refArgs...)
		args = append(args, repoURL, dir)
		if err := runGitFn(ctx, args...); err != nil {
			return err
		}
		return runGitFn(ctx, "-C", dir, "checkout", "--force", ref)
	}

	// When a ref is specified, try --branch first (works for branches and tags,
	// single network round-trip). Fall back to clone-then-fetch for arbitrary
	// refs that --branch doesn't support.
	if ref != "" {
		args := append([]string{"clone", "--depth", "1"}, refArgs...)
		args = append(args, "--branch", ref, repoURL, dir)
		err := runGitFn(ctx, args...)
		if err == nil {
			return nil
		}
		// --branch failed; fall back to default branch + fetch.
		_ = os.RemoveAll(dir)
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return mkErr
		}
	}
	args := append([]string{"clone", "--depth", "1"}, refArgs...)
	args = append(args, repoURL, dir)
	if err := runGitFn(ctx, args...); err != nil {
		return err
	}
	if ref != "" {
		return checkoutRef(ctx, dir, ref, runGitFn)
	}
	return nil
}

func checkoutRef(ctx context.Context, dir string, ref string, runGitFn func(ctx context.Context, args ...string) error) error {
	if err := runGitFn(ctx, "-C", dir, "fetch", "--depth", "1", "origin", ref); err != nil {
		return err
	}
	return runGitFn(ctx, "-C", dir, "checkout", "--force", "FETCH_HEAD")
}

// LsRemoteHead returns the commit hash for a ref on the remote without cloning.
// When ref is empty, it returns the hash for HEAD. When ref is set, it queries
// both refs/heads/<ref> and refs/tags/<ref> in a single network call.
// Returns ("", nil) when the ref cannot be resolved.
func LsRemoteHead(ctx context.Context, repoURL, ref string) (string, error) {
	return lsRemoteHead(ctx, repoURL, ref)
}

func lsRemoteHead(ctx context.Context, repoURL, ref string) (string, error) {
	var args []string
	if ref == "" {
		args = []string{"HEAD"}
	} else {
		// Query branch and tag in one round-trip.
		args = []string{"refs/heads/" + ref, "refs/tags/" + ref}
	}
	out, err := gitLsRemoteRaw(ctx, repoURL, args...)
	if err != nil {
		return "", err
	}
	return parseLsRemoteHash(out), nil
}

// gitLsRemoteRaw runs `git ls-remote [flags] <repoURL> [refspecs]` with the
// standard 30s timeout and credential-prompt suppression. Returns raw stdout.
// Used by lsRemoteHead and listRemoteTags to share scaffolding.
//
// Args starting with "-" are treated as flags and placed before the repoURL;
// other args are treated as refspecs and placed after. This matches git's
// expected order — `--tags` and `--heads` are silently mis-parsed as refspec
// patterns when they appear after the URL.
func gitLsRemoteRaw(ctx context.Context, repoURL string, args ...string) ([]byte, error) {
	if err := CheckGit(); err != nil {
		return nil, err
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var flags, refspecs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			refspecs = append(refspecs, a)
		}
	}
	cmdArgs := []string{"ls-remote"}
	cmdArgs = append(cmdArgs, flags...)
	cmdArgs = append(cmdArgs, repoURL)
	cmdArgs = append(cmdArgs, refspecs...)
	cmd := exec.CommandContext(tctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote %s: %w", repoURL, err)
	}
	return out, nil
}

// parseLsRemoteHash extracts the commit hash from ls-remote output.
// For annotated tags, ls-remote returns both the tag object hash and the
// dereferenced commit hash (refs/tags/v1.0^{}). We prefer the ^{} line
// because that matches what `git rev-parse HEAD` returns after checkout.
func parseLsRemoteHash(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	// Prefer the ^{} dereferenced line (commit SHA for annotated tags).
	for line := range strings.SplitSeq(s, "\n") {
		if strings.Contains(line, "^{}") {
			if hash, _, ok := strings.Cut(line, "\t"); ok {
				return hash
			}
		}
	}
	// Fall back to first line (branches, HEAD, lightweight tags).
	first := s
	if f, _, ok := strings.Cut(s, "\n"); ok {
		first = f
	}
	if hash, _, ok := strings.Cut(first, "\t"); ok {
		return hash
	}
	return ""
}

// GitHeadHash returns the HEAD commit SHA for the git repo at dir.
func GitHeadHash(ctx context.Context, dir string) (string, error) {
	return gitHeadHash(ctx, dir)
}

func gitHeadHash(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RunGit runs a git command with the given arguments.
func RunGit(ctx context.Context, args ...string) error {
	return runGit(ctx, args...)
}

func runGit(ctx context.Context, args ...string) error {
	_, err := runGitCore(ctx, args...)
	return err
}

// runGitCore runs a git command with shared setup (timeout, env, error formatting)
// and returns stdout bytes. Both runGit and runGitOutput delegate to this.
func runGitCore(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	// Prevent git from prompting for credentials in non-interactive contexts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("git %s timed out after 2m", strings.Join(args, " "))
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		hint := gitErrorHint(msg, args)
		if hint != "" {
			return nil, fmt.Errorf("git %s failed: %s\n\n%s", strings.Join(args, " "), msg, hint)
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// gitErrorHint returns an actionable hint for common git failures.
func gitErrorHint(output string, args []string) string {
	lower := strings.ToLower(output)

	// Xcode CLT missing (macOS stub git).
	if strings.Contains(lower, "xcrun") || strings.Contains(lower, "xcode-select") ||
		strings.Contains(lower, "command line tools") {
		return "hint: install Xcode Command Line Tools: xcode-select --install"
	}

	// SSH connection timeout — common with Bitbucket Server on non-standard ports.
	if strings.Contains(lower, "operation timed out") ||
		strings.Contains(lower, "connection timed out") {
		for _, arg := range args {
			if host, ok := strings.CutPrefix(arg, "git@"); ok && strings.Contains(arg, ":") && !strings.Contains(arg, "://") {
				// SCP-style URL defaults to port 22; Bitbucket Server typically uses 7999.
				if idx := strings.Index(host, ":"); idx >= 0 {
					host = host[:idx]
				}
				return "hint: SSH connection timed out on port 22. Bitbucket Server often uses port 7999.\n" +
					"  Try: ssh://git@" + host + ":7999/<project>/<repo>.git\n" +
					"  Or add to ~/.ssh/config:\n" +
					"    Host " + host + "\n" +
					"      Port 7999"
			}
		}
	}

	// Authentication / credential failures.
	if strings.Contains(lower, "could not read username") ||
		strings.Contains(lower, "terminal prompts disabled") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "invalid credentials") ||
		strings.Contains(lower, "authorization failed") ||
		strings.Contains(lower, "http basic:") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "401") {
		// Check if an HTTPS URL is in the args.
		for _, arg := range args {
			if strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://") {
				helper := "osxkeychain"
				switch runtime.GOOS {
				case "windows":
					helper = "manager"
				case "linux":
					helper = "store"
				}
				return "hint: HTTPS git requires credentials. Try one of:\n" +
					"  - Use SSH URL instead: git@<host>:<project>/<repo>.git\n" +
					"  - Configure a credential helper: git config --global credential.helper " + helper + "\n" +
					"  - Set up a personal access token"
			}
		}
		return "hint: git authentication failed. Check your SSH key or credential configuration."
	}

	return ""
}

// --- Git clone cache ---

// GitCacheDir returns the directory used for bare-repo clone caches.
func GitCacheDir(configDir string) string {
	return filepath.Join(configDir, ".cache", "git")
}

// CacheKeyForURL returns a stable directory name for caching a repo URL.
func CacheKeyForURL(repoURL string) string {
	h := sha256.Sum256([]byte(normalizeRepoURL(repoURL)))
	return hex.EncodeToString(h[:12]) // 24 hex chars — unique enough, short names
}

// CacheRefDir returns the bare-repo cache path for a given repo URL.
func CacheRefDir(configDir, repoURL string) string {
	return filepath.Join(GitCacheDir(configDir), CacheKeyForURL(repoURL))
}

// UpdateBareCache creates a bare-repo cache for repoURL if one doesn't
// already exist. When localSource is non-empty and contains a .git directory,
// it is used as the clone source instead of hitting the remote — avoiding a
// redundant network round-trip after the caller has already cloned. If the
// cache already exists, this is a no-op (the cache is a local object store
// for --reference, not a tracking clone — staleness only means slightly more
// network transfer on the next clone, not failure).
// The cache is NOT shallow: git --reference requires a non-shallow repo.
// Best-effort: errors are returned but callers may choose to ignore them.
func UpdateBareCache(ctx context.Context, repoURL, localSource, cacheDir string, runGitFn func(ctx context.Context, args ...string) error) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	key := CacheKeyForURL(repoURL)
	bareDir := filepath.Join(cacheDir, key)

	// Serialize writes for this cache key. Concurrent callers for the same
	// origin block here, then the second caller observes the populated cache
	// and returns nil on its own existence check. Independent origins don't
	// contend. Read paths (EnsureCloneWithRef --reference) are unaffected —
	// git handles concurrent readers of a bare repo safely.
	mu := bareCacheMutexFor(key)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(filepath.Join(bareDir, "HEAD")); err == nil {
		return nil // Cache exists — already usable as --reference.
	}
	// Seed from local clone when available (avoids redundant network call).
	src := repoURL
	if localSource != "" {
		if _, err := os.Stat(filepath.Join(localSource, ".git")); err == nil {
			src = localSource
		}
	}
	if err := runGitFn(ctx, "clone", "--bare", src, bareDir); err != nil {
		// Clean up partial state so future attempts can retry.
		_ = os.RemoveAll(bareDir)
		return err
	}
	// Unshallow: the source may be a --depth 1 clone, producing a shallow
	// bare repo. git --reference requires non-shallow repos. Remove the
	// shallow marker so subsequent clones can borrow objects.
	_ = os.Remove(filepath.Join(bareDir, "shallow"))
	return nil
}

// normalizeRepoURL produces a canonical form for cache key hashing.
// Lowercases the entire URL (scheme, host, and path), strips trailing .git,
// and normalizes SCP-style URLs (git@host:path) to ssh://git@host/path.
// Full lowercasing is intentional: major git hosting platforms (GitHub, GitLab,
// Bitbucket) treat repository paths case-insensitively, so Org/Repo and
// org/repo should share the same cache entry.
func normalizeRepoURL(raw string) string {
	s := strings.TrimSpace(raw)

	// SCP-style: git@host:path → ssh://git@host/path
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			if colon := strings.Index(s[at:], ":"); colon >= 0 {
				host := s[:at+colon]
				path := s[at+colon+1:]
				s = "ssh://" + host + "/" + path
			}
		}
	}

	s = strings.TrimSuffix(s, ".git")
	s = strings.ToLower(s)
	return s
}
