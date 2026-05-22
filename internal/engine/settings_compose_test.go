package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestComposeConfigFiles_SingleFile(t *testing.T) {
	t.Parallel()
	input := []domain.ConfigFile{
		{Filename: "settings.local.json", Content: []byte(`{"theme": "dark"}`), SourcePack: "alpha"},
	}
	out, warnings, err := composeConfigFiles(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if string(out) != `{"theme": "dark"}` {
		t.Errorf("single file should pass through unchanged, got %s", out)
	}
}

func TestComposeConfigFiles_JSON_DisjointKeys(t *testing.T) {
	t.Parallel()
	packA := []byte(`{
  "editor": {
    "tabSize": 4,
    "formatOnSave": true
  },
  "theme": "dark"
}`)
	packB := []byte(`{
  "linting": {
    "enabled": true,
    "rules": ["no-unused-vars"]
  },
  "telemetry": false
}`)
	files := []domain.ConfigFile{
		{Filename: "settings.local.json", Content: packA, SourcePack: "alpha"},
		{Filename: "settings.local.json", Content: packB, SourcePack: "beta"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("disjoint keys should produce no warnings, got %d", len(warnings))
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	// All keys from both packs should be present.
	if m["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", m["theme"])
	}
	if m["telemetry"] != false {
		t.Errorf("telemetry = %v, want false", m["telemetry"])
	}
	editor, _ := m["editor"].(map[string]any)
	if editor == nil || editor["tabSize"] != float64(4) {
		t.Errorf("editor.tabSize = %v, want 4", editor["tabSize"])
	}
	linting, _ := m["linting"].(map[string]any)
	if linting == nil || linting["enabled"] != true {
		t.Errorf("linting.enabled = %v, want true", linting["enabled"])
	}
}

func TestComposeConfigFiles_JSON_LeafConflict_FirstWins(t *testing.T) {
	t.Parallel()
	packA := []byte(`{
  "theme": "dark",
  "editor": {
    "tabSize": 4,
    "wordWrap": true
  }
}`)
	packB := []byte(`{
  "theme": "light",
  "editor": {
    "tabSize": 2,
    "formatOnSave": true
  }
}`)
	files := []domain.ConfigFile{
		{Filename: "settings.local.json", Content: packA, SourcePack: "alpha"},
		{Filename: "settings.local.json", Content: packB, SourcePack: "beta"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}

	// Expect warnings for "theme" and "editor.tabSize".
	if len(warnings) != 2 {
		t.Fatalf("expected 2 conflict warnings, got %d: %v", len(warnings), warnings)
	}
	var foundTheme, foundTabSize bool
	for _, w := range warnings {
		if strings.Contains(w.Message, `"theme"`) && strings.Contains(w.Message, "alpha") && strings.Contains(w.Message, "beta") {
			foundTheme = true
		}
		if strings.Contains(w.Message, `"editor.tabSize"`) {
			foundTabSize = true
		}
	}
	if !foundTheme {
		t.Error("missing warning for theme conflict")
	}
	if !foundTabSize {
		t.Error("missing warning for editor.tabSize conflict")
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	// First pack wins on conflicts.
	if m["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (first pack wins)", m["theme"])
	}
	editor, _ := m["editor"].(map[string]any)
	if editor["tabSize"] != float64(4) {
		t.Errorf("editor.tabSize = %v, want 4 (first pack wins)", editor["tabSize"])
	}
	// Non-conflicting key from second pack is added.
	if editor["formatOnSave"] != true {
		t.Errorf("editor.formatOnSave = %v, want true (from beta)", editor["formatOnSave"])
	}
	// Non-conflicting key from first pack preserved.
	if editor["wordWrap"] != true {
		t.Errorf("editor.wordWrap = %v, want true (from alpha)", editor["wordWrap"])
	}
}

func TestComposeConfigFiles_JSON_ThreePacks(t *testing.T) {
	t.Parallel()
	packA := []byte(`{"level": "team", "shared": {"x": 1}}`)
	packB := []byte(`{"level": "project", "shared": {"y": 2}}`)
	packC := []byte(`{"level": "personal", "shared": {"x": 99, "z": 3}}`)

	files := []domain.ConfigFile{
		{Filename: "s.json", Content: packA, SourcePack: "a"},
		{Filename: "s.json", Content: packB, SourcePack: "b"},
		{Filename: "s.json", Content: packC, SourcePack: "c"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}

	// "level" conflicts: b and c both conflict with a.
	// "shared.x" conflicts: c conflicts with a.
	levelWarnings := 0
	sharedXWarnings := 0
	for _, w := range warnings {
		if strings.Contains(w.Message, `"level"`) {
			levelWarnings++
		}
		if strings.Contains(w.Message, `"shared.x"`) {
			sharedXWarnings++
		}
	}
	if levelWarnings != 2 {
		t.Errorf("expected 2 level conflict warnings, got %d", levelWarnings)
	}
	if sharedXWarnings != 1 {
		t.Errorf("expected 1 shared.x conflict warning, got %d", sharedXWarnings)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["level"] != "team" {
		t.Errorf("level = %v, want team (first pack wins)", m["level"])
	}
	shared, _ := m["shared"].(map[string]any)
	if shared["x"] != float64(1) {
		t.Errorf("shared.x = %v, want 1 (first pack wins)", shared["x"])
	}
	if shared["y"] != float64(2) {
		t.Errorf("shared.y = %v, want 2 (from b)", shared["y"])
	}
	if shared["z"] != float64(3) {
		t.Errorf("shared.z = %v, want 3 (from c)", shared["z"])
	}
}

func TestComposeConfigFiles_JSON_NestedObjectMerge(t *testing.T) {
	t.Parallel()
	packA := []byte(`{
  "permissions": {
    "allow": ["Bash(npm test:*)"],
    "deny": ["Bash(rm -rf *)"]
  }
}`)
	packB := []byte(`{
  "permissions": {
    "allow": ["Edit(**)"],
    "custom": true
  }
}`)
	files := []domain.ConfigFile{
		{Filename: "settings.local.json", Content: packA, SourcePack: "team"},
		{Filename: "settings.local.json", Content: packB, SourcePack: "personal"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}

	// "permissions.allow" is a leaf (array) — first pack wins, warning emitted.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for permissions.allow conflict, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0].Message, "permissions.allow") {
		t.Errorf("warning should mention permissions.allow, got: %s", warnings[0].Message)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	perms, _ := m["permissions"].(map[string]any)
	// "allow" from first pack wins.
	allow, _ := perms["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(npm test:*)" {
		t.Errorf("permissions.allow = %v, want [Bash(npm test:*)]", allow)
	}
	// "deny" from first pack preserved.
	deny, _ := perms["deny"].([]any)
	if len(deny) != 1 {
		t.Errorf("permissions.deny = %v, want [Bash(rm -rf *)]", deny)
	}
	// "custom" from second pack added (no conflict).
	if perms["custom"] != true {
		t.Errorf("permissions.custom = %v, want true", perms["custom"])
	}
}

func TestComposeConfigFiles_JSON_IdenticalValues_NoWarning(t *testing.T) {
	t.Parallel()
	packA := []byte(`{"theme": "dark", "level": 1}`)
	packB := []byte(`{"theme": "dark", "level": 1}`)
	files := []domain.ConfigFile{
		{Filename: "s.json", Content: packA, SourcePack: "a"},
		{Filename: "s.json", Content: packB, SourcePack: "b"},
	}
	_, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("identical values should not warn, got %v", warnings)
	}
}

func TestComposeConfigFiles_TOML_DisjointKeys(t *testing.T) {
	t.Parallel()
	packA := []byte(`
[model]
name = "claude-sonnet-4-6"
temperature = 0.7

[logging]
level = "info"
`)
	packB := []byte(`
[editor]
tab_size = 4
format_on_save = true

[telemetry]
enabled = false
`)
	files := []domain.ConfigFile{
		{Filename: "config.toml", Content: packA, SourcePack: "alpha"},
		{Filename: "config.toml", Content: packB, SourcePack: "beta"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("disjoint TOML keys should produce no warnings, got %d", len(warnings))
	}

	var m map[string]any
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal merged TOML: %v", err)
	}
	model, _ := m["model"].(map[string]any)
	if model == nil || model["name"] != "claude-sonnet-4-6" {
		t.Errorf("model.name = %v, want claude-sonnet-4-6", model["name"])
	}
	editor, _ := m["editor"].(map[string]any)
	if editor == nil || editor["tab_size"] != int64(4) {
		t.Errorf("editor.tab_size = %v, want 4", editor["tab_size"])
	}
	telemetry, _ := m["telemetry"].(map[string]any)
	if telemetry == nil || telemetry["enabled"] != false {
		t.Errorf("telemetry.enabled = %v, want false", telemetry["enabled"])
	}
}

func TestComposeConfigFiles_TOML_LeafConflict_FirstWins(t *testing.T) {
	t.Parallel()
	packA := []byte(`
[model]
name = "claude-opus-4-6"
max_tokens = 4096

[settings]
theme = "dark"
`)
	packB := []byte(`
[model]
name = "claude-sonnet-4-6"
temperature = 0.5

[settings]
theme = "light"
auto_save = true
`)
	files := []domain.ConfigFile{
		{Filename: "config.toml", Content: packA, SourcePack: "team"},
		{Filename: "config.toml", Content: packB, SourcePack: "personal"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}

	// Expect warnings for model.name and settings.theme.
	if len(warnings) != 2 {
		t.Fatalf("expected 2 conflict warnings, got %d: %v", len(warnings), warnings)
	}
	var foundModelName, foundTheme bool
	for _, w := range warnings {
		if strings.Contains(w.Message, `"model.name"`) {
			foundModelName = true
		}
		if strings.Contains(w.Message, `"settings.theme"`) {
			foundTheme = true
		}
	}
	if !foundModelName {
		t.Error("missing warning for model.name conflict")
	}
	if !foundTheme {
		t.Error("missing warning for settings.theme conflict")
	}

	var m map[string]any
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	model, _ := m["model"].(map[string]any)
	// First pack wins.
	if model["name"] != "claude-opus-4-6" {
		t.Errorf("model.name = %v, want claude-opus-4-6 (first wins)", model["name"])
	}
	if model["max_tokens"] != int64(4096) {
		t.Errorf("model.max_tokens = %v, want 4096", model["max_tokens"])
	}
	// Non-conflicting key from second pack added.
	if model["temperature"] != 0.5 {
		t.Errorf("model.temperature = %v, want 0.5 (from personal)", model["temperature"])
	}

	settings, _ := m["settings"].(map[string]any)
	if settings["theme"] != "dark" {
		t.Errorf("settings.theme = %v, want dark (first wins)", settings["theme"])
	}
	if settings["auto_save"] != true {
		t.Errorf("settings.auto_save = %v, want true (from personal)", settings["auto_save"])
	}
}

func TestComposeConfigFiles_TOML_ThreePacks_DeepNesting(t *testing.T) {
	t.Parallel()
	packA := []byte(`
[agents.primary]
model = "opus"
instructions = "be helpful"

[agents.primary.limits]
max_tokens = 8192
`)
	packB := []byte(`
[agents.primary]
model = "sonnet"

[agents.primary.limits]
timeout = 30

[agents.secondary]
model = "haiku"
`)
	packC := []byte(`
[agents.primary.limits]
max_tokens = 4096
rate_limit = 100
`)
	files := []domain.ConfigFile{
		{Filename: "config.toml", Content: packA, SourcePack: "team"},
		{Filename: "config.toml", Content: packB, SourcePack: "project"},
		{Filename: "config.toml", Content: packC, SourcePack: "personal"},
	}
	out, warnings, err := composeConfigFiles(files)
	if err != nil {
		t.Fatal(err)
	}

	// Conflicts: agents.primary.model (b vs a), agents.primary.limits.max_tokens (c vs a).
	modelWarnings := 0
	maxTokensWarnings := 0
	for _, w := range warnings {
		if strings.Contains(w.Message, `"agents.primary.model"`) {
			modelWarnings++
		}
		if strings.Contains(w.Message, `"agents.primary.limits.max_tokens"`) {
			maxTokensWarnings++
		}
	}
	if modelWarnings != 1 {
		t.Errorf("expected 1 agents.primary.model warning, got %d", modelWarnings)
	}
	if maxTokensWarnings != 1 {
		t.Errorf("expected 1 agents.primary.limits.max_tokens warning, got %d", maxTokensWarnings)
	}

	var m map[string]any
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	agents, _ := m["agents"].(map[string]any)
	primary, _ := agents["primary"].(map[string]any)
	if primary["model"] != "opus" {
		t.Errorf("agents.primary.model = %v, want opus (first wins)", primary["model"])
	}
	if primary["instructions"] != "be helpful" {
		t.Errorf("agents.primary.instructions = %v, want 'be helpful'", primary["instructions"])
	}
	limits, _ := primary["limits"].(map[string]any)
	if limits["max_tokens"] != int64(8192) {
		t.Errorf("limits.max_tokens = %v, want 8192 (first wins)", limits["max_tokens"])
	}
	if limits["timeout"] != int64(30) {
		t.Errorf("limits.timeout = %v, want 30 (from project)", limits["timeout"])
	}
	if limits["rate_limit"] != int64(100) {
		t.Errorf("limits.rate_limit = %v, want 100 (from personal)", limits["rate_limit"])
	}
	// Entirely new subtree from second pack.
	secondary, _ := agents["secondary"].(map[string]any)
	if secondary == nil || secondary["model"] != "haiku" {
		t.Errorf("agents.secondary.model = %v, want haiku", secondary)
	}
}

func TestComposeConfigFiles_Empty(t *testing.T) {
	t.Parallel()
	out, warnings, err := composeConfigFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("expected nil output for empty input, got %s", out)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty input, got %v", warnings)
	}
}

func TestComposeConfigFiles_JSON_ParseError(t *testing.T) {
	t.Parallel()
	// Parse errors surface when merging (need 2+ files to trigger parsing).
	files := []domain.ConfigFile{
		{Filename: "bad.json", Content: []byte(`{"ok": true}`), SourcePack: "good"},
		{Filename: "bad.json", Content: []byte(`not json`), SourcePack: "broken"},
	}
	_, _, err := composeConfigFiles(files)
	if err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should mention pack name, got: %v", err)
	}
}

func TestComposeConfigFiles_TOML_ParseError(t *testing.T) {
	t.Parallel()
	files := []domain.ConfigFile{
		{Filename: "ok.toml", Content: []byte("[section]\nkey = \"val\"\n"), SourcePack: "good"},
		{Filename: "ok.toml", Content: []byte("not valid toml [[["), SourcePack: "broken"},
	}
	_, _, err := composeConfigFiles(files)
	if err == nil {
		t.Fatal("expected parse error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should mention pack name, got: %v", err)
	}
}

// writeCodexSettingsPack creates a pack rooted at <configDir>/packs/<name>
// with configs/codex/config.toml containing the given settings, and returns
// a ResolvedPack pointing at it with codex listed under HarnessSettings.
func writeCodexSettingsPack(t *testing.T, configDir, name string, settings []byte) config.ResolvedPack {
	t.Helper()
	root := filepath.Join(configDir, "packs", name)
	if err := os.MkdirAll(filepath.Join(root, "configs", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "configs", "codex", "config.toml"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	return config.ResolvedPack{
		Name: name, Root: root,
		Manifest: config.PackManifest{Configs: config.PackConfigs{
			HarnessSettings: map[string][]string{string(domain.HarnessCodex): {"config.toml"}},
		}},
	}
}

func writeCodexHookPack(t *testing.T, configDir, name, filename string, content []byte) config.ResolvedPack {
	t.Helper()
	root := filepath.Join(configDir, "packs", name)
	hookID := strings.TrimSuffix(filename, filepath.Ext(filename))
	if err := os.MkdirAll(filepath.Join(root, "hooks", hookID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks", hookID, domain.HookEntryFile), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return config.ResolvedPack{
		Name:     name,
		Root:     root,
		Hooks:    []string{hookID},
		Manifest: config.PackManifest{Hooks: []string{hookID}},
	}
}

func TestLoadHarnessSettings_ExpandsTemplateRefs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AIPACK_SETTINGS_HOME", home)

	configDir := t.TempDir()
	pack := writeCodexSettingsPack(t, configDir, "alpha", []byte(`
workdir = "{env:AIPACK_SETTINGS_HOME}/codex"
cache_dir = "{params.cache_dir}"

[tools.local]
data_dir = "{pack:root}/data"
`))

	eng := New(nil, nil)
	bundle, warnings, err := eng.loadHarnessSettings(
		[]config.ResolvedPack{pack},
		[]string{"alpha"},
		[]domain.Harness{domain.HarnessCodex},
		map[string]string{"cache_dir": "{env:AIPACK_SETTINGS_HOME}/cache"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	var got map[string]any
	if err := toml.Unmarshal(bundle.FileBytes(domain.HarnessCodex, "config.toml"), &got); err != nil {
		t.Fatal(err)
	}
	if got["workdir"] != home+"/codex" {
		t.Errorf("workdir = %v", got["workdir"])
	}
	if got["cache_dir"] != home+"/cache" {
		t.Errorf("cache_dir = %v", got["cache_dir"])
	}
	tools, _ := got["tools"].(map[string]any)
	local, _ := tools["local"].(map[string]any)
	if local["data_dir"] != filepath.Clean(pack.Root)+"/data" {
		t.Errorf("tools.local.data_dir = %v", local["data_dir"])
	}
}

func TestParseHooks_ExpandsTemplateRefs(t *testing.T) {
	t.Setenv("AIPACK_HOOK_ENDPOINT", "http://127.0.0.1:4318")
	configDir := t.TempDir()
	pack := writeCodexHookPack(t, configDir, "alpha", "tool-audit.yaml", []byte(`
name: tool-audit
events:
  - on: tool.after
    match:
      tool: "*"
    handler:
      type: command
      command: "python3 {hook:root}/bin/hook.py --pack {pack:root} --endpoint {env:AIPACK_HOOK_ENDPOINT} --user {params.user}"
`))

	eng := New(nil, nil)
	hooks, warnings, err := eng.parseHooks(pack, map[string]string{"user": "first.last"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(hooks))
	}
	content := hooks[0].Events[0].EffectiveHandlers()[0].Command
	normalizedContent := filepath.ToSlash(content)
	for _, want := range []string{
		filepath.Join(pack.Root, "hooks", "tool-audit", "bin", "hook.py"),
		filepath.Clean(pack.Root),
		"http://127.0.0.1:4318",
		"first.last",
	} {
		if !strings.Contains(normalizedContent, filepath.ToSlash(want)) {
			t.Fatalf("hook content missing %q:\n%s", want, content)
		}
	}
}

func TestLoadHarnessSettings_UnresolvedEnvRefErrors(t *testing.T) {
	const missingEnv = "AIPACK_SETTINGS_MISSING_VAR_12345"
	old, hadOld := os.LookupEnv(missingEnv)
	if err := os.Unsetenv(missingEnv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadOld {
			_ = os.Setenv(missingEnv, old)
		} else {
			_ = os.Unsetenv(missingEnv)
		}
	})

	pack := writeCodexSettingsPack(t, t.TempDir(), "alpha",
		[]byte(`workdir = "{env:`+missingEnv+`}/codex"`))

	eng := New(nil, nil)
	_, _, err := eng.loadHarnessSettings(
		[]config.ResolvedPack{pack},
		[]string{"alpha"},
		[]domain.Harness{domain.HarnessCodex},
		nil,
	)
	if err == nil {
		t.Fatal("expected unresolved env ref error")
	}
	for _, want := range []string{"config.toml", "alpha", "workdir", missingEnv, "aipack config env set " + missingEnv + " <value>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestLoadHarnessSettings_UnresolvedParamErrors(t *testing.T) {
	pack := writeCodexSettingsPack(t, t.TempDir(), "alpha",
		[]byte("model = \"gpt-test\"\nworkdir = \"{params.no_such_key}\"\n"))

	eng := New(nil, nil)
	_, _, err := eng.loadHarnessSettings(
		[]config.ResolvedPack{pack},
		[]string{"alpha"},
		[]domain.Harness{domain.HarnessCodex},
		map[string]string{"other_key": "value"},
	)
	if err == nil {
		t.Fatal("expected unresolved param error")
	}
	for _, want := range []string{"config.toml", "alpha", "workdir", "no_such_key"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestLoadHarnessSettings_MultiPackOnePackUsesRefs(t *testing.T) {
	t.Setenv("AIPACK_MULTIPACK_VAR", "/from-env")

	configDir := t.TempDir()
	packA := writeCodexSettingsPack(t, configDir, "alpha",
		[]byte("model = \"alpha-model\"\nworkdir = \"{env:AIPACK_MULTIPACK_VAR}/a\"\n"))
	packB := writeCodexSettingsPack(t, configDir, "beta",
		[]byte("[history]\nsave = false\n"))

	eng := New(nil, nil)
	bundle, warnings, err := eng.loadHarnessSettings(
		[]config.ResolvedPack{packA, packB},
		[]string{"alpha", "beta"},
		[]domain.Harness{domain.HarnessCodex},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	// Verify by parsing — re-marshal happens in the merge step, so quote
	// style isn't byte-stable, but the resolved values are.
	var parsed map[string]any
	if err := toml.Unmarshal(bundle.FileBytes(domain.HarnessCodex, "config.toml"), &parsed); err != nil {
		t.Fatalf("merged TOML failed to parse: %v\n%s", err, bundle.FileBytes(domain.HarnessCodex, "config.toml"))
	}
	if parsed["model"] != "alpha-model" {
		t.Errorf("model = %v, want alpha-model", parsed["model"])
	}
	if parsed["workdir"] != "/from-env/a" {
		t.Errorf("workdir = %v, want /from-env/a (env should expand before merge)", parsed["workdir"])
	}
	history, _ := parsed["history"].(map[string]any)
	if history["save"] != false {
		t.Errorf("history.save = %v, want false (from beta)", history["save"])
	}
}

func TestExpandConfigFileRefs_BackslashValueEscapedThroughMarshaller(t *testing.T) {
	t.Setenv("AIPACK_BS_VAR", `C:\Users\Dave`)

	got, err := expandConfigFileRefs(nil, "",
		domain.ConfigFile{Filename: "x.toml", Content: []byte(`workdir = "{env:AIPACK_BS_VAR}/sub"` + "\n")})
	if err != nil {
		t.Fatal(err)
	}
	// The marshaller is responsible for escaping; round-trip parse must
	// return the raw env value.
	var parsed map[string]any
	if err := toml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("expanded TOML failed to parse: %v\n%s", err, got)
	}
	if parsed["workdir"] != `C:\Users\Dave/sub` {
		t.Errorf("workdir = %v, want %s", parsed["workdir"], `C:\Users\Dave/sub`)
	}
}

func TestExpandConfigFileRefs_PackRootRequired(t *testing.T) {
	_, err := expandConfigFileRefs(nil, "",
		domain.ConfigFile{Filename: "x.json", Content: []byte(`{"data_dir": "{pack:root}/data"}`)})
	if err == nil || !strings.Contains(err.Error(), "pack:root") {
		t.Fatalf("expected pack:root error, got %v", err)
	}
}

func TestComposeConfigFiles_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	files := []domain.ConfigFile{
		{Filename: "a.yaml", Content: []byte("k: v"), SourcePack: "alpha"},
		{Filename: "a.yaml", Content: []byte("k: w"), SourcePack: "beta"},
	}
	_, _, err := composeConfigFiles(files)
	if err == nil || !strings.Contains(err.Error(), "unsupported settings file extension") {
		t.Fatalf("expected unsupported-extension error, got %v", err)
	}
}
