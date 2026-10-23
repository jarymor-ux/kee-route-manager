// Package preflight performs bounded, read-only installation checks. It never
// constructs a controller, starts Xray listeners or changes router settings.
package preflight

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

const MiB = 1 << 20

type Options struct {
	Mode       string
	StageDir   string
	ReleaseDir string
	// ResourcesOnly gates provisioning before native components exist. The
	// actual installer must still perform the complete preflight afterwards.
	ResourcesOnly bool
}
type Report struct {
	OK                   bool            `json:"ok"`
	Architecture         string          `json:"architecture"`
	MemoryTotalBytes     uint64          `json:"memory_total_bytes"`
	MemoryAvailableBytes uint64          `json:"memory_available_bytes"`
	OptAvailableBytes    uint64          `json:"opt_available_bytes"`
	StageAvailableBytes  uint64          `json:"stage_available_bytes"`
	OptRequiredBytes     uint64          `json:"opt_required_bytes"`
	Recommendation       string          `json:"recommendation"`
	RCIReadAvailable     map[string]bool `json:"rci_read_available,omitempty"`
	Warnings             []string        `json:"warnings"`
	Errors               []string        `json:"errors"`
}
type memory struct {
	Total, Available uint64
	FreeOnly         bool
}
type probes struct {
	memory     func() (memory, error)
	space      func(string) (uint64, error)
	dependency func(string) error
	validate   func(context.Context, config.Config) error
	routes     func(config.Config) error
	rci        func(context.Context, string) bool
}

func Run(ctx context.Context, c config.Config, o Options) Report {
	return run(ctx, c, o, probes{memory: readMemory, space: freeSpace, dependency: executable, validate: validateXray, routes: checkRoutes, rci: readRCI})
}
func run(ctx context.Context, c config.Config, o Options, p probes) Report {
	r := Report{Architecture: runtime.GOOS + "/" + runtime.GOARCH, Warnings: []string{}, Errors: []string{}, OptRequiredBytes: 32 * MiB}
	add := func(message string) { r.Errors = append(r.Errors, message) }
	if o.Mode != "core" && o.Mode != "local-ui" && o.Mode != "ui" {
		add("mode must be core, local-ui or ui")
		return r
	}
	ui := c.Instance.Role == "ui" || c.Instance.Role == "ui-proxy"
	if ui != (o.Mode == "ui") {
		add("configuration role does not match installation mode")
		return r
	}
	if err := ctx.Err(); err != nil {
		add("preflight cancelled")
		return r
	}
	if o.StageDir == "" {
		o.StageDir = os.TempDir()
	}
	if o.ReleaseDir != "" {
		size, err := selectedSize(o.ReleaseDir, o.Mode)
		if err != nil {
			add(err.Error())
			return r
		}
		// Retain room for the installed slot plus one trial/rollback slot and
		// a provisional 32 MiB state reserve. This is not an Xray RAM estimate.
		r.OptRequiredBytes += 2 * size
	}
	m, err := p.memory()
	if err != nil {
		add("cannot determine memory from /proc/meminfo")
	} else {
		r.MemoryTotalBytes = m.Total
		r.MemoryAvailableBytes = m.Available
		if m.FreeOnly {
			r.Warnings = append(r.Warnings, "kernel has no MemAvailable; using conservative MemFree without reclaimable cache")
		}
		// Operational guard, not a supported-model threshold. The validation
		// subprocess needs headroom; hardware acceptance remains mandatory.
		if m.Available < 32*MiB {
			add("less than 32 MiB available RAM: stop optional work and recheck before Xray validation (provisional safety reserve)")
		}
		if m.Total <= 256*MiB || m.Available < 128*MiB {
			r.Recommendation = "conservative-starting-point"
			r.Warnings = append(r.Warnings, "unverified starting point: core-only, pool.size=2, benchmark.batch_size=2, latency_workers=2, finalists=2, speed.enabled=false, subscriptions.max_sources=2, max_nodes=50, max_nodes_per_source=50, max_response_bytes=512KiB; apply explicitly, measure on hardware; no defaults changed")
		} else {
			r.Recommendation = "measure-before-enabling-speed"
		}
	}
	r.OptAvailableBytes, err = p.space("/opt")
	if err != nil {
		add("Entware /opt is unavailable")
	} else if r.OptAvailableBytes < r.OptRequiredBytes {
		add("insufficient /opt space for installation, trial slot and provisional state reserve")
	}
	r.StageAvailableBytes, err = p.space(o.StageDir)
	if err != nil {
		add("cannot determine staging free space")
	} else if r.StageAvailableBytes < 32*MiB {
		add("less than 32 MiB staging space after extraction")
	}
	r.Warnings = append(r.Warnings, "preflight does not certify hardware support, traffic capacity or fail-open; measure KeeneticOS + Xray + KRM peak memory and verify the real client route")
	if len(r.Errors) > 0 {
		return r
	}
	if !ui && !o.ResourcesOnly {
		for _, name := range []string{"/opt/bin/opkg", c.Xray.Binary, c.Platform.Keenetic.XKeenBinary, c.Platform.Keenetic.NDMCBinary} {
			if err := p.dependency(name); err != nil {
				add("required executable unavailable: " + name)
			}
		}
		if len(r.Errors) > 0 {
			return r
		}
		if err := p.routes(c); err != nil {
			add("Xray route selection: " + err.Error())
			return r
		}
		if err := p.validate(ctx, c); err != nil {
			add("Xray offline validation failed or timed out; inspect it privately with xray run -test -confdir (output withheld to protect secrets)")
			return r
		}
		r.RCIReadAvailable = make(map[string]bool)
		for _, path := range []string{"show/system", "show/ip/policy"} {
			r.RCIReadAvailable[path] = p.rci(ctx, strings.TrimRight(c.Platform.Keenetic.RCIBaseURL, "/")+"/"+path)
			if !r.RCIReadAvailable[path] {
				r.Warnings = append(r.Warnings, "RCI GET "+path+" unavailable; router telemetry/policy capability requires a device check")
			}
		}
	}
	if ctx.Err() != nil {
		add("preflight cancelled")
		return r
	}
	r.OK = true
	return r
}

