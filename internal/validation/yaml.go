package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

func decodeYAML(input []byte) (any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("invalid trailing YAML: %w", err)
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("YAML document must contain one root node")
	}
	value, err := yamlValue(document.Content[0], make(map[*yaml.Node]bool))
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("YAML value is not representable as JSON: %w", err)
	}
	decoded, err := decodeJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("convert YAML to JSON data model: %w", err)
	}
	return decoded, nil
}

func yamlValue(node *yaml.Node, active map[*yaml.Node]bool) (any, error) {
	if node == nil {
		return nil, fmt.Errorf("empty YAML node")
	}
	if node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return nil, fmt.Errorf("unresolved YAML alias at line %d", node.Line)
		}
		return yamlValue(node.Alias, active)
	}
	if active[node] {
		return nil, fmt.Errorf("recursive YAML alias at line %d", node.Line)
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, fmt.Errorf("invalid YAML document at line %d", node.Line)
		}
		return yamlValue(node.Content[0], active)

	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return nil, fmt.Errorf("invalid YAML mapping at line %d", node.Line)
		}
		active[node] = true
		defer delete(active, node)

		result := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
				return nil, fmt.Errorf("mapping keys must be strings (line %d)", keyNode.Line)
			}
			key := keyNode.Value
			if key == "<<" {
				return nil, fmt.Errorf("YAML merge keys are not supported (line %d)", keyNode.Line)
			}
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate YAML mapping key %q (line %d)", key, keyNode.Line)
			}
			value, err := yamlValue(node.Content[i+1], active)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil

	case yaml.SequenceNode:
		active[node] = true
		defer delete(active, node)
		result := make([]any, len(node.Content))
		for i, child := range node.Content {
			value, err := yamlValue(child, active)
			if err != nil {
				return nil, err
			}
			result[i] = value
		}
		return result, nil

	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!null", "!!bool", "!!int", "!!float":
			var value any
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("decode YAML scalar at line %d: %w", node.Line, err)
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unsupported YAML tag %q at line %d", node.Tag, node.Line)
		}
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d at line %d", node.Kind, node.Line)
	}
}
