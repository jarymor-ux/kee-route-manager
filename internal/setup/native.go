package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// InitNativeInstallerConfig prepares a clean native configuration offline. It
// never installs a dependency, starts a service or modifies router policies.
func InitNativeInstallerConfig(in io.Reader, out io.Writer, outputPath, uiPath, tlsDir, nativeDir string, platform Platform) error {
	if platform != PlatformKeenetic || !filepath.IsAbs(nativeDir) || nativeDir == "/" {
		return fmt.Errorf("clean native setup requires Keenetic/Netcraze and a new absolute staging directory")
	}
	if (uiPath == "") != (tlsDir == "") {
		return fmt.Errorf("local UI requires both UI output and TLS directory")
	}
	paths := []string{outputPath, nativeDir}
	if uiPath != "" {
		paths = append(paths, uiPath, tlsDir)
	}
	for i, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("installer outputs must be absolute")
		}
		for _, other := range paths[:i] {
			if filepath.Clean(other) == filepath.Clean(path) {
				return fmt.Errorf("installer outputs must be distinct")
			}
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("installer output already exists or cannot be inspected")
		}
	}
	return initConfig(in, out, outputPath, false, platform, uiPath, tlsDir, nativeDir)
}

func writeNativeConfig(dir string) (resultErr error) {
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("reserve private native staging: %w", err)
	}
	identity, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			if current, err := os.Lstat(dir); err == nil && os.SameFile(current, identity) {
				_ = os.RemoveAll(dir)
			}
		}
	}()
	configs := filepath.Join(dir, "configs")
	if err := os.Mkdir(configs, 0700); err != nil {
		return err
	}
	files := map[string]string{
		"03_inbounds.json":  `{"inbounds":[{"tag":"redirect","listen":"127.0.0.1","port":1181,"protocol":"tunnel","settings":{"allowedNetwork":"tcp","followRedirect":true}},{"tag":"tproxy","listen":"127.0.0.1","port":1181,"protocol":"tunnel","settings":{"allowedNetwork":"udp","followRedirect":true},"streamSettings":{"sockopt":{"tproxy":"tproxy"}}}]}`,
		"04_outbounds.json": `{"outbounds":[{"tag":"bootstrap-blocked","protocol":"blackhole","settings":{}}]}`,
		"05_routing.json":   `{"routing":{"rules":[{"inboundTag":["redirect","tproxy"],"outboundTag":"bootstrap-blocked"}]}}`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(configs, name), []byte(data+"\n"), 0600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "xkeen.json"), []byte("{}\n"), 0600); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(configs, "03_inbounds.json"), filepath.Join(configs, "04_outbounds.json"), filepath.Join(configs, "05_routing.json"), filepath.Join(dir, "xkeen.json")} {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := syncDirectory(configs); err != nil {
		return err
	}
	return syncDirectory(dir)
}
