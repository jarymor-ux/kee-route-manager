package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"go.yaml.in/yaml/v3"
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
	if _, err := config.Load(tmpName); err != nil {
		return fmt.Errorf("verify generated config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

type uiAPIDocument struct {
	Enabled bool `json:"enabled"`
}

type uiWebDocument struct {
	Enabled bool       `json:"enabled"`
	Listen  string     `json:"listen"`
	TLS     config.TLS `json:"tls"`
}

type uiPlatformDocument struct {
	Kind string `json:"kind"`
}

type uiConfigDocument struct {
	SchemaVersion int                `json:"schema_version"`
	Instance      config.Instance    `json:"instance"`
	Paths         config.Paths       `json:"paths"`
	API           uiAPIDocument      `json:"api"`
	Web           uiWebDocument      `json:"web"`
	Platform      uiPlatformDocument `json:"platform"`
	UI            config.UIProxy     `json:"ui"`
}

func marshalYAML(cfg config.Config) ([]byte, error) {
	document := any(cfg)
	if cfg.Instance.Role == "ui" || cfg.Instance.Role == "ui-proxy" {
		document = uiConfigDocument{
			SchemaVersion: cfg.SchemaVersion,
			Instance:      cfg.Instance,
			Paths:         cfg.Paths,
			API:           uiAPIDocument{Enabled: cfg.API.Enabled},
			Web: uiWebDocument{
				Enabled: cfg.Web.Enabled,
				Listen:  cfg.Web.Listen,
				TLS:     cfg.Web.TLS,
			},
			Platform: uiPlatformDocument{Kind: cfg.Platform.Kind},
			UI:       cfg.UIProxy,
		}
	}

	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}

	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	quoteYAMLStrings(&node)

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func quoteYAMLStrings(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		node.Style = yaml.DoubleQuotedStyle
	}
	for _, child := range node.Content {
		quoteYAMLStrings(child)
	}
}
