package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestClaudeLinkedJSONSelection(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) { testClaudeLinkedJSONSelection(t, kind) })
	}
}

func testClaudeLinkedJSONSelection(t *testing.T, kind string) {
	source, pack, market, home, cwd := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write(t, source, claudeManifest, `{"name":"link-probe","version":"1.0.0"}`, 0o644)
	write(t, source, "hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mkdir -p \"${CLAUDE_PLUGIN_DATA}\"; echo LINK_STOP >> \"${CLAUDE_PLUGIN_DATA}/link.log\""}]}]}}`, 0o444)
	write(t, source, ".mcp.json", `{"mcpServers":{"probe":{"command":"python3","args":["${CLAUDE_PLUGIN_ROOT}/scripts/server.py"]}}}`, 0o640)
	write(t, source, "scripts/server.py", `import json, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    if request.get("method") == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "links", "version": "1"}}
    else:
        result = {"tools": []}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`, 0o755)
	for _, path := range []string{claudeManifest, "hooks/hooks.json", ".mcp.json"} {
		target := filepath.Join("original", filepath.Base(path))
		if err := os.MkdirAll(filepath.Join(source, "original"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(source, path), filepath.Join(source, target)); err != nil {
			t.Fatal(err)
		}
		link, err := filepath.Rel(filepath.Dir(path), target)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(link, filepath.Join(source, path)); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "directory" {
		if err := os.MkdirAll(filepath.Join(source, "original/config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(source, "original/.mcp.json"), filepath.Join(source, "original/config/.mcp.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("original/config", filepath.Join(source, "config")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(source, ".mcp.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("config/.mcp.json", filepath.Join(source, ".mcp.json")); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{".claude-plugin", "hooks"} {
			name := "hooks.json"
			if dir == ".claude-plugin" {
				name = "plugin.json"
			}
			if err := os.Remove(filepath.Join(source, dir, name)); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(source, dir)); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join("original", dir)
			if err := os.MkdirAll(filepath.Join(source, target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(source, "original", name), filepath.Join(source, target, name)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(source, dir)); err != nil {
				t.Fatal(err)
			}
		}
	}
	native := os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") == "1"
	if native {
		claudeNative(t, home, cwd, "plugin", "validate", source, "--json")
	}
	entry := map[string]any{"name": "link-probe", "source": "./probe"}
	var s domain.NativePluginSelection
	var allHooks, allMCP []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		claudeScanResponse(t, w)
	}))
	defer api.Close()
	for phase := 0; phase < 5; phase++ {
		original, err := ReadFiles(source)
		files := original
		if phase == 1 {
			m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "link-market", Entry: entry})
			if err != nil {
				t.Fatal(err)
			}
			s, allHooks, allMCP = selection(m, pack), m.Hooks, m.MCP
		}
		if phase > 0 {
			s.Selected[domain.CategoryHooks] = allHooks
			s.Selected[domain.CategoryMCP] = allMCP
			if phase == 2 || phase == 3 {
				s.Selected[domain.CategoryHooks] = nil
			}
			if phase == 3 {
				s.Selected[domain.CategoryMCP] = nil
			}
			files, entry, err = RenderClaudePackage(s)
			if err == nil && (phase == 1 || phase == 4) && !reflect.DeepEqual(original, files) {
				t.Fatal("all-selected linked JSON lost source bytes or symlinks")
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		payload := filepath.Join(market, "probe")
		if err := os.RemoveAll(payload); err != nil {
			t.Fatal(err)
		}
		if err := WriteFiles(payload, files); err != nil {
			t.Fatal(err)
		}
		if phase > 0 || !native {
			loaded, err := ReadClaude(payload, domain.PluginSource{Marketplace: "link-market", Entry: entry})
			if err != nil || (len(loaded.Hooks) > 0) != (phase != 2 && phase != 3) || (len(loaded.MCP) > 0) != (phase != 3) {
				t.Fatalf("linked JSON selection failed phase=%d: %+v %v", phase, loaded, err)
			}
		}
		if !native {
			continue
		}
		catalog, err := json.Marshal(map[string]any{"name": "link-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{entry}})
		if err != nil {
			t.Fatal(err)
		}
		write(t, market, ".claude-plugin/marketplace.json", string(catalog), 0o644)
		claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
		if phase > 0 {
			claudeNative(t, home, cwd, "plugin", "uninstall", "link-probe@link-market", "--scope", "user", "--keep-data", "--json")
		}
		claudeNative(t, home, cwd, "plugin", "install", "link-probe@link-market", "--scope", "user", "--json")
		listed := claudeNative(t, home, cwd, "plugin", "list", "--json")
		var plugins []struct {
			MCP map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(listed, &plugins); err != nil || len(plugins) != 1 || (len(plugins[0].MCP) > 0) != (phase != 3) {
			t.Fatalf("native linked MCP inventory differs: %s %v", listed, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "json", "--tools", "", "--no-session-persistence", "AIPACK_LINK_PROMPT")
		cmd.Dir, cmd.WaitDelay = cwd, time.Second
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !bytes.Contains(out, []byte("AIPACK_SCAN_RESPONSE")) {
			t.Fatalf("native linked runtime: %v\n%s", err, out)
		}
		log := filepath.Join(home, ".claude/plugins/data/link-probe-link-market/link.log")
		body, err := os.ReadFile(log)
		if (phase == 2 || phase == 3) && !os.IsNotExist(err) || (phase != 2 && phase != 3) && (err != nil || !bytes.Contains(body, []byte("LINK_STOP"))) {
			t.Fatalf("native linked hook selection failed phase=%d: %s %v", phase, body, err)
		}
		if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}
