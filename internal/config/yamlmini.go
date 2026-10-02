package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type yamlLine struct {
	indent, number int
	text           string
}

func parseYAMLSubset(data []byte) (any, error) {
	lines := []yamlLine{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	n := 0
	for scanner.Scan() {
		n++
		raw := strings.TrimRight(scanner.Text(), " \r\t")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if strings.Contains(raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))], "\t") {
			return nil, fmt.Errorf("line %d: tabs are not allowed", n)
		}
		clean := stripYAMLComment(raw)
		if strings.TrimSpace(clean) == "" {
			continue
		}
		indent := len(clean) - len(strings.TrimLeft(clean, " "))
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: indentation must use multiples of two spaces", n)
		}
		lines = append(lines, yamlLine{indent: indent, number: n, text: strings.TrimSpace(clean)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	if lines[0].indent != 0 {
		return nil, fmt.Errorf("line %d: top-level indentation must be zero", lines[0].number)
	}
	value, next, err := parseYAMLBlock(lines, 0, 0)
	if err != nil {
		return nil, err
	}
	if next != len(lines) {
		return nil, fmt.Errorf("line %d: unexpected content", lines[next].number)
	}
	return value, nil
}

func parseYAMLBlock(lines []yamlLine, index, indent int) (any, int, error) {
	if index >= len(lines) {
		return nil, index, fmt.Errorf("missing nested value")
	}
	if lines[index].indent != indent {
		return nil, index, fmt.Errorf("line %d: expected indentation %d", lines[index].number, indent)
	}
	if strings.HasPrefix(lines[index].text, "-") {
		return parseYAMLList(lines, index, indent)
	}
	return parseYAMLMap(lines, index, indent)
}

func parseYAMLMap(lines []yamlLine, index, indent int) (map[string]any, int, error) {
	out := map[string]any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("line %d: unexpected indentation", line.number)
		}
		if strings.HasPrefix(line.text, "-") {
			break
		}
		key, rest, ok := splitYAMLKey(line.text)
		if !ok {
			return nil, index, fmt.Errorf("line %d: expected key: value", line.number)
		}
		if key == "" {
			return nil, index, fmt.Errorf("line %d: empty key", line.number)
		}
		if _, exists := out[key]; exists {
			return nil, index, fmt.Errorf("line %d: duplicate key %q", line.number, key)
		}
		index++
		if rest != "" {
			v, err := parseYAMLScalar(rest)
			if err != nil {
				return nil, index, fmt.Errorf("line %d: %w", line.number, err)
			}
			out[key] = v
			continue
		}
		if index >= len(lines) || lines[index].indent <= indent {
			out[key] = nil
			continue
		}
		if lines[index].indent != indent+2 {
			return nil, index, fmt.Errorf("line %d: nested indentation must be %d", lines[index].number, indent+2)
		}
		v, next, err := parseYAMLBlock(lines, index, indent+2)
		if err != nil {
			return nil, index, err
		}
		out[key] = v
		index = next
	}
	return out, index, nil
}

func parseYAMLList(lines []yamlLine, index, indent int) ([]any, int, error) {
	out := []any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("line %d: unexpected indentation", line.number)
		}
		if !strings.HasPrefix(line.text, "-") {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		index++
		if rest == "" {
			if index >= len(lines) || lines[index].indent <= indent {
				return nil, index, fmt.Errorf("line %d: empty list item", line.number)
			}
			if lines[index].indent != indent+2 {
				return nil, index, fmt.Errorf("line %d: nested indentation must be %d", lines[index].number, indent+2)
			}
			v, next, err := parseYAMLBlock(lines, index, indent+2)
			if err != nil {
				return nil, index, err
			}
			out = append(out, v)
			index = next
			continue
		}
		if key, value, ok := splitYAMLKey(rest); ok {
			item := map[string]any{}
			if key == "" {
				return nil, index, fmt.Errorf("line %d: empty key", line.number)
			}
			if value != "" {
				v, err := parseYAMLScalar(value)
				if err != nil {
					return nil, index, fmt.Errorf("line %d: %w", line.number, err)
				}
				item[key] = v
			} else if index < len(lines) && lines[index].indent > indent {
				if lines[index].indent != indent+4 {
					return nil, index, fmt.Errorf("line %d: nested indentation must be %d", lines[index].number, indent+4)
				}
				v, next, err := parseYAMLBlock(lines, index, indent+4)
				if err != nil {
					return nil, index, err
				}
				item[key] = v
				index = next
			} else {
				item[key] = nil
			}
			for index < len(lines) && lines[index].indent == indent+2 && !strings.HasPrefix(lines[index].text, "-") {
				entry := lines[index]
				k, r, ok := splitYAMLKey(entry.text)
				if !ok {
					return nil, index, fmt.Errorf("line %d: expected key: value", entry.number)
				}
				if _, exists := item[k]; exists {
					return nil, index, fmt.Errorf("line %d: duplicate key %q", entry.number, k)
				}
				index++
				if r != "" {
					v, err := parseYAMLScalar(r)
					if err != nil {
						return nil, index, fmt.Errorf("line %d: %w", entry.number, err)
					}
					item[k] = v
				} else if index < len(lines) && lines[index].indent > indent+2 {
					if lines[index].indent != indent+4 {
						return nil, index, fmt.Errorf("line %d: nested indentation must be %d", lines[index].number, indent+4)
					}
					v, next, err := parseYAMLBlock(lines, index, indent+4)
					if err != nil {
						return nil, index, err
					}
					item[k] = v
					index = next
				} else {
					item[k] = nil
				}
			}
			out = append(out, item)
			continue
		}
		v, err := parseYAMLScalar(rest)
		if err != nil {
			return nil, index, fmt.Errorf("line %d: %w", line.number, err)
		}
		out = append(out, v)
	}
	return out, index, nil
}

func splitYAMLKey(s string) (string, string, bool) {
	inSingle, inDouble := false, false
	escaped := false
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
		if r == ':' && !inSingle && !inDouble {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}
func stripYAMLComment(s string) string {
	inSingle, inDouble := false, false
	escaped := false
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
		if r == '#' && !inSingle && !inDouble && (i == 0 || s[i-1] == ' ') {
			return strings.TrimRight(s[:i], " ")
		}
	}
	return s
}

func parseYAMLScalar(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s == "[]" {
		return []any{}, nil
	}
	if s == "{}" {
		return map[string]any{}, nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		body := strings.TrimSpace(s[1 : len(s)-1])
		if body == "" {
			return []any{}, nil
		}
		parts := splitInline(body)
		out := make([]any, 0, len(parts))
		for _, part := range parts {
			v, err := parseYAMLScalar(part)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	if strings.HasPrefix(s, "\"") {
		v, err := strconv.Unquote(s)
		if err != nil {
			return nil, fmt.Errorf("invalid quoted string")
		}
		return v, nil
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null", "~":
		return nil, nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.ContainsAny(s, ".") {
		return f, nil
	}
	return s, nil
}
func splitInline(s string) []string {
	out := []string{}
	start := 0
	inSingle, inDouble := false, false
	escaped := false
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
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
		}
		if r == ',' && !inSingle && !inDouble {
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}

func yamlSubsetToJSON(data []byte) ([]byte, error) {
	v, err := parseYAMLSubset(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
