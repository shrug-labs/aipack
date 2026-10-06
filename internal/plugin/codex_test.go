package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestRenderedFilesDistinguishesExactNumbers(t *testing.T) {
	before := []byte(`{"counter":9007199254740993}`)
	after := []byte(`{"counter":9007199254740992}`)
	files, err := renderedFiles([]File{{Path: "manifest.json", Content: before, Mode: 0o755}}, map[string][]byte{"manifest.json": after}, nil, nil)
	if err != nil || len(files) != 1 || !bytes.Equal(files[0].Content, after) || files[0].Mode != 0o755 {
		t.Fatalf("native renderer conflated distinct integers: %+v %v", files, err)
	}
}

func TestResolvePayloadFileRelativePaths(t *testing.T) {
	files := map[string]File{
		".mcp.json":        {Path: ".mcp.json", Mode: 0o644, Content: []byte(`{"mcpServers":{}}`)},
		"nested":           {Path: "nested", Mode: os.ModeDir | 0o755},
		"nested/.mcp.json": {Path: "nested/.mcp.json", Mode: 0o644, Content: []byte(`{"mcpServers":{}}`)},
		"alias":            {Path: "alias", Mode: os.ModeSymlink | 0o777, Link: "nested"},
		"escaping":         {Path: "escaping", Mode: os.ModeSymlink | 0o777, Link: "../outside"},
	}
	for path, expected := range map[string]string{".mcp.json": ".mcp.json", "./.mcp.json": ".mcp.json", "./alias/.mcp.json": "nested/.mcp.json"} {
		file, err := ResolvePayloadFile(files, path)
		if err != nil || file.Path != expected {
			t.Fatalf("resolve %q: got %q, %v; want %q", path, file.Path, err, expected)
		}
	}
	for _, path := range []string{"../.mcp.json", "/.mcp.json", "./escaping/.mcp.json"} {
		if _, err := ResolvePayloadFile(files, path); err == nil {
			t.Fatalf("accepted outside payload path %q", path)
		}
	}
}

func TestRenderedFilesLinkedReplacements(t *testing.T) {
	for _, path := range []string{"manifest.json", "agent.md"} {
		t.Run(path, func(t *testing.T) {
			before, same, after := []byte(`{"keep":true}`), []byte("{\n  \"keep\": true\n}"), []byte(`{"keep":false}`)
			if filepath.Ext(path) == ".md" {
				before, same, after = []byte("original\n"), []byte("original\n"), []byte("selected\n")
			}
			files := []File{{Path: path, Link: "links/alias", Mode: os.ModeSymlink | 0o777}, {Path: "links/alias", Link: "../original", Mode: os.ModeSymlink | 0o777}, {Path: "original", Content: before, Mode: 0o440}}
			unchanged, err := renderedFiles(files, map[string][]byte{path: same}, nil, nil)
			if err != nil || !reflect.DeepEqual(unchanged, files) {
				t.Fatalf("unchanged replacement lost links or bytes: %+v %v", unchanged, err)
			}
			changed, err := renderedFiles(files, map[string][]byte{path: after}, nil, nil)
			if err != nil || changed[0].Link != "" || changed[0].Mode != 0o440 || !bytes.Equal(changed[0].Content, after) || !reflect.DeepEqual(changed[1:], files[1:]) {
				t.Fatalf("changed alias altered original or mode: %+v %v", changed, err)
			}
		})
	}
	for _, target := range []string{"missing", "manifest.json", "../escape", "/absolute", "directory"} {
		t.Run(target, func(t *testing.T) {
			files := []File{{Path: "manifest.json", Link: target, Mode: os.ModeSymlink}, {Path: "directory", Mode: os.ModeDir | 0o755}}
			if _, err := renderedFiles(files, map[string][]byte{"manifest.json": []byte(`{}`)}, nil, nil); err == nil {
				t.Fatal("invalid replacement target accepted")
			}
		})
	}
}

