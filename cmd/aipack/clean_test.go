package main

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestClean_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "clean", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("clean --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestClean_InvalidScope_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "clean", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("clean --scope badscope should fail, got exit=%d", code)
	}
}
