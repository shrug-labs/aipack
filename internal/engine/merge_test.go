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
