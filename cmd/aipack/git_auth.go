package main

import (
	"context"

	"github.com/shrug-labs/aipack/internal/source"
)

func (g *Globals) gitContext(ctx context.Context, jsonOutput bool) (context.Context, context.CancelFunc) {
	if g.NonInteractive || jsonOutput || !g.StdinTTY || !g.StderrTTY {
		return source.WithoutGitSession(ctx), func() {}
	}
	if source.GitMayPrompt(ctx) {
		return ctx, func() {}
	}
	return source.WithGitSession(ctx, g.Stdin, g.Stderr)
}
