package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func writeAutoSyncPackAddFixture(t *testing.T, autoSync bool) (home, configDir, projectDir string) {
	t.Helper()

	home = t.TempDir()
	projectDir = filepath.Join(home, "project")
	configDir = filepath.Join(home, ".config", "aipack")
	packDir := filepath.Join(configDir, "packs", "demo")
	for _, dir := range []string{
		projectDir,
		filepath.Join(configDir, "profiles"),
		filepath.Join(packDir, "rules"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	autoSyncLine := ""
	if autoSync {
		autoSyncLine = "  auto_sync: true\n"
	}
	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\n" + autoSyncLine
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte("schema_version: 2\npacks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "inactive.yaml"), []byte("schema_version: 2\npacks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePackManifestCmd(t, packDir, "demo")
	rule := "---\nname: demo-rule\ndescription: demo rule\nmetadata:\n  owner: test\n  last_updated: 2026-05-11\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(packDir, "rules", "demo-rule.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, configDir, projectDir
}

func TestPackList_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "list", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackList_JSON_Empty(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(entries))
	}
}

func TestPackList_JSON_WithPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "test-pack")

	_ = app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
		Link:      true,
		Add:       false,
	}, nil)

	stdout, stderr, code := runApp(t, "pack", "list", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack list --json exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}

	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%s", err, stdout)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["name"] != "test-pack" {
		t.Fatalf("name = %v, want test-pack", entries[0]["name"])
	}
}

func TestPackCreate_DefaultNameRejected(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, code := runApp(t, "pack", "create", "default", "--local", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("expected pack create default to fail")
	}
	if !strings.Contains(stderr, "reserved") {
		t.Fatalf("expected reserved-name error, got stderr=%q", stderr)
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "default")); !os.IsNotExist(err) {
		t.Fatalf("reserved pack should not be created, stat err=%v", err)
	}
}

