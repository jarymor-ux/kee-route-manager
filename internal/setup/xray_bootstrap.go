package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteXrayBootstrap creates a fresh, private Xray configuration directory.
// Existing paths, including empty directories and dangling symlinks, are refused.
// The bootstrap outbound deliberately blocks traffic until the daemon explicitly
// selects a subscription node; managed API and routing fragments belong to it.
func WriteXrayBootstrap(dir string) error {
	dir = filepath.Clean(dir)
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("refuse existing Xray configuration path %q", dir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect Xray configuration path: %w", err)
	}

	stage, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+"-krm-bootstrap-")
	if err != nil {
		return fmt.Errorf("stage Xray bootstrap: %w", err)
	}
	stageInfo, err := os.Lstat(stage)
	if err != nil {
		return fmt.Errorf("inspect Xray bootstrap stage: %w", err)
	}
	defer func() {
		if sameBootstrapPath(stage, stageInfo) {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, 0700); err != nil {
		return fmt.Errorf("protect Xray bootstrap stage: %w", err)
	}

	files := xrayBootstrapFiles()
	identities := make(map[string]os.FileInfo, len(files))
	for _, file := range files {
		path := filepath.Join(stage, file.name)
		if err := writeBootstrapFile(path, file.json+"\n"); err != nil {
			return fmt.Errorf("stage Xray bootstrap %s: %w", file.name, err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect Xray bootstrap %s: %w", file.name, err)
		}
		identities[file.name] = info
	}

	// Rename can replace an existing empty directory. Reserve the destination
	// exclusively instead, then publish complete staged files with no-replace
	// hard links on the same filesystem. No Xray process should use this fresh
	// directory before the caller receives success.
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create fresh Xray configuration directory: %w", err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect fresh Xray configuration directory: %w", err)
	}
	committed := false
	defer func() {
		if committed || !sameBootstrapPath(dir, dirInfo) {
			return
		}
		for name, info := range identities {
			path := filepath.Join(dir, name)
			if sameBootstrapPath(path, info) {
				_ = os.Remove(path)
			}
		}
		// Remove only an empty owned directory, preserving unexpected files.
		_ = os.Remove(dir)
	}()
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("protect Xray configuration directory: %w", err)
	}
	for _, file := range files {
		if !sameBootstrapPath(dir, dirInfo) {
			return fmt.Errorf("xray configuration directory changed during publication")
		}
		if err := os.Link(filepath.Join(stage, file.name), filepath.Join(dir, file.name)); err != nil {
			return fmt.Errorf("publish Xray bootstrap %s: %w", file.name, err)
		}
	}
	committed = true
	return nil
}

func sameBootstrapPath(path string, info os.FileInfo) bool {
	current, err := os.Lstat(path)
	return err == nil && os.SameFile(info, current)
}

func writeBootstrapFile(path, data string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func xrayBootstrapFiles() []struct{ name, json string } {
	return []struct{ name, json string }{
		{"01_log.json", `{"log":{"loglevel":"warn"}}`},
		{"02_dns.json", `{"dns":{}}`},
		{"03_inbounds.json", `{"inbounds":[
  {"tag":"redirect","port":61219,"protocol":"dokodemo-door","settings":{"network":"tcp","followRedirect":true},"sniffing":{"enabled":true,"destOverride":["http","tls"],"routeOnly":true},"streamSettings":{"sockopt":{"tproxy":"redirect"}}},
  {"tag":"tproxy","port":61219,"protocol":"dokodemo-door","settings":{"network":"udp","followRedirect":true},"sniffing":{"enabled":true,"destOverride":["http","tls"],"routeOnly":true},"streamSettings":{"sockopt":{"tproxy":"tproxy"}}}
]}`},
		{"04_outbounds.json", `{"outbounds":[
  {"tag":"krm-bootstrap","protocol":"blackhole","settings":{}},
  {"tag":"direct","protocol":"freedom","settings":{}},
  {"tag":"block","protocol":"blackhole","settings":{}}
]}`},
		{"05_routing.json", `{"routing":{"rules":[
  {"type":"field","inboundTag":["redirect","tproxy"],"domain":["domain:ru","ext:geosite_v2fly.dat:category-ru"],"outboundTag":"direct"},
  {"type":"field","inboundTag":["redirect","tproxy"],"network":"tcp,udp","outboundTag":"krm-bootstrap"}
]}}`},
		{"06_policy.json", `{"policy":{"levels":{"0":{"uplinkOnly":0,"downlinkOnly":0}}}}`},
	}
}
