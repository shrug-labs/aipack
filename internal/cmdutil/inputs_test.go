package cmdutil

import (
	"strings"
	"testing"
)

func TestParseHarnessEnv_TrimsAndSkipsEmpty(t *testing.T) {
	t.Parallel()

	got := ParseHarnessEnv(" codex, , opencode ,, ")
	want := []string{"codex", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("ParseHarnessEnv returned %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseHarnessEnv returned %#v, want %#v", got, want)
		}
	}
}

func TestResolveHarnesses_ErrorWhenNoHarness(t *testing.T) {
	// No t.Parallel: uses env.
	t.Setenv(DefaultHarnessEnv, "")

	_, err := ResolveHarnesses(nil)
	if err == nil {
		t.Fatal("expected error when no harness configured, got nil")
	}
	if !strings.Contains(err.Error(), "no harness configured") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "no harness configured")
	}
}

func TestResolveHarnesses_UsesEnvHarness(t *testing.T) {
	// No t.Parallel: uses env.
	t.Setenv(DefaultHarnessEnv, "opencode")

	hs, err := ResolveHarnesses(nil)
	if err != nil {
		t.Fatalf("ResolveHarnesses returned error: %v", err)
	}
	if len(hs) != 1 || string(hs[0]) != "opencode" {
		t.Fatalf("ResolveHarnesses = %#v, want [opencode]", hs)
	}
}
