package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/shrug-labs/aipack/internal/domain"
)

// This test uses the real native dispatcher with an offline response stream.
// Only the test's own synthetic hooks receive explicit native review hashes;
// imported definitions stay untrusted until that simulated operator review.
func TestCodexNativeRuntimeParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native runtime parity")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	root, src, pack := t.TempDir(), t.TempDir(), t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, src, ".codex-plugin/plugin.json", `{"name":"runtime-probe","version":"1.0.0"}`, 0o644)
	write(t, src, "hooks/hooks.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 \"${PLUGIN_ROOT}/scripts/record.py\"","timeout":3,"additionalContextLimit":0}]}],"Stop":[{"hooks":[{"type":"command","command":"python3 \"${PLUGIN_ROOT}/scripts/record.py\"","timeout":3}]}]}}`, 0o644)
	write(t, src, "scripts/record.py", `import json, os, pathlib, sys
request = json.load(sys.stdin)
data = pathlib.Path(os.environ["PLUGIN_DATA"])
data.mkdir(parents=True, exist_ok=True)
with (data / "events.jsonl").open("a") as log:
    log.write(json.dumps({"input": request, "cwd": os.getcwd(), "plugin_root": os.environ.get("PLUGIN_ROOT"), "plugin_data": os.environ.get("PLUGIN_DATA"), "claude_root": os.environ.get("CLAUDE_PLUGIN_ROOT")}) + "\n")
