package harness

import (
	"encoding/json"
	"testing"
)

func TestApplyEdit_EmptyJSON(t *testing.T) {
	t.Parallel()
	out, err := ApplyEdit(nil, FormatJSON, EditContext{}, func(root map[string]any, _ EditContext) {
		root["added"] = true
	})
	if err != nil {
		t.Fatalf("ApplyEdit empty JSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["added"] != true {
		t.Errorf("expected added=true, got %v", got)
	}
}

func TestApplyEdit_EmptyTOML(t *testing.T) {
	t.Parallel()
	out, err := ApplyEdit(nil, FormatTOML, EditContext{}, func(root map[string]any, _ EditContext) {
		delete(root, "nonexistent")
	})
	if err != nil {
		t.Fatalf("ApplyEdit empty TOML: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestStripManaged_NoMatch_PassThrough(t *testing.T) {
	t.Parallel()
	l := Layout{
		OwnedFiles: []OwnedFile{{
			Path: "/a/settings.json", Format: FormatJSON,
			Strip: func(root map[string]any, _ EditContext) { delete(root, "managed") },
		}},
	}
	input := []byte(`{"managed": true}`)
	out, err := l.StripManaged(input, "/b/other.json", EditContext{})
	if err != nil {
		t.Fatalf("StripManaged: %v", err)
	}
	if string(out) != string(input) {
		t.Errorf("expected pass-through, got %s", out)
	}
}