func TestPackUpdate_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack update --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackAdd_AutoSyncsActiveProfileWhenEnabled(t *testing.T) {
	home, configDir, projectDir := writeAutoSyncPackAddFixture(t, true)
	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "pack", "add", "demo", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack add exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, `auto-sync: syncing active profile "default"`) {
		t.Fatalf("stdout missing auto-sync line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "sync OK [") {
		t.Fatalf("stdout missing sync success:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".claude", "rules", "demo-rule.md")); err != nil {
		t.Fatalf("expected project rule from auto-sync: %v", err)
	}
}

func TestPackAdd_DoesNotAutoSyncInactiveProfile(t *testing.T) {
	home, configDir, projectDir := writeAutoSyncPackAddFixture(t, true)
	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "pack", "add", "demo", "--profile", "inactive", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack add exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.Contains(stdout, "auto-sync:") || strings.Contains(stdout, "sync OK [") {
		t.Fatalf("inactive profile should not auto-sync, got:\n%s", stdout)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "rules", "demo-rule.md"),
		filepath.Join(".claude", "rules", "demo-rule__aipack__demo.md"),
	} {
		if _, err := os.Stat(filepath.Join(projectDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("inactive profile auto-sync wrote project rule %s; stat err=%v", rel, err)
		}
	}
}

func TestPackAdd_DoesNotAutoSyncWhenDisabled(t *testing.T) {
	home, configDir, projectDir := writeAutoSyncPackAddFixture(t, false)
	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "pack", "add", "demo", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack add exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if strings.Contains(stdout, "auto-sync:") || strings.Contains(stdout, "sync OK [") {
		t.Fatalf("auto_sync disabled should not sync, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: run 'aipack sync'") {
		t.Fatalf("auto_sync disabled should print sync hint, got:\n%s", stdout)
	}
}

func TestPackImport_CLI(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(source, []byte("Summarize the active incident.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t,
		"pack", "import", source,
		"--type", "prompt",
		"--name", "prompt-pack",
		"--id", "incident-summary",
		"--config-dir", configDir,
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack import exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, `Imported Prompt "incident-summary" into pack "prompt-pack"`) {
		t.Fatalf("stdout missing import summary:\n%s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "packs", "prompt-pack", "prompts", "incident-summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Summarize the active incident.") {
		t.Fatalf("imported prompt missing body:\n%s", raw)
	}
}

func TestPackImport_CLIExistingPack(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := filepath.Join(configDir, "packs", "ops-pack")
	writePackManifestCmd(t, packDir, "ops-pack")

	source := filepath.Join(t.TempDir(), "triage.md")
	if err := os.WriteFile(source, []byte("Triage before escalating.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t,
		"pack", "import", source,
		"--type", "skill",
		"--pack", "ops-pack",
		"--config-dir", configDir,
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack import existing exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	if !strings.Contains(stdout, `Imported Skill "triage" into pack "ops-pack"`) {
		t.Fatalf("stdout missing import summary:\n%s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(packDir, "skills", "triage", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Triage before escalating.") {
		t.Fatalf("imported skill missing body:\n%s", raw)
	}
}

func TestPackInspect_CLIJSON(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "inspect-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "safety.md"), []byte("---\nname: safety\ndescription: Safety\n---\n\nSafety body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "pack", "inspect", packDir, "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["name"] != "inspect-pack" || got["status"] != "inspected" {
		t.Fatalf("unexpected inspect JSON: %v", got)
	}
	counts, ok := got["counts"].(map[string]any)
	if !ok || counts["rules"].(float64) != 1 {
		t.Fatalf("counts = %v", got["counts"])
	}
}

func TestPackInspect_CLIClearWipesInspectedRows(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "clear-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "preview.md"), []byte("---\nname: preview\ndescription: Preview\n---\n\nBody.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := runApp(t, "pack", "inspect", packDir, "--config-dir", configDir); code != cmdutil.ExitOK {
		t.Fatalf("seeding inspect failed: code=%d stderr=%s", code, stderr)
	}
	if stdout, _, code := runApp(t, "search", "--status", "inspected", "--config-dir", configDir, "--json"); code != cmdutil.ExitOK || !strings.Contains(stdout, "clear-pack") {
		t.Fatalf("expected inspected row before clear, stdout=%s", stdout)
	}

	stdout, stderr, code := runApp(t, "pack", "inspect", "--clear", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect --clear exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if removed, _ := got["removed"].(float64); removed != 1 {
		t.Fatalf("expected removed=1, got %v", got)
	}
	if stdout, _, code := runApp(t, "search", "--status", "inspected", "--config-dir", configDir, "--json"); code != cmdutil.ExitOK || strings.Contains(stdout, "clear-pack") {
		t.Fatalf("inspected row should be gone after --clear, stdout=%s", stdout)
	}

	if _, stderr, code := runApp(t, "pack", "inspect", "--clear", packDir, "--config-dir", configDir); code == cmdutil.ExitOK {
		t.Fatalf("--clear with input arg must fail; stderr=%s", stderr)
	}
}

func TestPackInspect_CLIURLArchiveDoesNotRequireInput(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"url-preview","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Preview prompt.\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	stdout, stderr, code := runApp(t, "pack", "inspect", "--url", server.URL+"/pack.zip", "--archive", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect --url exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["name"] != "url-preview" || got["method"] != config.MethodArchive {
		t.Fatalf("unexpected inspect JSON: %v", got)
	}
}

func TestPackInspect_CLIURLArchiveContentPathsKeepRepoRelativeValues(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/skills/triage/SKILL.md": "---\nname: triage\ndescription: Use when triaging test fixtures.\n---\nbody\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	stdout, stderr, code := runApp(t,
		"pack", "inspect",
		"--url", server.URL+"/content.zip",
		"--archive",
		"--name", "content-preview",
		"--skills", "skills",
		"--config-dir", configDir,
		"--json",
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect content_paths exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	counts, ok := got["counts"].(map[string]any)
	if got["name"] != "content-preview" || !ok || counts["skills"].(float64) != 1 {
		t.Fatalf("unexpected inspect JSON: %v", got)
	}
}

func TestShouldTreatPackSourceAsArchiveSkipsGitURLShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"https://example.com/pack.zip", true},
		{"ssh://host/org/repo.zip", false},
		{"git@host:org/repo.zip", false},
		{"github.com/org/repo.zip", false},
	}
	for _, tt := range tests {
		if got := shouldTreatPackSourceAsArchive(tt.input); got != tt.want {
			t.Errorf("shouldTreatPackSourceAsArchive(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestPackInspect_InfersRemoteArchiveFromPositionalURL(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"url-inferred-preview","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Preview prompt.\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	stdout, stderr, code := runApp(t, "pack", "inspect", server.URL+"/pack.zip", "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect positional archive URL exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["name"] != "url-inferred-preview" || got["method"] != config.MethodArchive {
		t.Fatalf("unexpected inspect JSON: %v", got)
	}
}

func TestPackInspect_InfersLocalArchiveFromPath(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"local-inferred-preview","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Preview prompt.\n",
	})
	archivePath := filepath.Join(t.TempDir(), "pack.zip")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	stdout, stderr, code := runApp(t, "pack", "inspect", archivePath, "--config-dir", configDir, "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack inspect local archive exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if got["name"] != "local-inferred-preview" || got["method"] != config.MethodArchive {
		t.Fatalf("unexpected inspect JSON: %v", got)
	}
}

func TestPackInstall_InfersRemoteArchiveFromPositionalURL(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"url-inferred-install","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Installed prompt.\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", server.URL+"/pack.zip", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack install positional archive URL exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["url-inferred-install"]
	if meta.Method != config.MethodArchive || meta.Origin != server.URL+"/pack.zip" {
		t.Fatalf("lockfile meta = %+v", meta)
	}
}

func TestPackInstall_CLIURLArchiveContentPathsKeepRepoRelativeValues(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/skills/triage/SKILL.md": "---\nname: triage\ndescription: Use when triaging test fixtures.\n---\nbody\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	_, stderr, code := runApp(t,
		"pack", "install",
		"--url", server.URL+"/content.zip",
		"--archive",
		"--name", "content-install",
		"--skills", "skills",
		"--config-dir", configDir,
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack install content_paths exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["content-install"]
	if meta.ContentPaths[domain.CategorySkills] != "skills" {
		t.Fatalf("content_paths.skills = %q, want skills", meta.ContentPaths[domain.CategorySkills])
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "content-install", "skills", "triage", "SKILL.md")); err != nil {
		t.Fatalf("installed content-path skill missing: %v", err)
	}
}

func TestPackInstall_InfersRemoteArchiveFromURLFlag(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"url-flag-inferred-install","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Installed prompt.\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "--url", server.URL+"/pack.zip", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack install --url archive exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["url-flag-inferred-install"]
	if meta.Method != config.MethodArchive || meta.Origin != server.URL+"/pack.zip" {
		t.Fatalf("lockfile meta = %+v", meta)
	}
}

func TestPackInstall_InfersLocalArchiveFromPath(t *testing.T) {
	t.Parallel()
	archive := buildCLIPackZip(t, map[string]string{
		"repo/pack.json":      `{"schema_version":2,"name":"local-inferred-install","version":"1.0.0","root":"."}`,
		"repo/prompts/run.md": "Installed prompt.\n",
	})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	archiveDir, err := os.MkdirTemp(wd, ".tmp-pack-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(archiveDir) })
	archivePath := filepath.Join(archiveDir, "pack.zip")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	archiveArg, err := filepath.Rel(wd, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	expectedOrigin, err := filepath.Abs(archiveArg)
	if err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", archiveArg, "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("pack install local archive exit=%d, want %d; stderr=%s", code, cmdutil.ExitOK, stderr)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["local-inferred-install"]
	if meta.Method != config.MethodArchive || meta.Origin != expectedOrigin {
		t.Fatalf("lockfile meta = %+v", meta)
	}
}

func TestPackInstall_MultipleRegistryPacks(t *testing.T) {
	t.Parallel()
	archives := map[string][]byte{
		"/alpha.zip": buildCLIPackZip(t, map[string]string{
			"repo/pack.json": `{"schema_version":2,"name":"alpha-pack","version":"1.0.0","root":"."}`,
		}),
		"/beta.zip": buildCLIPackZip(t, map[string]string{
			"repo/pack.json": `{"schema_version":2,"name":"beta-pack","version":"1.0.0","root":"."}`,
		}),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(registryPath, []byte(`schema_version: 1
packs:
  alpha-pack:
    method: archive
    url: `+server.URL+`/alpha.zip
  beta-pack:
    method: archive
    url: `+server.URL+`/beta.zip
`), 0o600); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	stdout, stderr, code := runApp(t,
		"pack", "install", "alpha-pack", "beta-pack",
		"--registry", registryPath,
		"--config-dir", configDir,
		"--add",
		"-w", "all",
	)
	if code != cmdutil.ExitOK {
		t.Fatalf("multi pack install exit=%d, want %d; stderr=%s stdout=%s", code, cmdutil.ExitOK, stderr, stdout)
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha-pack", "beta-pack"} {
		meta, ok := lf.Packs[name]
		if !ok {
			t.Fatalf("lockfile missing %s", name)
		}
		if meta.Method != config.MethodArchive {
			t.Fatalf("%s method = %q, want archive", name, meta.Method)
		}
		for _, cat := range domain.AllBundledCategories {
			if !slices.Contains(meta.Approved, cat) {
				t.Fatalf("%s approved = %v, missing %s", name, meta.Approved, cat)
			}
		}
	}
	profile, err := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gotProfilePacks := map[string]bool{}
	for _, entry := range profile.Packs {
		gotProfilePacks[entry.Name] = true
	}
	if !gotProfilePacks["alpha-pack"] || !gotProfilePacks["beta-pack"] {
		t.Fatalf("profile packs = %+v, want alpha-pack and beta-pack", profile.Packs)
	}
}

func TestPackInstall_MultipleRejectsURLFlags(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "pack", "install", "alpha-pack", "beta-pack", "--url", "https://example.com/repo.git")
	if code == cmdutil.ExitOK {
		t.Fatal("multi pack install with --url should fail")
	}
	if !strings.Contains(stderr, "--url and path argument are mutually exclusive") {
		t.Fatalf("stderr = %s", stderr)
	}
}

func buildCLIPackZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPackUpdate_MutualExclusion(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--all", "my-pack")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack update --all+name should fail, got exit=%d", code)
	}
}

// TestPackInstall_NonSemverVersionTreatedAsLiteralRef verifies that
// `pack install --version main` is dispatched as a literal ref through
// the clone path rather than rejected at the CLI validation gate. The
// install still fails downstream (fake URL), but the failure must NOT
// be an "invalid version" short-circuit.
func TestPackInstall_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "pack", "install", "--url", "https://example.com/repo", "--version", "main")
	if code == cmdutil.ExitOK {
		t.Fatalf("install with fake URL should still fail downstream, got exit=%d", code)
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
}

func TestPackUpdate_VersionAndAllMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--all", "--version", "1.0.0")
	if code == cmdutil.ExitOK {
		t.Fatalf("--version + --all should fail, got exit=%d", code)
	}
}

func TestPackVersions_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "versions", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack versions --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackVersions_MissingName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "versions")
	if code == cmdutil.ExitOK {
		t.Fatal("pack versions (no args) should fail")
	}
}

// TestPackVersions_NotInstalledOrRegistered exercises the full Kong → service
// → error wiring for `pack versions`. With an empty config dir the lockfile
// has no entry and the registry has nothing to fall back to, so the service
// returns the "not installed and not found in registry" error. The test
// asserts the error reaches stderr in the expected shape.
func TestPackVersions_NotInstalledOrRegistered(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "versions", "ghost-pack", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("pack versions on missing pack should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "ghost-pack") || !strings.Contains(stderr, "not installed") {
		t.Errorf("stderr should mention name and 'not installed', got: %s", stderr)
	}
}

// TestPackVersions_CopyPackRejects verifies that running `pack versions`
// against a copy-method install (no remote git origin) surfaces the
// service-layer error through the CLI. Proves the lockfile lookup wiring
// works without needing real network access.
func TestPackVersions_CopyPackRejects(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packDir := t.TempDir()
	writePackManifestCmd(t, packDir, "local-pack")

	if err := app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  packDir,
		ConfigDir: configDir,
	}, nil); err != nil {
		t.Fatalf("seeding pack install: %v", err)
	}

	_, stderr, code := runApp(t, "pack", "versions", "local-pack", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("pack versions on copy install should fail, got exit=%d", code)
	}
	if !strings.Contains(stderr, "remote clone install") {
		t.Errorf("stderr should mention 'remote clone install', got: %s", stderr)
	}
}

