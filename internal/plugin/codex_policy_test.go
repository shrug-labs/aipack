package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/shrug-labs/aipack/internal/config"
)

func managedCodexRequirements(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getenv("AIPACK_TEST_CODEX_MANAGED") != "1" {
		t.Skip("requires an explicitly isolated native managed-policy runner")
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	owned := false
	for line := range strings.SplitSeq(string(mounts), "\n") {
		if strings.Contains(line, " /etc/codex ") && strings.Contains(line, " - tmpfs ") {
			owned = true
			break
		}
	}
	if !owned {
		t.Fatal("managed-policy fixture requires a dedicated tmpfs at /etc/codex")
	}
	write(t, "/etc/codex", "requirements.toml", body, 0o600)
	t.Cleanup(func() {
		if err := os.Remove("/etc/codex/requirements.toml"); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

func TestCodexManagedMarketplaceAdmission(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" || os.Getenv("AIPACK_TEST_BINARY") == "" {
		t.Skip("requires the isolated native managed-policy runner and current CLI")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			managedCodexRequirements(t, "[marketplaces]\nrestrict_to_allowed_sources = true\n")
			market, home, nativeHome, cfg := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			write(t, home, ".codex/config.toml", "", 0o600)
			write(t, nativeHome, ".codex/config.toml", "", 0o600)
			manifest, manifestBody := ".codex-plugin/plugin.json", `{"name":"admission-probe","version":"1.0.0"}`
			if format == AgentPlugins {
				manifest, manifestBody = "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"admission-probe","version":"1.0.0"}`
			}
			write(t, market, "probe/"+manifest, manifestBody, 0o644)
			write(t, market, "probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Managed admission fixture.\n---\nADMISSION_BODY\n", 0o644)
			write(t, market, ".agents/plugins/marketplace.json", `{"name":"admission-market","plugins":[{"name":"admission-probe","source":"./probe"}]}`, 0o644)
			write(t, cfg, "sync-config.yaml", "schema_version: 1\ndefaults:\n  profile: default\n  auto_sync: false\n", 0o600)
			write(t, cfg, "profiles/default.yaml", "schema_version: 1\npacks: []\n", 0o600)
			run := func(targetHome, binary string, args ...string) ([]byte, error) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.WaitDelay = market, time.Second
				cmd.Env = slices.DeleteFunc(os.Environ(), func(value string) bool {
					return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
				})
				cmd.Env = append(cmd.Env, "HOME="+targetHome, "CODEX_HOME="+filepath.Join(targetHome, ".codex"), "AIPACK_NO_UPDATE_CHECK=1")
				return cmd.CombinedOutput()
			}
			cli := func(args ...string) ([]byte, error) {
				return run(home, os.Getenv("AIPACK_TEST_BINARY"), append(args, "--config-dir", cfg)...)
			}
			must := func(out []byte, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("command failed: %v %s", err, out)
				}
			}
			view := filepath.Join(cfg, "rendered-plugins/codex/admission-market")
			allow := func(paths ...string) {
				body := "[marketplaces]\nrestrict_to_allowed_sources = true\n"
				for i, path := range paths {
					body += fmt.Sprintf("[marketplaces.allowed_sources.local%d]\nsource = 'local'\npath = %q\n", i, path)
				}
				write(t, "/etc/codex", "requirements.toml", body, 0o600)
			}
			allow(market)
			must(run(nativeHome, "codex", "plugin", "marketplace", "add", market, "--json"))
			must(run(nativeHome, "codex", "plugin", "add", "admission-probe@admission-market", "--json"))
			must(cli("registry", "fetch", filepath.Join(market, ".agents/plugins/marketplace.json"), "--format", format))
			must(cli("pack", "install", "admission-probe", "--name", "alias", "--add"))
			paths := []string{config.LockfilePath(cfg), filepath.Join(cfg, "profiles/default.yaml"), filepath.Join(cfg, "ledger/codex.json"), filepath.Join(home, ".codex/config.toml"), filepath.Join(view, ".agents/plugins/marketplace.json"), filepath.Join(home, ".codex/plugins/cache/admission-market/admission-probe/1.0.0/skills/probe/SKILL.md")}
			snapshot := func() map[string]string {
				t.Helper()
				result := map[string]string{}
				for _, path := range paths {
					body, err := os.ReadFile(path)
					if os.IsNotExist(err) {
						result[path] = "absent"
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					result[path] = string(body)
				}
				return result
			}
			before := snapshot()
			out, err := cli("sync", "--harness", "codex", "--dry-run", "--verbose")
			if err != nil || strings.Contains(string(out), "marketplaces.allowed_sources") || !reflect.DeepEqual(before, snapshot()) {
				t.Fatalf("cold preview required approval before native refusal or mutated delivery: %v %s", err, out)
			}
			out, err = cli("sync", "--harness", "codex", "--yes")
			after := snapshot()
			for path, want := range before {
				if after[path] != want {
					t.Errorf("refusal changed %s: before=%q after=%q", path, want, after[path])
				}
			}
			if err == nil || !strings.Contains(string(out), "not allowed by requirements") || !strings.Contains(string(out), `source = "local"`) || !strings.Contains(string(out), view) || !reflect.DeepEqual(before, after) {
				t.Fatalf("original source rule authorized generated local delivery or refusal changed state: %v %s", err, out)
			}
			for _, rule := range []string{
				"[marketplaces]\nrestrict_to_allowed_sources = true\n[marketplaces.allowed_sources.original]\nsource = 'git'\nurl = 'https://github.com/example/plugins.git'\n",
				fmt.Sprintf("[marketplaces]\nrestrict_to_allowed_sources = true\n[marketplaces.allowed_sources.parent]\nsource = 'local'\npath = %q\n", filepath.Dir(view)),
			} {
				write(t, "/etc/codex", "requirements.toml", rule, 0o600)
				out, err = cli("sync", "--harness", "codex", "--yes")
				if err == nil || !strings.Contains(string(out), "not allowed by requirements") || !reflect.DeepEqual(before, snapshot()) {
					t.Fatalf("Git/parent allow rule admitted the generated local view or changed state: %v %s", err, out)
				}
			}
			allow(market, view)
			must(cli("sync", "--harness", "codex", "--yes"))
			must(cli("sync", "--harness", "codex", "--yes"))
			// Removing a registration must not turn its retained cache into a
			// successful no-op. Sync restores an approved source automatically.
			configPath := filepath.Join(home, ".codex/config.toml")
			var nativeConfig map[string]any
			configBody, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := toml.Unmarshal(configBody, &nativeConfig); err != nil {
				t.Fatal(err)
			}
			delete(nativeConfig["marketplaces"].(map[string]any), "admission-market")
			configBody, err = toml.Marshal(nativeConfig)
			if err != nil {
				t.Fatal(err)
			}
			write(t, home, ".codex/config.toml", string(configBody), 0o600)
			unregistered := snapshot()
			out, err = cli("sync", "--harness", "codex", "--dry-run", "--verbose")
			if err != nil || !strings.Contains(string(out), "1 plugins") || strings.Contains(string(out), "marketplaces.allowed_sources") || !reflect.DeepEqual(unregistered, snapshot()) {
				t.Fatalf("unregistered source preview added friction or omitted restoration: %v %s", err, out)
			}
			must(cli("sync", "--harness", "codex", "--yes"))
			configBody, err = os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := toml.Unmarshal(configBody, &nativeConfig); err != nil {
				t.Fatal(err)
			}
			registration, _ := nativeConfig["marketplaces"].(map[string]any)["admission-market"].(map[string]any)
			if registration["source_type"] != "local" || registration["source"] != view {
				t.Fatalf("sync did not restore the approved native source: %s", configBody)
			}
			installed := snapshot()
			allow()
			for _, args := range [][]string{{"--dry-run", "--verbose"}, {"--yes"}} {
				out, err = cli(append([]string{"sync", "--harness", "codex"}, args...)...)
				if err == nil || !strings.Contains(string(out), "does not admit local marketplace") || strings.Contains(string(out), "marketplaces.allowed_sources") || !strings.Contains(string(out), view) || !reflect.DeepEqual(installed, snapshot()) {
					t.Fatalf("unchanged sync ignored tightened managed policy or changed delivery: %v %s", err, out)
				}
			}
			allow(view)
			must(cli("sync", "--harness", "codex", "--yes"))
		})
	}
}