func TestRenderedFilesLinkedDirectories(t *testing.T) {
	files := []File{
		{Path: "alias", Link: "original/deep", Mode: os.ModeSymlink | 0o777},
		{Path: "original", Mode: os.ModeDir | 0o755},
		{Path: "original/deep", Mode: os.ModeDir | 0o550},
		{Path: "original/deep/hooks.json", Content: []byte(`{"enabled":true}`), Mode: 0o440},
		{Path: "original/deep/agent.md", Content: []byte("agent body"), Mode: 0o640},
		{Path: "original/deep/inside", Link: "hooks.json", Mode: os.ModeSymlink | 0o777},
		{Path: "original/deep/root", Link: ".", Mode: os.ModeSymlink | 0o777},
		{Path: "original/deep/outside", Link: "../../other.json", Mode: os.ModeSymlink | 0o777},
		{Path: "other.json", Content: []byte(`{}`), Mode: 0o644},
	}
	unchanged, err := renderedFiles(files, map[string][]byte{"alias/hooks.json": []byte(`{ "enabled": true }`)}, nil, nil)
	if err != nil || !reflect.DeepEqual(unchanged, files) {
		t.Fatalf("unchanged directory alias was expanded: %+v %v", unchanged, err)
	}
	changed, err := renderedFiles(files, map[string][]byte{"alias/hooks.json": []byte(`{"enabled":false}`)}, map[string]bool{"alias/agent.md": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, file := range changed {
		byPath[file.Path] = file
	}
	if byPath["alias"].Mode != os.ModeDir|0o550 || byPath["alias"].Link != "" || byPath["alias/hooks.json"].Mode != 0o440 || string(byPath["alias/hooks.json"].Content) != `{"enabled":false}` || byPath["alias/inside"].Link != "hooks.json" || byPath["alias/outside"].Link != "../other.json" {
		t.Fatal("materialized directory lost content, modes, or link destinations")
	}
	if _, exists := byPath["alias/agent.md"]; exists {
		t.Fatal("excluded alias child survived")
	}
	if byPath["alias/root"].Link != "." {
		t.Fatal("link to the directory root escaped the selected alias tree")
	}
	for _, file := range files[1:] {
		if !reflect.DeepEqual(byPath[file.Path], file) {
			t.Fatalf("original target changed: %s", file.Path)
		}
	}
	root := t.TempDir()
	t.Cleanup(func() {
		if err := util.RemoveOwnedTree(root); err != nil {
			t.Error(err)
		}
	})
	if err := WriteFiles(root, changed); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"alias", "../escape", "missing", "other.json"} {
		invalid := slices.Clone(files)
		invalid[0].Link = target
		if _, err := renderedFiles(invalid, map[string][]byte{"alias/hooks.json": []byte(`{}`)}, nil, nil); err == nil {
			t.Fatalf("invalid linked parent accepted: %s", target)
		}
	}
}

func TestCodexExplicitEmptyHooks(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{`{}`, `{"description":"No hooks"}`, `[{}]`, `{"hooks":{}}`} {
		t.Run(declaration, func(t *testing.T) {
			source, pack := t.TempDir(), t.TempDir()
			write(t, source, ".codex-plugin/plugin.json", `{"name":"empty-probe","hooks":`+declaration+`}`, 0o644)
			write(t, source, "hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo inactive"}]}]}}`, 0o644)
			manifest, err := MaterializeCodex(source, pack, "alias", "empty-market")
			if err != nil || len(manifest.Hooks) != 0 {
				t.Fatalf("explicit empty hooks used fallback: %+v %v", manifest.Hooks, err)
			}
			files, err := RenderCodex(selection(manifest, pack))
			original, readErr := ReadFiles(source)
			if err != nil || readErr != nil || !reflect.DeepEqual(files, original) {
				t.Fatalf("empty hooks changed payload: %v %v", err, readErr)
			}
		})
	}
}

func TestRenderedFilesDroppedAliasTargets(t *testing.T) {
	files := []File{
		{Path: "alias.md", Link: "original/agent.md", Mode: os.ModeSymlink | 0o777},
		{Path: "directory", Link: "original", Mode: os.ModeSymlink | 0o777},
		{Path: "dangling", Link: "not-installed", Mode: os.ModeSymlink | 0o777},
		{Path: "original", Mode: os.ModeDir | 0o755},
		{Path: "original/agent.md", Content: []byte("original body"), Mode: 0o440},
		{Path: "original/recursive", Link: ".", Mode: os.ModeSymlink | 0o777},
	}
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained-%t", retained), func(t *testing.T) {
			needed := map[string]bool{}
			if retained {
				needed["alias.md"], needed["directory/agent.md"] = true, true
			}
			rendered, err := renderedFiles(files, nil, map[string]bool{"original/agent.md": true}, needed)
			if err != nil {
				t.Fatal(err)
			}
			byPath := map[string]File{}
			for _, file := range rendered {
				byPath[file.Path] = file
			}
			if retained {
				for _, path := range []string{"alias.md", "directory/agent.md"} {
					file := byPath[path]
					if file.Link != "" || file.Mode != 0o440 || string(file.Content) != "original body" {
						t.Fatalf("alias lost original target content or mode: %+v", file)
					}
				}
				if byPath["directory/recursive"].Link != "." || len(rendered) > len(files)+2 {
					t.Fatal("unselected recursive directory link was expanded")
				}
			} else if byPath["alias.md"].Link != "original/agent.md" || byPath["directory"].Link != "original" {
				t.Fatal("unselected aliases were materialized")
			}
			if _, exists := byPath["original/agent.md"]; exists {
				t.Fatal("physical target selection was ignored")
			}
			if byPath["dangling"].Link != "not-installed" {
				t.Fatal("unreferenced dangling source link changed")
			}
			if err := WriteFiles(t.TempDir(), rendered); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func write(t *testing.T, root, path, text string, mode os.FileMode) {
	t.Helper()
	dst := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, root string) {
	t.Helper()
	write(t, root, ".codex-plugin/plugin.json", `{"name":"parity-probe","version":"1.0.0","description":"native parity fixture","hooks":["./hooks/first.json","./hooks/second.json"],"interface":{"displayName":"Parity probe"},"future":{"preserve":true}}`, 0o644)
	write(t, root, "skills/probe/SKILL.md", "---\nname: probe\ndescription: Use when testing plugin identity.\nmetadata:\n  owner: shrug-labs\n  last_updated: 2026-10-01\n---\nReturn AIPACK_PARITY_PROBE.\n", 0o644)
	write(t, root, "skills/probe/assets/marker.txt", "retained asset\n", 0o644)
	write(t, root, "scripts/hook.sh", "#!/bin/sh\nprintf 'AIPACK_NATIVE_HOOK\\n'\n", 0o755)
	write(t, root, "node_modules/bundled/index.js", "module.exports = 'bundled';\n", 0o644)
	write(t, root, "LICENSE", "Synthetic fixture: MIT\n", 0o644)
	write(t, root, "hooks/first.json", `{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[{"type":"command","command":"${PLUGIN_ROOT}/scripts/hook.sh","timeout":17,"additionalContextLimit":1234}]}],"Stop":[{"matcher":"","hooks":[{"type":"command","command":"echo first-stop","async":true,"futureHandlerField":42}]}]},"futureFileField":true}`, 0o644)
	write(t, root, "hooks/second.json", `{"hooks":{"Stop":[{"matcher":".*","hooks":[{"type":"command","command":"echo second-stop","timeout":19,"statusMessage":"done"}]}]}}`, 0o644)
	write(t, root, ".mcp.json", `{"mcpServers":{"probe":{"command":"${PLUGIN_ROOT}/scripts/hook.sh","args":[],"env":{"STATE":"${PLUGIN_DATA}"}},"other":{"url":"https://example.invalid/mcp"}},"nativeExtension":true}`, 0o644)
}

func selection(m config.PackManifest, root string) domain.NativePluginSelection {
	s := domain.NativePluginSelection{Package: *m.NativePlugin, Root: root, SourcePack: m.Name, SettingsEnabled: true,
		Selected: map[domain.PackCategory][]string{}}
	for cat, entries := range m.NativePlugin.Components {
		for id := range entries {
			s.Selected[cat] = append(s.Selected[cat], id)
		}
		slices.Sort(s.Selected[cat])
	}
	return s
}

func TestCodexNativeSettingsSelection(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, src, ".codex-plugin/plugin.json", `{"name":"probe","apps":"./config/apps.json","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md","future":true},"other":{"preserve":true}}}`, 0o644)
	write(t, src, "skills/setup/SKILL.md", "---\nname: setup\ndescription: Setup fixture.\n---\nSetup content.\n", 0o644)
	write(t, src, "config/apps.json", `{"apps":{"fixture":{"id":"fixture-id","category":"developer"}},"future":true}`, 0o644)
	m, err := MaterializeCodex(src, dst, "probe", "market")
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, dst)
	full, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	read := func(files []File, path string) []byte {
		t.Helper()
		for _, file := range files {
			if file.Path == path {
				return file.Content
			}
		}
		t.Fatalf("missing payload %s", path)
		return nil
	}
	if !bytes.Equal(read(full, "config/apps.json"), []byte(`{"apps":{"fixture":{"id":"fixture-id","category":"developer"}},"future":true}`)) {
		t.Fatal("all-selected native settings changed")
	}
	s.SettingsEnabled = false
	filtered, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(read(filtered, "config/apps.json"), []byte("fixture-id")) || bytes.Contains(read(filtered, ".codex-plugin/plugin.json"), []byte("onboardingSkill")) || !bytes.Contains(read(filtered, "config/apps.json"), []byte("future")) || !bytes.Contains(read(filtered, ".codex-plugin/plugin.json"), []byte("preserve")) {
		t.Fatal("settings toggle lost metadata or retained native setup activation")
	}
	s.SettingsEnabled = true
	s.Selected[domain.CategorySkills] = nil
	filtered, err = RenderCodex(s)
	if err != nil || bytes.Contains(read(filtered, ".codex-plugin/plugin.json"), []byte("onboardingSkill")) {
		t.Fatalf("excluded onboarding skill retained native activation: %v", err)
	}
}

func TestCodexNativeConversionAndSelection(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	fixture(t, src)
	m, err := MaterializeCodex(src, dst, "local-alias", "aipack-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "local-alias" || m.NativePlugin.Binding() != "parity-probe@aipack-fixture" {
		t.Fatalf("identity changed: %+v", m)
	}
	if !reflect.DeepEqual(m.Hooks, []string{"codex-stop", "codex-user-prompt-submit"}) {
		t.Fatalf("unstable events: %v", m.Hooks)
	}
	if len(m.NativePlugin.Components[domain.CategoryHooks]["codex-stop"]) != 2 {
		t.Fatal("event must include every hook source")
	}
	loaded, err := config.LoadPackManifest(filepath.Join(dst, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RelPath(domain.CategorySkills, "probe") != "upstream/skills/probe/SKILL.md" {
		t.Fatal("component paths did not survive serialization")
	}
	s := selection(m, dst)
	full, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	original, err := ReadFiles(src)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, original) {
		t.Fatal("all-selected native payload changed")
	}
	second := t.TempDir()
	m2, err := MaterializeCodex(src, second, "local-alias", "aipack-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, m2) {
		t.Fatal("conversion not deterministic")
	}
	s.Selected[domain.CategoryHooks] = []string{"codex-user-prompt-submit"}
	s.Selected[domain.CategorySkills] = nil
	s.Selected[domain.CategoryMCP] = []string{"probe"}
	filtered, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range filtered {
		if file.Path == "skills/probe/SKILL.md" {
			t.Fatal("excluded skill can still be discovered")
		}
		if strings.HasPrefix(file.Path, "hooks/") && bytes.Contains(file.Content, []byte(`"Stop"`)) {
			t.Fatalf("Stop survived in %s", file.Path)
		}
		if file.Path == "hooks/first.json" && !bytes.Contains(file.Content, []byte(`1234`)) {
			t.Fatal("native context limit lost")
		}
		if file.Path == ".mcp.json" && bytes.Contains(file.Content, []byte(`"other"`)) {
			t.Fatal("excluded MCP server survived")
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "upstream/skills/probe/SKILL.md")); err != nil {
		t.Fatal("selection damaged installed source", err)
	}
	// Changing handler order/contents must not change the event selector IDs.
	write(t, src, "hooks/second.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo changed-stop"},{"type":"command","command":"echo additional-stop"}]}]}}`, 0o644)
	updated, err := ReadCodex(src, "aipack-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Hooks, m.Hooks) {
		t.Fatal("handler edits changed selectors")
	}
}

func TestNativePayloadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	root := t.TempDir()
	files := []File{
		{Path: "empty", Mode: os.ModeDir | 0o710},
		{Path: "read-only", Mode: os.ModeDir | 0o550},
		{Path: "read-only/bin", Mode: 0o770, Content: []byte("#!/bin/sh\nexit 0\n")},
		{Path: "read-only/data", Mode: 0o660, Content: []byte("asset")},
	}
	defer os.Chmod(filepath.Join(root, "read-only"), 0o700)
	if err := WriteFiles(root, files); err != nil {
		t.Fatal(err)
	}
	actual, err := ReadFiles(root)
	if err != nil || !reflect.DeepEqual(actual, files) {
		t.Fatalf("native payload permissions changed: %+v %v", actual, err)
	}
}

func TestCodexInlineSelectionAndBoundary(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	fixture(t, src)
	write(t, src, ".codex-plugin/plugin.json", `{"name":"parity-probe","hooks":[{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}},{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo prompt"}]}]}}],"mcpServers":{"probe":{"command":"echo","customField":true},"other":{"command":"other"}},"extensions":{"preserve":true}}`, 0o644)
	m, err := MaterializeCodex(src, dst, "", "aipack-fixture")
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, dst)
	s.Selected[domain.CategoryHooks] = nil
	s.Selected[domain.CategoryMCP] = []string{"probe"}
	files, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path != m.NativePlugin.Manifest {
			continue
		}
		if bytes.Contains(file.Content, []byte(`"Stop"`)) || bytes.Contains(file.Content, []byte(`"other"`)) {
			t.Fatal("inline exclusions lost")
		}
		if !bytes.Contains(file.Content, []byte(`"extensions"`)) || !bytes.Contains(file.Content, []byte(`"customField"`)) {
			t.Fatal("unknown native fields lost")
		}
	}
	write(t, src, ".codex-plugin/plugin.json", `{"name":"parity-probe","hooks":"../escape.json"}`, 0o644)
	if _, err := ReadCodex(src, "aipack-fixture"); err == nil {
		t.Fatal("accepted path traversal")
	}
	outside := t.TempDir()
	write(t, outside, "secret", "not package content", 0o644)
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(src); err == nil {
		t.Fatal("accepted escaping symlink")
	}
}