// TestPackInstall_VersionInNameParses verifies the name@version positional
// parser strips the version before registry lookup. With no registry and an
// empty config the install fails, but the failure must reference the parsed
// pack name ("ghost-pack") and NOT the raw input ("ghost-pack@1.2.3"). That
// difference is the only end-to-end proof the parser ran.
func TestPackInstall_VersionInNameParses(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@1.2.3", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("install of nonexistent registry pack should fail, got exit=%d", code)
	}
	if strings.Contains(stderr, "ghost-pack@1.2.3") {
		t.Errorf("stderr leaked raw name@version (parser did not strip): %s", stderr)
	}
	if !strings.Contains(stderr, "ghost-pack") {
		t.Errorf("stderr should reference parsed name 'ghost-pack', got: %s", stderr)
	}
}

// TestPackInstall_VersionInNameAndFlagCollide verifies the @<ref> + --ref
// collision check at the install entry point. Both forms of intent are
// rejected with a clear error rather than silently letting one win.
// Covers both flag spellings since --version is a Kong alias for --ref.
func TestPackInstall_VersionInNameAndFlagCollide(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@1.2.3", "--version", "2.0.0", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("expected collision error, got exit=%d", code)
	}
	if !strings.Contains(stderr, "@<ref>") {
		t.Errorf("stderr should explain the @<ref> collision, got: %s", stderr)
	}
}

