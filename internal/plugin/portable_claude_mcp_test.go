package plugin

import (
	"bufio"
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

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCodexMCPClaudeRefusals(t *testing.T) {
	for _, test := range []struct {
		name, format, fields, reason string
	}{
		{"startup", CodexLegacy, `"startup_timeout_sec":0`, "positive finite"},
		{"null-args", AgentPlugins, `"args":null`, "string array"},
		{"null-arg-item", AgentPlugins, `"args":[null]`, "string array"},
		{"null-env-value", AgentPlugins, `"env":{"TOKEN":null}`, "string values"},
		{"reserved-env", AgentPlugins, `"env":{"PLUGIN_ROOT":"replacement"}`, "reserved environment"},
		{"target-reserved-env", CodexLegacy, `"env":{"CLAUDE_PLUGIN_ROOT":"replacement"}`, "target-owned"},
		{"env-overlay", AgentPlugins, `"command":"python3"`, "env_vars overlay"},
		{"legacy-literal-env", CodexLegacy, `"env":{"TOKEN":"${TOKEN}"}`, "literal braced environment"},
		{"literal-env-name", CodexLegacy, `"env":{"${TOKEN}":"literal"}`, "environment names"},
		{"agent-literal-args", AgentPlugins, `"args":["${CODEX_PLUGIN_ROOT}"]`, "literal braced environment"},
		{"agent-mixed-refs", AgentPlugins, `"args":["${PLUGIN_ROOT}/${TOKEN}"]`, "literal braced environment"},
		{"missing-agent-type", AgentPlugins, `"type":null`, "stdio transport"},
		{"invalid-agent-cwd", AgentPlugins, `"cwd":"."`, "Agent Plugins cwd"},
		{"invalid-agent-command", AgentPlugins, `"command":"${PLUGIN_ROOT}/server"`, "relative program paths"},
		{"empty-agent-program", AgentPlugins, `"command":"./"`, "relative program paths"},
		{"absolute-agent-command", AgentPlugins, `"command":"/bin/echo"`, "absolute commands"},
		{"agent-extra", AgentPlugins, `"tool_timeout_sec":3`, "tool_timeout_sec"},
		{"relative-cwd", CodexLegacy, `"cwd":"../other"`, "within the plugin payload"},
		{"root-cwd", AgentPlugins, `"cwd":"${PLUGIN_ROOT}/../other"`, "within the referenced root"},
		{"root-program", AgentPlugins, `"command":"./../server"`, "within the referenced payload"},
		{"short-timeout", CodexLegacy, `"tool_timeout_sec":0.5`, "between 1000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.name == "env-overlay" {
				write(t, root, "upstream/.codex-plugin/plugin.json", `{"name":"fixture","mcpServers":{"probe":{"command":"python3","env_vars":["TOKEN"]}}}`, 0o644)
			}
			entry := map[string]json.RawMessage{"type": json.RawMessage(`"stdio"`), "command": json.RawMessage(`"python3"`), "cwd": json.RawMessage(`"./"`)}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte("{"+test.fields+"}"), &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				entry[key] = value
			}
			body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"probe": entry}})
			write(t, root, "upstream/mcp.json", string(body), 0o644)
			s := domain.NativePluginSelection{Root: root, Package: domain.NativePlugin{Harness: domain.HarnessCodex, Format: test.format, ConverterVersion: ConverterVersion,
				Components: map[domain.PackCategory]map[string][]string{domain.CategoryMCP: {"probe": {"mcp.json"}}}}, Selected: map[domain.PackCategory][]string{domain.CategoryMCP: {"probe"}}}
			report := Compatibility(s, domain.HarnessClaudeCode)
			if len(report.Supported) != 0 || len(report.Unsupported) != 1 || !strings.Contains(report.Unsupported[0], test.reason) {
				t.Fatalf("unsafe or unsupported declaration accepted: %+v", report)
			}
		})
	}
}

