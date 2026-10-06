package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/index"
	"github.com/shrug-labs/aipack/internal/plugin"
	packsource "github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPackDeleteRetainsSiblingCodexMarketplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native plugin delivery requires POSIX")
	}
	cfg, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	writeFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AIPACK_TEST_NATIVE_CALLS\"\nprintf '{}\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AIPACK_TEST_NATIVE_CALLS", log)
	eng := engine.New(nil, nil)
	ledgerPath := engine.LedgerPath(cfg, domain.ScopeGlobal, "", domain.HarnessCodex)
	market := domain.NativeMarketplaceDir(cfg, domain.HarnessCodex, "market", "")
	ledger := domain.NewLedger()
	ledger.NativePlugins = map[string]domain.NativePluginRecord{}
	for _, name := range []string{"first", "second"} {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"`+name+`"}`)
		if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: cfg, PackPath: src, Name: name}, nil); err != nil {
			t.Fatal(err)
		}
		ledger.NativePlugins[name+"@market"] = domain.NativePluginRecord{
			Harness: domain.HarnessCodex, Home: home, ConfigHome: filepath.Join(home, ".codex"),
			MarketplaceDir: market, PayloadPath: "plugins/" + name,
			CachePath:    filepath.Join(home, ".codex/plugins/cache/market", name, "1.0.0"),
			SettingsPath: filepath.Join(home, ".codex/config.toml"), SourcePack: name,
		}
		writeFile(t, filepath.Join(market, "plugins", name, "marker"), name)
	}
	writeFile(t, filepath.Join(home, ".codex/config.toml"), fmt.Sprintf("[marketplaces.market]\nsource_type = 'local'\nsource = %q\n", market))
	writeFile(t, nativeCatalogPath(domain.HarnessCodex, market), `{"name":"market","plugins":[{"name":"first","source":"./plugins/first"},{"name":"second","source":"./plugins/second"}]}`)
	if err := eng.SaveLedger(ledgerPath, ledger, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfg, Name: name, Registry: testRegistry()}, nil); err != nil {
			t.Fatal(err)
		}
		calls, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		removed := strings.Contains(string(calls), "marketplace remove market")
		if removed != (name == "second") {
			t.Fatalf("marketplace removal after deleting %s: %s", name, calls)
		}
		if name == "first" {
			next, _, err := eng.LoadLedger(ledgerPath)
			if err != nil || len(next.NativePlugins) != 1 || next.NativePlugins["second@market"].SourcePack != "second" {
				t.Fatalf("lost sibling ownership: %+v %v", next, err)
			}
			if string(mustRead(t, filepath.Join(market, "plugins/second/marker"))) != "second" {
				t.Fatal("lost sibling payload")
			}
			entries, err := plugin.CatalogEntries(mustRead(t, nativeCatalogPath(domain.HarnessCodex, market)))
			if err != nil || len(entries) != 1 || entries[0]["name"] != "second" {
				t.Fatalf("lost sibling catalog entry: %+v %v", entries, err)
			}
		}
	}
}

func TestRemoveNativeMarketplacePreservesRetargetedCodexSource(t *testing.T) {
	t.Parallel()
	for _, sourceType := range []string{"local", "git"} {
		t.Run(sourceType, func(t *testing.T) {
			home, market, foreign := t.TempDir(), t.TempDir(), t.TempDir()
			record := domain.NativePluginRecord{Harness: domain.HarnessCodex, ConfigHome: filepath.Join(home, ".codex"), MarketplaceDir: market}
			source := foreign
			if sourceType == "git" {
				// A matching source string alone does not establish local ownership.
				source = market
			}
			path := nativeUserConfig(record)
			body := fmt.Sprintf("[marketplaces.market]\nsource_type = %q\nsource = %q\n", sourceType, source)
			writeFile(t, path, body)
			err := removeNativeMarketplace(context.Background(), home, record, "market")
			if err == nil || !strings.Contains(err.Error(), "changed after delivery; retain its registration") {
				t.Fatalf("retargeted marketplace was not protected: %v", err)
			}
			if got := string(mustRead(t, path)); got != body {
				t.Fatalf("retargeted marketplace registration changed: %s", got)
			}
		})
	}
}

func TestRelativePluginGitSourceBoundary(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "contained-link", "escaping-link", "escaping-parent", "broken-link"} {
		t.Run(kind, func(t *testing.T) {
			configDir, outside := t.TempDir(), t.TempDir()
			repo := "./payload.git"
			if kind == "escaping-parent" {
				repo = "./repos/payload.git"
			}
			var expected string
			runGit := func(_ context.Context, args ...string) error {
				if args[0] != "clone" {
					return nil
				}
				root := args[len(args)-1]
				expected = filepath.Join(root, "payload.git")
				if kind == "directory" {
					return os.MkdirAll(expected, 0o700)
				}
				link, target := expected, filepath.Join(outside, "payload.git")
				if kind == "contained-link" {
					expected = filepath.Join(root, "repos/payload.git")
					target = "repos/payload.git"
				} else if kind == "escaping-parent" {
					link, target = filepath.Join(root, "repos"), outside
				}
				if err := os.MkdirAll(filepath.Join(outside, "payload.git"), 0o700); err != nil {
					return err
				}
				if kind == "contained-link" {
					if err := os.MkdirAll(expected, 0o700); err != nil {
						return err
					}
				} else if kind == "broken-link" {
					target = "missing.git"
				}
				if err := os.Symlink(target, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks unavailable: %v", err)
					}
					return err
				}
				return nil
			}
			called := false
			err := withPluginGitRepository(context.Background(), configDir, &domain.PluginSource{MarketplaceURL: "https://example.invalid/market.git"}, repo, runGit, func(actual string) error {
				called = true
				parsed, err := url.Parse(actual)
				wantPath := filepath.ToSlash(canonicalPath(expected))
				if runtime.GOOS == "windows" {
					wantPath = "/" + wantPath
				}
				if err != nil || parsed.Scheme != "file" || parsed.Host != "" || parsed.Path != wantPath {
					t.Fatalf("unexpected resolved source: %s (%v)", actual, err)
				}
				return nil
			})
			allowed := kind == "directory" || kind == "contained-link"
			if (err == nil) != allowed || called != allowed {
				t.Fatalf("source %s: acquired=%t error=%v", kind, called, err)
			}
			if strings.HasPrefix(kind, "escaping") && !strings.Contains(err.Error(), "escapes the marketplace root") {
				t.Fatal(err)
			}
		})
	}
}

func TestRelativePluginGitSourceBoundaryCLI(t *testing.T) {
	binary := os.Getenv("AIPACK_TEST_BINARY")
	if binary == "" {
		t.Skip("set AIPACK_TEST_BINARY to a freshly built CLI")
	}
	for _, escaping := range []bool{false, true} {
		t.Run(fmt.Sprintf("escaping=%t", escaping), func(t *testing.T) {
			root := t.TempDir()
			configDir, home, market := filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "market")
			writeFile(t, filepath.Join(configDir, "sync-config.yaml"), "schema_version: 1\ndefaults:\n  auto_sync: false\n")
			source := filepath.Join(root, "source")
			writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
			git := func(args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
					t.Fatalf("fixture git %v: %v: %s", args, err, output)
				}
			}
			git("init", "--initial-branch=main", source)
			git("-C", source, "add", ".")
			git("-C", source, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic source boundary fixture")
			git("-C", source, "tag", "v1.0.0")
			payload := filepath.Join(market, "repos/payload.git")
			if escaping {
				payload = filepath.Join(root, "outside.git")
			}
			writeFile(t, filepath.Join(market, ".agents/plugins/marketplace.json"), `{"name":"market","plugins":[{"name":"probe","source":{"source":"url","url":"./payload.git"}}]}`)
			git("clone", "--bare", source, payload)
			git("--git-dir="+payload, "update-ref", "refs/heads/fixture", "HEAD")
			target := "repos/payload.git"
			if escaping {
				target = payload
			}
			if err := os.Symlink(target, filepath.Join(market, "payload.git")); err != nil {
				t.Fatal(err)
			}
			git("init", "--initial-branch=main", market)
			git("-C", market, "add", ".")
			git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic marketplace boundary fixture")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			cli := func(wantError bool, args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, append(args, "--config-dir", configDir)...)
				cmd.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool { return strings.HasPrefix(value, "HOME=") }), "HOME="+home, "AIPACK_TELEMETRY_DISABLED=1", "AIPACK_NO_UPDATE_CHECK=1")
				output, err := cmd.CombinedOutput()
				if (err != nil) != wantError || wantError && !bytes.Contains(output, []byte("escapes the marketplace root")) {
					t.Fatalf("CLI %v: %v: %s", args, err, output)
				}
			}
			marketURL := (&url.URL{Scheme: "file", Path: market}).String()
			cli(false, "registry", "fetch", marketURL, "--path", ".agents/plugins/marketplace.json", "--format", plugin.CodexLegacy)
			cli(escaping, "pack", "inspect", "probe")
			cli(escaping, "pack", "versions", "probe")
			cli(escaping, "pack", "install", "probe")
			if !escaping {
				cli(false, "pack", "update", "probe", "--dry-run")
			}
		})
	}
}

func TestPluginGitURLInstallSkipsPackProbe(t *testing.T) {
	t.Parallel()
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins, plugin.Claude} {
		t.Run(format, func(t *testing.T) {
			cfgDir := t.TempDir()
			writeTestSyncConfig(t, cfgDir)
			const repo = "https://github.com/aipack-fixture/probe/"
			var cloneURL string
			request := PackInstallRequestFromRegistryEntry(cfgDir, "alias", config.RegistryEntry{
				Repo: repo,
				Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "market", Entry: map[string]any{
					"name": "probe", "source": map[string]any{"source": "url", "url": repo},
				}},
			})
			request.URLOKFn = func(context.Context, string) (bool, error) {
				t.Error("native Git source triggered an ordinary pack manifest probe")
				return false, nil
			}
			request.RunGitFn = func(_ context.Context, args ...string) error {
				if len(args) > 0 && args[0] == "clone" && !slices.Contains(args, "--bare") {
					cloneURL = args[len(args)-2]
					root := args[len(args)-1]
					manifest := ".codex-plugin/plugin.json"
					body := `{"name":"probe","version":"1.0.0"}`
					if format == plugin.Claude {
						manifest = ".claude-plugin/plugin.json"
					} else if format == plugin.AgentPlugins {
						manifest = "plugin.json"
						body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
					}
					writeFile(t, filepath.Join(root, manifest), body)
					writeFile(t, filepath.Join(root, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Fixture.\n---\nPLUGIN_URL_BODY\n")
				}
				return nil
			}
			request.GitHashFn = fakeHashFn(fakeHash1)
			preview, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: cfgDir, URL: repo, Name: "alias", Plugin: request.Plugin, RunGitFn: request.RunGitFn})
			if err != nil || cloneURL != repo || preview.Source != repo || preview.NativePlugin == nil || preview.NativePlugin.Binding() != "probe@market" {
				t.Fatalf("plugin Git URL inspection changed source: %+v %q %v", preview, cloneURL, err)
			}
			if err := PackInstall(context.Background(), request, nil); err != nil {
				t.Fatal(err)
			}
			lock, err := config.LoadLockfile(config.LockfilePath(cfgDir))
			if err != nil || cloneURL != repo || lock.Packs["alias"].Origin != repo || lock.Packs["alias"].Plugin == nil || lock.Packs["alias"].Plugin.Format != format || !bytes.Contains(mustRead(t, filepath.Join(PacksDir(cfgDir), "alias/upstream/skills/probe/SKILL.md")), []byte("PLUGIN_URL_BODY")) {
				t.Fatalf("plugin Git URL acquisition lost source or content: %+v %q %v", lock, cloneURL, err)
			}
		})
	}
}

func TestLocalMarketplacePackLifecycle(t *testing.T) {
	for _, label := range []string{plugin.CodexLegacy, plugin.Claude, "claude-catalog", plugin.AgentPlugins} {
		format := label
		if label == "claude-catalog" {
			format = plugin.Claude
		}
		t.Run(label, func(t *testing.T) {
			root, configDir := t.TempDir(), t.TempDir()
			t.Cleanup(func() {
				if err := util.RemoveOwnedTree(configDir); err != nil {
					t.Error(err)
				}
			})
			source := filepath.Join(root, "plugins/probe")
			manifestPath, catalogPath := ".codex-plugin/plugin.json", ".agents/plugins/marketplace.json"
			if format == plugin.Claude {
				manifestPath, catalogPath = ".claude-plugin/plugin.json", ".claude-plugin/marketplace.json"
			}
			if format == plugin.AgentPlugins {
				manifestPath = "plugin.json"
				writeFile(t, filepath.Join(source, manifestPath), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-probe","version":"1.0.0"}`)
			} else if label != "claude-catalog" {
				writeFile(t, filepath.Join(source, manifestPath), `{"name":"native-probe","version":"1.0.0"}`)
			}
			writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Local fixture.\n---\nVersion one.\n")
			writeFile(t, filepath.Join(source, "node_modules/dependency/index.js"), "runtime dependency")
			readOnlyNativeAsset(t, source)
			rootVar := "CODEX_PLUGIN_ROOT"
			if format == plugin.Claude {
				rootVar = "CLAUDE_PLUGIN_ROOT"
			}
			writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"node","args":["${`+rootVar+`}/node_modules/dependency/index.js"]}}}`)
			if format == plugin.AgentPlugins {
				writeFile(t, filepath.Join(source, "mcp.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"node","args":["${PLUGIN_ROOT}/node_modules/dependency/index.js"]}}}`)
			}
			catalog := filepath.Join(root, catalogPath)
			const numbers = `{"integer":1208925819614629174706177,"fraction":0.12345678901234567890123456789,"nested":[1e+1000,-0.0,true,null,"9007199254740993"]}`
			fields := ""
			if label == "claude-catalog" {
				fields = `,"strict":false,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo catalog"}]}]},"mcpServers":{"probe":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/node_modules/dependency/index.js"]}}`
			}
			writeFile(t, catalog, `{"name":"market","numbers":`+numbers+`,"plugins":[{"name":"native-probe","version":"1.0.0","source":"./plugins/probe","numbers":`+numbers+fields+`}]}`)
			if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: catalog}, nil); err != nil {
				t.Fatal(err)
			}
			entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "native-probe")
			if err != nil {
				t.Fatal(err)
			}
			install := PackInstallRequestFromRegistryEntry(configDir, "alias", entry)
			if err := PackInstall(context.Background(), install, nil); err != nil {
				t.Fatal(err)
			}
			lock, err := config.LoadLockfile(config.LockfilePath(configDir))
			if err != nil || lock.Packs["alias"].Plugin == nil || lock.Packs["alias"].Plugin.Format != format {
				t.Fatalf("installed native format provenance missing: %+v %v", lock, err)
			}
			checkNumbers := func(value any) {
				t.Helper()
				actual, err := json.Marshal(value)
				var expected any
				if err != nil {
					t.Fatal(err)
				}
				if err := util.UnmarshalJSON([]byte(numbers), &expected); err != nil {
					t.Fatal(err)
				}
				canonical, err := json.Marshal(expected)
				if err != nil || !bytes.Equal(actual, canonical) {
					t.Fatalf("native catalog numbers changed: %s vs %s (%v)", actual, canonical, err)
				}
			}
			checkNumbers(entry.Plugin.Entry["numbers"])
			checkNumbers(entry.Plugin.MarketplaceMetadata["numbers"])
			checkNumbers(lock.Packs["alias"].Plugin.Entry["numbers"])
			checkNumbers(lock.Packs["alias"].Plugin.MarketplaceMetadata["numbers"])
			preview, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: configDir, Input: "native-probe", RegistryPath: catalog})
			if err != nil || preview.NativePlugin == nil || preview.NativePlugin.Binding() != "native-probe@market" {
				t.Fatalf("local native preview: %+v %v", preview, err)
			}
			resources, err := deepIndexOnePack(context.Background(), configDir, "native-probe", entry, nil)
			if err != nil {
				t.Fatal(err)
			}
			kinds := map[string]int{}
			for _, resource := range resources {
				kinds[resource.Kind]++
			}
			if kinds["skill"] != 1 || kinds["mcp"] != 1 || kinds["plugin"] != 1 {
				t.Fatalf("native discovery lost mapped components: %+v", resources)
			}
			if label == "claude-catalog" && (preview.NativePlugin.Manifest != "" || preview.Counts.Hooks != 1 || kinds["hook"] != 1) {
				t.Fatalf("catalog-only discovery lost inline content: %+v %+v", preview, kinds)
			}
			installed := filepath.Join(PacksDir(configDir), "alias")
			if string(mustRead(t, filepath.Join(installed, "upstream/node_modules/dependency/index.js"))) != "runtime dependency" {
				t.Fatal("default local install stripped executable dependencies")
			}
			beforeSave := mustRead(t, filepath.Join(installed, "pack.json"))
			if _, err := RunSavePipeline(engine.New(nil, nil), SavePipelineRequest{ConfigDir: configDir, PackName: "alias"}, testRegistry()); err == nil {
				t.Fatal("save accepted imported source as an authored destination")
			}
			if !bytes.Equal(beforeSave, mustRead(t, filepath.Join(installed, "pack.json"))) {
				t.Fatal("rejected save changed the imported manifest")
			}
			out := filepath.Join(t.TempDir(), "render")
			profile := domain.Profile{Packs: []domain.Pack{{Name: "alias", NativePlugin: &domain.NativePluginSelection{}}}}
			if err := RunRender(context.Background(), profile, out, testRegistry()); err == nil {
				t.Fatal("standalone render silently omitted native delivery")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("unsupported render wrote output")
			}
			writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Local fixture.\n---\nVersion two.\n")
			var refreshedCatalog map[string]any
			if err := util.UnmarshalJSON(mustRead(t, catalog), &refreshedCatalog); err != nil {
				t.Fatal(err)
			}
			refreshedEntry := refreshedCatalog["plugins"].([]any)[0].(map[string]any)
			refreshedEntry["version"] = "2.0.0"
			refreshedEntry["description"] = "Updated catalog metadata"
			if label == "claude-catalog" {
				refreshedEntry["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"] = "echo updated-catalog"
			}
			catalogBytes, err := json.Marshal(refreshedCatalog)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, catalog, string(catalogBytes))
			results, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias", DryRun: true}, nil, nil)
			if err != nil || len(results) != 1 || results[0].Status != StatusUpdated || !results[0].DryRun {
				t.Fatalf("local dry-run: %+v %v", results, err)
			}
			if strings.Contains(string(mustRead(t, filepath.Join(installed, "upstream/skills/probe/SKILL.md"))), "Version two") {
				t.Fatal("dry-run changed installed source")
			}
			if !bytes.Equal(beforeSave, mustRead(t, filepath.Join(installed, "pack.json"))) {
				t.Fatal("dry-run changed installed catalog metadata")
			}
			results, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
			if err != nil || len(results) != 1 || results[0].Status != StatusUpdated {
				t.Fatalf("local update: %+v %v", results, err)
			}
			priorSource := mustRead(t, filepath.Join(installed, "pack.json"))
			manifest, err := config.LoadPackManifest(filepath.Join(installed, "pack.json"))
			if err != nil {
				t.Fatal(err)
			}
			checkNumbers(manifest.NativePlugin.MarketplaceEntry["numbers"])
			checkNumbers(manifest.NativePlugin.MarketplaceMetadata["numbers"])
			if manifest.NativePlugin.MarketplaceEntry["description"] != "Updated catalog metadata" || (label == "claude-catalog" && manifest.Version != "2.0.0") {
				t.Fatalf("catalog update was frozen at installation: %+v", manifest)
			}
			priorLock, err := config.LoadLockfile(config.LockfilePath(configDir))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(map[string]any(priorLock.Packs["alias"].Plugin.Entry), manifest.NativePlugin.MarketplaceEntry) {
				t.Fatal("updated catalog entry was not recorded in the lockfile")
			}
			writeFile(t, filepath.Join(source, manifestPath), `{"name":"../invalid"}`)
			checkedAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
			results, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias", NowFn: func() time.Time { return checkedAt }}, nil, nil)
			if len(results) != 1 || results[0].Status != StatusError {
				t.Fatalf("invalid update accepted: %+v %v", results, err)
			}
			currentLock, err := config.LoadLockfile(config.LockfilePath(configDir))
			if err != nil || currentLock.Packs["alias"].LastCheckedAt != checkedAt.Format(time.RFC3339) {
				t.Fatalf("failed probe did not record its check time: %v", err)
			}
			priorMeta, currentMeta := priorLock.Packs["alias"], currentLock.Packs["alias"]
			priorMeta.LastCheckedAt, currentMeta.LastCheckedAt = "", ""
			if !bytes.Equal(priorSource, mustRead(t, filepath.Join(installed, "pack.json"))) || !reflect.DeepEqual(priorMeta, currentMeta) {
				t.Fatal("invalid conversion changed installed package or metadata")
			}
			deleted, err := PackDelete(configDir, "alias", io.Discard)
			if err != nil || !deleted.SourceRemoved {
				t.Fatalf("read-only native payload deletion: %+v %v", deleted, err)
			}
		})
	}
}

