package util

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestJSONPropertyOrder(t *testing.T) {
	raw := json.RawMessage(`{"z":9007199254740993,"2":1e+1000,"a":null,"z":18446744073709551617}`)
	order, err := JSONPropertyNames(raw)
	if err != nil || !slices.Equal(order, []string{"z", "2", "a"}) {
		t.Fatalf("insertion order differs: %v %v", order, err)
	}
	ordered, err := OrderJSONObject(raw, order)
	if err != nil || string(ordered) != `{"z":18446744073709551617,"2":1e+1000,"a":null}` {
		t.Fatalf("ordered values changed: %s %v", ordered, err)
	}
	filtered, err := OrderJSONObject(json.RawMessage(`{"b":true,"z":1}`), []string{"z", "removed", "z"})
	if err != nil || string(filtered) != `{"z":1,"b":true}` {
		t.Fatalf("removed or additional keys differ: %s %v", filtered, err)
	}
	for _, input := range []string{"[]", "null", "{} trailing", `{"broken":}`} {
		if _, err := JSONPropertyNames(json.RawMessage(input)); err == nil {
			t.Fatalf("accepted invalid object %s", input)
		}
		if _, err := OrderJSONObject(json.RawMessage(input), nil); err == nil {
			t.Fatalf("ordered invalid object %s", input)
		}
	}
}

func TestUnmarshalJSONRetainsNumbersAndRejectsTrailingData(t *testing.T) {
	var value map[string]any
	input := []byte(`{"integer":9007199254740993,"nested":[0.12345678901234567890123456789,1e+1000]}`)
	if err := UnmarshalJSON(input, &value); err != nil {
		t.Fatal(err)
	}
	if value["integer"] != json.Number("9007199254740993") || value["nested"].([]any)[0] != json.Number("0.12345678901234567890123456789") || value["nested"].([]any)[1] != json.Number("1e+1000") {
		t.Fatalf("JSON values rounded: %+v", value)
	}
	for _, input := range []string{"", "{} {}", "{} trailing", `{"invalid":01}`} {
		if err := UnmarshalJSON([]byte(input), &value); err == nil {
			t.Fatalf("accepted invalid/trailing JSON %q", input)
		}
	}
}
