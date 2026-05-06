package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
)

func TestConfigEnvSetGet(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if _, stderr, code := runApp(t, "config", "env", "set", "API_TOKEN", "secret", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("config env set exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	stdout, stderr, code := runApp(t, "config", "env", "get", "API_TOKEN", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env get exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "secret" {
		t.Fatalf("config env get stdout = %q, want secret", stdout)
	}
}

func TestConfigEnvPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "config", "env", "path", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env path exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	want := filepath.Join(configDir, ".env")
	if strings.TrimSpace(stdout) != want {
		t.Fatalf("config env path stdout = %q, want %q", stdout, want)
	}
}

func TestConfigEnvListJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	if _, stderr, code := runApp(t, "config", "env", "set", "API_TOKEN", "secret", "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("config env set exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	stdout, stderr, code := runApp(t, "config", "env", "list", "--json", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("config env list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got struct {
		Path    string `json:"path"`
		Entries []struct {
			Key    string `json:"key"`
			Value  string `json:"value,omitempty"`
			Length int    `json:"length"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal config env list JSON: %v\nstdout=%s", err, stdout)
	}
	if got.Path != filepath.Join(configDir, ".env") {
		t.Fatalf("path = %q, want config .env", got.Path)
	}
	if len(got.Entries) != 1 || got.Entries[0].Key != "API_TOKEN" || got.Entries[0].Value != "" || got.Entries[0].Length != len("secret") {
		t.Fatalf("entries = %+v, want masked API_TOKEN with length", got.Entries)
	}
}