// TestPackInstall_NonSemverInNameTreatedAsLiteralRef verifies that the
// @<spec> shorthand parses non-semver specs (like "ghost-pack@main") as
// literal refs instead of rejecting them. The install still fails
// downstream (pack not in registry), but the error path must go through
// registry lookup, not an "invalid version" short-circuit.
func TestPackInstall_NonSemverInNameTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "ghost-pack@main", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatalf("install of nonexistent registry pack should fail, got exit=%d", code)
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
	// The error must come from registry lookup (not validation), proving
	// the @version parser passed "main" through to the install dispatch.
	if !strings.Contains(stderr, "ghost-pack") {
		t.Errorf("stderr should reference parsed name 'ghost-pack', got: %s", stderr)
	}
}

func TestPackUpdate_BareUpdatesAll(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "update", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("bare pack update should succeed on empty config, got exit=%d stderr=%s", code, stderr)
	}
}

// TestPackInstall_BareReconcilesProfile verifies the new default: `pack
// install` with no arguments routes to profile reconciliation (equivalent to
// -m/--missing), succeeding on an empty config instead of erroring.
func TestPackInstall_BareReconcilesProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, stderr, code := runApp(t, "pack", "install", "--config-dir", configDir)
	if code != cmdutil.ExitOK {
		t.Fatalf("bare pack install should succeed on empty config, got exit=%d stderr=%s", code, stderr)
	}
}

