package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestImportedKeepRenderedCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AIPACK_TELEMETRY_DISABLED", "1")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	for _, kind := range []string{"hook", "skill", "mcp", "undelivered"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "mcp" && runtime.GOOS == "windows" {
				t.Skip("generic stdio MCP delivery requires POSIX")
			}
			src, cfg, project := t.TempDir(), t.TempDir(), t.TempDir()
			scope, deliveryRoot := "project", filepath.Join(project, ".opencode")
			if kind == "hook" {
				scope, deliveryRoot = "global", filepath.Join(project, "custom-opencode")
				t.Setenv("OPENCODE_CONFIG_DIR", deliveryRoot)
			}
			writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), []byte(`{"name":"probe"}`))
			writeFile(t, filepath.Join(src, "script.js"), []byte(`console.log("retained source")`))
			switch kind {
			case "hook":
				writeFile(t, filepath.Join(src, "hooks/hooks.json"), []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node $PLUGIN_ROOT/script.js"}]}]}}`))
			case "skill":
				writeFile(t, filepath.Join(src, "skills/probe/SKILL.md"), []byte("---\nname: probe\ndescription: Source asset fixture\n---\nRun ${PLUGIN_ROOT}/script.js.\n"))
			case "mcp":
				writeFile(t, filepath.Join(src, ".mcp.json"), []byte(`{"mcpServers":{"probe":{"command":"node","args":["script.js"],"cwd":"."}}}`))
			}
			run := func(args ...string) (string, int) {
				t.Helper()
				args = append(args, "--config-dir", cfg)
				if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
					out, err := exec.Command(binary, args...).CombinedOutput()
					if err != nil {
						return string(out), 1
					}
					return string(out), 0
				}
				out, stderr, code := runApp(t, args...)
				return out + stderr, code
			}
			if out, code := run("pack", "install", src, "--add"); code != 0 {
				t.Fatal(out)
			}
			if kind != "undelivered" {
				args := []string{"sync", "--harness", "opencode", "--scope", scope, "--yes"}
				if scope == "project" {
					args = append(args, "--project-dir", project)
				}
				if out, code := run(args...); code != 0 {
					t.Fatal(out)
				}
			}
			lockPath := config.LockfilePath(cfg)
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, flags := range [][]string{{"--dry-run"}, {}} {
				args := append([]string{"pack", "delete", "probe", "--keep-rendered"}, flags...)
				out, code := run(args...)
				if kind == "undelivered" {
					if code != 0 {
						t.Fatal(out)
					}
					continue
				}
				if code == 0 || !strings.Contains(out, "still depends on its installed source") {
					t.Fatalf("unsafe keep-rendered accepted: %s", out)
				}
				after, err := os.ReadFile(lockPath)
				if err != nil || string(after) != string(before) {
					t.Fatal("refusal changed installed ownership", err)
				}
				if _, err := os.Stat(filepath.Join(cfg, "packs/probe/upstream/script.js")); err != nil {
					t.Fatal("refusal removed source assets", err)
				}
			}
			if kind == "hook" {
				if out, code := run("pack", "delete", "probe"); code != 0 {
					t.Fatal(out)
				}
				if _, err := os.Stat(filepath.Join(deliveryRoot, "plugins/aipack-hooks.js")); !os.IsNotExist(err) {
					t.Fatalf("custom-root wrapper survived source deletion: %v", err)
				}
			}
		})
	}
}

func TestDirectRemotePluginCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "OPENCODE_CONFIG_DIR", "CLINE_DIR", "CLINE_DATA_DIR"} {
		t.Setenv(name, "")
	}
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins, plugin.Claude} {
		for _, catalog := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "/standalone", true: "/catalog"}[catalog], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "source.git")
				cfg, project := t.TempDir(), t.TempDir()
				t.Chdir(project)
				path := ""
				manifest := ".codex-plugin/plugin.json"
				body := `{"name":"probe","version":"1.0.0"}`
				catalogPath := ".agents/plugins/marketplace.json"
				if format == plugin.AgentPlugins {
					manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
				} else if format == plugin.Claude {
					manifest, catalogPath = ".claude-plugin/plugin.json", ".claude-plugin/marketplace.json"
				}
				if catalog {
					path = "plugins/probe"
					writeFile(t, filepath.Join(root, "pack.json"), []byte(`{"schema_version":2,"name":"root-pack","root":"."}`))
					writeFile(t, filepath.Join(root, catalogPath), []byte(`{"name":"owned","plugins":[{"name":"probe","source":"./plugins/probe","policy":{"installation":"AVAILABLE","authentication":"ON_USE"}}]}`))
				}
				writeFile(t, filepath.Join(root, path, manifest), []byte(body))
				skillPath := filepath.Join(root, path, "skills/probe/SKILL.md")
				writeFile(t, skillPath, []byte("---\nname: probe\ndescription: Direct import fixture\n---\nSOURCE_V1\n"))
				git := func(args ...string) {
					t.Helper()
					cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
					cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git %v: %v: %s", args, err, out)
					}
				}
				git("init", "--quiet")
				commit := func() {
					git("add", ".")
					git("-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")
				}
				commit()
				repo := (&url.URL{Scheme: "file", Path: filepath.ToSlash(root)}).String()
				writeFile(t, filepath.Join(cfg, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses: [cline]\n"))
				writeFile(t, filepath.Join(cfg, "profiles/default.yaml"), []byte("schema_version: 2\npacks: []\n"))
				run := func(success bool, args ...string) string {
					t.Helper()
					args = append(args, "--config-dir", cfg)
					if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
						out, err := exec.Command(binary, args...).CombinedOutput()
						if (err == nil) != success {
							t.Fatalf("%v: %v: %s", args, err, out)
						}
						return string(out)
					}
					out, stderr, code := runApp(t, args...)
					if (code == 0) != success {
						t.Fatalf("%v: exit=%d: %s %s", args, code, out, stderr)
					}
					return out
				}
				run(true, "pack", "inspect", repo, "--path", path)
				run(true, "pack", "install", repo, "--path", path, "--add")
				lf, err := config.LoadLockfile(config.LockfilePath(cfg))
				if err != nil || lf.Packs["probe"].Plugin == nil || lf.Packs["probe"].Plugin.Format != format {
					t.Fatalf("direct import lost native identity: %+v %v", lf.Packs["probe"], err)
				}
				sc, err := config.LoadSyncConfig(config.SyncConfigPath(cfg))
				if err != nil || len(sc.RegistrySources) != 0 {
					t.Fatalf("direct import fetched or registered a catalog: %+v %v", sc, err)
				}
				if catalog {
					if lf.Packs["probe"].Plugin.Marketplace != "owned" || lf.Packs["probe"].Plugin.MarketplacePath != catalogPath {
						t.Fatalf("direct import lost catalog source: %+v", lf.Packs["probe"].Plugin)
					}
					run(true, "registry", "fetch", repo)
					reg, err := config.LoadMergedRegistry(cfg)
					if err != nil || len(reg.Packs) != 2 || reg.Packs["probe"].Plugin == nil || reg.Packs["root-pack"].Repo != repo {
						t.Fatalf("bare fetch lost root pack or plugin: %+v %v", reg, err)
					}
					run(true, "pack", "install", "root-pack")
				}
				writeFile(t, skillPath, []byte("---\nname: probe\ndescription: Direct import fixture\n---\nSOURCE_V2\n"))
				commit()
				run(true, "pack", "update", "probe")
				bytes, err := os.ReadFile(filepath.Join(cfg, "packs/probe/upstream/skills/probe/SKILL.md"))
				if err != nil || !strings.Contains(string(bytes), "SOURCE_V2") {
					t.Fatalf("direct import lost update tracking: %s %v", bytes, err)
				}
				if format != plugin.Claude {
					run(true, "sync", "--harness", "cline", "--scope", "project", "--yes")
					bytes, err := os.ReadFile(filepath.Join(project, ".agents/skills/probe/SKILL.md"))
					if err != nil || !strings.Contains(string(bytes), "SOURCE_V2") {
						t.Fatalf("direct import was not delivered: %s %v", bytes, err)
					}
					if catalog {
						writeFile(t, filepath.Join(root, catalogPath), []byte(`{"name":"owned","plugins":[{"name":"probe","source":"./plugins/probe","policy":{"installation":"NOT_AVAILABLE"}}]}`))
						commit()
						run(false, "pack", "install", repo, "--path", path)
					}
				}
			})
		}
	}
}

func TestDirectLocalPluginCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins, plugin.Claude} {
		for _, catalog := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "/standalone", true: "/catalog"}[catalog], func(t *testing.T) {
				root, cfg := t.TempDir(), t.TempDir()
				payload := root
				manifest, catalogPath := ".codex-plugin/plugin.json", ".agents/plugins/marketplace.json"
				manifestBody := `{"name":"probe","version":"1.0.0"}`
				if format == plugin.AgentPlugins {
					manifest = "plugin.json"
					manifestBody = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
				} else if format == plugin.Claude {
					manifest, catalogPath = ".claude-plugin/plugin.json", ".claude-plugin/marketplace.json"
				}
				if catalog {
					payload = filepath.Join(root, "plugins/probe")
					writeFile(t, filepath.Join(root, "pack.json"), []byte(`{"schema_version":2,"name":"root-pack","root":"."}`))
					writeFile(t, filepath.Join(root, catalogPath), []byte(`{"name":"owned","plugins":[{"name":"probe","source":"./plugins/probe"}]}`))
				}
				writeFile(t, filepath.Join(payload, manifest), []byte(manifestBody))
				writeFile(t, filepath.Join(payload, "skills/probe/SKILL.md"), []byte("---\nname: probe\ndescription: Local fixture\n---\nSOURCE_V1\n"))
				run := func(args ...string) {
					t.Helper()
					args = append(args, "--config-dir", cfg)
					if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
						if out, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
							t.Fatalf("%v: %v: %s", args, err, out)
						}
					} else if out, stderr, code := runApp(t, args...); code != 0 {
						t.Fatalf("%v: exit=%d: %s %s", args, code, out, stderr)
					}
				}
				if catalog {
					run("registry", "fetch", root, "--name", "local")
					run("registry", "validate", config.SourceCachePath(cfg, "local"))
					run("pack", "install", "root-pack")
				}
				run("pack", "inspect", payload)
				run("pack", "install", payload, "--name", "alias")
				lf, err := config.LoadLockfile(config.LockfilePath(cfg))
				meta := lf.Packs["alias"]
				market := "probe"
				if catalog {
					market = "owned"
				}
				if err != nil || meta.Method != config.MethodCopy || meta.Plugin == nil || meta.Plugin.Marketplace != market || meta.Plugin.Format != format {
					t.Fatalf("local import lost identity or copy method: %+v %v", meta, err)
				}
				writeFile(t, filepath.Join(payload, "skills/probe/SKILL.md"), []byte("---\nname: probe\ndescription: Local fixture\n---\nSOURCE_V2\n"))
				run("pack", "update", "alias")
				body, err := os.ReadFile(filepath.Join(cfg, "packs/alias/upstream/skills/probe/SKILL.md"))
				if err != nil || !strings.Contains(string(body), "SOURCE_V2") {
					t.Fatalf("local update lost source: %s %v", body, err)
				}
			})
		}
	}
}

