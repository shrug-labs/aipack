package app

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

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestClaudeNativeMCPPolicy(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for native MCP permission checks")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	const serverID = "Probe.with-dots"
	var mu sync.Mutex
	var requests [][]byte
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
		requests = append(requests, body)
		mu.Unlock()
		var request struct{ Tools []struct{ Name string } }
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			return
		}
		tool := ""
		for _, candidate := range request.Tools {
			if strings.HasPrefix(candidate.Name, "mcp__plugin_") && strings.HasSuffix(candidate.Name, "_Probe_with-dots__observe") {
				tool = candidate.Name
				break
			}
		}
		call := tool != "" && !bytes.Contains(body, []byte(`"tool_result"`))
		block, delta, stop := `{"type":"text","text":""}`, `{"type":"text_delta","text":"AIPACK_POLICY_RESPONSE"}`, "end_turn"
		if call {
			block, delta, stop = fmt.Sprintf(`{"type":"tool_use","id":"policy_call","name":%q,"input":{}}`, tool), `{"type":"input_json_delta","partial_json":"{}"}`, "tool_use"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"policy_fixture","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
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
	}))
	defer api.Close()
	for _, fixture := range []struct {
		scope   domain.Scope
		name    string
		catalog bool
	}{{domain.ScopeGlobal, "policy-probe", false}, {domain.ScopeProject, "policy-probe", false}, {domain.ScopeGlobal, "policy-binding", false}, {domain.ScopeProject, "policy-binding", false}, {domain.ScopeGlobal, "policy-probe", true}, {domain.ScopeProject, "policy-probe", true}} {
		scope, bindingName := fixture.scope, fixture.name
		label := bindingName
		if fixture.catalog {
			label += "-catalog"
		}
		t.Run(string(scope)+"/"+label, func(t *testing.T) {
			permissionServer := "plugin_policy-probe_Probe_with-dots"
			tool := "mcp__" + permissionServer + "__observe"
			root := t.TempDir()
			source, cfgDir, home, nativeHome := filepath.Join(root, "market/plugins/probe"), filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "native")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			if !fixture.catalog {
				writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"policy-probe","version":"1.0.0"}`)
			}
			writeFile(t, filepath.Join(source, ".mcp.json"), fmt.Sprintf(`{"mcpServers":{%q:{"command":"python3","args":["${CLAUDE_PLUGIN_ROOT}/probe.py"]}}}`, serverID))
			writeFile(t, filepath.Join(source, "probe.py"), `import json, os, pathlib, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request: continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "policy-fixture", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": name, "description": "Inspect synthetic fixture.", "inputSchema": {"type": "object", "properties": {}}, "annotations": {"readOnlyHint": True}} for name in ["observe", "hidden"]]}
    elif method == "tools/call":
        data = pathlib.Path(os.environ["CLAUDE_PLUGIN_DATA"])
        data.mkdir(parents=True, exist_ok=True)
        with (data / "calls").open("a") as log: log.write(request["params"]["name"] + "\n")
        result = {"content": [{"type": "text", "text": "AIPACK_POLICY_TOOL_RESULT"}]}
    else: result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`)
			catalog := filepath.Join(root, "market/.claude-plugin/marketplace.json")
			writeFile(t, catalog, fmt.Sprintf(`{"name":"policy-market","owner":{"name":"shrug-labs"},"plugins":[{"name":%q,"source":"./plugins/probe"}]}`, bindingName))
			if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: cfgDir, URL: catalog}, nil); err != nil {
				t.Fatal(err)
			}
			entry, err := RegistryLookup(RegistryListRequest{ConfigDir: cfgDir}, bindingName)
			if err != nil {
				t.Fatal(err)
			}
			if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(cfgDir, "probe", entry), nil); err != nil {
				t.Fatal(err)
			}
			eng := engine.New(nil, nil)
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: scope, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
			resolveProfile := func() domain.Profile {
				t.Helper()
				p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				p.MCPServers = append(p.MCPServers, domain.MCPServer{Name: "ordinary", Command: []string{"echo"}, AllowedTools: []string{"keep"}, SourcePack: "ordinary"})
				p.SettingsPacks = []string{"ordinary"}
				return p
			}
			syncProfile := func() SyncResult {
				t.Helper()
				p := resolveProfile()
				result, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			var installed []byte
			type policyCase struct {
				name            string
				policy          config.MCPServerConfig
				visible, called bool
			}
			checkPolicy := func(tc policyCase) {
				t.Run(tc.name, func(t *testing.T) {
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: tc.policy}
					result := syncProfile()
					current := mustRead(t, filepath.Join(nativeHome, "plugins/installed_plugins.json"))
					if installed != nil && !bytes.Equal(current, installed) {
						t.Fatal("policy-only sync reinstalled native payload")
					}
					installed = current
					calls := filepath.Join(nativeHome, "plugins/data/"+bindingName+"-policy-market/calls")
					if err := os.Remove(calls); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					mu.Lock()
					requests = nil
					mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
					cmd := exec.CommandContext(ctx, binary, "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--permission-mode", "dontAsk", "--tools", "", "--no-session-persistence", "Inspect the synthetic fixture.")
					cmd.Dir, cmd.WaitDelay = root, time.Second
					cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + nativeHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
					out, err := cmd.CombinedOutput()
					cancel()
					if err != nil || !bytes.Contains(out, []byte("AIPACK_POLICY_RESPONSE")) {
						t.Fatalf("native policy run: %v\n%s", err, out)
					}
					mu.Lock()
					bodies := slices.Clone(requests)
					mu.Unlock()
					if len(bodies) == 0 {
						t.Fatal("no native model request")
					}
					var first struct{ Tools []struct{ Name string } }
					if err := json.Unmarshal(bodies[0], &first); err != nil {
						t.Fatal(err)
					}
					visible := slices.ContainsFunc(first.Tools, func(t struct{ Name string }) bool { return t.Name == tool })
					_, callErr := os.Stat(calls)
					if visible != tc.visible || (callErr == nil) != tc.called {
						t.Fatalf("visibility/call=%t/%t want %t/%t; tools=%+v output=%s", visible, callErr == nil, tc.visible, tc.called, first.Tools, out)
					}
					if len(tc.policy.DisabledTools) > 0 && slices.ContainsFunc(first.Tools, func(t struct{ Name string }) bool { return t.Name == "mcp__"+permissionServer+"__hidden" }) {
						t.Fatal("denied tool reached native model context")
					}
					if len(result.Plan.NativePlugins) != 1 {
						t.Fatal("missing native action")
					}
				})
			}
			for _, tc := range []policyCase{
				{"unapproved", config.MCPServerConfig{}, true, false},
				{"allowed", config.MCPServerConfig{AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}, true, true},
				{"always-allowed", config.MCPServerConfig{AlwaysAllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}, true, true},
				{"denied", config.MCPServerConfig{AllowedTools: []string{"observe"}, DisabledTools: []string{"observe", "hidden"}}, false, false},
				{"reset", config.MCPServerConfig{}, true, false},
			} {
				checkPolicy(tc)
			}
			cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}}
			result := syncProfile()
			ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
			if err != nil {
				t.Fatal(err)
			}
			record := ledger.NativePlugins[bindingName+"@policy-market"]
			if record.Namespace != "policy-probe" || len(record.MCPPermissionServers) != 1 || record.MCPPermissionServers[0] != permissionServer {
				t.Fatalf("native permission ownership missing: %+v", record)
			}
			for _, after := range []bool{false, true} {
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}}
				result = syncProfile()
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {AllowedTools: []string{"observe"}, DisabledTools: []string{"observe", "hidden"}}}
				p := resolveProfile()
				eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: after}
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
					t.Fatal("permission interruption did not run")
				}
				// A user edit made after interruption survives restoration of our rules.
				current, err := readNativeConfig(record.SettingsPath)
				if err != nil {
					t.Fatal(err)
				}
				perms := current["permissions"].(map[string]any)
				perms["allow"] = append(perms["allow"].([]any), "Read(/synthetic-fixture)")
				body, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, record.SettingsPath, string(body))
				if err := recoverNativeOperation(cfgDir); err != nil {
					t.Fatal(err)
				}
				current, err = readNativeConfig(record.SettingsPath)
				if err != nil {
					t.Fatal(err)
				}
				perms = current["permissions"].(map[string]any)
				denied := slices.Contains(perms["deny"].([]any), any(tool))
				if denied != after || !slices.Contains(perms["allow"].([]any), any("Read(/synthetic-fixture)")) {
					t.Fatalf("permission recovery chose wrong policy or lost user edits: %+v", perms)
				}
			}
			cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}}
			syncProfile()
			before, err := readNativeConfig(record.SettingsPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {DisabledTools: []string{"observe"}}}
			p := resolveProfile()
			eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
			if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil {
				t.Fatal("failed permission persistence returned success")
			}
			eng.FS = engine.OSFS{}
			restored, err := readNativeConfig(record.SettingsPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, root := range []map[string]any{before, restored} {
				for _, key := range []string{"allow", "deny"} {
					items, _ := root["permissions"].(map[string]any)[key].([]any)
					slices.SortFunc(items, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
				}
			}
			beforePermissions, _ := json.Marshal(before["permissions"])
			restoredPermissions, _ := json.Marshal(restored["permissions"])
			if !bytes.Equal(beforePermissions, restoredPermissions) {
				t.Fatalf("failed policy sync did not restore permissions: %s vs %s", beforePermissions, restoredPermissions)
			}
			if bindingName == "policy-binding" {
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{serverID: {AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}}
				result = syncProfile()
				view := filepath.Join(cfgDir, "rendered-plugins/claudecode/policy-market")
				viewBefore, err := packTreeDigest(view)
				if err != nil {
					t.Fatal(err)
				}
				cacheBefore, err := packTreeDigest(record.CachePath)
				if err != nil {
					t.Fatal(err)
				}
				ledgerBefore := mustRead(t, result.Plan.Ledger)
				data := filepath.Join(nativeHome, "plugins/data/"+bindingName+"-policy-market/marker")
				writeFile(t, data, "persistent native data")
				writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"policy-renamed","version":"1.0.0"}`)
				updates, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "probe"}, nil, nil)
				if err != nil || len(updates) != 1 || updates[0].Status != StatusUpdated {
					t.Fatalf("manifest namespace update: %+v %v", updates, err)
				}
				for _, phase := range []string{"failure", "before-ledger", "after-ledger"} {
					p := resolveProfile()
					if phase == "failure" {
						eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
						if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil {
							t.Fatal("failed namespace-update persistence returned success")
						}
					} else {
						eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: phase == "after-ledger"}
						func() {
							defer func() {
								if value := recover(); value != "native ledger interruption" {
									t.Fatalf("namespace-update interruption: %v", value)
								}
							}()
							_, _, _ = RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
						}()
						if err := recoverNativeOperation(cfgDir); err != nil {
							t.Fatal(err)
						}
					}
					eng.FS = engine.OSFS{}
					if phase != "after-ledger" {
						viewAfter, viewErr := packTreeDigest(view)
						cacheAfter, cacheErr := packTreeDigest(record.CachePath)
						if viewErr != nil || cacheErr != nil || viewAfter != viewBefore || cacheAfter != cacheBefore || !bytes.Equal(ledgerBefore, mustRead(t, result.Plan.Ledger)) {
							t.Fatalf("namespace-update rollback changed prior view/cache/ledger: %v %v", viewErr, cacheErr)
						}
					}
					if string(mustRead(t, data)) != "persistent native data" || util.PathExists(nativeOperationDir(cfgDir)) {
						t.Fatal("namespace-update recovery changed data or left pending delivery")
					}
				}
				settings := mustRead(t, record.SettingsPath)
				if bytes.Contains(settings, []byte(permissionServer)) || !bytes.Contains(settings, []byte("mcp__ordinary__keep")) {
					t.Fatalf("namespace update retained prior permission names or changed sibling permissions: %s", settings)
				}
				permissionServer = "plugin_policy-renamed_Probe_with-dots"
				tool = "mcp__" + permissionServer + "__observe"
				installed = nil
				for _, tc := range []policyCase{
					{"updated-allowed", config.MCPServerConfig{AllowedTools: []string{"observe"}, DisabledTools: []string{"hidden"}}, true, true},
					{"updated-denied", config.MCPServerConfig{DisabledTools: []string{"observe", "hidden"}}, false, false},
					{"updated-reset", config.MCPServerConfig{}, true, false},
				} {
					checkPolicy(tc)
				}
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				current := ledger.NativePlugins[bindingName+"@policy-market"]
				if err != nil || len(ledger.NativePlugins) != 1 || current.Namespace != "policy-renamed" || current.CachePath != record.CachePath || current.Generation == record.Generation || string(mustRead(t, data)) != "persistent native data" {
					t.Fatalf("namespace update changed installation identity/data or retained old ownership: %+v %v", current, err)
				}
			}
			if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "probe", Registry: testRegistry()}, nil); err != nil {
				t.Fatal(err)
			}
			settings := mustRead(t, record.SettingsPath)
			if bytes.Contains(settings, []byte(permissionServer)) || !bytes.Contains(settings, []byte("mcp__ordinary__keep")) {
				t.Fatalf("targeted delete changed sibling permissions: %s", settings)
			}
		})
	}
}
