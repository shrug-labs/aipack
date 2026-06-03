package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestMergeSettingsKeys_JSON_AddKeys(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"user_key": "user_val"}`)
	prev := []byte(`{}`)
	next := []byte(`{"managed_key": "managed_val"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if m["user_key"] != "user_val" {
		t.Errorf("user_key lost: %v", m)
	}
	if m["managed_key"] != "managed_val" {
		t.Errorf("managed_key not added: %v", m)
	}
	assertOp(t, ops, "managed_key", MergeAdd)
}

func TestMergeSettingsKeys_JSON_RemoveKeys(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"managed_key": "old_val", "user_key": "user_val"}`)
	prev := []byte(`{"managed_key": "old_val"}`)
	next := []byte(`{}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["managed_key"]; ok {
		t.Errorf("managed_key should be removed: %v", m)
	}
	if m["user_key"] != "user_val" {
		t.Errorf("user_key should be preserved: %v", m)
	}
	assertOp(t, ops, "managed_key", MergeRemove)
}

func TestMergeSettingsKeys_JSON_NestedMerge(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"outer": {"user_nested": "a", "managed_nested": "old"}}`)
	prev := []byte(`{"outer": {"managed_nested": "old"}}`)
	next := []byte(`{"outer": {"managed_nested": "new"}}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessOpenCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	outer := m["outer"].(map[string]any)
	if outer["user_nested"] != "a" {
		t.Errorf("user_nested lost")
	}
	if outer["managed_nested"] != "new" {
		t.Errorf("managed_nested not updated")
	}
	assertOp(t, ops, "outer.managed_nested", MergeUpdate)
}

func TestMergeSettingsKeys_JSON_ArrayMerge(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"items": ["user_item", "old_managed"]}`)
	prev := []byte(`{"items": ["old_managed"]}`)
	next := []byte(`{"items": ["new_managed"]}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	// Managed items first (new_managed), then user items (user_item).
	// old_managed removed because it was in prev but not next.
	want := []string{"new_managed", "user_item"}
	if len(items) != len(want) {
		t.Fatalf("items = %v, want %v", items, want)
	}
	for i, w := range want {
		if items[i].(string) != w {
			t.Errorf("items[%d] = %q, want %q", i, items[i], w)
		}
	}
	assertOp(t, ops, "items", MergeUpdate)
}

func TestMergeSettingsKeys_JSON_ArrayPreservesManagedOrder(t *testing.T) {
	t.Parallel()
	// Simulates the args swap bug: disk has args in wrong order,
	// managed has the correct order. The merge must restore managed order.
	existing := []byte(`{"args": ["--index", "url", "--env-file", "pkg@latest", "/path/.env"]}`)
	prev := []byte(`{"args": ["--index", "url", "--env-file", "/path/.env", "pkg@latest"]}`)
	next := []byte(`{"args": ["--index", "url", "--env-file", "/path/.env", "pkg@latest"]}`)

	result, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	args := m["args"].([]any)
	want := []string{"--index", "url", "--env-file", "/path/.env", "pkg@latest"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i, w := range want {
		if args[i].(string) != w {
			t.Errorf("args[%d] = %q, want %q (managed order not restored)", i, args[i], w)
		}
	}
}

func TestMergeSettingsKeys_JSON_ArrayPreservesNonStringUserElements(t *testing.T) {
	t.Parallel()
	// A user adds a non-string element (an object) to a managed array. The
	// merge must preserve it.
	existing := []byte(`{"items": ["user_str", {"user_obj": 1}, "old_managed"]}`)
	prev := []byte(`{"items": ["old_managed"]}`)
	next := []byte(`{"items": ["new_managed"]}`)

	result, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	// new_managed (managed) + user_str + the user object; old_managed removed.
	if len(items) != 3 {
		t.Fatalf("items = %v, want 3 (managed + user string + user object)", items)
	}
	foundObj := false
	for _, it := range items {
		if obj, ok := it.(map[string]any); ok && obj["user_obj"] != nil {
			foundObj = true
		}
	}
	if !foundObj {
		t.Errorf("user-added object element was dropped from merged array: %v", items)
	}
}

func TestMergeSettingsKeys_ClaudeHookObjectArraysReplaceManagedPreserveUser(t *testing.T) {
	t.Parallel()
	existing := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "old-managed"}]},
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "user-hook"}]},
      {"matcher": "Read", "hooks": [{"type": "command", "command": "old-managed-user-edited"}]}
    ]
  }
}`)
	prev := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "old-managed"}]},
      {"matcher": "Read", "hooks": [{"type": "command", "command": "old-managed"}]}
    ]
  }
}`)
	next := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "new-managed"}]}
    ]
  }
}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var root map[string]any
	if err := json.Unmarshal(result, &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	groups := hooks["PreToolUse"].([]any)
	got := hookGroupCommands(groups)
	want := []string{"new-managed", "user-hook", "old-managed-user-edited"}
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands = %v, want %v", got, want)
		}
	}
	assertOp(t, ops, "hooks.PreToolUse", MergeUpdate)
}

