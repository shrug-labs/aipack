package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// UnmarshalJSON retains number tokens instead of rounding them to float64.
// Validate the whole document so a decoder cannot accept trailing JSON.
func UnmarshalJSON(data []byte, dst any) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(dst)
}

// JSONPropertyNames retains insertion order and the first position of repeated
// keys. Values follow normal JSON decoding, where the last definition wins.
func JSONPropertyNames(raw json.RawMessage) ([]string, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var names []string
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		name := key.(string)
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names, nil
}

// OrderJSONObject preserves an object's requested key order after ordinary
// mapping storage. Removed keys stay removed; additional keys follow in sorted
// order. Number tokens remain exact.
func OrderJSONObject(raw json.RawMessage, order []string) (json.RawMessage, error) {
	var object map[string]any
	if err := UnmarshalJSON(raw, &object); err != nil {
		return nil, fmt.Errorf("expected a JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	seen := map[string]bool{}
	for _, name := range append(slices.Clone(order), slices.Sorted(maps.Keys(object))...) {
		value, exists := object[name]
		if !exists || seen[name] {
			continue
		}
		seen[name] = true
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(body)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

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
