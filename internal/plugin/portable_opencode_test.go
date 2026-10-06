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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

// Establish the native selected-directory contract before adding an adapter.
// The payload deliberately retains an excluded sibling's entrypoint and asset.
func TestCodexToOpenCode(t *testing.T) {
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) { testCodexToOpenCode(t, format) })
	}
}

func testCodexToOpenCode(t *testing.T, format string) {
	if os.Getenv("AIPACK_TEST_OPENCODE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_OPENCODE_NATIVE=1 for the native OpenCode control")
	}
	home, cwd := t.TempDir(), t.TempDir()
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--quiet", cwd).CombinedOutput(); err != nil {
		t.Fatalf("initialize owned workspace: %v %s", err, out)
	}
	root := filepath.Join(cwd, "retained payload")
	data := filepath.Join(home, "runtime-data")
	source, pack := t.TempDir(), t.TempDir()
	selectedName, nestedName := "portable-probe@portable-market:selected", "portable-probe@portable-market:nested"
	toolName := "portable-probe_portable-market_probe_observe"
	write(t, source, "skills/selected/SKILL.md", "---\nname: selected\ndescription: Owned selection fixture\n---\nOPENCODE_SELECTED_BODY\nRead [shared](../excluded/references/shared.txt). Root ${CODEX_PLUGIN_ROOT}; data ${CODEX_PLUGIN_DATA}; keep $CODEX_PLUGIN_ROOT_SUFFIX.\n", 0o644)
	write(t, source, "skills/selected/references/nested/SKILL.md", "---\nname: nested\ndescription: Recursive discovery fixture\n---\nOPENCODE_NESTED_BODY\n", 0o644)
	write(t, source, "skills/excluded/SKILL.md", "---\nname: excluded\ndescription: Excluded fixture\n---\nOPENCODE_EXCLUDED_BODY\n", 0o644)
	write(t, source, "skills/excluded/references/shared.txt", "OPENCODE_SHARED_ASSET", 0o440)
	write(t, source, "agents/hidden.md", "---\nname: hidden\ndescription: Inactive native asset\n---\nOPENCODE_HIDDEN_AGENT\n", 0o644)
	jsMarker := filepath.Join(root, "JS_ACTIVATED")
	write(t, source, "plugins/hidden.js", fmt.Sprintf("import { writeFileSync } from 'node:fs';\nwriteFileSync(%q, 'OPENCODE_HIDDEN_PLUGIN');\nexport default async () => ({});\n", jsMarker), 0o644)
	write(t, data, "fail-setup", "owned cold setup failure", 0o600)
	write(t, source, "scripts/server.py", `#!/usr/bin/env python3
import json, os, pathlib, sys
data = pathlib.Path(os.environ["OWNED_DATA"])
if (data / "fail-setup").exists():
    print("Owned setup input requires correction.", file=sys.stderr)
    sys.exit(2)
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "owned", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": name, "description": "Inspect the owned fixture.", "inputSchema": {"type": "object", "properties": {}}} for name in ["observe", "hidden"]]}
    elif method == "tools/call":
        record = data / "observed.json"
        count = json.loads(record.read_text())["count"] + 1 if record.exists() else 1
        observed = {"count": count, "cwd": os.getcwd(), "root": os.path.abspath(os.environ["OWNED_ROOT"]), "data": str(data), "literal": os.environ["OWNED_LITERAL"], "args": sys.argv[1:], "asset": pathlib.Path("skills/excluded/references/shared.txt").read_text(), "autoRoot": os.environ.get("PLUGIN_ROOT", ""), "autoData": os.environ.get("PLUGIN_DATA", "")}
        record.write_text(json.dumps(observed))
        result = {"content": [{"type": "text", "text": "OPENCODE_MCP_RESULT " + json.dumps(observed)}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
	manifest, mcpPath := ".codex-plugin/plugin.json", ".mcp.json"
	manifestBody := `{"name":"portable-probe","version":"1.0.0"}`
	entry := map[string]any{"command": "python3", "args": []string{"scripts/server.py", `literal ' $(touch SHOULD_NOT_EXIST);`, "${TOKEN}"}, "cwd": ".", "env": map[string]string{"OWNED_ROOT": ".", "OWNED_DATA": data, "OWNED_LITERAL": "${TOKEN}"}}
	if format == AgentPlugins {
		manifest, mcpPath = "plugin.json", "mcp.json"
		manifestBody = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable-probe","version":"1.0.0"}`
		entry["type"], entry["command"] = "stdio", "./scripts/server.py"
		entry["args"] = []string{`literal ' $(touch SHOULD_NOT_EXIST);`, "${TOKEN}"}
		delete(entry, "cwd")
		entry["env"].(map[string]string)["OWNED_ROOT"] = "${PLUGIN_ROOT}"
		entry["env"].(map[string]string)["OWNED_DATA"] = "${PLUGIN_DATA}"
	}
	write(t, source, manifest, manifestBody, 0o644)
	servers := map[string]any{"mcpServers": map[string]any{"probe": entry}}
	if format == AgentPlugins {
		servers["$schema"] = agentMCPSchema
	}
	mcpBody, _ := json.Marshal(servers)
	write(t, source, mcpPath, string(mcpBody), 0o644)
	m, err := MaterializeCodex(source, pack, "alias", "portable-market")
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, pack)
	s.Selected = map[domain.PackCategory][]string{domain.CategorySkills: {"selected"}, domain.CategoryMCP: {"probe"}}
	s.MCPPolicy = map[string]domain.NativeMCPPolicy{"probe": {AlwaysAllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}}
	files, config, err := RenderCodexForOpenCode(s, root, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(root, files); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		var input struct {
			Messages []struct {
				Role    string
				Content any
			}
		}
		_ = json.Unmarshal(body, &input)
		tools := 0
		for _, message := range input.Messages {
			if message.Role == "tool" {
				tools++
			}
		}
		delta := map[string]any{"content": "OPENCODE_RESPONSE"}
		finish := "stop"
		nested := bytes.Contains(body, []byte("REQUEST_NESTED"))
		if tools < 3 && (!nested || tools == 0) {
			name, args := "skill", map[string]string{"name": selectedName}
			if tools == 1 {
				name, args = "read", map[string]string{"filePath": filepath.Join(root, "skills/excluded/references/shared.txt")}
			}
			if tools == 2 {
				name, args = toolName, map[string]string{}
			}
			if nested {
				args = map[string]string{"name": nestedName}
			}
			encoded, _ := json.Marshal(args)
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("owned-%d", tools), "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"id": "owned", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		chunk, _ = json.Marshal(map[string]any{"id": "owned", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer api.Close()
	config["autoupdate"], config["share"], config["enabled_providers"] = false, "disabled", []string{"owned"}
	permissions := config["permission"].(map[string]any)
	permissions["*"] = "deny"
	permissions["read"] = map[string]string{"retained payload/*": "allow"}
	permissions["skill"].(map[string]string)[selectedName] = "allow" // Explicit fixture-only skill approval.
	config["provider"] = map[string]any{"owned": map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]any{"baseURL": api.URL + "/v1", "apiKey": "owned-offline-fixture"}, "models": map[string]any{"fixture": map[string]any{"name": "fixture", "tool_call": true, "limit": map[string]int{"context": 32000, "output": 1024}}}}}
	encoded, _ := json.Marshal(config)
	write(t, home, ".config/opencode/opencode.json", string(encoded), 0o600)
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "opencode", args...)
		cmd.Dir, cmd.WaitDelay = cwd, time.Second
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(home, ".state"), "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_PROJECT_CONFIG=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("native OpenCode %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	inventory := run("debug", "skill")
	if !bytes.Contains(inventory, []byte("OPENCODE_SELECTED_BODY")) || bytes.Contains(inventory, []byte("OPENCODE_EXCLUDED_BODY")) || bytes.Contains(inventory, []byte("OPENCODE_HIDDEN_AGENT")) {
		t.Fatalf("native selected-directory inventory: %s", inventory)
	}
	if health := run("mcp", "list"); !bytes.Contains(health, []byte("failed")) {
		t.Fatalf("native cold failure was hidden: %s", health)
	}
	if _, err := os.Stat(filepath.Join(data, "observed.json")); !os.IsNotExist(err) {
		t.Fatal("cold setup failure published a tool result")
	}
	if err := os.Remove(filepath.Join(data, "fail-setup")); err != nil {
		t.Fatal(err)
	}
	if health := run("mcp", "list"); !bytes.Contains(health, []byte("connected")) {
		t.Fatalf("native corrected-input retry failed: %s", health)
	}
	var out []byte
	for range 2 {
		out = run("run", "--model", "owned/fixture", "--format", "json", "Load the owned selected skill, read its shared asset and invoke the owned probe.")
		if !bytes.Contains(out, []byte("OPENCODE_RESPONSE")) {
			t.Fatalf("native provider execution failed: %s", out)
		}
	}
	mu.Lock()
	joined := bytes.Join(requests, nil)
	mu.Unlock()
	if !bytes.Contains(joined, []byte("OPENCODE_MCP_RESULT")) || !bytes.Contains(joined, []byte("OPENCODE_SELECTED_BODY")) || !bytes.Contains(joined, []byte("OPENCODE_SHARED_ASSET")) || !bytes.Contains(joined, []byte("$CODEX_PLUGIN_ROOT_SUFFIX")) || bytes.Contains(joined, []byte("OPENCODE_EXCLUDED_BODY")) || bytes.Contains(joined, []byte("OPENCODE_NESTED_BODY")) || bytes.Contains(joined, []byte("OPENCODE_HIDDEN_AGENT")) || bytes.Contains(joined, []byte(`"name":"portable-probe_portable-market_probe_hidden"`)) {
		t.Fatalf("native invocation or retained relative asset failed:\n%s", out)
	}
	observed, err := os.ReadFile(filepath.Join(data, "observed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Count                                               int
		Cwd, Root, Data, Literal, Asset, AutoRoot, AutoData string
		Args                                                []string
	}
	if err := json.Unmarshal(observed, &record); err != nil || record.Count != 2 || record.Cwd != root || record.Root != root || record.Data != data || record.Literal != "${TOKEN}" || record.Asset != "OPENCODE_SHARED_ASSET" || len(record.Args) != 2 || record.Args[0] != `literal ' $(touch SHOULD_NOT_EXIST);` || record.Args[1] != "${TOKEN}" {
		t.Fatalf("native cwd/environment/argv/data contract failed: %s %v", observed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "SHOULD_NOT_EXIST")); !os.IsNotExist(err) {
		t.Fatal("native stdio argv was evaluated as shell syntax")
	}
	if _, err := os.Stat(jsMarker); !os.IsNotExist(err) {
		t.Fatal("retained JavaScript asset activated as a native plugin")
	}
	if inventory := run("debug", "skill"); !bytes.Contains(inventory, []byte("OPENCODE_NESTED_BODY")) {
		t.Fatalf("native nested skill discovery contract changed: %s", inventory)
	}
	if format == AgentPlugins && (record.AutoRoot != root || record.AutoData != data) {
		t.Fatalf("native Agent Plugins automatic environment differs: %s", observed)
	}
	if out := run("run", "--model", "owned/fixture", "--format", "json", "REQUEST_NESTED"); !bytes.Contains(out, []byte(`"status":"error"`)) || bytes.Contains(out, []byte("OPENCODE_NESTED_BODY")) {
		t.Fatalf("excluded recursively discovered skill executed: %s", out)
	}
}
