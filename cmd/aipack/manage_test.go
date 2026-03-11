package main

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestManage_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "manage", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("manage --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestManage_NoTTY_ReturnsUsage(t *testing.T) {
	t.Parallel()
	_, _, code := runAppWithInput(t, "", false, "manage")
	if code != cmdutil.ExitUsage {
		t.Fatalf("manage (no TTY) exit=%d, want %d", code, cmdutil.ExitUsage)
	}
}