// TestPackInstall_BareWithWith verifies that combining bare install with
// --with still triggers the profile-reconciliation + --with error — implicit
// and explicit missing should behave identically.
func TestPackInstall_BareWithWith(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "install", "-w", "all")
	if code == cmdutil.ExitOK {
		t.Fatal("bare pack install with --with should error (reconciliation + --with conflict)")
	}
}

func TestPackUpdate_VersionWithoutName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "update", "--version", "1.0.0")
	if code == cmdutil.ExitOK {
		t.Fatal("pack update --version without a name should fail")
	}
}

// TestPackUpdate_NonSemverVersionTreatedAsLiteralRef verifies that
// `pack update my-pack --version main` no longer short-circuits on
// "invalid version". Under the unified ref model, the update dispatches
// through the literal-ref path. The command still fails (pack not
// installed in the empty config), but the error must not be a validation
// rejection.
func TestPackUpdate_NonSemverVersionTreatedAsLiteralRef(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	stdout, stderr, code := runApp(t, "pack", "update", "my-pack", "--version", "main", "--config-dir", configDir)
	if code == cmdutil.ExitOK {
		t.Fatal("pack update against missing pack should fail")
	}
	if strings.Contains(stdout, "error:") {
		t.Fatalf("pack update errors should be reported on stderr only, got stdout: %s", stdout)
	}
	if !strings.Contains(stderr, "my-pack") || !strings.Contains(stderr, "not installed") {
		t.Fatalf("pack update error should mention missing pack on stderr, got: %s", stderr)
	}
	if strings.Contains(stderr, "invalid version") {
		t.Fatalf("unified ref model should not reject 'main' as invalid version; stderr: %s", stderr)
	}
}