func hookGroupCommands(groups []any) []string {
	var out []string
	for _, rawGroup := range groups {
		group, _ := rawGroup.(map[string]any)
		rawHooks, _ := group["hooks"].([]any)
		if len(rawHooks) == 0 {
			out = append(out, "")
			continue
		}
		hook, _ := rawHooks[0].(map[string]any)
		cmd, _ := hook["command"].(string)
		out = append(out, cmd)
	}
	return out
}

func TestMergeSettingsKeys_AdditiveOnly_RetainsManagedKeyAbsentFromNext(t *testing.T) {
	t.Parallel()
	// additiveOnly must suppress removal of a managed key that drops out of
	// next, independent of the plugin-path exemption. Exercise both polarities
	// on a non-plugin key so the flag's own effect is observed.
	existing := []byte(`{"managed_key": "v", "user_key": "u"}`)
	prev := []byte(`{"managed_key": "v"}`)
	next := []byte(`{}`)

	// additiveOnly=false: the managed key, absent from next, is removed.
	subtractive, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	var sm map[string]any
	if err := json.Unmarshal(subtractive, &sm); err != nil {
		t.Fatal(err)
	}
	if _, ok := sm["managed_key"]; ok {
		t.Error("additiveOnly=false: managed_key should be removed when absent from next")
	}

	// additiveOnly=true: the same managed key is retained.
	additive, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, true)
	if err != nil {
		t.Fatal(err)
	}
	var am map[string]any
	if err := json.Unmarshal(additive, &am); err != nil {
		t.Fatal(err)
	}
	if _, ok := am["managed_key"]; !ok {
		t.Error("additiveOnly=true: managed_key should be retained even when absent from next")
	}
	if am["user_key"] != "u" {
		t.Error("user_key should always be preserved")
	}
}

func TestMergeSettingsKeys_JSON_EmptyManagedArrayMarshalsToEmptyNotNull(t *testing.T) {
	t.Parallel()
	// When a managed array empties out, the merge must yield JSON `[]`, not
	// `null` because `permissions.allow: null` breaks Claude Code startup.
	existing := []byte(`{"permissions":{"allow":[]}}`)
	prev := []byte(`{"permissions":{"allow":[]}}`)
	next := []byte(`{"permissions":{"allow":[]}}`)

	result, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "null") {
		t.Fatalf("merged output must not contain null: %s", result)
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	perms, ok := m["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions missing from merged output: %s", result)
	}
	if _, ok := perms["allow"].([]any); !ok {
		t.Errorf("permissions.allow should marshal to an empty array, got %v (%s)", perms["allow"], result)
	}
}

