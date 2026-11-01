package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// These fixtures describe the generated configuration consumed by Xray, rather
// than rebuilding expectations with the generator's own configuration values.
var bootstrapJSONFixtures = map[string]string{
	"01_log.json": `{"log":{"loglevel":"warn"}}`,
	"02_dns.json": `{"dns":{}}`,
	"03_inbounds.json": `{"inbounds":[
		{"tag":"redirect","port":61219,"protocol":"dokodemo-door","settings":{"network":"tcp","followRedirect":true},"sniffing":{"enabled":true,"destOverride":["http","tls"],"routeOnly":true},"streamSettings":{"sockopt":{"tproxy":"redirect"}}},
		{"tag":"tproxy","port":61219,"protocol":"dokodemo-door","settings":{"network":"udp","followRedirect":true},"sniffing":{"enabled":true,"destOverride":["http","tls"],"routeOnly":true},"streamSettings":{"sockopt":{"tproxy":"tproxy"}}}
	]}`,
	"04_outbounds.json": `{"outbounds":[{"tag":"krm-bootstrap","protocol":"blackhole","settings":{}},{"tag":"direct","protocol":"freedom","settings":{}},{"tag":"block","protocol":"blackhole","settings":{}}]}`,
	"05_routing.json":   `{"routing":{"rules":[{"type":"field","inboundTag":["redirect","tproxy"],"domain":["domain:ru","ext:geosite_v2fly.dat:category-ru"],"outboundTag":"direct"},{"type":"field","inboundTag":["redirect","tproxy"],"network":"tcp,udp","outboundTag":"krm-bootstrap"}]}}`,
	"06_policy.json":    `{"policy":{"levels":{"0":{"uplinkOnly":0,"downlinkOnly":0}}}}`,
}

func TestWriteXrayBootstrapPrivateStrictJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "configs")
	if err := WriteXrayBootstrap(dir); err != nil {
		t.Fatal(err)
	}
	assertBootstrapFiles(t, dir)
}

func assertBootstrapFiles(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions = %o, want 700", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(bootstrapJSONFixtures) {
		t.Fatalf("generated files = %v, want exactly six base fragments without managed API fragments", entries)
	}
	for name, fixture := range bootstrapJSONFixtures {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("%s is not strict JSON: %v", name, err)
		}
		if err := json.Unmarshal([]byte(fixture), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %s, want %s", name, data, fixture)
		}
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %v, want regular 600", name, info.Mode())
		}
	}
}

func TestWriteXrayBootstrapPreservesExistingPaths(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "directory", "symlink-directory", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "configs")
			sentinel := filepath.Join(parent, "sentinel")
			if err := os.WriteFile(sentinel, []byte("operator-owned"), 0644); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				if err := os.WriteFile(dir, []byte("existing-config"), 0644); err != nil {
					t.Fatal(err)
				}
			case "empty-directory", "directory":
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte("operator-routing"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink-directory":
				if err := os.Symlink(parent, dir); err != nil {
					t.Fatal(err)
				}
			case "dangling-symlink":
				if err := os.Symlink(filepath.Join(parent, "absent"), dir); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteXrayBootstrap(dir); err == nil {
				t.Fatal("expected refusal of existing path")
			}
			after, err := os.Lstat(dir)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("existing path changed: before=%v after=%v err=%v", before, after, err)
			}
			if kind == "file" {
				assertBootstrapFileContents(t, dir, "existing-config")
			}
			if kind == "directory" {
				assertBootstrapFileContents(t, filepath.Join(dir, "05_routing.json"), "operator-routing")
			}
			assertBootstrapFileContents(t, sentinel, "operator-owned")
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 2 {
				t.Fatalf("temporary stage leaked: %v, %v", entries, err)
			}
		})
	}
}

func assertBootstrapFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}

func TestWriteXrayBootstrapConcurrentCreationHasOneOwner(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "configs")
	var wg sync.WaitGroup
	results := make(chan error, 12)
	start := make(chan struct{})
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- WriteXrayBootstrap(dir)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("successful writers = %d, want one", winners)
	}
	assertBootstrapFiles(t, dir)
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary stages leaked: %v, %v", entries, err)
	}
}

func TestWriteXrayBootstrapMissingParentLeavesNoPartialTarget(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "missing", "configs")
	if err := WriteXrayBootstrap(dir); err == nil {
		t.Fatal("expected missing-parent failure")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failure left partial files: %v, %v", entries, err)
	}
}
