package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestPackDeleteCLI_JSONReportsCleanup(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "deletable")

	if err := app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
	}, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	rendered := filepath.Join(t.TempDir(), ".claude", "rules", "rendered.md")
	if err := os.MkdirAll(filepath.Dir(rendered), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rendered, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("body"), "deletable", nil, time.Now())
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "pack", "delete", "deletable", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack delete exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if got["name"] != "deletable" {
		t.Fatalf("name = %v", got["name"])
	}
	if sourceRemoved, _ := got["source_removed"].(bool); !sourceRemoved {
		t.Fatalf("source_removed = %v", got["source_removed"])
	}
	if renderedRemoved, _ := got["rendered_removed"].(float64); renderedRemoved != 1 {
		t.Fatalf("rendered_removed = %v, want 1", got["rendered_removed"])
	}
	if _, err := os.Stat(rendered); !os.IsNotExist(err) {
		t.Fatalf("rendered file should be removed")
	}
}

func TestPackDeleteCLI_KeepRendered(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "kept")

	if err := app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
	}, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	rendered := filepath.Join(t.TempDir(), "rendered.md")
	if err := os.WriteFile(rendered, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	lg := domain.NewLedger()
	lg.Record(rendered, []byte("body"), "kept", nil, time.Now())
	if err := eng.SaveLedger(ledgerPath, lg, false); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "pack", "delete", "kept", "--keep-rendered", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack delete --keep-rendered exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, `"keep_rendered": true`) {
		t.Fatalf("JSON should report keep_rendered=true: %s", stdout)
	}
	if _, err := os.Stat(rendered); err != nil {
		t.Fatalf("rendered file should persist: %v", err)
	}
}
