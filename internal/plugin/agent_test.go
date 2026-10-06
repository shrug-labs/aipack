package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCodexNativeAgentPluginRuntime(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for portable MCP and skill execution")
	}
	for _, transport := range []string{"stdio", "http"} {
		for _, converted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/converted=%t", transport, converted), func(t *testing.T) {
				root, src, home := t.TempDir(), t.TempDir(), t.TempDir()
				home, err := filepath.EvalSymlinks(home)
				if err != nil {
					t.Fatal(err)
				}
				agentFixture(t, src, "root")
				if err := os.Chmod(filepath.Join(src, "skills/setup/SKILL.md"), 0o640); err != nil {
					t.Fatal(err)
				}
				write(t, src, "skills/setup/SKILL.md", "---\nname: renamed-setup\ndescription: Use when checking native frontmatter invocation names.\n---\nAIPACK_PORTABLE_setup\n", 0o440)
				if err := os.Chmod(filepath.Join(src, "mcp.json"), 0o640); err != nil {
					t.Fatal(err)
				}
				write(t, src, "mcp.json", `{"$schema":"`+agentMCPSchema+`","mcpServers":{"probe":{"type":"stdio","command":"python3","args":["${PLUGIN_ROOT}/scripts/server.py"],"env":{"MARKER":"${PLUGIN_DATA}/marker","AIPACK_FIXTURE_TOKEN":"${AIPACK_FIXTURE_TOKEN}"}}}}`, 0o440)
				write(t, src, ".codex-plugin/plugin.json", `{"name":"inert-legacy","mcpServers":{"probe":{"command":"python3","env_vars":["AIPACK_FIXTURE_TOKEN"]}}}`, 0o644)
				write(t, src, "scripts/server.py", `import json, os, pathlib, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "portable", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": "observe", "description": "Inspect the portable fixture.", "inputSchema": {"type": "object", "properties": {}}}]}
    elif method == "tools/call":
        observed = {"root": os.environ["PLUGIN_ROOT"], "data": os.environ["PLUGIN_DATA"], "cwd": os.getcwd(), "marker": os.environ["MARKER"], "token": os.environ.get("AIPACK_FIXTURE_TOKEN", "missing")}
        data = pathlib.Path(observed["data"])
        data.mkdir(parents=True, exist_ok=True)
        with (data / "calls.jsonl").open("a") as log:
            log.write(json.dumps(observed) + "\n")
        result = {"content": [{"type": "text", "text": "AIPACK_PORTABLE_TOOL_RESULT"}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
				var mu sync.Mutex
				var httpCalls []http.Header
				if transport == "http" {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						if req.Method != http.MethodPost {
							w.WriteHeader(http.StatusMethodNotAllowed)
							return
						}
						var request struct {
							ID     json.RawMessage `json:"id"`
							Method string          `json:"method"`
							Params struct {
								ProtocolVersion string `json:"protocolVersion"`
							} `json:"params"`
						}
						if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						if len(request.ID) == 0 {
							w.WriteHeader(http.StatusAccepted)
							return
						}
						result := map[string]any{}
						switch request.Method {
						case "initialize":
							result = map[string]any{"protocolVersion": request.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "portable-http", "version": "1"}}
						case "tools/list":
							result = map[string]any{"tools": []any{map[string]any{"name": "observe", "description": "Inspect the portable fixture.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
						case "tools/call":
							mu.Lock()
							httpCalls = append(httpCalls, req.Header.Clone())
							mu.Unlock()
							result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "AIPACK_PORTABLE_TOOL_RESULT"}}}
						}
						w.Header().Set("Content-Type", "application/json")
						if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
							t.Error(err)
						}
					}))
					defer server.Close()
					body, err := json.Marshal(map[string]any{"$schema": agentMCPSchema, "mcpServers": map[string]any{"probe": map[string]any{"type": "streamable-http", "url": server.URL + "/mcp", "headers": map[string]string{"X-Probe": "native-http", "Authorization": "fixture-owned-header"}}}})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(filepath.Join(src, "mcp.json"), 0o640); err != nil {
						t.Fatal(err)
					}
					write(t, src, "mcp.json", string(body), 0o440)
				}
				sawSkill := false
				var lastRequest []byte
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					sawSkill = sawSkill || bytes.Contains(body, []byte("AIPACK_PORTABLE_setup"))
					lastRequest = body
					mu.Unlock()
					if bytes.Contains(body, []byte(`"name":"mcp__probe"`)) && !bytes.Contains(body, []byte("AIPACK_PORTABLE_TOOL_RESULT")) && !bytes.Contains(body, []byte(`"type":"function_call_output"`)) {
						nativeResponse(w, map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "namespace": "mcp__probe", "name": "observe", "arguments": "{}"})
						return
					}
					nativeResponse(w, map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "AIPACK_RESPONSE", "annotations": []any{}}}})
				}))
				defer api.Close()
				if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex"), "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost", "AIPACK_FIXTURE_TOKEN=fixture-token"}
				run := func(args ...string) []byte {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, "codex", args...)
					cmd.Env, cmd.Dir, cmd.WaitDelay = env, root, time.Second
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("portable execution %v: %v\n%s", args, err, out)
					}
					return out
				}
				market := filepath.Join(home, "market")
				write(t, market, ".agents/plugins/marketplace.json", `{"name":"agent-market","plugins":[{"name":"portable-probe","source":"./plugins/probe"}]}`, 0o644)
				files, err := ReadFiles(src)
				if converted {
					original := files
					pack := t.TempDir()
					m, convertErr := MaterializeCodex(src, pack, "alias", "agent-market")
					if convertErr != nil {
						t.Fatal(convertErr)
					}
					files, err = RenderCodex(selection(m, pack))
					if err == nil && !reflect.DeepEqual(files, original) {
						t.Fatal("all-selected executable MCP delivery changed original source bytes/modes")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := WriteFiles(filepath.Join(market, "plugins/probe"), files); err != nil {
					t.Fatal(err)
				}
				run("plugin", "marketplace", "add", market, "--json")
				run("plugin", "add", "portable-probe@agent-market", "--json")
				var cfg map[string]any
				if err := toml.Unmarshal(mustBytes(t, filepath.Join(home, ".codex/config.toml")), &cfg); err != nil {
					t.Fatal(err)
				}
				// The synthetic operator explicitly approves this read-only tool.
				entry := cfg["plugins"].(map[string]any)["portable-probe@agent-market"].(map[string]any)
				entry["mcp_servers"] = map[string]any{"probe": map[string]any{"enabled": true, "enabled_tools": []string{"observe"}, "tools": map[string]any{"observe": map[string]any{"approval_mode": "approve"}}}}
				configBytes, err := toml.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				write(t, home, ".codex/config.toml", string(configBytes), 0o600)
				var output []byte
				for range 2 {
					out := run("exec", "--skip-git-repo-check", "--json",
						"-c", `model_provider="aipack-test"`, "-c", `model="fixture-model"`, "-c", `model_providers.aipack-test.name="aipack-test"`,
						"-c", `model_providers.aipack-test.base_url="`+api.URL+`/v1"`, "-c", `model_providers.aipack-test.wire_api="responses"`,
						"-c", `model_providers.aipack-test.requires_openai_auth=false`, "$portable-probe:renamed-setup Run the portable fixture.")
					if !bytes.Contains(out, []byte("AIPACK_RESPONSE")) {
						t.Fatalf("portable runtime response missing: %s", out)
					}
					output = append(output, out...)
				}
				mu.Lock()
				loaded := sawSkill
				mu.Unlock()
				if !loaded {
					t.Fatal("portable namespaced skill did not load its original body")
				}
				if transport == "http" {
					mu.Lock()
					defer mu.Unlock()
					if len(httpCalls) != 2 {
						t.Fatalf("portable HTTP restart lost or repeated calls: %d\n%s", len(httpCalls), output)
					}
					for _, headers := range httpCalls {
						if headers.Get("X-Probe") != "native-http" || headers.Get("Authorization") == "fixture-owned-header" {
							t.Fatalf("portable HTTP headers differ: %v", headers)
						}
					}
					return
				}
				digest := sha256.Sum256([]byte("agent-market\x00portable-probe"))
				data := filepath.Join(home, ".codex/plugins/data/agent-plugins", fmt.Sprintf("%x", digest), "calls.jsonl")
				if _, err := os.Stat(data); err != nil {
					mu.Lock()
					var request map[string]json.RawMessage
					_ = json.Unmarshal(lastRequest, &request)
					t.Logf("portable native tools: %s", request["tools"])
					mu.Unlock()
					t.Fatalf("portable MCP did not execute: %v\n%s", err, output)
				}
				calls := bytes.Split(bytes.TrimSpace(mustBytes(t, data)), []byte("\n"))
				if len(calls) != 2 {
					t.Fatalf("portable restart lost or repeated tool execution: %d", len(calls))
				}
				for _, call := range calls {
					var observed map[string]string
					if err := json.Unmarshal(call, &observed); err != nil {
						t.Fatal(err)
					}
					cache := filepath.Join(home, ".codex/plugins/cache/agent-market/portable-probe/1.0.0")
					if observed["root"] != cache || observed["cwd"] != cache || observed["data"] != filepath.Dir(data) || observed["marker"] != filepath.Join(filepath.Dir(data), "marker") || observed["token"] != "fixture-token" {
						t.Fatalf("portable MCP environment/data differ: %+v", observed)
					}
				}
			})
		}
	}
}

func agentFixture(t *testing.T, root, extension string) {
	t.Helper()
	fields := ""
	if extension == "root" {
		fields = `,"extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"},"com.example":{"counter":18446744073709551617}}`
	} else if extension == "shadow" {
		fields = `,"extensions":{"com.openai":{}}`
	}
	write(t, root, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"portable-probe","version":" 1.0.0 ","author":{"name":"shrug-labs"},"unknown":{"preserve":true}`+fields+`}`, 0o440)
	write(t, root, ".codex-plugin/plugin.json", `{"name":"inert-legacy","skills":"./custom","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"}}}`, 0o644)
	for _, skill := range []string{"setup", "other"} {
		write(t, root, "skills/"+skill+"/SKILL.md", "---\nname: "+skill+"\ndescription: Use when testing portable native discovery.\n---\nAIPACK_PORTABLE_"+skill+"\n", 0o440)
	}
	write(t, root, "skills/nested/ignored/SKILL.md", "---\nname: ignored\ndescription: Inert recursive asset.\n---\nIGNORED\n", 0o644)
	write(t, root, "commands/inert.md", "Legacy command must not migrate.\n", 0o644)
	write(t, root, "hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo inert"}]}]}}`, 0o644)
	write(t, root, ".app.json", `{"apps":{"inert":{"id":"aipack-synthetic-connector"}}}`, 0o644)
	write(t, root, ".mcp.json", `{"mcpServers":{"inert":{"command":"echo"}}}`, 0o644)
	write(t, root, "mcp.json", `{"$schema":"`+agentMCPSchema+`","mcpServers":{"probe":{"type":"stdio","command":"python3","args":["${PLUGIN_ROOT}/scripts/server.py"]},"other":{"type":"streamable-http","url":"http://127.0.0.1:1/mcp"}}}`, 0o440)
	write(t, root, "scripts/server.py", "print('portable executable asset')\n", 0o755)
}

func TestAgentPluginConversion(t *testing.T) {
	for _, extension := range []string{"root", "overlay", "shadow"} {
		t.Run(extension, func(t *testing.T) {
			src, pack := t.TempDir(), t.TempDir()
			agentFixture(t, src, extension)
			m, err := MaterializeCodex(src, pack, "alias", "agent-market")
			if err != nil {
				t.Fatal(err)
			}
			if m.NativePlugin.Format != AgentPlugins || m.NativePlugin.Manifest != "plugin.json" || m.NativePlugin.Name != "portable-probe" || m.Version != "1.0.0" || !slices.Equal(m.Skills, []string{"other", "setup"}) || !slices.Equal(m.MCP, []string{"other", "probe"}) || len(m.Workflows)+len(m.Hooks) != 0 {
				t.Fatalf("portable conversion applied legacy discovery: %+v", m)
			}
			if (extension == "overlay") != (len(m.NativePlugin.SettingsFiles) == 1) {
				t.Fatal("portable extension did not take precedence over the legacy overlay")
			}
			s := selection(m, pack)
			full, err := RenderCodex(s)
			original, readErr := ReadFiles(src)
			if err != nil || readErr != nil || !reflect.DeepEqual(full, original) {
				t.Fatalf("all-selected portable delivery changed source bytes/modes: %v %v", err, readErr)
			}
			s.Selected[domain.CategorySkills], s.Selected[domain.CategoryMCP] = []string{"other"}, []string{"other"}
			filtered, err := RenderCodex(s)
			if err != nil {
				t.Fatal(err)
			}
			view := t.TempDir()
			if err := WriteFiles(view, filtered); err != nil {
				t.Fatal(err)
			}
			read, err := ReadCodex(view, "agent-market")
			if err != nil || !slices.Equal(read.Skills, []string{"other"}) || !slices.Equal(read.MCP, []string{"other"}) {
				t.Fatalf("portable selection retained excluded capabilities: %+v %v", read, err)
			}
			stored, err := ReadFiles(filepath.Join(pack, "upstream"))
			if err != nil || !reflect.DeepEqual(stored, original) {
				t.Fatal("portable selection changed stored upstream files")
			}
		})
	}
	for _, invalid := range []string{
		`{}`, `{"$schema":"future","name":"valid"}`, `{"$schema":"` + agentPluginSchema + `","name":"Upper"}`,
		`{"$schema":"` + agentPluginSchema + `","name":"bad--name"}`, `{"$schema":"` + agentPluginSchema + `","name":"ok","description":null}`,
		`{"$schema":"` + agentPluginSchema + `","name":"ok","keywords":null}`, `{"$schema":"` + agentPluginSchema + `","name":"ok","author":{"unknown":"x"}}`,
		`{"$schema":"` + agentPluginSchema + `","name":"ok","extensions":{"com.openai":{"name":null}}}`,
		`{"$schema":"` + agentPluginSchema + `","name":"ok","extensions":{"com.openai":{"apps":123}}}`,
		`{"$schema":"` + agentPluginSchema + `","name":"ok","extensions":{"com.openai":{"interface":{"capabilities":null}}}}`,
	} {
		root := t.TempDir()
		write(t, root, "plugin.json", invalid, 0o644)
		write(t, root, ".codex-plugin/plugin.json", `{"name":"legacy"}`, 0o644)
		if _, err := ReadCodex(root, "market"); err == nil {
			t.Fatalf("invalid portable manifest fell back to legacy: %s", invalid)
		}
	}
	for _, invalidMCP := range []string{`{`, `{}`, `{"$schema":"future","mcpServers":{}}`, `{"$schema":"` + agentMCPSchema + `","mcpServers":[]}`} {
		root, pack := t.TempDir(), t.TempDir()
		agentFixture(t, root, "root")
		if err := os.Chmod(filepath.Join(root, "mcp.json"), 0o640); err != nil {
			t.Fatal(err)
		}
		write(t, root, "mcp.json", invalidMCP, 0o640)
		m, err := MaterializeCodex(root, pack, "alias", "agent-market")
		if err != nil || len(m.MCP) != 0 || len(m.Skills) != 2 {
			t.Fatalf("invalid MCP component disabled the whole portable plugin: %+v %v", m, err)
		}
		files, err := RenderCodex(selection(m, pack))
		original, readErr := ReadFiles(root)
		if err != nil || readErr != nil || !reflect.DeepEqual(files, original) {
			t.Fatal("invalid portable MCP diagnostics were lost from all-selected delivery")
		}
	}
}

func TestCodexNativeAgentExtensionValidation(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for portable extension validation")
	}
	for _, test := range []struct{ name, fields string }{
		{"empty", `"extensions":{"com.openai":{}}`},
		{"root-null-keyword", `"keywords":[null]`},
		{"root-empty-author-sequence", `"author":[]`},
		{"root-author-sequence", `"author":["Fixture","fixture@example.invalid","https://example.invalid"]`},
		{"root-null-author-sequence", `"author":[null]`},
		{"inert-hooks-apps", `"extensions":{"com.openai":{"hooks":{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo inert"}]}]}},"apps":"./.app.json"}}`},
		{"nonobject-extensions", `"extensions":false`},
		{"nonobject-extension", `"extensions":{"com.openai":false}`},
		{"extension-null-name", `"extensions":{"com.openai":{"name":null}}`},
		{"extension-number-version", `"extensions":{"com.openai":{"version":1}}`},
		{"extension-null-keywords", `"extensions":{"com.openai":{"keywords":null}}`},
		{"extension-null-keyword-element", `"extensions":{"com.openai":{"keywords":[null]}}`},
		{"extension-number-description", `"extensions":{"com.openai":{"description":1}}`},
		{"extension-number-apps", `"extensions":{"com.openai":{"apps":1}}`},
		{"extension-array-interface", `"extensions":{"com.openai":{"interface":[]}}`},
		{"extension-array-display", `"extensions":{"com.openai":{"interface":["Display"]}}`},
		{"extension-array-invalid-capabilities", `"extensions":{"com.openai":{"interface":[null,null,null,null,null,null]}}`},
		{"extension-null-options", `"extensions":{"com.openai":{"version":null,"description":null,"apps":null,"interface":null}}`},
		{"extension-null-display", `"extensions":{"com.openai":{"interface":{"displayName":null,"defaultPrompt":123}}}`},
		{"extension-duplicate-alias", `"extensions":{"com.openai":{"interface":{"websiteURL":"https://example.invalid","websiteUrl":"https://example.invalid"}}}`},
		{"extension-ignored-paths", `"extensions":{"com.openai":{"skills":123,"mcpServers":false,"hooks":123}}`},
		{"overlay-null-name", `"extensions":false`},
		{"extension-number-display", `"extensions":{"com.openai":{"interface":{"displayName":1}}}`},
		{"extension-null-capabilities", `"extensions":{"com.openai":{"interface":{"capabilities":null}}}`},
		{"extension-null-capability-element", `"extensions":{"com.openai":{"interface":{"capabilities":[null]}}}`},
		{"extension-null-screenshot-element", `"extensions":{"com.openai":{"interface":{"screenshots":[null]}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, market, home := t.TempDir(), t.TempDir(), t.TempDir()
			manifest := `{"$schema":"` + agentPluginSchema + `","name":"extension-probe","version":"1.0.0",` + test.fields + `}`
			write(t, market, "plugins/probe/plugin.json", manifest, 0o644)
			write(t, market, "plugins/probe/skills/probe/SKILL.md", "---\nname: probe\ndescription: Extension validation fixture.\n---\nEXTENSION_PROBE\n", 0o644)
			write(t, market, "plugins/probe/.app.json", `{"apps":{"probe":{"id":"aipack-synthetic-connector"}}}`, 0o644)
			if test.name == "overlay-null-name" {
				write(t, market, "plugins/probe/.codex-plugin/plugin.json", `{"name":null}`, 0o644)
			}
			write(t, market, ".agents/plugins/marketplace.json", `{"name":"extension-market","plugins":[{"name":"extension-probe","source":"./plugins/probe"}]}`, 0o644)
			if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
				t.Fatal(err)
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex")}
			var installErr error
			var output []byte
			for _, args := range [][]string{{"plugin", "marketplace", "add", market, "--json"}, {"plugin", "add", "extension-probe@extension-market", "--json"}} {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, "codex", args...)
				cmd.Dir, cmd.Env = root, env
				output, installErr = cmd.CombinedOutput()
				cancel()
				if installErr != nil {
					break
				}
			}
			_, convertErr := MaterializeCodex(filepath.Join(market, "plugins/probe"), t.TempDir(), "alias", "extension-market")
			if (installErr == nil) != (convertErr == nil) {
				t.Fatalf("native/converted extension acceptance differs: native=%v converted=%v\n%s", installErr, convertErr, output)
			}
			if installErr == nil {
				read := nativeRequest(t, "codex", root, env, "plugin/read", map[string]any{"marketplacePath": filepath.Join(market, ".agents/plugins/marketplace.json"), "pluginName": "extension-probe"})
				var result struct {
					Plugin struct {
						Skills []any `json:"skills"`
						Apps   []any `json:"apps"`
					} `json:"plugin"`
				}
				if err := json.Unmarshal(read, &result); err != nil || len(result.Plugin.Skills) != 1 || len(result.Plugin.Apps) != 0 || len(nativeHooks(t, "codex", root, env)) != 0 {
					t.Fatalf("portable extension changed native capabilities: %s %v", read, err)
				}
			}
		})
	}
}

func TestCodexSkillFrontmatter(t *testing.T) {
	for _, tc := range []struct{ fields, want string }{
		{"name: actual\ndescription: Native skill.", "actual"},
		{"name: null\ndescription: Native skill.", "fallback"},
		{"description: Native skill.", "fallback"},
		{"name: '  spaced   name  '\ndescription: Native skill.", "spaced name"},
		{"name: 42\ndescription: Native skill.", "42"},
		{"name: path/name\ndescription: Native skill.", "path/name"},
		{"name: repaired\ndescription: Prose with colon: value", "repaired"},
		{"name: invalid", ""},
		{"name: [invalid]\ndescription: Native skill.", ""},
		{"name: invalid\ndescription: Native skill.\nmetadata: null", ""},
		{"name: " + strings.Repeat("x", 65) + "\ndescription: Native skill.", ""},
	} {
		if got := codexSkillID([]byte("---\n"+tc.fields+"\n---\nBody\n"), "fallback"); got != tc.want {
			t.Fatalf("native frontmatter %q: %q != %q", tc.fields, got, tc.want)
		}
	}
}

func TestCodexNativeSkillIdentifiers(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native skill identifier controls")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		for _, converted := range []bool{false, true} {
			for _, linkedRoot := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/converted=%t/linked-root=%t", format, converted, linkedRoot), func(t *testing.T) {
					root, src, home := t.TempDir(), t.TempDir(), t.TempDir()
					write(t, src, ".codex-plugin/plugin.json", `{"name":"skill-probe","version":"1.0.0"}`, 0o644)
					if format == AgentPlugins {
						write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"skill-probe","version":"1.0.0"}`, 0o644)
					}
					for path, frontmatter := range map[string]string{
						"skills/folder":              "name: actual\ndescription: Named native skill.",
						"skills/fallback":            "description: Unnamed native skill.",
						"skills/null-name":           "name: null\ndescription: Null native name.",
						"skills/spaced":              "name: '  spaced   name  '\ndescription: Spaced native name.",
						"skills/numeric":             "name: 42\ndescription: Numeric native name.",
						"skills/slash":               "name: path/name\ndescription: Native name with slash.",
						"skills/separator":           "name: a__aipack__b\ndescription: Native separator name.",
						"skills/duplicate-first":     "name: duplicate\ndescription: First native duplicate.",
						"skills/duplicate-second":    "name: duplicate\ndescription: Second native duplicate.",
						"skills/repaired":            "name: repaired\ndescription: Prose with colon: value",
						"skills/null-metadata":       "name: invalid-metadata\ndescription: Invalid metadata.\nmetadata: null",
						"skills/missing-description": "name: invalid-description",
						"skills/invalid-type":        "name: [invalid]\ndescription: Invalid native name.",
						"skills/long-name":           "name: " + strings.Repeat("x", 65) + "\ndescription: Too long.",
						"skills/.hidden":             "name: hidden\ndescription: Hidden native asset.",
						"skills/nested/child":        "name: nested\ndescription: Nested native skill.",
						"skills/deep/a/b/c/d":        "name: depth-limit\ndescription: Last native scan depth.",
						"skills/deep/a/b/c/d/e":      "name: depth-six\ndescription: Last native directory depth.",
						"skills/deep/a/b/c/d/e/f":    "name: too-deep\ndescription: Beyond native scan depth.",
					} {
						write(t, src, path+"/SKILL.md", "---\n"+frontmatter+"\n---\nNATIVE_SKILL_BODY\n", 0o440)
					}
					write(t, src, "skills/SKILL.md", " --- \nname: root-skill\ndescription: Native root file.\n --- \nROOT_SKILL_BODY\n", 0o440)
					write(t, src, "linked-directory/SKILL.md", "---\nname: directory-link\ndescription: Linked native directory.\n---\nDIRECTORY_LINK_BODY\n", 0o440)
					write(t, src, "canonical-directory/SKILL.md", "---\ndescription: Canonical unnamed native directory.\n---\nCANONICAL_LINK_BODY\n", 0o440)
					for _, name := range []string{"alias-first", "alias-second"} {
						if err := os.Symlink("../canonical-directory", filepath.Join(src, "skills", name)); err != nil {
							t.Fatal(err)
						}
					}
					write(t, src, "linked-file/SKILL.md", "---\nname: file-link\ndescription: Linked native file.\n---\nFILE_LINK_BODY\n", 0o440)
					if err := os.Symlink("../linked-directory", filepath.Join(src, "skills/directory-link")); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(src, "skills/file-link"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("../../linked-file/SKILL.md", filepath.Join(src, "skills/file-link/SKILL.md")); err != nil {
						t.Fatal(err)
					}
					if linkedRoot {
						if err := os.Rename(filepath.Join(src, "skills"), filepath.Join(src, "shared-skills")); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink("shared-skills", filepath.Join(src, "skills")); err != nil {
							t.Fatal(err)
						}
					}
					env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex")}
					if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
						t.Fatal(err)
					}
					market := filepath.Join(home, "market")
					write(t, market, ".agents/plugins/marketplace.json", `{"name":"skill-market","plugins":[{"name":"skill-probe","source":"./plugins/probe"}]}`, 0o644)
					files, err := ReadFiles(src)
					want := []string{"42", "a__aipack__b", "actual", "canonical-directory", "directory-link", "duplicate", "duplicate", "fallback", "null-name", "path/name", "repaired", "spaced name"}
					if format == CodexLegacy {
						want = append(want, "depth-limit", "depth-six", "nested", "root-skill")
						slices.Sort(want)
					}
					var selectedView domain.NativePluginSelection
					if converted {
						pack := t.TempDir()
						m, convertErr := MaterializeCodex(src, pack, "alias", "skill-market")
						if convertErr != nil || !slices.Equal(m.Skills, slices.Compact(slices.Clone(want))) {
							t.Fatalf("native identifiers differ: %v %v", m.Skills, convertErr)
						}
						for _, finding := range config.ValidatePackRoot(pack) {
							if finding.Severity == config.FindingSeverityError {
								t.Fatal(finding.String())
							}
						}
						selectedView = selection(m, pack)
						original := files
						files, err = RenderCodex(selectedView)
						if err == nil && !reflect.DeepEqual(files, original) {
							t.Fatal("all-selected native identifiers changed payload bytes, modes or links")
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := WriteFiles(filepath.Join(market, "plugins/probe"), files); err != nil {
						t.Fatal(err)
					}
					run := func(args ...string) {
						t.Helper()
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						cmd := exec.CommandContext(ctx, "codex", args...)
						cmd.Env, cmd.Dir = env, root
						out, err := cmd.CombinedOutput()
						cancel()
						if err != nil {
							t.Fatalf("native skill install: %v\n%s", err, out)
						}
					}
					run("plugin", "marketplace", "add", market, "--json")
					run("plugin", "add", "skill-probe@skill-market", "--json")
					readNames := func() []string {
						t.Helper()
						raw := nativeRequest(t, "codex", root, env, "plugin/read", map[string]any{"marketplacePath": filepath.Join(market, ".agents/plugins/marketplace.json"), "pluginName": "skill-probe"})
						var read struct {
							Plugin struct {
								Skills []struct{ Name string } `json:"skills"`
							} `json:"plugin"`
						}
						if err := json.Unmarshal(raw, &read); err != nil {
							t.Fatal(err)
						}
						var names []string
						for _, skill := range read.Plugin.Skills {
							names = append(names, strings.TrimPrefix(skill.Name, "skill-probe:"))
						}
						slices.Sort(names)
						return names
					}
					if names := readNames(); !slices.Equal(names, want) {
						t.Fatalf("native identifier baseline differs: %v != %v", names, want)
					}
					if converted {
						for _, chosen := range [][]string{{"a__aipack__b", "directory-link", "path/name"}, {"canonical-directory", "duplicate", "duplicate"}, nil, want} {
							selectedView.Selected[domain.CategorySkills] = slices.Compact(slices.Clone(chosen))
							filtered, err := RenderCodex(selectedView)
							if err != nil {
								t.Fatal(err)
							}
							payload := filepath.Join(market, "plugins/probe")
							if err := os.RemoveAll(payload); err != nil {
								t.Fatal(err)
							}
							if err := WriteFiles(payload, filtered); err != nil {
								t.Fatal(err)
							}
							run("plugin", "add", "skill-probe@skill-market", "--json")
							if names := readNames(); !slices.Equal(names, chosen) {
								t.Fatalf("native identifier selection differs: %v != %v", names, chosen)
							}
						}
					}
				})
			}
		}
	}
}