func TestNativeAgentCandidateSources(t *testing.T) {
	root, configDir := t.TempDir(), t.TempDir()
	pack := filepath.Join(configDir, "packs/alias")
	for _, dir := range []string{"first", "second"} {
		writeFile(t, filepath.Join(root, dir, "reviewer.md"), "---\nname: Reviewer\ndescription: "+dir+" native agent.\n---\n"+strings.ToUpper(dir)+"_AGENT\n")
	}
	writeFile(t, filepath.Join(root, "commands/action.md"), "Native command.")
	writeFile(t, filepath.Join(root, "skills/inert/SKILL.md"), "---\nname: inert\ndescription: A native fixture.\n---\nNative skill.")
	entry := map[string]any{"name": "candidate-probe", "source": "./plugins/probe", "agents": []string{"./first/reviewer.md", "./second/reviewer.md", "./first/reviewer.md"},
		"hooks":      map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo native"}}}}},
		"mcpServers": map[string]any{"probe": map[string]any{"command": "node", "args": []string{"fixture.js"}}}}
	m, err := plugin.MaterializeClaude(root, pack, "alias", domain.PluginSource{Marketplace: "candidate-market", Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	show := PackShowEntry{Path: pack, NativePlugin: m.NativePlugin, manifest: m}
	source := &domain.PluginSource{Format: plugin.Claude, Name: "candidate-probe", Marketplace: "candidate-market", MarketplaceURL: "https://example.com/plugins.git", MarketplacePath: ".claude-plugin/marketplace.json"}
	lock := config.Lockfile{Packs: map[string]config.InstalledPackMeta{"alias": {
		Method: config.MethodClone, Origin: source.MarketplaceURL, SubPath: "plugins/probe", CommitHash: fakeHash1,
		Plugin: source, MaterializedDigest: "recorded-import-digest",
	}}}
	if err := config.SaveLockfile(config.LockfilePath(configDir), lock); err != nil {
		t.Fatal(err)
	}
	profilePack := ProfilePackInfo{Root: pack, Manifest: m}
	wantPaths := []string{filepath.Join(pack, "upstream/first/reviewer.md"), filepath.Join(pack, "upstream/second/reviewer.md")}
	if !slices.Equal(show.ContentPaths(domain.CategoryAgents, "Reviewer"), wantPaths) || !slices.Equal(profilePack.ContentPaths(domain.CategoryAgents, "Reviewer"), wantPaths) {
		t.Fatal("pack/profile views lost declared agent sources")
	}
	var total int64
	for _, path := range wantPaths {
		total += int64(len(mustRead(t, path)))
	}
	if show.ContentSize(domain.CategoryAgents, "Reviewer") != total || profilePack.ContentSize(domain.CategoryAgents, "Reviewer") != total {
		t.Fatal("agent size omitted a candidate source")
	}
	db, err := index.Open(filepath.Join(configDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(index.PackInfo{Name: "alias"}, resourcesFromManifestRoot("alias", pack, m)); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"first", "second"} {
		results, err := db.Search(strings.ToUpper(dir)+"_AGENT", index.SearchFilters{Kind: "agent"})
		if err != nil || len(results) != 1 || results[0].Name != "Reviewer" || results[0].Path != filepath.Join(pack, "upstream", dir, "reviewer.md") {
			t.Fatalf("agent source missing or given another selector: %s %+v %v", dir, results, err)
		}
	}
	results, err := db.Search("", index.SearchFilters{Kind: "agent"})
	if err != nil || len(results) != 2 {
		t.Fatalf("candidate indexing duplicated repeated declarations: %+v %v", results, err)
	}
	home := t.TempDir()
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
	profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidates := FindTraceCandidates(profile, "Reviewer")
	if len(candidates) != 1 || !slices.Equal(candidates[0].SourcePaths, wantPaths) || candidates[0].NativeBinding != m.NativePlugin.Binding() {
		t.Fatalf("native trace lost candidate sources or split the selector: %+v", candidates)
	}
	if counts := CountProfileContent(profile); counts.Agents != 1 {
		t.Fatalf("native agent total = %d, want 1", counts.Agents)
	}
	req := TraceRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "native")}},
		ProfileName: "default", ProfileConfig: cfg, ResourceType: "agent", ResourceName: "Reviewer"}
	trace, err := RunTrace(context.Background(), eng, profile, req, testRegistry())
	if err != nil || !trace.Found || len(trace.Blockers) != 0 || len(trace.Destinations) != 3 || !slices.Equal(trace.Source.SourcePaths, wantPaths) {
		t.Fatalf("cold native trace: %+v %v", trace, err)
	}
	if trace.Source.Origin != source.MarketplaceURL || trace.Source.PluginSource == nil || trace.Source.PluginSource.MarketplacePath != source.MarketplacePath || trace.Source.SubPath != "plugins/probe" || trace.Source.CommitHash != fakeHash1 || trace.Source.ConverterVersion != m.NativePlugin.ConverterVersion || trace.Source.MaterializedDigest != "recorded-import-digest" || !slices.Equal(trace.Source.Selected[domain.CategoryAgents], []string{"Reviewer"}) {
		t.Fatalf("trace lost original source or transformed selection: %+v", trace.Source)
	}
	for _, destination := range trace.Destinations {
		if (destination.Location != "package" && destination.Location != "activation") || destination.DiffKind != domain.DiffCreate {
			t.Fatalf("cold trace invented an installed cache: %+v", destination)
		}
		if destination.PlannedGeneration == "" || destination.DeliveredGeneration != "" || destination.MarketplaceSource == "" {
			t.Fatalf("cold trace invented a delivery receipt or lost the local source: %+v", destination)
		}
	}
	for _, resource := range []struct{ kind, name string }{{"workflow", "action"}, {"skill", "inert"}, {"hook", m.Hooks[0]}, {"mcp", "probe"}, {"plugin", "candidate-probe"}} {
		componentReq := req
		componentReq.ResourceType, componentReq.ResourceName = resource.kind, resource.name
		component, err := RunTrace(context.Background(), eng, profile, componentReq, testRegistry())
		if err != nil || !component.Found || component.Source.NativeBinding != m.NativePlugin.Binding() || len(component.Destinations) != 2 || len(component.Blockers) != 0 {
			t.Fatalf("cold native %s trace: %+v %v", resource.kind, component, err)
		}
		if (resource.kind == "hook" || resource.kind == "mcp" || resource.kind == "plugin") && !component.Destinations[0].Embedded {
			t.Fatal("inline/package trace claimed a standalone resource file")
		}
	}
	planners, err := testRegistry().AsPlanners(req.Harnesses)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := engine.PlanSync(context.Background(), profile, planRequestForTarget(req.TargetSpec, configDir, false, domain.HarnessClaudeCode), planners)
	if err != nil {
		t.Fatal(err)
	}
	action := plan.NativePlugins[0]
	generation, err := nativeGeneration(action)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := nativePayloadPath(action.Package)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(action.ConfigHome, "plugins/cache/candidate-market/candidate-probe/1.0.0")
	for _, dir := range []string{"first", "second"} {
		rel := dir + "/reviewer.md"
		writeFile(t, filepath.Join(action.MarketplaceDir, payload, rel), string(mustRead(t, filepath.Join(pack, "upstream", rel))))
		writeFile(t, filepath.Join(cache, rel), string(mustRead(t, filepath.Join(pack, "upstream", rel))))
	}
	installed := map[string]any{"plugins": map[string]any{m.NativePlugin.Binding(): []any{map[string]any{"scope": "user", "installPath": cache}}}}
	installedBody, err := json.Marshal(installed)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(action.ConfigHome, "plugins/installed_plugins.json"), string(installedBody))
	ledger := domain.NewLedger()
	ledger.NativePlugins = map[string]domain.NativePluginRecord{m.NativePlugin.Binding(): {Generation: generation, Harness: domain.HarnessClaudeCode,
		ConfigHome: action.ConfigHome, MarketplaceDir: action.MarketplaceDir, PayloadPath: payload, CachePath: cache, SettingsPath: action.SettingsPath, SourcePack: "alias"}}
	for _, settings := range append(plan.Settings, plan.MCP...) {
		writeFile(t, settings.Dst, string(settings.Desired))
		ledger.Record(settings.Dst, settings.Desired, settings.SourcePack, settings.Desired, time.Now())
	}
	if err := eng.SaveLedger(plan.Ledger, ledger, false); err != nil {
		t.Fatal(err)
	}
	trace, err = RunTrace(context.Background(), eng, profile, req, testRegistry())
	if err != nil || len(trace.Destinations) != 5 || len(trace.Blockers) != 0 {
		t.Fatalf("installed native trace: %+v %v", trace, err)
	}
	for _, destination := range trace.Destinations {
		if destination.DiffKind != domain.DiffIdentical || destination.PlannedGeneration != generation || destination.DeliveredGeneration != generation || destination.MarketplaceSource != action.MarketplaceDir {
			t.Fatalf("installed trace misclassified candidate: %+v", destination)
		}
	}
	changed := filepath.Join(cache, "second/reviewer.md")
	writeFile(t, changed, "Changed native file.")
	trace, err = RunTrace(context.Background(), eng, profile, req, testRegistry())
	i := slices.IndexFunc(trace.Destinations, func(destination TraceDestination) bool { return destination.Path == changed })
	if err != nil || i < 0 || trace.Destinations[i].DiffKind != domain.DiffConflict || trace.Destinations[i].DeliveredGeneration != generation {
		t.Fatalf("trace trusted a generation over changed source bytes: %+v %v", trace, err)
	}
	writeFile(t, plan.Ledger, "invalid ledger")
	trace, err = RunTrace(context.Background(), eng, profile, req, testRegistry())
	if err != nil || !trace.Found || len(trace.Destinations) != 0 || len(trace.Blockers) == 0 || !strings.Contains(trace.Blockers[0], "corrupt ledger") {
		t.Fatalf("trace invented ownership after a ledger read failure: %+v %v", trace, err)
	}
	cfg.Packs[0].Agents = config.SelectionsToVector(m.Agents, nil)
	profile, _, err = eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.ProfileConfig = cfg
	trace, err = RunTrace(context.Background(), eng, profile, req, testRegistry())
	if err != nil || trace.ProfileState != TraceProfileStateContentExcluded || len(trace.Destinations) != 0 || !slices.Equal(trace.Source.SourcePaths, wantPaths) {
		t.Fatalf("excluded native trace: %+v %v", trace, err)
	}
	no := false
	cfg.Packs[0].Enabled = &no
	req.ProfileConfig = cfg
	req.ResourceType, req.ResourceName = "plugin", "candidate-probe"
	trace, err = RunTrace(context.Background(), eng, domain.Profile{}, req, testRegistry())
	if err != nil || trace.ProfileState != TraceProfileStatePackDisabled || trace.Source == nil || trace.Source.NativeBinding != m.NativePlugin.Binding() || len(trace.Destinations) != 0 {
		t.Fatalf("disabled native plugin trace: %+v %v", trace, err)
	}
}

func TestColocatedPluginCatalogRefresh(t *testing.T) {
	root := t.TempDir()
	const repo = "https://fixture.invalid/market.git"
	const catalogPath = ".claude-plugin/marketplace.json"
	meta := config.InstalledPackMeta{Method: config.MethodClone, Origin: repo, SubPath: "plugins/probe", Ref: fakeHash1, Plugin: &domain.PluginSource{Format: plugin.Claude, Name: "probe", Marketplace: "market", MarketplaceURL: repo, MarketplacePath: catalogPath, MarketplaceRef: fakeHash1, Entry: domain.NativeJSONMap{"name": "probe", "version": "1.0.0"}}}
	writeFile(t, filepath.Join(root, catalogPath), `{"name":"market","plugins":[{"name":"probe","source":"./plugins/probe","version":"2.0.0"}]}`)
	updated, err := refreshPluginCatalog(context.Background(), meta, root, fakeHash2, packUpdateContext{})
	if err != nil || updated.Plugin.Entry["version"] != "2.0.0" || updated.Plugin.MarketplaceRef != fakeHash2 || meta.Plugin.Entry["version"] != "1.0.0" || meta.Ref != fakeHash1 {
		t.Fatalf("same-revision catalog refresh changed original metadata or lost new entry: %+v %v", updated, err)
	}
	for _, bad := range []string{
		`{"name":"market","plugins":[]}`,
		`{"name":"other-market","plugins":[{"name":"probe","source":"./plugins/probe"}]}`,
		`{"name":"market","plugins":[{"name":"probe","source":"./plugins/other"}]}`,
		`{"name":"market","plugins":[{"name":"probe","source":{"source":"url","url":"https://fixture.invalid/other.git"}}]}`,
		`{"name":"market","plugins":[{"name":"probe","source":"../outside"}]}`,
		`invalid JSON`,
	} {
		writeFile(t, filepath.Join(root, catalogPath), bad)
		if _, err := refreshPluginCatalog(context.Background(), meta, root, fakeHash2, packUpdateContext{}); err == nil {
			t.Fatalf("accepted changed identity/source or invalid catalog: %s", bad)
		}
	}
	if err := os.Remove(filepath.Join(root, catalogPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshPluginCatalog(context.Background(), meta, root, fakeHash2, packUpdateContext{}); err == nil {
		t.Fatal("missing recorded catalog was ignored")
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "catalog.json")
		writeFile(t, outside, `{"name":"market","plugins":[{"name":"probe","source":"./plugins/probe"}]}`)
		if err := os.Symlink(outside, filepath.Join(root, catalogPath)); err != nil {
			t.Fatal(err)
		}
		if _, err := refreshPluginCatalog(context.Background(), meta, root, fakeHash2, packUpdateContext{}); err == nil {
			t.Fatal("catalog symlink escaped its acquired revision")
		}
	}
}

func TestSeparatePluginCatalogRefresh(t *testing.T) {
	t.Parallel()
	const repo = "https://fixture.invalid/payload.git"
	var catalog atomic.Value
	var requests atomic.Int32
	setCatalog := func(sourceURL string) {
		catalog.Store(fmt.Sprintf(`{"name":"market","plugins":[{"name":"probe","version":"2.0.0","source":{"source":"url","url":%q}}]}`, sourceURL))
	}
	setCatalog(repo)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, catalog.Load().(string))
	}))
	defer server.Close()
	meta := config.InstalledPackMeta{Method: config.MethodClone, Origin: repo, Ref: "main", CommitHash: fakeHash1, Plugin: &domain.PluginSource{Format: plugin.Claude, Name: "probe", Marketplace: "market", MarketplaceURL: server.URL, Entry: domain.NativeJSONMap{"name": "probe", "version": "1.0.0"}}}
	updated, err := refreshPluginCatalog(context.Background(), meta, "", fakeHash2, packUpdateContext{})
	if err != nil || updated.Plugin.Entry["version"] != "2.0.0" || updated.CommitHash != fakeHash1 || meta.Plugin.Entry["version"] != "1.0.0" {
		t.Fatalf("catalog refresh changed payload revision or original metadata: %+v %v", updated, err)
	}
	pinned := meta
	pinned.Ref = fakeHash1
	before := requests.Load()
	unchanged, err := refreshPluginCatalog(context.Background(), pinned, "", fakeHash1, packUpdateContext{})
	if err != nil || unchanged.Plugin != meta.Plugin || requests.Load() != before {
		t.Fatalf("pinned payload refreshed its separate catalog: %+v %v", unchanged, err)
	}
	setCatalog("https://fixture.invalid/repointed.git")
	if _, err := refreshPluginCatalog(context.Background(), meta, "", fakeHash2, packUpdateContext{}); err == nil {
		t.Fatal("catalog changed the recorded payload source")
	}
}

func TestClaudeNativeCatalogCommandOrder(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for catalog command-order delivery")
	}
	root := t.TempDir()
	configDir, home := filepath.Join(root, "config"), filepath.Join(root, "home")
	catalogPath := filepath.Join(root, "market/.claude-plugin/marketplace.json")
	writeCatalog := func(reverse bool) {
		commands := `"z-first":{"source":"./custom/action.md"},"a-later":{"source":"./custom/action.md"}`
		if reverse {
			commands = `"a-later":{"source":"./custom/action.md"},"z-first":{"source":"./custom/action.md"}`
		}
		writeFile(t, catalogPath, `{"name":"order-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"probe","source":"./plugins/probe","commands":{`+commands+`}},{"name":"sibling","source":"./plugins/sibling"}]}`)
	}
	for _, name := range []string{"probe", "sibling"} {
		writeFile(t, filepath.Join(root, "market/plugins", name, ".claude-plugin/plugin.json"), `{"name":"`+name+`","version":"1.0.0"}`)
	}
	writeFile(t, filepath.Join(root, "market/plugins/probe/custom/action.md"), "---\ndescription: Catalog command order.\n---\nORDERED_COMMAND_BODY\n")
	writeCatalog(false)
	if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: catalogPath}, nil); err != nil {
		t.Fatal(err)
	}
	for _, names := range []struct{ source, alias string }{{"probe", "alias"}, {"sibling", "sibling"}} {
		entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, names.source)
		if err != nil {
			t.Fatal(err)
		}
		if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, names.alias, entry), nil); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}, {Name: "sibling"}}}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "native")}}, Yes: true, Quiet: true}
	resolve := func() domain.Profile {
		t.Helper()
		p, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func() (SyncResult, domain.NativePluginRecord) {
		t.Helper()
		result, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
		if err != nil {
			t.Fatal(err)
		}
		return result, ledger.NativePlugins["probe@order-market"]
	}
	check := func(record domain.NativePluginRecord, want []string) {
		t.Helper()
		entries, err := plugin.CatalogEntries(mustRead(t, nativeRecordCatalogPath(record)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry["name"] != "probe" {
				continue
			}
			raw, err := json.Marshal(entry["commands"])
			if err != nil {
				t.Fatal(err)
			}
			order, err := util.JSONPropertyNames(raw)
			if err != nil || !slices.Equal(order, want) {
				t.Fatalf("delivered catalog order differs: %v want %v: %v", order, want, err)
			}
			return
		}
		t.Fatal("delivered catalog lost the active plugin")
	}
	result, record := run()
	check(record, []string{"z-first", "a-later"})
	data := filepath.Join(record.ConfigHome, "plugins/data/probe-order-market/marker")
	writeFile(t, data, "retained data")
	cfg.Packs[0].Workflows = config.SelectionsToVector([]string{"z-first"}, nil)
	_, excluded := run()
	check(excluded, nil)
	cfg.Packs[0].Workflows = config.VectorSelector{}
	_, record = run()
	check(record, []string{"z-first", "a-later"})
	if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "sibling", Registry: testRegistry()}, nil); err != nil {
		t.Fatal(err)
	}
	check(record, []string{"z-first", "a-later"})
	cfg.Packs = cfg.Packs[:1]
	writeCatalog(true)
	updated, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
	if err != nil || len(updated) != 1 || updated[0].Status != StatusUpdated {
		t.Fatalf("order-only update did not refresh the pack: %+v %v", updated, err)
	}
	result, record = run()
	check(record, []string{"a-later", "z-first"})
	priorCatalog, priorLedger := mustRead(t, nativeRecordCatalogPath(record)), mustRead(t, result.Plan.Ledger)
	eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
	cfg.Packs[0].Workflows = config.SelectionsToVector([]string{"a-later"}, nil)
	if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
		t.Fatal("failed catalog persistence was reported as success")
	}
	if !bytes.Equal(priorCatalog, mustRead(t, nativeRecordCatalogPath(record))) || !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) || string(mustRead(t, data)) != "retained data" {
		t.Fatal("catalog order rollback changed prior state or data")
	}
	eng.FS = engine.OSFS{}
	for _, after := range []bool{false, true} {
		cfg.Packs[0].Workflows = config.VectorSelector{}
		result, record = run()
		eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: after}
		cfg.Packs[0].Workflows = config.SelectionsToVector([]string{"a-later"}, nil)
		interrupted := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered != "native ledger interruption" {
						panic(recovered)
					}
					interrupted = true
				}
			}()
			if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
				t.Fatal(err)
			}
		}()
		eng.FS = engine.OSFS{}
		if !interrupted {
			t.Fatal("catalog-order interruption did not execute")
		}
		unlock, err := lockPackMutation(configDir, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
		want := []string{"a-later", "z-first"}
		if after {
			want = nil
		}
		check(record, want)
		if string(mustRead(t, data)) != "retained data" {
			t.Fatal("interrupted catalog update removed runtime data")
		}
	}
	if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "alias", Registry: testRegistry()}, nil); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, data)) != "retained data" {
		t.Fatal("deletion removed native runtime data")
	}
}

func TestClaudeNativeCatalogSync(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for catalog-backed native delivery")
	}
	for _, rootSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("root-%t", rootSource), func(t *testing.T) { testClaudeNativeCatalogSync(t, rootSource, false, false) })
	}
	t.Run("unnamed-root", func(t *testing.T) { testClaudeNativeCatalogSync(t, true, true, false) })
	t.Run("shared-root", func(t *testing.T) { testClaudeNativeCatalogSync(t, true, false, true) })
}

