package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrBrokenReferenceCache indicates that `git clone --reference <cache>`
// failed because the bare reference cache is missing objects — the symptom
// of a cache seeded from a shallow local clone before the UpdateBareCache
// preventive fix shipped. Callers that observe this error can invalidate
// the cache and retry the clone without `--reference`. Matches the
// sentinel-error pattern already used by ErrHTTPTarballFailed.
var ErrBrokenReferenceCache = errors.New("broken reference cache: missing objects")

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
		cloneArgs := append([]string{"clone"}, refArgs...)
		cloneArgs = append(cloneArgs, repoURL, dir)
		if err := classifyCloneError(runGitFn(ctx, cloneArgs...)); err != nil {
			if len(refArgs) == 0 || !errors.Is(err, ErrBrokenReferenceCache) {
				return err
			}
			// Poisoned --reference cache (e.g. seeded from a shallow
			// local before the UpdateBareCache fix shipped). Invalidate
			// the cache, clear the destination, and retry without
			// --reference. One-shot fallback; if the retry also fails,
			// we return that error.
			_ = os.RemoveAll(referenceDir)
			_ = os.RemoveAll(dir)
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				return mkErr
			}
			if retryErr := runGitFn(ctx, "clone", repoURL, dir); retryErr != nil {
				return retryErr
			}
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

// gitLsRemoteRaw runs `git ls-remote [flags] <repoURL> [refspecs]` with a
// non-interactive environment. Returns raw stdout. Used by lsRemoteHead and
// listRemoteTags to share scaffolding.
//
// Args starting with "-" are treated as flags and placed before the repoURL;
// other args are treated as refspecs and placed after. This matches git's
// expected order — `--tags` and `--heads` are silently mis-parsed as refspec
// patterns when they appear after the URL.
//
// Timeout policy: if ctx already has a deadline (e.g. a TUI path with a
// short budget), it is honored as-is; otherwise a 30s ceiling is applied so
// CLI callers don't hang indefinitely on an unreachable remote.
func gitLsRemoteRaw(ctx context.Context, repoURL string, args ...string) ([]byte, error) {
	if err := CheckGit(); err != nil {
		return nil, err
	}
	tctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
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
	cmd.Env = nonInteractiveGitEnv()
	stdout, stderr, err := runAndCaptureStderr(cmd)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git ls-remote %s: %s", repoURL, msg)
	}
	return stdout, nil
}

// runAndCaptureStderr runs cmd with both stdout and stderr buffered and
// returns the captured bytes alongside any process error. Used instead of
// cmd.Output() at every site that cares about the content of stderr — the
// *exec.ExitError default Error() string is just "exit status N", so the
// stderr bytes are invisible to downstream error classifiers and log
// consumers unless explicitly pulled out here.
func runAndCaptureStderr(cmd *exec.Cmd) (stdout, stderr []byte, err error) {
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.Bytes(), se.Bytes(), err
}