func TestGenericMCPRefusesPackPlaceholders(t *testing.T) {
	for _, target := range []domain.Harness{domain.HarnessCline, domain.HarnessClaudeCode, domain.HarnessOpenCode} {
		for _, format := range []string{CodexLegacy, AgentPlugins} {
			for _, placeholder := range []string{"{env:TOKEN}", "{params.token}", "{pack:root}"} {
				t.Run(string(target)+"/"+format+"/"+placeholder, func(t *testing.T) {
					root := t.TempDir()
					body, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"probe": map[string]any{
						"type": "stdio", "command": "python3", "cwd": "./", "args": []string{placeholder},
					}}})
					if err != nil {
						t.Fatal(err)
					}
					write(t, root, "upstream/mcp.json", string(body), 0o644)
					s := domain.NativePluginSelection{Root: root, Package: domain.NativePlugin{Format: format,
						Components: map[domain.PackCategory]map[string][]string{domain.CategoryMCP: {"probe": {"mcp.json"}}}}}
					if _, err := GenericMCPServer(s, "probe", target, filepath.Join(root, "data")); err == nil {
						t.Fatalf("literal source placeholder was accepted: %v", err)
					}
				})
			}
		}
	}
}

func TestGenericMCPRetainsNativeTimeout(t *testing.T) {
	root := t.TempDir()
	write(t, root, "upstream/mcp.json", `{"mcpServers":{"probe":{"command":"python3","cwd":".","tool_timeout_sec":3}}}`, 0o644)
	s := domain.NativePluginSelection{Root: root, Package: domain.NativePlugin{Harness: domain.HarnessCodex, Format: CodexLegacy, Name: "fixture", Marketplace: "owned", ConverterVersion: ConverterVersion,
		Components: map[domain.PackCategory]map[string][]string{domain.CategoryMCP: {"probe": {"mcp.json"}}}}, Selected: map[domain.PackCategory][]string{domain.CategoryMCP: {"probe"}}}
	if _, err := GenericMCPServer(s, "probe", domain.HarnessClaudeCode, filepath.Join(root, "data")); err == nil || !strings.Contains(err.Error(), "timeout") || GenericContentSelection(s, domain.HarnessClaudeCode) {
		t.Fatal("ordinary conversion silently dropped the native timeout", err)
	}
	report := Compatibility(s, domain.HarnessClaudeCode)
	entry, err := codexClaudeMCP(s, "probe")
	if err != nil || entry["timeout"] != int64(3000) || len(report.Supported) != 1 || len(report.Unsupported) != 0 {
		t.Fatal("verified native timeout fallback was lost", report, entry, err)
	}
}

