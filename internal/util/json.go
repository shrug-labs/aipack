package util

import (
	"bytes"
	"encoding/json"
)

// MarshalPrettyJSON writes aipack's standard human-edited JSON format:
// two-space indentation, no HTML escaping, and a trailing newline.
func MarshalPrettyJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
