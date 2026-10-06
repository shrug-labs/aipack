package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestSyncRecoversReplacementBeforeLoadingProfile(t *testing.T) {
	for _, candidate := range []string{"uncommitted rule body", "invalid manifest"} {
		t.Run(candidate, func(t *testing.T) {
			home, cfg, project := writeSyncFixture(t)
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Chdir(project)
			pack := filepath.Join(cfg, "packs/demo")
			backup := filepath.Join(cfg, "packs/.demo.pending-old")
			if err := os.Rename(pack, backup); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(pack, "rules"), 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := `{"schema_version":2,"name":"demo","root":"."}`
			if candidate == "invalid manifest" {
				manifest = "{"
			}
			if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pack, "rules/sample.md"), []byte("uncommitted rule body"), 0o600); err != nil {
				t.Fatal(err)
			}
			op := map[string]any{"name": "demo", "stage": filepath.Join(cfg, ".tmp/pack-staging/interrupted"), "digest": strings.Repeat("0", 64), "had_previous": true}
			body, err := json.Marshal(op)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(cfg, ".tmp/pack-operations")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "demo.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			files, err := plugin.ReadFiles(pack)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(files)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(raw)
			op["digest"] = hex.EncodeToString(hash[:])
			body, err = json.Marshal(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "demo.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			out, stderr, code := runApp(t, "sync", "--yes", "--config-dir", cfg)
			if code != 0 {
				t.Fatalf("sync: exit=%d %s %s", code, out, stderr)
			}
			delivered, err := os.ReadFile(filepath.Join(project, ".claude/rules/sample.md"))
			if err != nil || strings.Contains(string(delivered), "uncommitted") || !strings.Contains(string(delivered), "body") {
				t.Fatalf("delivered stale profile: %s %v", delivered, err)
			}
		})
	}
}

func TestRunSync_DryRunShowsRemovedAndDisabledContent(t *testing.T) {
	for _, hid := range domain.AllHarnesses() {
		for _, scope := range []domain.Scope{domain.ScopeProject, domain.ScopeGlobal} {
			for _, action := range []string{"remove", "disable"} {
				for _, autoSync := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/auto=%t", hid, scope, action, autoSync), func(t *testing.T) {
						home, cfg, project := writeSyncFixture(t)
						t.Setenv("HOME", home)
						t.Setenv("USERPROFILE", home)
						for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "CLINE_DIR", "CLINE_DATA_DIR"} {
							t.Setenv(key, "")
						}
						if runtime.GOOS == "windows" && hid == domain.HarnessCline {
							t.Setenv("CLINE_DIR", filepath.Join(home, "Documents", "Cline"))
						}
						t.Chdir(project)
						sc, err := config.LoadSyncConfig(config.SyncConfigPath(cfg))
						if err != nil {
							t.Fatal(err)
						}
						sc.Defaults.Scope, sc.Defaults.Harnesses, sc.Defaults.AutoSync = string(scope), []string{string(hid)}, autoSync
						if err := config.SaveSyncConfig(config.SyncConfigPath(cfg), sc); err != nil {
							t.Fatal(err)
						}
						run := func(args ...string) string {
							t.Helper()
							out, stderr, code := runApp(t, append(args, "--config-dir", cfg)...)
							if code != 0 {
								t.Fatalf("%v: exit=%d %s %s", args, code, out, stderr)
							}
							return out
						}
						run("sync", "--yes")
						run("pack", action, "demo")
						base := project
						if scope == domain.ScopeGlobal {
							base = home
						}
						rel := map[domain.Harness]string{domain.HarnessClaudeCode: ".claude/rules/sample.md", domain.HarnessOpenCode: ".opencode/rules/sample.md", domain.HarnessCodex: "AGENTS.override.md", domain.HarnessCline: ".clinerules/sample.md"}[hid]
						if scope == domain.ScopeGlobal {
							rel = map[domain.Harness]string{domain.HarnessClaudeCode: ".claude/rules/sample.md", domain.HarnessOpenCode: ".config/opencode/rules/sample.md", domain.HarnessCodex: ".codex/AGENTS.override.md", domain.HarnessCline: "Documents/Cline/Rules/sample.md"}[hid]
						}
						path := filepath.Join(base, rel)
						ledger := engine.LedgerPath(cfg, scope, project, hid)
						before, err := os.ReadFile(ledger)
						if err != nil {
							t.Fatal(err)
						}
						for _, flags := range [][]string{{"--dry-run"}, {"--dry-run", "--verbose"}} {
							out := run(append([]string{"sync"}, flags...)...)
							if !autoSync && (!strings.Contains(out, "stale: ") || !strings.Contains(out, filepath.Base(path))) {
								t.Fatalf("%v omitted the pending stale deletion: %s", flags, out)
							}
							if autoSync && strings.Contains(out, "stale: ") {
								t.Fatalf("auto-sync left pending cleanup: %s", out)
							}
							after, err := os.ReadFile(ledger)
							if err != nil || string(after) != string(before) {
								t.Fatalf("preview changed the ledger: %v", err)
							}
							if _, err := os.Stat(path); !autoSync && err != nil {
								t.Fatalf("preview deleted rendered content: %v", err)
							}
						}
						run("sync", "--yes")
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Fatalf("sync retained removed content: %v", err)
						}
						if out := run("sync", "--dry-run"); strings.Contains(out, "stale:") {
							t.Fatalf("cleanup did not converge: %s", out)
						}
					})
				}
			}
		}
	}
}