func TestPortableStartupTimeoutPolicy(t *testing.T) {
	for _, test := range []struct {
		name, policy, seconds string
		target                domain.Harness
		timeout               int
		reason                string
	}{
		{"default", "", "600", domain.HarnessCline, 0, ""},
		{"claude-default", "", "600", domain.HarnessClaudeCode, 0, ""},
		{"opencode-default", "", "30", domain.HarnessOpenCode, 0, ""},
		{"strict", "strict", "600", domain.HarnessOpenCode, 0, "startup_timeout_sec"},
		{"cline", "unified", "600", domain.HarnessCline, 600, ""},
		{"opencode", "unified", "30", domain.HarnessOpenCode, 30, ""},
		{"claude", "host", "600", domain.HarnessClaudeCode, 0, ""},
		{"cline-host", "host", "600", domain.HarnessCline, 0, ""},
		{"opencode-host", "host", "30", domain.HarnessOpenCode, 0, ""},
		{"claude-unified", "unified", "600", domain.HarnessClaudeCode, 0, "cannot express"},
		{"null", "host", "null", domain.HarnessClaudeCode, 0, "positive finite"},
		{"zero", "host", "0", domain.HarnessClaudeCode, 0, "positive finite"},
		{"negative", "unified", "-1", domain.HarnessCline, 0, "positive finite"},
		{"fractional", "unified", "0.5", domain.HarnessCline, 0, "whole seconds"},
		{"overflow", "unified", "2147484", domain.HarnessOpenCode, 0, "whole seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "upstream/mcp.json", `{"mcpServers":{"probe":{"command":"python3","cwd":".","startup_timeout_sec":`+test.seconds+`}}}`, 0o644)
			s := domain.NativePluginSelection{Root: root, SourcePack: "alias", Package: domain.NativePlugin{Harness: domain.HarnessCodex, Format: CodexLegacy, Name: "fixture", Marketplace: "owned", ConverterVersion: ConverterVersion,
				Components: map[domain.PackCategory]map[string][]string{domain.CategoryMCP: {"probe": {"mcp.json"}}}}, Selected: map[domain.PackCategory][]string{domain.CategoryMCP: {"probe"}}, MCPPolicy: map[string]domain.NativeMCPPolicy{"probe": {StartupTimeout: test.policy}}}
			server, err := GenericMCPServer(s, "probe", test.target, filepath.Join(root, "data"))
			report := Compatibility(s, test.target)
			if test.reason != "" {
				if err == nil || !strings.Contains(err.Error(), test.reason) || len(report.Unsupported) != 1 || len(report.Warnings) != 0 {
					t.Fatal("unsafe timeout policy accepted", server, report, err)
				}
				return
			}
			policy := test.policy
			if policy == "" {
				policy = "host"
			}
			if err != nil || server.Timeout != test.timeout || !GenericContentSelection(s, test.target) || len(report.Unsupported) != 0 || len(report.Warnings) != 1 || !strings.Contains(report.Summary(), "startup_timeout="+policy) {
				t.Fatal("explicit policy not preserved/reported", server, report, err)
			}
			if test.target == domain.HarnessOpenCode && test.policy == "unified" {
				entry, err := codexStdioMCP(s, "probe", test.target, "/payload", "/data")
				if err != nil || entry["timeout"] != int64(test.timeout*1000) {
					t.Fatal("native fallback lost unified timeout", entry, err)
				}
			}
			if len(Compatibility(s, domain.HarnessCodex).Warnings) != 0 {
				t.Fatal("native source timing changed")
			}
		})
	}
}

