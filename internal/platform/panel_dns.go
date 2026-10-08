package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type PanelDNSAdapter interface {
	EnsurePanelAlias(context.Context, string, string) error
	ReconcilePanelAlias(context.Context) error
}

var (
	ErrPanelDNSConflict    = errors.New("local panel DNS name is already assigned")
	ErrPanelDNSDrift       = errors.New("router configuration changed; local panel DNS requires reconciliation")
	ErrPanelDNSUnavailable = errors.New("local panel DNS could not be verified")
	ErrPanelDNSPending     = errors.New("local panel DNS change requires reconciliation")
)

type panelDNSIntent struct {
	Schema           int    `json:"schema"`
	Hostname         string `json:"hostname"`
	IP               string `json:"ip"`
	BaselineDigest   string `json:"baseline_normalized_digest"`
	OriginalChecksum string `json:"original_checksum"`
	Phase            string `json:"phase"`
}

func (k *keenetic) panelDNSJournal() string {
	return filepath.Join(k.cfg.Paths.StateDir, ".panel-dns-pending.json")
}
func (k *keenetic) panelDNSBackup(intent panelDNSIntent) string {
	return filepath.Join(k.cfg.Paths.StateDir, "panel-dns-backups", intent.BaselineDigest+".txt")
}

// Only a single canonical global ip host command is owned. Everything else,
// including indentation and other host aliases, remains part of the baseline.
func panelDNSNormalized(data []byte, hostname, ip string) (string, int, error) {
	var lines []string
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "!") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 3 && fields[0] == "ip" && fields[1] == "host" {
			name := fields[2]
			if unquoted, err := strconv.Unquote(name); err == nil {
				name = unquoted
			}
			if strings.EqualFold(name, hostname) {
				count++
				if count > 1 || len(fields) != 4 || line != strings.TrimLeft(line, " \t") || net.ParseIP(fields[3]) == nil || !net.ParseIP(fields[3]).Equal(net.ParseIP(ip)) {
					return "", count, ErrPanelDNSConflict
				}
				continue
			}
		}
		lines = append(lines, strings.TrimRight(line, " \t"))
	}
	return strings.Join(lines, "\n"), count, nil
}
func panelDNSDigest(normalized string) string {
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}
func panelDNSPrivateRead(path string, max int64) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil || !panelDNSOwned(st) || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > max {
		return nil, ErrPanelDNSPending
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrPanelDNSPending
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) || !panelDNSOwned(opened) || opened.Mode().Perm()&0077 != 0 {
		return nil, ErrPanelDNSPending
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, ErrPanelDNSPending
	}
	return data, nil
}

