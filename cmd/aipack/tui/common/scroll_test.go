package common

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTruncateTextFitsDisplayWidth(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"abcdefghijklmnopqrstuvwxyz",
		"界界界界界界",
		lipgloss.NewStyle().Bold(true).Render("abcdefghijklmnopqrstuvwxyz"),
	} {
		got := TruncateText(input, 8)
		if lipgloss.Width(got) > 8 {
			t.Fatalf("TruncateText(%q) width = %d, want <= 8; got %q", input, lipgloss.Width(got), got)
		}
		if !strings.Contains(got, "…") {
			t.Fatalf("TruncateText(%q) = %q, want ellipsis", input, got)
		}
	}
}

func TestTruncateTextLeavesShortInputAlone(t *testing.T) {
	t.Parallel()

	if got := TruncateText("short", 8); got != "short" {
		t.Fatalf("TruncateText short input = %q, want short", got)
	}
	if got := TruncateText("short", 0); got != "" {
		t.Fatalf("TruncateText width 0 = %q, want empty", got)
	}
}

func TestTruncateTextPreservesANSIReset(t *testing.T) {
	t.Parallel()

	input := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("standard-coding-rules-and-more")
	got := TruncateText(input, 24)

	if lipgloss.Width(got) > 24 {
		t.Fatalf("TruncateText styled width = %d, want <= 24; got %q", lipgloss.Width(got), got)
	}
	if ansi.Strip(got) != "standard-coding-rules-a…" {
		t.Fatalf("TruncateText styled strip = %q", ansi.Strip(got))
	}
	if !strings.Contains(got, "\x1b[0m") && !strings.Contains(got, "\x1b[m") {
		t.Fatalf("TruncateText styled output is missing ANSI reset: %q", got)
	}
}