// Native smoke tests are opt-in: they run installed host binaries against a
// disposable HOME, never the user's active plugin installation or credentials.
func TestCodexNativeCacheRefresh(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CODEX_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CODEX_NATIVE=1 to exercise the installed Codex CLI")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	root, src, pack := t.TempDir(), t.TempDir(), t.TempDir()
	fixture(t, src)
	// The native fixture uses the host's accepted schema. Preservation tests
	// above deliberately include unknown fields, which this pinned host rejects.
	write(t, src, "hooks/first.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"${PLUGIN_ROOT}/scripts/hook.sh","timeout":17,"additionalContextLimit":1234}]}],"Stop":[{"hooks":[{"type":"command","command":"echo first-stop","async":true}]}]}}`, 0o644)
	m, err := MaterializeCodex(src, pack, "alias", "aipack-fixture")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	env = slices.DeleteFunc(env, func(value string) bool {
		return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=")
	})
	env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env, cmd.Dir = env, root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("codex %v: %v\n%s", args, err, out)
		}
		return out
	}
	t.Logf("host: %s", run("--version"))
	market := filepath.Join(root, "market")
	write(t, market, ".agents/plugins/marketplace.json", `{"name":"aipack-fixture","plugins":[{"name":"parity-probe","source":"./plugins/parity-probe"}]}`, 0o644)
	s := selection(m, pack)
	files, err := RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	selectedRoot := filepath.Join(market, "plugins/parity-probe")
	if err := WriteFiles(selectedRoot, files); err != nil {
		t.Fatal(err)
	}
	run("plugin", "marketplace", "add", market, "--json")
	var installed struct {
		PluginID      string `json:"pluginId"`
		InstalledPath string `json:"installedPath"`
	}
	if err := json.Unmarshal(run("plugin", "add", m.NativePlugin.Binding(), "--json"), &installed); err != nil {
		t.Fatal(err)
	}
	if installed.PluginID != m.NativePlugin.Binding() {
		t.Fatal("native identity changed")
	}
	before, err := ReadFiles(installed.InstalledPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, files) {
		t.Fatal("native installer changed fixture payload")
	}
	hooks := nativeHooks(t, binary, root, env)
	if len(hooks) != 3 {
		t.Fatalf("native loader found %d hooks, expected all three handlers: %v", len(hooks), hooks)
	}
	for _, hook := range hooks {
		if hook["pluginId"] != m.NativePlugin.Binding() || hook["trustStatus"] != "untrusted" || hook["isManaged"] != false {
			t.Fatalf("native identity/trust changed: %v", hook)
		}
		if hook["eventName"] == "userPromptSubmit" && hook["additionalContextLimit"] != float64(1234) {
			t.Fatal("native loader lost context limit")
		}
	}
	s.Selected[domain.CategoryHooks] = []string{"codex-user-prompt-submit"}
	s.Selected[domain.CategorySkills] = nil
	files, err = RenderCodex(s)
	if err != nil {
		t.Fatal(err)
	}
	// Removing this disposable view is necessary to prove entrypoint subtraction.
	if err := os.RemoveAll(selectedRoot); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(selectedRoot, files); err != nil {
		t.Fatal(err)
	}
	run("plugin", "add", m.NativePlugin.Binding(), "--json")
	after, err := ReadFiles(installed.InstalledPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, files) {
		t.Fatal("selection-only refresh did not reach native cache")
	}
	hooks = nativeHooks(t, binary, root, env)
	if len(hooks) != 1 || hooks[0]["eventName"] != "userPromptSubmit" {
		t.Fatalf("native event exclusion failed: %v", hooks)
	}
	configBytes, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(configBytes, []byte("trusted_hash")) {
		t.Fatal("native install granted hook trust")
	}
}

