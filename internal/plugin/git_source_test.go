package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	sourcepkg "github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestCodexNativeGitSourceNormalization(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native Git acquisition")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			root, repo := t.TempDir(), t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
				if err != nil {
					t.Fatalf("fixture Git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			for _, dir := range []string{repo, filepath.Join(repo, "plugins/probe")} {
				manifest, body := ".codex-plugin/plugin.json", `{"name":"git-probe","version":"1.0.0"}`
				if format == AgentPlugins {
					manifest, body = "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"git-probe","version":"1.0.0"}`
				}
				write(t, dir, manifest, body, 0o644)
				write(t, dir, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Git acquisition fixture.\n---\nGIT_BODY_ONE\n", 0o644)
				write(t, dir, "scripts/run.sh", "#!/bin/sh\nprintf SHOULD_NOT_EXECUTE\n", 0o755)
			}
			git("init", "--initial-branch=main", repo)
			git("-C", repo, "add", ".")
			git("-C", repo, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic Git source fixture")
			first := git("-C", repo, "rev-parse", "HEAD")
			for _, dir := range []string{repo, filepath.Join(repo, "plugins/probe")} {
				write(t, dir, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Git acquisition fixture.\n---\nGIT_BODY_TWO\n", 0o644)
			}
			git("-C", repo, "add", ".")
			git("-C", repo, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Update synthetic Git source")
			git("-C", repo, "tag", "v1.0.0", first)
			bare := filepath.Join(root, "payload.git")
			git("clone", "--bare", repo, bare)
			gitConfig := filepath.Join(root, "gitconfig")
			write(t, root, "gitconfig", fmt.Sprintf("[url %q]\n\tinsteadOf = https://github.com/aipack-fixture/probe.git\n\tinsteadOf = https://github.com/aipack-fixture/probe\n", "file://"+bare), 0o600)
			t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
			t.Setenv("GIT_ALLOW_PROTOCOL", "file")
			for _, test := range []struct {
				name, kind, url, path string
				ref, sha              any
				pinned, refused       bool
			}{
				{"shorthand", "url", "aipack-fixture/probe", "", "", "", false, false},
				{"suffix", "url", "aipack-fixture/probe.git", "", "", "", false, false},
				{"https", "url", " https://github.com/aipack-fixture/probe ", " ./plugins/probe ", " main ", " ", false, false},
				{"subdir", "git-subdir", "aipack-fixture/probe", " ./plugins/probe ", "", "", false, false},
				{"sha", "url", "aipack-fixture/probe", "", " ignored-missing-branch ", " " + first + " ", true, false},
				{"sha-upper", "url", "aipack-fixture/probe", "", " ignored-missing-branch ", strings.ToUpper(first), true, false},
				{"short-sha", "url", "aipack-fixture/probe", "", " ignored-missing-branch ", first[:7], false, true},
				{"nullable", "url", "aipack-fixture/probe", "", nil, nil, false, false},
				{"local-relative", "url", "./payload.git", "", "", "", false, false},
				{"remote-relative", "url", "./payload.git", "", "", "", false, false},
				{"remote-relative-subdir", "git-subdir", "./payload.git", "plugins/probe", "", "", false, false},
				{"remote-relative-sha", "url", "./payload.git", "", "missing-branch", first, true, false},
				{"remote-relative-tag", "url", "./payload.git", "", "v1.0.0", "", true, false},
				{"remote-relative-market-ref", "url", "./payload.git", "", "", "", false, false},
				{"remote-relative-market-commit", "url", "./payload.git", "", "", "", false, false},
				{"remote-relative-market-nosuffix", "url", "./payload.git", "", "", "", false, false},
				{"remote-relative-credentials", "url", "./payload.git", "", "", "", false, false},
				{"absolute", "url", bare, "", "", "", false, false},
				{"file", "url", "file://" + bare, "", "", "", false, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					market, home := t.TempDir(), t.TempDir()
					if err := os.MkdirAll(filepath.Join(home, "codex"), 0o700); err != nil {
						t.Fatal(err)
					}
					expectedRepo := "https://github.com/aipack-fixture/probe.git"
					if strings.HasPrefix(test.name, "remote-relative") {
						git("clone", "--bare", bare, filepath.Join(market, "payload.git"))
						git("--git-dir="+filepath.Join(market, "payload.git"), "update-ref", "refs/heads/fixture", "HEAD")
						expectedRepo = "file://" + filepath.Join(market, "payload.git")
					} else if test.name == "local-relative" {
						if err := os.Symlink(bare, filepath.Join(market, "payload.git")); err != nil {
							t.Fatal(err)
						}
						expectedRepo = "file://" + filepath.Join(market, "payload.git")
					} else if test.name == "absolute" || test.name == "file" {
						expectedRepo = "file://" + bare
					}
					spec := map[string]any{"source": test.kind, "url": test.url, "ref": test.ref, "sha": test.sha}
					if test.path != "" {
						spec["path"] = test.path
					}
					catalog := map[string]any{"name": "git-market", "plugins": []any{map[string]any{"name": "git-probe", "source": spec}}}
					body, err := json.Marshal(catalog)
					if err != nil {
						t.Fatal(err)
					}
					write(t, market, ".agents/plugins/marketplace.json", string(body), 0o644)
					coordinates := config.RegistrySourceEntry{URL: market, Format: format}
					var authenticated, denied atomic.Int64
					if strings.HasPrefix(test.name, "remote-relative") {
						git("init", "--initial-branch=main", market)
						git("-C", market, "add", ".")
						git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic relative source marketplace")
						if test.name == "remote-relative-market-ref" || test.name == "remote-relative-market-commit" {
							git("-C", market, "tag", "catalog-v1")
							coordinates.Ref = "catalog-v1"
							if test.name == "remote-relative-market-commit" {
								coordinates.Ref = git("-C", market, "rev-parse", "HEAD")
							}
							if err := util.RemoveOwnedTree(filepath.Join(market, "payload.git")); err != nil {
								t.Fatal(err)
							}
							git("-C", market, "add", ".")
							git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Remove source on default marketplace branch")
						}
						marketBare := filepath.Join(t.TempDir(), "market.git")
						git("clone", "--bare", market, marketBare)
						coordinates.URL, coordinates.Path = "https://github.com/aipack-fixture/market.git", ".agents/plugins/marketplace.json"
						if test.name == "remote-relative-market-nosuffix" {
							coordinates.URL = strings.TrimSuffix(coordinates.URL, ".git")
						}
						baseConfig, err := os.ReadFile(gitConfig)
						if err != nil {
							t.Fatal(err)
						}
						actualMarketplace, extraConfig := "file://"+marketBare, ""
						if test.name == "remote-relative-credentials" {
							gitPath, err := exec.LookPath("git")
							if err != nil {
								t.Fatal(err)
							}
							backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(marketBare), "GIT_HTTP_EXPORT_ALL=1"}}
							server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
								user, password, ok := req.BasicAuth()
								if !ok || user != "fixture" || password != "owned-fixture-password" {
									denied.Add(1)
									w.Header().Set("WWW-Authenticate", `Basic realm="aipack-owned-fixture"`)
									http.Error(w, "fixture Git credentials required", http.StatusUnauthorized)
									return
								}
								authenticated.Add(1)
								backend.ServeHTTP(w, req)
							}))
							t.Cleanup(server.Close)
							write(t, market, "git-ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), 0o600)
							actualMarketplace = server.URL + "/market.git"
							extraConfig = fmt.Sprintf("[http %q]\n\tsslVerify = true\n\tsslCAInfo = %q\n[credential]\n\thelper =\n[credential %q]\n\thelper = \"!f() { printf 'username=fixture\\npassword=owned-fixture-password\\n'; }; f\"\n", server.URL, filepath.Join(market, "git-ca.pem"), server.URL)
							t.Setenv("GIT_ALLOW_PROTOCOL", "file:https")
							t.Setenv("GIT_SSL_NO_VERIFY", "false")
						}
						write(t, market, "fixture.gitconfig", string(baseConfig)+fmt.Sprintf("[url %q]\n\tinsteadOf = %s\n\tinsteadOf = %s\n", actualMarketplace, coordinates.URL, strings.TrimSuffix(coordinates.URL, ".git")+".git")+extraConfig, 0o600)
						t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(market, "fixture.gitconfig"))
						expectedRepo = test.url
					}
					env := slices.DeleteFunc(os.Environ(), func(value string) bool {
						return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
					})
					env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, "codex"))
					var acquired string
					for _, args := range [][]string{{"plugin", "marketplace", "add", coordinates.URL, "--json"}, {"plugin", "add", "git-probe@git-market", "--json"}} {
						if args[1] == "marketplace" && coordinates.Ref != "" {
							args = append(args, "--ref", coordinates.Ref)
						}
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						cmd := exec.CommandContext(ctx, "codex", args...)
						cmd.Env, cmd.Dir, cmd.WaitDelay = env, market, time.Second
						out, err := cmd.CombinedOutput()
						cancel()
						if args[1] == "add" && test.refused {
							if err == nil || !bytes.Contains(out, []byte("does not match requested SHA")) {
								t.Fatalf("native short SHA unexpectedly acquired: %v %s", err, out)
							}
							continue
						}
						if err != nil {
							t.Fatalf("native %v: %v\n%s", args, err, out)
						}
						if args[1] == "add" {
							var result struct {
								InstalledPath string `json:"installedPath"`
							}
							if err := json.Unmarshal(out, &result); err != nil || result.InstalledPath == "" {
								t.Fatalf("native install omitted cache path: %s %v", out, err)
							}
							acquired = result.InstalledPath
						}
					}
					if test.name == "remote-relative-credentials" {
						if authenticated.Load() == 0 {
							t.Fatal("native marketplace acquisition skipped the authenticated Git server")
						}
						// Count subsequent AIPack requests independently of the native control.
						authenticated.Store(0)
					}
					reg, err := config.ParseMarketplace(body, coordinates)
					if err != nil {
						t.Fatal(err)
					}
					entry := reg.Packs["git-probe"]
					if test.refused {
						if entry.Unsupported == "" {
							t.Fatal("AIPack accepted a SHA the native installer refuses")
						}
						return
					}
					original, _ := json.Marshal(entry.Plugin.Entry["source"])
					declared, _ := json.Marshal(spec)
					if entry.Unsupported != "" || entry.Repo != expectedRepo || !bytes.Equal(original, declared) {
						t.Fatalf("normalized Git source lost coordinates: %+v", entry)
					}
					clone := t.TempDir()
					acquisitionURL := entry.Repo
					if strings.HasPrefix(test.name, "remote-relative") {
						parent := t.TempDir()
						if err := sourcepkg.EnsureCloneWithRef(context.Background(), coordinates.URL, parent, coordinates.Ref, "", sourcepkg.RunGit); err != nil {
							t.Fatal(err)
						}
						acquisitionURL = "file://" + filepath.Join(parent, entry.Repo)
					}
					if err := sourcepkg.EnsureCloneWithRef(context.Background(), acquisitionURL, clone, entry.Ref, "", sourcepkg.RunGit); err != nil {
						t.Fatal(err)
					}
					fetched := filepath.Join(clone, entry.Path)
					files, err := ReadFiles(acquired)
					if err != nil {
						t.Fatal(err)
					}
					actual, err := ReadFiles(fetched)
					body, readErr := os.ReadFile(filepath.Join(fetched, "skills/probe/SKILL.md"))
					if err != nil || readErr != nil || !reflect.DeepEqual(actual, files) || bytes.Contains(body, []byte("GIT_BODY_ONE")) != test.pinned {
						t.Fatalf("native/imported Git acquisition differs: %v", err)
					}
					pack := t.TempDir()
					manifest, err := MaterializeCodex(fetched, pack, "alias", "git-market")
					if err != nil {
						t.Fatal(err)
					}
					selected := map[domain.PackCategory][]string{}
					for category, components := range manifest.NativePlugin.Components {
						for id := range components {
							selected[category] = append(selected[category], id)
						}
					}
					rendered, err := RenderCodex(domain.NativePluginSelection{Package: *manifest.NativePlugin, Root: pack, Selected: selected, SettingsEnabled: true})
					if err != nil || !reflect.DeepEqual(rendered, files) {
						t.Fatalf("Git source conversion changed bytes or modes: %v", err)
					}
					if binary := os.Getenv("AIPACK_TEST_BINARY"); binary != "" {
						authenticated.Store(0)
						cfg := t.TempDir()
						write(t, cfg, "sync-config.yaml", "schema_version: 1\ndefaults:\n  profile: default\n  auto_sync: false\n", 0o600)
						cliDir := market
						if strings.HasPrefix(test.name, "remote-relative") {
							cliDir = t.TempDir()
						}
						cli := func(args ...string) []byte {
							t.Helper()
							ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
							defer cancel()
							cmd := exec.CommandContext(ctx, binary, append(args, "--config-dir", cfg)...)
							cmd.Env, cmd.Dir = append(env, "AIPACK_TELEMETRY_DISABLED=1", "AIPACK_NO_UPDATE_CHECK=1"), cliDir
							out, err := cmd.CombinedOutput()
							if err != nil {
								t.Fatalf("Git source CLI %v: %v\n%s", args, err, out)
							}
							return out
						}
						if strings.HasPrefix(test.name, "remote-relative") {
							args := []string{"registry", "fetch", coordinates.URL, "--path", coordinates.Path, "--deep"}
							if test.name != "remote-relative-market-ref" {
								args = append(args, "--format", format)
							}
							if coordinates.Ref != "" {
								args = append(args, "--ref", coordinates.Ref)
							}
							cli(args...)
						} else {
							cli("registry", "fetch", filepath.Join(market, ".agents/plugins/marketplace.json"), "--format", format, "--deep")
						}
						var preview struct {
							Source string `json:"source"`
						}
						if err := json.Unmarshal(cli("pack", "inspect", "git-probe", "--json"), &preview); err != nil || preview.Source != expectedRepo {
							t.Fatalf("named Git inspection changed source: %+v %v", preview, err)
						}
						if strings.HasPrefix(test.name, "remote-relative") {
							cli("pack", "versions", "git-probe", "--json")
						}
						cli("pack", "install", "git-probe", "--name", "alias")
						lock, err := config.LoadLockfile(config.LockfilePath(cfg))
						if err != nil || lock.Packs["alias"].Origin != expectedRepo || lock.Packs["alias"].Method != config.MethodClone || lock.Packs["alias"].Plugin == nil {
							t.Fatalf("named Git installation lost origin/method: %+v %v", lock, err)
						}
						original, _ := json.Marshal(lock.Packs["alias"].Plugin.Entry["source"])
						if !bytes.Equal(original, declared) {
							t.Fatal("named installation rewrote original source fields")
						}
						cli("pack", "show", "alias", "--json")
						if strings.HasPrefix(test.name, "remote-relative") {
							cli("pack", "versions", "alias", "--json")
						}
						cli("pack", "update", "alias", "--dry-run", "--json")
						cli("pack", "update", "alias")
						if test.name == "remote-relative-credentials" {
							packPath := filepath.Join(cfg, "packs/alias")
							before, err := ReadFiles(packPath)
							if err != nil {
								t.Fatal(err)
							}
							gitSettings, err := os.ReadFile(filepath.Join(market, "fixture.gitconfig"))
							if err != nil {
								t.Fatal(err)
							}
							write(t, market, "fixture.gitconfig", strings.ReplaceAll(string(gitSettings), "owned-fixture-password", "denied-fixture-password"), 0o600)
							denied.Store(0)
							nativeHome := t.TempDir()
							if err := os.MkdirAll(filepath.Join(nativeHome, "codex"), 0o700); err != nil {
								t.Fatal(err)
							}
							nativeEnv := slices.DeleteFunc(slices.Clone(env), func(value string) bool {
								return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
							})
							ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
							nativeCmd := exec.CommandContext(ctx, "codex", "plugin", "marketplace", "add", coordinates.URL, "--json")
							nativeCmd.Env, nativeCmd.Dir = append(nativeEnv, "HOME="+nativeHome, "CODEX_HOME="+filepath.Join(nativeHome, "codex")), cliDir
							nativeOut, nativeErr := nativeCmd.CombinedOutput()
							cancel()
							if nativeErr == nil || denied.Load() == 0 {
								t.Fatalf("native did not reach Git credential refusal: %v %s", nativeErr, nativeOut)
							}
							denied.Store(0)
							ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
							cmd := exec.CommandContext(ctx, binary, "pack", "update", "alias", "--config-dir", cfg)
							cmd.Env, cmd.Dir = append(env, "AIPACK_NO_UPDATE_CHECK=1"), cliDir
							out, updateErr := cmd.CombinedOutput()
							cancel()
							write(t, market, "fixture.gitconfig", string(gitSettings), 0o600)
							after, readErr := ReadFiles(packPath)
							retained, lockErr := config.LoadLockfile(config.LockfilePath(cfg))
							meta := retained.Packs["alias"]
							if updateErr == nil || denied.Load() == 0 || readErr != nil || lockErr != nil || !reflect.DeepEqual(before, after) || meta.Origin != expectedRepo || meta.CommitHash != lock.Packs["alias"].CommitHash || !reflect.DeepEqual(meta.Plugin, lock.Packs["alias"].Plugin) {
								t.Fatalf("failed Git authentication replaced package/provenance: %v %v %v %s", updateErr, readErr, lockErr, out)
							}
							cli("pack", "update", "alias")
						}
						if test.pinned {
							cli("pack", "update", "alias", "--ref", "latest")
							body, err := os.ReadFile(filepath.Join(cfg, "packs/alias/upstream/skills/probe/SKILL.md"))
							if err != nil || !bytes.Contains(body, []byte("GIT_BODY_TWO")) {
								t.Fatalf("Git SHA unpin did not advance payload: %v", err)
							}
						}
						cli("pack", "rename", "alias", "renamed")
						cli("pack", "delete", "renamed", "--yes")
						if test.name == "remote-relative-credentials" && authenticated.Load() == 0 {
							t.Fatal("AIPack lifecycle skipped the authenticated Git server")
						}
					}
				})
			}
		})
	}
}