if request["hook_event_name"] == "UserPromptSubmit":
    print(json.dumps({"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"AIPACK_CONTEXT_FROM_NATIVE_HOOK"}}))
else:
    print("{}")
`, 0o755)
	write(t, src, ".mcp.json", `{"mcpServers":{"probe":{"command":"python3","args":["scripts/mcp.py"],"cwd":".","env":{"AIPACK_MCP_ENV":"native-env"}}}}`, 0o644)
	write(t, src, "scripts/mcp.py", `import json, os, pathlib, sys
for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    if "id" not in request:
        continue
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "aipack-fixture", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": name, "description": "Inspect the native fixture environment.", "inputSchema": {"type": "object", "properties": {}}} for name in ["observe", "hidden"]]}
    elif method == "tools/call":
        observed = {"cwd": os.getcwd(), "marker": os.environ.get("AIPACK_MCP_ENV"), "method": method}
        data = pathlib.Path.home() / ".codex/plugins/data/runtime-probe-aipack-runtime"
        data.mkdir(parents=True, exist_ok=True)
        with (data / "mcp.jsonl").open("a") as log:
            log.write(json.dumps(observed) + "\n")
        result = {"content": [{"type": "text", "text": "AIPACK_MCP_TOOL_RESULT " + json.dumps(observed)}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
	m, err := MaterializeCodex(src, pack, "alias", "aipack-runtime")
	if err != nil {
		t.Fatal(err)
	}
	var requestMu sync.Mutex
	var requests [][]byte
	var contextRequests [][][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requestMu.Lock()
		requests = append(requests, body)
		requestMu.Unlock()
		if bytes.Contains(body, []byte(`"name":"mcp__probe"`)) && !bytes.Contains(body, []byte("AIPACK_MCP_TOOL_RESULT")) && !bytes.Contains(body, []byte(`"type":"function_call_output"`)) {
			call := map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "namespace": "mcp__probe", "name": "observe", "arguments": "{}"}
			nativeResponse(w, call)
			return
		}
		message := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "AIPACK_RESPONSE", "annotations": []any{}}}}
		nativeResponse(w, message)
	}))
	defer server.Close()
	// Compare native source installation and all-selected converted delivery.
	var baseline []map[string]any
	var failureBaseline bool
	for _, converted := range []bool{false, true} {
		label := "native"
		if converted {
			label = "converted"
		}
		home := filepath.Join(root, label)
		if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
			t.Fatal(err)
		}
		env := slices.DeleteFunc(os.Environ(), func(value string) bool {
			return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
		})
		env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
		run := func(args ...string) []byte {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.WaitDelay = time.Second
			cmd.Env, cmd.Dir = env, root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s codex %v: %v\n%s", label, args, err, out)
			}
			return out
		}
		market := filepath.Join(home, "market")
		write(t, market, ".agents/plugins/marketplace.json", `{"name":"aipack-runtime","plugins":[{"name":"runtime-probe","source":"./plugins/runtime-probe"}]}`, 0o644)
		files, err := ReadFiles(src)
		if converted {
			files, err = RenderCodex(selection(m, pack))
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteFiles(filepath.Join(market, "plugins/runtime-probe"), files); err != nil {
			t.Fatal(err)
		}
		run("plugin", "marketplace", "add", market, "--json")
		run("plugin", "add", m.NativePlugin.Binding(), "--json")
		hooks := nativeHooks(t, binary, root, env)
		if len(hooks) != 2 {
			t.Fatalf("%s native loader: %v", label, hooks)
		}
		state := map[string]any{}
		for _, hook := range hooks {
			if hook["trustStatus"] != "untrusted" {
				t.Fatal("native install bypassed review")
			}
			state[hook["key"].(string)] = map[string]any{"trusted_hash": hook["currentHash"]}
		}
		path := filepath.Join(home, ".codex/config.toml")
		var config map[string]any
		if err := toml.Unmarshal(mustBytes(t, path), &config); err != nil {
			t.Fatal(err)
		}
		config["hooks"] = map[string]any{"state": state}
		// Simulate operator approval for this synthetic read-only tool only.
		// Native import does not grant executable trust or MCP approval.
		entry := config["plugins"].(map[string]any)[m.NativePlugin.Binding()].(map[string]any)
		entry["mcp_servers"] = map[string]any{"probe": map[string]any{"enabled": true, "enabled_tools": []string{"observe"},
			"tools": map[string]any{"observe": map[string]any{"approval_mode": "approve"}}}}
		body, err := toml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		write(t, home, ".codex/config.toml", string(body), 0o600)
		execFixture := func(prompt string) []byte {
			return run("exec", "--skip-git-repo-check", "--json", "-c", `model_provider="aipack-test"`, "-c", `model="fixture-model"`,
				"-c", `model_providers.aipack-test.name="aipack-test"`, "-c", `model_providers.aipack-test.base_url="`+server.URL+`/v1"`,
				"-c", `model_providers.aipack-test.wire_api="responses"`, "-c", `model_providers.aipack-test.requires_openai_auth=false`, prompt)
		}
		requestMu.Lock()
		firstRequest := len(requests)
		requestMu.Unlock()
		firstOutput := execFixture("Run the hook parity fixture.")
		requestMu.Lock()
		contextRequests = append(contextRequests, slices.Clone(requests[firstRequest:]))
		requestMu.Unlock()
		mcpData := filepath.Join(home, ".codex/plugins/data/runtime-probe-aipack-runtime/mcp.jsonl")
		if _, err := os.Stat(mcpData); err != nil {
			requestMu.Lock()
			var request map[string]json.RawMessage
			_ = json.Unmarshal(requests[len(requests)-1], &request)
			t.Logf("offline model tools: %s", request["tools"])
			requestMu.Unlock()
			t.Fatalf("%s native MCP did not execute: %v\n%s", label, err, firstOutput)
		}
		var mcpCall map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(mustBytes(t, mcpData)), &mcpCall); err != nil {
			t.Fatal(err)
		}
		if mcpCall["marker"] != "native-env" || mcpCall["method"] != "tools/call" || filepath.Clean(mcpCall["cwd"].(string)) != filepath.Join(home, ".codex/plugins/cache/aipack-runtime/runtime-probe/1.0.0") {
			t.Fatalf("%s native MCP execution differs: %+v", label, mcpCall)
		}
		data := filepath.Join(home, ".codex/plugins/data/runtime-probe-aipack-runtime/events.jsonl")
		lines := bytes.Split(bytes.TrimSpace(mustBytes(t, data)), []byte("\n"))
		var observed []map[string]any
		for _, line := range lines {
			var event map[string]any
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			input := event["input"].(map[string]any)
			if event["plugin_root"] != event["claude_root"] || event["cwd"] != root || input["cwd"] != root {
				t.Fatalf("native environment/cwd differs: %v", event)
			}
			observed = append(observed, map[string]any{"event": input["hook_event_name"], "cwd": event["cwd"], "inputCwd": input["cwd"]})
			if !strings.Contains(event["plugin_root"].(string), "plugins/cache/aipack-runtime/runtime-probe/1.0.0") {
				t.Fatal("wrong native plugin root")
			}
		}
		if len(observed) != 2 || observed[0]["event"] != "UserPromptSubmit" || observed[1]["event"] != "Stop" {
			t.Fatalf("%s dispatch: %v", label, observed)
		}
		if converted {
			a, _ := json.Marshal(baseline)
			b, _ := json.Marshal(observed)
			if !bytes.Equal(a, b) {
				t.Fatalf("native/converted execution differs: %s vs %s", a, b)
			}
		} else {
			baseline = observed
		}
		// A same-version definition change invalidates native review. After
		// explicitly reviewing it, nonzero prompt exit and Stop timeout must
		// retain the host's failure behavior on both installation paths.
		var changed map[string]any
		if err := json.Unmarshal(mustBytes(t, filepath.Join(src, "hooks/hooks.json")), &changed); err != nil {
			t.Fatal(err)
		}
		groups := changed["hooks"].(map[string]any)
		prompt := groups["UserPromptSubmit"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		stop := groups["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		prompt["command"] = prompt["command"].(string) + "; exit 1"
		stop["command"] = stop["command"].(string) + "; sleep 5"
		stop["timeout"] = 1
		changedBytes, _ := json.Marshal(changed)
		write(t, market, "plugins/runtime-probe/hooks/hooks.json", string(changedBytes), 0o644)
		run("plugin", "add", m.NativePlugin.Binding(), "--json")
		hooks = nativeHooks(t, binary, root, env)
		state = map[string]any{}
		for _, hook := range hooks {
			if hook["trustStatus"] != "modified" {
				t.Fatalf("changed definition retained trust: %v", hook)
			}
			state[hook["key"].(string)] = map[string]any{"trusted_hash": hook["currentHash"]}
		}
		if err := toml.Unmarshal(mustBytes(t, path), &config); err != nil {
			t.Fatal(err)
		}
		config["hooks"] = map[string]any{"state": state}
		body, err = toml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		write(t, home, ".codex/config.toml", string(body), 0o600)
		started := time.Now()
		out := execFixture("Run the failure fixture.")
		if elapsed := time.Since(started); elapsed < time.Second || elapsed >= 5*time.Second {
			t.Fatalf("native Stop timeout not honored: %s\n%s", elapsed, out)
		}
		succeeded := bytes.Contains(out, []byte("AIPACK_RESPONSE"))
		if converted && succeeded != failureBaseline {
			t.Fatal("native and converted failure outcomes differ")
		}
		failureBaseline = succeeded
		// A selection-only refresh removes the entire Stop event while
		// retaining native review state; no deleted handler can execute.
		delete(groups, "Stop")
		changedBytes, _ = json.Marshal(changed)
		write(t, market, "plugins/runtime-probe/hooks/hooks.json", string(changedBytes), 0o644)
		run("plugin", "add", m.NativePlugin.Binding(), "--json")
		if hooks := nativeHooks(t, binary, root, env); len(hooks) != 1 || hooks[0]["eventName"] != "userPromptSubmit" {
			t.Fatalf("excluded Stop remains in native loader: %v", hooks)
		}
		if converted {
			beforeCalls := mustBytes(t, mcpData)
			selected := selection(m, pack)
			selected.Selected[domain.CategoryMCP] = nil
			filtered, err := RenderCodex(selected)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteFiles(filepath.Join(market, "plugins/runtime-probe"), filtered); err != nil {
				t.Fatal(err)
			}
			run("plugin", "add", m.NativePlugin.Binding(), "--json")
			execFixture("Run the excluded MCP fixture.")
			if !bytes.Equal(beforeCalls, mustBytes(t, mcpData)) {
				t.Fatal("excluded native MCP server executed")
			}
			requestMu.Lock()
			exposed := bytes.Contains(requests[len(requests)-1], []byte(`"name":"mcp__probe"`))
			requestMu.Unlock()
			if exposed {
				t.Fatal("excluded native MCP server remained exposed after reload")
			}
			full, err := RenderCodex(selection(m, pack))
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteFiles(filepath.Join(market, "plugins/runtime-probe"), full); err != nil {
				t.Fatal(err)
			}
			run("plugin", "add", m.NativePlugin.Binding(), "--json")
			execFixture("Run the re-enabled MCP fixture.")
			if bytes.Equal(beforeCalls, mustBytes(t, mcpData)) {
				t.Fatal("re-enabled native MCP server did not execute")
			}
		}
	}
	requestMu.Lock()
	defer requestMu.Unlock()
	for _, batch := range contextRequests {
		hasContext := false
		for _, request := range batch {
			hasContext = hasContext || bytes.Contains(request, []byte("AIPACK_CONTEXT_FROM_NATIVE_HOOK"))
			if bytes.Contains(request, []byte(`"name":"hidden"`)) {
				t.Fatal("native plugin MCP tool policy did not filter exposure")
			}
		}
		if !hasContext {
			t.Fatal("prompt hook context did not reach native model input")
		}
	}
}

