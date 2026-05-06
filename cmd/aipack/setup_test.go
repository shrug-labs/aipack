package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestSetupShowsMissingEnvAndParamFixes(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(configDir, "packs", "setup-pack")
	writePackManifestCmd(t, packDir, "setup-pack")
	mcpDir := filepath.Join(packDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "srv.json"), []byte(`{
  "name": "srv",
  "transport": "stdio",
  "command": ["tool", "--region", "{params.region}", "--region-again", "{params.region}", "--token", "{env:API_TOKEN}", "--token-again", "{env:API_TOKEN}"],
  "available_tools": ["ping"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.MCP = []string{"srv"}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte("schema_version: 2\npacks:\n  - name: setup-pack\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "setup", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("setup exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	for _, want := range []string{
		"Profile: default",
		"Missing params",
		"region",
		"aipack profile set-param default region <value>",
		"Missing env",
		"API_TOKEN",
		"aipack config env set API_TOKEN <value>",
		filepath.Join(configDir, ".env"),
		"Next",
		"aipack sync",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("setup output missing %q:\n%s", want, stdout)
		}
	}
	if count := strings.Count(stdout, "aipack profile set-param default region <value>"); count != 1 {
		t.Fatalf("setup output should dedupe region set command, got %d:\n%s", count, stdout)
	}
	if count := strings.Count(stdout, "aipack config env set API_TOKEN <value>"); count != 1 {
		t.Fatalf("setup output should dedupe API_TOKEN set command, got %d:\n%s", count, stdout)
	}
}
