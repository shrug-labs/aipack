package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCloneWith_FreshClone(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	var calls []string
	mock := func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		// Simulate git clone creating .git dir.
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	if err := EnsureCloneWith("https://example.com/repo.git", dir, "", mock); err != nil {
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
	mock := func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if len(args) >= 1 && args[0] == "clone" {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	if err := EnsureCloneWith("https://example.com/repo.git", dir, "v1.0", mock); err != nil {
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
	mock := func(args ...string) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		// --branch fails (e.g. commit SHA), plain clone succeeds.
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

	if err := EnsureCloneWith("https://example.com/repo.git", dir, "abc123", mock); err != nil {
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

func TestEnsureCloneWith_AlreadyCloned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Pre-create .git to simulate existing clone.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls []string
	mock := func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}

	if err := EnsureCloneWith("https://example.com/repo.git", dir, "", mock); err != nil {
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
	mock := func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}

	if err := EnsureCloneWith("https://example.com/repo.git", dir, "main", mock); err != nil {
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
	mock := func(args ...string) error {
		return fmt.Errorf("git failed")
	}

	err := EnsureCloneWith("https://example.com/repo.git", dir, "", mock)
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
	mock := func(args ...string) error {
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

	err := EnsureCloneWith("https://example.com/repo.git", dir, "v1.0", mock)
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

func TestGitArchiveFilesWith_Success(t *testing.T) {
	t.Parallel()
	wantData := []byte("fake-tar-data")
	var capturedArgs []string
	mock := func(args ...string) ([]byte, error) {
		capturedArgs = args
		return wantData, nil
	}
	got, err := GitArchiveFilesWith("git@example.com:org/repo.git", "main", []string{"pack.json", "rules/"}, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, wantData) {
		t.Fatalf("got %q, want %q", got, wantData)
	}
	wantArgs := []string{"archive", "--remote=git@example.com:org/repo.git", "main", "pack.json", "rules/"}
	if len(capturedArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", capturedArgs, wantArgs)
	}
	for i, w := range wantArgs {
		if capturedArgs[i] != w {
			t.Fatalf("arg[%d] = %q, want %q", i, capturedArgs[i], w)
		}
	}
}

func TestGitArchiveFilesWith_DefaultRef(t *testing.T) {
	t.Parallel()
	var capturedArgs []string
	mock := func(args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte("data"), nil
	}
	_, err := GitArchiveFilesWith("git@example.com:org/repo.git", "", []string{"file.txt"}, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty ref should default to "HEAD"
	if len(capturedArgs) < 3 {
		t.Fatalf("expected at least 3 args, got %v", capturedArgs)
	}
	if capturedArgs[2] != "HEAD" {
		t.Fatalf("ref arg = %q, want %q", capturedArgs[2], "HEAD")
	}
}

func TestGitArchiveFilesWith_ArchiveNotSupported(t *testing.T) {
	t.Parallel()
	mock := func(args ...string) ([]byte, error) {
		return nil, ErrArchiveNotSupported
	}
	_, err := GitArchiveFilesWith("https://github.com/org/repo.git", "main", []string{"file.txt"}, mock)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrArchiveNotSupported) {
		t.Fatalf("got %v, want ErrArchiveNotSupported", err)
	}
}

func TestGitArchiveFilesWith_ArchivePathNotFound(t *testing.T) {
	t.Parallel()
	mock := func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%w: pathspec 'rules/missing.md' did not match", ErrArchivePathNotFound)
	}
	_, err := GitArchiveFilesWith("git@example.com:org/repo.git", "main", []string{"rules/missing.md"}, mock)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrArchivePathNotFound) {
		t.Fatalf("got %v, want ErrArchivePathNotFound", err)
	}
	if errors.Is(err, ErrArchiveNotSupported) {
		t.Fatal("should not be ErrArchiveNotSupported for path-not-found errors")
	}
}

func TestGitArchiveFilesWith_GenericError(t *testing.T) {
	t.Parallel()
	mock := func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("git archive failed: something went wrong")
	}
	_, err := GitArchiveFilesWith("git@example.com:org/repo.git", "main", []string{"file.txt"}, mock)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrArchiveNotSupported) {
		t.Fatal("should not be ErrArchiveNotSupported for generic errors")
	}
}