func TestCodexMarketplaceOwnership(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" || os.Getenv("AIPACK_TEST_BINARY") == "" {
		t.Skip("requires the isolated native runner and current CLI")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			market, home, cfg := t.TempDir(), t.TempDir(), t.TempDir()
			manifest, body := ".codex-plugin/plugin.json", `{"name":"ownership-probe","version":"1.0.0"}`
			if format == AgentPlugins {
				manifest, body = "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"ownership-probe","version":"1.0.0"}`
			}
			write(t, market, "probe/"+manifest, body, 0o644)
			write(t, market, "probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Ownership fixture.\n---\nOWNERSHIP_BODY\n", 0o644)
			write(t, market, ".agents/plugins/marketplace.json", `{"name":"ownership-market","plugins":[{"name":"ownership-probe","source":"./probe"}]}`, 0o644)
			write(t, home, ".codex/config.toml", "", 0o600)
			write(t, cfg, "sync-config.yaml", "schema_version: 1\ndefaults:\n  profile: default\n  auto_sync: false\n", 0o600)
			write(t, cfg, "profiles/default.yaml", "schema_version: 1\npacks: []\n", 0o600)
			run := func(binary string, args ...string) ([]byte, error) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.WaitDelay = market, time.Second
				cmd.Env = slices.DeleteFunc(os.Environ(), func(value string) bool {
					return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
				})
				cmd.Env = append(cmd.Env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"), "AIPACK_NO_UPDATE_CHECK=1")
				return cmd.CombinedOutput()
			}
			cli := func(args ...string) ([]byte, error) {
				return run(os.Getenv("AIPACK_TEST_BINARY"), append(args, "--config-dir", cfg)...)
			}
			must := func(out []byte, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("command failed: %v %s", err, out)
				}
			}
			must(cli("registry", "fetch", filepath.Join(market, ".agents/plugins/marketplace.json"), "--format", format))
			must(cli("pack", "install", "ownership-probe", "--name", "alias", "--add"))
			must(run("codex", "plugin", "marketplace", "add", market, "--json"))
			refuse := func(message string) {
				t.Helper()
				beforeHome, err := ReadFiles(home)
				if err != nil {
					t.Fatal(err)
				}
				beforeCfg, err := ReadFiles(cfg)
				if err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{{"--dry-run", "--verbose"}, {"--yes"}} {
					out, err := cli(append([]string{"sync", "--harness", "codex"}, args...)...)
					if err == nil || !strings.Contains(string(out), message) {
						t.Fatalf("native ownership was taken over: %v %s", err, out)
					}
					afterHome, homeErr := ReadFiles(home)
					afterCfg, cfgErr := ReadFiles(cfg)
					if homeErr != nil || cfgErr != nil || !reflect.DeepEqual(beforeHome, afterHome) || !reflect.DeepEqual(beforeCfg, afterCfg) {
						t.Fatalf("ownership refusal changed installation: home=%v config=%v", homeErr, cfgErr)
					}
				}
			}
			refuse("registered outside this AIPack delivery")
			must(run("codex", "plugin", "add", "ownership-probe@ownership-market", "--json"))
			refuse("already installed outside AIPack")
			must(run("codex", "plugin", "remove", "ownership-probe@ownership-market", "--json"))
			must(run("codex", "plugin", "marketplace", "remove", "ownership-market", "--json"))
			must(cli("sync", "--harness", "codex", "--yes"))
			must(cli("sync", "--harness", "codex", "--yes"))
			must(run("codex", "plugin", "marketplace", "remove", "ownership-market", "--json"))
			must(run("codex", "plugin", "marketplace", "add", market, "--json"))
			path := filepath.Join(home, ".codex/config.toml")
			registration := func() any {
				t.Helper()
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var root map[string]any
				if err := toml.Unmarshal(body, &root); err != nil {
					t.Fatal(err)
				}
				return root["marketplaces"]
			}
			before := registration()
			out, err := cli("clean", "--harness", "codex", "--yes")
			if err == nil || !strings.Contains(string(out), "changed after delivery; retain its registration") {
				t.Fatalf("cleanup did not protect the retargeted marketplace: %v %s", err, out)
			}
			if after := registration(); before == nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("cleanup changed the retargeted registration: before=%v after=%v", before, after)
			}
		})
	}
}