// nonInteractiveGitEnv returns the process environment with every interactive
// credential channel disabled. Non-interactive credential sources — credential
// helpers returning cached creds, ssh-agent (including PIV/smartcard keys),
// user-defined silent askpass scripts — are deliberately left intact so a
// pack install or update that WOULD succeed in a normal shell still succeeds
// here.
//
//   - GIT_TERMINAL_PROMPT=0 — blocks git's own "Username:"/"Password:" prompt
//     on a TTY, without touching credential helpers.
//   - GIT_SSH_COMMAND — forces ssh BatchMode (refuses interactive auth) and a
//     short ConnectTimeout so an unreachable host fails quickly. ssh-agent
//     auth still works; only passphrase prompts for locked on-disk keys are
//     refused. Git for Windows bundles OpenSSH which honors these options.
//     StrictHostKeyChecking=accept-new is load-bearing: under BatchMode the
//     default `ask` becomes a hard refusal on any host not already in
//     known_hosts, which breaks first-run clones against github/bitbucket on
//     a fresh machine. `accept-new` lets first-connect succeed without
//     prompting, persists the key to known_hosts (so later ops in any tool
//     verify against the cached entry), and still refuses if a cached key
//     changes. Do not tighten to `yes` (breaks first-run) or loosen to `no`
//     (silent TOFU every time, loses key-rotation detection).
//   - SSH_ASKPASS_REQUIRE=never — OpenSSH 8.4+ hard-disables the fallback
//     askpass GUI even when DISPLAY is set. No-op on older ssh.
//   - GCM_INTERACTIVE=Never — blocks Git Credential Manager's GUI popup on
//     Windows while still allowing GCM to return already-cached creds.
//
// Do NOT add `-c credential.helper=` or `-c core.askPass=` here: at runtime
// those reset the helper chain / override a legitimately-configured askpass
// script, breaking users who rely on osxkeychain (macOS default via Xcode
// CLT gitconfig), GCM's cached credentials, libsecret, or a silent askpass
// wrapper around a password manager.
func nonInteractiveGitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new",
		"SSH_ASKPASS_REQUIRE=never",
		"GCM_INTERACTIVE=Never",
	)
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
	cmd.Env = nonInteractiveGitEnv()
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
//
// The environment blocks every interactive credential channel (ssh passphrase
// prompts, GCM GUI popups, askpass dialogs, terminal prompts) so clone/fetch
// can't hang on a pinentry for a locked SSH key. Non-interactive credential
// paths — credential helpers returning cached creds, ssh-agent, user-defined
// silent askpass scripts — are deliberately left intact.
func runGitCore(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = nonInteractiveGitEnv()
	stdout, stderr, err := runAndCaptureStderr(cmd)
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("git %s timed out after 2m", strings.Join(args, " "))
	}
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(stdout))
		}
		hint := gitErrorHint(msg, args)
		if hint != "" {
			return nil, fmt.Errorf("git %s failed: %s\n\n%s", strings.Join(args, " "), msg, hint)
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return stdout, nil
}

// classifyCloneError inspects a git clone failure and, when its stderr
// matches a broken-reference-cache pattern, wraps the error with
// ErrBrokenReferenceCache via %w so callers can match it via errors.Is.
// Non-matching errors are returned unchanged. Matched patterns:
// "unresolved deltas" (the index-pack phase surfacing missing base
// objects) and "invalid index-pack output" (a corrupt partial pack).
// The match must be tight — auth failures ("could not read Username"),
// network errors ("could not read from remote repository"), and
// ref-not-found must NOT wrap as this sentinel, because the retry path
// wipes the reference cache on a hit.
func classifyCloneError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unresolved deltas") ||
		strings.Contains(msg, "invalid index-pack output") {
		return fmt.Errorf("%w: %s", ErrBrokenReferenceCache, err)
	}
	return err
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
	// Seed from local clone when available (avoids redundant network call)
	// — but only when the local source is non-shallow. A shallow local
	// produces a bare cache with an incomplete object database; later
	// `clone --reference` calls fail with "pack has N unresolved deltas"
	// on any commit that isn't in the shallow pack files. Prefer one
	// extra network round-trip over poisoning a long-lived cache.
	src := repoURL
	if localSource != "" {
		if _, err := os.Stat(filepath.Join(localSource, ".git")); err == nil {
			if _, shallowErr := os.Stat(filepath.Join(localSource, ".git", "shallow")); shallowErr != nil {
				src = localSource
			}
		}
	}
	if err := runGitFn(ctx, "clone", "--bare", src, bareDir); err != nil {
		// Clean up partial state so future attempts can retry.
		_ = os.RemoveAll(bareDir)
		return err
	}
	// Defense-in-depth: if a shallow source slipped past the check above
	// (e.g. a caller passed a path the stat couldn't reach), strip the
	// shallow marker so the bare repo at least reports non-shallow to
	// downstream --reference consumers. Missing objects still fail, but
	// the check-then-clone guard is the load-bearing fix.
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
