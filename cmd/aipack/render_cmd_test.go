package main

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestRender_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "render", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("render --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}
