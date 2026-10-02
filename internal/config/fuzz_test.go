package config

import (
	"encoding/json"
	"testing"
)

func FuzzYAMLSubset(f *testing.F) {
	for _, s := range []string{validYAML, "a: 1\na: 2\n", "a: [true, false]\n", "a:\n  - name: item\n    b: 2\n", "{\"schema_version\":1}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 8192 {
			t.Skip()
		}
		b, err := yamlSubsetToJSON([]byte(raw))
		if err == nil && !json.Valid(b) {
			t.Fatal("parser produced invalid JSON")
		}
	})
}
func FuzzByteSize(f *testing.F) {
	for _, s := range []string{"1MiB", "NaN", "Inf", "9223372036854775808", "-1KiB", "0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 128 {
			t.Skip()
		}
		n, err := ParseByteSize(s)
		if err == nil && n < 0 {
			t.Fatal("negative byte size accepted")
		}
	})
}
