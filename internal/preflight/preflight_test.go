package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func healthyProbes() probes {
	return probes{
		memory:     func() (memory, error) { return memory{Total: 256 << 20, Available: 96 << 20}, nil },
		space:      func(string) (uint64, error) { return 512 << 20, nil },
		dependency: func(string) error { return nil },
		validate:   func(context.Context, config.Config) error { return nil },
		routes:     func(config.Config) error { return nil },
		rci:        func(context.Context, string) bool { return false },
	}
}

func TestResourcesFailBeforeExecutingXray(t *testing.T) {
	for _, problem := range []string{"memory", "opt", "stage", "dependency", "routes"} {
		t.Run(problem, func(t *testing.T) {
			p := healthyProbes()
			called := false
			p.validate = func(context.Context, config.Config) error { called = true; return nil }
			switch problem {
			case "memory":
				p.memory = func() (memory, error) { return memory{Total: 256 << 20, Available: 8 << 20}, nil }
			case "opt", "stage":
				p.space = func(path string) (uint64, error) {
					if (problem == "opt") == (path == "/opt") {
						return 1, nil
					}
					return 512 << 20, nil
				}
			case "dependency":
				p.dependency = func(string) error { return errors.New("missing") }
			case "routes":
				p.routes = func(config.Config) error { return errors.New("missing selected inbound") }
			}
			r := run(context.Background(), config.Default(), Options{Mode: "core", StageDir: "/tmp"}, p)
			if r.OK || len(r.Errors) == 0 || called {
				t.Fatalf("unsafe preflight: %+v, Xray executed=%v", r, called)
			}
		})
	}
}

func TestProfilesDoNotMutateConfigOrChangeSafety(t *testing.T) {
	c := config.Default()
	r := run(context.Background(), c, Options{Mode: "core", StageDir: "/tmp"}, healthyProbes())
	if !r.OK || r.Recommendation != "conservative-starting-point" || len(r.Warnings) == 0 {
		t.Fatalf("%+v", r)
	}
	if c.Pool.Size != 5 || c.Benchmark.BatchSize != 20 || c.Benchmark.LatencyWorkers != 8 || c.Update.AutoApply {
		t.Fatal("preflight changed existing defaults")
	}
	if !strings.Contains(strings.Join(r.Warnings, " "), "hardware") {
		t.Fatal("missing hardware qualification warning")
	}
}

func TestStandaloneUIAvoidsRouterDependencies(t *testing.T) {
	p := healthyProbes()
	p.dependency = func(string) error { t.Fatal("router dependency used for standalone UI"); return nil }
	p.routes = func(config.Config) error { t.Fatal("router routing inspected for standalone UI"); return nil }
	p.validate = func(context.Context, config.Config) error { t.Fatal("Xray started for standalone UI"); return nil }
	c := config.Default()
	c.Instance.Role = "ui"
	if r := run(context.Background(), c, Options{Mode: "ui", StageDir: "/tmp"}, p); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := run(context.Background(), c, Options{Mode: "core", StageDir: "/tmp"}, p); r.OK {
		t.Fatal("UI config accepted for core")
	}
}

func TestProvisioningResourcesCheckCannotExecuteNativeDependencies(t *testing.T) {
	p := healthyProbes()
	p.dependency = func(string) error { t.Fatal("resource check inspected a dependency"); return nil }
	p.routes = func(config.Config) error { t.Fatal("resource check inspected routes"); return nil }
	p.validate = func(context.Context, config.Config) error { t.Fatal("resource check executed Xray"); return nil }
	p.rci = func(context.Context, string) bool { t.Fatal("resource check accessed RCI"); return false }
	if r := run(context.Background(), config.Default(), Options{Mode: "core", ResourcesOnly: true}, p); !r.OK {
		t.Fatal(r)
	}
	p.memory = func() (memory, error) { return memory{Total: 128 * MiB, Available: 8 * MiB}, nil }
	if r := run(context.Background(), config.Default(), Options{Mode: "core", ResourcesOnly: true}, p); r.OK {
		t.Fatal("resource-only bypassed memory gate")
	}
}

func TestMemoryParserHandlesOldKernelsAndRejectsOverflow(t *testing.T) {
	for _, test := range []struct {
		text      string
		available uint64
		bad       bool
	}{
		{"MemTotal: 262144 kB\nMemAvailable: 65536 kB\n", 64 << 20, false},
		{"MemTotal: 262144 kB\nMemFree: 32768 kB\nCached: 100000 kB\n", 32 << 20, false},
		{"MemTotal: 18446744073709551615 kB\nMemAvailable: 1 kB\n", 0, true},
		{"MemTotal: 262144 kB\nMemAvailable: 999999 kB\n", 0, true},
		{"MemTotal: nonsense kB\n", 0, true},
	} {
		m, err := parseMemory(strings.NewReader(test.text))
		if (err != nil) != test.bad || (!test.bad && m.Available != test.available) {
			t.Fatalf("%q: %+v %v", test.text, m, err)
		}
	}
}
