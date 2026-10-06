package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"net/http"
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
)

func TestCodexNativeNPMSource(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native npm acquisition")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		for _, packageName := range []string{"aipack-probe", "@aipack/probe"} {
			for _, pinned := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/pinned=%t", format, packageName, pinned), func(t *testing.T) {
					root, home, src := t.TempDir(), t.TempDir(), t.TempDir()
					if err := os.MkdirAll(filepath.Join(home, "codex"), 0o700); err != nil {
						t.Fatal(err)
					}
					marker := filepath.Join(home, "lifecycle-ran")
					if format == AgentPlugins {
						write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"npm-probe","version":"1.0.0"}`, 0o644)
					} else {
						write(t, src, ".codex-plugin/plugin.json", `{"name":"npm-probe","version":"1.0.0"}`, 0o644)
					}
					body := fmt.Sprintf(`{"name":%q,"version":"1.0.0","scripts":{"prepack":"node lifecycle.js","postinstall":"node lifecycle.js"},"dependencies":{"never-resolve-me":"1.0.0"}}`, packageName)
					write(t, src, "package.json", body, 0o644)
					write(t, src, "lifecycle.js", `require('fs').writeFileSync(process.env.AIPACK_NPM_MARKER, 'ran');`, 0o755)
					write(t, src, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Use when testing npm acquisition.\n---\nAIPACK_NPM_SKILL\n", 0o644)
					files, err := ReadFiles(src)
					if err != nil {
						t.Fatal(err)
					}
					var archive bytes.Buffer
					gz := gzip.NewWriter(&archive)
					tw := tar.NewWriter(gz)
					for _, file := range files {
						header := &tar.Header{Name: "package/" + file.Path, Mode: int64(file.Mode.Perm()), Size: int64(len(file.Content)), Typeflag: tar.TypeReg}
						if file.Mode.IsDir() {
							header.Typeflag, header.Size = tar.TypeDir, 0
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
					digest := sha512.Sum512(archive.Bytes())
					var registry *httptest.Server
					registry = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						if req.Header.Get("Authorization") != "Bearer fixture-owned-token" {
							http.Error(w, "fixture registry requires npm credentials", http.StatusUnauthorized)
							return
						}
						if req.URL.Path == "/archive.tgz" {
							_, _ = w.Write(archive.Bytes())
							return
						}
						if req.URL.Path != "/"+packageName {
							http.NotFound(w, req)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						metadata := map[string]any{"name": packageName, "dist-tags": map[string]string{"latest": "1.0.0"}, "versions": map[string]any{"1.0.0": map[string]any{"name": packageName, "version": "1.0.0", "dist": map[string]string{"tarball": registry.URL + "/archive.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(digest[:])}}}}
						_ = json.NewEncoder(w).Encode(metadata)
					}))
					defer registry.Close()
					write(t, home, "ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: registry.Certificate().Raw})), 0o600)
					write(t, home, "npmrc", "//"+strings.TrimPrefix(registry.URL, "https://")+"/:_authToken=fixture-owned-token\n", 0o600)
					env := slices.DeleteFunc(os.Environ(), func(value string) bool {
						key := strings.SplitN(value, "=", 2)[0]
						return key == "HOME" || key == "CODEX_HOME" || strings.HasPrefix(strings.ToLower(key), "npm_config_")
					})
					env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, "codex"), "AIPACK_NPM_MARKER="+marker, "npm_config_cache="+filepath.Join(home, "npm-cache"), "npm_config_userconfig="+filepath.Join(home, "npmrc"), "npm_config_cafile="+filepath.Join(home, "ca.pem"), "npm_config_fetch_retries=0")
					source := map[string]any{"source": "npm", "package": packageName, "registry": registry.URL}
					if pinned {
						source["version"] = "1.0.0"
					}
					catalog := map[string]any{"name": "npm-market", "plugins": []any{map[string]any{"name": "npm-probe", "source": source}}}
					catalogBody, err := json.Marshal(catalog)
					if err != nil {
						t.Fatal(err)
					}
					write(t, root, ".agents/plugins/marketplace.json", string(catalogBody), 0o644)
					for _, args := range [][]string{{"plugin", "marketplace", "add", root, "--json"}, {"plugin", "add", "npm-probe@npm-market", "--json"}} {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						cmd := exec.CommandContext(ctx, "codex", args...)
						cmd.Env, cmd.Dir, cmd.WaitDelay = env, root, time.Second
						output, err := cmd.CombinedOutput()
						cancel()
						if err != nil {
							t.Fatalf("%v: %v\n%s", args, err, output)
						}
					}
					var acquired string
					cache := filepath.Join(home, "codex/plugins/cache/npm-market/npm-probe")
					err = filepath.WalkDir(cache, func(path string, entry fs.DirEntry, err error) error {
						if err == nil && entry.Name() == "plugin.json" {
							acquired = filepath.Dir(path)
							if filepath.Base(acquired) == ".codex-plugin" {
								acquired = filepath.Dir(acquired)
							}
						}
						return err
					})
					if err != nil || acquired == "" {
						t.Fatalf("native npm cache missing: %s %v", cache, err)
					}
					if _, err := os.Stat(filepath.Join(acquired, "node_modules")); !os.IsNotExist(err) {
						t.Fatalf("native package acquisition installed dependencies: %v", err)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatalf("native acquisition executed package scripts: %v", err)
					}
					for _, value := range env {
						key, value, _ := strings.Cut(value, "=")
						if key == "HOME" || key == "AIPACK_NPM_MARKER" || strings.HasPrefix(key, "npm_config_") {
							t.Setenv(key, value)
						}
					}
					spec := domain.NPMSource{Package: packageName, Registry: registry.URL}
					if pinned {
						spec.Version = "1.0.0"
					}
					fetched, version, archiveHash, err := sourcepkg.FetchNPM(context.Background(), spec, t.TempDir())
					if err != nil || version != "1.0.0" || len(archiveHash) != 64 {
						t.Fatalf("AIPack npm acquisition differs: %s %s %v", version, archiveHash, err)
					}
					fetchedFiles, err := ReadFiles(fetched)
					if err != nil || !reflect.DeepEqual(fetchedFiles, files) {
						t.Fatalf("AIPack npm acquisition changed source bytes/modes: %v", err)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatalf("AIPack acquisition executed package scripts: %v", err)
					}
					m, err := MaterializeCodex(acquired, filepath.Join(home, "pack"), "alias", "npm-market")
					if err != nil {
						t.Fatal(err)
					}
					rendered, err := RenderCodex(domain.NativePluginSelection{Package: *m.NativePlugin, Root: filepath.Join(home, "pack"), Selected: map[domain.PackCategory][]string{domain.CategorySkills: {"probe"}}, SettingsEnabled: true})
					if err != nil || !reflect.DeepEqual(rendered, files) {
						t.Fatalf("npm conversion changed acquired payload: %v", err)
					}
					raw := nativeRequest(t, "codex", root, env, "plugin/read", map[string]any{"marketplacePath": filepath.Join(root, ".agents/plugins/marketplace.json"), "pluginName": "npm-probe"})
					var read struct {
						Plugin struct {
							Skills []struct{ Name string } `json:"skills"`
						} `json:"plugin"`
					}
					if err := json.Unmarshal(raw, &read); err != nil {
						t.Fatal(err)
					}
					if len(read.Plugin.Skills) != 1 || strings.TrimPrefix(read.Plugin.Skills[0].Name, "npm-probe:") != "probe" {
						t.Fatalf("native npm skill missing: %s", raw)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatalf("native read executed package scripts: %v", err)
					}
				})
			}
		}
	}
}

func TestClaudeNativeNPMSource(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for Claude npm acquisition")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Fatal(err)
	}
	src, credentials := t.TempDir(), t.TempDir()
	marker := filepath.Join(credentials, "lifecycle-ran")
	write(t, src, claudeManifest, `{"name":"npm-probe","version":"3.0.0"}`, 0o644)
	write(t, src, "package.json", `{"name":"@aipack/probe","version":"1.0.0","scripts":{"prepack":"node lifecycle.js","postinstall":"node lifecycle.js"},"dependencies":{"never-resolve-me":"1.0.0"}}`, 0o644)
	write(t, src, "lifecycle.js", `require('fs').writeFileSync(process.env.AIPACK_NPM_MARKER, 'ran');`, 0o755)
	write(t, src, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Native npm fixture.\n---\nCLAUDE_NPM_BODY\n", 0o644)
	files, err := ReadFiles(src)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(files []File) []byte {
		t.Helper()
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		for _, file := range files {
			header := &tar.Header{Name: "package/" + file.Path, Mode: int64(file.Mode.Perm()), Size: int64(len(file.Content)), Typeflag: tar.TypeReg}
			if file.Mode.IsDir() {
				header.Typeflag, header.Size = tar.TypeDir, 0
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
		return archive.Bytes()
	}
	var archive atomic.Value
	archive.Store(encode(files))
	dependency := encode([]File{{Path: "package.json", Content: []byte(`{"name":"never-resolve-me","version":"1.0.0","main":"index.js","scripts":{"postinstall":"node -e \"require('fs').writeFileSync(process.env.AIPACK_NPM_MARKER,'dependency')\""}}`), Mode: 0o644}, {Path: "index.js", Content: []byte(`module.exports='CLAUDE_NPM_DEPENDENCY';`), Mode: 0o644}})
	var requests, dependencyDownloads atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.Header.Get("Authorization") != "Bearer fixture-owned-token" {
			http.Error(w, "fixture requires npm credentials", http.StatusUnauthorized)
			return
		}
		if req.URL.Path == "/archive.tgz" {
			_, _ = w.Write(archive.Load().([]byte))
			return
		}
		if req.URL.Path == "/dependency.tgz" {
			dependencyDownloads.Add(1)
			_, _ = w.Write(dependency)
			return
		}
		if req.URL.Path != "/@aipack/probe" {
			http.NotFound(w, req)
			return
		}
		origin := "http://" + req.Host
		if req.TLS != nil {
			origin = "https://" + req.Host
		}
		digest := sha512.Sum512(archive.Load().([]byte))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "@aipack/probe", "dist-tags": map[string]string{"latest": "1.0.0", "stable": "1.0.0"}, "versions": map[string]any{"1.0.0": map[string]any{"name": "@aipack/probe", "version": "1.0.0", "dist": map[string]string{"tarball": origin + "/archive.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(digest[:])}}}})
	})
	registry := httptest.NewTLSServer(handler)
	defer registry.Close()
	plainRegistry := httptest.NewServer(handler)
	defer plainRegistry.Close()
	write(t, credentials, "ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: registry.Certificate().Raw})), 0o600)
	write(t, credentials, "npmrc", "//"+strings.TrimPrefix(registry.URL, "https://")+"/:_authToken=fixture-owned-token\n//"+strings.TrimPrefix(plainRegistry.URL, "http://")+"/:_authToken=fixture-owned-token\n", 0o600)
	t.Setenv("npm_config_cache", filepath.Join(credentials, "npm-cache"))
	t.Setenv("npm_config_userconfig", filepath.Join(credentials, "npmrc"))
	t.Setenv("npm_config_cafile", filepath.Join(credentials, "ca.pem"))
	t.Setenv("npm_config_registry", registry.URL)
	t.Setenv("npm_config_fetch_retries", "0")
	t.Setenv("AIPACK_NPM_MARKER", marker)
	install := func(home, cwd string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "plugin", "install", "npm-probe@npm-market", "--json")
		cmd.Dir = cwd
		cmd.Env = slices.DeleteFunc(os.Environ(), func(value string) bool {
			key := strings.SplitN(value, "=", 2)[0]
			return key == "HOME" || key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"
		})
		cmd.Env = append(cmd.Env, "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
		return cmd.CombinedOutput()
	}
	for _, test := range []struct {
		name, pkg, version string
		locked, plain      bool
	}{
		{"latest", "@aipack/probe", "", false, false},
		{"default-registry", "@aipack/probe", "", false, false},
		{"empty-registry-fails", "@aipack/probe", "", false, false},
		{"exact", "@aipack/probe", "1.0.0", false, false},
		{"range", "@aipack/probe", "^1.0.0", false, false},
		{"tag", "@aipack/probe", "stable", false, false},
		{"inline", "@aipack/probe@1.0.0", "", false, false},
		{"inline-with-version-fails", "@aipack/probe@1.0.0", "9.0.0", false, false},
		{"alias", "npm:@aipack/probe", "", false, false},
		{"tarball", registry.URL + "/archive.tgz", "", false, false},
		{"tarball-with-version-fails", registry.URL + "/archive.tgz", "9.0.0", false, false},
		{"http-nondefault-tarball", plainRegistry.URL + "/archive.tgz", "", false, false},
		{"http-default-registry", "@aipack/probe", "", false, true},
		{"http-nondefault-registry", "@aipack/probe", "", false, true},
		{"http-default-tarball", plainRegistry.URL + "/archive.tgz", "", false, true},
		{"locked-exact", "@aipack/probe", "1.0.0", true, false},
		{"locked-tarball", registry.URL + "/archive.tgz", "", true, false},
		{"unversioned", "@aipack/probe", "1.0.0", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, market, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			registryURL := registry.URL
			if test.plain {
				registryURL = plainRegistry.URL
			}
			t.Setenv("npm_config_registry", registryURL)
			if test.name == "http-nondefault-registry" {
				t.Setenv("npm_config_registry", registry.URL)
			}
			t.Setenv("npm_config_cache", filepath.Join(home, "npm-cache"))
			expected := slices.Clone(files)
			expectedVersion := "3.0.0"
			if test.name == "unversioned" {
				expectedVersion = "unknown"
				for i := range expected {
					if expected[i].Path == claudeManifest {
						expected[i].Content = []byte(`{"name":"npm-probe"}`)
					}
				}
			}
			if test.locked {
				lock := fmt.Sprintf(`{"name":"@aipack/probe","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"@aipack/probe","version":"1.0.0","dependencies":{"never-resolve-me":"1.0.0"}},"node_modules/never-resolve-me":{"version":"1.0.0","resolved":%q,"hasInstallScript":true}}}`, registryURL+"/dependency.tgz")
				expected = append(slices.Clone(files), File{Path: "package-lock.json", Content: []byte(lock), Mode: 0o644})
				slices.SortFunc(expected, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
			}
			archive.Store(encode(expected))
			before := dependencyDownloads.Load()
			sourceRegistry := registryURL
			if test.name == "default-registry" || test.name == "empty-registry-fails" {
				sourceRegistry = ""
			}
			source := map[string]string{"source": "npm", "package": test.pkg, "version": test.version, "registry": sourceRegistry}
			if test.name == "default-registry" {
				delete(source, "registry")
			}
			catalog := map[string]any{"name": "npm-market", "owner": map[string]string{"name": "Fixture"}, "plugins": []any{map[string]any{"name": "npm-probe", "source": source}}}
			body, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			write(t, market, ".claude-plugin/marketplace.json", string(body), 0o644)
			claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
			spec := domain.NPMSource{Package: test.pkg, Version: test.version, Registry: sourceRegistry}
			parsed, err := config.ParseMarketplace(body, config.RegistrySourceEntry{URL: market, Path: ".claude-plugin/marketplace.json"})
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "empty-registry-fails" {
				before := requests.Load()
				if output, err := install(home, cwd); err == nil || !bytes.Contains(output, []byte("registry")) || requests.Load() != before || parsed.Packs["npm-probe"].Unsupported == "" {
					t.Fatalf("empty registry did not fail before acquisition: %s %v", output, err)
				}
				return
			}
			if test.name == "inline-with-version-fails" || test.name == "tarball-with-version-fails" {
				if parsed.Packs["npm-probe"].Unsupported == "" {
					t.Fatal("marketplace parser accepted a conflicting native selector")
				}
				if output, err := install(home, cwd); err == nil || !bytes.Contains(output, []byte("9.0.0")) {
					t.Fatalf("native accepted conflicting selector: %s %v", output, err)
				}
				if _, _, _, err := sourcepkg.FetchClaudeNPM(context.Background(), spec, t.TempDir()); err == nil {
					t.Fatal("AIPack accepted a conflicting native selector")
				}
				return
			}
			claudeNative(t, home, cwd, "plugin", "install", "npm-probe@npm-market", "--json")
			if parsed.Packs["npm-probe"].Unsupported != "" || len(config.ValidateRegistry(parsed)) != 0 {
				t.Fatalf("marketplace parser rejected a native npm source: %+v", parsed.Packs["npm-probe"])
			}
			listed := claudeNative(t, home, cwd, "plugin", "list", "--json")
			var installed []struct {
				ID, Version, InstallPath string
			}
			if err := json.Unmarshal(listed, &installed); err != nil || len(installed) != 1 || installed[0].ID != "npm-probe@npm-market" || installed[0].Version != expectedVersion {
				t.Fatalf("native npm identity: %s %v", listed, err)
			}
			cache := installed[0].InstallPath
			if cache == "" {
				cache = filepath.Join(home, ".claude/plugins/cache/npm-market/npm-probe", expectedVersion)
			}
			acquired, err := ReadFiles(cache)
			if err != nil {
				t.Fatal(err)
			}
			// Native loading adds an empty cache-use directory, not package content.
			payload := slices.DeleteFunc(slices.Clone(acquired), func(file File) bool {
				return file.Path == ".in_use" && file.Mode.IsDir() || test.locked && (file.Path == "node_modules" || strings.HasPrefix(file.Path, "node_modules/"))
			})
			if !reflect.DeepEqual(payload, expected) {
				t.Fatal("native npm changed source bytes/modes or installed dependencies")
			}
			if test.locked {
				cmd := exec.Command("node", "-e", "process.stdout.write(require('./node_modules/never-resolve-me'))")
				cmd.Dir = cache
				if output, err := cmd.CombinedOutput(); err != nil || string(output) != "CLAUDE_NPM_DEPENDENCY" || dependencyDownloads.Load() <= before {
					t.Fatalf("native locked dependency loading: %s %v", output, err)
				}
			} else if dependencyDownloads.Load() != before {
				t.Fatal("native acquisition fetched an unlocked dependency")
			}
			beforeFetch := dependencyDownloads.Load()
			fetched, packageVersion, archiveHash, err := sourcepkg.FetchClaudeNPM(context.Background(), spec, t.TempDir())
			if err != nil || packageVersion != "1.0.0" || len(archiveHash) != 64 {
				t.Fatalf("AIPack Claude npm acquisition: %s %s %v", packageVersion, archiveHash, err)
			}
			fetchedFiles, err := ReadFiles(fetched)
			if err != nil || !reflect.DeepEqual(fetchedFiles, expected) || dependencyDownloads.Load() != beforeFetch {
				t.Fatalf("AIPack npm acquisition changed bytes/modes or installed dependencies: %v", err)
			}
			pack := t.TempDir()
			m, err := MaterializeClaude(cache, pack, "alias", domain.PluginSource{Marketplace: "npm-market"})
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := RenderClaude(selection(m, pack))
			if err != nil || !reflect.DeepEqual(rendered, acquired) {
				t.Fatalf("npm conversion changed native payload: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("native npm ran lifecycle scripts: %v", err)
			}
		})
	}
	for _, pkg := range []string{"file:./probe", "git+https://example.invalid/probe.git"} {
		t.Run("refused/"+pkg, func(t *testing.T) {
			home, market, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			body := fmt.Sprintf(`{"name":"npm-market","owner":{"name":"Fixture"},"plugins":[{"name":"npm-probe","source":{"source":"npm","package":%q,"registry":%q}}]}`, pkg, registry.URL)
			write(t, market, ".claude-plugin/marketplace.json", body, 0o644)
			claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
			before := requests.Load()
			output, err := install(home, cwd)
			if err == nil || !bytes.Contains(output, []byte(pkg)) || requests.Load() != before {
				t.Fatalf("native npm did not refuse before acquisition: %s %v", output, err)
			}
			if _, _, _, err := sourcepkg.FetchClaudeNPM(context.Background(), domain.NPMSource{Package: pkg, Registry: registry.URL}, t.TempDir()); err == nil || requests.Load() != before {
				t.Fatalf("AIPack did not refuse before acquisition: %v", err)
			}
		})
	}
}