func testClaudeNativeCatalogSync(t *testing.T, rootSource, unnamed, shared bool) {
	root := t.TempDir()
	t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
	configDir, home := filepath.Join(root, "config"), filepath.Join(root, "home")
	catalog := filepath.Join(root, "market/.claude-plugin/marketplace.json")
	source := "./plugins/probe"
	fields := ""
	if unnamed {
		source = "./"
		writeFile(t, filepath.Join(root, "market/SKILL.md"), "---\ndescription: Native unnamed local root fixture.\n---\nReturn the marker.\n")
	} else if rootSource {
		source, fields = "./", `,"skills":["./selected"]`
		writeFile(t, filepath.Join(root, "market/skills/inert/SKILL.md"), "---\nname: inert\ndescription: Inert native default fixture.\n---\nRetained source asset.\n")
		writeFile(t, filepath.Join(root, "market/selected/SKILL.md"), "---\nname: selected\ndescription: Native selected fixture.\n---\nReturn the marker.\n")
	} else {
		writeFile(t, filepath.Join(root, "market/plugins/probe/skills/probe/SKILL.md"), "---\nname: probe\ndescription: Native catalog fixture.\n---\nReturn the marker.\n")
	}
	if !shared && !unnamed {
		fields += `,"commands":["./custom/action.md","./custom/action.md","./alternative/action.md","./commands/default.md"],"agents":["./custom/reviewer.md","./custom/reviewer.md","./alternative/reviewer.md","./agents/default.md"]`
		payload := filepath.Join(root, "market", source)
		writeFile(t, filepath.Join(payload, "commands/default.md"), "---\ndescription: Native default command.\n---\nDEFAULT_COMMAND\n")
		writeFile(t, filepath.Join(payload, "agents/default.md"), "---\nname: Default-Agent\ndescription: Native default agent.\n---\nDEFAULT_AGENT\n")
		writeFile(t, filepath.Join(payload, "commands/operations/action.md"), "---\nname: ignored\ndescription: Native nested command.\n---\nNESTED_COMMAND\n")
		writeFile(t, filepath.Join(payload, "agents/operations/reviewer.md"), "---\nname: Nested-Reviewer\ndescription: Native nested agent.\n---\nNESTED_AGENT\n")
		writeFile(t, filepath.Join(payload, "custom/action.md"), "---\ndescription: Native catalog command.\n---\nCATALOG_COMMAND\n")
		writeFile(t, filepath.Join(payload, "custom/reviewer.md"), "---\nname: Catalog-Reviewer\ndescription: Native catalog agent.\n---\nCATALOG_AGENT\n")
		writeFile(t, filepath.Join(payload, "alternative/action.md"), "---\ndescription: Alternate native command.\n---\nALTERNATE_COMMAND\n")
		writeFile(t, filepath.Join(payload, "alternative/reviewer.md"), "---\nname: Catalog-Reviewer\ndescription: Alternate native agent.\n---\nALTERNATE_AGENT\n")
	}
	header := `"owner":{"name":"shrug-labs"},"metadata":{"revision":18446744073709551617},"retired":true`
	includeSibling := shared
	writeCatalog := func(version, marker string) {
		sibling := ""
		if includeSibling {
			sibling = `,{"name":"shared-probe","source":"./","version":"1.0.0","skills":["./shared"]}`
		}
		writeFile(t, catalog, `{"name":"catalog-market",`+header+`,"plugins":[{"name":"catalog-probe","source":"`+source+`","version":"`+version+`"`+fields+`,"strict":false,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo `+marker+`"}]}]}}`+sibling+`]}`)
	}
	if shared {
		writeFile(t, filepath.Join(root, "market/shared/SKILL.md"), "---\nname: shared\ndescription: Native shared-root fixture.\n---\nRetain the sibling skill.\n")
		writeFile(t, filepath.Join(root, "market/commands/ops/shared.md"), "---\ndescription: Shared default command.\n---\nSHARED_COMMAND\n")
		writeFile(t, filepath.Join(root, "market/agents/ops/shared.md"), "---\nname: Shared-Agent\ndescription: Shared default agent.\n---\nSHARED_AGENT\n")
		if err := os.Chmod(filepath.Join(root, "market/agents/ops/shared.md"), 0o440); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog("1.0.0", "original")
	if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: catalog}, nil); err != nil {
		t.Fatal(err)
	}
	entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "catalog-probe")
	if err != nil {
		t.Fatal(err)
	}
	if unnamed {
		inspection, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: configDir, Input: "catalog-probe"})
		if err != nil || !slices.Equal(inspection.Skills, []string{"market"}) {
			t.Fatalf("local root inspection lost source directory identity: %+v %v", inspection, err)
		}
	}
	if !shared && !unnamed {
		inspection, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: configDir, Input: "catalog-probe"})
		if err != nil || !slices.Equal(inspection.Workflows, []string{"action", "default", "operations:action"}) || !slices.Equal(inspection.Agents, []string{"Catalog-Reviewer", "Default-Agent", "operations:Nested-Reviewer"}) {
			t.Fatalf("named inspection lost native component identities: %+v %v", inspection, err)
		}
		db, err := index.Open(filepath.Join(configDir, "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		results, err := db.Search("CATALOG_COMMAND", index.SearchFilters{Kind: "workflow", Status: "inspected"})
		if err != nil || len(results) != 1 || results[0].Name != "action" {
			t.Fatalf("inspection indexed a shadowed native command: %+v %v", results, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "alias", entry), nil); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
	if shared {
		sibling, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "shared-probe")
		if err != nil {
			t.Fatal(err)
		}
		if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "sibling", sibling), nil); err != nil {
			t.Fatal(err)
		}
		cfg.Packs = append(cfg.Packs, config.PackEntry{Name: "sibling"})
	}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "native")}}, Yes: true, Quiet: true}
	ledgerPath := ""
	run := func() domain.NativePluginRecord {
		t.Helper()
		profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ledgerPath = result.Plan.Ledger
		ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
		if err != nil {
			t.Fatal(err)
		}
		if shared {
			for _, pack := range profile.Packs {
				s := pack.NativePlugin
				owner := ledger.NativePlugins[s.Package.Binding()]
				entries, err := plugin.CatalogEntries(mustRead(t, filepath.Join(owner.MarketplaceDir, ".claude-plugin/marketplace.json")))
				if err != nil {
					t.Fatal(err)
				}
				i := slices.IndexFunc(entries, func(entry map[string]any) bool { return entry["name"] == s.Package.Name })
				if i < 0 {
					t.Fatal("shared default lifecycle lost its selected catalog entry")
				}
				m, err := plugin.ReadClaude(owner.CachePath, domain.PluginSource{Marketplace: s.Package.Marketplace, MarketplaceURL: owner.MarketplaceDir, Entry: entries[i]})
				if err != nil {
					t.Fatal(err)
				}
				for _, cat := range []domain.PackCategory{domain.CategoryWorkflows, domain.CategoryAgents} {
					got, want := slices.Sorted(maps.Keys(m.NativePlugin.Components[cat])), slices.Sorted(slices.Values(s.Selected[cat]))
					if !slices.Equal(got, want) {
						t.Fatalf("shared native cache changed %s selection: got %v, want %v", s.Package.Name, got, want)
					}
				}
				if !bytes.Contains(mustRead(t, filepath.Join(s.Root, "upstream/agents/ops/shared.md")), []byte("name: Shared-Agent")) {
					t.Fatal("shared delivery changed installed agent metadata")
				}
			}
		}
		if !shared && !unnamed {
			show, err := PackShow(configDir, "alias")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(show.Path, "upstream/custom/action.md")
			if show.ContentPath(domain.CategoryWorkflows, "action") != path || !bytes.Contains(mustRead(t, path), []byte("CATALOG_COMMAND")) {
				t.Fatal("show resolved a shadowed native command")
			}
			resources := indexManifestContent("alias", show.manifest, show.Path, nil)
			i := slices.IndexFunc(resources, func(resource index.Resource) bool { return resource.Kind == "workflow" && resource.Name == "action" })
			if i < 0 || resources[i].Path != path || !strings.Contains(resources[i].Body, "CATALOG_COMMAND") {
				t.Fatal("installed search resolved a shadowed native command")
			}
			if show.NativePlugin == nil || len(show.ContentPaths(domain.CategoryAgents, "Catalog-Reviewer")) != 2 {
				t.Fatal("native lifecycle lost agent candidate provenance")
			}
			for _, candidate := range []struct{ dir, marker string }{{"custom", "CATALOG_AGENT"}, {"alternative", "ALTERNATE_AGENT"}} {
				matches, err := RunIndexSearch(IndexSearchRequest{ConfigDir: configDir, Terms: candidate.marker, Kind: "agent", Pack: "alias", Status: "installed"})
				if err != nil || len(matches) != 1 || matches[0].Name != "Catalog-Reviewer" || matches[0].Path != filepath.Join(show.Path, "upstream", candidate.dir, "reviewer.md") {
					t.Fatalf("native lifecycle lost a searchable agent source: %+v %v", matches, err)
				}
			}
			traced, err := RunTrace(context.Background(), eng, profile, TraceRequest{TargetSpec: req.TargetSpec, ProfileName: "default", ProfileConfig: cfg, ResourceType: "agent", ResourceName: "Catalog-Reviewer"}, testRegistry())
			if err != nil || !traced.Found || len(traced.Source.SourcePaths) != 2 {
				t.Fatalf("native lifecycle trace lost candidate sources: %+v %v", traced, err)
			}
			if slices.Contains(profile.Packs[0].NativePlugin.Selected[domain.CategoryAgents], "Catalog-Reviewer") {
				if len(traced.Destinations) != 5 || traced.ProfileState != TraceProfileStateActive || len(traced.Blockers) != 0 {
					t.Fatalf("active native trace lost source/cache paths: %+v", traced)
				}
				for _, destination := range traced.Destinations {
					if destination.DiffKind != domain.DiffIdentical || !util.PathExists(destination.Path) {
						t.Fatalf("native trace misclassified installed candidate: %+v", destination)
					}
				}
			} else if traced.ProfileState != TraceProfileStateContentExcluded || len(traced.Destinations) != 0 {
				t.Fatalf("excluded native trace retained a destination: %+v", traced)
			}
		}
		return ledger.NativePlugins["catalog-probe@catalog-market"]
	}
	record := run()
	if !shared && !unnamed {
		m, err := config.LoadPackManifest(filepath.Join(configDir, "packs/alias/pack.json"))
		if err != nil || !slices.Equal(m.Workflows, []string{"action", "default", "operations:action"}) || !slices.Equal(m.Agents, []string{"Catalog-Reviewer", "Default-Agent", "operations:Nested-Reviewer"}) {
			t.Fatalf("named install lost default/catalog inventory: %+v %v", m, err)
		}
		if len(m.NativePlugin.Components[domain.CategoryWorkflows]["action"]) != 2 || len(m.NativePlugin.Components[domain.CategoryAgents]["Catalog-Reviewer"]) != 2 {
			t.Fatal("named install lost repeated/colliding source mappings")
		}
		cfg.Packs[0].Workflows = config.SelectionsToVector(m.Workflows, nil)
		cfg.Packs[0].Agents = config.SelectionsToVector(m.Agents, nil)
		excluded := run()
		for _, rel := range []string{"commands/default.md", "commands/operations/action.md", "agents/default.md", "agents/operations/reviewer.md"} {
			if util.PathExists(filepath.Join(excluded.CachePath, rel)) || util.PathExists(filepath.Join(excluded.MarketplaceDir, source, rel)) || !util.PathExists(filepath.Join(configDir, "packs/alias/upstream", rel)) {
				t.Fatalf("default component exclusion lost source or retained activation: %s", rel)
			}
		}
		cfg.Packs[0].Workflows, cfg.Packs[0].Agents = config.VectorSelector{}, config.VectorSelector{}
		reenabled := run()
		if reenabled.Generation != record.Generation || !util.PathExists(filepath.Join(reenabled.CachePath, "commands/default.md")) || !util.PathExists(filepath.Join(reenabled.CachePath, "agents/default.md")) {
			t.Fatal("default command/agent re-enable lost native delivery")
		}
		record = reenabled
	}
	if shared {
		if record.SharedRoot == "" || !bytes.Equal(mustRead(t, catalog), mustRead(t, filepath.Join(record.MarketplaceDir, ".claude-plugin/marketplace.json"))) {
			t.Fatal("all-selected shared-root delivery changed canonical catalog bytes or lost ownership")
		}
		for _, enabled := range []int{0, 1, -1, 2} {
			for i := range cfg.Packs {
				cfg.Packs[i].Workflows, cfg.Packs[i].Agents = config.VectorSelector{}, config.VectorSelector{}
				if enabled != 2 && enabled != i {
					cfg.Packs[i].Workflows = config.SelectionsToVector([]string{"ops:shared"}, nil)
					cfg.Packs[i].Agents = config.SelectionsToVector([]string{"ops:Shared-Agent"}, nil)
				}
			}
			selected := run()
			if enabled == 1 {
				profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				traceReq := TraceRequest{TargetSpec: req.TargetSpec, ProfileName: "default", ProfileConfig: cfg, ResourceType: "agent", ResourceName: "ops:Shared-Agent", PackName: "sibling"}
				traced, err := RunTrace(context.Background(), eng, profile, traceReq, testRegistry())
				if err != nil || !traced.Found || traced.ProfileState != TraceProfileStateActive || len(traced.Blockers) != 0 || len(traced.Source.SourcePaths) != 1 || len(traced.Destinations) != 5 {
					t.Fatalf("shared selected agent lost original provenance or generated delivery paths: %+v %v", traced, err)
				}
				for _, destination := range traced.Destinations {
					if destination.DiffKind != domain.DiffIdentical {
						t.Fatalf("shared selected agent trace disagrees with native delivery: %+v", destination)
					}
				}
				backing := slices.IndexFunc(traced.Destinations, func(destination TraceDestination) bool {
					return destination.Location == "cache" && strings.HasSuffix(destination.Path, ".source")
				})
				if backing < 0 {
					t.Fatal("shared agent trace omitted its generated backing body")
				}
				path := traced.Destinations[backing].Path
				body := mustRead(t, path)
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, "changed generated agent body\n")
				changed, err := RunTrace(context.Background(), eng, profile, traceReq, testRegistry())
				if err != nil || !slices.ContainsFunc(changed.Destinations, func(destination TraceDestination) bool {
					return destination.Path == path && destination.DiffKind == domain.DiffConflict
				}) {
					t.Fatalf("unchanged generation hid a changed shared agent body: %+v %v", changed, err)
				}
				writeFile(t, path, string(body))
				if err := os.Chmod(path, 0o440); err != nil {
					t.Fatal(err)
				}
			}
			if enabled == 2 && selected.Generation != record.Generation {
				t.Fatal("shared default re-enable changed original generation")
			}
		}
		for _, index := range []int{0, 1} {
			cfg.Packs[index].Skills = config.SelectionsToVector([]string{[]string{"selected", "shared"}[index]}, nil)
			excluded := run()
			fields, err := readNativeConfig(filepath.Join(excluded.MarketplaceDir, ".claude-plugin/marketplace.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range fields["plugins"].([]any) {
				entry := raw.(map[string]any)
				if (entry["name"] == "catalog-probe" || (index == 1 && entry["name"] == "shared-probe")) && !strings.Contains(fmt.Sprint(entry["skills"]), "empty-skills") {
					t.Fatal("shared-root exclusion lost its native fallback suppression")
				}
			}
			if !util.PathExists(filepath.Join(excluded.MarketplaceDir, "selected/SKILL.md")) || !util.PathExists(filepath.Join(excluded.MarketplaceDir, "shared/SKILL.md")) {
				t.Fatal("shared-root exclusion deleted another entry's retained source assets")
			}
		}
		cfg.Packs[0].Skills, cfg.Packs[1].Skills = config.VectorSelector{}, config.VectorSelector{}
		record = run()
	}
	if unnamed {
		if record.RootDirectoryName != "market" || filepath.Base(record.MarketplaceDir) != "market" || !bytes.Equal(mustRead(t, filepath.Join(root, "market/SKILL.md")), mustRead(t, filepath.Join(record.CachePath, "SKILL.md"))) {
			t.Fatal("native delivery lost the local root identity or changed the unnamed skill bytes")
		}
		cfg.Packs[0].Skills = config.SelectionsToVector([]string{"market"}, nil)
		excludedSkill := run()
		if util.PathExists(filepath.Join(excludedSkill.CachePath, "SKILL.md")) {
			t.Fatal("local root skill exclusion did not reach native cache")
		}
		cfg.Packs[0].Skills = config.VectorSelector{}
		record = run()
	}
	if util.PathExists(filepath.Join(record.CachePath, ".claude-plugin/plugin.json")) {
		t.Fatal("catalog-backed native delivery fabricated a payload manifest")
	}
	data := filepath.Join(record.ConfigHome, "plugins/data/catalog-probe-catalog-market/marker")
	writeFile(t, data, "retained")
	siblingData := filepath.Join(record.ConfigHome, "plugins/data/shared-probe-catalog-market/marker")
	if shared {
		writeFile(t, siblingData, "sibling retained")
		before, err := plugin.ReadFiles(record.MarketplaceDir)
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(cfg.Packs)
		reordered := run()
		after, err := plugin.ReadFiles(record.MarketplaceDir)
		if err != nil || reordered.Generation != record.Generation || !reflect.DeepEqual(before, after) {
			t.Fatalf("profile reordering changed shared-root source generation: %v", err)
		}
		slices.Reverse(cfg.Packs)
		both := slices.Clone(cfg.Packs)
		localReq := req
		localReq.Scope, localReq.ProjectDir = domain.ScopeProject, filepath.Join(root, "project")
		if err := os.MkdirAll(localReq.ProjectDir, 0o700); err != nil {
			t.Fatal(err)
		}
		profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		localResult, _, err := RunSync(context.Background(), eng, profile, localReq, testRegistry(), nil, nil)
		if err != nil {
			t.Fatalf("matching shared-root local scope: %v", err)
		}
		beforeView, err := plugin.ReadFiles(record.MarketplaceDir)
		if err != nil {
			t.Fatal(err)
		}
		beforeGlobal, beforeLocal := mustRead(t, ledgerPath), mustRead(t, localResult.Plan.Ledger)
		cfg.Packs = []config.PackEntry{both[0]}
		profile, _, err = eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil); err == nil {
			t.Fatal("single-entry transition replaced another scope's shared root")
		}
		view, err := plugin.ReadFiles(record.MarketplaceDir)
		if err != nil || !reflect.DeepEqual(beforeView, view) || !bytes.Equal(beforeGlobal, mustRead(t, ledgerPath)) || !bytes.Equal(beforeLocal, mustRead(t, localResult.Plan.Ledger)) {
			t.Fatalf("rejected shared-root transition changed owned state: %v", err)
		}
		if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: localReq.TargetSpec, Yes: true}, testRegistry()); err != nil {
			t.Fatal(err)
		}
		global, _, err := eng.LoadLedger(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		for binding, owner := range global.NativePlugins {
			installed, err := nativePluginInstalled(owner, binding)
			if err != nil || !installed || !util.PathExists(owner.CachePath) {
				t.Fatalf("cleaning local shared-root scope changed global installation: %s %v", binding, err)
			}
		}
		for _, index := range []int{0, 1} {
			cfg.Packs = []config.PackEntry{both[index]}
			run()
			ledger, _, err := eng.LoadLedger(ledgerPath)
			if err != nil || len(ledger.NativePlugins) != 1 {
				t.Fatalf("shared-root scope transition lost its surviving entry: %+v %v", ledger.NativePlugins, err)
			}
			binding := []string{"catalog-probe@catalog-market", "shared-probe@catalog-market"}[index]
			survivor := ledger.NativePlugins[binding]
			if survivor.SharedRoot != "" || !util.PathExists(survivor.CachePath) || !util.PathExists(filepath.Join(survivor.MarketplaceDir, []string{"selected", "shared"}[index], "SKILL.md")) || string(mustRead(t, data)) != "retained" || string(mustRead(t, siblingData)) != "sibling retained" {
				t.Fatal("shared-root scope transition lost its source, cache, ownership or data")
			}
			run()
			cfg.Packs = slices.Clone(both)
			record = run()
			if record.SharedRoot == "" {
				t.Fatal("re-enabling a shared-root sibling lost joint ownership")
			}
		}
	}
	exclude := []string{"claude-stop"}
	if shared {
		cfg.Packs[0].Workflows = config.SelectionsToVector([]string{"ops:shared"}, nil)
		cfg.Packs[0].Agents = config.SelectionsToVector([]string{"ops:Shared-Agent"}, nil)
	}
	cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
	excluded := run()
	renderedCatalog := nativeCatalogPath(domain.HarnessClaudeCode, excluded.MarketplaceDir)
	if bytes.Contains(mustRead(t, renderedCatalog), []byte(`"Stop"`)) || excluded.Generation == record.Generation {
		t.Fatal("catalog-only hook selection did not refresh native delivery")
	}
	if rootSource {
		phases := []string{"failure", "before-ledger"}
		if shared {
			phases = append(phases, "after-ledger", "membership-failure", "membership-before-ledger", "membership-after-ledger")
		}
		for _, phase := range phases {
			membership := strings.HasPrefix(phase, "membership-")
			phase = strings.TrimPrefix(phase, "membership-")
			interrupted := phase != "failure"
			beforeView, err := plugin.ReadFiles(excluded.MarketplaceDir)
			if err != nil {
				t.Fatal(err)
			}
			beforeCache, err := plugin.ReadFiles(excluded.CachePath)
			if err != nil {
				t.Fatal(err)
			}
			beforeLedger := mustRead(t, ledgerPath)
			packs := slices.Clone(cfg.Packs)
			if membership {
				cfg.Packs = cfg.Packs[:1]
			} else {
				cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
				if shared {
					cfg.Packs[0].Workflows, cfg.Packs[0].Agents = config.VectorSelector{}, config.VectorSelector{}
				}
			}
			if interrupted {
				eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: ledgerPath, After: phase == "after-ledger"}
			} else {
				eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: ledgerPath}
			}
			profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			lost := false
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						if !interrupted || recovered != "native ledger interruption" {
							panic(recovered)
						}
						lost = true
					}
				}()
				if _, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil); err == nil || interrupted {
					t.Fatalf("root native persistence injection did not run: interrupted=%t error=%v", interrupted, err)
				}
			}()
			if interrupted && !lost {
				t.Fatal("root native interruption injection did not run")
			}
			eng.FS = engine.OSFS{}
			cfg.Packs = packs
			cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
			if err := recoverNativeOperation(configDir); err != nil {
				t.Fatal(err)
			}
			view, viewErr := plugin.ReadFiles(excluded.MarketplaceDir)
			cache, cacheErr := plugin.ReadFiles(excluded.CachePath)
			if phase == "after-ledger" {
				ledger, _, err := eng.LoadLedger(ledgerPath)
				first, second := ledger.NativePlugins["catalog-probe@catalog-market"], ledger.NativePlugins["shared-probe@catalog-market"]
				valid := first.SharedRoot != "" && first.SharedRoot == second.SharedRoot && bytes.Contains(mustRead(t, renderedCatalog), []byte(`"Stop"`))
				if membership {
					valid = len(ledger.NativePlugins) == 1 && first.SharedRoot == "" && !bytes.Contains(mustRead(t, renderedCatalog), []byte(`"Stop"`))
				}
				if err != nil || !valid || first.Generation == excluded.Generation {
					t.Fatalf("committed shared-root recovery lost its selected generation: %v", err)
				}
			} else if viewErr != nil || cacheErr != nil || !reflect.DeepEqual(beforeView, view) || !reflect.DeepEqual(beforeCache, cache) || !bytes.Equal(beforeLedger, mustRead(t, ledgerPath)) || string(mustRead(t, data)) != "retained" {
				t.Fatalf("root catalog rollback/recovery changed prior state (interrupted=%t): view=%t cache=%t ledger=%t data=%t errors=%v %v", interrupted, reflect.DeepEqual(beforeView, view), reflect.DeepEqual(beforeCache, cache), bytes.Equal(beforeLedger, mustRead(t, ledgerPath)), string(mustRead(t, data)) == "retained", viewErr, cacheErr)
			}
			if shared && (string(mustRead(t, siblingData)) != "sibling retained" || string(mustRead(t, data)) != "retained") {
				t.Fatal("shared-root recovery changed native runtime data")
			}
			excluded = run()
		}
	}
	header = `"owner":{"name":"updated owner"},"metadata":{"revision":18446744073709551618}`
	if shared {
		writeFile(t, filepath.Join(root, "market/commands/ops/shared.md"), "---\ndescription: Shared default command.\n---\nSHARED_COMMAND_UPDATED\n")
		path := filepath.Join(root, "market/agents/ops/shared.md")
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, "---\nname: Shared-Agent\ndescription: Shared default agent.\n---\nSHARED_AGENT_UPDATED\n")
		if err := os.Chmod(path, 0o440); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog("2.0.0", "updated")
	updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
	if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
		t.Fatalf("catalog source update: %+v %v", updates, err)
	}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
	if shared {
		beforeView, err := plugin.ReadFiles(record.MarketplaceDir)
		if err != nil {
			t.Fatal(err)
		}
		beforeCache, err := plugin.ReadFiles(excluded.CachePath)
		if err != nil {
			t.Fatal(err)
		}
		beforeLedger := mustRead(t, ledgerPath)
		profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "conflicting marketplace metadata") {
			t.Fatalf("mixed shared catalog metadata was silently delivered: %v", err)
		}
		view, viewErr := plugin.ReadFiles(record.MarketplaceDir)
		cache, cacheErr := plugin.ReadFiles(excluded.CachePath)
		if viewErr != nil || cacheErr != nil || !reflect.DeepEqual(beforeView, view) || !reflect.DeepEqual(beforeCache, cache) || !bytes.Equal(beforeLedger, mustRead(t, ledgerPath)) {
			t.Fatalf("rejected mixed shared catalog changed native state: %v %v", viewErr, cacheErr)
		}
		updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "sibling"}, nil, nil)
		if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
			t.Fatalf("shared catalog sibling update: %+v %v", updates, err)
		}
	}
	updated := run()
	if shared && (!bytes.Contains(mustRead(t, filepath.Join(updated.CachePath, "commands/ops/shared.md")), []byte("SHARED_COMMAND_UPDATED")) || !bytes.Contains(mustRead(t, filepath.Join(updated.CachePath, "agents/ops/shared.md.aipack-agent-shared-probe.source")), []byte("SHARED_AGENT_UPDATED"))) {
		t.Fatal("shared default source update did not reach selected native backing bodies")
	}
	if updated.Generation == excluded.Generation || updated.CachePath == excluded.CachePath || !bytes.Contains(mustRead(t, renderedCatalog), []byte("echo updated")) || string(mustRead(t, data)) != "retained" {
		t.Fatal("updated catalog version/hooks did not reach native cache with data retained")
	}
	updatedCatalog, err := readNativeConfig(renderedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	sourceCatalog, err := readNativeConfig(catalog)
	delete(updatedCatalog, "plugins")
	delete(sourceCatalog, "plugins")
	if err != nil || !reflect.DeepEqual(sourceCatalog, updatedCatalog) {
		t.Fatalf("catalog refresh retained stale metadata or rounded a number: %+v %v", updatedCatalog, err)
	}
	if rootSource && (updated.PayloadPath != "." || (!shared && !bytes.Equal(mustRead(t, catalog), mustRead(t, filepath.Join(updated.CachePath, ".claude-plugin/marketplace.json")))) || (!unnamed && !util.PathExists(filepath.Join(updated.CachePath, "skills/inert/SKILL.md")))) {
		t.Fatal("root delivery changed original catalog bytes or removed an inert source entrypoint")
	}
	if shared {
		includeSibling = false
		writeCatalog("3.0.0", "partial")
		updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
		if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
			t.Fatalf("partial catalog membership update: %+v %v", updates, err)
		}
		slices.Reverse(cfg.Packs)
		updated = run()
		canonical, err := readNativeConfig(filepath.Join(updated.MarketplaceDir, ".claude-plugin/marketplace.json"))
		if err != nil {
			t.Fatal(err)
		}
		entries := canonical["plugins"].([]any)
		if len(entries) != 2 || entries[0].(map[string]any)["name"] != "catalog-probe" || entries[0].(map[string]any)["version"] != "3.0.0" || entries[1].(map[string]any)["name"] != "shared-probe" || entries[1].(map[string]any)["version"] != "1.0.0" {
			t.Fatalf("partial update lost an active sibling declaration: %+v", entries)
		}
		ledger, _, err := eng.LoadLedger(ledgerPath)
		sibling := ledger.NativePlugins["shared-probe@catalog-market"]
		installed, installErr := nativePluginInstalled(sibling, "shared-probe@catalog-market")
		if err != nil || installErr != nil || !installed || !util.PathExists(sibling.CachePath) || string(mustRead(t, data)) != "retained" || string(mustRead(t, siblingData)) != "sibling retained" {
			t.Fatalf("partial catalog update lost sibling activation/cache/data: %v %v", err, installErr)
		}
		slices.Reverse(cfg.Packs)
		reordered := run()
		if reordered.Generation != updated.Generation {
			t.Fatal("profile order changed a partially updated shared-root generation")
		}
	}
	if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "alias"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if shared {
		if string(mustRead(t, data)) != "retained" || string(mustRead(t, siblingData)) != "sibling retained" || !util.PathExists(filepath.Join(updated.MarketplaceDir, "shared/SKILL.md")) {
			t.Fatal("shared-root deletion lost a sibling or runtime data")
		}
		cfg.Packs = cfg.Packs[1:]
		run()
		ledger, _, err := eng.LoadLedger(ledgerPath)
		if err != nil || len(ledger.NativePlugins) != 1 || ledger.NativePlugins["shared-probe@catalog-market"].SharedRoot != "" {
			t.Fatalf("sync after shared-root deletion retained stale joint ownership: %+v %v", ledger.NativePlugins, err)
		}
		if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "sibling"}, io.Discard); err != nil {
			t.Fatal(err)
		}
		if string(mustRead(t, siblingData)) != "sibling retained" {
			t.Fatal("last shared-root deletion discarded native runtime data")
		}
	}
	if string(mustRead(t, data)) != "retained" || util.PathExists(filepath.Join(updated.MarketplaceDir, updated.PayloadPath)) {
		t.Fatal("catalog deletion lost runtime data or left its owned payload")
	}
}

