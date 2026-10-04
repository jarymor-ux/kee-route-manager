package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `schema_version: 1
instance:
  name: Test
  role: controller
paths:
  state_dir: state
  cache_dir: cache
  log_file: logs/krm.log
  run_dir: run
web:
  enabled: true
  listen: "127.0.0.1:9444"
  credentials_file: credentials.json
  session_ttl: 1h
  tls:
    enabled: false
    auto_generate: false
    cert_file: tls.crt
    key_file: tls.key
platform:
  kind: linux-systemd
xray:
  binary: /bin/true
  config_dir: /tmp/xray
  managed_dir: /tmp/xray
  base_routing_file: /tmp/xray/route.json
  api_address: "127.0.0.1:10085"
subscriptions:
  sources:
    - id: provider_a
      name: Provider A
      url: "file:///tmp/sub.txt"
      enabled: true
targets:
  - id: score_target
    name: Score
    url: "https://example.com/"
    role: score
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 64KiB
  - id: health_target
    name: Health
    url: "https://example.com/"
    role: health
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 64KiB
benchmark:
  speed:
    enabled: false
update:
  enabled: false
`

func TestLoadYAMLSubsetAndDefaults(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	if e := os.WriteFile(p, []byte(validYAML), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	if c.Pool.Size != 5 {
		t.Fatalf("pool default=%d", c.Pool.Size)
	}
	if c.Paths.StateDir != filepath.Join(d, "state") {
		t.Fatalf("relative path not resolved: %s", c.Paths.StateDir)
	}
	if len(c.Subscriptions.Sources) != 1 || c.Subscriptions.Sources[0].ID != "provider_a" {
		t.Fatalf("source parse failed")
	}
}
func TestUnknownFieldRejected(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	s := strings.Replace(validYAML, "schema_version: 1", "schema_version: 1\nunknown_root: true", 1)
	_ = os.WriteFile(p, []byte(s), 0600)
	if _, e := Load(p); e == nil {
		t.Fatal("expected unknown field error")
	}
}
func TestByteSizeAndDuration(t *testing.T) {
	v, e := ParseByteSize("1.5MiB")
	if e != nil || v != 1572864 {
		t.Fatalf("got %d %v", v, e)
	}
	if _, e = ParseByteSize("wat"); e == nil {
		t.Fatal("expected error")
	}
}
func TestDuplicateKeyRejected(t *testing.T) {
	_, e := parseYAMLSubset([]byte("a: 1\na: 2\n"))
	if e == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestYAMLSubsetRejectsUnsupportedSyntax(t *testing.T) {
	tests := map[string]string{
		"document marker": "---\na: 1\n",
		"anchor":          "a: &shared value\n",
		"alias":           "a: &shared value\nb: *shared\n",
		"merge key":       "a:\n  <<: {}\n",
		"explicit tag":    "a: !!str value\n",
		"literal block":   "a: |\n  value\n",
		"folded block":    "a: >\n  value\n",
		"flow mapping":    "a: {b: value}\n",
		"odd indentation": "a:\n   b: value\n",
		"tab indentation": "a:\n\tb: value\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseYAMLSubset([]byte(input)); err == nil {
				t.Fatalf("unsupported YAML syntax accepted: %q", input)
			}
		})
	}
}

func TestYAMLSubsetKeepsPlainTimestampLikeValuesAsStrings(t *testing.T) {
	value, err := parseYAMLSubset([]byte("date: 2026-10-05\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := value.(map[string]any)["date"].(string)
	if !ok || got != "2026-10-05" {
		t.Fatalf("date = %#v", value)
	}
}

func TestShippedControllerTemplatesUseRepositoryDiscovery(t *testing.T) {
	for _, name := range []string{"keenetic.yaml", "openwrt.yaml", "linux-systemd.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "configs", name))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Update.GitHubRepository == "" {
				t.Fatal("shipped update template must select a GitHub repository")
			}
			if cfg.Update.ManifestURL != "" || cfg.Update.SignatureURL != "" {
				t.Fatalf("repository discovery must not ship redundant pinned manifest URLs: manifest=%q signature=%q", cfg.Update.ManifestURL, cfg.Update.SignatureURL)
			}
		})
	}
}
