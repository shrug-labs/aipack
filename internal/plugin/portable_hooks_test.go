package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCodexHooksToClaude(t *testing.T) {
	source, pack := t.TempDir(), t.TempDir()
	write(t, source, ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0","hooks":[{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"printf '%s\\n' \"$PLUGIN_ROOT|$CODEX_PLUGIN_ROOT|$CLAUDE_PLUGIN_ROOT|$PLUGIN_DATA|$CODEX_PLUGIN_DATA\""}]}]}},{"hooks":{"Stop":[{"matcher":"never","hooks":[{"type":"command","command":"true","timeout":5}]}]}}]}`, 0o644)
	write(t, source, "hooks/hooks.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"false"}]}]}}`, 0o644)
	m, err := MaterializeCodex(source, pack, "alias", "owned")
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, pack)
	before, err := ReadFiles(filepath.Join(pack, "upstream"))
	if err != nil {
		t.Fatal(err)
	}
	if c := Compatibility(s, domain.HarnessClaudeCode); len(c.Unsupported) != 0 || len(c.Supported) != 2 {
		t.Fatalf("selected hooks require exclusions: %+v", c)
	}
	files, pkg, err := RenderCodexForClaude(s)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := WriteFiles(root, files); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadClaude(root, domain.PluginSource{Marketplace: pkg.Marketplace, Entry: pkg.MarketplaceEntry})
	if err != nil || len(loaded.Hooks) != 2 {
		t.Fatalf("Claude inventory: %+v %v", loaded, err)
	}
	var manifest struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(root, claudeManifest))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("cannot read rendered manifest")
	}
	group := manifest.Hooks["SessionStart"][0]
	if group["matcher"] != "startup|resume" || manifest.Hooks["Stop"][0]["matcher"] != nil || manifest.Hooks["UserPromptSubmit"] != nil {
		t.Fatal("source dispatch/matcher semantics changed")
	}
	handler := group["hooks"].([]any)[0].(map[string]any)
	if handler["timeout"] != float64(600) {
		t.Fatal("lost source timeout default")
	}
	data := t.TempDir()
	cmd := exec.Command("sh", "-c", handler["command"].(string))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "CLAUDE_PLUGIN_ROOT=" + root, "CLAUDE_PLUGIN_DATA=" + data}
	out, err := cmd.CombinedOutput()
	want := strings.Join([]string{root + "/payload", root + "/payload", root + "/payload", data, data}, "|") + "\n"
	if err != nil || string(out) != want {
		t.Fatalf("root/data aliases: %v %q want %q", err, out, want)
	}
	s.Selected[domain.CategoryHooks] = []string{"codex-stop"}
	filtered, _, err := RenderCodexForClaude(s)
	if err != nil || reflect.DeepEqual(files, filtered) {
		t.Fatal("hook exclusion did not change activation")
	}
	s.Selected[domain.CategoryHooks] = nil
	if !GenericContentSelection(s, domain.HarnessClaudeCode) {
		t.Fatal("hooks-off cannot return to ordinary content")
	}
	after, err := ReadFiles(filepath.Join(pack, "upstream"))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rendering/toggling changed source")
	}

	// The source explicitly disables fallback hooks; do not revive dormant assets.
	write(t, source, ".codex-plugin/plugin.json", `{"name":"probe","hooks":{}}`, 0o644)
	m, err = MaterializeCodex(source, t.TempDir(), "alias", "owned")
	if err != nil || len(m.Hooks) != 0 {
		t.Fatalf("source-disabled hooks became active: %+v %v", m, err)
	}
}

func TestPortableHookValidation(t *testing.T) {
	for _, group := range []string{
		`{"matcher":42,"hooks":[{"type":"command","command":"true"}]}`,
		`{"hooks":[{"type":"command"}]}`,
		`{"hooks":[{"type":"command","command":"true","timeout":0}]}`,
		`{"hooks":[{"type":"command","command":"true","timeout":1.5}]}`,
	} {
		source, pack := t.TempDir(), t.TempDir()
		write(t, source, ".codex-plugin/plugin.json", `{"name":"probe"}`, 0o644)
		write(t, source, "hooks/hooks.json", fmt.Sprintf(`{"hooks":{"PreToolUse":[%s]}}`, group), 0o644)
		m, err := MaterializeCodex(source, pack, "alias", "owned")
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessCline} {
			if c := Compatibility(selection(m, pack), target); len(c.Unsupported) == 0 {
				t.Fatalf("%s silently admitted malformed command hook %s: %+v", target, group, c)
			}
		}
	}
}