func readOnlyNativeAsset(t *testing.T, source string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	dir := filepath.Join(source, "read-only")
	writeFile(t, filepath.Join(dir, "asset.txt"), "retained native asset")
	if err := os.Chmod(dir, 0o550); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func TestClaudeNativeSetupParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native setup parity")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		if req.URL.Path != "/v1/messages" {
			http.NotFound(w, req)
			return
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"AIPACK_RESPONSE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			var value struct{ Type string }
			_ = json.Unmarshal([]byte(event), &value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value.Type, event)
		}
	}))
	defer api.Close()
	for _, scope := range []domain.Scope{domain.ScopeGlobal, domain.ScopeProject} {
		t.Run(string(scope), func(t *testing.T) {
			var baseline []byte
			for _, converted := range []bool{false, true} {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				source, cfgDir, home, nativeHome := filepath.Join(root, "market/plugins/probe"), filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "native")
				if err := os.MkdirAll(home, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"setup-probe","version":"1.0.0"}`)
				writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"Setup":[{"matcher":"init|maintenance","hooks":[{"type":"command","command":"python3 \"${CLAUDE_PLUGIN_ROOT}/setup.py\"","timeout":3}]}],"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"python3 \"${CLAUDE_PLUGIN_ROOT}/setup.py\"","timeout":3}]}]}}`)
				writeFile(t, filepath.Join(source, "setup.py"), `import json, os, pathlib, sys
request = json.load(sys.stdin)
data = pathlib.Path(os.environ["CLAUDE_PLUGIN_DATA"])
data.mkdir(parents=True, exist_ok=True)
event = request["hook_event_name"]
with (data / "events.jsonl").open("a") as log:
    log.write(json.dumps({"event": event, "trigger": request.get("trigger"), "source": request.get("source"), "cwd": os.getcwd(), "input_cwd": request["cwd"], "root": os.environ["CLAUDE_PLUGIN_ROOT"], "data": str(data), "env_file": bool(os.environ.get("CLAUDE_ENV_FILE"))}) + "\n")
if event == "Setup":
    print(json.dumps({"continue": False, "stopReason": "AIPACK_SETUP_BLOCK", "hookSpecificOutput": {"hookEventName": "Setup", "additionalContext": "AIPACK_SETUP_DISCARDED"}}))
    print("AIPACK_EXPLICIT_SETUP_ERROR", file=sys.stderr)
    sys.exit(2)
ready = data / "ready"
if ready.exists():
    print("AIPACK_SETUP_REUSED")
else:
    with (data / "attempts").open("a") as attempts: attempts.write("attempt\n")
    fail = data / "fail-once"
    if fail.exists():
        fail.unlink()
        print("AIPACK_SETUP_FAILED", file=sys.stderr)
        sys.exit(1)
    ready.write_text("runtime-v1")
    print("AIPACK_SETUP_READY")
`)
				catalog := filepath.Join(root, "market/.claude-plugin/marketplace.json")
				writeFile(t, catalog, `{"name":"setup-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"setup-probe","source":"./plugins/probe"}]}`)
				settings := filepath.Join(nativeHome, "settings.json")
				if scope == domain.ScopeProject {
					settings = filepath.Join(root, ".claude/settings.local.json")
				}
				record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: nativeHome, Home: home, SettingsPath: settings, MarketplaceDir: filepath.Dir(filepath.Dir(catalog))}
				const binding = "setup-probe@setup-market"
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: scope, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}
				eng := engine.New(nil, nil)
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				data := filepath.Join(nativeHome, "plugins/data/setup-probe-setup-market")
				if converted {
					if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: cfgDir, URL: catalog}, nil); err != nil {
						t.Fatal(err)
					}
					entry, err := RegistryLookup(RegistryListRequest{ConfigDir: cfgDir}, "setup-probe")
					if err != nil {
						t.Fatal(err)
					}
					if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(cfgDir, "probe", entry), nil); err != nil {
						t.Fatal(err)
					}
					dry := req
					dry.DryRun = true
					if _, _, err := RunSync(context.Background(), eng, resolve(), dry, testRegistry(), nil, nil); err != nil {
						t.Fatal(err)
					}
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
						t.Fatal(err)
					}
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := addNativeMarketplace(context.Background(), home, record); err != nil {
						t.Fatal(err)
					}
					if _, err := installNativePlugin(context.Background(), home, record, binding); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := runNativePlugin(context.Background(), home, record, "list", "--json"); err != nil {
					t.Fatal(err)
				}
				logPath := filepath.Join(data, "events.jsonl")
				if _, err := os.Stat(logPath); !os.IsNotExist(err) {
					t.Fatal("install/read/dry-run/no-op executed a setup hook")
				}
				writeFile(t, filepath.Join(data, "fail-once"), "synthetic failure")
				run := func(flags ...string) ([]byte, []byte) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					args := []string{"--model", "claude-sonnet-4-5", "--tools", "", "--no-session-persistence"}
					args = append(args, flags...)
					cmd := exec.CommandContext(ctx, binary, args...)
					cmd.WaitDelay, cmd.Dir = time.Second, root
					cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + nativeHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
					mu.Lock()
					requests = nil
					mu.Unlock()
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("native setup (converted=%t flags=%v): %v\n%s", converted, flags, err, out)
					}
					mu.Lock()
					body := bytes.Join(requests, nil)
					mu.Unlock()
					return out, body
				}
				prompt := []string{"--print", "--output-format", "stream-json", "--verbose", "AIPACK_PROMPT"}
				out, _ := run(prompt...)
				if !bytes.Contains(out, []byte("AIPACK_SETUP_FAILED")) {
					t.Fatal("first-use setup failure was not reported")
				}
				ready := filepath.Join(data, "ready")
				if _, err := os.Stat(ready); !os.IsNotExist(err) {
					t.Fatal("failed setup reported readiness")
				}
				_, body := run(prompt...)
				if !bytes.Contains(body, []byte("AIPACK_SETUP_READY")) || string(mustRead(t, ready)) != "runtime-v1" {
					t.Fatal("setup retry did not complete")
				}
				readyBefore, err := os.Stat(ready)
				if err != nil {
					t.Fatal(err)
				}
				_, body = run(prompt...)
				if !bytes.Contains(body, []byte("AIPACK_SETUP_REUSED")) || string(mustRead(t, filepath.Join(data, "attempts"))) != "attempt\nattempt\n" {
					t.Fatal("warm restart repeated setup")
				}
				_, body = run("--init-only")
				if len(body) != 0 {
					t.Fatal("init-only started a model conversation")
				}
				for _, flag := range []string{"--init", "--maintenance"} {
					out, body = run(append([]string{flag}, prompt...)...)
					if !bytes.Contains(out, []byte("AIPACK_EXPLICIT_SETUP_ERROR")) || !bytes.Contains(body, []byte("AIPACK_PROMPT")) || bytes.Contains(body, []byte("AIPACK_SETUP_DISCARDED")) || bytes.Contains(body, []byte("AIPACK_SETUP_BLOCK")) {
						t.Fatalf("explicit Setup failure/output semantics changed: %s", out)
					}
				}
				log := mustRead(t, logPath)
				var normalized []map[string]any
				var dispatch []string
				for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
					var event map[string]any
					if err := json.Unmarshal(line, &event); err != nil {
						t.Fatal(err)
					}
					if event["cwd"] != root || event["input_cwd"] != root || event["data"] != data || event["env_file"] != true || !filepath.IsAbs(event["root"].(string)) {
						t.Fatalf("setup environment changed: %s", line)
					}
					for _, key := range []string{"cwd", "input_cwd", "data", "root"} {
						delete(event, key)
					}
					normalized = append(normalized, event)
					key := event["event"].(string)
					if trigger, ok := event["trigger"].(string); ok {
						key += ":" + trigger
					}
					dispatch = append(dispatch, key)
				}
				want := []string{"SessionStart", "SessionStart", "SessionStart", "Setup:init", "SessionStart", "Setup:init", "SessionStart", "Setup:maintenance", "SessionStart"}
				if !slices.Equal(dispatch, want) {
					t.Fatalf("explicit setup dispatch: %v want %v", dispatch, want)
				}
				actual, err := json.Marshal(normalized)
				if err != nil {
					t.Fatal(err)
				}
				if converted && !bytes.Equal(actual, baseline) {
					t.Fatalf("source/converted setup dispatch differs: %s vs %s", actual, baseline)
				}
				baseline = actual
				if converted {
					for _, excluded := range []bool{true, false} {
						cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
						if excluded {
							ids := []string{"claude-setup"}
							cfg.Packs[0].Hooks.VectorSelector.Exclude = &ids
						}
						before := mustRead(t, logPath)
						if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(before, mustRead(t, logPath)) {
							t.Fatal("selection sync executed setup")
						}
						run("--init-only")
						added := mustRead(t, logPath)[len(before):]
						if bytes.Contains(added, []byte(`"event": "Setup"`)) == excluded || !bytes.Contains(added, []byte(`"event": "SessionStart"`)) {
							t.Fatalf("Setup exclusion/re-enable changed dispatch: %s", added)
						}
					}
				}
				readyAfter, err := os.Stat(ready)
				if err != nil || !readyAfter.ModTime().Equal(readyBefore.ModTime()) {
					t.Fatal("explicit setup/warm restart rewrote persistent data")
				}
			}
		})
	}
}

func TestNativeSetupWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, modules    string
		lock, codex, pending, want bool
	}{
		{name: "no-lock", manifest: `{"dependencies":{"probe":"1"}}`},
		{name: "no-packages", lock: true, manifest: `{}`},
		{name: "missing", lock: true, manifest: `{"dependencies":{"probe":"1"}}`, want: true},
		{name: "dev-packages-may-be-omitted", lock: true, manifest: `{"devDependencies":{"probe":"1"}}`},
		{name: "malformed", lock: true, manifest: `{`, want: true},
		{name: "modules-file", lock: true, manifest: `{"dependencies":{"probe":"1"}}`, modules: "file", want: true},
		{name: "modules-present", lock: true, manifest: `{"dependencies":{"probe":"1"}}`, modules: "directory"},
		{name: "modules-partial", lock: true, manifest: `{"dependencies":{"probe":"1","missing":"1"}}`, modules: "directory", want: true},
		{name: "optional-absent", lock: true, manifest: `{"dependencies":{"probe":"1","optional":"1"},"optionalDependencies":{"optional":"1"}}`, modules: "directory"},
		{name: "all-optional-absent", lock: true, manifest: `{"dependencies":{"optional":"1"},"optionalDependencies":{"optional":"1"}}`},
		{name: "invalid-name", lock: true, manifest: `{"dependencies":{"../outside":"1"}}`, modules: "directory", want: true},
		{name: "pending", lock: true, manifest: `{"dependencies":{"probe":"1"}}`, modules: "directory", pending: true, want: true},
		{name: "codex", lock: true, manifest: `{"dependencies":{"probe":"1"}}`, codex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "package.json"), tc.manifest)
			if tc.lock {
				writeFile(t, filepath.Join(root, "package-lock.json"), `{}`)
			}
			if tc.modules == "file" {
				writeFile(t, filepath.Join(root, "node_modules"), "not a directory")
			} else if tc.modules == "directory" {
				if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "node_modules/probe/package.json"), `{"name":"probe","version":"1"}`)
			}
			record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, CachePath: root, SetupPending: tc.pending}
			if tc.codex {
				record.Harness = domain.HarnessCodex
			}
			warnings := nativeSetupWarnings(record, "probe@market")
			if (len(warnings) > 0) != tc.want {
				t.Fatalf("warnings=%v want=%t", warnings, tc.want)
			}
		})
	}
}

func TestNativeNodeSetupInspection(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is required for installed-tree validation")
	}
	home := t.TempDir()
	t.Setenv("npm_config_userconfig", filepath.Join(home, "npmrc"))
	t.Setenv("npm_config_cache", filepath.Join(home, "npm-cache"))
	t.Setenv("npm_config_global", "true")
	t.Setenv("npm_config_prefix", home)
	t.Setenv("npm_config_depth", "0")
	t.Setenv("npm_config_package_lock_only", "true")
	t.Setenv("npm_config_include", "dev")
	for _, tc := range []struct {
		name, dependency string
		missing, want    bool
	}{
		{name: "complete-transitive"},
		{name: "missing-transitive", missing: true, want: true},
		{name: "invalid-version", dependency: `{"name":"probe","version":"2.0.0"}`, want: true},
		{name: "malformed-package", dependency: `{`, want: true},
		{name: "optional-omitted", dependency: `{"name":"probe","version":"1.0.0","optionalDependencies":{"leaf":"1.0.0"}}`, missing: true},
		{name: "optional-peer-omitted", dependency: `{"name":"probe","version":"1.0.0","peerDependencies":{"leaf":"1.0.0"},"peerDependenciesMeta":{"leaf":{"optional":true}}}`, missing: true},
		{name: "dev-omitted", dependency: `{"name":"probe","version":"1.0.0","devDependencies":{"leaf":"1.0.0"}}`, missing: true},
		{name: "extra-package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "package.json"), `{"name":"node-probe","version":"1.0.0","dependencies":{"probe":"1.0.0"},"devDependencies":{"dev-only":"1.0.0"},"optionalDependencies":{"optional-only":"1.0.0"},"scripts":{"postinstall":"exit 99"}}`)
			writeFile(t, filepath.Join(root, "package-lock.json"), `{}`)
			dependency := tc.dependency
			if dependency == "" {
				dependency = `{"name":"probe","version":"1.0.0","dependencies":{"leaf":"1.0.0"}}`
			}
			writeFile(t, filepath.Join(root, "node_modules/probe/package.json"), dependency)
			if !tc.missing {
				writeFile(t, filepath.Join(root, "node_modules/leaf/package.json"), `{"name":"leaf","version":"1.0.0"}`)
			}
			if tc.name == "extra-package" {
				writeFile(t, filepath.Join(root, "node_modules/extra/package.json"), `{"name":"extra","version":"1.0.0"}`)
			}
			before, err := plugin.ReadFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, Home: home, CachePath: root}
			warnings := nativeNodeSetupWarnings(context.Background(), home, record, "probe@market")
			if (len(warnings) > 0) != tc.want {
				t.Fatalf("warnings=%v want=%t", warnings, tc.want)
			}
			after, err := plugin.ReadFiles(root)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("read-only validation changed the installed package: %v", err)
			}
		})
	}
}

func TestNativeSharedSetupState(t *testing.T) {
	root := t.TempDir()
	current := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: t.TempDir(), MarketplaceDir: filepath.Join(root, "rendered-plugins/claudecode/market"), Generation: "generation", SourcePack: "local-scope"}
	current.CachePath = filepath.Join(current.ConfigHome, "plugins/cache/market/probe/1.0.0")
	binding := "probe@market"
	for _, tc := range []struct {
		name, home, gen, version string
		harness                  domain.Harness
		wantPending              bool
	}{
		{name: "legacy-shared-failure", wantPending: true},
		{name: "other-home", home: t.TempDir()},
		{name: "other-generation-same-cache", gen: "other", wantPending: true},
		{name: "other-cache-version", version: "2.0.0"},
		{name: "other-harness", harness: domain.HarnessCodex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := current
			other.SetupPending, other.SourcePack = true, "other-scope"
			if tc.home != "" {
				other.ConfigHome = tc.home
			}
			if tc.gen != "" {
				other.Generation = tc.gen
			}
			if tc.version != "" {
				other.CachePath = filepath.Join(filepath.Dir(current.CachePath), tc.version)
			}
			if tc.harness != "" {
				other.Harness = tc.harness
			}
			want := current
			want.SetupPending = tc.wantPending
			got, err := nativeSetupState(current, binding, []domain.NativePluginRecord{other})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("shared setup changed scope identity or selected wrong result: %+v want %+v error=%v", got, want, err)
			}
		})
	}
	failed := current
	failed.SetupPending = true
	if err := setNativeSetupState(current, binding, false); err != nil {
		t.Fatal(err)
	}
	setupTarget, _ := nativeSetupTarget(current, binding)
	stamp := time.Unix(1, 0)
	if err := os.Chtimes(setupTarget.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := setNativeSetupState(current, binding, false); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(setupTarget.Path); err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged setup state was rewritten: info=%v error=%v", info, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(setupTarget.Path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := setNativeSetupState(current, binding, false); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(setupTarget.Path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("setup state permissions were not restored: info=%v error=%v", info, err)
		}
	}
	if got, err := nativeSetupState(failed, binding, []domain.NativePluginRecord{failed}); err != nil || got.SetupPending {
		t.Fatal("completed repair did not supersede a legacy failure")
	}
	if err := setNativeSetupState(current, binding, true); err != nil {
		t.Fatal(err)
	}
	if got, err := nativeSetupState(current, binding, nil); err != nil || !got.SetupPending {
		t.Fatal("removing all ledger owners lost shared pending setup")
	}
	if got, err := nativeSetupState(current, "another@market", nil); err != nil || got.SetupPending {
		t.Fatal("setup leaked into another native binding")
	}
	target, _ := nativeSetupTarget(current, binding)
	for _, invalid := range []string{`{"setups":[]}`, `{"setups":{"` + target.Binding + `":true}}`, `{"setups":{"` + target.Binding + `":{"1.0.0":"invalid"}}}`, `{"setups":{"` + target.Binding + `":{"../outside":true}}}`} {
		writeFile(t, target.Path, invalid)
		if _, err := nativeSetupState(current, binding, nil); err == nil {
			t.Fatal("invalid shared setup state was accepted")
		}
		if err := setNativeSetupState(current, binding, false); err == nil || string(mustRead(t, target.Path)) != invalid {
			t.Fatal("invalid shared setup state was overwritten")
		}
	}
	if err := os.Remove(target.Path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "setup.json")
	writeFile(t, outside, `{}`)
	if err := os.Symlink(outside, target.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeSetupState(current, binding, nil); err == nil {
		t.Fatal("shared setup accepted a path outside the config directory")
	}
	if err := setNativeSetupState(current, binding, false); err == nil || string(mustRead(t, outside)) != `{}` {
		t.Fatal("shared setup overwrote an external symlink target")
	}
}

func TestCodexMarketplaceApprovalOnlyOnPolicyRefusal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native plugins are unsupported on Windows")
	}
	root := t.TempDir()
	shim := filepath.Join(root, "codex")
	writeFile(t, shim, "#!/bin/sh\nprintf '%s\\n' \"$AIPACK_POLICY_FIXTURE_ERROR\"\n[ -n \"$AIPACK_POLICY_FIXTURE_ERROR\" ] && exit 1\nexit 0\n")
	if err := os.Chmod(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	record := domain.NativePluginRecord{Harness: domain.HarnessCodex, ConfigHome: filepath.Join(root, "home"), MarketplaceDir: filepath.Join(root, "market")}
	for _, message := range []string{"", "installer failed", "marketplace source is not allowed by requirements"} {
		t.Setenv("AIPACK_POLICY_FIXTURE_ERROR", message)
		err := addNativeMarketplace(context.Background(), root, record)
		if message == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("native error lost: %v", err)
		}
		approval := strings.Contains(err.Error(), "marketplaces.allowed_sources") && strings.Contains(err.Error(), fmt.Sprintf("path = %q", record.MarketplaceDir))
		if approval != strings.Contains(message, "not allowed by requirements") {
			t.Fatalf("incorrect source approval action: %v", err)
		}
	}
}

func TestCodexCachedPluginRequiresSourceRegistration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	record := domain.NativePluginRecord{Harness: domain.HarnessCodex, ConfigHome: filepath.Join(root, "home"), MarketplaceDir: filepath.Join(root, "market"), CachePath: filepath.Join(root, "cache")}
	if err := os.MkdirAll(record.CachePath, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, config string
		installed    bool
	}{
		{"unregistered", "", false},
		{"different-source", "[marketplaces.market]\nsource_type='local'\nsource='/somewhere/else'\n", false},
		{"registered", fmt.Sprintf("[marketplaces.market]\nsource_type='local'\nsource=%q\n", record.MarketplaceDir), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(record.ConfigHome, "config.toml"), tc.config)
			installed, err := nativePluginInstalled(record, "probe@market")
			if err != nil || installed != tc.installed {
				t.Fatalf("installed=%t, want %t: %v", installed, tc.installed, err)
			}
		})
	}
}

func TestCodexMissingMarketplaceDoesNotPrescribeApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native plugins are unsupported on Windows")
	}
	root := t.TempDir()
	shim := filepath.Join(root, "codex")
	writeFile(t, shim, "#!/bin/sh\nprintf '%s\\n' '{\"marketplaces\":[]}'\n")
	if err := os.Chmod(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	action := domain.NativePluginAction{Package: domain.NativePlugin{Marketplace: "market"}, ConfigHome: root, MarketplaceDir: filepath.Join(root, "market")}
	err := checkNativeCodexMarketplace(context.Background(), root, action)
	if err == nil || !strings.Contains(err.Error(), "does not admit local marketplace") || strings.Contains(err.Error(), "administrator") || strings.Contains(err.Error(), "allowed_sources") {
		t.Fatalf("missing registration was treated as a source-policy refusal: %v", err)
	}
}