func TestCodexNativeSkillScanLimits(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native traversal limits")
	}
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		for _, limit := range []string{"directories", "entries", "response-bytes"} {
			t.Run(format+"/"+limit, func(t *testing.T) {
				root := t.TempDir()
				home, err := os.MkdirTemp("/tmp", "aipack-limit-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.RemoveAll(home); err != nil {
						t.Error(err)
					}
				})
				want := []string{"first"}
				if limit == "response-bytes" {
					want = nil
				}
				market := filepath.Join(home, "market")
				src := filepath.Join(market, "plugins/probe")
				write(t, src, ".codex-plugin/plugin.json", `{"name":"limit-probe","version":"1.0.0"}`, 0o644)
				if format == AgentPlugins {
					write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"limit-probe","version":"1.0.0"}`, 0o644)
				}
				first, last := "skills/one", "skills/two"
				if limit == "directories" {
					for i := range 2000 {
						if err := os.MkdirAll(filepath.Join(src, fmt.Sprintf("skills/d%04d", i)), 0o755); err != nil {
							t.Fatal(err)
						}
					}
					first, last = "skills/d1998", "skills/d1999"
				} else {
					entries := 19997
					prefix := "entry-"
					if limit == "response-bytes" {
						entries = 14997
						prefix += strings.Repeat("x", 190)
					}
					for i := range entries {
						write(t, src, fmt.Sprintf("skills/%s%05d", prefix, i), "", 0o644)
					}
				}
				for path, name := range map[string]string{first: "first", last: "last"} {
					write(t, src, path+"/SKILL.md", "---\nname: "+name+"\ndescription: Native traversal boundary.\n---\nBOUNDARY_SKILL_BODY\n", 0o440)
				}
				write(t, market, ".agents/plugins/marketplace.json", `{"name":"limit-market","plugins":[{"name":"limit-probe","source":"./plugins/probe"}]}`, 0o644)
				env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex")}
				if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				run := func(args ...string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					cmd := exec.CommandContext(ctx, "codex", args...)
					cmd.Env, cmd.Dir = env, root
					out, err := cmd.CombinedOutput()
					cancel()
					if err != nil {
						t.Fatalf("native traversal install: %v\n%s", err, out)
					}
				}
				run("plugin", "marketplace", "add", market, "--json")
				run("plugin", "add", "limit-probe@limit-market", "--json")
				readNames := func() []string {
					t.Helper()
					raw := nativeRequest(t, "codex", root, env, "plugin/read", map[string]any{"marketplacePath": filepath.Join(market, ".agents/plugins/marketplace.json"), "pluginName": "limit-probe"})
					var read struct {
						Plugin struct {
							Skills []struct{ Name string } `json:"skills"`
						} `json:"plugin"`
					}
					if err := json.Unmarshal(raw, &read); err != nil {
						t.Fatal(err)
					}
					var names []string
					for _, skill := range read.Plugin.Skills {
						names = append(names, strings.TrimPrefix(skill.Name, "limit-probe:"))
					}
					return names
				}
				if names := readNames(); !slices.Equal(names, want) {
					t.Fatalf("native %s scan differs: %v != %v", limit, names, want)
				}
				original, err := ReadFiles(src)
				if err != nil {
					t.Fatal(err)
				}
				pack := t.TempDir()
				m, err := MaterializeCodex(src, pack, "limit-alias", "limit-market")
				if err != nil || !slices.Equal(m.Skills, []string{"first", "last"}) {
					t.Fatalf("complete source selectors differ: %v %v", m.Skills, err)
				}
				view := selection(m, pack)
				for _, selectedIDs := range [][]string{m.Skills, {"first"}, {"last"}, nil, m.Skills} {
					view.Selected[domain.CategorySkills] = selectedIDs
					files, err := RenderCodex(view)
					if err != nil {
						t.Fatal(err)
					}
					if len(selectedIDs) == len(m.Skills) && !reflect.DeepEqual(files, original) {
						t.Fatal("all-selected delivery changed source scan-budget inputs")
					}
					if err := os.RemoveAll(src); err != nil {
						t.Fatal(err)
					}
					if err := WriteFiles(src, files); err != nil {
						t.Fatal(err)
					}
					run("plugin", "add", "limit-probe@limit-market", "--json")
					expected := want
					if len(selectedIDs) == 0 {
						expected = nil
					} else if limit == "directories" && !slices.Contains(selectedIDs, "first") {
						expected = nil
					} else if limit == "entries" && len(selectedIDs) == 1 {
						expected = selectedIDs
					}
					if names := readNames(); !slices.Equal(names, expected) {
						t.Fatalf("converted %s selection differs: %v != %v", limit, names, expected)
					}
				}
			})
		}
	}
}

func TestCodexNativeAgentMCPDeclarations(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for portable MCP declaration controls")
	}
	for _, linked := range []bool{false, true} {
		for _, converted := range []bool{false, true} {
			t.Run(fmt.Sprintf("linked=%t/converted=%t", linked, converted), func(t *testing.T) {
				root, src, home := t.TempDir(), t.TempDir(), t.TempDir()
				write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"mcp-probe","version":"1.0.0"}`, 0o440)
				write(t, src, "skills/valid/SKILL.md", "---\nname: valid\ndescription: Native skill alongside rejected MCP declarations.\n---\nVALID_SKILL_BODY\n", 0o440)
				servers := map[string]any{
					"good":          map[string]any{"type": "stdio", "command": "python3", "args": []string{"${PLUGIN_ROOT}/scripts/probe.py"}, "env": map[string]string{"MARKER": filepath.Join(home, "executed")}},
					"relative":      map[string]any{"type": "stdio", "command": "./scripts/probe.py", "cwd": "${PLUGIN_DATA}"},
					"http":          map[string]any{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp", "headers": map[string]string{"X-Probe": "preserved", "Authorization": "fixture"}},
					"bad-sse":       map[string]any{"type": "sse", "url": "http://127.0.0.1:1/mcp"},
					"bad-absolute":  map[string]any{"type": "stdio", "command": "/bin/echo"},
					"bad-env":       map[string]any{"type": "stdio", "command": "python3", "env": map[string]string{"PLUGIN_ROOT": "replacement"}},
					"bad-cwd":       map[string]any{"type": "stdio", "command": "python3", "cwd": nil},
					"bad-extra":     map[string]any{"type": "stdio", "command": "python3", "enabled": true},
					"bad-null-args": map[string]any{"type": "stdio", "command": "python3", "args": nil},
					"bad-http":      map[string]any{"type": "streamable-http", "url": "http://example.invalid/mcp"},
					"bad-fragment":  map[string]any{"type": "streamable-http", "url": "https://example.invalid/mcp#fragment"},
					"bad-headers":   map[string]any{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp", "headers": map[string]string{"X-Probe": "first", "x-probe": "second"}},
				}
				body, err := json.Marshal(map[string]any{"$schema": agentMCPSchema, "mcpServers": servers})
				if err != nil {
					t.Fatal(err)
				}
				path := "mcp.json"
				if linked {
					path = "configs/mcp.json"
				}
				write(t, src, path, string(body), 0o440)
				if linked {
					if err := os.Symlink(path, filepath.Join(src, "mcp.json")); err != nil {
						t.Fatal(err)
					}
				}
				write(t, src, "scripts/probe.py", "#!/usr/bin/env python3\nimport os, pathlib\npathlib.Path(os.environ['MARKER']).write_text('unexpected execution')\n", 0o755)
				original, err := ReadFiles(src)
				if err != nil {
					t.Fatal(err)
				}
				files := original
				var view domain.NativePluginSelection
				if converted {
					pack := t.TempDir()
					m, err := MaterializeCodex(src, pack, "mcp-alias", "mcp-market")
					count := len(servers)
					if linked {
						count = 0
					}
					if err != nil || len(m.MCP) != count {
						t.Fatalf("source declarations lost: %v %v", m.MCP, err)
					}
					view = selection(m, pack)
					files, err = RenderCodex(view)
					if err != nil || !reflect.DeepEqual(files, original) {
						t.Fatalf("all-selected MCP bytes/modes/links changed: %v", err)
					}
				}
				market := filepath.Join(home, "market")
				write(t, market, ".agents/plugins/marketplace.json", `{"name":"mcp-market","plugins":[{"name":"mcp-probe","source":"./plugins/probe"}]}`, 0o644)
				payload := filepath.Join(market, "plugins/probe")
				if err := WriteFiles(payload, files); err != nil {
					t.Fatal(err)
				}
				env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex")}
				if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				run := func(args ...string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					cmd := exec.CommandContext(ctx, "codex", args...)
					cmd.Env, cmd.Dir = env, root
					out, err := cmd.CombinedOutput()
					cancel()
					if err != nil {
						t.Fatalf("native MCP install: %v\n%s", err, out)
					}
				}
				run("plugin", "marketplace", "add", market, "--json")
				run("plugin", "add", "mcp-probe@mcp-market", "--json")
				readNames := func() []string {
					t.Helper()
					var diagnostics string
					raw := nativeRequest(t, "codex", root, append(slices.Clone(env), "RUST_LOG=warn"), "plugin/read", map[string]any{"marketplacePath": filepath.Join(market, ".agents/plugins/marketplace.json"), "pluginName": "mcp-probe"}, &diagnostics)
					if linked {
						if !strings.Contains(diagnostics, "not a regular file; disabling MCP") {
							t.Fatalf("linked MCP diagnostic missing: %s", diagnostics)
						}
					} else {
						for name := range servers {
							if !strings.HasPrefix(name, "bad-") {
								continue
							}
							present := !converted || slices.Contains(view.Selected[domain.CategoryMCP], name)
							if strings.Contains(diagnostics, name) != present {
								t.Fatalf("native rejection diagnostic for %s differs (present=%t): %s", name, present, diagnostics)
							}
						}
					}
					var read struct {
						Plugin struct {
							MCP    []string                `json:"mcpServers"`
							Skills []struct{ Name string } `json:"skills"`
						} `json:"plugin"`
					}
					if err := json.Unmarshal(raw, &read); err != nil {
						t.Fatal(err)
					}
					if len(read.Plugin.Skills) != 1 || read.Plugin.Skills[0].Name != "mcp-probe:valid" {
						t.Fatal("rejected MCP declarations disabled a valid skill")
					}
					slices.Sort(read.Plugin.MCP)
					return read.Plugin.MCP
				}
				want := []string{"good", "http", "relative"}
				if linked {
					want = nil
				}
				if names := readNames(); !slices.Equal(names, want) {
					t.Fatalf("native valid MCP servers differ: %v != %v", names, want)
				}
				if converted {
					for _, chosen := range [][]string{{"good", "bad-env"}, nil, view.Selected[domain.CategoryMCP]} {
						view.Selected[domain.CategoryMCP] = chosen
						if linked {
							view.Selected[domain.CategoryMCP] = nil
						}
						filtered, err := RenderCodex(view)
						if err != nil {
							t.Fatal(err)
						}
						if linked && !reflect.DeepEqual(filtered, original) {
							t.Fatal("inactive linked MCP file changed during selection")
						}
						if err := os.RemoveAll(payload); err != nil {
							t.Fatal(err)
						}
						if err := WriteFiles(payload, filtered); err != nil {
							t.Fatal(err)
						}
						run("plugin", "add", "mcp-probe@mcp-market", "--json")
						var expected []string
						for _, name := range want {
							if slices.Contains(chosen, name) {
								expected = append(expected, name)
							}
						}
						if names := readNames(); !slices.Equal(names, expected) {
							t.Fatalf("selected MCP declarations differ: %v != %v", names, expected)
						}
					}
				}
				if _, err := os.Stat(filepath.Join(home, "executed")); !os.IsNotExist(err) {
					t.Fatal("metadata inspection executed a bundled MCP server")
				}
			})
		}
	}
}

