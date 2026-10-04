package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func parseYAMLSubset(data []byte) (any, error) {
	raw, err := yamlSubsetToJSON(data)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func yamlSubsetToJSON(data []byte) ([]byte, error) {
	if err := validateYAMLSubsetSource(data); err != nil {
		return nil, err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return json.Marshal(map[string]any{})
		}
		return nil, err
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	if len(document.Content) == 0 {
		return json.Marshal(map[string]any{})
	}

	value, err := yamlSubsetNode(document.Content[0])
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateYAMLSubsetSource(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimRight(scanner.Text(), " \r\t")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		leading := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		if strings.ContainsRune(leading, '\t') {
			return fmt.Errorf("line %d: tabs are not allowed", line)
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(leading)%2 != 0 {
			return fmt.Errorf("line %d: indentation must use multiples of two spaces", line)
		}
		if trimmed == "---" || trimmed == "..." || strings.HasPrefix(trimmed, "%") {
			return fmt.Errorf("line %d: YAML directives and document markers are not supported", line)
		}
	}
	return scanner.Err()
}

func yamlSubsetNode(node *yaml.Node) (any, error) {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("YAML anchors and aliases are not supported")
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return nil, fmt.Errorf("explicit YAML tags are not supported")
	}

	switch node.Kind {
	case yaml.MappingNode:
		if node.Style&yaml.FlowStyle != 0 && len(node.Content) != 0 {
			return nil, fmt.Errorf("non-empty flow mappings are not supported")
		}
		out := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			if keyNode.Kind != yaml.ScalarNode || keyNode.Anchor != "" || keyNode.Style&yaml.TaggedStyle != 0 || keyNode.Tag != "!!str" {
				return nil, fmt.Errorf("YAML mapping key must be a plain string")
			}
			key := keyNode.Value
			if key == "<<" {
				return nil, fmt.Errorf("YAML merge keys are not supported")
			}
			if _, exists := out[key]; exists {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := yamlSubsetNode(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		return out, nil

	case yaml.SequenceNode:
		out := make([]any, len(node.Content))
		for i, item := range node.Content {
			value, err := yamlSubsetNode(item)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil

	case yaml.ScalarNode:
		if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return nil, fmt.Errorf("block scalar strings are not supported")
		}
		if node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
			return node.Value, nil
		}
		return parseYAMLSubsetScalar(node.Value), nil

	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
}

func parseYAMLSubsetScalar(raw string) any {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.Contains(s, ".") {
		return f
	}
	return s
}
