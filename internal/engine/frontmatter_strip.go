package engine

import (
	"gopkg.in/yaml.v3"
)

// StripFrontmatterKeys marshals a frontmatter struct to a map, removes the
// named keys, and returns clean YAML bytes. Used by harness adapters to strip
// fields the target harness doesn't support before rendering content as
// markdown for model consumption.
//
// Examples: "harness" on agents (only meaningful to the native Codex adapter),
// "paths" on rules (only Claude Code supports conditional path loading).
func StripFrontmatterKeys(fm any, keys ...string) ([]byte, error) {
	// Marshal the struct to a generic map so we can remove arbitrary keys.
	raw, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for _, k := range keys {
		delete(m, k)
	}
	return yaml.Marshal(m)
}
