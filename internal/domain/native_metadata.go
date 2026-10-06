package domain

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// NativeJSONMap retains native JSON number tokens in YAML registry/lock files.
// The stored shape remains a mapping, including nested objects and arrays.
type NativeJSONMap map[string]any

func (m NativeJSONMap) MarshalYAML() (any, error) {
	return nativeJSONNode(map[string]any(m))
}

func (m *NativeJSONMap) UnmarshalYAML(node *yaml.Node) error {
	value, err := nativeJSONValue(node)
	if err != nil {
		return err
	}
	obj, ok := value.(map[string]any)
	if value != nil && !ok {
		return fmt.Errorf("native JSON metadata must be an object")
	}
	*m = obj
	return nil
}

func nativeJSONNode(value any) (*yaml.Node, error) {
	var node yaml.Node
	switch value := value.(type) {
	case json.Number:
		var number json.Number
		if err := json.Unmarshal([]byte(value), &number); err != nil {
			return nil, err
		}
		if number.String() != string(value) {
			return nil, fmt.Errorf("invalid native JSON number %q", value)
		}
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!int", string(value)
		if strings.ContainsAny(string(value), ".eE") {
			node.Tag = "!!float"
		}
	case map[string]any:
		node.Kind, node.Tag = yaml.MappingNode, "!!map"
		for _, key := range slices.Sorted(maps.Keys(value)) {
			child, err := nativeJSONNode(value[key])
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
	case []any:
		node.Kind, node.Tag = yaml.SequenceNode, "!!seq"
		for _, item := range value {
			child, err := nativeJSONNode(item)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
	default:
		if err := node.Encode(value); err != nil {
			return nil, err
		}
	}
	return &node, nil
}

func nativeJSONValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		obj := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, fmt.Errorf("native JSON metadata keys must be strings")
			}
			if _, exists := obj[key.Value]; exists {
				return nil, fmt.Errorf("duplicate native JSON metadata key %q", key.Value)
			}
			value, err := nativeJSONValue(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			obj[key.Value] = value
		}
		return obj, nil
	case yaml.SequenceNode:
		items := []any{}
		for _, child := range node.Content {
			value, err := nativeJSONValue(child)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		return items, nil
	case yaml.ScalarNode:
		if node.Tag == "!!int" || node.Tag == "!!float" {
			var value json.Number
			err := json.Unmarshal([]byte(node.Value), &value)
			if err == nil && value.String() != node.Value {
				return nil, fmt.Errorf("invalid native JSON number %q", node.Value)
			}
			return value, err
		}
		if node.Tag != "!!str" && node.Tag != "!!bool" && node.Tag != "!!null" {
			return nil, fmt.Errorf("native JSON metadata cannot contain YAML tag %q", node.Tag)
		}
		var value any
		err := node.Decode(&value)
		return value, err
	default:
		return nil, fmt.Errorf("native JSON metadata cannot contain YAML aliases")
	}
}
