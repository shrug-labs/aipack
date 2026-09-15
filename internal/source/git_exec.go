package source

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type gitSessionKey struct{}

// A session grants terminal access only to operations within one CLI command.
type gitSession struct {
	stdin  io.Reader
	stderr io.Writer
	gate   chan struct{}
	cancel context.CancelFunc
}

func WithGitSession(ctx context.Context, stdin io.Reader, stderr io.Writer) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	return context.WithValue(ctx, gitSessionKey{}, &gitSession{stdin, stderr, make(chan struct{}, 1), cancel}), cancel
}

func WithoutGitSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, gitSessionKey{}, (*gitSession)(nil))
}

func gitSessionFrom(ctx context.Context) *gitSession {
	session, _ := ctx.Value(gitSessionKey{}).(*gitSession)
	return session
}

func GitMayPrompt(ctx context.Context) bool { return gitSessionFrom(ctx) != nil }

// executeGit owns subprocess setup, capture, cancellation, and diagnostics.
// A provider may supply its own environment; execution stays serialized and cancellable.
func executeGit(ctx context.Context, env []string, args ...string) ([]byte, error) {
	session := gitSessionFrom(ctx)
	budget := 2 * time.Minute
	if len(args) > 0 && args[0] == "ls-remote" {
		budget = 30 * time.Second
	}
	if session != nil {
		select {
		case session.gate <- struct{}{}:
			defer func() { <-session.gate }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		budget = 5 * time.Minute
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if env == nil {
		env = nonInteractiveGitEnv()
		if session != nil {
			env = os.Environ()
		}
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if session != nil {
		cmd.Stdin = session.stdin
		cmd.Stderr = io.MultiWriter(session.stderr, &stderr)
	}
	restore, err := prepareGitProcess(cmd)
	if err != nil {
		return nil, err
	}
	defer restore()
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	if session != nil && gitProcessInterrupted(cmd.ProcessState) {
		session.cancel()
		return nil, context.Canceled
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		hint := gitErrorHint(message, args)
		if session == nil && strings.Contains(strings.ToLower(message), "permission denied (publickey)") {
			hint = "Git interactive prompts were disabled. Run the ordinary command in a terminal, or prepare an SSH agent for unattended use."
		}
		if hint != "" {
			message += "\n\n" + hint
		}
		return nil, fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, message)
	}
	return stdout.Bytes(), nil
}
