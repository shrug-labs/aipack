package cmdutil

import (
	"bytes"
	"encoding/json"
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

func TestWriteJSONEscapesStringContent(t *testing.T) {
	t.Parallel()

	type searchLikeResult struct {
		Name    string `json:"name"`
		Snippet string `json:"snippet"`
	}

	input := searchLikeResult{
		Name:    `"}], "injected": true, "x":"`,
		Snippet: "line one\n</script>&\"'}]",
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, input); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("WriteJSON output should end with newline, got %q", buf.String())
	}

	var decoded searchLikeResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("WriteJSON output is invalid JSON: %v\n%s", err, buf.String())
	}
	if decoded != input {
		t.Fatalf("decoded payload = %#v, want %#v", decoded, input)
	}

	var object map[string]any
	if err := json.Unmarshal(buf.Bytes(), &object); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if _, ok := object["injected"]; ok {
		t.Fatalf("string content escaped into JSON structure: %s", buf.String())
	}
}