func nativeHooks(t *testing.T, binary, cwd string, env []string) []map[string]any {
	t.Helper()
	result := nativeRequest(t, binary, cwd, env, "hooks/list", map[string]any{"cwds": []string{cwd}})
	var response struct {
		Data []struct {
			Hooks  []map[string]any `json:"hooks"`
			Errors []any            `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Errors) > 0 {
		t.Fatalf("hooks/list: %s", result)
	}
	return response.Data[0].Hooks
}

func nativeRequest(t *testing.T, binary, cwd string, env []string, method string, params any, diagnosticOutput ...*string) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.WaitDelay = time.Second
	cmd.Env, cmd.Dir = env, cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopTimeout := context.AfterFunc(ctx, func() { _ = stdout.Close(); _ = stdin.Close() })
	defer stopTimeout()
	defer func() {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		if len(diagnosticOutput) != 0 {
			*diagnosticOutput[0] = diagnostics.String()
		}
	}()
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 4096), 4*1024*1024)
	request := func(id int, method string, params any) json.RawMessage {
		t.Helper()
		body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(stdin, "%s\n", body); err != nil {
			t.Fatal(err)
		}
		t.Logf("native request %s", method)
		for reader.Scan() {
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(reader.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) > 0 {
				t.Fatalf("%s: %s", method, response.Error)
			}
			return response.Result
		}
		t.Fatalf("app-server closed during %s: %v\n%s", method, reader.Err(), diagnostics.String())
		return nil
	}
	request(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "aipack_native_test", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	if _, err := fmt.Fprintln(stdin, `{"method":"initialized"}`); err != nil {
		t.Fatal(err)
	}
	return request(2, method, params)
}

func TestCodexNativeProfileScoping(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	fixture(t, src)
	first := filepath.Join(root, "packs/first")
	if _, err := MaterializeCodex(src, first, "first", "aipack-fixture"); err != nil {
		t.Fatal(err)
	}
	write(t, src, ".codex-plugin/plugin.json", `{"name":"second-probe","hooks":["./hooks/first.json","./hooks/second.json"]}`, 0o644)
	if _, err := MaterializeCodex(src, filepath.Join(root, "packs/second"), "second", "aipack-fixture"); err != nil {
		t.Fatal(err)
	}
	excludes := []string{"codex-stop"}
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "first", Hooks: config.HookSelector{VectorSelector: config.VectorSelector{Exclude: &excludes}}}, {Name: "second"}}}
	resolved, err := config.ResolveProfileWithOptions(cfg, "", root, config.ResolveOptions{CollisionStrategy: config.CollisionError, Namespaced: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Packs) != 2 || len(resolved.Packs[0].Hooks) != 1 || len(resolved.Packs[1].Hooks) != 2 {
		t.Fatal("cross-plugin hook collision or exclusion leaked")
	}
	for _, pack := range resolved.Packs {
		if len(pack.Skills) != 1 || len(pack.MCP) != 2 {
			t.Fatal("native leaf IDs collided across plugins")
		}
	}
	copyPack := filepath.Join(root, "packs/duplicate")
	if _, err := MaterializeCodex(src, copyPack, "duplicate", "aipack-fixture"); err != nil {
		t.Fatal(err)
	}
	cfg.Packs = append(cfg.Packs, config.PackEntry{Name: "duplicate"})
	if _, err := config.ResolveProfileWithOptions(cfg, "", root, config.ResolveOptions{}); err == nil {
		t.Fatal("duplicate native binding accepted")
	}
	for _, finding := range config.ValidatePackRoot(first) {
		if finding.Severity == config.FindingSeverityError {
			t.Fatal(finding.String())
		}
	}
}
