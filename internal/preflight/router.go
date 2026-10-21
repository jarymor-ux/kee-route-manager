package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func executable(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func validateXray(ctx context.Context, c config.Config) error {
	limit := c.Platform.CommandTimeout.Duration
	if limit <= 0 || limit > 30*time.Second {
		limit = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Xray.Binary, "run", "-test", "-confdir", c.Xray.ConfigDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+c.Xray.AssetDir, "xray.location.asset="+c.Xray.AssetDir)
	// Xray errors may include full subscription credentials or UUIDs. Never
	// buffer or echo subprocess output in the installer/preflight report.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func readRCI(ctx context.Context, rawURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var value map[string]json.RawMessage
	const limit = 256 << 10
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&value) != nil || len(value) == 0 {
		return false
	}
	if _, bad := value["status"]; bad {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

// Check only strict JSON files that Xray reads, without displaying their data.
// Limits bound this auxiliary check before invoking Xray's own validator.
func checkRoutes(c config.Config) error {
	entries, err := os.ReadDir(c.Xray.ConfigDir)
	if err != nil {
		return fmt.Errorf("configuration directory is unreadable")
	}
	inbounds := map[string]bool{}
	selected := map[string]bool{}
	// These four files are completely replaced by the candidate manager. Like
	// xray.checkRoutingFiles, treat only the configured adoption file as the
	// authoritative user routing section, and refuse other user sections.
	managed := map[string]bool{}
	for _, name := range []string{"00_90_kee_route_manager_api.json", "03_90_kee_route_manager_inbounds.json", "04_90_kee_route_manager_outbounds.json", "05_90_kee_route_manager_routing.json"} {
		managed[filepath.Join(c.Xray.ManagedDir, name)] = true
	}
	base := filepath.Clean(c.Xray.BaseRoutingFile)
	foundBase := false
	var total int64
	files := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files++
		if files > 128 {
			return fmt.Errorf("more than 128 configuration files")
		}
		path := filepath.Join(c.Xray.ConfigDir, entry.Name())
		// Opening before fstat must not block if a path is replaced by a FIFO.
		// Regular-file symlinks remain usable, as they are by Xray itself.
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("configuration file is unreadable")
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
			f.Close()
			return fmt.Errorf("configuration file is not regular or exceeds 4 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		f.Close()
		if err != nil || len(data) > 4<<20 {
			return fmt.Errorf("configuration file is unreadable or exceeds 4 MiB")
		}
		total += int64(len(data))
		if total > 16<<20 {
			return fmt.Errorf("configuration exceeds 16 MiB")
		}
		var doc struct {
			Inbounds []struct {
				Tag string `json:"tag"`
			} `json:"inbounds"`
			Routing *struct {
				Rules []struct {
					InboundTag  []string `json:"inboundTag"`
					OutboundTag string   `json:"outboundTag"`
					BalancerTag string   `json:"balancerTag"`
				} `json:"rules"`
			} `json:"routing"`
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		err = dec.Decode(&doc)
		if err == nil && dec.Decode(new(any)) != io.EOF {
			err = fmt.Errorf("trailing JSON")
		}
		if err != nil {
			return fmt.Errorf("configuration must be strict JSON")
		}
		for _, in := range doc.Inbounds {
			inbounds[in.Tag] = true
		}
		if path != base {
			if doc.Routing != nil && !managed[path] {
				return fmt.Errorf("routing section outside configured base file")
			}
			continue
		}
		if doc.Routing == nil {
			return fmt.Errorf("configured base file has no routing object")
		}
		foundBase = true
		for _, rule := range doc.Routing.Rules {
			if rule.BalancerTag != "" {
				continue
			}
			for _, tag := range rule.InboundTag {
				selected[tag+"\x00"+rule.OutboundTag] = true
			}
		}
	}
	if !foundBase {
		return fmt.Errorf("configured base routing file is missing")
	}
	for _, tag := range c.Xray.Route.InboundTags {
		if !inbounds[tag] {
			return fmt.Errorf("selected inbound tag is absent")
		}
		matched := false
		for _, out := range c.Xray.Route.ReplaceOutboundTags {
			matched = matched || selected[tag+"\x00"+out]
		}
		if !matched {
			return fmt.Errorf("no selected inbound/outbound routing rule exists")
		}
	}
	return nil
}