func TestCodexNativeAgentPluginSelection(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native portable discovery")
	}
	for _, extension := range []string{"root", "overlay", "shadow"} {
		for _, converted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/converted=%t", extension, converted), func(t *testing.T) {
				root, src, home := t.TempDir(), t.TempDir(), t.TempDir()
				agentFixture(t, src, extension)
				if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex"), "CODEX_DISABLE_UPDATE_CHECK=1"}
				run := func(args ...string) []byte {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, "codex", args...)
					cmd.Env, cmd.Dir, cmd.WaitDelay = env, root, time.Second
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("native portable %v: %v\n%s", args, err, out)
					}
					return out
				}
				market := filepath.Join(home, "market")
				catalog := filepath.Join(market, ".agents/plugins/marketplace.json")
				write(t, market, ".agents/plugins/marketplace.json", `{"name":"agent-market","plugins":[{"name":"portable-probe","source":"./plugins/probe"}]}`, 0o644)
				var s domain.NativePluginSelection
				if converted {
					pack := t.TempDir()
					m, err := MaterializeCodex(src, pack, "alias", "agent-market")
					if err != nil {
						t.Fatal(err)
					}
					s = selection(m, pack)
				}
				for phase := range 4 {
					if !converted && phase > 0 {
						break
					}
					if phase == 1 {
						s.Selected[domain.CategorySkills], s.Selected[domain.CategoryMCP] = []string{"other"}, nil
					} else if phase == 2 {
						s.Selected[domain.CategorySkills], s.Selected[domain.CategoryMCP], s.SettingsEnabled = []string{"other", "setup"}, []string{"other", "probe"}, false
					} else if phase == 3 {
						s.SettingsEnabled = true
					}
					files, err := ReadFiles(src)
					if converted {
						files, err = RenderCodex(s)
					}
					if err != nil {
						t.Fatal(err)
					}
					payload := filepath.Join(market, "plugins/probe")
					if phase > 0 {
						if err := os.RemoveAll(payload); err != nil {
							t.Fatal(err)
						}
					}
					if err := WriteFiles(payload, files); err != nil {
						t.Fatal(err)
					}
					if phase == 0 {
						run("plugin", "marketplace", "add", market, "--json")
					}
					run("plugin", "add", "portable-probe@agent-market", "--json")
					params := map[string]any{"marketplacePath": catalog, "pluginName": "portable-probe"}
					raw := nativeRequest(t, "codex", root, env, "plugin/read", params)
					var read struct {
						Plugin struct {
							Skills     []struct{ Name string } `json:"skills"`
							Apps       []any                   `json:"apps"`
							MCP        []string                `json:"mcpServers"`
							Onboarding map[string]any          `json:"onboardingSkill"`
						} `json:"plugin"`
					}
					if err := json.Unmarshal(raw, &read); err != nil {
						t.Fatal(err)
					}
					var skills []string
					for _, skill := range read.Plugin.Skills {
						skills = append(skills, skill.Name)
					}
					wantSkills, wantMCP := []string{"portable-probe:other", "portable-probe:setup"}, []string{"other", "probe"}
					if phase == 1 {
						wantSkills, wantMCP = []string{"portable-probe:other"}, nil
					}
					if !slices.Equal(slices.Sorted(slices.Values(skills)), wantSkills) || !slices.Equal(slices.Sorted(slices.Values(read.Plugin.MCP)), wantMCP) || len(read.Plugin.Apps) != 0 || len(nativeHooks(t, "codex", root, env)) != 0 {
						t.Fatalf("portable native capabilities differ in phase %d: %s", phase, raw)
					}
					wantOnboarding := extension != "shadow" && phase != 1 && phase != 2
					if (read.Plugin.Onboarding != nil) != wantOnboarding || wantOnboarding && read.Plugin.Onboarding["name"] != "portable-probe:setup" {
						t.Fatalf("portable onboarding precedence differs in phase %d: %s", phase, raw)
					}
				}
			})
		}
	}
}