func nativeResponse(w http.ResponseWriter, item map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	added := item
	if item["type"] == "message" {
		added = map[string]any{"id": item["id"], "type": "message", "role": "assistant", "content": []any{}}
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "object": "response", "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": added},
	}
	if item["type"] == "message" {
		text := item["content"].([]map[string]any)[0]["text"]
		events = append(events, map[string]any{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": text})
	}
	events = append(events,
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
	for _, event := range events {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
}

func mustBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCodexNativeSetupContract(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 for native setup contract")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{CodexLegacy, AgentPlugins, "agent-plugins-overlay"} {
		t.Run(format, func(t *testing.T) {
			root, src, pack := t.TempDir(), t.TempDir(), t.TempDir()
			write(t, src, ".codex-plugin/plugin.json", `{"name":"setup-probe","version":"1.0.0","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"}}}`, 0o644)
			if format == AgentPlugins {
				write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"setup-probe","version":"1.0.0","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"}}}`, 0o644)
			} else if format == "agent-plugins-overlay" {
				write(t, src, "plugin.json", `{"$schema":"`+agentPluginSchema+`","name":"setup-probe","version":"1.0.0"}`, 0o644)
			}
			write(t, src, "skills/setup/SKILL.md", "---\nname: setup\ndescription: Use when bootstrapping the setup fixture.\n---\nAIPACK_SETUP_SKILL\nRun the bundled scripts/bootstrap.py once; retain its data on subsequent use.\n", 0o644)
			write(t, src, ".app.json", `{"apps":{"fixture":{"id":"aipack-synthetic-connector","category":"developer"}}}`, 0o644)
			write(t, src, "package.json", `{"name":"aipack-setup-fixture","scripts":{"postinstall":"python3 scripts/bootstrap.py"}}`, 0o644)
			write(t, src, "scripts/bootstrap.py", `import pathlib, sys
data = pathlib.Path.home() / ".codex/plugins/data/setup-probe-aipack-setup"
data.mkdir(parents=True, exist_ok=True)
if (data / "fail-once").exists():
    (data / "fail-once").unlink()
    print("AIPACK_SETUP_FAILED")
    sys.exit(1)
if (data / "ready").exists():
    print("AIPACK_SETUP_REUSED")
else:
    (data / "ready").write_text("runtime-v1")
    print("AIPACK_SETUP_READY")
`, 0o755)
			for _, authPolicy := range []string{"ON_INSTALL", "ON_USE"} {
				for _, converted := range []bool{false, true} {
					label := fmt.Sprintf("%s/converted=%t", authPolicy, converted)
					t.Run(label, func(t *testing.T) {
						home := t.TempDir()
						if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
							t.Fatal(err)
						}
						env := slices.DeleteFunc(os.Environ(), func(value string) bool {
							return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
						})
						env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
						run := func(args ...string) []byte {
							t.Helper()
							ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
							defer cancel()
							cmd := exec.CommandContext(ctx, binary, args...)
							cmd.Env, cmd.Dir, cmd.WaitDelay = env, root, time.Second
							out, err := cmd.CombinedOutput()
							if err != nil {
								t.Fatalf("codex %v: %v\n%s", args, err, out)
							}
							return out
						}
						market := filepath.Join(home, "market")
						catalog := filepath.Join(market, ".agents/plugins/marketplace.json")
						write(t, market, ".agents/plugins/marketplace.json", fmt.Sprintf(`{"name":"aipack-setup","plugins":[{"name":"setup-probe","source":"./plugins/setup-probe","policy":{"authentication":%q}}]}`, authPolicy), 0o644)
						files, err := ReadFiles(src)
						binding := "setup-probe@aipack-setup"
						if converted {
							m, convertErr := MaterializeCodex(src, pack, "local-alias", "aipack-setup")
							if convertErr != nil {
								t.Fatal(convertErr)
							}
							binding = m.NativePlugin.Binding()
							files, err = RenderCodex(selection(m, pack))
						}
						if err != nil {
							t.Fatal(err)
						}
						if err := WriteFiles(filepath.Join(market, "plugins/setup-probe"), files); err != nil {
							t.Fatal(err)
						}
						run("plugin", "marketplace", "add", market, "--json")
						var install map[string]any
						if err := json.Unmarshal(run("plugin", "add", binding, "--json"), &install); err != nil {
							t.Fatal(err)
						}
						if install["authPolicy"] != authPolicy || install["pluginId"] != binding {
							t.Fatalf("CLI installation lost identity/auth policy: %+v", install)
						}
						params := map[string]any{"marketplacePath": catalog, "pluginName": "setup-probe"}
						var apiInstall map[string]any
						if err := json.Unmarshal(nativeRequest(t, binary, root, env, "plugin/install", params), &apiInstall); err != nil {
							t.Fatal(err)
						}
						if apiInstall["authPolicy"] != authPolicy {
							t.Fatalf("native API installation lost auth policy: %+v", apiInstall)
						}
						for range 2 { // read after separate native restarts
							var read struct {
								Plugin struct {
									Summary    map[string]any   `json:"summary"`
									Onboarding map[string]any   `json:"onboardingSkill"`
									Apps       []map[string]any `json:"apps"`
								} `json:"plugin"`
							}
							raw := nativeRequest(t, binary, root, env, "plugin/read", params)
							if err := json.Unmarshal(raw, &read); err != nil {
								t.Fatal(err)
							}
							if read.Plugin.Summary["authPolicy"] != authPolicy || read.Plugin.Summary["enabled"] != true || read.Plugin.Onboarding["name"] != "setup-probe:setup" {
								t.Fatalf("native setup surface differs after restart: %s", raw)
							}
							// Native routing hides account apps without ChatGPT authentication.
							if len(read.Plugin.Apps) != 0 {
								t.Fatalf("unauthenticated native app exposure differs: %+v", read.Plugin.Apps)
							}
						}
						data := filepath.Join(home, ".codex/plugins/data/setup-probe-aipack-setup")
						ready := filepath.Join(data, "ready")
						if _, err := os.Stat(ready); !os.IsNotExist(err) {
							t.Fatal("native install/read executed bootstrap code")
						}
						cache := install["installedPath"].(string)
						if !bytes.Equal(mustBytes(t, filepath.Join(cache, ".app.json")), mustBytes(t, filepath.Join(src, ".app.json"))) {
							t.Fatal("native cache lost app declarations")
						}
						var skillMu sync.Mutex
						sawSkill := false
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							body, err := io.ReadAll(req.Body)
							if err != nil {
								t.Error(err)
								return
							}
							mentioned := bytes.Contains(body, []byte("AIPACK_SETUP_SKILL"))
							skillMu.Lock()
							sawSkill = sawSkill || mentioned
							skillMu.Unlock()
							if mentioned && !bytes.Contains(body, []byte(`"type":"function_call_output"`)) {
								args, _ := json.Marshal(map[string]any{"cmd": fmt.Sprintf("python3 %q", filepath.Join(cache, "scripts/bootstrap.py")), "login": false})
								nativeResponse(w, map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "namespace": "functions", "name": "exec_command", "arguments": string(args)})
								return
							}
							nativeResponse(w, map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "AIPACK_RESPONSE", "annotations": []any{}}}})
						}))
						defer server.Close()
						execSetup := func() []byte {
							return run("exec", "--skip-git-repo-check", "--json", "--sandbox", "workspace-write", "--add-dir", data,
								"-c", `model_provider="aipack-test"`, "-c", `model="fixture-model"`,
								"-c", `model_providers.aipack-test.name="aipack-test"`, "-c", `model_providers.aipack-test.base_url="`+server.URL+`/v1"`,
								"-c", `model_providers.aipack-test.wire_api="responses"`, "-c", `model_providers.aipack-test.requires_openai_auth=false`, "$setup-probe:setup Run the setup fixture.")
						}
						write(t, data, "fail-once", "synthetic setup failure", 0o600)
						if out := execSetup(); !bytes.Contains(out, []byte("AIPACK_SETUP_FAILED")) {
							t.Fatalf("first-use setup failure was not visible: %s", out)
						}
						if _, err := os.Stat(ready); !os.IsNotExist(err) {
							t.Fatal("failed setup produced ready state")
						}
						if out := execSetup(); !bytes.Contains(out, []byte("AIPACK_SETUP_READY")) || string(mustBytes(t, ready)) != "runtime-v1" {
							t.Fatalf("setup retry did not succeed: %s", out)
						}
						before, err := os.Stat(ready)
						if err != nil {
							t.Fatal(err)
						}
						if out := execSetup(); !bytes.Contains(out, []byte("AIPACK_SETUP_REUSED")) {
							t.Fatalf("warm restart did not reuse setup: %s", out)
						}
						after, err := os.Stat(ready)
						if err != nil || before.ModTime() != after.ModTime() {
							t.Fatal("warm setup replaced runtime state")
						}
						if converted {
							m, err := ReadCodex(filepath.Join(pack, "upstream"), "aipack-setup")
							if err != nil {
								t.Fatal(err)
							}
							for _, phase := range []struct{ settings, skill bool }{{false, true}, {true, false}, {false, false}, {true, true}} {
								selected := selection(m, pack)
								selected.SettingsEnabled = phase.settings
								if !phase.skill {
									selected.Selected[domain.CategorySkills] = nil
								}
								filtered, err := RenderCodex(selected)
								if err != nil {
									t.Fatal(err)
								}
								packageDir := filepath.Join(market, "plugins/setup-probe")
								if err := os.RemoveAll(packageDir); err != nil {
									t.Fatal(err)
								}
								if err := WriteFiles(packageDir, filtered); err != nil {
									t.Fatal(err)
								}
								run("plugin", "add", binding, "--json")
								var read struct {
									Plugin struct {
										Onboarding any   `json:"onboardingSkill"`
										Skills     []any `json:"skills"`
									} `json:"plugin"`
								}
								if err := json.Unmarshal(nativeRequest(t, binary, root, env, "plugin/read", params), &read); err != nil {
									t.Fatal(err)
								}
								if (read.Plugin.Onboarding != nil) != (phase.settings && phase.skill) || !phase.skill && len(read.Plugin.Skills) != 0 || phase.skill && len(read.Plugin.Skills) != 1 {
									t.Fatalf("native setup exclusion differs: %+v", read.Plugin)
								}
								if string(mustBytes(t, ready)) != "runtime-v1" || !bytes.Equal(mustBytes(t, filepath.Join(cache, "scripts/bootstrap.py")), mustBytes(t, filepath.Join(src, "scripts/bootstrap.py"))) {
									t.Fatal("setup exclusion damaged runtime data or shared assets")
								}
							}
						}
						skillMu.Lock()
						defer skillMu.Unlock()
						if !sawSkill {
							t.Fatal("native skill invocation did not reach model input")
						}
					})
				}
			}
		})
	}
}