func TestCodexMCPToClaude(t *testing.T) {
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			source, pack, runtimeData := t.TempDir(), t.TempDir(), t.TempDir()
			manifest, mcpPath := ".codex-plugin/plugin.json", ".mcp.json"
			body := `{"name":"portable-mcp","version":"1.0.0"}`
			servers := map[string]any{"mcpServers": map[string]any{
				"probe": map[string]any{"command": "python3", "args": []string{"scripts/server.py", `literal ' $(touch SHOULD_NOT_EXIST);`, `literal $CODEX_PLUGIN_ROOT $PLUGIN_DATA`}, "cwd": ".", "tool_timeout_sec": 3,
					"env": map[string]string{"AIPACK_ROOT": ".", "AIPACK_DATA": runtimeData, "AIPACK_MARKER": "portable marker"}},
				"excluded": map[string]any{"command": "false", "cwd": ".", "startup_timeout_sec": 600},
			}}
			if format == AgentPlugins {
				manifest, mcpPath = "plugin.json", "mcp.json"
				body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable-mcp","version":"1.0.0"}`
				servers["$schema"] = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
				probe := servers["mcpServers"].(map[string]any)["probe"].(map[string]any)
				delete(probe, "tool_timeout_sec")
				delete(probe, "cwd") // Native Agent Plugins defaults to its payload root.
				probe["type"] = "stdio"
				probe["command"] = "./scripts/server.py"
				probe["args"] = []string{`literal ' $(touch SHOULD_NOT_EXIST);`, `literal $CODEX_PLUGIN_ROOT $PLUGIN_DATA`}
				probe["env"] = map[string]string{"AIPACK_ROOT": "${PLUGIN_ROOT}", "AIPACK_DATA": "${PLUGIN_DATA}", "AIPACK_MARKER": "portable marker"}
			}
			write(t, source, manifest, body, 0o644)
			encoded, _ := json.Marshal(servers)
			write(t, source, mcpPath, string(encoded), 0o644)
			write(t, source, "shared.txt", "shared asset", 0o440)
			write(t, source, "scripts/server.py", `#!/usr/bin/env python3
import json, os, pathlib, sys
data = pathlib.Path(os.environ["AIPACK_DATA"])
attempts = data / "attempts.txt"
attempts.write_text(str(int(attempts.read_text()) + 1 if attempts.exists() else 1))
if (data / "fail-setup").exists():
    print("Owned fixture setup failed; retry after correcting its input.", file=sys.stderr)
    sys.exit(2)
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "portable-mcp", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": name, "description": "Inspect the owned fixture.", "inputSchema": {"type": "object", "properties": {}}} for name in ["observe", "hidden"]]}
    elif method == "tools/call":
        record = pathlib.Path(os.environ["AIPACK_DATA"], "observed.json")
        count = json.loads(record.read_text())["count"] + 1 if record.exists() else 1
        observed = {"count": count, "cwd": os.getcwd(), "root": os.path.abspath(os.environ["AIPACK_ROOT"]), "data": os.environ["AIPACK_DATA"], "autoRoot": os.environ.get("PLUGIN_ROOT", ""), "autoData": os.environ.get("PLUGIN_DATA", ""), "marker": os.environ["AIPACK_MARKER"], "args": sys.argv[1:], "asset": pathlib.Path("shared.txt").read_text()}
        pathlib.Path(observed["data"], "observed.json").write_text(json.dumps(observed))
        result = {"content": [{"type": "text", "text": "PORTABLE_MCP_RESULT " + json.dumps(observed)}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
			m, err := MaterializeCodex(source, pack, "alias", "portable-market")
			if err != nil {
				t.Fatal(err)
			}
			s := selection(m, pack)
			s.Selected[domain.CategoryMCP] = []string{"probe"}
			issues := CodexClaudeIssues(s)
			if len(issues) != 0 {
				t.Fatalf("host startup timing blocked delivery: %v", issues)
			}
			files, pkg, err := RenderCodexForClaude(s)
			if err != nil {
				t.Fatal(err)
			}
			home, market, cwd := t.TempDir(), filepath.Join(t.TempDir(), "market with spaces"), t.TempDir()
			if err := WriteFiles(filepath.Join(market, "plugins/portable-mcp"), files); err != nil {
				t.Fatal(err)
			}
			converted, err := ReadClaude(filepath.Join(market, "plugins/portable-mcp"), domain.PluginSource{Marketplace: pkg.Marketplace, Entry: pkg.MarketplaceEntry})
			if err != nil || !slices.Equal(converted.MCP, []string{"probe"}) {
				t.Fatalf("unexpected target server activation: %+v %v", converted, err)
			}
			entry, err := codexClaudeMCP(s, "probe")
			if err != nil || (format == CodexLegacy && entry["timeout"] != int64(3000)) || (format == AgentPlugins && entry["timeout"] != nil) || entry["command"] != "sh" {
				t.Fatalf("working directory or tool timeout lost: %+v %v", entry, err)
			}
			if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
				return
			}
			write(t, market, ".claude-plugin/marketplace.json", `{"name":"portable-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"portable-mcp","source":"./plugins/portable-mcp"}]}`, 0o644)
			claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
			claudeNative(t, home, cwd, "plugin", "install", pkg.Binding(), "--scope", "user", "--json")
			var mu sync.Mutex
			var requests [][]byte
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, _ := io.ReadAll(req.Body)
				if strings.HasSuffix(req.URL.Path, "/count_tokens") {
					fmt.Fprint(w, `{"input_tokens":10}`)
					return
				}
				mu.Lock()
				requests = append(requests, body)
				mu.Unlock()
				content, stop := map[string]any{"type": "text", "text": "PORTABLE_MCP_RESPONSE"}, "end_turn"
				delta := map[string]any{"type": "text_delta", "text": "PORTABLE_MCP_RESPONSE"}
				if !bytes.Contains(body, []byte("PORTABLE_MCP_RESULT")) && !bytes.Contains(body, []byte("No such tool available: mcp__plugin_portable-mcp_probe__observe")) {
					content, stop = map[string]any{"type": "tool_use", "id": "fixture-call", "name": "mcp__plugin_portable-mcp_probe__observe", "input": map[string]any{}}, "tool_use"
					delta = map[string]any{"type": "input_json_delta", "partial_json": "{}"}
				} else {
					content["text"] = ""
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []map[string]any{
					{"type": "message_start", "message": map[string]any{"id": "fixture", "type": "message", "role": "assistant", "content": []any{}, "model": "claude-sonnet-4-5", "stop_reason": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 1}}},
					{"type": "content_block_start", "index": 0, "content_block": content},
					{"type": "content_block_delta", "index": 0, "delta": delta},
					{"type": "content_block_stop", "index": 0},
					{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 2}},
					{"type": "message_stop"},
				} {
					encoded, _ := json.Marshal(event)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
				}
			}))
			defer api.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--allowedTools", "mcp__plugin_portable-mcp_probe__observe", "--no-session-persistence", "Inspect the owned fixture.")
			cmd.Dir, cmd.WaitDelay = cwd, time.Second
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "ENABLE_TOOL_SEARCH=0", "CLAUDE_CODE_MCP_STARTUP_WAIT_MS=5000", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
			if format == AgentPlugins {
				runtimeData = filepath.Join(home, ".claude/plugins/data/portable-mcp-portable-market")
			}
			write(t, runtimeData, "fail-setup", "owned invalid setup input", 0o600)
			installedPath := filepath.Join(home, ".claude/plugins/installed_plugins.json")
			installed, err := os.ReadFile(installedPath)
			if err != nil {
				t.Fatal(err)
			}
			check := exec.CommandContext(ctx, cmd.Path, "mcp", "list")
			check.Dir, check.Env, check.WaitDelay = cmd.Dir, cmd.Env, cmd.WaitDelay
			failed, err := check.CombinedOutput()
			if err != nil || !bytes.Contains(bytes.ToLower(failed), []byte("failed")) || !bytes.Contains(failed, []byte("probe")) {
				t.Fatalf("native setup failure was not visible: %v\n%s", err, failed)
			}
			afterFailure, err := os.ReadFile(installedPath)
			if err != nil || !bytes.Equal(installed, afterFailure) {
				t.Fatalf("runtime setup failure changed installation: %v", err)
			}
			if _, err := os.Stat(filepath.Join(runtimeData, "observed.json")); !os.IsNotExist(err) {
				t.Fatal("failed setup published a successful tool result")
			}
			if _, err := os.Stat(filepath.Join(runtimeData, "attempts.txt")); err != nil {
				t.Fatalf("failure did not execute the owned setup path: %v", err)
			}
			if err := os.Remove(filepath.Join(runtimeData, "fail-setup")); err != nil {
				t.Fatal(err)
			}
			retry := exec.CommandContext(ctx, cmd.Path, "mcp", "list")
			retry.Dir, retry.Env, retry.WaitDelay = cmd.Dir, cmd.Env, cmd.WaitDelay
			retried, err := retry.CombinedOutput()
			if err != nil || !bytes.Contains(bytes.ToLower(retried), []byte("connected")) || bytes.Contains(bytes.ToLower(retried), []byte("failed")) {
				t.Fatalf("native setup retry was not ready: %v\n%s", err, retried)
			}
			// Native reconnect clears the failed-startup needs-auth cache. Use the
			// client's control protocol rather than modifying its cache on disk.
			streamArgs := append(slices.Clone(cmd.Args[1:len(cmd.Args)-1]), "--input-format", "stream-json")
			stream := exec.CommandContext(ctx, cmd.Path, streamArgs...)
			stream.Dir, stream.Env, stream.WaitDelay = cmd.Dir, cmd.Env, cmd.WaitDelay
			stdin, err := stream.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := stream.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr, output bytes.Buffer
			stream.Stderr = &stderr
			if err := stream.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if stream.ProcessState == nil {
					_ = stream.Process.Kill()
					_ = stream.Wait()
				}
			}()
			encoder := json.NewEncoder(stdin)
			if err := encoder.Encode(map[string]any{"type": "control_request", "request_id": "init", "request": map[string]any{"subtype": "initialize"}}); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 4096), 2<<20)
			reconnected := false
			for scanner.Scan() {
				output.Write(scanner.Bytes())
				var event struct {
					Type     string
					Response struct {
						Subtype   string `json:"subtype"`
						RequestID string `json:"request_id"`
					} `json:"response"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "control_response" {
					if event.Response.Subtype != "success" {
						t.Fatalf("native reconnect control failed: %s", scanner.Text())
					}
					if event.Response.RequestID == "init" {
						err = encoder.Encode(map[string]any{"type": "control_request", "request_id": "retry", "request": map[string]any{"subtype": "mcp_reconnect", "serverName": "plugin:portable-mcp:probe"}})
					} else if event.Response.RequestID == "retry" {
						reconnected = true
						err = encoder.Encode(map[string]any{"type": "user", "session_id": "", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": "Inspect the owned fixture."}})
					}
					if err != nil {
						t.Fatal(err)
					}
				} else if event.Type == "result" {
					_ = stdin.Close()
				}
			}
			if err := stream.Wait(); err != nil || scanner.Err() != nil || !reconnected || !bytes.Contains(output.Bytes(), []byte("PORTABLE_MCP_RESULT")) {
				t.Fatalf("native reconnect and actual tool retry: %v %v\n%s\n%s", err, scanner.Err(), stderr.Bytes(), output.Bytes())
			}
			invoke := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
			invoke.Dir, invoke.Env, invoke.WaitDelay = cmd.Dir, cmd.Env, cmd.WaitDelay
			out, err := invoke.CombinedOutput()
			if err != nil || !bytes.Contains(out, []byte("PORTABLE_MCP_RESPONSE")) || !bytes.Contains(out, []byte("PORTABLE_MCP_RESULT")) {
				t.Fatalf("native portable server invocation: %v\n%s", err, out)
			}
			observedBytes, err := os.ReadFile(filepath.Join(runtimeData, "observed.json"))
			var observed struct {
				Cwd, Root, Data, AutoRoot, AutoData, Marker, Asset string
				Args                                               []string
				Count                                              int
			}
			if err != nil || json.Unmarshal(observedBytes, &observed) != nil || observed.Count != 2 || !strings.HasSuffix(observed.Root, "/payload") || observed.Marker != "portable marker" || observed.Asset != "shared asset" || !slices.Equal(observed.Args, []string{`literal ' $(touch SHOULD_NOT_EXIST);`, `literal $CODEX_PLUGIN_ROOT $PLUGIN_DATA`}) {
				t.Fatalf("portable cwd/root/data/environment/argv differs: %s %v", observedBytes, err)
			}
			if format == AgentPlugins && (observed.AutoRoot != observed.Root || observed.AutoData != observed.Data) {
				t.Fatalf("source-native automatic process environment is missing: %s", observedBytes)
			}
			cwdResolved, cwdErr := filepath.EvalSymlinks(observed.Cwd)
			rootResolved, rootErr := filepath.EvalSymlinks(observed.Root)
			if cwdErr != nil || rootErr != nil || cwdResolved != rootResolved {
				t.Fatalf("portable process cwd differs from the payload root: %s", observedBytes)
			}
			if _, err := os.Stat(filepath.Join(observed.Root, "SHOULD_NOT_EXIST")); !os.IsNotExist(err) {
				t.Fatal("source argv was interpreted as shell syntax")
			}
			mu.Lock()
			joined := bytes.Join(requests, nil)
			mu.Unlock()
			if !bytes.Contains(joined, []byte("PORTABLE_MCP_RESULT")) || bytes.Contains(joined, []byte("plugin_portable-mcp_excluded")) {
				t.Fatal("MCP result or selected server inventory differs")
			}
		})
	}
}
