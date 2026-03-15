package engine

import (
	"encoding/json"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestMergeSettingsKeys_JSON_AddKeys(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"user_key": "user_val"}`)
	prev := []byte(`{}`)
	next := []byte(`{"managed_key": "managed_val"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode)
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

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode)
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

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessOpenCode)
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

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	// user_item should be preserved, old_managed dropped, new_managed added.
	found := map[string]bool{}
	for _, v := range items {
		found[v.(string)] = true
	}
	if !found["user_item"] {
		t.Error("user_item should be preserved")
	}
	if found["old_managed"] {
		t.Error("old_managed should be removed")
	}
	if !found["new_managed"] {
		t.Error("new_managed should be added")
	}
	assertOp(t, ops, "items", MergeUpdate)
}

func TestMergeSettingsKeys_FirstSync(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"user_key": "user_val"}`)
	var prev []byte // nil = first sync
	next := []byte(`{"managed_key": "managed_val"}`)

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode)
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

	result, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Error("result should be non-empty")
	}
	assertOp(t, ops, "managed_key", MergeAdd)
}

func TestMergeSettingsKeys_UnsupportedHarness(t *testing.T) {
	t.Parallel()
	_, _, err := mergeSettingsKeys(nil, nil, nil, "unknown")
	if err == nil {
		t.Error("expected error for unknown harness")
	}
}

func TestMergeSettingsKeys_NoOpsWhenIdentical(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"managed_key": "val"}`)
	prev := []byte(`{"managed_key": "val"}`)
	next := []byte(`{"managed_key": "val"}`)

	_, ops, err := mergeSettingsKeys(existing, prev, next, domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for identical merge, got %d: %v", len(ops), ops)
	}
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
