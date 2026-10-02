package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		die("usage: krm-release-tool keygen|manifest|sign")
	}
	var e error
	switch os.Args[1] {
	case "keygen":
		e = keygen(os.Args[2:])
	case "manifest":
		e = manifest(os.Args[2:])
	case "sign":
		e = sign(os.Args[2:])
	default:
		e = fmt.Errorf("unknown command")
	}
	if e != nil {
		die(e.Error())
	}
}
func keygen(args []string) error {
	f := flag.NewFlagSet("keygen", flag.ContinueOnError)
	pubPath := f.String("public", "release-public.key", "public key output")
	privPath := f.String("private", "release-private.key", "private key output")
	if e := f.Parse(args); e != nil {
		return e
	}
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	if e = writeExclusive(*privPath, []byte(base64.RawStdEncoding.EncodeToString(priv)+"\n"), 0600); e != nil {
		return e
	}
	return writeExclusive(*pubPath, []byte(base64.RawStdEncoding.EncodeToString(pub)+"\n"), 0644)
}
func manifest(args []string) error {
	f := flag.NewFlagSet("manifest", flag.ContinueOnError)
	version := f.String("version", "", "release version")
	channel := f.String("channel", "rc", "channel")
	baseURL := f.String("base-url", "", "asset base URL")
	dist := f.String("dist", "dist", "binary directory")
	out := f.String("out", "manifest-rc.json", "manifest path")
	private := f.String("private", "", "optional signing key")
	signature := f.String("signature", "", "signature output")
	if e := f.Parse(args); e != nil {
		return e
	}
	base, err := url.Parse(*baseURL)
	if *version == "" || err != nil || base.Scheme != "https" || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("--version and an HTTPS --base-url required")
	}
	entries, e := os.ReadDir(*dist)
	if e != nil {
		return e
	}
	assets := []update.Asset{}
	for _, x := range entries {
		if x.IsDir() || strings.HasPrefix(x.Name(), "manifest-") || strings.HasPrefix(x.Name(), "SHA256SUMS") {
			continue
		}
		if x.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink asset rejected")
		}
		name := x.Name()
		if strings.ContainsAny(name, " \t\r\n\\") {
			return fmt.Errorf("unsafe asset name")
		}
		component, arch, goarm := "file", "", ""
		for _, kind := range []string{"kee-route-managerd", "kee-route-manager-ui", "kee-route-managerctl", "krm-release-tool"} {
			prefix := kind + "-linux-"
			if strings.HasPrefix(name, prefix) {
				var ok bool
				arch, goarm, ok = parseName("kee-route-manager-linux-" + strings.TrimPrefix(name, prefix))
				if !ok {
					return fmt.Errorf("invalid binary asset architecture")
				}
				component = map[string]string{"kee-route-managerd": "daemon", "kee-route-manager-ui": "ui", "kee-route-managerctl": "ctl", "krm-release-tool": "release-tool"}[kind]
			}
		}
		h, size, e := hashFile(filepath.Join(*dist, name))
		if e != nil {
			return e
		}
		asset := update.Asset{Name: name, Component: component, Arch: arch, GOARM: goarm, URL: strings.TrimRight(*baseURL, "/") + "/" + name, SHA256: h, Size: size}
		if arch != "" {
			asset.OS = "linux"
		}
		assets = append(assets, asset)
	}
	if len(assets) == 0 {
		return fmt.Errorf("no release binaries found")
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	m := update.Manifest{SchemaVersion: 1, Version: *version, Channel: *channel, PublishedAt: time.Now().UTC(), MinConfigSchema: 1, Assets: assets}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if e = os.WriteFile(*out, b, 0644); e != nil {
		return e
	}
	if *private != "" {
		sig := *signature
		if sig == "" {
			sig = *out + ".sig"
		}
		return signFiles(*private, *out, sig)
	}
	return nil
}
func sign(args []string) error {
	f := flag.NewFlagSet("sign", flag.ContinueOnError)
	private := f.String("private", "", "private key")
	input := f.String("input", "", "input file")
	out := f.String("out", "", "signature output")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *private == "" || *input == "" || *out == "" {
		return fmt.Errorf("--private --input --out required")
	}
	return signFiles(*private, *input, *out)
}
func signFiles(keyPath, input, out string) error {
	k, e := os.ReadFile(keyPath)
	if e != nil {
		return e
	}
	raw, e := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(k)))
	if e != nil || len(raw) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid Ed25519 private key")
	}
	b, e := os.ReadFile(input)
	if e != nil {
		return e
	}
	sig := ed25519.Sign(ed25519.PrivateKey(raw), b)
	return os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0644)
}
func parseName(n string) (string, string, bool) {
	s := strings.TrimPrefix(n, "kee-route-manager-linux-")
	switch s {
	case "amd64":
		return "amd64", "", true
	case "arm64":
		return "arm64", "", true
	case "armv7":
		return "arm", "7", true
	case "mipsle":
		return "mipsle", "", true
	}
	return "", "", false
}
func hashFile(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func writeExclusive(path string, b []byte, mode os.FileMode) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	return e
}
func die(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(1) }
