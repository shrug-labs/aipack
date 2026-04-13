package app

import (
	"os"
	"path/filepath"
	"testing"
)

func writePromptFile(t *testing.T, packDir, id, body string) {
	t.Helper()
	dir := filepath.Join(packDir, "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPromptList_AutodiscoversPromptsDir verifies that PromptList finds prompts
// dropped into a pack's prompts/ directory even when the manifest omits the
// Prompts field — matching the autodiscovery behavior of rules/agents/workflows/skills.
func TestPromptList_AutodiscoversPromptsDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(PacksDir(configDir), "auto-pack")
	writePackManifest(t, packDir, "auto-pack")
	writePromptFile(t, packDir, "incident-summary", "---\ndescription: Summarize an incident\n---\nbody\n")

	prompts, err := PromptList(configDir)
	if err != nil {
		t.Fatalf("PromptList: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d: %+v", len(prompts), prompts)
	}
	if prompts[0].Name != "incident-summary" || prompts[0].Pack != "auto-pack" {
		t.Errorf("got %+v", prompts[0])
	}
	if prompts[0].Description != "Summarize an incident" {
		t.Errorf("description = %q", prompts[0].Description)
	}
}

// TestPromptList_ManifestDeclaredStillWorks verifies the legacy path — a manifest
// that explicitly lists prompts — continues to work unchanged.
func TestPromptList_ManifestDeclaredStillWorks(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(PacksDir(configDir), "declared-pack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":1,"name":"declared-pack","version":"1.0.0","root":".","prompts":["greet"]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	writePromptFile(t, packDir, "greet", "---\ndescription: Say hi\n---\nhi\n")
	writePromptFile(t, packDir, "extra", "---\ndescription: Unlisted\n---\nhey\n")

	prompts, err := PromptList(configDir)
	if err != nil {
		t.Fatalf("PromptList: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Name != "greet" {
		t.Fatalf("expected only declared prompt 'greet', got %+v", prompts)
	}
}
