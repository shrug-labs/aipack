package domain

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNativeMetadataRejectsNonJSONValues(t *testing.T) {
	for _, input := range []string{
		"not-an-object", "[1,2]", "value: .inf", "value: !!int null",
		"value: !!timestamp 2026-10-02", "1: value", "value: 1\nvalue: 2",
		"value: &cycle [*cycle]",
	} {
		var value NativeJSONMap
		if err := yaml.Unmarshal([]byte(input), &value); err == nil {
			t.Fatalf("native JSON metadata accepted %q: %+v", input, value)
		}
	}
}