func TestClaudeNativeNodeInstallContract(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native package-install checks")
	}
	shim := t.TempDir()
	writeFile(t, filepath.Join(shim, "npm"), `#!/usr/bin/env python3
import json, os, pathlib, sys, time
if sys.argv[1:2] == ["ls"]:
    print("{}")
    sys.exit(0)
with pathlib.Path(os.environ["AIPACK_NPM_LOG"]).open("a") as log:
    log.write(json.dumps({"args": sys.argv[1:], "cwd": os.getcwd()}) + "\n")
if sys.argv[1:3] == ["config", "get"]:
    print("https://registry.npmjs.org/")
elif "ci" in sys.argv:
    if os.environ.get("AIPACK_NPM_FAIL"):
        print("AIPACK_NPM_FAILURE", file=sys.stderr)
        sys.exit(9)
    module = pathlib.Path("node_modules/probe-dependency")
    module.mkdir(parents=True, exist_ok=True)
    (module / "index.js").write_text("module.exports = 'AIPACK_NODE_READY';\n")
    (module / "package.json").write_text('{"name":"probe-dependency","version":"1.0.0"}')
    pause = os.environ.get("AIPACK_NPM_PAUSE")
    if pause:
        pathlib.Path(pause).write_text(json.dumps({"pid": os.getpid(), "cwd": os.getcwd()}))
        while not pathlib.Path(pause + ".release").exists():
            time.sleep(0.05)
        pathlib.Path(pause + ".done").write_text("done")
else:
    sys.exit(1)
`)
	if err := os.Chmod(filepath.Join(shim, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, origin := range []string{"directory", "file", "failure", "converted-failure", "converted-payload-refresh", "converted-selection-refresh", "converted-disabled-refresh", "converted-version-refresh", "converted-interruption", "converted-cold-interruption", "converted-git-interruption", "converted-git-cold-interruption"} {
		t.Run(origin, func(t *testing.T) {
			if strings.HasSuffix(origin, "interruption") && os.Getenv("AIPACK_TEST_BINARY") == "" {
				t.Skip("set AIPACK_TEST_BINARY to a freshly built CLI for process-loss checks")
			}
			root, home := t.TempDir(), t.TempDir()
			market, nativeHome := filepath.Join(root, "market"), filepath.Join(home, "native")
			source := filepath.Join(market, "plugins/probe")
			writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"node-probe","version":"1.0.0"}`)
			writeFile(t, filepath.Join(source, "package.json"), `{"name":"node-probe","version":"1.0.0","dependencies":{"probe-dependency":"1.0.0"},"scripts":{"postinstall":"exit 99"}}`)
			writeFile(t, filepath.Join(source, "package-lock.json"), `{"name":"node-probe","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"node-probe","version":"1.0.0","dependencies":{"probe-dependency":"1.0.0"}},"node_modules/probe-dependency":{"version":"1.0.0","resolved":"https://registry.npmjs.org/probe-dependency/-/probe-dependency-1.0.0.tgz"}}}`)
			if strings.HasSuffix(origin, "refresh") {
				writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)
			}
			catalog := filepath.Join(market, ".claude-plugin/marketplace.json")
			writeFile(t, catalog, `{"name":"node-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"node-probe","source":"./plugins/probe"}]}`)
			log := filepath.Join(root, "npm.jsonl")
			t.Setenv("AIPACK_NPM_LOG", log)
			record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: nativeHome, Home: home, SettingsPath: filepath.Join(nativeHome, "settings.json"), MarketplaceDir: market}
			registration := market
			if origin == "file" {
				registration = catalog
			}
			if _, err := runNativePlugin(context.Background(), home, record, "marketplace", "add", registration); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(origin, "converted-") {
				// AIPack owns a separate registry/view from the native control.
				if err := removeNativeMarketplace(context.Background(), home, record, "node-market"); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "1")
				cfgDir := filepath.Join(root, "config")
				if strings.Contains(origin, "-git-") {
					for _, args := range [][]string{
						{"init", "--initial-branch=main", source},
						{"-C", source, "add", "."},
						{"-C", source, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic native interruption fixture"},
						{"clone", "--bare", source, filepath.Join(root, "fixture.git")},
					} {
						if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
							t.Fatalf("fixture git %v: %v\n%s", args, err, out)
						}
					}
					writeFile(t, catalog, fmt.Sprintf(`{"name":"node-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"node-probe","source":{"source":"url","url":%q}}]}`, "file://"+filepath.Join(root, "fixture.git")))
				}
				if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: cfgDir, URL: catalog}, nil); err != nil {
					t.Fatal(err)
				}
				entry, err := RegistryLookup(RegistryListRequest{ConfigDir: cfgDir}, "node-probe")
				if err != nil {
					t.Fatal(err)
				}
				if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(cfgDir, "probe", entry), nil); err != nil {
					t.Fatal(err)
				}
				eng := engine.New(nil, nil)
				p, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", cfgDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
				result, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(warnings, func(w domain.Warning) bool { return strings.Contains(w.String(), "setup is incomplete") }) {
					t.Fatalf("failed native setup was silently reported as complete: %v", warnings)
				}
				binding := "node-probe@node-market"
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				if err != nil || !ledger.NativePlugins[binding].SetupPending {
					t.Fatalf("incomplete setup was not persisted: %v", err)
				}
				prior := ledger.NativePlugins[binding]
				data := filepath.Join(nativeHome, "plugins/data/node-probe-node-market/marker")
				writeFile(t, data, "retained runtime data")
				updatePack := func() {
					t.Helper()
					updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "probe"}, nil, nil)
					if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated && updates[0].Status != StatusUpToDate {
						t.Fatalf("native source update failed: %+v %v", updates, err)
					}
					p, _, err = eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				if origin == "converted-version-refresh" {
					for _, step := range []struct {
						version                  string
						fail, wantError, pending bool
					}{{"2.0.0", true, false, true}, {"2.0.0", false, false, false}, {"1.0.0", true, true, false}, {"1.0.0", false, false, false}} {
						writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"node-probe","version":"`+step.version+`"}`)
						updatePack()
						failure := ""
						if step.fail {
							failure = "1"
							writeFile(t, filepath.Join(prior.CachePath, "node_modules/probe-dependency/package.json"), `{"name":"probe-dependency","version":"1.0.0"}`)
						}
						t.Setenv("AIPACK_NPM_FAIL", failure)
						beforeLedger := mustRead(t, result.Plan.Ledger)
						_, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
						if step.wantError {
							if err == nil || !strings.Contains(err.Error(), "AIPACK_NPM_FAILURE") || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
								t.Fatalf("returning to an incomplete older cache lost its failure: %v", err)
							}
							continue
						}
						ledger, _, loadErr := eng.LoadLedger(result.Plan.Ledger)
						ready := ledger.NativePlugins[binding]
						if err != nil || loadErr != nil || ready.SetupPending != step.pending || (len(warnings) > 0) != step.pending || nativeSetupCacheVersion(ready, binding) != step.version || string(mustRead(t, data)) != "retained runtime data" {
							t.Fatalf("setup crossed native cache versions: %+v %v %v %v", ready, warnings, err, loadErr)
						}
						if !step.pending && !util.PathExists(filepath.Join(ready.CachePath, "node_modules/probe-dependency/index.js")) {
							t.Fatal("completed cache version has no executable package output")
						}
					}
					return
				}
				if strings.HasSuffix(origin, "refresh") {
					// A failed setup may leave package metadata without executable output.
					writeFile(t, filepath.Join(prior.CachePath, "node_modules/probe-dependency/package.json"), `{"name":"probe-dependency","version":"1.0.0"}`)
					if origin == "converted-selection-refresh" {
						p.Packs[0].NativePlugin.Selected[domain.CategoryHooks] = nil
					} else {
						writeFile(t, filepath.Join(source, "marker.txt"), "changed payload")
						updatePack()
					}
					if origin == "converted-disabled-refresh" {
						if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
							t.Fatal(err)
						}
					}
					beforeLedger := mustRead(t, result.Plan.Ledger)
					beforeCache, err := plugin.ReadFiles(prior.CachePath)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "AIPACK_NPM_FAILURE") {
						t.Fatalf("refresh lost a known setup failure in a reused cache: %v", err)
					}
					if !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) || string(mustRead(t, data)) != "retained runtime data" {
						t.Fatal("failed refresh changed source identity or runtime data")
					}
					cacheAfter, err := plugin.ReadFiles(prior.CachePath)
					if err != nil || !reflect.DeepEqual(beforeCache, cacheAfter) {
						t.Fatalf("failed refresh changed prior native cache bytes: %v", err)
					}
					t.Setenv("AIPACK_NPM_FAIL", "")
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
						t.Fatalf("refreshed setup could not recover: %v %v", err, warnings)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					ready := ledger.NativePlugins[binding]
					if err != nil || ready.SetupPending || ready.Generation == prior.Generation || ready.CachePath != prior.CachePath || string(mustRead(t, data)) != "retained runtime data" || !util.PathExists(filepath.Join(ready.CachePath, "node_modules/probe-dependency/index.js")) {
						t.Fatalf("refresh did not repair setup while retaining cache and data: %+v %v", ready, err)
					}
					if origin == "converted-selection-refresh" {
						if bytes.Contains(mustRead(t, filepath.Join(ready.CachePath, "hooks/hooks.json")), []byte(`"Stop"`)) {
							t.Fatal("native cache retained an excluded hook after refresh")
						}
					} else if string(mustRead(t, filepath.Join(ready.CachePath, "marker.txt"))) != "changed payload" {
						t.Fatal("native cache retained stale payload after refresh")
					}
					beforeLog := mustRead(t, log)
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 || !bytes.Equal(beforeLog, mustRead(t, log)) {
						t.Fatalf("completed refreshed setup was repeated: %v %v", err, warnings)
					}
					return
				}
				if strings.HasSuffix(origin, "interruption") {
					view := prior.MarketplaceDir
					viewBefore, err := packTreeDigest(view)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(origin, "-git-") && !util.PathExists(filepath.Join(view, ".aipack-marketplace/node-probe.git/HEAD")) {
						t.Fatal("process-loss fixture lacks copied delivery repository")
					}
					if strings.Contains(origin, "cold-interruption") {
						if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
							t.Fatal(err)
						}
						// Reset only this disposable, deactivated native fixture cache.
						if err := util.RemoveOwnedTree(prior.CachePath); err != nil {
							t.Fatal(err)
						}
					}
					writeFile(t, filepath.Join(cfgDir, "profiles/default.yaml"), "schema_version: 2\npacks:\n  - name: probe\n")
					pause := filepath.Join(root, "npm-paused.json")
					cli := func(paused bool, extra ...string) *exec.Cmd {
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						t.Cleanup(cancel)
						args := append([]string{"sync", "--harness", "claudecode", "--scope", "global", "--config-dir", cfgDir, "--yes", "--json"}, extra...)
						cmd := exec.CommandContext(ctx, os.Getenv("AIPACK_TEST_BINARY"), args...)
						cmd.Dir, cmd.WaitDelay = root, time.Second
						cmd.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
							return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(value, "AIPACK_NPM_FAIL=") || strings.HasPrefix(value, "AIPACK_NPM_PAUSE=")
						}), "HOME="+home, "CLAUDE_CONFIG_DIR="+nativeHome)
						if paused {
							cmd.Env = append(cmd.Env, "AIPACK_NPM_PAUSE="+pause)
						}
						return cmd
					}
					cmd := cli(true)
					beforeLedger := mustRead(t, result.Plan.Ledger)
					var output bytes.Buffer
					cmd.Stdout, cmd.Stderr = &output, &output
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						writeFile(t, pause+".release", "release")
						_ = cmd.Process.Kill()
						deadline := time.Now().Add(2 * time.Second)
						for util.PathExists(pause) && !util.PathExists(pause+".done") && time.Now().Before(deadline) {
							time.Sleep(20 * time.Millisecond)
						}
					})
					deadline := time.Now().Add(15 * time.Second)
					for !util.PathExists(pause) && time.Now().Before(deadline) {
						time.Sleep(20 * time.Millisecond)
					}
					if !util.PathExists(pause) {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
						t.Fatalf("CLI did not reach npm setup: %s", output.Bytes())
					}
					if err := cmd.Process.Kill(); err != nil {
						t.Fatal(err)
					}
					if err := cmd.Wait(); err == nil {
						t.Fatal("killed CLI reported success")
					}
					if !util.PathExists(filepath.Join(nativeOperationDir(cfgDir), "operation.json")) || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
						t.Fatal("process loss did not retain native recovery state")
					}
					beforeLog := mustRead(t, log)
					out, err := cli(false).CombinedOutput()
					if err == nil || !bytes.Contains(out, []byte("another AIPack mutation is running")) || !bytes.Equal(beforeLog, mustRead(t, log)) {
						t.Fatalf("recovery raced an orphaned native installer: %v\n%s", err, out)
					}
					writeFile(t, pause+".release", "release")
					deadline = time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						unlock, err := util.LockConfig(cfgDir)
						if err == nil {
							_ = unlock()
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
					out, err = cli(false).CombinedOutput()
					if err != nil {
						t.Fatalf("process-loss recovery failed after installer exit: %v\n%s", err, out)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					ready := ledger.NativePlugins[binding]
					if err != nil || ready.SetupPending || ready.Generation != prior.Generation || ready.CachePath != prior.CachePath || string(mustRead(t, data)) != "retained runtime data" {
						t.Fatalf("process-loss recovery changed source/data or retained pending setup: %+v %v", ready, err)
					}
					viewAfter, err := packTreeDigest(view)
					if err != nil || viewAfter != viewBefore || util.PathExists(nativeOperationDir(cfgDir)) {
						t.Fatalf("process-loss recovery changed the native view/repository or left pending delivery: %v", err)
					}
					return
				}
				beforeLog := mustRead(t, log)
				req.DryRun = true
				if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("dry run executed package setup: %v", err)
				}
				req.DryRun = false
				foreign := prior
				foreign.SettingsPath = filepath.Join(root, ".claude/settings.local.json")
				if _, err := installNativePlugin(context.Background(), home, foreign, binding); err != nil {
					t.Fatal(err)
				}
				beforeLog = mustRead(t, log)
				if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "scope outside AIPack") || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("setup retry changed a cache shared with an unowned scope: %v", err)
				}
				if err := uninstallNativePlugin(context.Background(), home, foreign, binding); err != nil {
					t.Fatal(err)
				}
				// An older ledger or damaged warm cache first records incomplete setup.
				legacy := prior
				legacy.SetupPending = false
				ledger.NativePlugins[binding] = legacy
				legacyTarget, _ := nativeSetupTarget(legacy, binding)
				if err := restoreNativeConfigEntry(legacyTarget.Path, legacyTarget.Section, legacyTarget.Binding, nil, false); err != nil {
					t.Fatal(err)
				}
				if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
					t.Fatal(err)
				}
				beforeLog = mustRead(t, log)
				if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) == 0 || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("unrecorded incomplete setup was retried before persistence: %v %v", err, warnings)
				}
				if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "AIPACK_NPM_FAILURE") {
					t.Fatalf("failed setup retry was not reported: %v", err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "")
				if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
					t.Fatalf("setup retry did not complete: %v %v", err, warnings)
				}
				ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
				ready := ledger.NativePlugins[binding]
				if err != nil || ready.SetupPending || ready.Generation != prior.Generation || ready.CachePath != prior.CachePath || string(mustRead(t, data)) != "retained runtime data" {
					t.Fatalf("retry changed source identity/data or retained pending state: %+v %v", ready, err)
				}
				localReq := req
				localReq.Scope = domain.ScopeProject
				if _, _, err := RunSync(context.Background(), eng, p, localReq, testRegistry(), nil, nil); err != nil {
					t.Fatal(err)
				}
				installedState := filepath.Join(nativeHome, "plugins/installed_plugins.json")
				beforeLog = mustRead(t, log)
				if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("completed setup was not reused: %v %v", err, warnings)
				}
				ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				sharedFailure := ledger.NativePlugins[binding]
				sharedFailure.SetupPending = true
				ledger.NativePlugins[binding] = sharedFailure
				target, _ := nativeSetupTarget(sharedFailure, binding)
				if err := restoreNativeConfigEntry(target.Path, target.Section, target.Binding, nil, false); err != nil {
					t.Fatal(err)
				}
				if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "1")
				beforeLog = mustRead(t, log)
				planned, err := PlanWithDiffs(context.Background(), eng, p, localReq, testRegistry())
				if err != nil || !slices.ContainsFunc(planned.Ops, func(op PlanOp) bool { return op.Kind == PlanOpPlugin && strings.Contains(op.Diff, "Retry native Node") }) || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("shared pending setup was absent from the side-effect-free plan: %v %+v", err, planned.Ops)
				}
				if _, _, err := RunSync(context.Background(), eng, p, localReq, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "AIPACK_NPM_FAILURE") {
					t.Fatalf("a shared incomplete cache was reported ready by another scope: %v", err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "")
				if _, warnings, err := RunSync(context.Background(), eng, p, localReq, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
					t.Fatalf("another scope could not repair the shared cache: %v %v", err, warnings)
				}
				beforeLog = mustRead(t, log)
				if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 || !bytes.Equal(beforeLog, mustRead(t, log)) {
					t.Fatalf("stale setup failure repeated an already-completed shared repair: %v %v", err, warnings)
				}
				ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				sharedFailure = ledger.NativePlugins[binding]
				sharedFailure.SetupPending = true
				ledger.NativePlugins[binding] = sharedFailure
				if err := setNativeSetupState(sharedFailure, binding, true); err != nil {
					t.Fatal(err)
				}
				if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
					t.Fatal(err)
				}
				if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "1")
				if _, _, err := RunSync(context.Background(), eng, p, localReq, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "AIPACK_NPM_FAILURE") {
					t.Fatalf("removing the latest setup owner lost a shared failure: %v", err)
				}
				t.Setenv("AIPACK_NPM_FAIL", "")
				if _, warnings, err := RunSync(context.Background(), eng, p, localReq, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
					t.Fatalf("shared setup could not recover after another scope was disabled: %v %v", err, warnings)
				}
				if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
					t.Fatalf("re-enabling an owner lost completed setup: %v %v", err, warnings)
				}
				priorInstallations, err := readNativeConfig(installedState)
				if err != nil {
					t.Fatal(err)
				}
				for _, after := range []bool{false, true} {
					// Persist pending before invoking npm, as after a failed cold install.
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					if err != nil {
						t.Fatal(err)
					}
					ready = ledger.NativePlugins[binding]
					ready.SetupPending = true
					if err := setNativeSetupState(ready, binding, true); err != nil {
						t.Fatal(err)
					}
					ledger.NativePlugins[binding] = ready
					if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
						t.Fatal(err)
					}
					eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: after}
					beforeLog = mustRead(t, log)
					interrupted := false
					func() {
						defer func() {
							if recovered := recover(); recovered != nil {
								if recovered != "native ledger interruption" {
									panic(recovered)
								}
								interrupted = true
							}
						}()
						if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil {
							t.Fatal(err)
						}
					}()
					eng.FS = engine.OSFS{}
					if !interrupted {
						t.Fatal("setup ledger interruption did not run")
					}
					if bytes.Equal(beforeLog, mustRead(t, log)) {
						t.Fatal("setup interruption did not retry npm")
					}
					unlock, err := lockPackMutation(cfgDir, false)
					if err != nil {
						t.Fatal(err)
					}
					if err := unlock(); err != nil {
						t.Fatal(err)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					recovered := ledger.NativePlugins[binding]
					if err != nil || recovered.SetupPending == after || recovered.Generation != prior.Generation || recovered.CachePath != prior.CachePath || string(mustRead(t, data)) != "retained runtime data" {
						t.Fatalf("setup recovery chose wrong state (after=%t): %+v %v", after, recovered, err)
					}
					shared, err := nativeSetupState(recovered, binding, nil)
					if err != nil || shared.SetupPending != recovered.SetupPending {
						t.Fatalf("shared setup recovery disagreed with the committed ledger (after=%t): %+v %v", after, shared, err)
					}
					installations, err := readNativeConfig(installedState)
					if err != nil || !reflect.DeepEqual(priorInstallations, installations) {
						t.Fatalf("setup recovery changed shared scopes (after=%t): %v\nbefore=%v\nafter=%v", after, err, priorInstallations, installations)
					}
					beforeLog = mustRead(t, log)
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
						t.Fatalf("setup recovery could not converge: %v %v", err, warnings)
					}
					if bytes.Equal(beforeLog, mustRead(t, log)) != after {
						t.Fatalf("setup recovery repeated or skipped the wrong repair (after=%t)", after)
					}
				}
				return
			}
			if origin == "failure" {
				t.Setenv("AIPACK_NPM_FAIL", "1")
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "claude", "plugin", "install", "node-probe@node-market", "--json")
				cmd.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
					return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CLAUDE_CONFIG_DIR=")
				}), "HOME="+home, "CLAUDE_CONFIG_DIR="+nativeHome)
				var out, diagnostics bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostics
				err := cmd.Run()
				t.Logf("failed package install: error=%v result=%s diagnostics=%s", err, out.Bytes(), diagnostics.Bytes())
				listed, listErr := runNativePlugin(context.Background(), home, record, "list", "--json")
				t.Logf("failed package install list: %s error=%v", listed, listErr)
				if err != nil || !bytes.Contains(out.Bytes(), []byte(`"outcome":"ok"`)) || diagnostics.Len() != 0 || !bytes.Contains(mustRead(t, log), []byte(`"ci", "--ignore-scripts"`)) {
					t.Fatal("pinned native npm-failure reporting changed")
				}
				t.Setenv("AIPACK_NPM_FAIL", "")
				updated, updateErr := runNativePlugin(context.Background(), home, record, "update", "node-probe@node-market", "--json", "--scope", "user")
				t.Logf("native package retry update: result=%s error=%v", updated, updateErr)
				cache, retryErr := installNativePlugin(context.Background(), home, record, "node-probe@node-market")
				if retryErr != nil {
					t.Fatal(retryErr)
				}
				if _, err := os.Stat(filepath.Join(cache, "node_modules/probe-dependency/index.js")); !os.IsNotExist(err) {
					t.Fatal("pinned native same-version retry behavior changed")
				}
				if !slices.ContainsFunc(nativeSetupWarnings(domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, CachePath: cache}, "node-probe@node-market"), func(w domain.Warning) bool { return strings.Contains(w.String(), "locked Node packages are absent") }) {
					t.Fatal("missing native packages did not produce a setup warning")
				}
				return
			}
			cache, err := installNativePlugin(context.Background(), home, record, "node-probe@node-market")
			if err != nil {
				t.Fatal(err)
			}
			calls, readErr := os.ReadFile(log)
			t.Logf("origin=%s cache=%s npm=%s read=%v", origin, cache, calls, readErr)
			if !bytes.Contains(calls, []byte(`"ci", "--ignore-scripts"`)) || canonicalPath(cache) == canonicalPath(source) {
				t.Fatal("pinned host did not copy/install the locked packages without scripts")
			}
			if got := string(mustRead(t, filepath.Join(cache, "node_modules/probe-dependency/index.js"))); !strings.Contains(got, "AIPACK_NODE_READY") {
				t.Fatal("native package install did not retain its output")
			}
		})
	}
}

func TestNativeCopiedSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".aipack-marketplace"), 0o700); err != nil {
		t.Fatal(err)
	}
	action := domain.NativePluginAction{MarketplaceDir: root, Package: domain.NativePlugin{Harness: domain.HarnessClaudeCode, Name: "probe"}, Files: []domain.NativePluginFile{
		{Path: "run.sh", Content: []byte("#!/bin/sh\nprintf fixture\n"), Mode: 0o755},
		{Path: "data", Content: []byte{'\r', '\n', 0, 255}, Mode: 0o444},
		{Path: "link", Link: "data", Mode: os.ModeSymlink | 0o777},
		{Path: ".gitattributes", Content: []byte("* text eol=lf filter=fixture\n"), Mode: 0o644},
	}}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--git-dir=" + filepath.Join(root, ".aipack-marketplace/probe.git")}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	var first string
	for i := 0; i < 3; i++ {
		if i == 2 {
			action.Files[0].Content = []byte("changed fixture\n")
		}
		finish, err := writeNativeCopiedSource(context.Background(), action, SyncRequest{})
		if err != nil {
			t.Fatal(err)
		}
		head := git("rev-parse", "HEAD")
		if i == 0 {
			first = head
		} else if (head == first) != (i == 1) {
			t.Fatal("copied source identity changed independently of payload bytes")
		}
		if git("show", "HEAD:data") != string(action.Files[1].Content) || git("show", "HEAD:link") != "data" || !strings.Contains(git("ls-tree", "HEAD", "run.sh", "link"), "100755") || !strings.Contains(git("ls-tree", "HEAD", "link"), "120000") {
			t.Fatal("copied source changed binary bytes, executable mode, or symlink")
		}
		if err := finish(i != 2); err != nil {
			t.Fatal(err)
		}
	}
	if git("rev-parse", "HEAD") != first || git("show", "HEAD:run.sh") != "#!/bin/sh\nprintf fixture\n" {
		t.Fatal("copied source rollback did not restore the previous package")
	}
}

func TestClaudeNativeGitHubSourceValidation(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native GitHub source validation")
	}
	root := t.TempDir()
	for _, kind := range []string{"github", "url", "git-subdir"} {
		for _, extra := range []string{
			``, `,"repo":"team/probe.git"`, `,"repo":"team/.github"`,
			`,"repo":null`,
			`,"ref":"main"`, `,"ref":""`, `,"ref":null`, `,"ref":123`,
			`,"sha":"0123456789012345678901234567890123456789"`, `,"sha":"1234567"`, `,"sha":""`, `,"sha":null`, `,"sha":123`,
			`,"sha":"012345678901234567890123456789012345678A"`, `,"sha":"01234567890123456789012345678901234567890"`,
		} {
			if kind != "github" && strings.HasPrefix(extra, `,"repo":`) {
				continue
			}
			body := []byte(`{"name":"github-market","owner":{"name":"Fixture"},"plugins":[{"name":"probe","source":{"source":"` + kind + `","repo":"team/probe","url":"https://example.invalid/probe.git","path":"plugin"` + extra + `}}]}`)
			path := filepath.Join(root, ".claude-plugin/marketplace.json")
			writeFile(t, path, string(body))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, "claude", "plugin", "validate", path)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "HOME="+root, "CLAUDE_CONFIG_DIR="+filepath.Join(root, "native"))
			out, nativeErr := cmd.CombinedOutput()
			cancel()
			reg, err := config.ParseMarketplace(body, config.RegistrySourceEntry{URL: root, Format: "claude"})
			if err != nil {
				t.Fatal(err)
			}
			if (nativeErr == nil) != (reg.Packs["probe"].Unsupported == "") {
				t.Errorf("Claude %s source fields %s: native=%v AIPack=%q\n%s", kind, extra, nativeErr, reg.Packs["probe"].Unsupported, out)
			}
		}
	}
}

func TestClaudeNativeNodeInstallParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for real npm installation parity")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	archives := map[string][]byte{}
	for name, files := range map[string][]struct{ name, body string }{
		"probe-dependency": {
			{"package/package.json", `{"name":"probe-dependency","version":"1.0.0","main":"index.js","dependencies":{"probe-leaf":"1.0.0"},"scripts":{"postinstall":"node postinstall.js"}}`},
			{"package/index.js", `module.exports = require('probe-leaf');`},
			{"package/postinstall.js", `require('fs').writeFileSync(process.env.AIPACK_NODE_LIFECYCLE, 'dependency-script-ran');`},
		},
		"probe-leaf": {
			{"package/package.json", `{"name":"probe-leaf","version":"1.0.0","main":"index.js","scripts":{"postinstall":"node postinstall.js"}}`},
			{"package/index.js", `module.exports = 'AIPACK_NODE_READY';`},
			{"package/postinstall.js", `require('fs').writeFileSync(process.env.AIPACK_NODE_LIFECYCLE, 'leaf-script-ran');`},
		},
	} {
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		for _, file := range files {
			if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(tw, file.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		archives[name] = archive.Bytes()
	}
	var unavailable atomic.Bool
	var npmArchive atomic.Value
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/node-probe" || req.URL.Path == "/node-probe/-/node-probe-1.0.0.tgz" {
			archive, _ := npmArchive.Load().([]byte)
			w.Header().Set("Cache-Control", "no-cache")
			if req.URL.Path == "/node-probe" {
				checksum := sha512.Sum512(archive)
				_ = json.NewEncoder(w).Encode(map[string]any{"name": "node-probe", "dist-tags": map[string]string{"latest": "1.0.0"}, "versions": map[string]any{"1.0.0": map[string]any{"name": "node-probe", "version": "1.0.0", "dist": map[string]string{"tarball": "http://" + req.Host + "/node-probe/-/node-probe-1.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(checksum[:])}}}})
			} else {
				_, _ = w.Write(archive)
			}
			return
		}
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		if req.URL.Path == "/v1/messages" {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Error(err)
				return
			}
			block, delta, stop := `{"type":"text","text":""}`, `{"type":"text_delta","text":"AIPACK_NODE_RESPONSE"}`, "end_turn"
			if bytes.Contains(body, []byte("mcp__plugin_node-probe_probe__observe")) && !bytes.Contains(body, []byte(`"tool_result"`)) {
				block, delta, stop = `{"type":"tool_use","id":"package_call","name":"mcp__plugin_node-probe_probe__observe","input":{}}`, `{"type":"input_json_delta","partial_json":"{}"}`, "tool_use"
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []string{
				`{"type":"message_start","message":{"id":"package_fixture","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":` + block + `}`,
				`{"type":"content_block_delta","index":0,"delta":` + delta + `}`,
				`{"type":"content_block_stop","index":0}`,
				fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":1}}`, stop),
				`{"type":"message_stop"}`,
			} {
				var value struct{ Type string }
				_ = json.Unmarshal([]byte(event), &value)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value.Type, event)
			}
			return
		}
		name := strings.SplitN(strings.TrimPrefix(req.URL.Path, "/"), "/", 2)[0]
		if archive, exists := archives[name]; exists && req.URL.Path == "/"+name+"/-/"+name+"-1.0.0.tgz" {
			if unavailable.Load() {
				http.Error(w, "package temporarily unavailable", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(archive)
			return
		}
		// Audit/telemetry are outside this synthetic package registry.
		http.NotFound(w, req)
	}))
	defer api.Close()
	for _, origin := range []string{
		"native", "converted", "converted-retry", "converted-missing-transitive",
		"native-git", "converted-git", "native-local-git", "converted-local-git",
		"native-root-git", "converted-root-git", "native-catalog-git", "converted-catalog-git",
		"native-github-root-git", "converted-github-root-git", "native-github-unversioned-root-git", "converted-github-unversioned-root-git",
		"native-github-ref-root-git", "converted-github-ref-root-git", "native-github-sha-unversioned-root-git", "converted-github-sha-unversioned-root-git",
		"native-github-tag-unversioned-root-git", "converted-github-tag-unversioned-root-git",
		"native-url-root-git", "converted-url-root-git", "native-url-trailing-root-git", "converted-url-trailing-root-git",
		"native-url-path-root-git", "converted-url-path-root-git",
		"native-subdir-shorthand-git", "converted-subdir-shorthand-git", "native-subdir-suffixed-git", "converted-subdir-suffixed-git",
		"native-root-catalog-git", "converted-root-catalog-git", "native-project-git", "converted-project-git",
		"native-unversioned-git", "converted-unversioned-git", "native-unversioned-local-git", "converted-unversioned-local-git",
		"native-unversioned-root-git", "converted-unversioned-root-git", "native-unversioned-catalog-git", "converted-unversioned-catalog-git",
		"native-unversioned-root-catalog-git", "converted-unversioned-root-catalog-git", "native-unversioned-project-git", "converted-unversioned-project-git",
		"native-npm", "converted-npm", "native-unversioned-npm", "converted-unversioned-npm",
		"native-inline-npm", "converted-inline-npm", "native-alias-npm", "converted-alias-npm",
		"native-tarball-npm", "converted-tarball-npm", "native-catalog-npm", "converted-catalog-npm",
		"native-project-npm", "converted-project-npm", "converted-retry-npm", "converted-missing-transitive-npm",
	} {
		t.Run(origin, func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			unversioned := strings.Contains(origin, "unversioned")
			npm := strings.HasSuffix(origin, "-npm")
			skillID := "1-0-0"
			if npm && unversioned {
				skillID = "unknown"
			}
			market, cfgDir, nativeHome := filepath.Join(root, "market"), filepath.Join(root, "config"), filepath.Join(home, "native")
			source := filepath.Join(market, "plugins/probe")
			payload := "./plugins/probe"
			if strings.Contains(origin, "root") {
				source, payload = market, "./"
			}
			scope := domain.ScopeGlobal
			settings := filepath.Join(nativeHome, "settings.json")
			if strings.Contains(origin, "project") {
				scope, settings = domain.ScopeProject, filepath.Join(root, ".claude/settings.local.json")
			}
			marker := filepath.Join(root, "lifecycle")
			t.Setenv("AIPACK_NODE_LIFECYCLE", marker)
			t.Setenv("npm_config_registry", api.URL)
			t.Setenv("npm_config_cache", filepath.Join(root, "npm-cache"))
			t.Setenv("npm_config_userconfig", filepath.Join(root, "user.npmrc"))
			t.Setenv("npm_config_audit", "false")
			t.Setenv("npm_config_fund", "false")
			t.Setenv("npm_config_fetch_retries", "0")
			if npm {
				t.Setenv("npm_config_prefer_online", "true")
			}
			if !strings.Contains(origin, "catalog") {
				manifest := `{"name":"node-probe","version":"1.0.0"}`
				if unversioned {
					manifest = `{"name":"node-probe"}`
				}
				writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), manifest)
			}
			writeFile(t, filepath.Join(source, "package.json"), `{"name":"node-probe","version":"1.0.0","dependencies":{"probe-dependency":"1.0.0"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync(process.env.AIPACK_NODE_LIFECYCLE,'root-script-ran')\""}}`)
			packages := map[string]any{"": map[string]any{"name": "node-probe", "version": "1.0.0", "dependencies": map[string]string{"probe-dependency": "1.0.0"}}}
			for name, archive := range archives {
				checksum := sha512.Sum512(archive)
				entry := map[string]any{"version": "1.0.0", "resolved": api.URL + "/" + name + "/-/" + name + "-1.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(checksum[:]), "hasInstallScript": true}
				if name == "probe-dependency" {
					entry["dependencies"] = map[string]string{"probe-leaf": "1.0.0"}
				}
				packages["node_modules/"+name] = entry
			}
			lock := map[string]any{"name": "node-probe", "version": "1.0.0", "lockfileVersion": 3, "packages": packages}
			body, err := json.Marshal(lock)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(source, "package-lock.json"), string(body))
			runtimeCheck := strings.HasSuffix(origin, "-git") || npm
			if runtimeCheck {
				writeFile(t, filepath.Join(source, "SKILL.md"), "---\ndescription: Native unnamed root fixture.\n---\nNATIVE_ROOT_SKILL\n")
				writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"Setup":[{"matcher":"init","hooks":[{"type":"command","command":"node \"${CLAUDE_PLUGIN_ROOT}/setup.js\"","timeout":5}]}]}}`)
				writeFile(t, filepath.Join(source, "setup.js"), `const fs = require('fs'), path = require('path');
