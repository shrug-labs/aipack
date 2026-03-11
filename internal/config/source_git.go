package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CheckGit verifies that the git binary is available and returns an actionable
// error when it is missing (e.g. Xcode CLT not installed on macOS).
func CheckGit() error {
	_, err := exec.LookPath("git")
	if err != nil {
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("git not found: install Xcode Command Line Tools with: xcode-select --install")
		}
		return fmt.Errorf("git not found: install git from https://git-scm.com/downloads")
	}
	return nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	// Prevent git from prompting for credentials in non-interactive contexts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("git %s timed out after 2m", strings.Join(args, " "))
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		hint := gitErrorHint(msg, args)
		if hint != "" {
			return fmt.Errorf("git %s failed: %s\n\n%s", strings.Join(args, " "), msg, hint)
		}
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return nil
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