func TestCodexNativeMarketplacePolicy(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native marketplace policy checks")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		for _, test := range []struct {
			name, policy string
			allowed      bool
		}{
			{"omitted", "", true},
			{"default", `{}`, true},
			{"available", `{"installation":"AVAILABLE"}`, true},
			{"installed-default", `{"installation":"INSTALLED_BY_DEFAULT"}`, true},
			{"unavailable", `{"installation":"NOT_AVAILABLE"}`, false},
			{"on-install", `{"authentication":"ON_INSTALL"}`, true},
			{"on-use", `{"authentication":"ON_USE"}`, true},
			{"products-null", `{"products":null}`, true},
			{"products-empty", `{"products":[]}`, false},
			{"products-codex", `{"products":["codex"]}`, true},
			{"products-uppercase", `{"products":["CODEX","CHATGPT","ATLAS"]}`, true},
			{"products-other", `{"products":["chatgpt","atlas"]}`, false},
			{"unknown-fields", `{"future":{"untouched":true}}`, true},
			{"sequence", `["AVAILABLE","ON_USE",["codex"]]`, true},
			{"empty-sequence", `[]`, false},
			{"short-sequence", `["AVAILABLE","ON_USE"]`, false},
			{"policy-null", `null`, false},
			{"policy-string", `"AVAILABLE"`, false},
			{"installation-null", `{"installation":null}`, false},
			{"installation-invalid", `{"installation":"available"}`, false},
			{"authentication-null", `{"authentication":null}`, false},
			{"authentication-invalid", `{"authentication":"on_use"}`, false},
			{"products-string", `{"products":"codex"}`, false},
			{"products-null-item", `{"products":[null]}`, false},
			{"products-mixed-case", `{"products":["Codex"]}`, false},
			{"products-unknown", `{"products":["future"]}`, false},
			{"sequence-extra", `["AVAILABLE","ON_USE",null,true]`, false},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				market, home := t.TempDir(), t.TempDir()
				write(t, home, "codex/config.toml", "", 0o600)
				manifest := ".codex-plugin/plugin.json"
				if format == AgentPlugins {
					manifest = "plugin.json"
				}
				manifestBody := `{"name":"policy-probe","version":"1.0.0"}`
				if format == AgentPlugins {
					manifestBody = `{"$schema":"` + agentPluginSchema + `","name":"policy-probe","version":"1.0.0"}`
				}
				write(t, market, "probe/"+manifest, manifestBody, 0o644)
				write(t, market, "probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Policy fixture.\n---\nPOLICY_BODY\n", 0o644)
				body := `{"name":"policy-market","plugins":[{"name":"policy-probe","source":"./probe"`
				if test.policy != "" {
					body += `,"policy":` + test.policy
				}
				body += `}]}`
				write(t, market, ".agents/plugins/marketplace.json", body, 0o644)
				env := slices.DeleteFunc(os.Environ(), func(v string) bool {
					return strings.HasPrefix(v, "HOME=") || strings.HasPrefix(v, "CODEX_HOME=")
				})
				env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, "codex"), "AIPACK_NO_UPDATE_CHECK=1")
				run := func(binary string, args ...string) ([]byte, error) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, args...)
					cmd.Env, cmd.Dir, cmd.WaitDelay = env, market, time.Second
					return cmd.CombinedOutput()
				}
				out, nativeErr := run("codex", "plugin", "marketplace", "add", market, "--json")
				if nativeErr == nil {
					out, nativeErr = run("codex", "plugin", "add", "policy-probe@policy-market", "--json")
				}
				if (nativeErr == nil) != test.allowed {
					t.Fatalf("native policy expectation: allowed=%v err=%v output=%s", test.allowed, nativeErr, out)
				}
				reg, err := config.ParseMarketplace([]byte(body), config.RegistrySourceEntry{URL: market, Format: format})
				if err != nil {
					t.Fatal(err)
				}
				entry := reg.Packs["policy-probe"]
				if (entry.Unsupported == "") != test.allowed {
					t.Fatalf("AIPack policy disagrees with native: allowed=%v limitation=%q", test.allowed, entry.Unsupported)
				}
				if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
					cfg := t.TempDir()
					write(t, cfg, "sync-config.yaml", "schema_version: 1\ndefaults:\n  profile: default\n  auto_sync: false\n", 0o600)
					out, err := run(binary, "registry", "fetch", filepath.Join(market, ".agents/plugins/marketplace.json"), "--format", format, "--config-dir", cfg)
					if err != nil {
						t.Fatalf("policy registry fetch: %v %s", err, out)
					}
					out, err = run(binary, "pack", "install", "policy-probe", "--config-dir", cfg)
					if (err == nil) != test.allowed {
						t.Fatalf("named install policy: allowed=%v err=%v output=%s", test.allowed, err, out)
					}
					if test.allowed {
						lock, err := config.LoadLockfile(config.LockfilePath(cfg))
						if err != nil || lock.Packs["policy-probe"].Plugin == nil {
							t.Fatalf("installed policy missing: %v", err)
						}
						got, _ := json.Marshal(lock.Packs["policy-probe"].Plugin.Entry["policy"])
						want, _ := json.Marshal(entry.Plugin.Entry["policy"])
						if !bytes.Equal(got, want) {
							t.Fatal("policy declaration changed in installation provenance")
						}
						if test.name == "default" {
							packPath := filepath.Join(cfg, "packs/policy-probe")
							before, err := ReadFiles(packPath)
							if err != nil {
								t.Fatal(err)
							}
							for _, policy := range []string{`{"installation":"NOT_AVAILABLE"}`, `{"products":[]}`, `{"products":["atlas"]}`, `{"authentication":null}`} {
								changed := strings.Replace(body, `"policy":{}`, `"policy":`+policy, 1)
								write(t, market, ".agents/plugins/marketplace.json", changed, 0o644)
								out, err := run("codex", "plugin", "add", "policy-probe@policy-market", "--json")
								if err == nil {
									t.Fatalf("native reinstall ignored changed policy: %s", out)
								}
								out, err = run(binary, "pack", "update", "policy-probe", "--config-dir", cfg)
								if err == nil || !bytes.Contains(out, []byte("policy")) {
									t.Fatalf("catalog update ignored changed policy: %v %s", err, out)
								}
								after, readErr := ReadFiles(packPath)
								current, lockErr := config.LoadLockfile(config.LockfilePath(cfg))
								if readErr != nil || lockErr != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(current.Packs["policy-probe"].Plugin, lock.Packs["policy-probe"].Plugin) {
									t.Fatal("rejected catalog policy changed installed files or plugin metadata")
								}
							}
							write(t, market, ".agents/plugins/marketplace.json", body, 0o644)
							if out, err := run(binary, "pack", "update", "policy-probe", "--config-dir", cfg); err != nil {
								t.Fatalf("restored policy did not retry: %v %s", err, out)
							}
						}
					} else if _, err := os.Stat(filepath.Join(cfg, "packs/policy-probe")); !os.IsNotExist(err) {
						t.Fatal("rejected policy wrote an installed pack")
					}
				}
			})
		}
	}
}
