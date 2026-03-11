package main

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestSync_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestSync_InvalidScope_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--scope", "badscope")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --scope badscope should fail, got exit=%d", code)
	}
}

func TestSync_UnknownFlag_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--mode", "merge")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --mode (unknown flag) should fail, got exit=%d", code)
	}
}

func TestSync_ProjectDir_InvalidWithGlobalScope(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "sync", "--scope", "global", "--project-dir", "/tmp/proj")
	if code == cmdutil.ExitOK {
		t.Fatalf("sync --scope global --project-dir should fail, got exit=%d", code)
	}
}