func TestRunSync_DryRunVerboseDoesNotAppendZeroSummary(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run", "--verbose")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "plan: 1 changes") {
		t.Fatalf("expected verbose dry-run plan in stdout, got: %s", stdout)
	}
	if strings.Contains(stdout, "dry-run: 0 content, 0 settings") {
		t.Fatalf("unexpected zero-summary appended to verbose dry-run output: %s", stdout)
	}
}

func TestRunSync_DryRunJSONIsValidJSONOnly(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run", "--json")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	// Sync emits one result object per harness; the fixture targets a single harness.
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if len(got) != 1 {
		t.Fatalf("got %d harness results, want 1\nstdout=%s", len(got), stdout)
	}
	if got[0]["harness"] != string(domain.HarnessClaudeCode) {
		t.Fatalf("harness = %#v, want claudecode", got[0]["harness"])
	}
	if got[0]["rules"] != float64(1) {
		t.Fatalf("rules = %#v, want 1", got[0]["rules"])
	}
	if got[0]["mcp"] != float64(0) {
		t.Fatalf("mcp = %#v, want 0", got[0]["mcp"])
	}
}

func TestRunSync_DryRunDoesNotMutateProfile(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	// mcp server declaration required — an empty servers map gives
	// load+marshal nothing to materialize and defeats the test.
	packDir := filepath.Join(configDir, "packs", "demo")
	manifest := `{"schema_version":2,"name":"demo","root":".","mcp":["example"]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpServer := `{"name":"example","command":["echo","hi"]}`
	if err := os.WriteFile(filepath.Join(packDir, "mcp", "example.json"), []byte(mcpServer), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-alphabetical params keys and `mcp: {}` both exercise
	// yaml.Marshal normalization paths that would mutate the file on a
	// naive load+save cycle.
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	original := []byte("schema_version: 2\n" +
		"params:\n" +
		"  zebra: first\n" +
		"  apple: second\n" +
		"packs:\n" +
		"  - name: demo\n" +
		"    enabled: true\n" +
		"mcp: {}\n")
	if err := os.WriteFile(profilePath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--dry-run")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync --dry-run: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	got, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("sync --dry-run mutated profile:\n--- original\n%s\n--- after\n%s", original, got)
	}
}

func TestRunSync_HarnessAllSyncsEachHarness(t *testing.T) {
	home, configDir, projectDir := writeSyncFixture(t)

	t.Setenv("HOME", home)
	t.Setenv("AIPACK_NO_UPDATE_CHECK", "1")
	t.Chdir(projectDir)

	stdout, stderr, code := runApp(t, "sync", "--config-dir", configDir, "--yes", "--harness", "all")
	if code != cmdutil.ExitOK {
		t.Fatalf("sync --harness all exit=%d, want %d; stdout=%s stderr=%s", code, cmdutil.ExitOK, stdout, stderr)
	}
	// Each harness syncs independently and reports its own OK line.
	for _, want := range []string{"sync OK [claudecode]", "sync OK [codex]"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing per-harness sync line %q:\n%s", want, stdout)
		}
	}
}

func TestResolveWatchDirs_ReturnsPackRootsWhenProfileContentIsInvalid(t *testing.T) {
	home, configDir, _ := writeSyncFixture(t)
	t.Setenv("HOME", home)

	packDir := filepath.Join(configDir, "packs", "demo")
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := resolveWatchDirs("", "", configDir)
	if err != nil {
		t.Fatalf("resolveWatchDirs returned error: %v", err)
	}

	want := packDir
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("resolveWatchDirs = %v, want [%s]", dirs, want)
	}
}

func writeSyncFixture(t *testing.T) (home, configDir, projectDir string) {
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

	syncCfg := "schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses:\n    - claudecode\ninstalled_packs: {}\n"
	if err := os.WriteFile(filepath.Join(configDir, "sync-config.yaml"), []byte(syncCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := "schema_version: 2\npacks:\n  - name: demo\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "default.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schema_version":2,"name":"demo","root":"."}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	rule := "---\nname: sample\ndescription: sample rule\nmetadata:\n  owner: test\n  last_updated: 2026-03-14\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(packDir, "rules", "sample.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}

	return home, configDir, projectDir
}
