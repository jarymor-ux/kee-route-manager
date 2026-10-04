package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func WriteConfig(path string, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config before write: %w", err)
	}

	data, err := marshalYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".krm-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func marshalYAML(cfg config.Config) ([]byte, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := writeYAMLValue(&out, value, 0); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeYAMLValue(out *bytes.Buffer, value any, indent int) error {
	switch v := value.(type) {
	case map[string]any:
		return writeYAMLMap(out, v, indent)
	case []any:
		return writeYAMLList(out, v, indent)
	default:
		return fmt.Errorf("top-level YAML value must be a map or list")
	}
}

func writeYAMLMap(out *bytes.Buffer, values map[string]any, indent int) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := values[key]
		writeIndent(out, indent)
		out.WriteString(key)
		switch v := value.(type) {
		case map[string]any:
			if len(v) == 0 {
				out.WriteString(": {}\n")
				continue
			}
			out.WriteString(":\n")
			if err := writeYAMLMap(out, v, indent+2); err != nil {
				return err
			}
		case []any:
			if len(v) == 0 {
				out.WriteString(": []\n")
				continue
			}
			out.WriteString(":\n")
			if err := writeYAMLList(out, v, indent+2); err != nil {
				return err
			}
		default:
			scalar, err := yamlScalar(v)
			if err != nil {
				return err
			}
			out.WriteString(": ")
			out.WriteString(scalar)
			out.WriteByte('\n')
		}
	}
	return nil
}

func writeYAMLList(out *bytes.Buffer, values []any, indent int) error {
	for _, value := range values {
		writeIndent(out, indent)
		switch v := value.(type) {
		case map[string]any:
			if len(v) == 0 {
				out.WriteString("- {}\n")
				continue
			}
			out.WriteString("-\n")
			if err := writeYAMLMap(out, v, indent+2); err != nil {
				return err
			}
		case []any:
			if len(v) == 0 {
				out.WriteString("- []\n")
				continue
			}
			out.WriteString("-\n")
			if err := writeYAMLList(out, v, indent+2); err != nil {
				return err
			}
		default:
			scalar, err := yamlScalar(v)
			if err != nil {
				return err
			}
			out.WriteString("- ")
			out.WriteString(scalar)
			out.WriteByte('\n')
		}
	}
	return nil
}

func yamlScalar(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return strconv.Quote(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		return v.String(), nil
	case nil:
		return "null", nil
	default:
		return "", fmt.Errorf("unsupported YAML scalar %T", value)
	}
}

func writeIndent(out *bytes.Buffer, count int) {
	out.WriteString(strings.Repeat(" ", count))
}
