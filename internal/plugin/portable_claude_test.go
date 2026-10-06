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
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCodexSkillsToClaude(t *testing.T) {
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) { testCodexSkillsToClaude(t, format) })
	}
}

func testCodexSkillsToClaude(t *testing.T, format string) {
	source, pack := t.TempDir(), t.TempDir()
	manifestPath, manifest := ".codex-plugin/plugin.json", `{"name":"portable-probe","version":"1.0.0","description":"Portable fixture"}`
	if format == AgentPlugins {
		manifestPath, manifest = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable-probe","version":"1.0.0","description":"Portable fixture"}`
	}
	write(t, source, manifestPath, manifest, 0o644)
	write(t, source, "skills/selected/SKILL.md", "---\nname: selected\ndescription: Selected fixture ${CODEX_PLUGIN_ROOT}\n---\nPORTABLE_SELECTED_BODY\nRead [shared](../excluded/references/shared.md). Run ${CODEX_PLUGIN_ROOT}/scripts/probe.sh. Data: ${CODEX_PLUGIN_DATA}. Keep $CODEX_PLUGIN_ROOT_SUFFIX intact.\n", 0o644)
	write(t, source, "skills/excluded/SKILL.md", "---\nname: excluded\ndescription: Excluded fixture\n---\nPORTABLE_EXCLUDED_BODY\n", 0o644)
	write(t, source, "skills/excluded/references/shared.md", "shared asset", 0o440)
	write(t, source, "scripts/probe.sh", "#!/bin/sh\nprintf portable\n", 0o755)
	write(t, source, "hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`, 0o644)
	write(t, source, "agents/hidden.md", "---\nname: hidden\ndescription: Hidden native asset\n---\nPORTABLE_HIDDEN_AGENT\n", 0o644)
	m, err := MaterializeCodex(source, pack, "alias", "portable-market")
	if err != nil {
		t.Fatal(err)
	}
	before, err := ReadFiles(filepath.Join(pack, "upstream"))
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, pack)
	s.Selected[domain.CategoryHooks] = nil
	s.Selected[domain.CategorySkills] = []string{"selected"}
	files, pkg, err := RenderCodexForClaude(s)
	if err != nil {
		t.Fatal(err)
	}
	rendered := t.TempDir()
	if err := WriteFiles(rendered, files); err != nil {
		t.Fatal(err)
	}
	converted, err := ReadClaude(rendered, domain.PluginSource{Marketplace: pkg.Marketplace, Entry: pkg.MarketplaceEntry})
	if err != nil || !slices.Equal(converted.Skills, []string{"selected"}) || len(converted.Hooks)+len(converted.Agents)+len(converted.MCP) != 0 {
		t.Fatalf("unexpected target activation: %+v %v", converted, err)
	}
	for _, original := range before {
		if original.Path == "skills/selected/SKILL.md" {
			continue
		}
		want := original
		want.Path = "payload/" + want.Path
		if !slices.ContainsFunc(files, func(file File) bool { return reflect.DeepEqual(file, want) }) {
			t.Fatalf("lost source asset or permissions: %s", original.Path)
		}
	}
	after, err := ReadFiles(filepath.Join(pack, "upstream"))
	if err != nil || !reflect.DeepEqual(before, after) || s.Package.Harness != domain.HarnessCodex {
		t.Fatal("portable rendering changed installed source or source identity")
	}
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		return
	}
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
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PORTABLE_RESPONSE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			var value struct{ Type string }
			_ = json.Unmarshal([]byte(event), &value)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value.Type, event)
		}
	}))
	defer api.Close()
	home, market, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, market, ".claude-plugin/marketplace.json", `{"name":"portable-market","owner":{"name":"shrug-labs"},"plugins":[{"name":"portable-probe","source":"./plugins/portable-probe"}]}`, 0o644)
	if err := WriteFiles(filepath.Join(market, "plugins/portable-probe"), files); err != nil {
		t.Fatal(err)
	}
	claudeNative(t, home, cwd, "plugin", "marketplace", "add", market)
	claudeNative(t, home, cwd, "plugin", "install", pkg.Binding(), "--scope", "user", "--json")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--no-session-persistence", "/portable-probe:selected")
	cmd.Dir, cmd.WaitDelay = cwd, time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("PORTABLE_RESPONSE")) {
		t.Fatalf("native portable skill invocation: %v\n%s", err, out)
	}
	mu.Lock()
	joined := bytes.Join(requests, nil)
	mu.Unlock()
	if !bytes.Contains(joined, []byte("PORTABLE_SELECTED_BODY")) || bytes.Contains(joined, []byte("PORTABLE_EXCLUDED_BODY")) || bytes.Contains(joined, []byte("PORTABLE_HIDDEN_AGENT")) || bytes.Contains(joined, []byte("${CLAUDE_PLUGIN_ROOT}")) || bytes.Contains(joined, []byte("${CLAUDE_PLUGIN_DATA}")) || !bytes.Contains(joined, []byte("/payload/scripts/probe.sh")) || !bytes.Contains(joined, []byte("/plugins/data/portable-probe-portable-market")) || !bytes.Contains(joined, []byte("$CODEX_PLUGIN_ROOT_SUFFIX")) {
		t.Fatalf("skill selection or plugin-root expansion failed: %s", joined)
	}
}
