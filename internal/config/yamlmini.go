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

	decoder := yaml.NewDecoder(bytes.NewReader(normalizeLegacyPlainMappingScalars(data)))
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

// normalizeLegacyPlainMappingScalars preserves the previous config grammar
// where an unquoted mapping value could contain ": ". yaml.v3 follows the
// YAML specification and rejects that form, so quote only that legacy scalar
// shape before decoding. Unsupported YAML constructs remain untouched and are
// still rejected by yamlSubsetNode.
func normalizeLegacyPlainMappingScalars(data []byte) []byte {
	lines := bytes.Split(data, []byte{'\n'})
	changed := false
	for i, line := range lines {
		normalized, ok := normalizeLegacyPlainMappingLine(string(line))
		if !ok {
			continue
		}
		lines[i] = []byte(normalized)
		changed = true
	}
	if !changed {
		return data
	}
	return bytes.Join(lines, []byte{'\n'})
}

func normalizeLegacyPlainMappingLine(raw string) (string, bool) {
	cr := ""
	if strings.HasSuffix(raw, "\r") {
		raw = strings.TrimSuffix(raw, "\r")
		cr = "\r"
	}

	leading := len(raw) - len(strings.TrimLeft(raw, " "))
	prefix, body := raw[:leading], raw[leading:]
	if body == "" || strings.HasPrefix(body, "#") {
		return raw + cr, false
	}
	if strings.HasPrefix(body, "- ") {
		prefix += "- "
		body = body[2:]
	}

	separator := legacyMappingColon(body)
	if separator < 0 {
		return raw + cr, false
	}
	rest := body[separator+1:]
	spaceLen := len(rest) - len(strings.TrimLeft(rest, " \t"))
	valueAndSuffix := rest[spaceLen:]
	if valueAndSuffix == "" {
		return raw + cr, false
	}

	valueEnd := len(valueAndSuffix)
	if comment := legacyPlainCommentIndex(valueAndSuffix); comment >= 0 {
		valueEnd = comment
	}
	value := strings.TrimRight(valueAndSuffix[:valueEnd], " \t")
	if !legacyPlainScalarNeedsQuote(value) {
		return raw + cr, false
	}
	suffix := valueAndSuffix[len(value):]

	normalized := prefix + body[:separator+1] + rest[:spaceLen] + strconv.Quote(value) + suffix + cr
	return normalized, true
}

func legacyPlainScalarNeedsQuote(value string) bool {
	if value == "" || !strings.ContainsAny(value, ":") ||
		(!strings.Contains(value, ": ") && !strings.Contains(value, ":\t")) {
		return false
	}
	switch value[0] {
	case '"', '\'', '{', '[', '&', '*', '!', '|', '>':
		return false
	default:
		return true
	}
}

func legacyMappingColon(s string) int {
	inSingle, inDouble, escaped := false, false, false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inDouble {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == ':' && !inSingle && !inDouble && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return i
		}
	}
	return -1
}

func legacyPlainCommentIndex(s string) int {
	inSingle, inDouble, escaped := false, false, false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inDouble {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == '#' && !inSingle && !inDouble && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return i
		}
	}
	return -1
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
		if isYAMLDocumentMarker(trimmed) || strings.HasPrefix(trimmed, "%") {
			return fmt.Errorf("line %d: YAML directives and document markers are not supported", line)
		}
	}
	return scanner.Err()
}

func isYAMLDocumentMarker(line string) bool {
	if line == "---" || line == "..." {
		return true
	}
	if len(line) < 4 || line[:3] != "---" && line[:3] != "..." {
		return false
	}
	return line[3] == ' ' || line[3] == '\t'
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
			if keyNode.Kind != yaml.ScalarNode || keyNode.Anchor != "" || keyNode.Style&yaml.TaggedStyle != 0 {
				return nil, fmt.Errorf("YAML mapping key must be a scalar string")
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