const data = process.env.CLAUDE_PLUGIN_DATA;
fs.mkdirSync(data, {recursive: true});
fs.writeFileSync(path.join(data, 'setup.json'), JSON.stringify({root: process.env.CLAUDE_PLUGIN_ROOT, value: 'pending'}));
fs.writeFileSync(path.join(data, 'setup.json'), JSON.stringify({root: process.env.CLAUDE_PLUGIN_ROOT, value: require('probe-dependency')}));
`)
				writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/mcp.js"]}}}`)
				writeFile(t, filepath.Join(source, "mcp.js"), `const fs = require('fs'), path = require('path'), value = require('probe-dependency');
require('readline').createInterface({input: process.stdin}).on('line', line => {
  const request = JSON.parse(line);
  if (!('id' in request)) return;
  let result = {};
  if (request.method === 'initialize') result = {protocolVersion: request.params.protocolVersion, capabilities: {tools: {}}, serverInfo: {name: 'package-fixture', version: '1'}};
  if (request.method === 'tools/list') result = {tools: [{name: 'observe', description: 'Read synthetic package output.', inputSchema: {type: 'object', properties: {}}, annotations: {readOnlyHint: true}}]};
  if (request.method === 'tools/call') {
    fs.mkdirSync(process.env.CLAUDE_PLUGIN_DATA, {recursive: true});
    fs.writeFileSync(path.join(process.env.CLAUDE_PLUGIN_DATA, 'mcp.json'), JSON.stringify({root: process.env.CLAUDE_PLUGIN_ROOT, value}));
    result = {content: [{type: 'text', text: value}]};
  }
  process.stdout.write(JSON.stringify({jsonrpc: '2.0', id: request.id, result}) + '\n');
});
`)
			}
			catalog := filepath.Join(market, ".claude-plugin/marketplace.json")
			writeFile(t, catalog, fmt.Sprintf(`{"name":"node-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"node-probe","version":"1.0.0","source":%q}]}`, payload))
			if unversioned {
				writeFile(t, catalog, fmt.Sprintf(`{"name":"node-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"node-probe","source":%q}]}`, payload))
			}
			publishNPM := func() {
				t.Helper()
				files, err := plugin.ReadFiles(source)
				if err != nil {
					t.Fatal(err)
				}
				var archive bytes.Buffer
				gz := gzip.NewWriter(&archive)
				tw := tar.NewWriter(gz)
				for _, file := range files {
					header := &tar.Header{Name: "package/" + file.Path, Mode: int64(file.Mode.Perm()), Size: int64(len(file.Content))}
					if file.Mode.IsDir() {
						header.Typeflag = tar.TypeDir
					}
					if err := tw.WriteHeader(header); err != nil {
						t.Fatal(err)
					}
					if _, err := tw.Write(file.Content); err != nil {
						t.Fatal(err)
					}
				}
				if err := tw.Close(); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
				npmArchive.Store(archive.Bytes())
			}
			if npm {
				packageSpec := "node-probe"
				switch {
				case strings.Contains(origin, "inline"):
					packageSpec += "@1.0.0"
				case strings.Contains(origin, "alias"):
					packageSpec = "npm:node-probe@1.0.0"
				case strings.Contains(origin, "tarball"):
					packageSpec = api.URL + "/node-probe/-/node-probe-1.0.0.tgz"
				}
				entry := map[string]any{"name": "node-probe", "source": map[string]string{"source": "npm", "package": packageSpec, "registry": api.URL}}
				if !unversioned {
					entry["version"] = "1.0.0"
				}
				body, err := json.Marshal(map[string]any{"name": "node-market", "owner": map[string]string{"name": "shrug-labs"}, "plugins": []any{entry}})
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, catalog, string(body))
				publishNPM()
			}
			record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: nativeHome, Home: home, SettingsPath: settings, MarketplaceDir: market}
			registry := RegistryFetchRequest{ConfigDir: cfgDir, URL: catalog}
			if strings.HasSuffix(origin, "-git") {
				git, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{
					{"init", "--initial-branch=main", market},
					{"-C", market, "add", "."},
					{"-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic native setup fixture"},
					{"clone", "--bare", market, filepath.Join(root, "fixture.git")},
				} {
					cmd := exec.Command(git, args...)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("fixture git %v: %v\n%s", args, err, out)
					}
				}
				server := httptest.NewServer(&cgi.Handler{Path: git, Args: []string{"http-backend"}, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}})
				defer server.Close()
				registry.URL, registry.Path = server.URL+"/fixture.git", ".claude-plugin/marketplace.json"
				record.MarketplaceDir = registry.URL
				if unversioned {
					out, err := exec.Command(git, "-C", market, "rev-parse", "HEAD").Output()
					if err != nil {
						t.Fatal(err)
					}
					skillID = strings.TrimSpace(string(out))[:12]
				}
				if strings.Contains(origin, "local-git") {
					if unversioned {
						skillID += "-c967d508"
					}
					entry := map[string]any{"name": "node-probe", "source": map[string]any{"source": "git-subdir", "url": "file://" + filepath.Join(root, "fixture.git"), "path": "plugins/probe"}}
					body, err := json.Marshal(map[string]any{"name": "node-market", "owner": map[string]string{"name": "shrug-labs"}, "plugins": []any{entry}})
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, catalog, string(body))
					record.MarketplaceDir = market
					registry.URL, registry.Path = catalog, ""
				}
				if strings.Contains(origin, "github") || strings.Contains(origin, "-url-") || strings.Contains(origin, "-subdir-") {
					gitConfig := filepath.Join(root, "github.gitconfig")
					writeFile(t, gitConfig, fmt.Sprintf("[url %q]\n\tinsteadOf = https://github.com/aipack-fixture/probe.git\n\tinsteadOf = https://github.com/aipack-fixture/probe\n\tinsteadOf = git@github.com:aipack-fixture/probe.git\n\tinsteadOf = git@github.com:aipack-fixture/probe\n", "file://"+filepath.Join(root, "fixture.git")))
					t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
					t.Setenv("GIT_ALLOW_PROTOCOL", "file")
					githubSource := map[string]string{"source": "github", "repo": "aipack-fixture/probe"}
					if strings.Contains(origin, "github-tag") {
						for _, args := range [][]string{
							{"-C", market, "tag", "v1.0.0"},
							{"--git-dir=" + filepath.Join(root, "fixture.git"), "fetch", market, "refs/tags/v1.0.0:refs/tags/v1.0.0"},
						} {
							if out, err := exec.Command(git, args...).CombinedOutput(); err != nil {
								t.Fatalf("fixture git tag %v: %v\n%s", args, err, out)
							}
						}
						githubSource["ref"] = "v1.0.0"
					}
					if strings.Contains(origin, "github-ref") {
						githubSource["repo"], githubSource["ref"] = "aipack-fixture/probe.git", "main"
						if err := os.Symlink(filepath.Join(root, "fixture.git"), filepath.Join(root, "fixture.git.git")); err != nil {
							t.Fatal(err)
						}
					}
					if strings.Contains(origin, "github-sha") {
						out, err := exec.Command(git, "-C", market, "rev-parse", "HEAD").Output()
						if err != nil {
							t.Fatal(err)
						}
						githubSource["sha"], githubSource["ref"] = strings.TrimSpace(string(out)), "ignored-missing-branch"
					}
					if strings.Contains(origin, "-url-") {
						githubSource = map[string]string{"source": "url", "url": "https://github.com/aipack-fixture/probe"}
						if strings.Contains(origin, "trailing") {
							githubSource["url"] += "/"
						}
						if strings.Contains(origin, "url-path") {
							githubSource["path"] = "../ignored"
						}
					}
					if strings.Contains(origin, "-subdir-") {
						githubSource = map[string]string{"source": "git-subdir", "url": "aipack-fixture/probe", "path": "plugins/probe"}
						if strings.Contains(origin, "suffixed") {
							githubSource["url"] += ".git"
							if err := os.Symlink(filepath.Join(root, "fixture.git"), filepath.Join(root, "fixture.git.git")); err != nil {
								t.Fatal(err)
							}
						}
					}
					entry := map[string]any{"name": "node-probe", "source": githubSource}
					body, err := json.Marshal(map[string]any{"name": "node-market", "owner": map[string]string{"name": "shrug-labs"}, "plugins": []any{entry}})
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, catalog, string(body))
					record.MarketplaceDir = market
					registry.URL, registry.Path = catalog, ""
				}
			}
			var cache string
			if strings.HasPrefix(origin, "converted") {
				if err := RegistryFetch(context.Background(), registry, nil); err != nil {
					t.Fatal(err)
				}
				entry, err := RegistryLookup(RegistryListRequest{ConfigDir: cfgDir}, "node-probe")
				if err != nil {
					t.Fatal(err)
				}
				if unversioned {
					inspection, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: cfgDir, Input: "node-probe"})
					if err != nil || !slices.Equal(inspection.Skills, []string{skillID}) {
						t.Fatalf("unversioned source inspection lost original revision: %+v %v", inspection, err)
					}
					resources, err := deepIndexOnePack(context.Background(), cfgDir, "probe", entry, func(repo, dst, ref string) error {
						return packsource.EnsureCloneWithRef(context.Background(), repo, dst, ref, "", packsource.RunGit)
					})
					if err != nil || !slices.ContainsFunc(resources, func(resource index.Resource) bool { return resource.Kind == "skill" && resource.Name == skillID }) {
						t.Fatalf("unversioned deep indexing lost original revision: %+v %v", resources, err)
					}
				}
				if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(cfgDir, "probe", entry), nil); err != nil {
					t.Fatal(err)
				}
				if (strings.Contains(origin, "github") || strings.Contains(origin, "-url-") || strings.Contains(origin, "-subdir-")) && os.Getenv("AIPACK_TEST_BINARY") != "" {
					cliConfig := filepath.Join(root, "cli-config")
					writeTestSyncConfig(t, cliConfig)
					cli := func(args ...string) []byte {
						t.Helper()
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, os.Getenv("AIPACK_TEST_BINARY"), append(args, "--config-dir", cliConfig)...)
						cmd.Dir = root
						cmd.Env = append(os.Environ(), "AIPACK_NO_UPDATE_CHECK=1")
						out, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("github CLI %v: %v\n%s", args, err, out)
						}
						return out
					}
					cli("registry", "fetch", catalog, "--deep")
					var inspection PackInspectResult
					if err := json.Unmarshal(cli("pack", "inspect", "node-probe", "--json"), &inspection); err != nil {
						t.Fatal(err)
					}
					cli("pack", "install", "node-probe", "--name", "cli-probe")
					var shown PackShowEntry
					originURL := "https://github.com/aipack-fixture/probe.git"
					if strings.Contains(origin, "-url-") {
						originURL = strings.TrimSuffix(originURL, ".git")
						if strings.Contains(origin, "trailing") {
							originURL += "/"
						}
					}
					if strings.Contains(origin, "github-ref") || strings.Contains(origin, "subdir-suffixed") {
						originURL += ".git"
					}
					if inspection.Source != originURL || inspection.NativePlugin == nil || inspection.NativePlugin.Binding() != "node-probe@node-market" {
						t.Fatalf("Git source CLI inspection changed origin or identity: %+v", inspection)
					}
					if err := json.Unmarshal(cli("pack", "show", "cli-probe", "--json"), &shown); err != nil || shown.Method != config.MethodClone || shown.Origin != originURL {
						t.Fatalf("github CLI changed source identity: %+v %v", shown, err)
					}
					var versions PackListVersionsResult
					if err := json.Unmarshal(cli("pack", "versions", "cli-probe", "--json"), &versions); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(origin, "github-tag") && (versions.InstalledVersion != "1.0.0" || !slices.ContainsFunc(versions.Versions, func(v PackVersion) bool { return v.Version == "v1.0.0" && v.Installed })) {
						t.Fatalf("github CLI lost tag pin: %+v", versions)
					}
					cli("pack", "update", "cli-probe", "--dry-run", "--json")
					cli("pack", "update", "cli-probe")
					cli("pack", "rename", "cli-probe", "cli-renamed")
					cli("pack", "delete", "cli-renamed", "--yes")
				}
				eng := engine.New(nil, nil)
				p, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", cfgDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				if runtimeCheck && !p.Packs[0].NativePlugin.Package.CopiedSource {
					t.Fatal("native import lost copied-source loading")
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: scope, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
				unavailable.Store(strings.HasPrefix(origin, "converted-retry"))
				result, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(origin, "converted-retry") {
					ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
					prior := ledger.NativePlugins["node-probe@node-market"]
					if err != nil || !prior.SetupPending || len(warnings) == 0 {
						t.Fatalf("real npm failure was silently reported as complete: %v %v", err, warnings)
					}
					if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "E403") {
						t.Fatalf("real npm retry failure was not reported: %v", err)
					}
					unavailable.Store(false)
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
						t.Fatalf("real npm setup retry failed: %v %v", err, warnings)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					ready := ledger.NativePlugins["node-probe@node-market"]
					if err != nil || ready.SetupPending || ready.Generation != prior.Generation || ready.CachePath != prior.CachePath {
						t.Fatalf("real npm retry changed source identity or retained pending state: %+v %v", ready, err)
					}
				}
				installed, err := claudeInstalledEntry(record, "node-probe@node-market")
				if err != nil {
					t.Fatal(err)
				}
				cache, _ = installed["installPath"].(string)
				if strings.HasPrefix(origin, "converted-missing-transitive") {
					ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
					prior := ledger.NativePlugins["node-probe@node-market"]
					if err != nil || prior.SetupPending || len(warnings) != 0 {
						t.Fatalf("complete native package tree was reported as incomplete: %v %v", err, warnings)
					}
					if err := util.RemoveOwnedTree(filepath.Join(cache, "node_modules/probe-leaf")); err != nil {
						t.Fatal(err)
					}
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) == 0 {
						t.Fatalf("missing transitive package was silently reported as complete: %v %v", err, warnings)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					pending := ledger.NativePlugins["node-probe@node-market"]
					if err != nil || !pending.SetupPending || pending.CachePath != prior.CachePath || pending.Generation != prior.Generation {
						t.Fatalf("missing transitive package did not retain pending setup and identity: %+v %v", pending, err)
					}
					if _, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil || len(warnings) != 0 {
						t.Fatalf("missing transitive package could not be repaired: %v %v", err, warnings)
					}
					ledger, _, err = eng.LoadLedger(result.Plan.Ledger)
					ready := ledger.NativePlugins["node-probe@node-market"]
					if err != nil || ready.SetupPending || ready.CachePath != prior.CachePath || ready.Generation != prior.Generation {
						t.Fatalf("transitive package repair changed identity: %+v %v", ready, err)
					}
				}
			} else {
				if err := addNativeMarketplace(context.Background(), home, record); err != nil {
					t.Fatal(err)
				}
				cache, err = installNativePlugin(context.Background(), home, record, "node-probe@node-market")
				if err != nil {
					t.Fatal(err)
				}
			}
			if unversioned && filepath.Base(cache) != skillID {
				t.Fatalf("native installation used delivery revision instead of original source revision: %s vs %s", cache, skillID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", `console.log(require('./node_modules/probe-dependency'))`)
			cmd.Dir = cache
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "AIPACK_NODE_READY" {
				t.Fatalf("real installed dependency unavailable: %v\n%s", err, out)
			}
			if runtimeCheck {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "claude", "--init-only", "--no-session-persistence")
				cmd.Dir, cmd.WaitDelay = root, time.Second
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + nativeHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
				out, err := cmd.CombinedOutput()
				body, readErr := os.ReadFile(filepath.Join(nativeHome, "plugins/data/node-probe-node-market/setup.json"))
				var result struct{ Root, Value string }
				if err != nil || readErr != nil || json.Unmarshal(body, &result) != nil || result.Value != "AIPACK_NODE_READY" {
					t.Fatalf("native runtime dependency resolution (%s): %v %v\n%s\n%s", origin, err, readErr, out, body)
				}
				actual, err := filepath.EvalSymlinks(result.Root)
				want, wantErr := filepath.EvalSymlinks(cache)
				if err != nil || wantErr != nil || actual != want {
					t.Fatalf("native runtime root differs from installed cache (%s): %s %v %v", origin, result.Root, err, wantErr)
				}
				runtimeEnv := cmd.Env
				// Only this synthetic operator approves the fixture's read-only tool.
				cmd = exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--permission-mode", "dontAsk", "--allowedTools", "mcp__plugin_node-probe_probe__observe", "--no-session-persistence", "Observe the synthetic package.")
				cmd.Dir, cmd.WaitDelay, cmd.Env = root, time.Second, runtimeEnv
				out, err = cmd.CombinedOutput()
				body, readErr = os.ReadFile(filepath.Join(nativeHome, "plugins/data/node-probe-node-market/mcp.json"))
				if err != nil || readErr != nil || json.Unmarshal(body, &result) != nil || result.Value != "AIPACK_NODE_READY" || canonicalPath(result.Root) != canonicalPath(cache) || !bytes.Contains(out, []byte("AIPACK_NODE_RESPONSE")) {
					t.Fatalf("native MCP dependency resolution (%s): %v %v\n%s\n%s", origin, err, readErr, out, body)
				}
				if !bytes.Contains(out, []byte(fmt.Sprintf(`"node-probe:%s"`, skillID))) {
					t.Fatalf("copied root skill lost native cache-directory identity (%s): %s", origin, out)
				}
				if strings.HasPrefix(origin, "converted") {
					eng := engine.New(nil, nil)
					cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}
					req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: scope, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
					resolve := func() domain.Profile {
						t.Helper()
						p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
						if err != nil {
							t.Fatal(err)
						}
						return p
					}
					writeFile(t, filepath.Join(source, "mcp.js"), strings.Replace(string(mustRead(t, filepath.Join(source, "mcp.js"))), "value = require('probe-dependency')", "value = require('probe-dependency') + '-UPDATED'", 1))
					if npm {
						publishNPM()
					} else {
						for _, args := range [][]string{
							{"-C", market, "add", "."},
							{"-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Update synthetic native package without changing its version"},
							{"--git-dir=" + filepath.Join(root, "fixture.git"), "fetch", market, "main:main"},
						} {
							if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
								t.Fatalf("fixture git update %v: %v\n%s", args, err, out)
							}
						}
					}
					updateRequest := PackUpdateRequest{ConfigDir: cfgDir, Name: "probe"}
					if origin == "converted-unversioned-git" || strings.Contains(origin, "github-sha") || strings.Contains(origin, "github-tag") {
						lock, err := config.LoadLockfile(config.LockfilePath(cfgDir))
						if err != nil {
							t.Fatal(err)
						}
						revision := lock.Packs["probe"].CommitHash
						pinned, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "probe", Ref: revision}, nil, nil)
						if err != nil || len(pinned) != 1 || pinned[0].Status == StatusError {
							t.Fatalf("unversioned source pin failed: %+v %v", pinned, err)
						}
						lock, err = config.LoadLockfile(config.LockfilePath(cfgDir))
						if err != nil {
							t.Fatal(err)
						}
						meta := lock.Packs["probe"]
						meta.ConverterVersion = 0
						lock.Packs["probe"] = meta
						if err := config.SaveLockfile(config.LockfilePath(cfgDir), lock); err != nil {
							t.Fatal(err)
						}
						refreshed, err := PackUpdate(context.Background(), updateRequest, nil, nil)
						lock, loadErr := config.LoadLockfile(config.LockfilePath(cfgDir))
						if err != nil || loadErr != nil || len(refreshed) != 1 || refreshed[0].Status != StatusUpdated || lock.Packs["probe"].Ref != revision || lock.Packs["probe"].CommitHash != revision || lock.Packs["probe"].ConverterVersion != plugin.ConverterVersion || resolve().Packs[0].NativePlugin.Package.CacheVersion != skillID {
							t.Fatalf("unversioned pinned converter refresh changed source identity: %+v %v %v", refreshed, err, loadErr)
						}
						if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil || string(mustRead(t, filepath.Join(nativeHome, "plugins/data/node-probe-node-market/mcp.json"))) != string(body) {
							t.Fatalf("unversioned pinned refresh changed native data: %v", err)
						}
						updateRequest.Ref = "latest"
					}
					updates, err := PackUpdate(context.Background(), updateRequest, nil, nil)
					if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
						t.Fatalf("copied-source same-version update: %+v %v", updates, err)
					}
					prior, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					if unversioned {
						if !npm {
							out, err := exec.Command("git", "-C", market, "rev-parse", "HEAD").Output()
							if err != nil {
								t.Fatal(err)
							}
							skillID = strings.TrimSpace(string(out))[:12]
							if strings.Contains(origin, "local-git") {
								skillID += "-c967d508"
							}
						}
						installed, err := claudeInstalledEntry(record, "node-probe@node-market")
						if err != nil {
							t.Fatal(err)
						}
						cache, _ = installed["installPath"].(string)
						if filepath.Base(cache) != skillID || !slices.Equal(resolve().Packs[0].NativePlugin.Selected[domain.CategorySkills], []string{skillID}) {
							t.Fatalf("unversioned source update lost revised native identity: %s vs %s", cache, skillID)
						}
					}
					updateCtx, updateCancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer updateCancel()
					cmd = exec.CommandContext(updateCtx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--permission-mode", "dontAsk", "--allowedTools", "mcp__plugin_node-probe_probe__observe", "--no-session-persistence", "Observe the synthetic package.")
					cmd.Dir, cmd.WaitDelay, cmd.Env = root, time.Second, runtimeEnv
					out, err = cmd.CombinedOutput()
					body, readErr = os.ReadFile(filepath.Join(nativeHome, "plugins/data/node-probe-node-market/mcp.json"))
					if err != nil || readErr != nil || json.Unmarshal(body, &result) != nil || result.Value != "AIPACK_NODE_READY-UPDATED" || canonicalPath(result.Root) != canonicalPath(cache) || !bytes.Contains(out, []byte("AIPACK_NODE_RESPONSE")) {
						t.Fatalf("copied-source updated MCP execution (%s): %v %v\n%s\n%s", origin, err, readErr, out, body)
					}
					view := prior.Plan.NativePlugins[0].MarketplaceDir
					viewBefore, err := packTreeDigest(view)
					if err != nil {
						t.Fatal(err)
					}
					cacheBefore, err := packTreeDigest(cache)
					if err != nil {
						t.Fatal(err)
					}
					ledgerBefore := mustRead(t, prior.Plan.Ledger)
					cfg.Packs[0].Hooks.Enabled = config.BoolPtr(false)
					cfg.Packs[0].Skills = config.SelectionsToVector([]string{skillID}, nil)
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {Enabled: config.BoolPtr(false)}}
					eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: prior.Plan.Ledger}
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
						t.Fatal("failed copied-source persistence was reported as success")
					}
					viewAfter, viewErr := packTreeDigest(view)
					cacheAfter, cacheErr := packTreeDigest(cache)
					if viewErr != nil || cacheErr != nil || viewAfter != viewBefore || cacheAfter != cacheBefore || !bytes.Equal(ledgerBefore, mustRead(t, prior.Plan.Ledger)) {
						t.Fatalf("copied-source rollback changed view, repository, cache, or ledger: %v %v", viewErr, cacheErr)
					}
					if origin == "converted-git" || origin == "converted-root-git" || strings.Contains(origin, "github") || strings.Contains(origin, "-url-") || strings.Contains(origin, "-subdir-") || npm || unversioned {
						for _, after := range []bool{false, true} {
							eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: prior.Plan.Ledger, After: after}
							func() {
								defer func() {
									if value := recover(); value != "native ledger interruption" {
										t.Fatalf("copied-source persistence interruption: %v", value)
									}
								}()
								_, _, _ = RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
							}()
							if err := recoverNativeOperation(cfgDir); err != nil {
								t.Fatal(err)
							}
							if !after {
								viewAfter, viewErr := packTreeDigest(view)
								cacheAfter, cacheErr := packTreeDigest(cache)
								if viewErr != nil || cacheErr != nil || viewAfter != viewBefore || cacheAfter != cacheBefore || !bytes.Equal(ledgerBefore, mustRead(t, prior.Plan.Ledger)) {
									t.Fatalf("interrupted copied-source rollback changed view, repository, cache, or ledger: %v %v", viewErr, cacheErr)
								}
							}
							if err := filepath.WalkDir(view, func(path string, entry os.DirEntry, err error) error {
								if err == nil && util.IsBackupDir(entry.Name()) {
									return fmt.Errorf("copied-source recovery retained a delivery backup: %s", path)
								}
								return err
							}); err != nil || util.PathExists(nativeOperationDir(cfgDir)) {
								t.Fatalf("copied-source recovery left unfinished delivery: %v", err)
							}
						}
					}
					eng.FS = engine.OSFS{}
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
						t.Fatal(err)
					}
					if util.PathExists(filepath.Join(cache, "SKILL.md")) || bytes.Contains(mustRead(t, filepath.Join(cache, "hooks/hooks.json")), []byte(`"Setup"`)) || bytes.Contains(mustRead(t, filepath.Join(cache, ".mcp.json")), []byte(`"probe"`)) {
						t.Fatal("copied-source selection did not reach the runtime cache")
					}
					if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "probe", Registry: testRegistry()}, nil); err != nil {
						t.Fatal(err)
					}
					if util.PathExists(filepath.Join(view, ".aipack-marketplace/node-probe.git")) || util.PathExists(filepath.Join(cfgDir, "packs/probe")) || string(mustRead(t, filepath.Join(nativeHome, "plugins/data/node-probe-node-market/mcp.json"))) != string(body) {
						t.Fatal("copied-source deletion retained delivery or changed runtime data")
					}
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("native install executed a root or dependency lifecycle script")
			}
		})
	}
}