func TestMergeSettingsKeys_JSON_DoesNotHTMLEscapeUserValues(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"mcpServers":{"user":{"command":"sh","args":["-lc","echo '<ok>' && run 2> >(tee log >&2)"]}}}`)
	prev := []byte(`{}`)
	next := []byte(`{"mcpServers":{"managed":{"command":"echo","args":["hi"]}}}`)

	result, _, err := mergeSettingsKeys(existing, prev, next, domain.HarnessCline, false)
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(content, escaped) {
			t.Fatalf("merged JSON should not HTML-escape %s:\n%s", escaped, content)
		}
	}
	if !strings.Contains(content, `echo '<ok>' && run 2> >(tee log >&2)`) {
		t.Fatalf("user command changed unexpectedly:\n%s", content)
	}
}

func TestMergeSettingsKeys_FirstSync(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"user_key": "user_val"}`)
	var prev []byte // nil = first sync
	next := []byte(`{"managed_key": "managed_val"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if m["user_key"] != "user_val" {
		t.Error("user_key should be preserved on first sync")
	}
	if m["managed_key"] != "managed_val" {
		t.Error("managed_key should be added on first sync")
	}
	assertOp(t, ops, "managed_key", MergeAdd)
}

func TestMergeSettingsKeys_FirstSyncPreservesScalarCollision(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"profile": "local-profile", "user_key": "user_val"}`)
	var prev []byte // nil = first sync
	next := []byte(`{"profile": "pack-profile", "sandbox_mode": "workspace-write"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if m["profile"] != "local-profile" {
		t.Fatalf("first sync should preserve existing scalar collision, got profile=%v", m["profile"])
	}
	if m["sandbox_mode"] != "workspace-write" {
		t.Fatalf("first sync should add non-conflicting managed scalar, got sandbox_mode=%v", m["sandbox_mode"])
	}
	assertNoOp(t, ops, "profile", MergeUpdate)
	assertOp(t, ops, "profile", MergeConflict)
	assertOp(t, ops, "sandbox_mode", MergeAdd)
}

func TestMergeSettingsKeys_PreservesLocallyEditedManagedScalar(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"managed_key": "local-edit", "unchanged_key": "old"}`)
	prev := []byte(`{"managed_key": "old", "unchanged_key": "old"}`)
	next := []byte(`{"managed_key": "new", "unchanged_key": "new"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if m["managed_key"] != "local-edit" {
		t.Fatalf("locally edited scalar should be preserved, got managed_key=%v", m["managed_key"])
	}
	if m["unchanged_key"] != "new" {
		t.Fatalf("unchanged managed scalar should update, got unchanged_key=%v", m["unchanged_key"])
	}
	assertNoOp(t, ops, "managed_key", MergeUpdate)
	assertOp(t, ops, "managed_key", MergeConflict)
	assertOp(t, ops, "unchanged_key", MergeUpdate)
}

func TestMergeSettingsKeys_TOML(t *testing.T) {
	t.Parallel()
	existing := []byte("user_key = \"user_val\"\n")
	prev := []byte("")
	next := []byte("managed_key = \"managed_val\"\n")

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Error("result should be non-empty")
	}
	assertOp(t, ops, "managed_key", MergeAdd)
}

func TestMergeSettingsKeys_TOML_FirstSyncPreservesCodexProfile(t *testing.T) {
	t.Parallel()
	existing := []byte(`
profile = "local-profile"

[profiles.local-profile]
model_provider = "local-provider"
`)
	var prev []byte // nil = first sync
	next := []byte(`
profile = "pack-profile"
sandbox_mode = "workspace-write"

[profiles.pack-profile]
model_provider = "pack-provider"
`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessCodex, false)
	if err != nil {
		t.Fatal(err)
	}

	m, err := parseTOMLMap(result)
	if err != nil {
		t.Fatalf("parse merged TOML: %v\n%s", err, result)
	}
	if m["profile"] != "local-profile" {
		t.Fatalf("Codex profile should preserve existing value, got %v", m["profile"])
	}
	if m["sandbox_mode"] != "workspace-write" {
		t.Fatalf("sandbox_mode should be added from managed settings, got %v", m["sandbox_mode"])
	}
	profiles, ok := m["profiles"].(map[string]any)
	if !ok {
		t.Fatalf("profiles table missing from merged TOML:\n%s", result)
	}
	if _, ok := profiles["local-profile"]; !ok {
		t.Fatalf("existing profile table should be preserved:\n%s", result)
	}
	if _, ok := profiles["pack-profile"]; !ok {
		t.Fatalf("managed profile table should be added:\n%s", result)
	}
	assertNoOp(t, ops, "profile", MergeUpdate)
	assertOp(t, ops, "profile", MergeConflict)
	assertOp(t, ops, "sandbox_mode", MergeAdd)
}

func TestMergeSettingsKeys_TOML_RemovesCodexMCPServerWithRuntimeToolApproval(t *testing.T) {
	t.Parallel()
	existing := []byte(`
[mcp_servers.current]
command = "keep"

[mcp_servers.current.tools.runtime_allowed]
approval_mode = "approve"

[mcp_servers.previous]
command = "previous"
args = ["arg"]

[mcp_servers.previous.tools.runtime_allowed]
approval_mode = "approve"
`)
	prev := []byte(`
[mcp_servers.current]
command = "keep"

[mcp_servers.previous]
command = "previous"
args = ["arg"]
`)
	next := []byte(`
[mcp_servers.current]
command = "keep"
`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	content := string(result)
	if strings.Contains(content, "previous") {
		t.Fatalf("removed MCP server should not leave orphaned tool approval tables:\n%s", content)
	}
	if !strings.Contains(content, "runtime_allowed") {
		t.Fatalf("runtime tool approval for retained server should be preserved:\n%s", content)
	}
	assertOp(t, ops, "mcp_servers.previous", MergeRemove)
}

func TestMergeSettingsKeys_UnsupportedHarness(t *testing.T) {
	t.Parallel()
	_, _, err := mergeSettingsKeys(nil, nil, nil, "unknown", false)
	if err == nil {
		t.Error("expected error for unknown harness")
	}
}

func TestMergeSettingsKeys_NoOpsWhenIdentical(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"managed_key": "val"}`)
	prev := []byte(`{"managed_key": "val"}`)
	next := []byte(`{"managed_key": "val"}`)

	_, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for identical merge, got %d: %v", len(ops), ops)
	}
}

