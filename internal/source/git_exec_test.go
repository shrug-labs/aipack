package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func fullCacheSeed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fakeSessionGit(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitSessionNativeAuthenticationIO(t *testing.T) {
	fakeSessionGit(t, `
test "$GIT_SSH_COMMAND" = 'custom-ssh --identity selected' || exit 20
test -z "$GIT_TERMINAL_PROMPT" || exit 21
printf 'credential prompt\n' >&2
read -r answer
test "$answer" = fixture-input || exit 22
printf 'authenticated\n'
`)
	t.Setenv("GIT_SSH_COMMAND", "custom-ssh --identity selected")
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	var stderr bytes.Buffer
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader("fixture-input\n"), &stderr)
	defer cancel()
	out, err := runGitCore(ctx, "clone", "ssh://example/repo")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "authenticated\n" || stderr.String() != "credential prompt\n" {
		t.Fatalf("stdout=%q stderr=%q", out, stderr.String())
	}
	if strings.Contains(stderr.String(), "fixture-input") {
		t.Fatal("input was written to diagnostic output")
	}
}

func TestGitSessionRemoteQueriesUseSamePolicy(t *testing.T) {
	fakeSessionGit(t, `
test "$GIT_SSH_COMMAND" = chosen-ssh || exit 20
test "$1" = ls-remote || exit 21
case "$2" in
  --tags) printf 'abc123\trefs/tags/v1.2.3\n' ;;
  *) printf 'abc123\tHEAD\n' ;;
esac
`)
	t.Setenv("GIT_SSH_COMMAND", "chosen-ssh")
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader(""), io.Discard)
	defer cancel()
	if hash, err := LsRemoteHead(ctx, "ssh://example/repo", ""); err != nil || hash != "abc123" {
		t.Fatalf("head=%q err=%v", hash, err)
	}
	if tags, err := ListRemoteTags(ctx, "ssh://example/repo"); err != nil || len(tags) != 1 || tags[0] != "v1.2.3" {
		t.Fatalf("tags=%v err=%v", tags, err)
	}
}

func TestGitSessionBackgroundAndCacheCannotPrompt(t *testing.T) {
	fakeSessionGit(t, `
case "$GIT_SSH_COMMAND" in *BatchMode=yes*) ;; *) exit 20 ;; esac
test "$GIT_TERMINAL_PROMPT" = 0 || exit 21
test "$SSH_ASKPASS_REQUIRE" = never || exit 22
printf 'git@example: Permission denied (publickey).\n' >&2
exit 128
`)
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader(""), io.Discard)
	defer cancel()
	for _, c := range []context.Context{context.Background(), WithoutGitSession(ctx)} {
		_, err := runGitCore(c, "clone", "ssh://example/repo")
		if err == nil || !strings.Contains(err.Error(), "interactive prompts were disabled") {
			t.Fatalf("missing unattended authentication guidance: %v", err)
		}
		_, err = LsRemoteHead(c, "ssh://example/repo", "")
		if err == nil || !strings.Contains(err.Error(), "interactive prompts were disabled") {
			t.Fatalf("missing remote-query guidance: %v", err)
		}
	}
	called := false
	err := UpdateBareCache(ctx, "ssh://example/repo", fullCacheSeed(t), t.TempDir(), func(c context.Context, args ...string) error {
		called = true
		if GitMayPrompt(c) {
			t.Error("optional cache operation inherited terminal access")
		}
		return os.MkdirAll(args[len(args)-1], 0o700)
	})
	if err != nil || !called {
		t.Fatalf("cache called=%v err=%v", called, err)
	}
}

func TestGitSessionSerializesConcurrentProcesses(t *testing.T) {
	fakeSessionGit(t, `
mkdir "$AIPACK_GIT_TEST_DIR/active" || exit 70
sleep 0.03
rmdir "$AIPACK_GIT_TEST_DIR/active"
`)
	t.Setenv("AIPACK_GIT_TEST_DIR", t.TempDir())
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader(""), io.Discard)
	defer cancel()
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := runGitCore(ctx, "fetch"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestGitSessionCancelledWaitDoesNotRun(t *testing.T) {
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader(""), io.Discard)
	session := gitSessionFrom(ctx)
	session.gate <- struct{}{}
	cancel()
	_, err := runGitCore(ctx, "must-not-start")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	<-session.gate
}

func TestGitSessionCancellationStopsProcessTree(t *testing.T) {
	fakeSessionGit(t, "sleep 30 &\nwait\n")
	ctx, cancel := WithGitSession(context.Background(), strings.NewReader(""), io.Discard)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 150*time.Millisecond)
	defer timeout()
	start := time.Now()
	_, err := runGitCore(ctx, "fetch")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Git child kept pipes open after cancellation: %s", elapsed)
	}
}

func TestUnattendedGitCancellationStopsProcessTree(t *testing.T) {
	fakeSessionGit(t, "sleep 30 &\nwait\n")
	for _, remoteQuery := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		start := time.Now()
		var err error
		if remoteQuery {
			_, err = LsRemoteHead(ctx, "ssh://example/repo", "")
		} else {
			_, err = runGitCore(ctx, "fetch")
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("remoteQuery=%v: %v", remoteQuery, err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("unattended child retained pipes: %s", elapsed)
		}
	}
}

func TestEnsureCloneDoesNotRetryAuthenticationOrCancellation(t *testing.T) {
	for _, failure := range []error{
		errors.New("Permission denied (publickey)"),
		errors.New("Host key verification failed"),
		errors.New("Connection timed out"),
		context.Canceled,
		fmt.Errorf("git: %w", context.DeadlineExceeded),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			calls := 0
			err := EnsureCloneWith(context.Background(), "ssh://example/repo", filepath.Join(t.TempDir(), "clone"), "main", func(context.Context, ...string) error {
				calls++
				return failure
			})
			if calls != 1 || !errors.Is(err, failure) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