func TestClaudeNativePluginSync(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 to exercise the native installer")
	}
	for _, homeMode := range []string{"default", "custom", "same-home", "linked-json", "linked-directories"} {
		t.Run(homeMode, func(t *testing.T) {
			testClaudeNativePluginSync(t, homeMode)
		})
	}
}

func testClaudeNativePluginSync(t *testing.T, homeMode string) {
	customHome := homeMode != "default"
	linked := strings.HasPrefix(homeMode, "linked-")
	root := t.TempDir()
	t.Cleanup(func() {
		if err := util.RemoveOwnedTree(root); err != nil {
			t.Error(err)
		}
	})
	source, configDir, home := filepath.Join(root, "market/plugins/probe"), filepath.Join(root, "config"), filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"parity-probe","version":"1.0.0"}`)
	writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Native fixture.\n---\nReturn the marker.\n")
	writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo prompt"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)
	writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"echo"}}}`)
	if linked {
		for _, path := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", ".mcp.json"} {
			linkPath := path
			target := filepath.Join("original", filepath.Base(path))
			modePath := target
			if homeMode == "linked-directories" && path != ".mcp.json" {
				linkPath = filepath.Dir(path)
				target = filepath.Join("original", linkPath)
				modePath = filepath.Join(target, filepath.Base(path))
			}
			if homeMode == "linked-directories" && path == ".mcp.json" {
				target = filepath.Join("original/config", path)
				modePath = target
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(source, target)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(source, linkPath), filepath.Join(source, target)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(source, modePath), 0o440); err != nil {
				t.Fatal(err)
			}
			link, err := filepath.Rel(filepath.Dir(linkPath), target)
			if err != nil {
				t.Fatal(err)
			}
			if homeMode == "linked-directories" && path == ".mcp.json" {
				if err := os.Symlink("original/config", filepath.Join(source, "config")); err != nil {
					t.Fatal(err)
				}
				link = "config/.mcp.json"
			}
			if err := os.Symlink(link, filepath.Join(source, linkPath)); err != nil {
				t.Fatal(err)
			}
		}
	}
	catalogPath := filepath.Join(root, "market/.claude-plugin/marketplace.json")
	writeFile(t, catalogPath, `{"name":"aipack-fixture","owner":{"name":"shrug-labs"},"plugins":[{"name":"parity-probe","source":"./plugins/probe"}]}`)
	if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: catalogPath}, nil); err != nil {
		t.Fatal(err)
	}
	entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "parity-probe")
	if err != nil {
		t.Fatal(err)
	}
	if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "probe", entry), nil); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}
	resolve := func() domain.Profile {
		t.Helper()
		p, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	nativeHome := ""
	if customHome {
		nativeHome = filepath.Join(root, "native-home")
		if homeMode == "same-home" {
			nativeHome = home
		}
	}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
	run := func(p domain.Profile) SyncResult {
		t.Helper()
		result, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	req.DryRun = true
	if len(run(resolve()).Plan.NativePlugins) != 1 {
		t.Fatal("Claude native delivery missing from plan")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("native Claude planning changed installation state")
	}
	if customHome && nativeHome != home && util.PathExists(nativeHome) {
		t.Fatal("native Claude planning created its custom config home")
	}
	req.DryRun = false
	result := run(resolve())
	ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	binding := "parity-probe@aipack-fixture"
	record := ledger.NativePlugins[binding]
	if customHome && (record.ConfigHome != nativeHome || record.SettingsPath != filepath.Join(nativeHome, "settings.json")) {
		t.Fatalf("Claude native delivery ignored custom config home: %+v", record)
	}
	if record.Harness != domain.HarnessClaudeCode || record.Generation == "" || !util.PathExists(record.CachePath) {
		t.Fatalf("Claude native installation missing: %+v", record)
	}
	statePath := filepath.Join(record.ConfigHome, "plugins/installed_plugins.json")
	if linked {
		original, err := plugin.ReadFiles(source)
		if err != nil {
			t.Fatal(err)
		}
		view, err := plugin.ReadFiles(filepath.Join(record.MarketplaceDir, "plugins/probe"))
		if err != nil || !reflect.DeepEqual(original, view) {
			t.Fatal("all-selected native JSON view lost links, modes, or bytes")
		}
	}
	state := mustRead(t, statePath)
	run(resolve())
	if !bytes.Equal(state, mustRead(t, statePath)) {
		t.Fatal("identical sync reinstalled native Claude plugin")
	}
	exclude := []string{"claude-stop"}
	cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
	cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {Enabled: config.BoolPtr(false)}}
	result = run(resolve())
	viewHooks := filepath.Join(record.MarketplaceDir, "plugins/probe/hooks/hooks.json")
	if bytes.Contains(mustRead(t, viewHooks), []byte(`"Stop"`)) {
		t.Fatal("excluded Claude hook event remains in native view")
	}
	if linked {
		for _, path := range []string{"hooks/hooks.json", ".mcp.json"} {
			info, err := os.Lstat(filepath.Join(record.MarketplaceDir, "plugins/probe", path))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o440 {
				t.Fatalf("filtered JSON alias lost target permissions: %v", err)
			}
			target := filepath.Join("original", filepath.Base(path))
			if homeMode == "linked-directories" && path == "hooks/hooks.json" {
				target = filepath.Join("original", path)
			}
			if homeMode == "linked-directories" && path == ".mcp.json" {
				target = filepath.Join("original/config", path)
			}
			if !bytes.Equal(mustRead(t, filepath.Join(source, target)), mustRead(t, filepath.Join(record.MarketplaceDir, "plugins/probe", target))) {
				t.Fatal("filtered JSON changed its original target")
			}
		}
	}
	listed, err := runNativePlugin(context.Background(), home, record, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var native []struct {
		ID  string         `json:"id"`
		MCP map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(listed, &native); err != nil || len(native) != 1 || native[0].ID != binding || len(native[0].MCP) != 0 {
		t.Fatalf("native Claude reload ignored server exclusion: %s %v", listed, err)
	}
	priorHooks, priorLedger := mustRead(t, viewHooks), mustRead(t, result.Plan.Ledger)
	priorState, err := readNativeConfig(statePath)
	if err != nil {
		t.Fatal(err)
	}
	eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
	if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
		t.Fatal("Claude failed ledger persistence was reported as success")
	}
	currentState, err := readNativeConfig(statePath)
	if err != nil || !reflect.DeepEqual(priorState, currentState) || !bytes.Equal(priorHooks, mustRead(t, viewHooks)) || !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) {
		t.Fatalf("Claude rollback changed working state: %v", err)
	}
	eng.FS = engine.OSFS{}
	cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
	data := filepath.Join(record.ConfigHome, "plugins/data/parity-probe-aipack-fixture/marker")
	writeFile(t, data, "retained runtime data")
	for _, interruption := range []struct{ after, remove, cold bool }{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, false}, {false, false, true}, {true, false, true}} {
		t.Logf("Claude interruption after=%t remove=%t cold=%t", interruption.after, interruption.remove, interruption.cold)
		cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
		result = run(resolve())
		if interruption.cold {
			result = run(domain.NewProfile())
		}
		beforeLedger := mustRead(t, result.Plan.Ledger)
		eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: interruption.after}
		cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Exclude: &exclude}
		if interruption.cold {
			cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
		}
		desired := resolve()
		if interruption.remove {
			desired = domain.NewProfile()
		}
		interrupted := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered != "native ledger interruption" {
						panic(recovered)
					}
					interrupted = true
				}
			}()
			if _, _, err := RunSync(context.Background(), eng, desired, req, testRegistry(), nil, nil); err != nil {
				t.Fatal(err)
			}
		}()
		eng.FS = engine.OSFS{}
		if !interrupted {
			t.Fatal("Claude interruption injection did not run")
		}
		unlock, err := lockPackMutation(configDir, false)
		if err != nil {
			t.Fatalf("Claude recovery (after=%t remove=%t cold=%t): %v", interruption.after, interruption.remove, interruption.cold, err)
		}
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
		installed, err := nativePluginInstalled(record, binding)
		wantInstalled := !(interruption.after && interruption.remove || !interruption.after && interruption.cold)
		if err != nil || installed != wantInstalled {
			t.Fatalf("Claude recovery selected the wrong activation: %t %v", installed, err)
		}
		if !wantInstalled {
			snapshots, err := snapshotNativeConfigEntries(nativeMarketplaceTargets(record, "aipack-fixture"))
			if err != nil {
				t.Fatal(err)
			}
			for _, snapshot := range snapshots {
				if snapshot.Present {
					t.Fatal("Claude removal recovery retained a marketplace declaration")
				}
			}
		}
		if wantInstalled && bytes.Contains(mustRead(t, viewHooks), []byte(`"Stop"`)) != (!interruption.after || interruption.cold) {
			t.Fatal("Claude recovery selected the wrong hook generation")
		}
		if !interruption.after && !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
			t.Fatal("Claude rollback changed the prior ledger")
		}
		if string(mustRead(t, data)) != "retained runtime data" {
			t.Fatal("Claude interruption recovery removed runtime data")
		}
		if _, err := os.Stat(nativeOperationDir(configDir)); !os.IsNotExist(err) {
			t.Fatal("recovered Claude operation remained pending")
		}
	}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Exclude: &exclude}
	run(resolve())
	if linked {
		path := filepath.Join(source, "original/hooks.json")
		if homeMode == "linked-directories" {
			path = filepath.Join(source, "original/hooks/hooks.json")
		}
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo updated-linked-source"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)
		if err := os.Chmod(path, 0o440); err != nil {
			t.Fatal(err)
		}
		updated, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "probe"}, nil, nil)
		if err != nil || len(updated) != 1 || updated[0].Status != StatusUpdated {
			t.Fatalf("linked JSON source update failed: %+v %v", updated, err)
		}
		run(resolve())
		if !bytes.Contains(mustRead(t, viewHooks), []byte("updated-linked-source")) || bytes.Contains(mustRead(t, viewHooks), []byte(`"Stop"`)) {
			t.Fatal("linked JSON update lost selection or changed source")
		}
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	req.Scope, req.ProjectDir = domain.ScopeProject, project
	projectResult := run(resolve())
	projectLedger, _, err := eng.LoadLedger(projectResult.Plan.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	projectRecord := projectLedger.NativePlugins[binding]
	if claudeScope(projectRecord) != "local" {
		t.Fatal("project native delivery did not use local scope")
	}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
	if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
		t.Fatal("project selection silently replaced a shared native generation")
	}
	cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
	req.Scope = domain.ScopeGlobal
	run(domain.NewProfile())
	userSettings, err := readNativeConfig(nativeUserConfig(record))
	if err != nil || userSettings["extraKnownMarketplaces"].(map[string]any)["aipack-fixture"] == nil {
		t.Fatalf("global removal changed another scope's marketplace declaration: %v", err)
	}
	installed, err := nativePluginInstalled(projectRecord, binding)
	if err != nil || !installed || !util.PathExists(projectRecord.CachePath) {
		t.Fatalf("global removal changed project installation: %v", err)
	}
	req.Scope = domain.ScopeProject
	if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: req.TargetSpec, Yes: true}, testRegistry()); err != nil {
		t.Fatal(err)
	}
	installed, err = nativePluginInstalled(projectRecord, binding)
	if err != nil || installed {
		t.Fatalf("clean retained native Claude scope: %v", err)
	}
	run(resolve())
	if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "probe", Registry: testRegistry()}, nil); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, data)) != "retained runtime data" {
		t.Fatal("Claude lifecycle removed runtime data")
	}
	if bytes.Contains(mustRead(t, projectRecord.SettingsPath), []byte(binding)) {
		t.Fatal("delete retained Claude project activation")
	}
	// Native installations outside AIPack must fail preflight before changing
	// activation settings, installer state, or the selected source view.
	foreign := record
	foreign.MarketplaceDir = filepath.Join(root, "market")
	if err := addNativeMarketplace(context.Background(), home, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := installNativePlugin(context.Background(), home, foreign, binding); err != nil {
		t.Fatal(err)
	}
	if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "probe", entry), nil); err != nil {
		t.Fatal(err)
	}
	beforeState, beforeSettings := mustRead(t, statePath), mustRead(t, nativeUserConfig(foreign))
	if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside AIPack") {
		t.Fatalf("outside native installation was adopted: %v", err)
	}
	if !bytes.Equal(beforeState, mustRead(t, statePath)) || !bytes.Equal(beforeSettings, mustRead(t, nativeUserConfig(foreign))) {
		t.Fatal("rejected native collision changed host state")
	}
	if customHome && (util.PathExists(filepath.Join(home, ".claude")) || nativeHome != home && util.PathExists(filepath.Join(home, ".claude.json"))) {
		t.Fatal("custom Claude config home leaked state into the default home")
	}
}

func TestNativeAgentPluginSync(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for portable native pack delivery")
	}
	for _, acquisition := range []string{"local", "git", "remote-relative", "remote-relative-legacy"} {
		t.Run(acquisition, func(t *testing.T) {
			remoteRelative := strings.HasPrefix(acquisition, "remote-relative")
			gitSource := acquisition == "git" || remoteRelative
			format := plugin.AgentPlugins
			if acquisition == "remote-relative-legacy" {
				format = plugin.CodexLegacy
			}
			root := t.TempDir()
			t.Cleanup(func() {
				if err := util.RemoveOwnedTree(root); err != nil {
					t.Error(err)
				}
			})
			configDir, home := filepath.Join(root, "config"), filepath.Join(root, "home")
			source := filepath.Join(root, "market/plugins/probe")
			catalog := filepath.Join(root, "market/.agents/plugins/marketplace.json")
			writeFile(t, filepath.Join(source, "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable-probe","version":"1.0.0","extensions":{"com.openai":{"onboardingSkill":"./skills/probe/SKILL.md"}}}`)
			writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: path/name__aipack__skill\ndescription: Portable native fixture.\n---\nORIGINAL_PORTABLE_BODY\n")
			writeFile(t, filepath.Join(source, "mcp.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"python3","args":["${PLUGIN_ROOT}/scripts/server.py"]}}}`)
			writeFile(t, filepath.Join(source, "scripts/server.py"), "print('native fixture asset')\n")
			if format == plugin.CodexLegacy {
				if err := os.Remove(filepath.Join(source, "plugin.json")); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"portable-probe","version":"1.0.0"}`)
				writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"python3","args":["scripts/server.py"],"cwd":"."}}}`)
			}
			readOnlyNativeAsset(t, source)
			writeFile(t, catalog, `{"name":"portable-market","plugins":[{"name":"portable-probe","source":"./plugins/probe"}]}`)
			registry := RegistryFetchRequest{ConfigDir: configDir, URL: catalog}
			git := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "git", args...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("portable fixture git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			firstRevision := ""
			gitRoot := filepath.Join(root, "market")
			if remoteRelative {
				gitRoot = source
			}
			if gitSource {
				git("init", "--initial-branch=main", gitRoot)
				git("-C", gitRoot, "add", ".")
				git("-C", gitRoot, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic portable plugin fixture")
				firstRevision = git("-C", gitRoot, "rev-parse", "HEAD")
				git("clone", "--bare", gitRoot, filepath.Join(root, "fixture.git"))
				if remoteRelative {
					market := filepath.Join(root, "market")
					writeFile(t, catalog, `{"name":"portable-market","plugins":[{"name":"portable-probe","source":{"source":"url","url":"./payload.git"}}]}`)
					git("clone", "--bare", filepath.Join(root, "fixture.git"), filepath.Join(market, "payload.git"))
					git("--git-dir="+filepath.Join(market, "payload.git"), "update-ref", "refs/heads/fixture", "HEAD")
					git("init", "--initial-branch=main", market)
					git("-C", market, "add", ".agents", "payload.git")
					git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Synthetic relative Git marketplace")
					git("clone", "--bare", "--no-local", market, filepath.Join(root, "market.git"))
				}
				binary, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(&cgi.Handler{Path: binary, Args: []string{"http-backend"}, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}})
				defer server.Close()
				registry.URL, registry.Path = server.URL+"/fixture.git", ".agents/plugins/marketplace.json"
				if remoteRelative {
					registry.URL, registry.Format = server.URL+"/market.git", format
				}
			}
			if err := RegistryFetch(context.Background(), registry, nil); err != nil {
				t.Fatal(err)
			}
			entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "portable-probe")
			if err != nil {
				t.Fatal(err)
			}
			if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "alias", entry), nil); err != nil {
				t.Fatal(err)
			}
			if gitSource {
				updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias", Ref: firstRevision}, nil, nil)
				if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated && updates[0].Status != StatusUpToDate {
					t.Fatalf("portable immutable source pin: %+v %v", updates, err)
				}
				lock, err := config.LoadLockfile(config.LockfilePath(configDir))
				if err != nil {
					t.Fatal(err)
				}
				meta := lock.Packs["alias"]
				if meta.Method != config.MethodClone || meta.Ref != firstRevision || meta.CommitHash != firstRevision || meta.Plugin.Format != format {
					t.Fatalf("portable Git source identity: %+v", meta)
				}
				meta.ConverterVersion = 0
				lock.Packs["alias"] = meta
				if err := config.SaveLockfile(config.LockfilePath(configDir), lock); err != nil {
					t.Fatal(err)
				}
				updates, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
				if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
					t.Fatalf("portable pinned converter refresh: %+v %v", updates, err)
				}
				lock, err = config.LoadLockfile(config.LockfilePath(configDir))
				if err != nil || lock.Packs["alias"].Ref != firstRevision || lock.Packs["alias"].CommitHash != firstRevision || lock.Packs["alias"].ConverterVersion != plugin.ConverterVersion {
					t.Fatalf("portable converter refresh moved its immutable pin: %+v %v", lock, err)
				}
			}
			eng := engine.New(nil, nil)
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessCodex}, Env: map[string]string{"CODEX_HOME": ""}}, Yes: true, Quiet: true}
			resolve := func() domain.Profile {
				t.Helper()
				profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				return profile
			}
			run := func(profile domain.Profile) (domain.NativePluginRecord, string) {
				t.Helper()
				result, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				return ledger.NativePlugins["portable-probe@portable-market"], result.Plan.Ledger
			}
			selectContent := func(enabled bool) {
				cfg.Packs[0].Skills = config.VectorSelector{}
				if !enabled {
					cfg.Packs[0].Skills = config.SelectionsToVector([]string{"path/name__aipack__skill"}, nil)
				}
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {Enabled: &enabled}}
			}
			record, _ := run(resolve())
			manifestPath := "plugin.json"
			if format == plugin.CodexLegacy {
				manifestPath = ".codex-plugin/plugin.json"
			}
			if !util.PathExists(filepath.Join(record.CachePath, manifestPath)) || bytes.Contains(mustRead(t, filepath.Join(home, ".codex/config.toml")), []byte("trusted_hash")) {
				t.Fatal("portable native install changed dialect or granted hook trust")
			}
			digest := sha256.Sum256([]byte("portable-market\x00portable-probe"))
			data := filepath.Join(home, ".codex/plugins/data/agent-plugins", fmt.Sprintf("%x", digest), "keep")
			if format == plugin.CodexLegacy {
				data = filepath.Join(home, ".codex/plugins/data/portable-probe-portable-market/keep")
			}
			writeFile(t, data, "portable runtime data")
			before := record.Generation
			for _, after := range []bool{false, true} {
				selectContent(true)
				record, ledgerPath := run(resolve())
				prior := mustRead(t, ledgerPath)
				selectContent(false)
				eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: ledgerPath, After: after}
				lost := false
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							if recovered != "native ledger interruption" {
								panic(recovered)
							}
							lost = true
						}
					}()
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
						t.Fatal(err)
					}
				}()
				eng.FS = engine.OSFS{}
				if !lost {
					t.Fatal("portable native interruption injection did not run")
				}
				if err := recoverNativeOperation(configDir); err != nil {
					t.Fatal(err)
				}
				m, err := plugin.ReadCodex(record.CachePath, "portable-market")
				if err != nil || (len(m.Skills) == 0) != after || (len(m.MCP) == 0) != after || !after && !bytes.Equal(prior, mustRead(t, ledgerPath)) || string(mustRead(t, data)) != "portable runtime data" {
					t.Fatalf("portable recovery chose the wrong capabilities or lost data: %+v %v", m, err)
				}
			}
			selectContent(true)
			reenabled, _ := run(resolve())
			if reenabled.Generation != before {
				t.Fatal("portable re-enable changed the original generation")
			}
			writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: path/name__aipack__skill\ndescription: Portable native fixture.\n---\nUPDATED_PORTABLE_BODY\n")
			update := PackUpdateRequest{ConfigDir: configDir, Name: "alias"}
			if gitSource {
				git("-C", gitRoot, "add", ".")
				git("-C", gitRoot, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Update synthetic portable plugin body")
				git("--git-dir="+filepath.Join(root, "fixture.git"), "fetch", gitRoot, "main:main")
				if remoteRelative {
					market := filepath.Join(root, "market")
					git("--git-dir="+filepath.Join(market, "payload.git"), "fetch", gitRoot, "main:main")
					git("-C", market, "add", "payload.git")
					git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Update contained relative Git source")
					git("--git-dir="+filepath.Join(root, "market.git"), "fetch", market, "main:main")
					lock, err := config.LoadLockfile(config.LockfilePath(configDir))
					if err != nil {
						t.Fatal(err)
					}
					drift := DetectPackDrift(context.Background(), configDir, lock.Packs, nil)
					if len(drift) != 1 || drift[0].Name != "alias" {
						t.Fatalf("relative source doctor missed moved Git head: %+v", drift)
					}
				}
				pinned, err := PackUpdate(context.Background(), update, nil, nil)
				if err != nil || len(pinned) != 1 || pinned[0].Status != StatusUpToDate || bytes.Contains(mustRead(t, filepath.Join(configDir, "packs/alias/upstream/skills/probe/SKILL.md")), []byte("UPDATED_PORTABLE_BODY")) {
					t.Fatalf("portable pinned update advanced its source: %+v %v", pinned, err)
				}
				update.Ref = "latest"
			}
			updates, err := PackUpdate(context.Background(), update, nil, nil)
			if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
				t.Fatalf("portable source update: %+v %v", updates, err)
			}
			updated, _ := run(resolve())
			if updated.Generation == reenabled.Generation || !bytes.Contains(mustRead(t, filepath.Join(updated.CachePath, "skills/probe/SKILL.md")), []byte("UPDATED_PORTABLE_BODY")) {
				t.Fatal("same-version portable update did not reach native cache")
			}
			if gitSource {
				lock, err := config.LoadLockfile(config.LockfilePath(configDir))
				if err != nil || lock.Packs["alias"].Ref != "" || lock.Packs["alias"].CommitHash != git("-C", gitRoot, "rev-parse", "HEAD") {
					t.Fatalf("portable unpin did not acquire the new revision: %+v %v", lock, err)
				}
			}
			if remoteRelative {
				publishCatalog := func(repository, description string) {
					t.Helper()
					writeFile(t, catalog, fmt.Sprintf(`{"name":"portable-market","plugins":[{"name":"portable-probe","description":%q,"source":{"source":"url","url":%q}}]}`, description, repository))
					market := filepath.Join(root, "market")
					git("-C", market, "add", ".agents")
					git("-C", market, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--signoff", "-m", "Update synthetic relative marketplace")
					git("--git-dir="+filepath.Join(root, "market.git"), "fetch", market, "main:main")
				}
				publishCatalog("./payload.git", "Updated relative catalog")
				priorLock := mustRead(t, config.LockfilePath(configDir))
				dry, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias", DryRun: true}, nil, nil)
				if err != nil || len(dry) != 1 || dry[0].Status != StatusUpdated || !bytes.Equal(priorLock, mustRead(t, config.LockfilePath(configDir))) {
					t.Fatalf("relative catalog dry-run changed installed state: %+v %v", dry, err)
				}
				updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
				manifest, loadErr := config.LoadPackManifest(filepath.Join(configDir, "packs/alias/pack.json"))
				if err != nil || loadErr != nil || len(updates) != 1 || updates[0].Status != StatusUpdated || manifest.NativePlugin.MarketplaceEntry["description"] != "Updated relative catalog" {
					t.Fatalf("relative catalog-only refresh failed: %+v %v %v", updates, err, loadErr)
				}
				updated, _ = run(resolve())
				packDir := filepath.Join(configDir, "packs/alias")
				priorFiles, err := plugin.ReadFiles(packDir)
				if err != nil {
					t.Fatal(err)
				}
				assertRetained := func() {
					t.Helper()
					files, err := plugin.ReadFiles(packDir)
					if err != nil || !reflect.DeepEqual(priorFiles, files) || string(mustRead(t, data)) != "portable runtime data" || !bytes.Contains(mustRead(t, filepath.Join(updated.CachePath, "skills/probe/SKILL.md")), []byte("UPDATED_PORTABLE_BODY")) {
						t.Fatalf("failed relative acquisition changed package/cache/data: %v", err)
					}
				}
				publishCatalog("./replacement.git", "Changed coordinates")
				updates, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
				if err != nil || len(updates) != 1 || updates[0].Status != StatusError || !strings.Contains(updates[0].Message, "changed acquisition coordinates") {
					t.Fatalf("changed relative catalog coordinates were adopted: %+v %v", updates, err)
				}
				assertRetained()
				marketBare := filepath.Join(root, "market.git")
				if err := os.Rename(marketBare, marketBare+".offline"); err != nil {
					t.Fatal(err)
				}
				updates, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
				if err != nil || len(updates) != 1 || updates[0].Status != StatusError {
					t.Fatalf("failed relative marketplace acquisition was accepted: %+v %v", updates, err)
				}
				assertRetained()
				if err := os.Rename(marketBare+".offline", marketBare); err != nil {
					t.Fatal(err)
				}
			}
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			req.Scope, req.ProjectDir = domain.ScopeProject, project
			projectRecord, projectLedger := run(resolve())
			projectState := mustRead(t, projectLedger)
			if projectRecord.CachePath != updated.CachePath || !bytes.Contains(mustRead(t, filepath.Join(project, ".codex/config.toml")), []byte("portable-probe@portable-market")) {
				t.Fatal("portable matching project scope changed native identity or lost activation")
			}
			selectContent(false)
			if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !bytes.Equal(projectState, mustRead(t, projectLedger)) || !bytes.Contains(mustRead(t, filepath.Join(updated.CachePath, "skills/probe/SKILL.md")), []byte("UPDATED_PORTABLE_BODY")) {
				t.Fatal("portable conflicting scopes changed owned selection")
			}
			selectContent(true)
			if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: req.TargetSpec, Yes: true}, testRegistry()); err != nil || !util.PathExists(updated.CachePath) || string(mustRead(t, data)) != "portable runtime data" {
				t.Fatalf("portable project cleanup damaged global ownership or data: %v", err)
			}
			req.Scope, req.ProjectDir = domain.ScopeGlobal, root
			if err := PackRename(eng, configDir, "alias", "renamed", io.Discard); err != nil {
				t.Fatal(err)
			}
			cfg.Packs[0].Name = "renamed"
			renamed, _ := run(resolve())
			if renamed.CachePath != updated.CachePath {
				t.Fatal("portable pack rename changed native identity")
			}
			if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "renamed"}, io.Discard); err != nil {
				t.Fatal(err)
			}
			if util.PathExists(updated.CachePath) || string(mustRead(t, data)) != "portable runtime data" {
				t.Fatal("portable deletion retained activation or lost native runtime data")
			}
		})
	}
}

