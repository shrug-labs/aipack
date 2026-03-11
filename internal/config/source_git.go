package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EnsureClone clones a repo into dir (using the real git binary) if .git is not already present.
func EnsureClone(repoURL, dir, ref string) error {
	return ensureClone(repoURL, dir, ref, runGit)
}

// EnsureCloneWith is like EnsureClone but accepts a custom git runner for testing.
func EnsureCloneWith(repoURL, dir, ref string, runGitFn func(args ...string) error) error {
	return ensureClone(repoURL, dir, ref, runGitFn)
}

func ensureClone(repoURL string, dir string, ref string, runGitFn func(args ...string) error) error {
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
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
