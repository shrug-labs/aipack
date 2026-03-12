package config

import (
	"bytes"
	"context"
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

var checkGitOnce sync.Once
var checkGitErr error

// CheckGit verifies that the git binary is available and returns an actionable
// error when it is missing (e.g. Xcode CLT not installed on macOS).
// The result is cached after the first call.
func CheckGit() error {
	checkGitOnce.Do(func() {
		_, err := exec.LookPath("git")
		if err != nil {
			if runtime.GOOS == "darwin" {
				checkGitErr = fmt.Errorf("git not found: install Xcode Command Line Tools with: xcode-select --install")
			} else {
				checkGitErr = fmt.Errorf("git not found: install git from https://git-scm.com/downloads")
			}
		}
	})
	return checkGitErr
}

// EnsureClone clones a repo into dir (using the real git binary) if .git is not already present.
func EnsureClone(repoURL, dir, ref string) error {
	return ensureClone(repoURL, dir, ref, runGit)
}

// EnsureCloneWith is like EnsureClone but accepts a custom git runner for testing.
func EnsureCloneWith(repoURL, dir, ref string, runGitFn func(args ...string) error) error {
	return ensureClone(repoURL, dir, ref, runGitFn)
}

func ensureClone(repoURL string, dir string, ref string, runGitFn func(args ...string) error) error {
	if err := CheckGit(); err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		if ref != "" {
			return checkoutRef(dir, ref, runGitFn)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// When a ref is specified, try --branch first (works for branches and tags,
	// single network round-trip). Fall back to clone-then-fetch for arbitrary
	// refs (e.g. commit SHAs) that --branch doesn't support.
	if ref != "" {
		err := runGitFn("clone", "--depth", "1", "--branch", ref, repoURL, dir)
		if err == nil {
			return nil
		}
		// --branch failed; fall back to default branch + fetch.
		_ = os.RemoveAll(dir)
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return mkErr
		}
	}
	if err := runGitFn("clone", "--depth", "1", repoURL, dir); err != nil {
		return err
	}
	if ref != "" {
		return checkoutRef(dir, ref, runGitFn)
	}
	return nil
}

func checkoutRef(dir string, ref string, runGitFn func(args ...string) error) error {
	if err := runGitFn("-C", dir, "fetch", "--depth", "1", "origin", ref); err != nil {
		return err
	}
	return runGitFn("-C", dir, "checkout", "--force", "FETCH_HEAD")
}

// GitHeadHash returns the HEAD commit SHA for the git repo at dir.
func GitHeadHash(dir string) (string, error) {
	return gitHeadHash(dir)
}

func gitHeadHash(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
func RunGit(args ...string) error {
	return runGit(args...)
}

func runGit(args ...string) error {
	_, err := runGitCore(args...)
	return err
}

// ErrArchiveNotSupported indicates the remote does not support git archive --remote.
var ErrArchiveNotSupported = errors.New("remote does not support git archive --remote")

// ErrArchivePathNotFound indicates git archive failed because a requested path
// does not exist in the repo. This usually means the pack manifest (pack.json)
// declares content that hasn't been committed.
var ErrArchivePathNotFound = errors.New("archive path not found")

// GitArchiveFiles fetches specific files/directories from a remote git repo
// using `git archive --remote`. Returns the raw tar stream as bytes.
// Caller is responsible for extracting and validating the archive content.
//
// The ref defaults to "HEAD" if empty. Paths are passed directly to git archive.
// Directory paths are fetched recursively by git archive.
//
// Returns ErrArchiveNotSupported if the remote does not support git archive.
func GitArchiveFiles(repoURL, ref string, paths []string) ([]byte, error) {
	return gitArchiveFiles(repoURL, ref, paths, runGitOutput)
}

// GitArchiveFilesWith is like GitArchiveFiles but accepts a custom git runner for testing.
func GitArchiveFilesWith(repoURL, ref string, paths []string, runFn func(args ...string) ([]byte, error)) ([]byte, error) {
	return gitArchiveFiles(repoURL, ref, paths, runFn)
}

func gitArchiveFiles(repoURL, ref string, paths []string, runFn func(args ...string) ([]byte, error)) ([]byte, error) {
	if err := CheckGit(); err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	args := []string{"archive", "--remote=" + repoURL, ref}
	args = append(args, paths...)
	return runFn(args...)
}

// runGitCore runs a git command with shared setup (timeout, env, error formatting)
// and returns stdout bytes. Both runGit and runGitOutput delegate to this.
func runGitCore(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

// runGitOutput runs a git command and returns its stdout as bytes.
// Adds archive-specific error classification on top of runGitCore.
func runGitOutput(args ...string) ([]byte, error) {
	out, err := runGitCore(args...)
	if err != nil {
		return nil, classifyArchiveError(err)
	}
	return out, nil
}

// classifyArchiveError maps generic git errors to archive-specific sentinel errors.
func classifyArchiveError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "operation not supported") ||
		strings.Contains(lower, "does not appear to support") {
		return ErrArchiveNotSupported
	}
	// "archiver died" can mean the remote doesn't support archive, OR a
	// pathspec didn't match. A missing file is a content error (pack.json
	// declares files not in the repo), not a capability error.
	if strings.Contains(lower, "archiver died") {
		if strings.Contains(lower, "pathspec") {
			return fmt.Errorf("%w: %s", ErrArchivePathNotFound, msg)
		}
		return ErrArchiveNotSupported
	}
	// GitHub (and some other forges) reject git archive --remote over HTTPS
	// with HTTP 422, producing: "expected ACK/NAK, got a flush packet".
	if strings.Contains(lower, "expected ack/nak") {
		return ErrArchiveNotSupported
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
			if strings.HasPrefix(arg, "git@") && strings.Contains(arg, ":") && !strings.Contains(arg, "://") {
				// SCP-style URL defaults to port 22; Bitbucket Server typically uses 7999.
				host := arg[len("git@"):]
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
				return "hint: HTTPS git requires credentials. Try one of:\n" +
					"  - Use SSH URL instead: git@<host>:<project>/<repo>.git\n" +
					"  - Configure a credential helper: git config --global credential.helper osxkeychain\n" +
					"  - Set up a personal access token"
			}
		}
		return "hint: git authentication failed. Check your SSH key or credential configuration."
	}

	return ""
}