func panelDNSOwned(st os.FileInfo) bool {
	identity, ok := st.Sys().(*syscall.Stat_t)
	return ok && identity.Uid == uint32(os.Geteuid())
}
func panelDNSSyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return ErrPanelDNSUnavailable
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrPanelDNSUnavailable
	}
	return nil
}
func panelDNSWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".krm-panel-dns-*")
	if err != nil {
		return ErrPanelDNSUnavailable
	}
	name := f.Name()
	defer os.Remove(name)
	if f.Chmod(0600) != nil {
		f.Close()
		return ErrPanelDNSUnavailable
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil || closed != nil || os.Rename(name, path) != nil {
		return ErrPanelDNSUnavailable
	}
	return panelDNSSyncDir(filepath.Dir(path))
}
func (k *keenetic) panelDNSWriteIntent(intent panelDNSIntent) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return ErrPanelDNSUnavailable
	}
	return panelDNSWrite(k.panelDNSJournal(), data)
}
func (k *keenetic) panelDNSClearIntent() error {
	if err := os.Remove(k.panelDNSJournal()); err != nil && !os.IsNotExist(err) {
		return ErrPanelDNSUnavailable
	}
	return panelDNSSyncDir(k.cfg.Paths.StateDir)
}
func panelDNSValidAddress(hostname, ip string) bool {
	address := net.ParseIP(ip)
	return config.ValidPanelHostname(hostname) && address != nil && address.To4() != nil && address.IsPrivate() && address.String() == ip
}
func (k *keenetic) panelDNSReadConfig(ctx context.Context) ([]byte, error) {
	data, err := k.configFile(ctx, "running-config.txt")
	if err != nil || len(data) == 0 || len(data) >= 4<<20 {
		return nil, ErrPanelDNSUnavailable
	}
	return data, nil
}
func (k *keenetic) panelDNSObserve(ctx context.Context, intent panelDNSIntent) (int, string, string, error) {
	before, err := k.runningConfigChecksum(ctx)
	if err != nil {
		return 0, "", "", ErrPanelDNSUnavailable
	}
	data, err := k.panelDNSReadConfig(ctx)
	if err != nil {
		return 0, "", "", err
	}
	normalized, count, err := panelDNSNormalized(data, intent.Hostname, intent.IP)
	if err != nil {
		return 0, "", "", err
	}
	if panelDNSDigest(normalized) != intent.BaselineDigest {
		return 0, "", "", ErrPanelDNSDrift
	}
	current, saved, err := k.configurationChecksums(ctx)
	if err != nil {
		return 0, "", "", ErrPanelDNSUnavailable
	}
	if current != before {
		return 0, "", "", ErrPanelDNSDrift
	}
	return count, current, saved, nil
}
func (k *keenetic) EnsurePanelAlias(ctx context.Context, hostname, ip string) error {
	if !panelDNSValidAddress(hostname, ip) {
		return ErrPanelDNSUnavailable
	}
	if _, err := os.Lstat(k.panelDNSJournal()); !os.IsNotExist(err) {
		return ErrPanelDNSPending
	}
	before, err := k.runningConfigChecksum(ctx)
	if err != nil {
		return ErrPanelDNSUnavailable
	}
	baseline, err := k.panelDNSReadConfig(ctx)
	if err != nil {
		return err
	}
	normalized, count, err := panelDNSNormalized(baseline, hostname, ip)
	if err != nil {
		return err
	}
	current, saved, err := k.configurationChecksums(ctx)
	if err != nil {
		return ErrPanelDNSUnavailable
	}
	if current != before {
		return ErrPanelDNSDrift
	}
	if count == 1 {
		return nil
	}
	if current != saved {
		return ErrPanelDNSDrift
	}
	intent := panelDNSIntent{Schema: 1, Hostname: hostname, IP: ip, BaselineDigest: panelDNSDigest(normalized), OriginalChecksum: current, Phase: "prepared"}
	dir := filepath.Dir(k.panelDNSBackup(intent))
	if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return ErrPanelDNSUnavailable
	}
	st, err := os.Lstat(dir)
	if err != nil || !panelDNSOwned(st) || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return ErrPanelDNSUnavailable
	}
	if err = panelDNSWrite(k.panelDNSBackup(intent), baseline); err != nil {
		return err
	}
	if err = k.panelDNSWriteIntent(intent); err != nil {
		return err
	}
	count, current, saved, err = k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count != 0 || current != intent.OriginalChecksum || saved != intent.OriginalChecksum {
		return ErrPanelDNSDrift
	}
	_, runErr := k.r.Run(ctx, []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", "ip host " + hostname + " " + ip})
	count, current, saved, err = k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count == 0 {
		if current == intent.OriginalChecksum && saved == intent.OriginalChecksum {
			_ = k.panelDNSClearIntent()
		}
		return ErrPanelDNSUnavailable
	}
	if runErr != nil {
		return ErrPanelDNSPending
	}
	if current == intent.OriginalChecksum {
		return ErrPanelDNSDrift
	}
	intent.Phase = "added"
	if err = k.panelDNSWriteIntent(intent); err != nil {
		return err
	}
	return k.panelDNSPersist(ctx, intent)
}
func (k *keenetic) panelDNSPersist(ctx context.Context, intent panelDNSIntent) error {
	count, current, saved, err := k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrPanelDNSDrift
	}
	if current == intent.OriginalChecksum {
		return ErrPanelDNSDrift
	}
	if current == saved {
		return k.panelDNSClearIntent()
	}
	if saved != intent.OriginalChecksum || current == intent.OriginalChecksum {
		return ErrPanelDNSDrift
	}
	intent.Phase = "saving"
	if err = k.panelDNSWriteIntent(intent); err != nil {
		return err
	}
	// Last ownership check immediately before the global persistent save.
	count, latest, saved, err := k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count != 1 || latest != current || saved != intent.OriginalChecksum {
		return ErrPanelDNSDrift
	}
	if _, err = k.rciPost(ctx, "system/configuration/save", map[string]any{}); err != nil {
		return ErrPanelDNSPending
	}
	if err = k.waitConfigurationSaved(ctx, current); err != nil {
		return ErrPanelDNSPending
	}
	count, latest, saved, err = k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count != 1 || latest != current || saved != current {
		return ErrPanelDNSDrift
	}
	return k.panelDNSClearIntent()
}

// Reconciliation runs under daemon process ownership before controllers start.
// It may persist an already-added owned alias; it never creates a missing one.
func (k *keenetic) ReconcilePanelAlias(ctx context.Context) error {
	if _, err := os.Lstat(k.panelDNSJournal()); os.IsNotExist(err) {
		return nil
	}
	data, err := panelDNSPrivateRead(k.panelDNSJournal(), 4096)
	if err != nil {
		return err
	}
	var intent panelDNSIntent
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&intent) != nil || dec.Decode(new(any)) != io.EOF || intent.Schema != 1 || !panelDNSValidAddress(intent.Hostname, intent.IP) {
		return ErrPanelDNSPending
	}
	digest, digestErr := hex.DecodeString(intent.BaselineDigest)
	checksum, checksumErr := hex.DecodeString(intent.OriginalChecksum)
	if digestErr != nil || len(digest) != sha256.Size || checksumErr != nil || len(checksum) != 16 || intent.BaselineDigest != strings.ToLower(intent.BaselineDigest) || intent.OriginalChecksum != strings.ToLower(intent.OriginalChecksum) {
		return ErrPanelDNSPending
	}
	if intent.Phase != "prepared" && intent.Phase != "added" && intent.Phase != "saving" {
		return ErrPanelDNSPending
	}
	backupDirectory, err := os.Lstat(filepath.Dir(k.panelDNSBackup(intent)))
	if err != nil || !panelDNSOwned(backupDirectory) || !backupDirectory.IsDir() || backupDirectory.Mode().Perm()&0077 != 0 {
		return ErrPanelDNSPending
	}
	baseline, err := panelDNSPrivateRead(k.panelDNSBackup(intent), 4<<20)
	if err != nil {
		return err
	}
	normalized, count, err := panelDNSNormalized(baseline, intent.Hostname, intent.IP)
	if err != nil || count != 0 || panelDNSDigest(normalized) != intent.BaselineDigest {
		return ErrPanelDNSDrift
	}
	count, current, saved, err := k.panelDNSObserve(ctx, intent)
	if err != nil {
		return err
	}
	if count == 0 {
		if current != intent.OriginalChecksum || saved != intent.OriginalChecksum {
			return ErrPanelDNSDrift
		}
		return k.panelDNSClearIntent()
	}
	return k.panelDNSPersist(ctx, intent)
}
