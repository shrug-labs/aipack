package harness

import (
	"bytes"
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

func TestApplyEditRetainsNumberTokens(t *testing.T) {
	input := []byte(`{"managed":true,"counter":9007199254740993,"nested":[0.12345678901234567890123456789,1e+1000]}`)
	out, err := ApplyEdit(input, FormatJSON, EditContext{}, func(root map[string]any, _ EditContext) { delete(root, "managed") })
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"9007199254740993", "0.12345678901234567890123456789", "1e+1000"} {
		if !bytes.Contains(out, []byte(token)) {
			t.Fatalf("capture/clean rounded %s: %s", token, out)
		}
	}
	for _, invalid := range []string{"null", "[]", "{} {}"} {
		if _, err := ApplyEdit([]byte(invalid), FormatJSON, EditContext{}, func(map[string]any, EditContext) { t.Fatal("edit ran on invalid input") }); err == nil {
			t.Fatalf("edited non-object JSON %q", invalid)
		}
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
