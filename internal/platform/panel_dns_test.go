package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type panelDNSFixture struct {
	rejectDownloads                      bool
	readCommands                         []string
	mu                                   sync.Mutex
	k                                    *keenetic
	running, original, mutated, baseline string
	saved                                string
	cmds                                 [][]string
	saves, reads, checks                 int
	injectRead, injectCheck              int
	parseFailure, saveFailure, lostSave  bool
	server                               *httptest.Server
}

func newPanelDNSFixture(t *testing.T) *panelDNSFixture {
	t.Helper()
	f := &panelDNSFixture{original: regressionChecksum(1), mutated: regressionChecksum(2), saved: regressionChecksum(1), baseline: "hostname router\nusername admin password PRIVATE-DNS-BASELINE-SECRET\nip host other.jopa 192.168.1.2\ninterface Home\n    ip address 192.168.1.1 255.255.255.0\n"}
	f.running = f.baseline
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/rci/show/last-change":
			f.checks++
			if f.injectCheck == f.checks {
				f.running += "ip name-server 8.8.8.8\n"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"checksum": f.checksum()})
		case "/ci/running-config.txt":
			if f.rejectDownloads {
				http.Error(w, "downloads denied", http.StatusForbidden)
				return
			}
			f.reads++
			if f.injectRead == f.reads {
				f.running += "ip name-server 8.8.8.8\n"
			}
			_, _ = fmt.Fprint(w, f.running)
		case "/ci/startup-config.txt":
			if f.rejectDownloads {
				http.Error(w, "downloads denied", http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, regressionStartupConfig(f.saved))
		case "/rci/system/configuration/save":
			if r.Method != "POST" {
				t.Error("save did not use POST")
			}
			f.saves++
			if f.lostSave {
				f.saved = f.checksum()
				http.Error(w, "lost response", http.StatusServiceUnavailable)
				return
			}
			if f.saveFailure {
				http.Error(w, "PRIVATE-RCI-ERROR", http.StatusServiceUnavailable)
				return
			}
			f.saved = f.checksum()
			_, _ = fmt.Fprint(w, `{}`)
		case "/native-ndmc":
			var args []string
			if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
				t.Error(err)
			}
			if len(args) == 2 && args[0] == "-c" && (args[1] == "more running-config" || args[1] == "more startup-config") {
				f.readCommands = append(f.readCommands, args[1])
				if args[1] == "more running-config" {
					f.reads++
					if f.injectRead == f.reads {
						f.running += "ip name-server 8.8.8.8\n"
					}
					_, _ = fmt.Fprint(w, "\x1b[K"+regressionStartupConfig(f.checksum())+f.running+"!\n\x1b[K")
				} else {
					body := f.baseline
					if f.saved == f.mutated {
						body += "ip host alice.jopa 192.168.1.1\n"
					}
					_, _ = fmt.Fprint(w, "\x1b[K"+regressionStartupConfig(f.saved)+body+"!\n\x1b[K")
				}
				return
			}
			f.cmds = append(f.cmds, args)
			if len(args) != 2 || args[0] != "-c" || args[1] != "ip host alice.jopa 192.168.1.1" {
				t.Errorf("unsafe argv: %q", args)
				http.Error(w, "bad argv", 400)
				return
			}
			if !f.parseFailure {
				f.running += "ip host alice.jopa 192.168.1.1\n"
			}
			_, _ = fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected DNS endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	cfg := config.Default()
	cfg.Paths.StateDir = t.TempDir()
	cfg.Platform.Keenetic.RCIBaseURL = f.server.URL + "/rci"
	// A real inert executable forwards its argv to the fixture. No router process,
	// system service or host DNS is changed by these tests.
	binary := filepath.Join(t.TempDir(), "ndmc-inert")
	helper := fmt.Sprintf("#!/usr/bin/env python3\nimport json,sys,urllib.request\nreq=urllib.request.Request(%q,data=json.dumps(sys.argv[1:]).encode(),headers={'Content-Type':'application/json'})\ntry:\n sys.stdout.buffer.write(urllib.request.urlopen(req,timeout=3).read())\nexcept Exception:\n sys.exit(1)\n", f.server.URL+"/native-ndmc")
	if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	cfg.Platform.Keenetic.NDMCBinary = binary
	f.k = newKeenetic(cfg, Runner{Timeout: 5 * time.Second}).(*keenetic)
	return f
}
func (f *panelDNSFixture) checksum() string {
	if f.running == f.baseline {
		return f.original
	}
	if f.running == f.baseline+"ip host alice.jopa 192.168.1.1\n" {
		return f.mutated
	}
	return regressionChecksum(99)
}
func requirePanelDNSError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("DNS errors exposed router configuration")
	}
}
func (f *panelDNSFixture) pendingIntent(t *testing.T) panelDNSIntent {
	t.Helper()
	raw, err := os.ReadFile(f.k.panelDNSJournal())
	if err != nil {
		t.Fatal(err)
	}
	var intent panelDNSIntent
	if json.Unmarshal(raw, &intent) != nil {
		t.Fatal("invalid pending record")
	}
	return intent
}