func selectedSize(dir, mode string) (uint64, error) {
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv7"
	}
	names := []string{"kee-route-manager-ui"}
	if mode != "ui" {
		names = []string{"kee-route-managerd", "kee-route-managerctl", "kee-route-manager-launcher"}
		if mode == "local-ui" {
			names = append(names, "kee-route-manager-ui")
		}
	}
	var total uint64
	for _, name := range names {
		st, err := os.Stat(filepath.Join(dir, "dist", name+"-linux-"+arch))
		if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 {
			return 0, fmt.Errorf("release component missing or invalid: %s", name)
		}
		if uint64(st.Size()) > (math.MaxUint64-32*MiB)/16 {
			return 0, fmt.Errorf("release component size exceeds accounting limit")
		}
		total += uint64(st.Size())
	}
	return total, nil
}

func readMemory() (memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memory{}, err
	}
	defer f.Close()
	return parseMemory(io.LimitReader(f, 64<<10))
}
func parseMemory(reader io.Reader) (memory, error) {
	values := map[string]uint64{}
	s := bufio.NewScanner(reader)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 0 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" && key != "MemFree" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return memory{}, fmt.Errorf("invalid memory units")
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || n > math.MaxUint64/1024 {
			return memory{}, fmt.Errorf("invalid memory size")
		}
		values[key] = n * 1024
	}
	if err := s.Err(); err != nil {
		return memory{}, err
	}
	m := memory{Total: values["MemTotal"]}
	var ok bool
	m.Available, ok = values["MemAvailable"]
	if !ok {
		m.Available = values["MemFree"]
		m.FreeOnly = true
	}
	if m.Total == 0 || m.Available > m.Total {
		return memory{}, fmt.Errorf("invalid memory totals")
	}
	return m, nil
}