func TestMergeSettingsKeys_JSON_CorruptedOnDisk(t *testing.T) {
	t.Parallel()
	garbage := []byte("CORRUPTED BY TEST")
	prev := []byte(`{}`)
	next := []byte(`{"managed_key": "managed_val"}`)

	result, ops, err := mergeSettingsKeys(garbage, prev, next, domain.HarnessClaudeCode, false)
	if err != nil {
		t.Fatalf("corrupted on-disk JSON should not fail: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if m["managed_key"] != "managed_val" {
		t.Errorf("managed state should win when on-disk is corrupted: %v", m)
	}
	assertOp(t, ops, "(root)", MergeReset)
	assertOp(t, ops, "managed_key", MergeAdd)
}

func TestMergeSettingsKeys_TOML_CorruptedOnDisk(t *testing.T) {
	t.Parallel()
	garbage := []byte("CORRUPTED BY TEST")
	prev := []byte(``)
	next := []byte("[mcp_servers]\n[mcp_servers.test]\ncmd = \"echo\"\n")

	result, ops, err := mergeSettingsKeys(garbage, prev, next, domain.HarnessCodex, false)
	if err != nil {
		t.Fatalf("corrupted on-disk TOML should not fail: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected non-empty result when managed state overwrites corrupted disk")
	}
	assertOp(t, ops, "(root)", MergeReset)
}

// assertOp checks that ops contains a MergeOp with the given key and action.
func assertOp(t *testing.T, ops []MergeOp, key string, action MergeAction) {
	t.Helper()
	for _, op := range ops {
		if op.Key == key && op.Action == action {
			return
		}
	}
	t.Errorf("expected merge op {Key: %q, Action: %q} not found in %v", key, action, ops)
}

func assertNoOp(t *testing.T, ops []MergeOp, key string, action MergeAction) {
	t.Helper()
	for _, op := range ops {
		if op.Key == key && op.Action == action {
			t.Fatalf("unexpected merge op {Key: %q, Action: %q} found in %v", key, action, ops)
		}
	}
}
