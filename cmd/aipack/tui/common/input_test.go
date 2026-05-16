package common

import "testing"

func TestAppendSingleLinePasteNormalizesTerminalPaste(t *testing.T) {
	t.Parallel()

	got := AppendSingleLinePaste("prefix-", "alpha\r\nbeta\n")
	if got != "prefix-alpha beta" {
		t.Fatalf("AppendSingleLinePaste() = %q, want %q", got, "prefix-alpha beta")
	}
}
