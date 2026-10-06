package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
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

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// The native dispatcher runs against a local API stream and disposable homes.
func TestClaudeNativeRuntimeParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native runtime parity")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	root, source, pack := t.TempDir(), t.TempDir(), t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, source, claudeManifest, `{"name":"runtime-probe","version":"1.0.0"}`, 0o644)
	write(t, source, "hooks/hooks.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 \"${CLAUDE_PLUGIN_ROOT}/scripts/record.py\"","timeout":3}]}],"Stop":[{"hooks":[{"type":"command","command":"python3 \"${CLAUDE_PLUGIN_ROOT}/scripts/record.py\"","timeout":3}]}]}}`, 0o644)
	write(t, source, "scripts/record.py", `import json, os, pathlib, sys
request = json.load(sys.stdin)
data = pathlib.Path(os.environ["CLAUDE_PLUGIN_DATA"])
data.mkdir(parents=True, exist_ok=True)
with (data / "events.jsonl").open("a") as log:
    log.write(json.dumps({"event": request["hook_event_name"], "cwd": os.getcwd(), "input_cwd": request["cwd"], "root": os.environ["CLAUDE_PLUGIN_ROOT"], "data": str(data)}) + "\n")
if request["hook_event_name"] == "UserPromptSubmit":
    print(json.dumps({"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"AIPACK_CLAUDE_NATIVE_CONTEXT"}}))
else:
    print("{}")
`, 0o755)
	write(t, source, ".mcp.json", `{"mcpServers":{"probe":{"command":"python3","args":["${CLAUDE_PLUGIN_ROOT}/scripts/mcp.py"],"env":{"AIPACK_MCP_ENV":"native-env","AIPACK_MCP_DATA":"${CLAUDE_PLUGIN_DATA}"}}}}`, 0o644)
	write(t, source, "scripts/mcp.py", `import json, os, pathlib, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "aipack-fixture", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": name, "description": "Inspect fixture environment.", "inputSchema": {"type": "object", "properties": {}}, "annotations": {"readOnlyHint": True}} for name in ["observe", "hidden"]]}
    elif method == "tools/call":
        observed = {"cwd": os.getcwd(), "marker": os.environ.get("AIPACK_MCP_ENV"), "data": os.environ.get("AIPACK_MCP_DATA")}
        data = pathlib.Path(observed["data"])
        data.mkdir(parents=True, exist_ok=True)
        (data / "mcp.json").write_text(json.dumps(observed))
        result = {"content": [{"type": "text", "text": "AIPACK_MCP_RESULT " + json.dumps(observed)}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
	m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "aipack-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		if req.URL.Path != "/v1/messages" {
			http.NotFound(w, req)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"AIPACK_RESPONSE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		}
		var input struct {
			Tools []struct{ Name string }
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Error(err)
			return
		}
		for _, tool := range input.Tools {
			if tool.Name == "mcp__plugin_runtime-probe_probe__observe" && !bytes.Contains(body, []byte("AIPACK_MCP_RESULT")) {
				events[1] = `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_fixture","name":"mcp__plugin_runtime-probe_probe__observe","input":{}}}`
				events[2] = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`
				events[4] = `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`
			}
		}
		for _, event := range events {
			var value struct{ Type string }
			if err := json.Unmarshal([]byte(event), &value); err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value.Type, event)
		}
	}))
	defer server.Close()
	for _, homeMode := range []string{"default", "custom", "same-home"} {
		for _, converted := range []bool{false, true} {
			home, market := filepath.Join(root, fmt.Sprintf("home-%s-%t", homeMode, converted)), t.TempDir()
			configHome := filepath.Join(home, ".claude")
			if homeMode == "custom" {
				configHome = filepath.Join(home, "native-config")
			} else if homeMode == "same-home" {
				configHome = home
			}
			// Only this synthetic read-only tool receives operator approval.
			write(t, configHome, "settings.json", `{"permissions":{"allow":["mcp__plugin_runtime-probe_probe__observe"]}}`, 0o600)
			write(t, market, ".claude-plugin/marketplace.json", `{"name":"aipack-runtime","owner":{"name":"shrug-labs"},"plugins":[{"name":"runtime-probe","source":"./plugins/probe"}]}`, 0o644)
			selected := selection(m, pack)
			for _, excludeStop := range []bool{false, true, false} {
				selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit", "claude-stop"}
				selected.Selected[domain.CategoryMCP] = []string{"probe"}
				if excludeStop {
					selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit"}
					selected.Selected[domain.CategoryMCP] = nil
				}
				files, err := ReadFiles(source)
				if converted {
					files, err = RenderClaude(selected)
				} else if excludeStop {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := WriteFiles(filepath.Join(market, "plugins/probe"), files); err != nil {
					t.Fatal(err)
				}
				claudeNativeAt(t, home, configHome, root, "plugin", "marketplace", "add", market)
				claudeNativeAt(t, home, configHome, root, "plugin", "install", m.NativePlugin.Binding(), "--scope", "user", "--json")
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				cmd := exec.CommandContext(ctx, binary, "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--no-session-persistence", "AIPACK_PROMPT")
				cmd.WaitDelay, cmd.Dir = time.Second, root
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + server.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + server.URL, "HTTPS_PROXY=" + server.URL, "NO_PROXY=127.0.0.1,localhost"}
				mu.Lock()
				requests = nil
				mu.Unlock()
				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil || !bytes.Contains(out, []byte("AIPACK_RESPONSE")) {
					t.Fatalf("Claude runtime (converted=%t excluded=%t): %v\n%s", converted, excludeStop, err, out)
				}
				mu.Lock()
				allRequests := bytes.Join(requests, nil)
				contextFound := bytes.Contains(allRequests, []byte("AIPACK_CLAUDE_NATIVE_CONTEXT"))
				mcpResult := bytes.Contains(allRequests, []byte("AIPACK_MCP_RESULT"))
				mu.Unlock()
				if !contextFound {
					t.Fatal("native hook context did not reach model input")
				}
				dataDir := filepath.Join(configHome, "plugins/data/runtime-probe-aipack-runtime")
				if mcpResult == excludeStop {
					t.Fatalf("native MCP selection (converted=%t excluded=%t): tool result=%t", converted, excludeStop, mcpResult)
				}
				if !excludeStop {
					observed, err := os.ReadFile(filepath.Join(dataDir, "mcp.json"))
					if err != nil {
						t.Fatal(err)
					}
					var value map[string]string
					if err := json.Unmarshal(observed, &value); err != nil || value["cwd"] != root || value["marker"] != "native-env" || value["data"] != dataDir {
						t.Fatalf("native MCP environment: %s %v", observed, err)
					}
				}
				logPath := filepath.Join(dataDir, "events.jsonl")
				log, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				var events []string
				for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
					var event map[string]string
					if err := json.Unmarshal(line, &event); err != nil {
						t.Fatal(err)
					}
					if event["cwd"] != root || event["input_cwd"] != root || event["data"] != dataDir || !filepath.IsAbs(event["root"]) {
						t.Fatalf("native root/data/cwd changed: %+v", event)
					}
					events = append(events, event["event"])
				}
				want := []string{"UserPromptSubmit", "Stop"}
				if excludeStop {
					want = want[:1]
				}
				if !reflect.DeepEqual(events, want) {
					t.Fatalf("native selection execution (converted=%t excluded=%t): %v want %v", converted, excludeStop, events, want)
				}
				if err := os.Remove(logPath); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestClaudeNativeHookFailureParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native hook failure parity")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
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
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			var value struct{ Type string }
			if err := json.Unmarshal([]byte(event), &value); err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value.Type, event)
		}
	}))
	defer server.Close()
	for _, mode := range []string{"prompt-error", "prompt-block", "stop-error", "stop-block", "prompt-timeout", "stop-timeout"} {
		t.Run(mode, func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			write(t, source, claudeManifest, `{"name":"failure-probe","version":"1.0.0"}`, 0o644)
			command := `python3 "${CLAUDE_PLUGIN_ROOT}/scripts/failure.py" ` + mode
			hooks, err := json.Marshal(map[string]any{"hooks": map[string]any{
				"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 1}}}},
				"Stop":             []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 1}}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			write(t, source, "hooks/hooks.json", string(hooks), 0o644)
			write(t, source, "scripts/failure.py", `import json, os, pathlib, sys, time
request = json.load(sys.stdin)
event = request["hook_event_name"]
target, behavior = sys.argv[1].split("-")
data = pathlib.Path(os.environ["CLAUDE_PLUGIN_DATA"])
data.mkdir(parents=True, exist_ok=True)
def record(status):
    with (data / "events.jsonl").open("a") as log:
        log.write(json.dumps({"event": event, "active": request.get("stop_hook_active", False), "status": status}) + "\n")
record("started")
if event == {"prompt": "UserPromptSubmit", "stop": "Stop"}[target]:
    if behavior == "error":
        print("AIPACK_NONBLOCKING_ERROR", file=sys.stderr)
        sys.exit(1)
    if behavior == "block" and not request.get("stop_hook_active", False):
        print("AIPACK_BLOCK_REASON", file=sys.stderr)
        sys.exit(2)
    if behavior == "timeout":
        if event == "UserPromptSubmit":
            print(json.dumps({"hookSpecificOutput":{"hookEventName":event,"additionalContext":"AIPACK_TIMEOUT_CONTEXT"}}), flush=True)
        else:
            print(json.dumps({"decision":"block","reason":"AIPACK_TIMEOUT_BLOCK"}), flush=True)
        time.sleep(6)
record("finished")
print("{}")
`, 0o755)
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "aipack-failure"})
			if err != nil {
				t.Fatal(err)
			}
			var baseline []byte
			var baselineFailed bool
			for _, converted := range []bool{false, true} {
				home, market := t.TempDir(), t.TempDir()
				configHome := filepath.Join(home, "native-config")
				files, err := ReadFiles(source)
				if converted {
					files, err = RenderClaude(selection(m, pack))
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := WriteFiles(filepath.Join(market, "plugins/probe"), files); err != nil {
					t.Fatal(err)
				}
				write(t, market, ".claude-plugin/marketplace.json", `{"name":"aipack-failure","owner":{"name":"shrug-labs"},"plugins":[{"name":"failure-probe","source":"./plugins/probe"}]}`, 0o644)
				claudeNativeAt(t, home, configHome, cwd, "plugin", "marketplace", "add", market)
				claudeNativeAt(t, home, configHome, cwd, "plugin", "install", m.NativePlugin.Binding(), "--scope", "user", "--json")
				debug := filepath.Join(home, "debug.log")
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, binary, "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--no-session-persistence", "--debug-file", debug, "AIPACK_PROMPT")
				cmd.WaitDelay, cmd.Dir = time.Second, cwd
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + server.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + server.URL, "HTTPS_PROXY=" + server.URL, "NO_PROXY=127.0.0.1,localhost"}
				mu.Lock()
				requests = nil
				mu.Unlock()
				started := time.Now()
				out, runErr := cmd.CombinedOutput()
				elapsed, timedOut := time.Since(started), ctx.Err() != nil
				cancel()
				if timedOut {
					t.Fatalf("fixture hung (converted=%t): %s", converted, out)
				}
				mu.Lock()
				count, allRequests := len(requests), bytes.Join(requests, nil)
				mu.Unlock()
				wantRequests := 1
				if mode == "prompt-block" {
					wantRequests = 0
				} else if mode == "stop-block" {
					wantRequests = 2
				}
				if count != wantRequests || (mode != "prompt-block" && !bytes.Contains(out, []byte("AIPACK_RESPONSE"))) {
					t.Fatalf("native failure behavior (converted=%t): requests=%d want=%d error=%v\n%s", converted, count, wantRequests, runErr, out)
				}
				if strings.HasSuffix(mode, "timeout") && (elapsed < time.Second || elapsed >= 6*time.Second || bytes.Contains(allRequests, []byte("AIPACK_TIMEOUT_"))) {
					t.Fatalf("timeout/output discard changed (converted=%t): elapsed=%s\n%s", converted, elapsed, out)
				}
				if strings.HasSuffix(mode, "error") && !bytes.Contains(mustBytes(t, debug), []byte("AIPACK_NONBLOCKING_ERROR")) {
					t.Fatal("native hook failure was not reported")
				}
				if mode == "stop-block" && !bytes.Contains(allRequests, []byte("AIPACK_BLOCK_REASON")) {
					t.Fatal("Stop blocking reason did not reach resumed model input")
				}
				log := mustBytes(t, filepath.Join(configHome, "plugins/data/failure-probe-aipack-failure/events.jsonl"))
				if strings.HasSuffix(mode, "timeout") {
					target := "UserPromptSubmit"
					if mode == "stop-timeout" {
						target = "Stop"
					}
					for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
						var event struct{ Event, Status string }
						if err := json.Unmarshal(line, &event); err != nil {
							t.Fatal(err)
						}
						if event.Event == target && event.Status == "finished" {
							t.Fatal("timed-out hook continued past its sleep")
						}
					}
				}
				if converted && (!bytes.Equal(log, baseline) || (runErr != nil) != baselineFailed) {
					t.Fatalf("source/converted outcomes differ: error=%v\n%s\nvs\n%s", runErr, log, baseline)
				}
				baseline, baselineFailed = log, runErr != nil
			}
		})
	}
}

func claudeFixture(t *testing.T, root string) {
	t.Helper()
	write(t, root, ".claude-plugin/plugin.json", `{"name":"parity-probe","version":"1.0.0","description":"Native parity fixture","author":{"name":"shrug-labs"},"skills":["./extra-skills"],"commands":{"inline":{"content":"Return INLINE_COMMAND."},"custom":{"source":"./custom/action.md"}},"agents":["./custom/reviewer.md"],"hooks":["./config/hooks.json",{"Stop":[{"hooks":[{"type":"command","command":"echo INLINE_STOP"}]}]}],"mcpServers":["./config/mcp.json",{"shared":{"command":"echo","args":["override"]}}],"metadata":{"preserve":true}}`, 0o644)
	write(t, root, "skills/default/SKILL.md", "---\nname: default\ndescription: Default native fixture.\n---\nDEFAULT_SKILL\n", 0o644)
	write(t, root, "extra-skills/custom-skill/SKILL.md", "---\nname: custom-skill\ndescription: Custom native fixture.\n---\nCUSTOM_SKILL\n", 0o644)
	write(t, root, "commands/ignored.md", "IGNORED_COMMAND\n", 0o644)
	write(t, root, "agents/ignored.md", "---\nname: ignored\ndescription: Ignored agent.\n---\nIGNORED_AGENT\n", 0o644)
	write(t, root, "custom/action.md", "---\ndescription: Custom native command.\n---\nCUSTOM_COMMAND\n", 0o644)
	write(t, root, "custom/reviewer.md", "---\nname: reviewer\ndescription: Custom native agent.\n---\nREVIEWER_AGENT\n", 0o644)
	write(t, root, "custom/asset.txt", "retained asset\n", 0o644)
	write(t, root, "hooks/hooks.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo DEFAULT_PROMPT"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo DEFAULT_STOP"}]}]}}`, 0o644)
	write(t, root, "config/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo CUSTOM_STOP","timeout":17}]}]}}`, 0o644)
	write(t, root, ".mcp.json", `{"mcpServers":{"default":{"command":"echo"},"shared":{"command":"echo","args":["default"]}}}`, 0o644)
	write(t, root, "config/mcp.json", `{"custom":{"command":"echo"},"shared":{"command":"echo","args":["custom"]}}`, 0o644)
	write(t, root, "LICENSE", "Synthetic fixture: MIT\n", 0o644)
}

func claudeNative(t *testing.T, home, cwd string, args ...string) []byte {
	t.Helper()
	return claudeNativeAt(t, home, filepath.Join(home, ".claude"), cwd, args...)
}

func claudeNativeAt(t *testing.T, home, configHome, cwd string, args ...string) []byte {
	t.Helper()
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "HOME=") && !strings.HasPrefix(value, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(value, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+home, "CLAUDE_CONFIG_DIR="+configHome, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native Claude %v: %v\n%s", args, err, body)
	}
	return body
}

func TestClaudeNativeScopes(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 to exercise native scope persistence")
	}
	home, configHome, market, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	claudeFixture(t, filepath.Join(market, "plugins/probe"))
	write(t, market, ".claude-plugin/marketplace.json", `{"name":"aipack-claude-fixture","owner":{"name":"shrug-labs"},"plugins":[{"name":"parity-probe","source":"./plugins/probe"}]}`, 0o644)
	claudeNativeAt(t, home, configHome, project, "plugin", "marketplace", "add", market)
	binding := "parity-probe@aipack-claude-fixture"
	for _, scope := range []string{"user", "project", "local"} {
		claudeNativeAt(t, home, configHome, project, "plugin", "install", binding, "--json", "--scope", scope)
	}
	read := func(path string) map[string]any {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal(body, &root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	for _, path := range []string{filepath.Join(configHome, "settings.json"), filepath.Join(project, ".claude/settings.json"), filepath.Join(project, ".claude/settings.local.json")} {
		if read(path)["enabledPlugins"].(map[string]any)[binding] != true {
			t.Fatalf("native activation missing from %s", path)
		}
	}
	marketConfig := read(filepath.Join(configHome, "plugins/known_marketplaces.json"))["aipack-claude-fixture"].(map[string]any)
	if marketConfig["source"].(map[string]any)["source"] != "directory" || marketConfig["installLocation"] != market {
		t.Fatalf("native local marketplace registration: %+v", marketConfig)
	}
	if read(filepath.Join(configHome, "settings.json"))["extraKnownMarketplaces"].(map[string]any)["aipack-claude-fixture"] == nil {
		t.Fatal("native marketplace add did not persist its user settings entry")
	}
	statePath := filepath.Join(configHome, "plugins/installed_plugins.json")
	state := read(statePath)
	entries := state["plugins"].(map[string]any)[binding].([]any)
	if state["version"] != float64(2) || len(entries) != 3 {
		t.Fatalf("native scope state: %+v", state)
	}
	cache := filepath.Join(configHome, "plugins/cache/aipack-claude-fixture/parity-probe/1.0.0")
	for i, scope := range []string{"user", "project", "local"} {
		entry := entries[i].(map[string]any)
		if entry["scope"] != scope || entry["installPath"] != cache || scope != "user" && entry["projectPath"] != canonicalTestPath(t, project) {
			t.Fatalf("native scope identity: %+v", entry)
		}
	}
	write(t, configHome, "plugins/data/parity-probe-aipack-claude-fixture/marker", "retained runtime data", 0o600)
	claudeNativeAt(t, home, configHome, project, "mcp", "add", "standalone", "--scope", "user", "--", "python3")
	if read(filepath.Join(configHome, ".claude.json"))["mcpServers"].(map[string]any)["standalone"] == nil {
		t.Fatal("native user MCP config did not follow CLAUDE_CONFIG_DIR")
	}
	claudeNativeAt(t, home, configHome, project, "plugin", "uninstall", binding, "--json", "--scope", "local", "--keep-data")
	entries = read(statePath)["plugins"].(map[string]any)[binding].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["scope"] != "user" || entries[1].(map[string]any)["scope"] != "project" {
		t.Fatalf("local removal changed another scope: %+v", entries)
	}
	if read(filepath.Join(project, ".claude/settings.local.json"))["enabledPlugins"].(map[string]any)[binding] != nil {
		t.Fatal("native local uninstall left its activation")
	}
	if _, err := os.Stat(filepath.Join(cache, ".claude-plugin/plugin.json")); err != nil {
		t.Fatal("local uninstall removed another scope's cache")
	}
	if _, err := os.Stat(filepath.Join(configHome, "plugins/data/parity-probe-aipack-claude-fixture/marker")); err != nil {
		t.Fatal("native scoped uninstall removed persistent data")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/plugins")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE_CONFIG_DIR escaped into default home: %v", err)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func claudeCatalogFixture(t *testing.T, root string, manifest bool) map[string]any {
	t.Helper()
	command := func(tag string) string { return `python3 "${CLAUDE_PLUGIN_ROOT}/scripts/record.py" ` + tag }
	write(t, root, "skills/default/SKILL.md", "---\nname: default\ndescription: Catalog fixture.\n---\nDEFAULT_SKILL\n", 0o644)
	write(t, root, "extra-skills/catalog/SKILL.md", "---\nname: catalog\ndescription: Catalog skill.\n---\nCATALOG_SKILL\n", 0o644)
	write(t, root, "custom/reviewer.md", "---\nname: reviewer\ndescription: Catalog reviewer.\n---\nCATALOG_AGENT\n", 0o644)
	write(t, root, "custom/catalog-command.md", "---\ndescription: Catalog command.\n---\nAIPACK_CATALOG_COMMAND\n", 0o644)
	write(t, root, "scripts/record.py", `import json, os, pathlib, sys
request = json.load(sys.stdin)
data = pathlib.Path(os.environ["CLAUDE_PLUGIN_DATA"])
data.mkdir(parents=True, exist_ok=True)
with (data / "events.jsonl").open("a") as log:
    log.write(json.dumps({"event": request["hook_event_name"], "tag": sys.argv[1], "cwd": os.getcwd(), "root": os.environ["CLAUDE_PLUGIN_ROOT"], "data": str(data)}) + "\n")
if request["hook_event_name"] == "UserPromptSubmit":
    print(json.dumps({"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"AIPACK_CATALOG_CONTEXT"}}))
else:
    print("{}")
`, 0o755)
	write(t, root, "scripts/mcp.py", `import json, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    if request["method"] == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "catalog-fixture", "version": "1"}}
    elif request["method"] == "tools/list":
        result = {"tools": []}
    else:
        result = {}
    print(json.dumps({"jsonrpc":"2.0", "id":request["id"], "result":result}), flush=True)
`, 0o755)
	server := map[string]any{"command": "python3", "args": []any{"${CLAUDE_PLUGIN_ROOT}/scripts/mcp.py"}}
	encode := func(value any) string {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	write(t, root, "hooks/hooks.json", encode(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command("default")}}}}}}), 0o644)
	write(t, root, ".mcp.json", encode(map[string]any{"mcpServers": map[string]any{"base": server}}), 0o644)
	if manifest {
		write(t, root, claudeManifest, encode(map[string]any{"name": "catalog-probe", "version": "1.0.0", "commands": map[string]any{"manifest": map[string]any{"content": "MANIFEST_COMMAND"}}, "agents": []any{}, "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command("manifest")}}}}}, "mcpServers": map[string]any{"manifest-mcp": server}}), 0o644)
	}
	return map[string]any{"name": "catalog-probe", "version": "2.0.0", "source": "./plugins/probe", "strict": manifest,
		"description": "Catalog fixture", "author": map[string]any{"name": "shrug-labs"},
		"skills": []any{"./extra-skills/catalog"}, "commands": []any{"./custom/catalog-command.md"},
		"agents": []any{"./custom/reviewer.md"}, "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command("catalog")}}}}},
		"mcpServers": map[string]any{"catalog-mcp": server}, "metadata": map[string]any{"exact": json.Number("9007199254740993")}}
}

func TestClaudeNativeCatalogParity(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native catalog loading")
	}
	var mu sync.Mutex
	var requests []byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		if req.URL.Path != "/v1/messages" {
			http.NotFound(w, req)
			return
		}
		mu.Lock()
		requests = append(requests, body...)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_catalog","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"AIPACK_CATALOG_RESPONSE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			var item struct{ Type string }
			if err := json.Unmarshal([]byte(event), &item); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item.Type, event)
		}
	}))
	defer api.Close()
	for _, rootSource := range []bool{false, true} {
		for _, hasManifest := range []bool{false, true} {
			for _, converted := range []bool{false, true} {
				for _, declarations := range []string{"plain", "repeated", "collision", "collision-repeat", "default-overlap", "default-collision", "object-source", "object-carrier", "object-order", "object-numeric", "object-name", "object-catalog-order", "object-catalog-numeric", "object-cross-agent"} {
					objectCommands := strings.HasPrefix(declarations, "object-")
					if (objectCommands && (rootSource || !hasManifest)) || (!objectCommands && declarations != "plain" && (rootSource || hasManifest)) {
						continue
					}
					name := fmt.Sprintf("root-%t/manifest-%t/converted-%t", rootSource, hasManifest, converted)
					if declarations != "plain" {
						name += "/" + declarations
					}
					t.Run(name, func(t *testing.T) {
						source, pack, market, home, cwd := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
						home, err := filepath.EvalSymlinks(home)
						if err != nil {
							t.Fatal(err)
						}
						cwd, err = filepath.EvalSymlinks(cwd)
						if err != nil {
							t.Fatal(err)
						}
						entry := claudeCatalogFixture(t, source, hasManifest)
						commandAlias := "source-alias"
						if declarations == "object-numeric" || declarations == "object-catalog-numeric" {
							commandAlias = "2"
						}
						write(t, source, "commands/inert.md", "---\nname: ignored-frontmatter\ndescription: Replaced default command.\n---\nINERT_COMMAND\n", 0o644)
						write(t, source, "commands/operations/nested.md", "---\nname: ignored-nested-frontmatter\ndescription: Nested default command.\n---\nNESTED_COMMAND\n", 0o644)
						write(t, source, "agents/inert.md", "---\nname: Frontmatter-Inert\ndescription: Replaced default agent.\n---\nINERT_AGENT\n", 0o644)
						write(t, source, "agents/operations/nested.md", "---\nname: Frontmatter-Nested\ndescription: Nested default agent.\n---\nNESTED_AGENT\n", 0o644)
						write(t, source, "agents/operations/fallback.md", "A native agent without frontmatter.\n", 0o644)
						write(t, source, "custom/review/reviewer.md", "---\nname: Frontmatter-Reviewer\ndescription: Catalog reviewer.\n---\nCATALOG_AGENT\n", 0o644)
						entry["agents"] = []string{"./custom/review/reviewer.md"}
						if declarations == "object-cross-agent" {
							entry["agents"] = append(entry["agents"].([]string), "./custom/catalog-command.md")
						}
						payload := "plugins/probe"
						if rootSource {
							entry["source"], payload = "./", "."
						}
						if objectCommands {
							manifest, err := readObject(source, claudeManifest)
							if err != nil {
								t.Fatal(err)
							}
							commands := map[string]any{"manifest": map[string]any{"content": "MANIFEST_ONLY_OBJECT_BODY"}, "source-alias": map[string]any{"source": "./custom/catalog-command.md"}}
							if err := setJSON(manifest, "commands", commands); err != nil {
								t.Fatal(err)
							}
							if declarations == "object-order" {
								manifest["commands"] = json.RawMessage(`{"source-alias":{"source":"./custom/catalog-command.md"},"a-later-alias":{"source":"./custom/catalog-command.md"},"manifest":{"content":"MANIFEST_ONLY_OBJECT_BODY"}}`)
							}
							if declarations == "object-numeric" {
								manifest["commands"] = json.RawMessage(`{"source-alias":{"source":"./custom/catalog-command.md"},"2":{"source":"./custom/catalog-command.md"},"manifest":{"content":"MANIFEST_ONLY_OBJECT_BODY"}}`)
							}
							if declarations == "object-catalog-order" {
								manifest["commands"] = json.RawMessage(`{"manifest":{"content":"MANIFEST_ONLY_OBJECT_BODY"}}`)
								entry["commands"] = json.RawMessage(`{"source-alias":{"source":"./custom/catalog-command.md"},"a-later-alias":{"source":"./custom/catalog-command.md"}}`)
							}
							if declarations == "object-catalog-numeric" {
								manifest["commands"] = json.RawMessage(`{"manifest":{"content":"MANIFEST_ONLY_OBJECT_BODY"}}`)
								entry["commands"] = json.RawMessage(`{"source-alias":{"source":"./custom/catalog-command.md"},"2":{"source":"./custom/catalog-command.md"}}`)
							}
							body, err := json.Marshal(manifest)
							if err != nil {
								t.Fatal(err)
							}
							write(t, source, claudeManifest, string(body), 0o644)
							if declarations == "object-carrier" || declarations == "object-name" {
								write(t, source, "custom/catalog-extra.md", "---\ndescription: Additional catalog source.\n---\nCATALOG_EXTRA_BODY\n", 0o644)
								entry["commands"] = map[string]any{"manifest": map[string]any{"content": "CATALOG_ONLY_OBJECT_BODY"}, "catalog-inline": map[string]any{"content": "CATALOG_ONLY_OBJECT_BODY"}, "catalog-command": map[string]any{"source": "./custom/catalog-command.md"}, "z-extra-alias": map[string]any{"source": "./custom/catalog-extra.md"}}
								if declarations == "object-name" {
									delete(entry["commands"].(map[string]any), "z-extra-alias")
									entry["commands"].(map[string]any)["manifest"] = map[string]any{"source": "./custom/catalog-extra.md"}
								}
							}
						}
						// Source controls must load independently of converter acceptance.
						m := config.PackManifest{MCP: []string{"base", "catalog-mcp"}, NativePlugin: &domain.NativePlugin{Name: "catalog-probe", Marketplace: "aipack-catalog"}}
						if hasManifest {
							m.MCP[1] = "manifest-mcp"
						}
						if declarations != "plain" && !objectCommands {
							command, agent := "./custom/catalog-command.md", "./custom/review/reviewer.md"
							if declarations == "collision" || declarations == "collision-repeat" {
								command, agent = "./alternative/catalog-command.md", "./alternative/reviewer.md"
								write(t, source, "alternative/catalog-command.md", "---\ndescription: Alternate command.\n---\nALTERNATE_COMMAND\n", 0o644)
								write(t, source, "alternative/reviewer.md", "---\nname: Frontmatter-Reviewer\ndescription: Alternate reviewer.\n---\nALTERNATE_AGENT\n", 0o644)
							}
							if declarations == "default-overlap" {
								command, agent = "./commands/inert.md", "./agents/inert.md"
							}
							if declarations == "default-collision" {
								command, agent = "./alternative/inert.md", "./alternative/inert-agent.md"
								write(t, source, "alternative/inert.md", "---\ndescription: Alternate default command.\n---\nALTERNATE_DEFAULT_COMMAND\n", 0o644)
								write(t, source, "alternative/inert-agent.md", "---\nname: Frontmatter-Inert\ndescription: Alternate default agent.\n---\nALTERNATE_DEFAULT_AGENT\n", 0o644)
							}
							entry["commands"] = []string{"./custom/catalog-command.md", command}
							entry["agents"] = []string{"./custom/review/reviewer.md", agent}
							if declarations == "collision-repeat" {
								entry["commands"] = append(entry["commands"].([]string), "./custom/catalog-command.md")
								entry["agents"] = append(entry["agents"].([]string), "./custom/review/reviewer.md")
							}
						}
						if converted {
							m, err = MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "aipack-catalog", Entry: entry})
							if err != nil {
								t.Fatal(err)
							}
							wantCommands := []string{"manifest", commandAlias}
							if declarations == "object-carrier" {
								wantCommands = append(wantCommands, "z-extra-alias")
							}
							if objectCommands && !slices.Equal(m.Workflows, slices.Sorted(slices.Values(wantCommands))) {
								t.Fatalf("converter inventory differs from native source control: %v", m.Workflows)
							}
							if declarations == "object-name" && !slices.Equal(m.NativePlugin.Components[domain.CategoryWorkflows]["manifest"], []string{"custom/catalog-extra.md", claudeManifest}) {
								t.Fatalf("converter command priority differs from native body: %v", m.NativePlugin.Components[domain.CategoryWorkflows])
							}
						}
						configHome := filepath.Join(home, "native")
						write(t, configHome, "settings.json", `{}`, 0o600)
						selected := selection(m, pack)
						for phase := 0; phase < 6; phase++ {
							if phase == 5 && declarations != "object-cross-agent" {
								break
							}
							if (!converted && phase > 0) || (!objectCommands && (phase == 4 || (hasManifest && phase == 3))) {
								break
							}
							excluded := phase == 1
							selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit", "claude-stop"}
							selected.Selected[domain.CategoryWorkflows] = m.Workflows
							selected.Selected[domain.CategoryAgents] = m.Agents
							selected.Selected[domain.CategoryMCP] = m.MCP
							if excluded {
								selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit"}
								if !hasManifest {
									selected.Selected[domain.CategoryHooks] = nil
								}
								selected.Selected[domain.CategoryWorkflows] = nil
								selected.Selected[domain.CategoryAgents] = nil
								selected.Selected[domain.CategoryMCP] = nil
							}
							if objectCommands && (phase == 3 || phase == 4) {
								selected.Selected[domain.CategoryWorkflows] = []string{commandAlias}
								if phase == 4 {
									selected.Selected[domain.CategoryWorkflows] = []string{"manifest"}
								}
							} else if phase == 5 {
								selected.Selected[domain.CategoryAgents] = []string{"Frontmatter-Reviewer"}
							} else if phase == 3 {
								selected.Selected[domain.CategoryWorkflows] = []string{"operations:nested"}
								selected.Selected[domain.CategoryAgents] = []string{"operations:Frontmatter-Nested"}
							}
							files, err := ReadFiles(source)
							renderedEntry := entry
							if converted {
								files, renderedEntry, err = RenderClaudePackage(selected)
							}
							if err != nil {
								t.Fatal(err)
							}
							if err := os.RemoveAll(filepath.Join(market, payload)); err != nil {
								t.Fatal(err)
							}
							if err := WriteFiles(filepath.Join(market, payload), files); err != nil {
								t.Fatal(err)
							}
							catalog, err := json.Marshal(map[string]any{"name": "aipack-catalog", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{renderedEntry}})
							if err != nil {
								t.Fatal(err)
							}
							write(t, market, ".claude-plugin/marketplace.json", string(catalog), 0o644)
							claudeNativeAt(t, home, configHome, cwd, "plugin", "marketplace", "add", market)
							if phase > 0 {
								claudeNativeAt(t, home, configHome, cwd, "plugin", "uninstall", m.NativePlugin.Binding(), "--scope", "user", "--keep-data", "--json")
							}
							claudeNativeAt(t, home, configHome, cwd, "plugin", "install", m.NativePlugin.Binding(), "--scope", "user", "--json")
							listed := claudeNativeAt(t, home, configHome, cwd, "plugin", "list", "--json")
							var plugins []struct {
								MCP map[string]any `json:"mcpServers"`
							}
							if err := json.Unmarshal(listed, &plugins); err != nil || len(plugins) != 1 || (len(plugins[0].MCP) == 0) != excluded {
								t.Fatalf("native catalog MCP selection differs: %v\n%s", err, listed)
							}
							wantServers := len(m.MCP)
							if excluded {
								wantServers = 0
							}
							if len(plugins[0].MCP) != wantServers {
								t.Fatalf("catalog MCP inventory differs: %v", plugins[0].MCP)
							}
							prompt := "AIPACK_PROMPT"
							ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
							cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--no-session-persistence", prompt)
							cmd.Dir, cmd.WaitDelay = cwd, time.Second
							cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
							mu.Lock()
							requests = nil
							mu.Unlock()
							out, err := cmd.CombinedOutput()
							cancel()
							if err != nil || !bytes.Contains(out, []byte("AIPACK_CATALOG_RESPONSE")) {
								t.Fatalf("native catalog runtime phase=%d: %v\n%s", phase, err, out)
							}
							mu.Lock()
							body := bytes.Clone(requests)
							mu.Unlock()
							foundInventory := false
							for _, line := range bytes.Split(out, []byte("\n")) {
								var init map[string]any
								if json.Unmarshal(line, &init) == nil && init["subtype"] == "init" {
									foundInventory = true
									commands, _ := json.Marshal(init["slash_commands"])
									agents, _ := json.Marshal(init["agents"])
									catalogActive := !excluded && (phase != 3 || objectCommands)
									t.Logf("native declarations=%s commands=%s", declarations, commands)
									catalogCommandActive := catalogActive && !objectCommands
									if objectCommands && bytes.Contains(commands, []byte(`"catalog-probe:`+commandAlias+`"`)) != (!excluded && phase != 4) {
										t.Fatalf("native command source alias differs: %s", commands)
									}
									if (declarations == "object-order" || declarations == "object-catalog-order") && bytes.Contains(commands, []byte(`"catalog-probe:a-later-alias"`)) {
										t.Fatalf("native repeated source alias differs: %s", commands)
									}
									if (declarations == "object-numeric" || declarations == "object-catalog-numeric") && bytes.Contains(commands, []byte(`"catalog-probe:source-alias"`)) {
										t.Fatalf("native numeric command alias differs: %s", commands)
									}
									if declarations == "object-carrier" && (bytes.Contains(commands, []byte(`"catalog-probe:catalog-inline"`)) || bytes.Contains(commands, []byte(`"catalog-probe:z-extra-alias"`)) != (!excluded && phase < 3)) {
										t.Fatalf("native catalog command object differs: %s", commands)
									}
									if declarations == "object-cross-agent" && bytes.Contains(agents, []byte(`"catalog-probe:catalog-command"`)) != (!excluded && phase != 5) {
										t.Fatalf("shared command/agent source lost independent activation: %s", agents)
									}
									if declarations != "plain" && (bytes.Count(commands, []byte(`"catalog-probe:catalog-command"`)) != map[bool]int{false: 0, true: 1}[catalogCommandActive] || bytes.Count(agents, []byte(`"catalog-probe:Frontmatter-Reviewer"`)) != map[bool]int{false: 0, true: 1}[catalogActive]) {
										t.Fatalf("native duplicate inventory changed: %s %s", commands, agents)
									}
									if bytes.Contains(commands, []byte(`"catalog-probe:default"`)) == rootSource || !bytes.Contains(commands, []byte(`"catalog-probe:catalog"`)) || bytes.Contains(agents, []byte(`"catalog-probe:Frontmatter-Reviewer"`)) != catalogActive || bytes.Contains(commands, []byte(`"catalog-probe:catalog-command"`)) != catalogCommandActive || bytes.Contains(commands, []byte(`"catalog-probe:manifest"`)) != (hasManifest && !excluded && (!objectCommands || phase != 3)) || bytes.Contains(commands, []byte(`"catalog-probe:inert"`)) != (!hasManifest && catalogActive) || bytes.Contains(commands, []byte(`"catalog-probe:operations:nested"`)) != (!hasManifest && !excluded) || bytes.Contains(agents, []byte(`"catalog-probe:Frontmatter-Inert"`)) != (!hasManifest && catalogActive) {
										t.Fatalf("catalog component inventory differs: %s %s", commands, agents)
									}
									for _, id := range []string{"catalog-probe:operations:Frontmatter-Nested", "catalog-probe:operations:fallback"} {
										active := !hasManifest && !excluded && (phase != 3 || strings.HasSuffix(id, "Frontmatter-Nested"))
										if bytes.Contains(agents, []byte(`"`+id+`"`)) != active {
											t.Fatalf("nested agent identity differs: %s", agents)
										}
									}
								}
							}
							if !foundInventory {
								t.Fatalf("native runtime omitted component inventory: %s", out)
							}
							if bytes.Contains(body, []byte("AIPACK_CATALOG_CONTEXT")) != hasManifest {
								t.Fatalf("catalog default hook context differs (manifest=%t excluded=%t)", hasManifest, excluded)
							}
							data := filepath.Join(configHome, "plugins/data/catalog-probe-aipack-catalog")
							logPath := filepath.Join(data, "events.jsonl")
							log, err := os.ReadFile(logPath)
							if err != nil && !(excluded && !hasManifest && os.IsNotExist(err)) {
								t.Fatal(err)
							}
							var tags []string
							for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
								if len(line) == 0 {
									continue
								}
								var event map[string]string
								if err := json.Unmarshal(line, &event); err != nil || event["data"] != data || event["cwd"] != cwd || !filepath.IsAbs(event["root"]) {
									t.Fatalf("catalog root/data/cwd changed: %s %v", line, err)
								}
								tags = append(tags, event["tag"])
							}
							want := []string{"default", "catalog"}
							if !hasManifest {
								want = []string{"catalog"}
							}
							if excluded {
								want = want[:len(want)-1]
								if len(want) == 0 {
									want = nil
								}
							}
							if !reflect.DeepEqual(tags, want) {
								t.Fatalf("catalog hook precedence/selection differs: %v want %v", tags, want)
							}
							if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
								t.Fatal(err)
							}
							if !excluded {
								var invocations []struct{ name, marker string }
								if phase != 3 && !objectCommands {
									invocations = append(invocations, struct{ name, marker string }{"catalog-command", "AIPACK_CATALOG_COMMAND"})
								}
								if !hasManifest {
									invocations = append(invocations, struct{ name, marker string }{"operations:nested", "NESTED_COMMAND"})
								}
								if objectCommands {
									marker := "MANIFEST_ONLY_OBJECT_BODY"
									if declarations == "object-name" {
										marker = "CATALOG_EXTRA_BODY"
									}
									if phase != 3 {
										invocations = append(invocations, struct{ name, marker string }{"manifest", marker})
									}
									if phase != 4 {
										invocations = append(invocations, struct{ name, marker string }{commandAlias, "AIPACK_CATALOG_COMMAND"})
									}
								}
								if declarations == "object-carrier" && phase < 3 {
									invocations = append(invocations, struct{ name, marker string }{"z-extra-alias", "CATALOG_EXTRA_BODY"})
								}
								if declarations == "default-collision" && phase != 3 {
									invocations = append(invocations, struct{ name, marker string }{"inert", "INERT_COMMAND"})
								}
								for _, invocation := range invocations {
									ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
									command := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--no-session-persistence", "/catalog-probe:"+invocation.name)
									command.Dir, command.WaitDelay, command.Env = cmd.Dir, cmd.WaitDelay, cmd.Env
									mu.Lock()
									requests = nil
									mu.Unlock()
									out, err := command.CombinedOutput()
									cancel()
									mu.Lock()
									expanded := bytes.Contains(requests, []byte(invocation.marker))
									mu.Unlock()
									if err != nil || !bytes.Contains(out, []byte("AIPACK_CATALOG_RESPONSE")) || !expanded {
										t.Fatalf("catalog command expansion failed: expanded=%t error=%v\n%s", expanded, err, out)
									}
									mu.Lock()
									catalogObjectLoaded := bytes.Contains(requests, []byte("CATALOG_ONLY_OBJECT_BODY"))
									mu.Unlock()
									if objectCommands && catalogObjectLoaded {
										t.Fatal("catalog object replaced the native payload command body")
									}
									if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
										t.Fatal(err)
									}
								}
								if ((declarations == "collision" || declarations == "collision-repeat" || declarations == "default-collision") && phase != 3) || (declarations == "object-cross-agent" && phase != 5) {
									agent, first, second := "Frontmatter-Reviewer", "CATALOG_AGENT", "ALTERNATE_AGENT"
									if declarations == "default-collision" {
										agent, first, second = "Frontmatter-Inert", "INERT_AGENT", "ALTERNATE_DEFAULT_AGENT"
									}
									if declarations == "object-cross-agent" {
										agent, first, second = "catalog-command", "AIPACK_CATALOG_COMMAND", "UNDECLARED_AGENT_BODY"
									}
									attempts := 1
									if declarations == "collision-repeat" && !converted {
										attempts = 3
									}
									for range attempts {
										ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
										command := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--agent", "catalog-probe:"+agent, "--no-session-persistence", "AIPACK_AGENT_PROMPT")
										command.Dir, command.WaitDelay, command.Env = cmd.Dir, cmd.WaitDelay, cmd.Env
										mu.Lock()
										requests = nil
										mu.Unlock()
										out, err := command.CombinedOutput()
										cancel()
										mu.Lock()
										firstLoaded := bytes.Contains(requests, []byte(first))
										secondLoaded := bytes.Contains(requests, []byte(second))
										mu.Unlock()
										if err != nil || !bytes.Contains(out, []byte("AIPACK_CATALOG_RESPONSE")) || firstLoaded == secondLoaded {
											t.Fatalf("native agent did not load exactly one declared body: first=%t second=%t error=%v\n%s", firstLoaded, secondLoaded, err, out)
										}
										// Repeated-path native controls have returned either body.
										// Preserve declarations rather than assuming a stable winner.
										t.Logf("native agent %s: first=%t second=%t", agent, firstLoaded, secondLoaded)
										if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
											t.Fatal(err)
										}
									}
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestClaudeNativeSkillLayouts(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native skill layout parity")
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"msg_skill","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"AIPACK_SKILL_RESPONSE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			var item struct{ Type string }
			if err := json.Unmarshal([]byte(event), &item); err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item.Type, event)
		}
	}))
	defer api.Close()
	for _, layout := range []string{
		"defaults", "default-symbols", "catalog-root", "root-catalog",
		"shared-root", "shared-file", "shared-sidecar", "shared-duplicate", "shared-empty-directory", "shared-assembled", "shared-assembled-duplicate", "shared-assembled-reordered", "shared-assembled-duplicate-reordered",
		"single-root", "single-no-name", "root-no-name", "explicit-root",
		"single-unnamed-symbols", "single-named-symbols", "single-unnamed-unicode",
		"object-git-named", "object-git-unnamed", "object-subdir-unnamed", "object-git-unnamed-version",
		"object-git-unnamed-unversioned", "object-subdir-unnamed-unversioned",
		"object-subdir-unnamed-unversioned-relative", "object-subdir-unnamed-unversioned-symbols",
	} {
		t.Run(layout, func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			if layout == "root-no-name" {
				source = filepath.Join(source, "Mixed...Root__with  spaces__😀")
			}
			entry := map[string]any{"name": "skill-probe", "version": "1.0.0", "source": "./plugins/probe"}
			sharedHooks := map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": `mkdir -p "${CLAUDE_PLUGIN_DATA}"; echo shared >> "${CLAUDE_PLUGIN_DATA}/root-hook.log"`}}}}}
			if !strings.HasPrefix(layout, "single-") && layout != "root-no-name" && !strings.HasPrefix(layout, "object-") {
				write(t, source, "skills/base/SKILL.md", "---\nname: frontmatter-base\ndescription: Native base fixture.\n---\nBASE_SKILL\n", 0o644)
			}
			if layout == "default-symbols" {
				write(t, source, "skills/Mixed...Name__with  spaces__😀/SKILL.md", "---\nname: ignored\ndescription: Native directory fixture.\n---\nDIRECTORY_SKILL\n", 0o644)
			}
			if layout == "catalog-root" || layout == "root-catalog" || strings.HasPrefix(layout, "shared-") {
				entry["source"], entry["skills"] = "./", []string{"./selected"}
				write(t, source, "selected/SKILL.md", "---\nname: frontmatter-selected\ndescription: Native selected fixture.\n---\nSELECTED_SKILL\n", 0o644)
				if strings.HasPrefix(layout, "shared-") {
					write(t, source, "shared/SKILL.md", "---\nname: frontmatter-shared\ndescription: Native shared fixture.\n---\nSHARED_SKILL\n", 0o644)
				}
			}
			if layout == "root-catalog" {
				entry["hooks"] = map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": `mkdir -p "${CLAUDE_PLUGIN_DATA}"; echo native >> "${CLAUDE_PLUGIN_DATA}/root-hook.log"`}}}}}
				original, err := json.Marshal(map[string]any{"name": "skill-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{entry}})
				if err != nil {
					t.Fatal(err)
				}
				write(t, source, ".claude-plugin/marketplace.json", string(original), 0o644)
			}
			if strings.HasPrefix(layout, "shared-assembled") {
				entry["hooks"] = map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": `mkdir -p "${CLAUDE_PLUGIN_DATA}"; echo native >> "${CLAUDE_PLUGIN_DATA}/root-hook.log"`}}}}}
			}
			if strings.HasPrefix(layout, "single-") || layout == "root-no-name" || layout == "explicit-root" || strings.HasPrefix(layout, "object-") {
				write(t, source, "SKILL.md", "---\nname: frontmatter-root\ndescription: Native root fixture.\n---\nROOT_SKILL\n", 0o644)
				if layout == "single-no-name" || layout == "root-no-name" || strings.Contains(layout, "unnamed") {
					write(t, source, "SKILL.md", "---\ndescription: Native root fixture.\n---\nROOT_SKILL\n", 0o644)
				}
				if layout == "root-no-name" {
					entry["source"] = "./"
				}
				if layout == "explicit-root" {
					entry["skills"] = []string{"."}
				}
				if strings.HasSuffix(layout, "symbols") {
					entry["source"] = "./plugins/Mixed.Name_with spaces"
					if layout == "single-named-symbols" {
						write(t, source, "SKILL.md", "---\nname: Frontmatter.Mixed_name\ndescription: Native root fixture.\n---\nROOT_SKILL\n", 0o644)
					}
				}
				if layout == "single-unnamed-unicode" {
					entry["source"] = "./plugins/Mixed...Name__with  spaces__😀"
				}
				if strings.HasPrefix(layout, "object-") {
					write(t, source, "LICENSE", "Synthetic native skill source.\n", 0o644)
					entry["source"] = map[string]any{"source": "url", "url": "https://example.invalid/fixture.git"}
					if strings.HasPrefix(layout, "object-subdir-") {
						entry["source"] = map[string]any{"source": "git-subdir", "url": "https://example.invalid/fixture.git", "path": "nested"}
						if strings.HasSuffix(layout, "relative") {
							entry["source"].(map[string]any)["path"] = "./nested"
						}
						if strings.HasSuffix(layout, "symbols") {
							entry["source"].(map[string]any)["path"] = "Dir.With spaces__😀"
						}
					}
					if layout == "object-git-unnamed-version" {
						entry["version"] = "2.3.4-beta.1+local"
					}
					if strings.Contains(layout, "unversioned") {
						delete(entry, "version")
					}
				}
			}
			var sourceCommit string
			runtimeInventory := func(files []File, catalogEntry map[string]any, rootSource bool) []string {
				t.Helper()
				market, home, cwd := t.TempDir(), t.TempDir(), t.TempDir()
				market = filepath.Join(market, filepath.Base(source))
				payload := filepath.Join(market, "repository")
				if path, ok := catalogEntry["source"].(string); ok {
					payload = filepath.Join(market, filepath.Clean(path))
				}
				repo := payload
				object, copied := catalogEntry["source"].(map[string]any)
				if copied && object["source"] == "git-subdir" {
					payload = filepath.Join(repo, object["path"].(string))
				}
				if rootSource {
					payload = market
				}
				if err := os.MkdirAll(payload, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := WriteFiles(payload, files); err != nil {
					t.Fatal(err)
				}
				if copied {
					for _, args := range [][]string{
						{"init", "--initial-branch=main", repo},
						{"-C", repo, "add", "."},
						{"-C", repo, "-c", "user.name=AIPack Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "--signoff", "-m", "Synthetic native skill source"},
						{"clone", "--bare", repo, filepath.Join(market, "fixture.git")},
					} {
						if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
							t.Fatalf("fixture git %v: %v\n%s", args, err, out)
						}
					}
					if strings.Contains(layout, "unversioned") {
						out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
						if err != nil {
							t.Fatal(err)
						}
						sourceCommit = strings.TrimSpace(string(out))
					}
					object = maps.Clone(object)
					object["url"] = "file://" + filepath.Join(market, "fixture.git")
					catalogEntry = maps.Clone(catalogEntry)
					catalogEntry["source"] = object
				}
				entries := []any{catalogEntry}
				disabled := false
				if strings.HasPrefix(layout, "shared-") {
					if !slices.ContainsFunc(files, func(file File) bool { return file.Path == "selected/SKILL.md" }) || (strings.HasPrefix(layout, "shared-assembled") && strings.Contains(fmt.Sprint(catalogEntry["skills"]), "empty-skills")) {
						disabled = true
						filtered := map[string]any{}
						maps.Copy(filtered, catalogEntry)
						filtered["skills"] = []string{}
						if strings.HasPrefix(layout, "shared-assembled") {
							filtered["skills"] = catalogEntry["skills"]
						}
						if layout == "shared-empty-directory" {
							filtered["skills"] = []string{"./empty-selected"}
							if err := os.MkdirAll(filepath.Join(payload, "empty-selected"), 0o755); err != nil {
								t.Fatal(err)
							}
						}
						entries[0] = filtered
					}
					sharedPath := "./shared"
					if strings.Contains(layout, "duplicate") {
						sharedPath = "./selected"
					}
					sibling := map[string]any{"name": "shared-probe", "source": "./", "version": "1.0.0", "skills": []string{sharedPath}}
					if strings.HasPrefix(layout, "shared-assembled") {
						sibling["hooks"] = sharedHooks
					}
					entries = append(entries, sibling)
				}
				if strings.HasSuffix(layout, "-reordered") {
					slices.Reverse(entries)
				}
				catalog, err := json.Marshal(map[string]any{"name": "skill-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": entries})
				if err != nil {
					t.Fatal(err)
				}
				if layout != "root-catalog" {
					write(t, market, ".claude-plugin/marketplace.json", string(catalog), 0o644)
				}
				configHome := filepath.Join(home, "native")
				write(t, configHome, "settings.json", `{}`, 0o600)
				origin := market
				if layout == "shared-file" {
					origin = filepath.Join(market, ".claude-plugin/marketplace.json")
				} else if layout == "root-catalog" {
					origin = filepath.Join(market, ".aipack-marketplace/marketplace.json")
					write(t, market, ".aipack-marketplace/marketplace.json", string(catalog), 0o600)
				} else if layout == "shared-sidecar" {
					// Match AIPack delivery: keep the original catalog intact and
					// register a separate catalog containing the selected declarations.
					origin = filepath.Join(market, ".aipack-marketplace/marketplace.json")
					write(t, market, ".aipack-marketplace/marketplace.json", string(catalog), 0o600)
					entries[0] = entry
					original, err := json.Marshal(map[string]any{"name": "skill-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": entries})
					if err != nil {
						t.Fatal(err)
					}
					write(t, market, ".claude-plugin/marketplace.json", string(original), 0o644)
				}
				claudeNativeAt(t, home, configHome, cwd, "plugin", "marketplace", "add", origin)
				claudeNativeAt(t, home, configHome, cwd, "plugin", "install", "skill-probe@skill-market", "--scope", "user", "--json")
				if strings.HasPrefix(layout, "shared-") {
					if disabled && !strings.HasPrefix(layout, "shared-assembled") && util.PathExists(filepath.Join(configHome, "plugins/cache/skill-market/skill-probe/1.0.0/selected/SKILL.md")) {
						t.Fatal("disabled skill unexpectedly exists in the first installed cache")
					}
					original, err := ReadFiles(source)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(layout, "shared-assembled") {
						if err := WriteFiles(payload, original); err != nil {
							t.Fatal(err)
						}
					}
					claudeNativeAt(t, home, configHome, cwd, "plugin", "install", "shared-probe@skill-market", "--scope", "user", "--json")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--no-session-persistence", "AIPACK_PROMPT")
				cmd.Dir, cmd.WaitDelay = cwd, time.Second
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
				out, err := cmd.CombinedOutput()
				if err != nil || !bytes.Contains(out, []byte("AIPACK_SKILL_RESPONSE")) {
					t.Fatalf("native skill runtime: %v\n%s", err, out)
				}
				if layout == "root-catalog" {
					hooks, err := json.Marshal(catalogEntry["hooks"])
					want := bytes.Contains(hooks, []byte(`"Stop"`))
					log := filepath.Join(configHome, "plugins/data/skill-probe-skill-market/root-hook.log")
					if err != nil || util.PathExists(log) != want {
						t.Fatalf("root catalog overrode selected Stop execution: want=%t error=%v", want, err)
					}
				}
				if strings.HasPrefix(layout, "shared-assembled") {
					hooks, _ := json.Marshal(catalogEntry["hooks"])
					for _, plugin := range []string{"skill-probe", "shared-probe"} {
						want := plugin == "shared-probe" || bytes.Contains(hooks, []byte(`"Stop"`))
						log := filepath.Join(configHome, "plugins/data", plugin+"-skill-market/root-hook.log")
						if util.PathExists(log) != want {
							t.Fatalf("shared-root Stop selection did not reach native dispatch for %s: want=%t\n%s", plugin, want, out)
						}
					}
				}
				for _, line := range bytes.Split(out, []byte("\n")) {
					var init struct {
						Subtype  string
						Commands []string `json:"slash_commands"`
						Plugins  []struct{ Name, Path string }
					}
					if json.Unmarshal(line, &init) == nil && init.Subtype == "init" {
						if strings.HasPrefix(layout, "shared-") {
							loaded := 0
							for _, native := range init.Plugins {
								if native.Name != "skill-probe" && native.Name != "shared-probe" {
									continue
								}
								actual, err := filepath.EvalSymlinks(native.Path)
								want, wantErr := filepath.EvalSymlinks(payload)
								if err != nil || wantErr != nil || actual != want {
									t.Fatalf("native shared-source plugin loaded a cache instead of its source: %s %v %v", native.Path, err, wantErr)
								}
								loaded++
							}
							if loaded != 2 {
								t.Fatalf("native shared-source runtime lost an installed plugin: %d", loaded)
							}
						}
						var ids []string
						for _, name := range init.Commands {
							if strings.HasPrefix(name, "skill-probe:") {
								ids = append(ids, strings.TrimPrefix(name, "skill-probe:"))
							}
						}
						if strings.HasPrefix(layout, "shared-") {
							name := "shared-probe:frontmatter-shared"
							want := true
							if strings.Contains(layout, "duplicate") {
								name, want = "shared-probe:frontmatter-selected", disabled
								// The native loader deduplicates one source file across
								// plugin namespaces until the earlier declaration is removed.
							}
							if slices.Contains(init.Commands, name) != want {
								t.Fatalf("native shared-source skill discovery changed: %v", init.Commands)
							}
						}
						slices.Sort(ids)
						return ids
					}
				}
				t.Fatalf("native skill inventory absent: %s", out)
				return nil
			}
			files, err := ReadFiles(source)
			if err != nil {
				t.Fatal(err)
			}
			native := runtimeInventory(files, entry, entry["source"] == "./")
			t.Logf("native %s skills: %v", layout, native)
			if strings.Contains(layout, "unversioned") && (len(sourceCommit) < 12 || len(native) != 1 || !strings.HasPrefix(native[0], sourceCommit[:12])) {
				t.Fatalf("native unnamed unversioned skill does not use the source revision: %v vs %s", native, sourceCommit)
			}
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "skill-market", MarketplaceURL: source, Entry: entry, SourceRevision: sourceCommit})
			if err != nil || !reflect.DeepEqual(m.Skills, native) {
				t.Fatalf("converted inventory differs from native: %v vs %v (%v)", m.Skills, native, err)
			}
			selected := selection(m, pack)
			for _, enabled := range []bool{true, false} {
				selected.Selected[domain.CategorySkills] = m.Skills
				if !enabled {
					selected.Selected[domain.CategorySkills] = nil
					if layout == "root-catalog" || strings.HasPrefix(layout, "shared-assembled") {
						selected.Selected[domain.CategoryHooks] = nil
					}
				}
				files, catalogEntry, err := RenderClaudePackage(selected)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(layout, "shared-assembled") {
					secondEntry := map[string]any{"name": "shared-probe", "source": "./", "version": "1.0.0", "skills": []string{"./shared"}}
					secondEntry["hooks"] = sharedHooks
					if strings.Contains(layout, "duplicate") {
						secondEntry["skills"] = []string{"./selected"}
					}
					secondPack := t.TempDir()
					second, err := MaterializeClaude(source, secondPack, "second", domain.PluginSource{Marketplace: "skill-market", MarketplaceURL: source, Entry: secondEntry})
					if err != nil {
						t.Fatal(err)
					}
					secondFiles, secondCatalog, err := RenderClaudePackage(selection(second, secondPack))
					if err != nil {
						t.Fatal(err)
					}
					body, _ := json.Marshal(map[string]any{"name": "skill-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{entry, secondEntry}})
					for _, target := range []*[]File{&files, &secondFiles} {
						*target = append(*target, File{Path: ".claude-plugin", Mode: os.ModeDir | 0o755}, File{Path: ".claude-plugin/marketplace.json", Content: body, Mode: 0o644})
					}
					firstPackage, secondPackage := *m.NativePlugin, *second.NativePlugin
					firstPackage.MarketplaceEntry, secondPackage.MarketplaceEntry = catalogEntry, secondCatalog
					actions := []domain.NativePluginAction{{Package: firstPackage, Files: files, MarketplaceDir: "/synthetic/market"}, {Package: secondPackage, Files: secondFiles, MarketplaceDir: "/synthetic/market"}}
					if err := RenderClaudeSharedRoots(actions); err != nil {
						t.Fatal(err)
					}
					files, catalogEntry = actions[0].Files, actions[0].Package.MarketplaceEntry
				}
				got := runtimeInventory(files, catalogEntry, catalogEntry["source"] == "./")
				want := native
				if !enabled {
					want = nil
					if strings.HasPrefix(layout, "shared-") {
						// This control restores shared source files for the second
						// plugin and tests an empty declaration in the first entry.
						// Empty declarations reactivate native default scanning.
						want = []string{"base"}
						if layout == "shared-empty-directory" || strings.HasPrefix(layout, "shared-assembled") {
							want = nil
						}
						if layout == "shared-sidecar" {
							// Native root-catalog loading overrides the selected sidecar.
							want = native
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("rendered skill selection differs: %v native=%v enabled=%t", got, native, enabled)
				}
			}
		})
	}
}

func TestClaudeUnversionedSourceRevision(t *testing.T) {
	root, pack := t.TempDir(), t.TempDir()
	write(t, root, claudeManifest, `{"name":"revision-probe"}`, 0o644)
	write(t, root, "SKILL.md", "---\ndescription: Synthetic source revision fixture.\n---\nREVISION_SKILL\n", 0o644)
	spec := domain.PluginSource{Marketplace: "revision-market", Entry: map[string]any{"name": "revision-probe", "source": map[string]any{"source": "url", "url": "https://example.invalid/source.git"}}}
	for _, revision := range []string{"", "../source", strings.Repeat("z", 40), strings.Repeat("a", 12)} {
		spec.SourceRevision = revision
		if _, err := ReadClaude(root, spec); err == nil || !strings.Contains(err.Error(), "acquired source revision") {
			t.Fatalf("unresolved or invalid source revision was accepted: %q %v", revision, err)
		}
	}
	for _, revision := range []string{strings.Repeat("ab", 20), strings.Repeat("CD", 32)} {
		spec.SourceRevision = revision
		m, err := MaterializeClaude(root, pack, "alias", spec)
		want := strings.ToLower(revision[:12])
		if err != nil || m.Version != "" || m.NativePlugin.CacheVersion != want || !slices.Equal(m.Skills, []string{want}) {
			t.Fatalf("source revision changed upstream version or native identity: %+v %v", m, err)
		}
		files, entry, err := RenderClaudePackage(selection(m, pack))
		original, readErr := ReadFiles(root)
		if err != nil || readErr != nil || !reflect.DeepEqual(files, original) || entry["version"] != want || spec.Entry["version"] != nil || m.NativePlugin.MarketplaceEntry["version"] != nil {
			t.Fatalf("delivery changed original payload/catalog or lost cache identity: %v %v %v", entry, err, readErr)
		}
	}
}

func TestClaudeLocalMarketplaceRootIdentity(t *testing.T) {
	root := t.TempDir()
	write(t, root, claudeManifest, `{"name":"local-probe","version":"1.0.0"}`, 0o644)
	write(t, root, "SKILL.md", "---\ndescription: Native root directory identity.\n---\nROOT_SKILL\n", 0o644)
	market := filepath.Join(t.TempDir(), "Mixed...Root__with  spaces__😀")
	for _, source := range []any{"./", map[string]any{"source": "url", "url": "https://example.invalid/source.git"}} {
		m, err := ReadClaude(root, domain.PluginSource{Marketplace: "identity-market", MarketplaceURL: market, Entry: map[string]any{"name": "local-probe", "source": source}})
		if err != nil {
			t.Fatal(err)
		}
		if m.NativePlugin.RootDirectoryName != filepath.Base(market) {
			t.Fatalf("colocated entries lost their shared marketplace root: %+v", m.NativePlugin)
		}
		want := "Mixed---Root__with--spaces__--"
		if m.NativePlugin.CopiedSource {
			want = "1-0-0"
		}
		if !slices.Equal(m.Skills, []string{want}) {
			t.Fatalf("native skill identity: %v vs %s", m.Skills, want)
		}
	}
}

func TestClaudeRootCatalogSelection(t *testing.T) {
	source, pack := t.TempDir(), t.TempDir()
	entry := map[string]any{"name": "root-probe", "source": "./", "version": "1.0.0", "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo fixture"}}}}}}
	original := `{"name":"market","owner":{"name":"shrug-labs"},"counter":9007199254740993,"plugins":[{"name":"root-probe","source":"./","version":"1.0.0","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo fixture"}]}]}},{"name":"sibling","source":"./sibling","counter":9007199254740995}]}`
	write(t, source, ".claude-plugin/marketplace.json", original, 0o644)
	m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "market", Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, pack)
	all, _, err := RenderClaudePackage(s)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(file File) bool { return file.Path == ".claude-plugin/marketplace.json" })
	if i < 0 || string(all[i].Content) != original {
		t.Fatal("all-selected root catalog bytes changed")
	}
	s.Selected[domain.CategoryHooks] = nil
	filtered, _, err := RenderClaudePackage(s)
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]any
	for _, file := range filtered {
		if file.Path == ".claude-plugin/marketplace.json" {
			if err := util.UnmarshalJSON(file.Content, &catalog); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries := catalog["plugins"].([]any)
	if catalog["counter"] != json.Number("9007199254740993") || entries[1].(map[string]any)["counter"] != json.Number("9007199254740995") || len(entries[0].(map[string]any)["hooks"].(map[string]any)) != 0 {
		t.Fatalf("selected root catalog changed metadata or retained Stop: %+v", catalog)
	}
	for _, root := range []string{source, filepath.Join(pack, "upstream")} {
		body, err := os.ReadFile(filepath.Join(root, ".claude-plugin/marketplace.json"))
		if err != nil || string(body) != original {
			t.Fatalf("root catalog selection changed upstream source: %s %v", root, err)
		}
	}
}

func TestClaudeCatalogConversion(t *testing.T) {
	for _, hasManifest := range []bool{false, true} {
		t.Run(fmt.Sprintf("manifest-%t", hasManifest), func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			entry := claudeCatalogFixture(t, source, hasManifest)
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "aipack-catalog", Entry: entry})
			if err != nil {
				t.Fatal(err)
			}
			wantVersion := "2.0.0"
			if hasManifest {
				wantVersion = "1.0.0"
			}
			wantHooks := 1
			if hasManifest {
				wantHooks = 2
			}
			if m.Version != wantVersion || (m.NativePlugin.Manifest != "") != hasManifest || len(m.Skills) != 2 || len(m.Agents) != 1 || len(m.Hooks) != wantHooks || len(m.MCP) != 2 {
				t.Fatalf("catalog inventory or metadata changed: %+v", m)
			}
			for _, finding := range config.ValidatePackRoot(pack) {
				if finding.Severity == config.FindingSeverityError {
					t.Fatal(finding.String())
				}
			}
			before, err := ReadFiles(source)
			if err != nil {
				t.Fatal(err)
			}
			selected := selection(m, pack)
			files, renderedEntry, err := RenderClaudePackage(selected)
			if err != nil || !reflect.DeepEqual(before, files) || !reflect.DeepEqual(entry, renderedEntry) {
				t.Fatalf("all-selected catalog or payload changed: %v\nentry=%v", err, renderedEntry)
			}
			selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit"}
			if !hasManifest {
				selected.Selected[domain.CategoryHooks] = nil
			}
			selected.Selected[domain.CategoryWorkflows] = nil
			selected.Selected[domain.CategoryAgents] = nil
			selected.Selected[domain.CategorySkills] = []string{"default"}
			selected.Selected[domain.CategoryMCP] = []string{"base"}
			files, renderedEntry, err = RenderClaudePackage(selected)
			if err != nil {
				t.Fatal(err)
			}
			if len(renderedEntry["hooks"].(map[string]any)) != 0 || len(renderedEntry["commands"].([]any)) != 0 || len(renderedEntry["agents"].([]any)) != 0 {
				t.Fatalf("excluded catalog declarations remain active: %v", renderedEntry)
			}
			if !hasManifest && len(renderedEntry["mcpServers"].(map[string]any)) != 0 {
				t.Fatal("excluded catalog MCP server remains active")
			}
			for _, file := range files {
				if file.Path == "extra-skills/catalog/SKILL.md" || (file.Path == claudeManifest && strings.Contains(string(file.Content), `"Stop"`)) {
					t.Fatalf("excluded source content remains active: %s", file.Path)
				}
			}
			if len(entry["commands"].([]any)) == 0 || entry["hooks"].(map[string]any)["Stop"] == nil {
				t.Fatal("selection mutated source catalog metadata")
			}
			m.NativePlugin.Components[domain.CategoryHooks]["claude-stop"] = []string{""}
			delete(m.NativePlugin.MarketplaceEntry, "hooks")
			if err := config.SavePackManifest(filepath.Join(pack, "pack.json"), m); err != nil {
				t.Fatal(err)
			}
			if _, err := config.LoadPackManifest(filepath.Join(pack, "pack.json")); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range config.ValidatePackRoot(pack) {
				found = found || finding.Severity == config.FindingSeverityError
			}
			if !found {
				t.Fatal("accepted an inline source with no catalog declaration")
			}
		})
	}
}

func TestClaudeCatalogDefaultCommandsAndAgents(t *testing.T) {
	for _, declaration := range []string{"missing", "empty", "explicit"} {
		t.Run(declaration, func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			entry := claudeCatalogFixture(t, source, false)
			write(t, source, "commands/inert.md", "---\ndescription: Default command.\n---\nDEFAULT_COMMAND\n", 0o644)
			write(t, source, "agents/inert.md", "---\nname: inert\ndescription: Default agent.\n---\nDEFAULT_AGENT\n", 0o644)
			if declaration == "missing" {
				delete(entry, "commands")
				delete(entry, "agents")
			} else if declaration == "empty" {
				entry["commands"], entry["agents"] = []string{}, []string{}
			}
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "aipack-catalog", Entry: entry})
			if err != nil || !slices.Contains(m.Workflows, "inert") || !slices.Contains(m.Agents, "inert") {
				t.Fatalf("catalog declarations hid native defaults: %+v %v", m, err)
			}
			s := selection(m, pack)
			s.Selected[domain.CategoryWorkflows], s.Selected[domain.CategoryAgents] = nil, nil
			files, _, err := RenderClaudePackage(s)
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(files, func(file File) bool { return file.Path == "commands/inert.md" || file.Path == "agents/inert.md" }) {
				t.Fatal("excluded default command/agent remains in active payload")
			}
			if !util.PathExists(filepath.Join(pack, "upstream/commands/inert.md")) || !util.PathExists(filepath.Join(pack, "upstream/agents/inert.md")) {
				t.Fatal("selection removed original payload assets")
			}
		})
	}
}

func TestClaudeCommandAndAgentIdentities(t *testing.T) {
	source, pack := t.TempDir(), t.TempDir()
	write(t, source, "commands/operations/action.md", "---\nname: ignored\n---\nOPERATION\n", 0o644)
	write(t, source, "commands/review/action.md", "REVIEW\n", 0o644)
	write(t, source, "agents/operations/reviewer.md", "---\nname: Frontmatter-Reviewer\n---\nOPERATION_AGENT\n", 0o644)
	write(t, source, "agents/review/reviewer.md", "---\nname: Frontmatter-Reviewer\n---\nREVIEW_AGENT\n", 0o644)
	write(t, source, "agents/review/fallback.md", "FALLBACK_AGENT\n", 0o644)
	write(t, source, "custom/nested/declared.md", "---\nname: Declared-Reviewer\n---\nDECLARED_AGENT\n", 0o644)
	entry := map[string]any{"name": "identity-probe", "source": "./plugins/probe", "agents": []string{"./custom/nested/declared.md"}}
	m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "identity-market", Entry: entry})
	if err != nil || !slices.Equal(m.Workflows, []string{"operations:action", "review:action"}) || !slices.Equal(m.Agents, []string{"Declared-Reviewer", "operations:Frontmatter-Reviewer", "review:Frontmatter-Reviewer", "review:fallback"}) {
		t.Fatalf("native component names were flattened or replaced by filenames: %+v %v", m, err)
	}
	for _, finding := range config.ValidatePackRoot(pack) {
		if finding.Severity == config.FindingSeverityError {
			t.Fatal(finding.String())
		}
	}
	s := selection(m, pack)
	s.Selected[domain.CategoryWorkflows] = []string{"operations:action"}
	s.Selected[domain.CategoryAgents] = []string{"Declared-Reviewer", "review:fallback"}
	files, _, err := RenderClaudePackage(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"commands/review/action.md", "agents/operations/reviewer.md", "agents/review/reviewer.md"} {
		if slices.ContainsFunc(files, func(file File) bool { return file.Path == rel }) || !util.PathExists(filepath.Join(pack, "upstream", rel)) {
			t.Fatalf("native selector lost source or retained excluded component: %s", rel)
		}
	}
}

func TestClaudeRepeatedComponents(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(fmt.Sprintf("collision-%t", collision), func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			first, second := "custom", "custom"
			if collision {
				second = "alternative"
			}
			for _, dir := range []string{first, second} {
				write(t, source, dir+"/action.md", "COMMAND\n", 0o644)
				write(t, source, dir+"/reviewer.md", "---\nname: Review-Agent\ndescription: Repeated native agent.\n---\nAGENT\n", 0o644)
			}
			entry := map[string]any{"name": "repeated-probe", "source": "./plugins/probe", "commands": []string{"./" + first + "/action.md", "./" + second + "/action.md"}, "agents": []string{"./" + first + "/reviewer.md", "./" + second + "/reviewer.md"}}
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "repeated-market", Entry: entry})
			if err != nil || !slices.Equal(m.Workflows, []string{"action"}) || !slices.Equal(m.Agents, []string{"Review-Agent"}) {
				t.Fatalf("native repeated declarations were rejected or duplicated: %+v %v", m, err)
			}
			wantSources := 1
			if collision {
				wantSources = 2
			}
			if len(m.NativePlugin.Components[domain.CategoryWorkflows]["action"]) != wantSources || len(m.NativePlugin.Components[domain.CategoryAgents]["Review-Agent"]) != wantSources {
				t.Fatal("native repeated source mappings were lost")
			}
			if m.RelPath(domain.CategoryWorkflows, "action") != "upstream/"+first+"/action.md" {
				t.Fatal("primary source differs from native first-command precedence")
			}
			entry["commands"] = append(entry["commands"].([]string), "./"+first+"/action.md")
			entry["agents"] = append(entry["agents"].([]string), "./"+first+"/reviewer.md")
			m, err = MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "repeated-market", Entry: entry})
			if err != nil || m.RelPath(domain.CategoryWorkflows, "action") != "upstream/"+first+"/action.md" || len(m.NativePlugin.Components[domain.CategoryAgents]["Review-Agent"]) != wantSources {
				t.Fatalf("repeated path lost native command precedence or agent sources: %+v %v", m, err)
			}
			s := selection(m, pack)
			files, rendered, err := RenderClaudePackage(s)
			original, readErr := ReadFiles(source)
			if err != nil || readErr != nil || !reflect.DeepEqual(original, files) || fmt.Sprint(entry["commands"]) != fmt.Sprint(rendered["commands"]) || fmt.Sprint(entry["agents"]) != fmt.Sprint(rendered["agents"]) {
				t.Fatalf("all-selected native declaration order or payload changed: %v %v", err, readErr)
			}
			s.Selected[domain.CategoryWorkflows], s.Selected[domain.CategoryAgents] = nil, nil
			files, rendered, err = RenderClaudePackage(s)
			if err != nil || len(rendered["commands"].([]any)) != 0 || len(rendered["agents"].([]any)) != 0 || !reflect.DeepEqual(original, files) {
				t.Fatalf("excluded repeated declarations remain active or lost source assets: %v %v", rendered, err)
			}
		})
	}
}

func TestClaudeRepeatedDefaultComponents(t *testing.T) {
	for _, hasManifest := range []bool{false, true} {
		t.Run(fmt.Sprintf("manifest-%t", hasManifest), func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			write(t, source, "commands/action.md", "COMMAND\n", 0o644)
			write(t, source, "agents/reviewer.md", "---\nname: Review-Agent\n---\nAGENT\n", 0o644)
			write(t, source, "alternative/action.md", "ALTERNATE_COMMAND\n", 0o644)
			write(t, source, "alternative/reviewer.md", "---\nname: Review-Agent\n---\nALTERNATE_AGENT\n", 0o644)
			if hasManifest {
				write(t, source, claudeManifest, `{"name":"repeated-probe","commands":["./commands/action.md"],"agents":["./agents/reviewer.md"]}`, 0o644)
			}
			entry := map[string]any{"name": "repeated-probe", "source": "./plugins/probe", "commands": []string{"./commands/action.md", "./alternative/action.md", "./alternative/action.md"}, "agents": []string{"./agents/reviewer.md", "./alternative/reviewer.md", "./alternative/reviewer.md"}}
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "repeated-market", Entry: entry})
			if err != nil {
				t.Fatal(err)
			}
			if m.RelPath(domain.CategoryWorkflows, "action") != "upstream/commands/action.md" || len(m.NativePlugin.Components[domain.CategoryWorkflows]["action"]) != 2 || len(m.NativePlugin.Components[domain.CategoryAgents]["Review-Agent"]) != 2 {
				t.Fatal("default and declared source priority or deduplication differs from native loading")
			}
			s := selection(m, pack)
			s.Selected[domain.CategoryWorkflows], s.Selected[domain.CategoryAgents] = nil, nil
			files, rendered, err := RenderClaudePackage(s)
			if err != nil || len(rendered["commands"].([]any)) != 0 || len(rendered["agents"].([]any)) != 0 {
				t.Fatalf("repeated catalog declarations remain active: %v %v", rendered, err)
			}
			for _, rel := range []string{"commands/action.md", "agents/reviewer.md"} {
				retained := slices.ContainsFunc(files, func(file File) bool { return file.Path == rel })
				if retained != hasManifest || !util.PathExists(filepath.Join(pack, "upstream", rel)) {
					t.Fatalf("default fallback or manifest declaration asset retention differs: %s", rel)
				}
			}
			if hasManifest {
				for _, file := range files {
					if file.Path == claudeManifest {
						var raw map[string]json.RawMessage
						if err := json.Unmarshal(file.Content, &raw); err != nil {
							t.Fatal(err)
						}
						for _, key := range []string{"commands", "agents"} {
							var paths []string
							if err := json.Unmarshal(raw[key], &paths); err != nil || len(paths) != 0 {
								t.Fatalf("payload declaration survived catalog exclusion: %s", file.Content)
							}
						}
					}
				}
			}
		})
	}
}

func TestClaudeCommandObjectSelection(t *testing.T) {
	source, pack := t.TempDir(), t.TempDir()
	write(t, source, "custom/action.md", "COMMAND_SOURCE_BODY\n", 0o644)
	write(t, source, claudeManifest, `{"name":"probe","commands":{"z-first":{"source":"./custom/action.md"},"a-later":{"source":"./custom/action.md"},"inline":{"content":"INLINE_BODY"},"other-inline":{"content":"OTHER_INLINE_BODY"}}}`, 0o644)
	entry := map[string]any{"name": "probe", "source": "./probe", "commands": map[string]any{"z-first": map[string]any{"content": "IGNORED_CATALOG_BODY"}}, "agents": []string{"./custom/action.md"}}
	m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "fixture", Entry: entry})
	if err != nil || !slices.Equal(m.Workflows, []string{"inline", "other-inline", "z-first"}) {
		t.Fatalf("native command inventory differs: %v %v", m.Workflows, err)
	}
	s := selection(m, pack)
	for _, keep := range [][]string{m.Workflows, {"z-first"}, {"inline"}, nil, m.Workflows} {
		s.Selected[domain.CategoryWorkflows] = keep
		files, catalog, err := RenderClaudePackage(s)
		raw, marshalErr := json.Marshal(catalog["commands"])
		var commands map[string]any
		decodeErr := util.UnmarshalJSON(raw, &commands)
		if err != nil || marshalErr != nil || decodeErr != nil || !reflect.DeepEqual(commands, entry["commands"]) {
			t.Fatalf("ignored catalog object changed: %v %v", catalog, err)
		}
		if slices.Equal(keep, m.Workflows) {
			original, err := ReadFiles(source)
			if err != nil || !reflect.DeepEqual(original, files) {
				t.Fatal("all-selected commands changed original declarations or their order")
			}
		}
		filtered := t.TempDir()
		if err := WriteFiles(filtered, files); err != nil {
			t.Fatal(err)
		}
		loaded, err := ReadClaude(filtered, domain.PluginSource{Marketplace: "fixture", Entry: catalog})
		if err != nil || !slices.Equal(loaded.Workflows, keep) {
			t.Fatalf("filtered command selection differs: %v want %v: %v", loaded.Workflows, keep, err)
		}
		if !slices.Equal(loaded.Agents, m.Agents) {
			t.Fatalf("command selection disabled a selected agent: %v", loaded.Agents)
		}
		if !util.PathExists(filepath.Join(filtered, "custom/action.md")) {
			t.Fatal("selection discarded the shared source asset")
		}
	}
	s.Selected[domain.CategoryAgents] = nil
	files, catalog, err := RenderClaudePackage(s)
	if err != nil {
		t.Fatal(err)
	}
	filtered := t.TempDir()
	if err := WriteFiles(filtered, files); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadClaude(filtered, domain.PluginSource{Marketplace: "fixture", Entry: catalog})
	if err != nil || len(loaded.Agents) != 0 || !slices.Equal(loaded.Workflows, m.Workflows) {
		t.Fatalf("agent exclusion changed shared command activation: %+v %v", loaded, err)
	}
	ids, err := claudeCommandIDs(json.RawMessage(`{"later":{},"02":{},"4294967295":{},"2":{},"1":{},"later":{},"0":{}}`))
	if err != nil || !slices.Equal(ids, []string{"0", "1", "2", "later", "02", "4294967295"}) {
		t.Fatalf("native object enumeration differs: %v %v", ids, err)
	}
}

func TestClaudeConversionAndInventory(t *testing.T) {
	source, converted := t.TempDir(), t.TempDir()
	claudeFixture(t, source)
	m, err := MaterializeClaude(source, converted, "local-alias", domain.PluginSource{Marketplace: "aipack-claude-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "local-alias" || m.NativePlugin.Binding() != "parity-probe@aipack-claude-fixture" || m.NativePlugin.Harness != domain.HarnessClaudeCode {
		t.Fatalf("native identity changed: %+v", m)
	}
	if !reflect.DeepEqual(m.Skills, []string{"custom-skill", "default"}) || !reflect.DeepEqual(m.Workflows, []string{"custom", "inline"}) || !reflect.DeepEqual(m.Agents, []string{"reviewer"}) || len(m.MCP) != 3 {
		t.Fatalf("native default/declaration inventory: %+v", m)
	}
	if len(m.NativePlugin.Components[domain.CategoryHooks]["claude-stop"]) != 3 || !reflect.DeepEqual(m.NativePlugin.Components[domain.CategoryMCP]["shared"], []string{claudeManifest}) {
		t.Fatalf("hook merge or MCP override lost: %+v", m.NativePlugin)
	}
	for _, finding := range config.ValidatePackRoot(converted) {
		if finding.Severity == config.FindingSeverityError {
			t.Fatal(finding.String())
		}
	}
	before, err := ReadFiles(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadFiles(filepath.Join(converted, "upstream"))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("conversion changed complete payload: %v", err)
	}
	selected := selection(m, converted)
	rendered, err := RenderClaude(selected)
	if err != nil || !reflect.DeepEqual(before, rendered) {
		t.Fatalf("all-selected native view changed: %v", err)
	}
	selected.Selected[domain.CategorySkills] = []string{"default"}
	selected.Selected[domain.CategoryAgents] = nil
	selected.Selected[domain.CategoryWorkflows] = []string{"inline"}
	selected.Selected[domain.CategoryHooks] = []string{"claude-user-prompt-submit"}
	selected.Selected[domain.CategoryMCP] = []string{"default"}
	rendered, err = RenderClaude(selected)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range rendered {
		if file.Path == "extra-skills/custom-skill/SKILL.md" {
			t.Fatal("excluded skill remains discoverable")
		}
		if file.Path == "hooks/hooks.json" || file.Path == "config/hooks.json" || file.Path == claudeManifest {
			if strings.Contains(string(file.Content), `"Stop"`) {
				t.Fatalf("excluded event remains in %s", file.Path)
			}
		}
	}
	filtered := t.TempDir()
	if err := WriteFiles(filtered, rendered); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filtered, "custom/action.md")); err != nil {
		t.Fatal("excluded explicit command removed its shared source asset")
	}
	if _, err := os.Stat(filepath.Join(filtered, "custom/reviewer.md")); err != nil {
		t.Fatal("excluded explicit agent removed its shared source asset")
	}
	second, err := MaterializeClaude(source, t.TempDir(), "local-alias", domain.PluginSource{Marketplace: "aipack-claude-fixture"})
	if err != nil || !reflect.DeepEqual(m, second) {
		t.Fatalf("conversion is not deterministic: %v", err)
	}
	for _, raw := range []string{
		`{"name":"parity-probe","agents":"./agents"}`,
		`{"name":"parity-probe","skills":"../outside"}`,
		`{"name":"parity-probe","commands":{"bad":{"source":"./custom/action.md","content":"both"}}}`,
		`{"name":"parity-probe","mcpServers":"./bundle.mcpb"}`,
	} {
		write(t, source, claudeManifest, raw, 0o644)
		if _, err := ReadClaude(source, domain.PluginSource{Marketplace: "aipack-claude-fixture"}); err == nil {
			t.Fatalf("accepted invalid or unimplemented native declaration: %s", raw)
		}
	}
}

func TestClaudeNativeLocalMarketplace(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 to exercise the native installer")
	}
	home, market, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(market, "plugins/probe")
	claudeFixture(t, source)
	write(t, market, ".claude-plugin/marketplace.json", `{"name":"aipack-claude-fixture","owner":{"name":"shrug-labs"},"plugins":[{"name":"parity-probe","source":"./plugins/probe"}]}`, 0o644)
	claudeNative(t, home, cwd, "plugin", "validate", market, "--json")
	claudeNative(t, home, cwd, "plugin", "validate", source, "--json")
	claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
	result := claudeNative(t, home, cwd, "plugin", "install", "parity-probe@aipack-claude-fixture", "--json", "--scope", "user")
	var installed map[string]any
	if err := json.Unmarshal(result, &installed); err != nil {
		t.Fatalf("install result: %s %v", result, err)
	}
	t.Logf("native install: %s", result)
	listed := claudeNative(t, home, cwd, "plugin", "list", "--json")
	t.Logf("native list: %s", listed)
	if !strings.Contains(string(listed), "parity-probe@aipack-claude-fixture") {
		t.Fatal("native binding was not retained")
	}
	var native []struct {
		ID  string         `json:"id"`
		MCP map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(listed, &native); err != nil || len(native) != 1 || len(native[0].MCP) != 3 {
		t.Fatalf("native server inventory: %s %v", listed, err)
	}
	converted := t.TempDir()
	if _, err := MaterializeClaude(source, converted, "local-alias", domain.PluginSource{Marketplace: "aipack-claude-fixture"}); err != nil {
		t.Fatal(err)
	}
	// Marketplace paths are relative to its root. Place the converted source
	// inside a separate marketplace rather than changing native path semantics.
	convertedMarket := t.TempDir()
	files, err := ReadFiles(filepath.Join(converted, "upstream"))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(filepath.Join(convertedMarket, "plugins/probe"), files); err != nil {
		t.Fatal(err)
	}
	write(t, convertedMarket, ".claude-plugin/marketplace.json", `{"name":"aipack-claude-fixture","owner":{"name":"shrug-labs"},"plugins":[{"name":"parity-probe","source":"./plugins/probe"}]}`, 0o644)
	otherHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(otherHome, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	claudeNative(t, otherHome, cwd, "plugin", "marketplace", "add", convertedMarket)
	claudeNative(t, otherHome, cwd, "plugin", "install", "parity-probe@aipack-claude-fixture", "--json", "--scope", "user")
	listed = claudeNative(t, otherHome, cwd, "plugin", "list", "--json")
	var imported []struct {
		ID  string         `json:"id"`
		MCP map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(listed, &imported); err != nil || !reflect.DeepEqual(native, imported) {
		t.Fatalf("converted native identity/MCP differs: %s %v", listed, err)
	}
	m, err := ReadClaude(source, domain.PluginSource{Marketplace: "aipack-claude-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, converted)
	s.Selected[domain.CategoryMCP] = []string{"default"}
	s.Selected[domain.CategoryAgents], s.Selected[domain.CategoryWorkflows], s.Selected[domain.CategoryHooks] = nil, nil, nil
	filtered, err := RenderClaude(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(convertedMarket, "plugins/probe")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(filepath.Join(convertedMarket, "plugins/probe"), filtered); err != nil {
		t.Fatal(err)
	}
	claudeNative(t, otherHome, cwd, "plugin", "validate", filepath.Join(convertedMarket, "plugins/probe"), "--json")
	// Local directory loading observes a new selected view after restart.
	listed = claudeNative(t, otherHome, cwd, "plugin", "list", "--json")
	imported = nil
	if err := json.Unmarshal(listed, &imported); err != nil || len(imported) != 1 || len(imported[0].MCP) != 1 || imported[0].MCP["default"] == nil {
		t.Fatalf("native selection refresh: %s %v", listed, err)
	}
	for _, removal := range []struct {
		home string
		keep bool
	}{{home, false}, {otherHome, true}} {
		data := filepath.Join(removal.home, ".claude/plugins/data/parity-probe-aipack-claude-fixture/marker")
		write(t, removal.home, ".claude/plugins/data/parity-probe-aipack-claude-fixture/marker", "runtime state", 0o600)
		args := []string{"plugin", "uninstall", "parity-probe@aipack-claude-fixture", "--json", "--scope", "user"}
		if removal.keep {
			args = append(args, "--keep-data")
		}
		claudeNative(t, removal.home, cwd, args...)
		_, err := os.Stat(data)
		if removal.keep && err != nil || !removal.keep && !os.IsNotExist(err) {
			t.Fatalf("native uninstall data behavior (keep=%t): %v", removal.keep, err)
		}
	}
}
