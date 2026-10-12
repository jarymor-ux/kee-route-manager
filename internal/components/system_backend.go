package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

var versionRegexp = regexp.MustCompile(`[vV]?([0-9]+(?:\.[0-9]+){1,3}(?:-[a-zA-Z0-9.]+)?)`)

// SystemBackend reads only allowlisted runtime binaries and trusted release metadata.
// Installing binaries is intentionally unsupported until signatures and rollback are implemented.
type SystemBackend struct {
	cfg    config.Config
	client *http.Client
}

func NewSystemBackend(c config.Config, client *http.Client) *SystemBackend {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &SystemBackend{cfg: c, client: client}
}
func (b *SystemBackend) binary(id ID) string {
	switch id {
	case Xray:
		return b.cfg.Xray.Binary
	case XKeen:
		return b.cfg.Platform.Keenetic.XKeenBinary
	case AmneziaWG:
		if b.cfg.Platform.Kind == "keenetic" {
			return "/opt/bin/amneziawg-go"
		}
		if b.cfg.Platform.Kind == "openwrt" {
			return "/usr/bin/amneziawg-go"
		}
		return "/usr/local/bin/amneziawg-go"
	default:
		return ""
	}
}

func awgToolsAvailable(lookup func(string) (string, error)) bool {
	for _, binary := range []string{"amneziawg-go", "awg", "awg-quick", "ip", "bash"} {
		if _, err := lookup(binary); err != nil {
			return false
		}
	}
	return true
}
func (b *SystemBackend) Installed(ctx context.Context, id ID) (string, error) {
	if id == AmneziaWG && !awgToolsAvailable(exec.LookPath) {
		return "", nil
	}
	binary := b.binary(id)
	if binary == "" || !filepath.IsAbs(binary) {
		return "", errors.New("unknown runtime executable")
	}
	info, err := os.Stat(binary)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("runtime binary is not an executable regular file")
	}
	arg := "version"
	if id == XKeen {
		arg = "-v"
	}
	if id == AmneziaWG {
		arg = "--version"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, binary, arg).CombinedOutput()
	if err != nil {
		return "present", nil
	}
	found := versionRegexp.FindStringSubmatch(string(output))
	if len(found) < 2 {
		return "present", nil
	}
	return found[1], nil
}

func (b *SystemBackend) Latest(ctx context.Context, id ID) (string, error) {
	if id == AmneziaWG {
		return b.latestAWGTag(ctx)
	}
	var path string
	switch id {
	case Xray:
		path = "/repos/XTLS/Xray-core/releases/latest"
	case XKeen:
		path = "/repos/Corvus-Malus/XKeen/releases/latest"
	default:
		return "", errors.New("unsupported component")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Kee-Route-Manager")
	res, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream release lookup HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 16*1024))
	if err != nil {
		return "", err
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err = json.Unmarshal(data, &release); err != nil {
		return "", err
	}
	version := strings.TrimPrefix(release.Tag, "v")
	if !versionRegexp.MatchString(version) || len(version) > 80 {
		return "", errors.New("invalid upstream version")
	}
	return version, nil
}

// AWG userspace publishes source Git tags but no GitHub Releases. Discovery
// reports source version metadata only; it must never trigger binary installation.
var stableAWGTagPattern = regexp.MustCompile(`^v([0-9]+(?:\.[0-9]+){1,3})$`)

func newerAWGTag(candidate, existing string) bool {
	for _, part := range strings.Split(candidate, ".") {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	if existing == "" {
		return true
	}
	a, b := strings.Split(candidate, "."), strings.Split(existing, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		var av, bv uint64
		var err error
		if i < len(a) {
			av, err = strconv.ParseUint(a[i], 10, 32)
			if err != nil {
				return false
			}
		}
		if i < len(b) {
			bv, err = strconv.ParseUint(b[i], 10, 32)
			if err != nil {
				return false
			}
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

func (b *SystemBackend) latestAWGTag(ctx context.Context) (string, error) {
	const pageSize = 100
	const maxPages = 10
	var latest string
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/amnezia-vpn/amneziawg-go/git/refs/tags?per_page=%d&page=%d", pageSize, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "Kee-Route-Manager")
		res, err := b.client.Do(req)
		if err != nil {
			return "", err
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return "", fmt.Errorf("AWG upstream tag lookup HTTP %d", res.StatusCode)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 128*1024+1))
		closeErr := res.Body.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if len(raw) > 128*1024 {
			return "", errors.New("AWG upstream tag list oversized")
		}
		var refs []struct {
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(raw, &refs); err != nil {
			return "", fmt.Errorf("invalid AWG tag response: %w", err)
		}
		if len(refs) > pageSize {
			return "", errors.New("AWG upstream tag page exceeds limit")
		}
		for _, ref := range refs {
			if !strings.HasPrefix(ref.Ref, "refs/tags/") {
				continue
			}
			tag := strings.TrimPrefix(ref.Ref, "refs/tags/")
			match := stableAWGTagPattern.FindStringSubmatch(tag)
			if len(match) != 2 {
				continue
			}
			if newerAWGTag(match[1], latest) {
				latest = match[1]
			}
		}
		if len(refs) < pageSize {
			if latest == "" {
				return "", errors.New("AWG upstream has no stable version tags")
			}
			return latest, nil
		}
	}
	return "", errors.New("AWG upstream tag pagination limit exceeded")
}

func (*SystemBackend) Install(context.Context, ID, string) error {
	return errors.New("automatic runtime installation unavailable: verified installers and rollback not yet implemented")
}
