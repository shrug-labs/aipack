package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestRegistryFetchExplicitNativeFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"market","plugins":[{"name":"probe","source":{"source":"npm","package":"probe@1"}}]}`))
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"registry", "fetch", server.URL + "/catalog.json", "--format", "claude", "--name", "market"},
		{"registry", "fetch", server.URL + "/catalog.json", "--name", "market"},
	} {
		_, stderr, code := runApp(t, append(args, "--config-dir", dir)...)
		if code != cmdutil.ExitOK {
			t.Fatalf("fetch exit=%d: %s", code, stderr)
		}
	}
	stdout, stderr, code := runApp(t, "registry", "sources", "--json", "--config-dir", dir)
	var sources []struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal([]byte(stdout), &sources); err != nil || code != cmdutil.ExitOK || len(sources) != 1 || sources[0].Format != "claude" {
		t.Fatalf("sources format lost: %s %s %v", stdout, stderr, err)
	}
	for _, args := range [][]string{
		{"registry", "fetch", "--format", "claude"},
		{"registry", "fetch", server.URL, "--format", "typo"},
	} {
		if _, stderr, code := runApp(t, append(args, "--config-dir", dir)...); code == cmdutil.ExitOK || !strings.Contains(stderr, "format") {
			t.Fatalf("invalid format flags accepted: %v exit=%d: %s", args, code, stderr)
		}
	}
}

func TestRegistryFetchLocalDirectoryPath(t *testing.T) {
	t.Parallel()
	root, configDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(`{"name":"market","plugins":[{"name":"probe","source":"./probe"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runApp(t, "registry", "fetch", root, "--path", "catalog.json", "--format", "codex-legacy", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("directory catalog fetch exit=%d: %s", code, stderr)
	}
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil || len(sc.RegistrySources) != 1 || sc.RegistrySources[0].URL != root || sc.RegistrySources[0].Path != "catalog.json" {
		t.Fatalf("directory source lost coordinates: %+v %v", sc.RegistrySources, err)
	}
}

func TestRegistryValidate_JSONValid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(`schema_version: 1
packs:
  demo:
    repo: https://github.com/example/demo.git
    description: Demo pack
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "registry", "validate", path, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("registry validate exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["valid"] != true {
		t.Fatalf("valid = %v, want true; output=%s", got["valid"], stdout)
	}
}

func TestRegistryList_JSONIncludesWinnerAndShadowedSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "team", URL: "https://example.com/team.yaml"},
		{Name: "public", URL: "https://example.com/public.yaml"},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.RegistriesCacheDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, source := range sc.RegistrySources {
		body := "schema_version: 1\npacks:\n  demo:\n    repo: https://example.com/" + source.Name + ".git\n"
		if err := os.WriteFile(config.SourceCachePath(dir, source.Name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code := runApp(t, "registry", "list", "--json", "--config-dir", dir)
	if code != cmdutil.ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var results []struct {
		Name   string `json:"name"`
		Source struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"source"`
		Shadowed []struct {
			Name string `json:"name"`
		} `json:"shadowed_sources"`
	}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(results) != 1 || results[0].Name != "demo" {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Source.Name != "team" || results[0].Source.URL != "https://example.com/team.yaml" {
		t.Fatalf("winner = %+v", results[0].Source)
	}
	if len(results[0].Shadowed) != 1 || results[0].Shadowed[0].Name != "public" {
		t.Fatalf("shadowed = %+v", results[0].Shadowed)
	}

	human, _, code := runApp(t, "registry", "list", "--config-dir", dir)
	if code != cmdutil.ExitOK {
		t.Fatalf("human exit=%d", code)
	}
	if !strings.Contains(human, "Source:      team") || !strings.Contains(human, "Shadowed:    public") {
		t.Fatalf("human provenance missing:\n%s", human)
	}
}

func TestRegistryValidate_InvalidReportsAllErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, []byte(`schema_version: 1
packs:
  missing:
    description: Missing source
  archive:
    method: archive
    repo: https://github.com/example/not-used.git
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "registry", "validate", path)
	if code != cmdutil.ExitFail {
		t.Fatalf("registry validate exit=%d, want %d; stderr=%s stdout=%s", code, cmdutil.ExitFail, stderr, stdout)
	}
	if !strings.Contains(stdout, `pack "missing": missing required field repo`) {
		t.Fatalf("stdout missing missing-source error:\n%s", stdout)
	}
	if !strings.Contains(stdout, `pack "archive": missing required field url`) {
		t.Fatalf("stdout missing archive url error:\n%s", stdout)
	}
}