func TestPackShow_HelpReturnsOK(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show", "--help")
	if code != cmdutil.ExitOK {
		t.Fatalf("pack show --help exit=%d, want %d", code, cmdutil.ExitOK)
	}
}

func TestPackShow_MissingName(t *testing.T) {
	t.Parallel()
	_, _, code := runApp(t, "pack", "show")
	if code == cmdutil.ExitOK {
		t.Fatalf("pack show (no args) should fail, got exit=%d", code)
	}
}

func TestPackShow_NotInstalled(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	_, _, code := runApp(t, "pack", "show", "nonexistent", "--config-dir", configDir)
	if code != cmdutil.ExitFail {
		t.Fatalf("pack show nonexistent exit=%d, want %d", code, cmdutil.ExitFail)
	}
}

// ---------------------------------------------------------------------------
// parseWithFlag
// ---------------------------------------------------------------------------

func TestParseWithFlag_Empty(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
	got2, err := parseWithFlag([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != nil {
		t.Errorf("empty input should return nil, got %v", got2)
	}
}

func TestParseWithFlag_All(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range domain.AllBundledCategories {
		if !got.Has(cat) {
			t.Errorf("all should include %q", cat)
		}
	}
}

func TestParseWithFlag_AllMixedCase(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"ALL"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) {
		t.Error("ALL (uppercase) should expand to all categories")
	}
}

func TestParseWithFlag_AllShortCircuits(t *testing.T) {
	t.Parallel()
	// "all" combined with other values should still return all.
	got, err := parseWithFlag([]string{"profiles", "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range domain.AllBundledCategories {
		if !got.Has(cat) {
			t.Errorf("all should include %q even when combined", cat)
		}
	}
}

func TestParseWithFlag_Aliases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		alias string
		want  domain.BundledCategory
	}{
		{"p", domain.BundledProfiles},
		{"r", domain.BundledRegistries},
		{"e", domain.BundledExtras},
		{"P", domain.BundledProfiles}, // case-insensitive
	}
	for _, tc := range cases {
		got, err := parseWithFlag([]string{tc.alias})
		if err != nil {
			t.Fatalf("alias %q: %v", tc.alias, err)
		}
		if !got.Has(tc.want) {
			t.Errorf("alias %q should expand to %q", tc.alias, tc.want)
		}
	}
}

func TestParseWithFlag_Invalid(t *testing.T) {
	t.Parallel()
	_, err := parseWithFlag([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestParseWithFlag_Whitespace(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{" profiles ", " extras"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) || !got.Has(domain.BundledExtras) {
		t.Errorf("should trim whitespace, got %v", got)
	}
}

func TestParseWithFlag_Duplicates(t *testing.T) {
	t.Parallel()
	got, err := parseWithFlag([]string{"profiles", "profiles", "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has(domain.BundledProfiles) {
		t.Error("duplicates should not cause errors")
	}
}

func writePackManifestCmd(t *testing.T, dir string, name string) {
	t.Helper()
	m := map[string]any{
		"schema_version": 2,
		"name":           name,
		"version":        "1.0.0",
		"root":           ".",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