func TestNativePluginSync(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 to exercise the native installer")
	}
	root, source := t.TempDir(), t.TempDir()
	// Interrupted generations are deliberately retained for operator recovery.
	t.Cleanup(func() {
		if err := util.RemoveOwnedTree(root); err != nil {
			t.Error(err)
		}
	})
	writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"parity-probe","version":"1.0.0"}`)
	readOnlyNativeAsset(t, source)
	writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"python3","args":["scripts/server.py"],"cwd":"."}}}`)
	writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo prompt"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)
	writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Use when testing native installation.\nmetadata:\n  owner: shrug-labs\n  last_updated: 2026-10-01\n---\nReturn the fixture marker.\n")
	configDir, home := filepath.Join(root, "config"), filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `{"name":"aipack-fixture","interface":{"displayName":"Native fixture"},"plugins":[{"name":"parity-probe","source":"./","author":{"name":"shrug-labs"},"policy":{"installation":"AVAILABLE","authentication":"ON_USE"}}]}`
	writeFile(t, filepath.Join(source, ".agents/plugins/marketplace.json"), catalog)
	if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: "https://fixture.invalid/market.git", Path: ".agents/plugins/marketplace.json", GitFetchFn: func(string, string, string) ([]byte, error) { return []byte(catalog), nil }}, nil); err != nil {
		t.Fatal(err)
	}
	entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "parity-probe")
	if err != nil {
		t.Fatal(err)
	}
	clone := func(_ context.Context, args ...string) error {
		if len(args) > 0 && args[0] == "clone" && !slices.Contains(args, "--bare") {
			files, err := plugin.ReadFiles(source)
			if err != nil {
				return err
			}
			return plugin.WriteFiles(args[len(args)-1], files)
		}
		return nil
	}
	install := PackInstallRequestFromRegistryEntry(configDir, "probe", entry)
	install.RunGitFn, install.GitHashFn = clone, fakeHashFn(fakeHash1)
	if err := PackInstall(context.Background(), install, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := PackInspect(context.Background(), PackInspectRequest{ConfigDir: configDir, Input: "parity-probe", RunGitFn: clone})
	if err != nil || preview.NativePlugin == nil || len(preview.Hooks) != 2 {
		t.Fatalf("native inspect: %v %v", preview, err)
	}
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe", MCP: map[string]config.MCPServerConfig{
		"probe": {AllowedTools: []string{"observe"}, AlwaysAllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}},
	}}}}
	resolve := func() domain.Profile {
		t.Helper()
		p, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.AllHooks()) != 0 {
			t.Fatal("native hooks entered the portable auto-trust path")
		}
		return p
	}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root,
		Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessCodex}, Env: map[string]string{"CODEX_HOME": ""}}, Yes: true, Quiet: true}
	run := func(p domain.Profile) SyncResult {
		t.Helper()
		result, warnings, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, warning := range warnings {
			if warning.Field == "inventory" {
				t.Fatal(warning.String())
			}
			if warning.Field == "plugin-policy" {
				t.Fatalf("unrestricted sync reported a source approval action: %s", warning.String())
			}
		}
		if !req.DryRun && len(p.Packs) > 0 && p.Packs[0].NativePlugin != nil {
			native := p.Packs[0].NativePlugin
			resources := map[domain.PackCategory][]string{domain.CategoryPlugins: {native.Package.Name}}
			for key, value := range native.Selected {
				resources[key] = value
			}
			for cat, ids := range resources {
				for _, id := range ids {
					traced, err := RunTrace(context.Background(), eng, p, TraceRequest{TargetSpec: req.TargetSpec, ProfileName: "default", ProfileConfig: cfg,
						ResourceType: traceResourceType(cat), ResourceName: id, PackName: p.Packs[0].Name}, testRegistry())
					if err != nil || !traced.Found || traced.Source.NativeBinding != native.Package.Binding() || len(traced.Destinations) != 3 || len(traced.Blockers) != 0 {
						t.Fatalf("native Codex lifecycle trace lost %s %s: %+v %v", cat, id, traced, err)
					}
					for _, destination := range traced.Destinations {
						if destination.DiffKind != domain.DiffIdentical || !util.PathExists(destination.Path) {
							t.Fatalf("native Codex trace misclassified %s %s: %+v", cat, id, destination)
						}
					}
				}
			}
		}
		return result
	}
	req.DryRun = true
	result := run(resolve())
	if len(result.Plan.NativePlugins) != 1 {
		t.Fatal("native package missing from plan")
	}
	summary, err := PlanWithDiffs(context.Background(), eng, resolve(), req, testRegistry())
	if err != nil || summary.NumPlugins != 1 {
		t.Fatalf("native installation missing from classified plan: %+v %v", summary, err)
	}
	for _, warning := range summary.Warnings {
		if warning.Field == "plugin-policy" {
			t.Fatal("unrestricted preview reported a source approval action")
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("dry-run touched native installation")
	}
	if _, err := os.Stat(filepath.Join(configDir, "rendered-plugins")); !os.IsNotExist(err) {
		t.Fatal("planning created a selected package")
	}
	req.DryRun = false
	result = run(resolve())
	ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	record := ledger.NativePlugins["parity-probe@aipack-fixture"]
	if record.Generation == "" || !bytes.Contains(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), []byte(`"Stop"`)) {
		t.Fatal("native install incomplete")
	}
	var renderedCatalog map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(record.MarketplaceDir, ".agents/plugins/marketplace.json")), &renderedCatalog); err != nil {
		t.Fatal(err)
	}
	renderedEntry := renderedCatalog["plugins"].([]any)[0].(map[string]any)
	if renderedEntry["policy"].(map[string]any)["authentication"] != "ON_USE" || renderedCatalog["interface"].(map[string]any)["displayName"] != "Native fixture" {
		t.Fatal("native catalog metadata was discarded")
	}
	if bytes.Contains(mustRead(t, filepath.Join(home, ".codex/config.toml")), []byte("trusted_hash")) {
		t.Fatal("sync granted imported hook trust")
	}
	nativeConfig, err := readNativeConfig(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	nativeEntry := nativeConfig["plugins"].(map[string]any)["parity-probe@aipack-fixture"].(map[string]any)
	serverPolicy := nativeEntry["mcp_servers"].(map[string]any)["probe"].(map[string]any)
	if fmt.Sprint(serverPolicy["enabled_tools"]) != "[observe]" || fmt.Sprint(serverPolicy["disabled_tools"]) != "[hidden]" ||
		serverPolicy["tools"].(map[string]any)["observe"].(map[string]any)["approval_mode"] != "approve" {
		t.Fatalf("profile policy was not delivered under the native binding: %+v", serverPolicy)
	}
	before, err := os.Stat(filepath.Join(record.CachePath, "hooks/hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	run(resolve())
	after, err := os.Stat(filepath.Join(record.CachePath, "hooks/hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() {
		t.Fatal("identical sync reinstalled native package")
	}
	exclude := []string{"codex-stop"}
	cfg.Packs[0].Hooks.VectorSelector.Exclude = &exclude
	run(resolve())
	if bytes.Contains(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), []byte(`"Stop"`)) {
		t.Fatal("native Stop exclusion did not refresh cache")
	}
	data := filepath.Join(home, ".codex/plugins/data/parity-probe-aipack-fixture/keep.txt")
	writeFile(t, data, "user runtime state")
	run(domain.NewProfile())
	if _, err := os.Stat(record.CachePath); !os.IsNotExist(err) {
		t.Fatal("disabled native cache survived")
	}
	if string(mustRead(t, data)) != "user runtime state" {
		t.Fatal("native removal deleted user data")
	}
	removed, err := plugin.ReadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	req.DryRun = true
	run(resolve())
	afterPreview, err := plugin.ReadFiles(root)
	if err != nil || !reflect.DeepEqual(removed, afterPreview) {
		t.Fatalf("reactivation preview changed removed native state: %v", err)
	}
	req.DryRun = false
	run(resolve())
	// One shared native identity can be used by multiple scopes with identical
	// selections. A different selection must fail before altering either cache.
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	req.Scope, req.ProjectDir = domain.ScopeProject, project
	run(resolve())
	all := []string{"codex-user-prompt-submit", "codex-stop"}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Include: &all}
	_, _, err = RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
	if err == nil {
		t.Fatal("project selection silently replaced active global package")
	}
	if bytes.Contains(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), []byte(`"Stop"`)) {
		t.Fatal("failed project conflict changed native cache")
	}
	// Different native homes share the selected source view, but own separate
	// marketplace registrations. Removing one must allow clean reactivation.
	secondProject, secondHome := filepath.Join(root, "second-project"), filepath.Join(root, "second-codex")
	if err := os.MkdirAll(secondProject, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Exclude: &exclude}
	req.ProjectDir, req.Env = secondProject, map[string]string{"CODEX_HOME": secondHome}
	run(resolve())
	run(domain.NewProfile())
	secondConfig, err := readNativeConfig(filepath.Join(secondHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if markets, _ := secondConfig["marketplaces"].(map[string]any); markets["aipack-fixture"] != nil {
		t.Fatal("removal retained an unowned marketplace registration")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), mustRead(t, filepath.Join(record.MarketplaceDir, "plugins/parity-probe/hooks/hooks.json"))) {
		t.Fatal("removal in another native home changed the shared package")
	}
	run(resolve())
	run(domain.NewProfile())
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
	req.ProjectDir, req.Env = project, map[string]string{"CODEX_HOME": ""}
	run(domain.NewProfile())
	req.Scope, req.ProjectDir = domain.ScopeGlobal, root
	run(domain.NewProfile())
	// Cold project-only delivery must not accidentally enable the plugin globally.
	req.Scope, req.ProjectDir = domain.ScopeProject, project
	run(resolve())
	global, err := readNativeConfig(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	plugins, _ := global["plugins"].(map[string]any)
	if plugins["parity-probe@aipack-fixture"] != nil {
		t.Fatal("project installation enabled plugin globally")
	}
	if !bytes.Contains(mustRead(t, filepath.Join(project, ".codex/config.toml")), []byte("parity-probe@aipack-fixture")) {
		t.Fatal("project activation missing")
	}
	// Interrupted native delivery follows the persisted ledger, including
	// cold install, refresh, and removal on either side of its write.
	for _, interruption := range []struct{ after, remove, cold bool }{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, false}, {false, false, true}, {true, false, true}} {
		cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
		result = run(resolve())
		if interruption.cold {
			result = run(domain.NewProfile())
		}
		beforeLedger := mustRead(t, result.Plan.Ledger)
		eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: interruption.after}
		cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Exclude: &exclude}
		if interruption.cold {
			cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
		}
		desired := resolve()
		if interruption.remove {
			desired = domain.NewProfile()
		}
		interrupted := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered != "native ledger interruption" {
						panic(recovered)
					}
					interrupted = true
				}
			}()
			if _, _, err := RunSync(context.Background(), eng, desired, req, testRegistry(), nil, nil); err != nil {
				t.Fatal(err)
			}
		}()
		eng.FS = engine.OSFS{}
		if !interrupted {
			t.Fatal("native interruption injection did not run")
		}
		if unlock, err := lockPackMutation(configDir, false); err != nil {
			t.Fatalf("native interruption recovery (after=%t remove=%t cold=%t): %v", interruption.after, interruption.remove, interruption.cold, err)
		} else if err := unlock(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(nativeOperationDir(configDir)); !os.IsNotExist(err) {
			t.Fatal("recovered native operation remained pending")
		}
		if interruption.after && interruption.remove || !interruption.after && interruption.cold {
			if _, err := os.Stat(record.CachePath); !os.IsNotExist(err) {
				t.Fatal("recovery activated an uncommitted install or rolled back a committed removal")
			}
		} else if hasStop := bytes.Contains(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), []byte(`"Stop"`)); hasStop != (!interruption.after || interruption.cold) {
			t.Fatal("interrupted native update chose the wrong generation")
		}
		if !interruption.after && !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
			t.Fatal("uncommitted recovery changed the prior ledger")
		}
		if string(mustRead(t, data)) != "user runtime state" {
			t.Fatal("interruption recovery removed runtime data")
		}
	}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{}
	result = run(resolve())
	// Failed ledger persistence must restore the previous native generation.
	priorLedger := mustRead(t, result.Plan.Ledger)
	priorHooks := mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json"))
	priorSettings := mustRead(t, filepath.Join(project, ".codex/config.toml"))
	eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
	cfg.Packs[0].Hooks.VectorSelector = config.VectorSelector{Exclude: &exclude}
	_, _, err = RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
	if err == nil {
		t.Fatal("ledger write failure was reported as successful sync")
	}
	if !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) || !bytes.Equal(priorHooks, mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json"))) {
		t.Fatal("failed persistence changed working native generation or ledger")
	}
	_, _, err = RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil)
	if err == nil {
		t.Fatal("failed removal persistence was reported as successful sync")
	}
	if !bytes.Equal(priorHooks, mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json"))) {
		t.Fatal("failed removal destroyed prior native generation")
	}
	if !bytes.Equal(priorSettings, mustRead(t, filepath.Join(project, ".codex/config.toml"))) {
		t.Fatal("failed native apply changed project activation settings")
	}
	failedClean := nativePluginCleanOp{Engine: eng, ConfigDir: configDir, LedgerPath: result.Plan.Ledger}
	if err := failedClean.run(context.Background(), cleanRunContext{Yes: true}); err == nil {
		t.Fatal("failed clean persistence was reported as successful")
	}
	if !bytes.Equal(priorSettings, mustRead(t, filepath.Join(project, ".codex/config.toml"))) || !bytes.Equal(priorHooks, mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json"))) {
		t.Fatal("failed clean changed native activation or package")
	}
	eng.FS = engine.OSFS{}
	run(resolve())
	// A converter refresh must retain an immutable source pin even when the
	// installed commit has not changed.
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["probe"]
	meta.Ref, meta.ConverterVersion = fakeHash1, 0
	lf.Packs["probe"] = meta
	if err := config.SaveLockfile(config.LockfilePath(configDir), lf); err != nil {
		t.Fatal(err)
	}
	updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "probe", RunGitFn: clone, GitHashFn: fakeHashFn(fakeHash1), GitLsRemoteFn: func(context.Context, string, string) (string, error) { return fakeHash1, nil }}, nil, nil)
	if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
		t.Fatalf("pinned converter refresh: %+v %v", updates, err)
	}
	lf, err = config.LoadLockfile(config.LockfilePath(configDir))
	meta = lf.Packs["probe"]
	if err != nil || meta.Ref != fakeHash1 || meta.CommitHash != fakeHash1 || meta.ConverterVersion != plugin.ConverterVersion {
		t.Fatalf("converter refresh changed source pin: %+v %v", meta, err)
	}
	meta.Ref = ""
	lf.Packs["probe"] = meta
	if err := config.SaveLockfile(config.LockfilePath(configDir), lf); err != nil {
		t.Fatal(err)
	}
	// An upstream change with the same native version reconverts through the
	// ordinary update lifecycle, then sync refreshes the selected native cache.
	writeFile(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo upstream-change"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)
	updates, err = PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "probe", RunGitFn: clone, GitHashFn: fakeHashFn(fakeHash2), GitLsRemoteFn: func(context.Context, string, string) (string, error) { return fakeHash2, nil }}, nil, nil)
	if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
		t.Fatalf("plugin update: %v %v", updates, err)
	}
	run(resolve())
	if !bytes.Contains(mustRead(t, filepath.Join(record.CachePath, "hooks/hooks.json")), []byte("upstream-change")) {
		t.Fatal("pack update did not reach native cache")
	}
	lf, err = config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil || lf.Packs["probe"].Plugin == nil || lf.Packs["probe"].ConverterVersion != plugin.ConverterVersion || lf.Packs["probe"].CommitHash != fakeHash2 {
		t.Fatalf("plugin update provenance: %v %v", lf, err)
	}
	if err := PackRename(eng, configDir, "probe", "renamed", io.Discard); err != nil {
		t.Fatal(err)
	}
	cfg.Packs[0].Name = "renamed"
	run(resolve())
	if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: req.TargetSpec, Yes: true}, testRegistry()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.CachePath); !os.IsNotExist(err) {
		t.Fatal("clean retained native activation")
	}
	run(resolve())
	deleted, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "renamed", Registry: testRegistry()}, nil)
	if err != nil || deleted.LedgerCleared == 0 {
		t.Fatalf("native delete: %v %v", deleted, err)
	}
	if _, err := os.Stat(record.CachePath); !os.IsNotExist(err) {
		t.Fatal("delete retained native activation")
	}
	if string(mustRead(t, data)) != "user runtime state" {
		t.Fatal("delete removed native runtime data")
	}
	if bytes.Contains(mustRead(t, filepath.Join(project, ".codex/config.toml")), []byte("parity-probe@aipack-fixture")) {
		t.Fatalf("delete retained project activation: %s", mustRead(t, filepath.Join(project, ".codex/config.toml")))
	}
}

type nativeLedgerFailure struct {
	engine.OSFS
	Path string
}

// A panic models process loss: no in-memory native finalizer executes. The
// next mutation sees only the durable record and the actual persisted ledger.
type nativeLedgerInterruption struct {
	engine.OSFS
	Path  string
	After bool
}

func (f nativeLedgerInterruption) WriteFile(path string, data []byte, mode os.FileMode) error {
	if filepath.Clean(path) == filepath.Clean(f.Path) {
		if f.After {
			if err := f.OSFS.WriteFile(path, data, mode); err != nil {
				return err
			}
		}
		panic("native ledger interruption")
	}
	return f.OSFS.WriteFile(path, data, mode)
}

func (f nativeLedgerFailure) WriteFile(path string, data []byte, mode os.FileMode) error {
	if filepath.Clean(path) == filepath.Clean(f.Path) {
		return fmt.Errorf("injected native ledger persistence failure")
	}
	return f.OSFS.WriteFile(path, data, mode)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