func TestPanelDNSCreatesOnlyOwnedAliasAndPersists(t *testing.T) {
	f := newPanelDNSFixture(t)
	if err := f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if f.running != f.baseline+"ip host alice.jopa 192.168.1.1\n" || f.saves != 1 || len(f.cmds) != 1 || f.saved != f.mutated {
		t.Fatal("unexpected DNS effects")
	}
	f.mu.Unlock()
	if _, err := os.Stat(f.k.panelDNSJournal()); !os.IsNotExist(err) {
		t.Fatal("completed journal retained")
	}
	files, err := os.ReadDir(filepath.Join(f.k.cfg.Paths.StateDir, "panel-dns-backups"))
	if err != nil || len(files) != 1 {
		t.Fatal("private baseline missing")
	}
	baselinePath := filepath.Join(f.k.cfg.Paths.StateDir, "panel-dns-backups", files[0].Name())
	raw, err := os.ReadFile(baselinePath)
	if err != nil || string(raw) != f.baseline {
		t.Fatal("baseline changed")
	}
	st, _ := os.Stat(baselinePath)
	if st.Mode().Perm() != 0600 {
		t.Fatal("baseline not private")
	}
	if err := f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saves != 1 || len(f.cmds) != 1 {
		t.Fatal("matching alias caused an extra global save")
	}
}
func TestPanelDNSRefusesSharedConflictUnsavedAndMalformedIdentity(t *testing.T) {
	for _, mode := range []string{"conflicting-ip", "duplicate", "unsaved", "loopback", "public", "shell-hostname", "local", "localhost"} {
		t.Run(mode, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			hostname, ip := "alice.jopa", "192.168.1.1"
			want := ErrPanelDNSUnavailable
			f.mu.Lock()
			switch mode {
			case "conflicting-ip":
				f.running += "ip host alice.jopa 192.168.1.2\n"
				want = ErrPanelDNSConflict
			case "duplicate":
				f.running += "ip host alice.jopa 192.168.1.1\nip host alice.jopa 192.168.1.1\n"
				want = ErrPanelDNSConflict
			case "unsaved":
				f.running += "ip name-server 8.8.8.8\n"
				want = ErrPanelDNSDrift
			case "loopback":
				ip = "127.0.0.1"
			case "public":
				ip = "8.8.8.8"
			case "shell-hostname":
				hostname = "alice.jopa;reboot"
			case "local":
				hostname = "alice.local"
			case "localhost":
				hostname = "alice.localhost"
			}
			unchanged := f.running
			f.mu.Unlock()
			requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), hostname, ip), want)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.running != unchanged || f.saves != 0 || len(f.cmds) != 0 {
				t.Fatal("invalid DNS request mutated router")
			}
		})
	}
}
func TestPanelDNSDetectsExternalDriftBeforeAddAfterAddAndBeforeSave(t *testing.T) {
	for _, mode := range []string{"before-baseline", "before-add", "after-add", "before-save"} {
		t.Run(mode, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			switch mode {
			case "before-baseline":
				f.injectRead = 1
			case "before-add":
				f.injectRead = 2
			case "after-add":
				f.injectRead = 3
			case "before-save":
				f.injectRead = 5
			}
			requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSDrift)
			f.mu.Lock()
			if f.saves != 0 {
				t.Fatal("saved external configuration")
			}
			before := f.running
			cmds := len(f.cmds)
			f.mu.Unlock()
			if mode == "before-baseline" || mode == "before-add" {
				if cmds != 0 {
					t.Fatal("drift before mutation still ran NDMC")
				}
			}
			if mode != "before-baseline" {
				requirePanelDNSError(t, f.k.ReconcilePanelAlias(context.Background()), ErrPanelDNSDrift)
				f.mu.Lock()
				if f.running != before || f.saves != 0 || len(f.cmds) != cmds {
					t.Fatal("startup replayed against drift")
				}
				f.mu.Unlock()
			}
		})
	}
}
func TestPanelDNSDetectsZeroExitParseFailure(t *testing.T) {
	f := newPanelDNSFixture(t)
	f.parseFailure = true
	requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSUnavailable)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running != f.baseline || f.saves != 0 || len(f.cmds) != 1 {
		t.Fatal("parse rejection caused persistent effects")
	}
	if _, err := os.Stat(f.k.panelDNSJournal()); !os.IsNotExist(err) {
		t.Fatal("unchanged rejected command retained intent")
	}
}
func TestPanelDNSRecoveryCompletesOwnedSaveWithoutRepeatingAdd(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			f := newPanelDNSFixture(t)
			f.saveFailure = !lost
			f.lostSave = lost
			requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSPending)
			intent := f.pendingIntent(t)
			if intent.Phase != "saving" {
				t.Fatal("missing pre-save intent")
			}
			st, _ := os.Stat(f.k.panelDNSJournal())
			if st.Mode().Perm() != 0600 {
				t.Fatal("journal not private")
			}
			f.mu.Lock()
			f.saveFailure = false
			f.lostSave = false
			f.mu.Unlock()
			if err := f.k.ReconcilePanelAlias(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			wantSaves := 2
			if lost {
				wantSaves = 1
			}
			if f.saves != wantSaves || len(f.cmds) != 1 || f.saved != f.mutated {
				t.Fatal("recovery repeated add or saved wrong configuration")
			}
		})
	}
}
func TestPanelDNSRecoveryNeverAddsMissingAlias(t *testing.T) {
	for _, phase := range []string{"prepared", "added", "saving"} {
		t.Run(phase, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			normalized, _, _ := panelDNSNormalized([]byte(f.baseline), "alice.jopa", "192.168.1.1")
			intent := panelDNSIntent{Schema: 1, Hostname: "alice.jopa", IP: "192.168.1.1", BaselineDigest: panelDNSDigest(normalized), OriginalChecksum: f.original, Phase: phase}
			if err := os.Mkdir(filepath.Dir(f.k.panelDNSBackup(intent)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := panelDNSWrite(f.k.panelDNSBackup(intent), []byte(f.baseline)); err != nil {
				t.Fatal(err)
			}
			if err := f.k.panelDNSWriteIntent(intent); err != nil {
				t.Fatal(err)
			}
			if err := f.k.ReconcilePanelAlias(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.saves != 0 || len(f.cmds) != 0 || f.running != f.baseline {
				t.Fatal("unaccepted/crashed request added alias at boot")
			}
		})
	}
}
func TestPanelDNSRecoveryRefusesChangedAliasAndMissingPrivateBackup(t *testing.T) {
	for _, mode := range []string{"changed-alias", "missing-backup", "corrupt-backup", "public-journal", "unknown-journal"} {
		t.Run(mode, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			f.saveFailure = true
			requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSPending)
			intent := f.pendingIntent(t)
			want := ErrPanelDNSPending
			switch mode {
			case "changed-alias":
				f.mu.Lock()
				f.running = strings.Replace(f.running, "ip host alice.jopa 192.168.1.1", "ip host alice.jopa 192.168.1.9", 1)
				f.mu.Unlock()
				want = ErrPanelDNSConflict
			case "missing-backup":
				_ = os.Remove(f.k.panelDNSBackup(intent))
			case "corrupt-backup":
				_ = os.WriteFile(f.k.panelDNSBackup(intent), []byte("hostname changed"), 0600)
				want = ErrPanelDNSDrift
			case "public-journal":
				_ = os.Chmod(f.k.panelDNSJournal(), 0644)
			case "unknown-journal":
				raw, _ := os.ReadFile(f.k.panelDNSJournal())
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"foreign":true}`)
				_ = os.WriteFile(f.k.panelDNSJournal(), raw, 0600)
			}
			f.mu.Lock()
			before, saves, cmds := f.running, f.saves, len(f.cmds)
			f.mu.Unlock()
			requirePanelDNSError(t, f.k.ReconcilePanelAlias(context.Background()), want)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.running != before || f.saves != saves || len(f.cmds) != cmds {
				t.Fatal("unsafe startup mutated configuration")
			}
		})
	}
}

func TestPanelDNSRefusesForeignUIDJournalBackupAndDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("foreign UID authority regression runs as root in isolated Docker")
	}
	for _, mode := range []string{"journal", "backup", "backup-directory", "recovery-directory"} {
		t.Run(mode, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			if mode == "backup-directory" {
				dir := filepath.Join(f.k.cfg.Paths.StateDir, "panel-dns-backups")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(dir, 1, 1); err != nil {
					t.Fatal(err)
				}
				requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSUnavailable)
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.running != f.baseline || f.saves != 0 || len(f.cmds) != 0 {
					t.Fatal("foreign-owned directory admitted DNS mutation")
				}
				return
			}
			f.saveFailure = true
			requirePanelDNSError(t, f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"), ErrPanelDNSPending)
			intent := f.pendingIntent(t)
			path := f.k.panelDNSJournal()
			if mode == "backup" {
				path = f.k.panelDNSBackup(intent)
			}
			if mode == "recovery-directory" {
				path = filepath.Dir(f.k.panelDNSBackup(intent))
			}
			if err := os.Chown(path, 1, 1); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			before, saves, cmds := f.running, f.saves, len(f.cmds)
			f.mu.Unlock()
			requirePanelDNSError(t, f.k.ReconcilePanelAlias(context.Background()), ErrPanelDNSPending)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.running != before || f.saves != saves || len(f.cmds) != cmds {
				t.Fatal("foreign-owned recovery file admitted persistent save")
			}
		})
	}
}

func TestPanelDNSNativeFallbackPreservesWholeSaveAndDriftGuards(t *testing.T) {
	for _, mode := range []string{"create", "noop", "unsaved", "conflict", "presave-drift", "lost-save"} {
		t.Run(mode, func(t *testing.T) {
			f := newPanelDNSFixture(t)
			f.rejectDownloads = true
			switch mode {
			case "noop":
				f.running += "ip host alice.jopa 192.168.1.1\n"
				f.saved = f.mutated
			case "unsaved":
				f.running += "ip name-server 8.8.8.8\n"
			case "conflict":
				f.running += "ip host alice.jopa 192.168.1.9\n"
			case "presave-drift":
				f.injectRead = 5
			case "lost-save":
				f.lostSave = true
			}
			err := f.k.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1")
			switch mode {
			case "create", "noop":
				if err != nil {
					t.Fatal(err)
				}
			case "unsaved", "presave-drift":
				requirePanelDNSError(t, err, ErrPanelDNSDrift)
			case "conflict":
				requirePanelDNSError(t, err, ErrPanelDNSConflict)
			case "lost-save":
				requirePanelDNSError(t, err, ErrPanelDNSPending)
			}
			if mode == "lost-save" {
				f.mu.Lock()
				f.lostSave = false
				f.mu.Unlock()
				if err = f.k.ReconcilePanelAlias(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.readCommands) == 0 {
				t.Fatal("DNS integration bypassed native fallback")
			}
			if mode == "create" || mode == "lost-save" {
				if len(f.cmds) != 1 || f.saves != 1 || f.saved != f.mutated {
					t.Fatal("native fallback lost DNS save identity")
				}
			} else if f.saves != 0 {
				t.Fatal("native fallback saved unrelated changes")
			}
		})
	}
}
