package launcher

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	webui "github.com/jarymor-ux/kee-route-manager/internal/web/ui"
)

func panelFixture(t *testing.T) (*releaseFixture, *supervisor, config.Config) {
	t.Helper()
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	path := fixtureUI(t, f)
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.rec.UIConfig = path
	s.mu.Unlock()
	s.ui, err = startChild(filepath.Join(f.old.Directory, "kee-route-manager-ui"), path, "", s.output)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.waitPanel(c, s.ui); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s.mu.Lock()
	s.uiConfigHash = panelDigest(data)
	s.mu.Unlock()
	return f, s, c
}
func panelRequest(s *supervisor, path string, value any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(value)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewReader(raw)))
	return w
}
func prepareFixturePanel(t *testing.T, s *supervisor, port int) config.PanelStatus {
	t.Helper()
	w := panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "alice.jopa", Port: port})
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body)
	}
	var out config.PanelStatus
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Status != "prepared" || !out.Supported {
		t.Fatal(w.Body)
	}
	return out
}
func waitPanelPhase(t *testing.T, s *supervisor, phase string) config.PanelStatus {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		st := s.panelSnapshot()
		if st.Status == phase {
			return st
		}
		if st.Status == "failed" {
			t.Fatalf("panel failed: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("panel never reached %s: %+v", phase, s.panelSnapshot())
	return config.PanelStatus{}
}
func freePanelPort(t *testing.T) int {
	t.Helper()
	_, raw, _ := net.SplitHostPort(unusedAddress(t))
	port, _ := strconv.Atoi(raw)
	return port
}
func TestPanelPortTrialConfirmsWithoutRestartingDaemonOrChangingTrust(t *testing.T) {
	f, s, old := panelFixture(t)
	daemonPID, uiPID, rec := s.daemon.cmd.Process.Pid, s.ui.cmd.Process.Pid, s.record()
	primary, _ := os.ReadFile(old.Web.TLS.CertFile)
	beforeCfg, _ := os.ReadFile(rec.UIConfig)
	prepared := prepareFixturePanel(t, s, freePanelPort(t))
	if w := panelRequest(s, "/panel/apply", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	trial := waitPanelPhase(t, s, "awaiting_confirmation")
	if !s.ui.alive() || s.ui.cmd.Process.Pid != uiPID {
		t.Fatal("old port vanished before confirmation")
	}
	if err := webui.Ready(context.Background(), old); err != nil {
		t.Fatal("old origin lost trust/readiness", err)
	}
	if w := panelRequest(s, "/channel", map[string]string{"channel": "stable"}); w.Code != 409 {
		t.Fatal("channel change allowed in trial", w.Code)
	}
	if w := panelRequest(s, "/apply", map[string]string{"version": "1.1.0"}); w.Code != 409 {
		t.Fatal("release update allowed in trial", w.Code)
	}
	next, err := config.Load(rec.UIConfig)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(trial.CertificatePEM))
	conn, err := tls.Dial("tcp", next.Web.Listen, &tls.Config{RootCAs: roots, ServerName: "alice.jopa", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("candidate SNI readiness", err)
	}
	conn.Close()
	if w := panelRequest(s, "/panel/confirm", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "applied")
	s.wg.Wait()
	if s.daemon.cmd.Process.Pid != daemonPID || !s.daemon.alive() || s.record() != rec {
		t.Fatal("UI change restarted daemon or changed release record")
	}
	if s.ui.cmd.Process.Pid == uiPID || !s.ui.alive() || s.panelTrial != nil {
		t.Fatal("trial UI was not promoted")
	}
	after, _ := os.ReadFile(old.Web.TLS.CertFile)
	if !bytes.Equal(primary, after) || next.UIProxy.UpstreamCAFile != old.UIProxy.UpstreamCAFile {
		t.Fatal("primary/upstream TLS trust changed")
	}
	if bytes.Contains(beforeCfg, []byte("additional_certificates")) || len(next.Web.TLS.AdditionalCertificates) != 1 {
		t.Fatal("SNI alias was not appended")
	}
	if _, err := net.DialTimeout("tcp", old.Web.Listen, 100*time.Millisecond); err == nil {
		t.Fatal("old port stayed open after confirm")
	}
	if err := webui.Ready(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	waitDaemonVersion(t, f.c, "1.0.0")
}
func TestPanelSamePortTLSAliasRestartsOnlyUIAndTimeoutRestoresConfig(t *testing.T) {
	_, s, old := panelFixture(t)
	daemonPID, uiPID := s.daemon.cmd.Process.Pid, s.ui.cmd.Process.Pid
	_, port, _ := config.PanelListen(old)
	s.panelWindow = 500 * time.Millisecond
	before, _ := os.ReadFile(s.record().UIConfig)
	prepared := prepareFixturePanel(t, s, port)
	if w := panelRequest(s, "/panel/apply", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "awaiting_confirmation")
	waitPanelPhase(t, s, "rolled_back")
	s.wg.Wait()
	if s.daemon.cmd.Process.Pid != daemonPID || !s.daemon.alive() || s.ui.cmd.Process.Pid == uiPID || !s.ui.alive() {
		t.Fatal("incorrect process lifecycle")
	}
	after, _ := os.ReadFile(s.record().UIConfig)
	if !bytes.Equal(before, after) {
		t.Fatal("timeout did not restore exact YAML")
	}
	if err := webui.Ready(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if st := s.panelSnapshot(); st.URL != config.PanelURL("127.0.0.1", port) || st.CertificateChanged {
		t.Fatal("rollback status points at candidate", st)
	}
}
func TestPanelCandidateTLSFailureRestoresOldPortWithoutDaemonRestart(t *testing.T) {
	_, s, old := panelFixture(t)
	daemonPID, uiPID := s.daemon.cmd.Process.Pid, s.ui.cmd.Process.Pid
	prepared := prepareFixturePanel(t, s, freePanelPort(t))
	raw, _ := os.ReadFile(s.panelPath("candidate.yaml"))
	c, err := config.LoadBytes(raw, s.record().UIConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(c.Web.TLS.AdditionalCertificates[0].CertFile); err != nil {
		t.Fatal(err)
	}
	if w := panelRequest(s, "/panel/apply", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "rolled_back")
	s.wg.Wait()
	if s.daemon.cmd.Process.Pid != daemonPID || s.ui.cmd.Process.Pid != uiPID || !s.ui.alive() {
		t.Fatal("old origin/daemon changed after failed candidate")
	}
	if err := webui.Ready(context.Background(), old); err != nil {
		t.Fatal(err)
	}
}
func TestPanelOccupiedPortAndConfigurationDriftRejected(t *testing.T) {
	_, s, _ := panelFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, raw, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(raw)
	if w := panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "alice.jopa", Port: port}); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	path := s.record().UIConfig
	data, _ := os.ReadFile(path)
	if err = os.WriteFile(path, append(data, []byte("\n# external writer\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if w := panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "alice.jopa", Port: freePanelPort(t)}); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if s.panel != nil {
		t.Fatal("rejected preparation changed durable state")
	}
}

func TestPanelPortOnlyKeepsExistingIPAndCertificate(t *testing.T) {
	_, s, old := panelFixture(t)
	daemonPID := s.daemon.cmd.Process.Pid
	port := freePanelPort(t)
	w := panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "127.0.0.2", Port: port})
	if w.Code != 400 {
		t.Fatal("foreign listener IP accepted", w.Code, w.Body)
	}
	w = panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "127.0.0.1", Port: port})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var prepared config.PanelStatus
	if json.Unmarshal(w.Body.Bytes(), &prepared) != nil || prepared.CertificateChanged {
		t.Fatal("port-only change replaced identity", w.Body)
	}
	if w := panelRequest(s, "/panel/apply", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "awaiting_confirmation")
	if w := panelRequest(s, "/panel/confirm", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "applied")
	s.wg.Wait()
	c, err := config.Load(s.record().UIConfig)
	if err != nil || c.Web.TLS.CertFile != old.Web.TLS.CertFile || len(c.Web.TLS.AdditionalCertificates) != 0 || s.daemon.cmd.Process.Pid != daemonPID {
		t.Fatal("port-only change altered infrastructure", err)
	}
}
func TestPanelInterruptedApplyRecoveryRestoresOnlyUIConfig(t *testing.T) {
	f, s, _ := panelFixture(t)
	before, _ := os.ReadFile(s.record().UIConfig)
	prepareFixturePanel(t, s, freePanelPort(t))
	s.mu.Lock()
	j := s.panel.journal
	j.Status.Status = "applying"
	s.mu.Unlock()
	if err := s.persistPanel(j); err != nil {
		t.Fatal(err)
	}
	candidate, _ := os.ReadFile(s.panelPath("candidate.yaml"))
	if err := writePanelConfig(j.ConfigFile, candidate); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.c.Paths.StateDir, "operator-note")
	if err := os.WriteFile(marker, []byte("preserve current state"), 0600); err != nil {
		t.Fatal(err)
	}
	other := &supervisor{c: s.c, rec: s.record(), ctx: s.ctx}
	if err := other.recoverPanel(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(j.ConfigFile)
	note, _ := os.ReadFile(marker)
	if !bytes.Equal(before, after) || string(note) != "preserve current state" || other.rec != s.record() || other.panel.journal.Status.Status != "rolled_back" {
		t.Fatal("recovery changed unrelated durable state")
	}
	if err := os.WriteFile(j.ConfigFile, append(candidate, []byte("# drift\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.persistPanel(j); err != nil {
		t.Fatal(err)
	}
	if err := other.recoverPanel(); err == nil {
		t.Fatal("recovery overwrote operator drift")
	}
}

func TestPanelFailedStopRetainsOwnedTrackerAndRefusesNewPreparation(t *testing.T) {
	f, s, old := panelFixture(t)
	daemonPID := s.daemon.cmd.Process.Pid
	_, port, _ := config.PanelListen(old)
	prepareFixturePanel(t, s, port)
	baseline, _ := os.ReadFile(s.record().UIConfig)
	// An uninitialized handle fails Signal without touching any host process.
	// This models a child whose termination has not been proven.
	tracker := &child{cmd: &exec.Cmd{Process: &os.Process{}}, done: make(chan struct{})}
	s.panelTrial = tracker
	defer close(tracker.done)
	s.rollbackPanel(s.panel, baseline, f.old.Directory, false)
	if s.panelTrial != tracker || !s.panelRecoveryPending() || s.panelSnapshot().Status != "failed" {
		t.Fatal("unjoined UI child lost from supervisor tracking")
	}
	if w := panelRequest(s, "/panel/prepare", config.PanelInput{Hostname: "another.jopa", Port: port}); w.Code != 409 {
		t.Fatal("new preparation allowed with an unjoined child", w.Code, w.Body)
	}
	s.transition.Lock()
	err := s.restartUIOnly()
	s.transition.Unlock()
	if err == nil || s.panelTrial != tracker || s.daemon.cmd.Process.Pid != daemonPID || !s.daemon.alive() {
		t.Fatal("failed UI cleanup affected controller ownership")
	}
}

func TestPanelSamePortDriftFailureNeverRestartsDaemonOrRestoresUnknownConfig(t *testing.T) {
	_, s, old := panelFixture(t)
	daemonPID := s.daemon.cmd.Process.Pid
	_, port, _ := config.PanelListen(old)
	prepared := prepareFixturePanel(t, s, port)
	if w := panelRequest(s, "/panel/apply", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitPanelPhase(t, s, "awaiting_confirmation")
	path := s.record().UIConfig
	data, _ := os.ReadFile(path)
	drift := append(data, []byte("\n# independent operator edit\n")...)
	if err := os.WriteFile(path, drift, 0600); err != nil {
		t.Fatal(err)
	}
	if w := panelRequest(s, "/panel/confirm", config.PanelRevision{Revision: prepared.Revision}); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	s.wg.Wait()
	if s.panelSnapshot().Status != "failed" || s.ui.alive() {
		t.Fatal("same-port drift did not fail closed")
	}
	s.transition.Lock()
	err := s.restartUIOnly()
	s.transition.Unlock()
	after, _ := os.ReadFile(path)
	if err == nil || !bytes.Equal(drift, after) || !s.daemon.alive() || s.daemon.cmd.Process.Pid != daemonPID {
		t.Fatal("UI failure restarted daemon or rewrote external configuration")
	}
}

func TestSupervisedUICrashRestartsOnlyVerifiedUI(t *testing.T) {
	_, s, old := panelFixture(t)
	daemonPID, uiPID, rec := s.daemon.cmd.Process.Pid, s.ui.cmd.Process.Pid, s.record()
	if err := s.ui.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.transition.Lock()
	err := s.restartUIOnly()
	s.transition.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !s.daemon.alive() || s.daemon.cmd.Process.Pid != daemonPID || s.ui.cmd.Process.Pid == uiPID || s.record() != rec {
		t.Fatal("UI crash affected controller or release record")
	}
	if err := webui.Ready(context.Background(), old); err != nil {
		t.Fatal(err)
	}
}
