package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

var componentFiles = map[string]string{
	"daemon": "kee-route-managerd",
	"ui":     "kee-route-manager-ui",
	"ctl":    "kee-route-managerctl",
}

var assetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

type releasePlatform struct{ os, arch, arm string }

func nativePlatform() (releasePlatform, error) {
	p := releasePlatform{os: runtime.GOOS, arch: runtime.GOARCH}
	if p.arch == "arm" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "GOARM" {
					p.arm = strings.SplitN(setting.Value, ",", 2)[0]
				}
			}
		}
		if p.arm != "5" && p.arm != "6" && p.arm != "7" {
			return p, fmt.Errorf("native ARM build level unavailable")
		}
	}
	return p, nil
}

func (p releasePlatform) matches(a Asset) bool {
	return a.OS == p.os && a.Arch == p.arch && a.GOARM == p.arm
}

func selectAsset(xs []Asset) (Asset, error) {
	p, err := nativePlatform()
	if err != nil {
		return Asset{}, err
	}
	var selected Asset
	found := false
	for _, a := range xs {
		if (a.Component == "" || a.Component == "daemon") && p.matches(a) {
			if found {
				return Asset{}, fmt.Errorf("duplicate native daemon asset")
			}
			selected, found = a, true
		}
	}
	if !found {
		return Asset{}, fmt.Errorf("no asset for %s/%s", p.os, p.arch)
	}
	return selected, nil
}

func authenticateManifest(data, signature []byte, publicKey, channel string) (Manifest, error) {
	var manifest Manifest
	if len(data) > 4<<20 || len(signature) > 4096 {
		return manifest, fmt.Errorf("signed metadata too large")
	}
	key, err := decodeKey(publicKey, ed25519.PublicKeySize)
	if err != nil {
		return manifest, fmt.Errorf("public key: %w", err)
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return manifest, err
	}
	if !ed25519.Verify(ed25519.PublicKey(key), data, sig) {
		return manifest, fmt.Errorf("manifest signature invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return manifest, fmt.Errorf("unexpected trailing manifest data")
	}
	if manifest.SchemaVersion != 1 {
		return manifest, fmt.Errorf("unsupported manifest schema")
	}
	if manifest.Channel != channel || (channel != "rc" && channel != "stable") {
		return manifest, fmt.Errorf("manifest channel %q does not match %q", manifest.Channel, channel)
	}
	if manifest.MinConfigSchema > config.SchemaVersion {
		return manifest, fmt.Errorf("release requires config schema %d", manifest.MinConfigSchema)
	}
	if _, err = parseSemVer(manifest.Version); err != nil {
		return manifest, fmt.Errorf("invalid release version: %w", err)
	}
	if channel == "stable" && strings.Contains(strings.SplitN(manifest.Version, "+", 2)[0], "-") {
		return manifest, fmt.Errorf("stable channel cannot contain prerelease")
	}
	return manifest, nil
}

func validateAsset(a Asset, named bool) error {
	if a.Size < 1 || a.Size > 200<<20 {
		return fmt.Errorf("invalid asset size")
	}
	if len(a.SHA256) != 64 {
		return fmt.Errorf("invalid asset checksum")
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return fmt.Errorf("invalid asset checksum")
	}
	if !secureURL(a.URL) {
		return fmt.Errorf("asset URL must use HTTPS")
	}
	if named && !assetNameRE.MatchString(a.Name) {
		return fmt.Errorf("unsafe or missing asset name")
	}
	return nil
}

func bundleAssets(manifest Manifest) (map[string]Asset, error) {
	p, err := nativePlatform()
	if err != nil {
		return nil, err
	}
	return bundleAssetsFor(manifest, p)
}

func bundleAssetsFor(manifest Manifest, platform releasePlatform) (map[string]Asset, error) {
	if manifest.UpdateProtocol != 1 {
		return nil, fmt.Errorf("release does not support update protocol 1")
	}
	if manifest.SchemaVersion != 1 || manifest.MinConfigSchema != config.SchemaVersion {
		return nil, fmt.Errorf("update requires compatible schema 1 without migration")
	}
	selected := map[string]Asset{}
	names := map[string]bool{}
	githubPrefix := ""
	nonGitHub := false
	for _, asset := range manifest.Assets {
		if _, needed := componentFiles[asset.Component]; !needed || !platform.matches(asset) {
			continue
		}
		if _, exists := selected[asset.Component]; exists {
			return nil, fmt.Errorf("duplicate native %s asset", asset.Component)
		}
		if err := validateAsset(asset, true); err != nil {
			return nil, err
		}
		if names[asset.Name] {
			return nil, fmt.Errorf("duplicate component asset name")
		}
		names[asset.Name] = true
		parsed, _ := url.Parse(asset.URL)
		if strings.EqualFold(parsed.Hostname(), "github.com") {
			parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
			if parsed.Host != "github.com" || len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" || strings.TrimPrefix(parts[4], "v") != strings.TrimPrefix(manifest.Version, "v") || parts[5] != asset.Name || parsed.RawQuery != "" || parsed.ForceQuery || parsed.RawPath != "" || strings.Contains(asset.URL, "#") {
				return nil, fmt.Errorf("asset is not bound to immutable GitHub release")
			}
			prefix := strings.Join(parts[:5], "/")
			if githubPrefix != "" && githubPrefix != prefix {
				return nil, fmt.Errorf("components belong to different GitHub releases")
			}
			githubPrefix = prefix
		} else {
			nonGitHub = true
		}
		selected[asset.Component] = asset
	}
	if githubPrefix != "" && nonGitHub {
		return nil, fmt.Errorf("components belong to different release origins")
	}
	for component := range componentFiles {
		if _, ok := selected[component]; !ok {
			return nil, fmt.Errorf("missing native %s asset for %s/%s", component, platform.os, platform.arch)
		}
	}
	return selected, nil
}