func TestQuietPluginCLI(t *testing.T) {
	for _, format := range []string{plugin.Claude, plugin.CodexLegacy, plugin.AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "OPENCODE_CONFIG_DIR", "CLINE_DIR", "CLINE_DATA_DIR"} {
				t.Setenv(name, "")
			}
			root, cfg, project := t.TempDir(), t.TempDir(), t.TempDir()
			t.Chdir(project)
			manifest, body := ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`
			if format == plugin.Claude {
				manifest = ".claude-plugin/plugin.json"
			} else if format == plugin.AgentPlugins {
				manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
			}
			writeFile(t, filepath.Join(root, "plugins/probe", manifest), []byte(body))
			writeFile(t, filepath.Join(root, "plugins/probe/skills/probe/SKILL.md"), []byte("---\nname: probe\ndescription: Quiet import fixture\n---\nPROBE_BODY\n"))
			catalog := filepath.Join(root, ".claude-plugin/marketplace.json")
			writeFile(t, catalog, []byte(`{"name":"fixture","plugins":[{"name":"probe","source":"./plugins/probe"}]}`))
			writeFile(t, filepath.Join(cfg, "sync-config.yaml"), []byte("schema_version: 1\ndefaults:\n  profile: default\n  scope: project\n  harnesses: [codex]\n"))
			writeFile(t, filepath.Join(cfg, "packs/base/pack.json"), []byte(`{"schema_version":2,"name":"base","root":"."}`))
			writeFile(t, filepath.Join(cfg, "packs/base/rules/base.md"), []byte("---\nname: base\ndescription: Ordinary content\n---\nBASE_BODY\n"))
			writeFile(t, filepath.Join(cfg, "profiles/default.yaml"), []byte("schema_version: 2\npacks:\n  - name: base\n"))
			run := func(wantSuccess bool, args ...string) string {
				t.Helper()
				args = append(args, "--config-dir", cfg)
				if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
					out, err := exec.Command(binary, args...).CombinedOutput()
					if (err == nil) != wantSuccess {
						t.Fatalf("%v: %v: %s", args, err, out)
					}
					return string(out)
				}
				out, stderr, code := runApp(t, args...)
				if (code == 0) != wantSuccess {
					t.Fatalf("%v: exit=%d: %s %s", args, code, out, stderr)
				}
				return out + stderr
			}
			run(true, "registry", "fetch", catalog, "--format", format)
			run(true, "pack", "install", "probe", "--add", "--quiet")
			for _, preview := range []bool{true, false} {
				args := []string{"sync", "--harness", "all", "--yes"}
				if preview {
					args = append(args, "--dry-run")
				}
				run(true, args...)
			}
			path := filepath.Join(project, "AGENTS.override.md")
			before, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(before), "BASE_BODY") {
				t.Fatalf("quiet import blocked ordinary rules: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg, "rendered-plugins")); !os.IsNotExist(err) {
				t.Fatalf("quiet import created native delivery: %v", err)
			}
			run(true, "profile", "include", "probe", "--kind", "skill", "--pack", "probe")
			if format == plugin.Claude {
				if out := run(false, "sync", "--harness", "codex", "--yes"); !strings.Contains(out, "delivery to codex is not supported") {
					t.Fatalf("selected incompatible content was not refused: %s", out)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatal("incompatible selection changed ordinary delivery")
				}
			} else {
				run(true, "sync", "--harness", "cline", "--yes")
				if _, err := os.Stat(filepath.Join(project, ".agents/skills/probe/SKILL.md")); err != nil {
					t.Fatal("explicit selection was not delivered", err)
				}
			}
			run(true, "profile", "exclude", "probe", "--kind", "skill", "--pack", "probe")
			run(true, "sync", "--harness", "all", "--yes")
			if _, err := os.Stat(filepath.Join(project, ".agents/skills/probe/SKILL.md")); !os.IsNotExist(err) {
				t.Fatalf("empty selection retained an old delivery: %v", err)
			}
		})
	}
}

func TestPluginHookCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, cfg, project := t.TempDir(), t.TempDir(), t.TempDir()
	assertCommand := func(path, marker string) {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, ".ps1") {
			_, encoded, found := strings.Cut(string(body), `FromBase64String("`)
			if !found {
				t.Fatal("PowerShell wrapper has no handler payload")
			}
			encoded, _, _ = strings.Cut(encoded, `"`)
			body, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(string(body), marker) {
			t.Fatalf("CLI hook command missing %s: %s", marker, body)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("plugins/probe/.codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`)
	hook := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"printf 'OWNED_CLI_HOOK_V1'"}]}],"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`
	write("plugins/probe/hooks/hooks.json", hook)
	write(".agents/plugins/marketplace.json", `{"name":"fixture","plugins":[{"name":"probe","source":"./plugins/probe"}]}`)
	run := func(args ...string) string {
		t.Helper()
		args = append(args, "--config-dir", cfg)
		if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s %v", args, out, err)
			}
			return string(out)
		}
		out, stderr, code := runApp(t, args...)
		if code != 0 {
			t.Fatalf("%v: %d: %s %s", args, code, out, stderr)
		}
		return out
	}
	t.Chdir(root)
	run("registry", "fetch", ".agents/plugins/marketplace.json")
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(cfg))
	if err != nil || len(sc.RegistrySources) != 1 {
		t.Fatalf("local registry registration: %+v %v", sc.RegistrySources, err)
	}
	t.Chdir(project)
	src := sc.RegistrySources[0]
	run("registry", "fetch", src.URL, "--path", src.Path, "--name", src.Name)
	run("pack", "install", "probe")
	if err := app.ProfileSave(app.ProfileSaveRequest{ConfigDir: cfg, Name: "selected", Config: config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(cfg, "packs/probe/pack.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	run("doctor", "--fix", "--profile", "selected")
	after, err := os.ReadFile(manifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("CLI doctor changed the imported manifest: %v", err)
	}
	for _, target := range []string{"opencode", "cline"} {
		args := []string{"sync", "--profile", "selected", "--scope", "project", "--project-dir", project, "--harness", target, "--yes"}
		wrapper := filepath.Join(project, ".opencode/plugins/aipack-hooks.js")
		if target == "cline" {
			wrapper = filepath.Join(project, ".clinerules/hooks/UserPromptSubmit")
			if runtime.GOOS == "windows" {
				wrapper += ".ps1"
			}
		}
		run(append(slices.Clone(args), "--dry-run")...)
		if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
			t.Fatal("CLI preview delivered a hook")
		}
		run(args...)
		assertCommand(wrapper, "OWNED_CLI_HOOK_V1")
		run("profile", "exclude", "codex-user-prompt-submit", "--kind", "hook", "--profile", "selected")
		run(args...)
		if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
			t.Fatal("CLI hook exclusion retained activation")
		}
		run("profile", "include", "codex-user-prompt-submit", "--kind", "hook", "--profile", "selected")
		run(args...)
		if _, err := os.Stat(wrapper); err != nil {
			t.Fatal("CLI hook inclusion did not restore activation", err)
		}
	}
	write("plugins/probe/hooks/hooks.json", strings.ReplaceAll(hook, "V1", "V2"))
	run("pack", "update", "probe")
	run("sync", "--profile", "selected", "--scope", "project", "--project-dir", project, "--harness", "opencode,cline", "--yes", "--dry-run")
	run("sync", "--profile", "selected", "--scope", "project", "--project-dir", project, "--harness", "opencode,cline", "--yes")
	for _, path := range []string{".opencode/plugins/aipack-hooks.js", ".clinerules/hooks/UserPromptSubmit"} {
		if runtime.GOOS == "windows" && strings.HasSuffix(path, "UserPromptSubmit") {
			path += ".ps1"
		}
		assertCommand(filepath.Join(project, path), "OWNED_CLI_HOOK_V2")
	}
}

func TestPluginPackCompatibilityCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			root, cfg := t.TempDir(), t.TempDir()
			write := func(rel, body string) {
				t.Helper()
				path := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path, body := ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`
			if format == plugin.AgentPlugins {
				path, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
			}
			write("plugins/probe/"+path, body)
			write("plugins/probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Portable fixture.\n---\nFixture.\n")
			mcp, mcpBody := ".mcp.json", `{"mcpServers":{"probe":{"type":"stdio","command":"node","args":["server.js"]}}}`
			components, unsupported := 3, 1
			portable := []string{"skills/probe"}
			if format == plugin.AgentPlugins {
				mcp = "mcp.json"
				mcpBody = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"node","args":["server.js"]}}}`
				components, unsupported = 2, 0 // Agent Plugins supplies cwd; legacy hooks remain inactive.
				portable = []string{"mcp/probe", "skills/probe"}
			}
			write("plugins/probe/"+mcp, mcpBody)
			write("plugins/probe/hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`)
			write(".agents/plugins/marketplace.json", `{"name":"fixture","plugins":[{"name":"probe","source":"./plugins/probe"}]}`)
			run := func(args ...string) string {
				t.Helper()
				args = append(args, "--config-dir", cfg)
				if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
					out, err := exec.Command(binary, args...).CombinedOutput()
					if err != nil {
						t.Fatalf("%v: %s %v", args, out, err)
					}
					return string(out)
				}
				out, stderr, code := runApp(t, args...)
				if code != 0 {
					t.Fatalf("%v: %d: %s %s", args, code, out, stderr)
				}
				return out
			}
			run("registry", "fetch", filepath.Join(root, ".agents/plugins/marketplace.json"))
			var inspected app.PackInspectResult
			if err := json.Unmarshal([]byte(run("pack", "inspect", "probe", "--json")), &inspected); err != nil {
				t.Fatal(err)
			}
			run("pack", "install", "probe")
			var shown app.PackShowEntry
			if err := json.Unmarshal([]byte(run("pack", "show", "probe", "--json")), &shown); err != nil {
				t.Fatal(err)
			}
			for _, reports := range [][]plugin.TargetCompatibility{inspected.Compatibility, shown.Compatibility} {
				if len(reports) != len(domain.AllHarnesses()) {
					t.Fatalf("missing target reports: %+v", reports)
				}
				for _, c := range reports {
					switch c.Target {
					case domain.HarnessCodex:
						if c.Delivery != "native" || len(c.Unsupported) != 0 || len(c.Supported) != components {
							t.Fatalf("native inventory lost components: %+v", c)
						}
					case domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessCline:
						want := slices.Clone(portable)
						if format == plugin.CodexLegacy && c.Target == domain.HarnessClaudeCode {
							want = append([]string{"hooks/codex-stop"}, want...)
						}
						if c.Delivery != "portable" || !slices.Equal(c.Supported, want) || len(c.Unsupported) != unsupported {
							t.Fatalf("portable report silently drops components: %+v", c)
						}
						if format == plugin.CodexLegacy && c.Target != domain.HarnessClaudeCode && len(c.Warnings) == 0 {
							t.Fatal("unavailable target event was not reported")
						}
					default:
						if c.Delivery != "unsupported" || len(c.Supported) != 0 || len(c.Unsupported) != components {
							t.Fatalf("unsupported target reported delivery: %+v", c)
						}
					}
				}
			}
			claudePortable := slices.Clone(portable)
			if format == plugin.CodexLegacy {
				claudePortable = append([]string{"hooks/codex-stop"}, claudePortable...)
			}
			phrases := []string{"all components", "codex: native", "claudecode: portable " + strings.Join(claudePortable, ", "), "mcp/probe", "opencode: portable " + strings.Join(portable, ", ")}
			if format == plugin.CodexLegacy {
				phrases = append(phrases, "hooks/codex-stop")
			}
			for _, out := range []string{run("pack", "inspect", "probe"), run("pack", "show", "probe")} {
				for _, phrase := range phrases {
					if !strings.Contains(out, phrase) {
						t.Fatalf("text inspection omits %q: %s", phrase, out)
					}
				}
			}
			// Inspection never activates the command or creates a native install.
			if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")); !os.IsNotExist(err) {
				t.Fatalf("inventory inspection changed native configuration: %v", err)
			}
			target := filepath.Join(root, "native")
			t.Setenv("OPENCODE_CONFIG_DIR", target)
			selected := "schema_version: 2\npacks:\n  - name: probe\n    settings:\n      enabled: false\n    hooks:\n      enabled: false\n    mcp:\n      probe:\n        enabled: false\n"
			write("selected.yaml", selected)
			profile, err := config.LoadProfile(filepath.Join(root, "selected.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if err := app.ProfileSave(app.ProfileSaveRequest{ConfigDir: cfg, Name: "portable-full", Config: profile}); err != nil {
				t.Fatal(err)
			}
			exclude := []string{"probe"}
			profile.Packs[0].Skills.Exclude = &exclude
			if err := app.ProfileSave(app.ProfileSaveRequest{ConfigDir: cfg, Name: "portable-review", Config: profile}); err != nil {
				t.Fatal(err)
			}
			run("profile", "set", "portable-full")
			syncArgs := []string{"sync", "--scope", "global", "--harness", "opencode", "--skip-settings", "--yes"}
			run(append(slices.Clone(syncArgs), "--dry-run")...)
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("CLI dry-run wrote native files: %v", err)
			}
			run(syncArgs...)
			var trace app.TraceResult
			if err := json.Unmarshal([]byte(run("trace", "skill", "probe", "--pack", "probe", "--profile-path", filepath.Join(root, "selected.yaml"), "--scope", "global", "--harness", "opencode", "--json")), &trace); err != nil || len(trace.Destinations) != 1 || trace.Source == nil || trace.Source.NativeBinding != "probe@fixture" {
				t.Fatalf("CLI portable trace: %+v %v", trace, err)
			}
			skillPath := filepath.Join(target, "skills/probe/SKILL.md")
			if trace.Destinations[0].Path != skillPath || trace.Destinations[0].Embedded {
				t.Fatal("CLI selected skill did not use ordinary delivery", trace.Destinations)
			}
			run("profile", "set", "portable-review")
			run(syncArgs...)
			if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
				t.Fatalf("CLI skill toggle retained activation: %v", err)
			}
			run("profile", "set", "portable-full")
			write("plugins/probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Portable fixture.\n---\nUPDATED_SAME_VERSION\n")
			run("pack", "update", "probe")
			run(syncArgs...)
			out := run("trace", "skill", "probe", "--pack", "probe", "--profile-path", filepath.Join(root, "selected.yaml"), "--scope", "global", "--harness", "opencode", "--json")
			if err := json.Unmarshal([]byte(out), &trace); err != nil {
				t.Fatal(err)
			}
			if len(trace.Destinations) != 1 || trace.Destinations[0].Path != skillPath {
				t.Fatal("CLI update lost ordinary delivery", trace.Destinations)
			}
			skillBody, err := os.ReadFile(skillPath)
			if err != nil || !strings.Contains(string(skillBody), "UPDATED_SAME_VERSION") {
				t.Fatalf("CLI update did not reach skill: %s %v", skillBody, err)
			}
			run("profile", "exclude", "probe", "--kind", "skill", "--profile", "portable-full")
			var selectedCounts []struct {
				Skills int `json:"skills"`
				Hooks  int `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(run(append(slices.Clone(syncArgs), "--dry-run", "--json")...)), &selectedCounts); err != nil || len(selectedCounts) != 1 || selectedCounts[0].Skills != 0 || selectedCounts[0].Hooks != 0 {
				t.Fatal("sync counts ignored profile selection", selectedCounts, err)
			}
			var nativeProfile domain.Profile
			if err := json.Unmarshal([]byte(run("profile", "show", "portable-full", "--json")), &nativeProfile); err != nil || len(nativeProfile.Packs) != 1 || nativeProfile.Packs[0].NativePlugin != nil || len(nativeProfile.Packs[0].Skills) != 0 {
				t.Fatal("fully deselected import remained active", err)
			}
			run(syncArgs...)
			if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
				t.Fatal("profile exclusion retained skill", err)
			}
			if err := json.Unmarshal([]byte(run("trace", "skill", "probe", "--pack", "probe", "--profile-path", filepath.Join(cfg, "profiles/portable-full.yaml"), "--scope", "global", "--harness", "opencode", "--json")), &trace); err != nil || trace.ProfileState != app.TraceProfileStateContentExcluded || len(trace.Destinations) != 0 || trace.Source.NativeBinding != "probe@fixture" {
				t.Fatalf("target exclusion trace: %+v %v", trace, err)
			}
			if len(trace.Remediation) == 0 || !strings.HasPrefix(trace.Remediation[0], "aipack profile include ") || strings.Contains(trace.Remediation[0], "--harness") {
				t.Fatal("trace remediation did not use shared profile", trace.Remediation)
			}
			if err := json.Unmarshal([]byte(run("trace", "probe@fixture:probe", "--pack", "probe", "--profile-path", filepath.Join(cfg, "profiles/portable-full.yaml"), "--scope", "global", "--harness", "opencode", "--json")), &trace); err != nil || trace.ProfileState != app.TraceProfileStateContentExcluded {
				t.Fatalf("inactive target smart trace: %+v %v", trace, err)
			}
			run("profile", "include", "probe", "--kind", "skill", "--profile", "portable-full")
			run(syncArgs...)
			if skillBody, err := os.ReadFile(skillPath); err != nil || !strings.Contains(string(skillBody), "UPDATED_SAME_VERSION") {
				t.Fatal("target re-enable lost updated content", err)
			}
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			run("pack", "rename", "probe", "renamed")
			renamedBody, err := os.ReadFile(skillPath)
			if err != nil || strings.Contains(string(renamedBody), filepath.Join(cfg, "packs/probe/upstream")) || !strings.Contains(string(renamedBody), filepath.Join(cfg, "packs/renamed/upstream")) {
				t.Fatalf("rename with an unset config root left stale skill references: %s %v", renamedBody, err)
			}
			t.Setenv("OPENCODE_CONFIG_DIR", target)
			run("pack", "delete", "renamed", "--yes")
			if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
				t.Fatalf("CLI removal retained ordinary skill: %v", err)
			}
		})
	}
}

func TestPluginPackCommandObjects(t *testing.T) {
	root, configDir := t.TempDir(), t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude-plugin/marketplace.json", `{"name":"fixture","owner":{"name":"shrug-labs"},"plugins":[{"name":"probe","source":"./plugins/probe","commands":{"z-catalog-alias":{"source":"./custom/catalog.md"},"a-catalog-later":{"source":"./custom/catalog.md"},"manifest":{"source":"./custom/override.md"},"fallback":{"source":"./custom/shared.md"},"ignored":{"content":"IGNORED_CATALOG_BODY"}}}]}`)
	write("plugins/probe/.claude-plugin/plugin.json", `{"name":"probe","commands":{"manifest":{"content":"SHADOWED_INLINE_BODY"},"z-alias":{"source":"./custom/shared.md"},"a-later":{"source":"./custom/shared.md"}}}`)
	write("plugins/probe/custom/shared.md", "---\ndescription: Aliased command.\n---\nSHARED_COMMAND_BODY\n")
	write("plugins/probe/custom/override.md", "---\ndescription: Primary file command.\n---\nPRIMARY_COMMAND_BODY\n")
	write("plugins/probe/custom/catalog.md", "---\ndescription: Catalog alias.\n---\nCATALOG_ALIAS_BODY\n")
	run := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := runApp(t, append(args, "--config-dir", configDir)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, stdout, stderr)
		}
		return stdout
	}
	run("registry", "fetch", filepath.Join(root, ".claude-plugin/marketplace.json"))
	run("pack", "inspect", "probe", "--json")
	run("pack", "install", "probe", "--name", "alias")
	check := func(marker string) {
		t.Helper()
		var shown app.PackShowEntry
		if err := json.Unmarshal([]byte(run("pack", "show", "alias", "--json")), &shown); err != nil || !slices.Equal(shown.Workflows, []string{"manifest", "z-alias", "z-catalog-alias"}) || shown.NativePlugin == nil || shown.NativePlugin.ConverterVersion != plugin.ConverterVersion || len(shown.NativePlugin.CatalogCommandOrder) == 0 || shown.NativePlugin.CatalogCommandOrder[0] != "z-catalog-alias" || shown.NativePlugin.Components[domain.CategoryWorkflows]["manifest"][0] != "custom/override.md" {
			t.Fatalf("command inventory or priority differs: %+v %v", shown, err)
		}
		var results []struct{ Name, Path string }
		if err := json.Unmarshal([]byte(run("search", marker, "--kind", "workflow", "--pack", "alias", "--json")), &results); err != nil || len(results) != 1 || results[0].Name != "manifest" || results[0].Path != filepath.Join(configDir, "packs/alias/upstream/custom/override.md") {
			t.Fatalf("primary command was not indexed: %+v %v", results, err)
		}
		if err := json.Unmarshal([]byte(run("search", "SHADOWED_INLINE_BODY", "--kind", "workflow", "--pack", "alias", "--json")), &results); err != nil || len(results) != 0 {
			t.Fatal("search indexed a shadowed inline command")
		}
		if err := json.Unmarshal([]byte(run("search", "CATALOG_ALIAS_BODY", "--kind", "workflow", "--pack", "alias", "--json")), &results); err != nil || len(results) != 1 || results[0].Name != "z-catalog-alias" {
			t.Fatalf("registry reload changed the catalog alias: %+v %v", results, err)
		}
	}
	check("PRIMARY_COMMAND_BODY")
	lockPath := config.LockfilePath(configDir)
	lf, err := config.LoadLockfile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["alias"]
	meta.ConverterVersion = plugin.ConverterVersion - 1
	lf.Packs["alias"] = meta
	if err := config.SaveLockfile(lockPath, lf); err != nil {
		t.Fatal(err)
	}
	run("pack", "update", "alias")
	check("PRIMARY_COMMAND_BODY")
	write("plugins/probe/custom/override.md", "---\ndescription: Updated primary command.\n---\nUPDATED_PRIMARY_BODY\n")
	run("pack", "update", "alias", "--dry-run")
	check("PRIMARY_COMMAND_BODY")
	run("pack", "update", "alias")
	check("UPDATED_PRIMARY_BODY")
	run("pack", "delete", "alias", "--yes")
	var remaining []any
	if err := json.Unmarshal([]byte(run("search", "UPDATED_PRIMARY_BODY", "--kind", "workflow", "--pack", "alias", "--json")), &remaining); err != nil || len(remaining) != 0 {
		t.Fatal("delete retained a command index entry")
	}
}

func TestPluginPackNamedCLILifecycle(t *testing.T) {
	root, configDir := t.TempDir(), t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".agents/plugins/marketplace.json", `{"name":"fixture","plugins":[{"name":"probe","source":"./plugins/probe"}]}`)
	write("plugins/probe/.codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`)
	write("plugins/probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: A native test skill.\n---\nFixture marker.\n")
	run := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := runApp(t, append(args, "--config-dir", configDir)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, stdout, stderr)
		}
		return stdout
	}
	run("registry", "fetch", filepath.Join(root, ".agents/plugins/marketplace.json"))
	var preview map[string]any
	if err := json.Unmarshal([]byte(run("pack", "inspect", "probe", "--json")), &preview); err != nil {
		t.Fatal(err)
	}
	if preview["native_plugin"] == nil || preview["counts"].(map[string]any)["skills"] != float64(1) {
		t.Fatalf("missing native inspect inventory: %+v", preview)
	}
	run("pack", "install", "probe", "--name", "alias")
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil || lf.Packs["alias"].Plugin == nil || lf.Packs["alias"].Method != config.MethodCopy {
		t.Fatalf("named install lost native source: %+v %v", lf, err)
	}
	var shown app.PackShowEntry
	if err := json.Unmarshal([]byte(run("pack", "show", "alias", "--json")), &shown); err != nil || shown.PluginSource == nil || shown.PluginSource.MarketplaceURL != lf.Packs["alias"].Plugin.MarketplaceURL || shown.PluginSource.MarketplacePath != lf.Packs["alias"].Plugin.MarketplacePath || shown.SubPath != lf.Packs["alias"].SubPath || shown.MaterializedDigest == "" || shown.MaterializedDigest != lf.Packs["alias"].MaterializedDigest {
		t.Fatalf("show lost source provenance: %+v %v", shown, err)
	}
	if text := run("pack", "show", "alias"); !strings.Contains(text, "Converter:") || !strings.Contains(text, "Catalog:") {
		t.Fatalf("show hides transformation or catalog: %s", text)
	}
	if got := run("search", "probe", "--json"); !strings.Contains(got, "Fixture marker") && !strings.Contains(got, "A native test skill") {
		t.Fatalf("native skill not discoverable: %s", got)
	}
	write("plugins/probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Updated native skill.\n---\nUpdated fixture.\n")
	run("pack", "update", "alias", "--dry-run")
	run("pack", "update", "alias")
	body, err := os.ReadFile(filepath.Join(configDir, "packs/alias/upstream/skills/probe/SKILL.md"))
	if err != nil || !strings.Contains(string(body), "Updated fixture") {
		t.Fatalf("CLI update did not reconvert: %s %v", body, err)
	}
	installed := filepath.Join(configDir, "packs/alias/upstream/skills/probe/SKILL.md")
	if err := os.WriteFile(installed, []byte("local customization"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"pack", "update", "alias", "--dry-run"}, {"pack", "update", "alias"}, {"pack", "install", "probe", "--name", "alias"}} {
		stdout, stderr, code := runApp(t, append(args, "--config-dir", configDir)...)
		if code == 0 || !strings.Contains(stdout+stderr, "local changes") {
			t.Fatalf("CLI replacement accepted local customization: %v exit=%d %s %s", args, code, stdout, stderr)
		}
		current, err := os.ReadFile(installed)
		if err != nil || string(current) != "local customization" {
			t.Fatalf("CLI refusal lost local customization: %s %v", current, err)
		}
	}
	if err := os.WriteFile(installed, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var report app.PackUpdateReport
	if err := json.Unmarshal([]byte(run("pack", "update", "alias", "--dry-run", "--json")), &report); err != nil || len(report.Results) != 1 || report.Results[0].OriginMigration != nil {
		t.Fatalf("restored local import reported false source migration: %+v %v", report, err)
	}
	run("pack", "delete", "alias", "--yes")
	if _, err := os.Stat(filepath.Join(configDir, "packs/alias")); !os.IsNotExist(err) {
		t.Fatal("CLI deletion retained source")
	}
}

func TestPluginPackNativeAgentSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "native"))
	root, configDir := t.TempDir(), t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude-plugin/marketplace.json", `{"name":"candidate-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"candidate-probe","source":"./plugins/probe","agents":["./first/reviewer.md","./second/reviewer.md","./first/reviewer.md"]}]}`)
	for _, dir := range []string{"first", "second"} {
		write("plugins/probe/"+dir+"/reviewer.md", "---\nname: Reviewer\ndescription: "+dir+" native agent.\n---\n"+strings.ToUpper(dir)+"_AGENT\n")
	}
	run := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := runApp(t, append(args, "--config-dir", configDir)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, stdout, stderr)
		}
		return stdout
	}
	run("registry", "fetch", filepath.Join(root, ".claude-plugin/marketplace.json"))
	run("pack", "inspect", "candidate-probe")
	run("pack", "install", "candidate-probe", "--name", "alias")
	if err := os.MkdirAll(filepath.Join(configDir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles/default.yaml"), []byte("schema_version: 1\npacks:\n  - name: alias\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var traced app.TraceResult
	if err := json.Unmarshal([]byte(run("trace", "Reviewer", "--harness", "claudecode", "--json")), &traced); err != nil || !traced.Found || traced.Source == nil || len(traced.Source.SourcePaths) != 2 || len(traced.Destinations) != 3 || len(traced.Blockers) != 0 {
		t.Fatalf("native smart trace: %+v %v", traced, err)
	}
	traceText := run("trace", "agent", "Reviewer", "--harness", "claudecode")
	for _, path := range traced.Source.SourcePaths {
		if !strings.Contains(traceText, path) || !strings.Contains(traceText, "candidate-probe@candidate-market") || !strings.Contains(traceText, "(package)") {
			t.Fatalf("human trace hides native provenance: %s", traceText)
		}
	}
	if err := json.Unmarshal([]byte(run("trace", "plugin", "candidate-probe", "--harness", "claudecode", "--json")), &traced); err != nil || !traced.Found || len(traced.Destinations) != 2 || !traced.Destinations[0].Embedded {
		t.Fatalf("whole native plugin trace: %+v %v", traced, err)
	}
	write(".claude-plugin/marketplace.json", `{"name":"candidate-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"candidate-probe","source":"./plugins/probe","agents":["./first/reviewer.md","./second/reviewer.md","./first/reviewer.md"]},{"name":"other-probe","source":"./plugins/other","agents":["./reviewer.md"]}]}`)
	write("plugins/other/reviewer.md", "---\nname: Reviewer\ndescription: Another native agent.\n---\nOTHER_AGENT\n")
	run("registry", "fetch", filepath.Join(root, ".claude-plugin/marketplace.json"))
	run("pack", "install", "other-probe", "--name", "other")
	if err := os.WriteFile(filepath.Join(configDir, "profiles/default.yaml"), []byte("schema_version: 1\npacks:\n  - name: alias\n  - name: other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"trace", "Reviewer"}, {"trace", "agent", "Reviewer"}} {
		_, stderr, code := runApp(t, append(args, "--harness", "claudecode", "--config-dir", configDir)...)
		if code != 1 || !strings.Contains(stderr, "--pack alias") || !strings.Contains(stderr, "--pack other") {
			t.Fatalf("native trace guessed between scoped names: %d %s", code, stderr)
		}
	}
	if err := json.Unmarshal([]byte(run("trace", "Reviewer", "--pack", "other", "--harness", "claudecode", "--json")), &traced); err != nil || traced.Source == nil || traced.Source.Pack != "other" || traced.Source.NativeBinding != "other-probe@candidate-market" || len(traced.Source.SourcePaths) != 1 {
		t.Fatalf("native trace pack filter: %+v %v", traced, err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "profiles/default.yaml"), []byte("schema_version: 1\npacks:\n  - name: other\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []struct{ kind, name, pack, state string }{{"agent", "Reviewer", "alias", "installed_not_in_profile"}, {"plugin", "candidate-probe", "alias", "installed_not_in_profile"}, {"agent", "Reviewer", "other", "pack_disabled"}, {"plugin", "other-probe", "other", "pack_disabled"}} {
		if err := json.Unmarshal([]byte(run("trace", resource.kind, resource.name, "--pack", resource.pack, "--harness", "claudecode", "--json")), &traced); err != nil || string(traced.ProfileState) != resource.state || traced.Source == nil || traced.Source.Pack != resource.pack || traced.Source.NativeBinding == "" || len(traced.Source.SourcePaths) == 0 || len(traced.Destinations) != 0 {
			t.Fatalf("inactive native %s trace: %+v %v", resource.kind, traced, err)
		}
	}
	var shown struct {
		Agents []string             `json:"agents"`
		Native *domain.NativePlugin `json:"native_plugin"`
	}
	if err := json.Unmarshal([]byte(run("pack", "show", "alias", "--json")), &shown); err != nil || len(shown.Agents) != 1 || shown.Native == nil || len(shown.Native.Components[domain.CategoryAgents]["Reviewer"]) != 2 {
		t.Fatalf("show lost native provenance or duplicated a selector: %+v %v", shown, err)
	}
	showText, searchText := run("pack", "show", "alias"), run("search", "--kind", "agent", "--pack", "alias")
	if !strings.Contains(showText, "candidate-probe@candidate-market") || !strings.Contains(showText, "Sources (Reviewer)") || !strings.Contains(searchText, "alias (1 agent)") {
		t.Fatalf("human output lost native sources or selector count: %s\n%s", showText, searchText)
	}
	for _, dir := range []string{"first", "second"} {
		path := filepath.Join(configDir, "packs/alias/upstream", dir, "reviewer.md")
		if !strings.Contains(showText, path) || !strings.Contains(searchText, path) {
			t.Fatal("human output hides an agent source")
		}
		var results []struct{ Name, Path string }
		if err := json.Unmarshal([]byte(run("search", strings.ToUpper(dir)+"_AGENT", "--kind", "agent", "--pack", "alias", "--json")), &results); err != nil || len(results) != 1 || results[0].Name != "Reviewer" || results[0].Path != path {
			t.Fatalf("candidate body/path absent from installed search: %+v %v", results, err)
		}
	}
	write("plugins/probe/second/reviewer.md", "---\nname: Reviewer\ndescription: Updated native agent.\n---\nUPDATED_AGENT\n")
	run("pack", "update", "alias", "--dry-run")
	var previewRows []any
	if err := json.Unmarshal([]byte(run("search", "UPDATED_AGENT", "--kind", "agent", "--pack", "alias", "--json")), &previewRows); err != nil || len(previewRows) != 0 {
		t.Fatal("update dry-run indexed uninstalled candidate content")
	}
	run("pack", "update", "alias")
	if got := run("search", "UPDATED_AGENT", "--kind", "agent", "--pack", "alias", "--json"); !strings.Contains(got, "second/reviewer.md") {
		t.Fatal("update did not refresh the alternate agent source")
	}
	if err := os.Remove(filepath.Join(configDir, "index.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(configDir, "index.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := run("pack", "update", "alias"); !strings.Contains(got, "Warning: search index for alias was not refreshed") {
		t.Fatal("index failure was silent")
	}
	if err := os.Remove(filepath.Join(configDir, "index.db")); err != nil {
		t.Fatal(err)
	}
	run("pack", "update", "alias")
	if got := run("search", "UPDATED_AGENT", "--kind", "agent", "--pack", "alias", "--json"); !strings.Contains(got, "second/reviewer.md") {
		t.Fatal("unchanged update did not repair the index")
	}
	run("pack", "delete", "alias", "--yes")
	var remaining []any
	if err := json.Unmarshal([]byte(run("search", "--kind", "agent", "--pack", "alias", "--json")), &remaining); err != nil || len(remaining) != 0 {
		t.Fatalf("delete retained candidate rows: %v %v", remaining, err)
	}
}

func TestPluginPackTraceSkillNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			root, cfg := t.TempDir(), t.TempDir()
			write := func(rel, body string) {
				t.Helper()
				path := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			manifest, body := ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`
			if format == plugin.AgentPlugins {
				manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
			}
			write("plugins/probe/"+manifest, body)
			write("plugins/probe/skills/selected/SKILL.md", "---\nname: selected\ndescription: Owned trace fixture\n---\nOwned skill.\n")
			write(".agents/plugins/marketplace.json", `{"name":"fixture","plugins":[{"name":"probe","source":"./plugins/probe"}]}`)
			run := func(args ...string) string {
				t.Helper()
				out, stderr, code := runApp(t, append(args, "--config-dir", cfg)...)
				if code != 0 {
					t.Fatalf("%v: exit %d: %s %s", args, code, out, stderr)
				}
				return out
			}
			run("registry", "fetch", filepath.Join(root, ".agents/plugins/marketplace.json"))
			run("pack", "install", "probe", "--name", "alias")
			writeRulePackFixture(t, cfg, "ordinary", "ambient", "1.0.0")
			for _, phase := range []struct{ fields, state string }{
				{"  - name: alias\n", "active"},
				{"  - name: alias\n    skills:\n      exclude: [selected]\n", "content_excluded"},
				{"  - name: alias\n    enabled: false\n", "pack_disabled"},
				{"  - name: ordinary\n", "installed_not_in_profile"},
			} {
				if err := os.WriteFile(filepath.Join(cfg, "profiles/default.yaml"), []byte("schema_version: 1\npacks:\n"+phase.fields), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"alias:selected", "probe@wrong-market:selected"} {
					_, _, code := runApp(t, "trace", "skill", name, "--config-dir", cfg, "--harness", "opencode", "--json")
					if code == 0 {
						t.Fatalf("trace accepted unrelated native identity %s", name)
					}
				}
				for _, name := range []string{"selected", "probe:selected", "probe@fixture:selected"} {
					for _, typed := range []bool{false, true} {
						args := []string{"trace"}
						if typed {
							args = append(args, "skill")
						}
						args = append(args, name, "--pack", "alias", "--harness", "opencode", "--json")
						var trace app.TraceResult
						if err := json.Unmarshal([]byte(run(args...)), &trace); err != nil || trace.ResourceName != "selected" || trace.Source == nil || trace.Source.Pack != "alias" || trace.Source.NativeBinding != "probe@fixture" || string(trace.ProfileState) != phase.state || !trace.Found {
							t.Fatalf("%s %s: %+v %v", phase.state, name, trace, err)
						}
					}
				}
			}
			ordinarySkill := filepath.Join(cfg, "packs/ordinary/skills/selected/SKILL.md")
			if err := os.MkdirAll(filepath.Dir(ordinarySkill), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ordinarySkill, []byte("---\nname: selected\ndescription: Ordinary trace fixture\n---\nOrdinary body.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, winner := range []string{"ordinary", "alias"} {
				fields := "schema_version: 1\npacks:\n"
				for _, name := range []string{"ordinary", "alias"} {
					fields += "  - name: " + name + "\n"
					if name == winner {
						fields += "    overrides:\n      skills: [selected]\n"
					}
				}
				if err := os.WriteFile(filepath.Join(cfg, "profiles/default.yaml"), []byte(fields), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, typed := range []bool{false, true} {
					args := []string{"trace"}
					if typed {
						args = append(args, "skill")
					}
					args = append(args, "selected", "--harness", "opencode", "--json")
					var trace app.TraceResult
					if err := json.Unmarshal([]byte(run(args...)), &trace); err != nil || trace.Source == nil || trace.Source.Pack != winner || len(trace.Destinations) != 1 || len(trace.Blockers) != 0 {
						t.Fatalf("CLI trace did not resolve profile winner %s: %+v, %v", winner, trace, err)
					}
				}
				for _, name := range []string{"probe@fixture:selected", "selected" + domain.RenderedIdentitySeparator + "alias"} {
					var trace app.TraceResult
					if err := json.Unmarshal([]byte(run("trace", "skill", name, "--harness", "opencode", "--json")), &trace); err != nil || trace.Source == nil || trace.Source.Pack != "alias" || trace.Source.NativeBinding != "probe@fixture" {
						t.Fatalf("CLI alias trace lost original provenance: %+v, %v", trace, err)
					}
					if winner == "ordinary" && (len(trace.Destinations) != 0 || len(trace.Blockers) != 1 || !strings.Contains(trace.Blockers[0], "suppressed")) {
						t.Fatalf("CLI alias trace reported overridden content as delivered: %+v", trace)
					}
				}
			}
		})
	}
}
