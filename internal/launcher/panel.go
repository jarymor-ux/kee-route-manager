package launcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	webui "github.com/jarymor-ux/kee-route-manager/internal/web/ui"
)

type panelJournal struct {
	Schema           int                `json:"schema"`
	ConfigFile       string             `json:"config_file"`
	Before           string             `json:"before"`
	After            string             `json:"after"`
	Status           config.PanelStatus `json:"status"`
	PreviousHostname string             `json:"previous_hostname,omitempty"`
	PreviousPort     int                `json:"previous_port,omitempty"`
	Committed        bool               `json:"committed,omitempty"`
}
type panelOperation struct {
	journal       panelJournal
	confirmed     chan struct{}
	confirmedOnce bool
}

func panelDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func readPanelConfig(path string, private bool) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 2<<20 || private && st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("panel configuration is unavailable")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("untrusted panel configuration")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("panel configuration is unavailable")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, errors.New("panel configuration changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("panel configuration is unavailable")
	}
	return data, nil
}
func writePanelConfig(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".panel-")
	if err != nil {
		return errors.New("cannot persist panel configuration")
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist panel configuration")
	}
	if err = os.Rename(name, path); err != nil {
		return errors.New("cannot persist panel configuration")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *supervisor) panelPath(name string) string {
	return filepath.Join(s.c.Update.InstallDir, "panel-"+name)
}
func (s *supervisor) persistPanel(j panelJournal) error {
	return atomicJSON(s.panelPath("state.json"), j)
}

// An interrupted unconfirmed change restores only the UI YAML, before any
// child is started. Controller state, release records and discovery are intact.
func (s *supervisor) recoverPanel() error {
	path := s.panelPath("state.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	raw, err := readPanelConfig(path, true)
	if err != nil {
		return err
	}
	var j panelJournal
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&j) != nil || d.Decode(new(any)) != io.EOF || j.Schema != 1 || j.ConfigFile != s.record().UIConfig || !filepath.IsAbs(j.ConfigFile) {
		return errors.New("invalid panel recovery record")
	}
	for _, hash := range []string{j.Before, j.After, j.Status.Revision} {
		decoded, e := hex.DecodeString(hash)
		if e != nil || len(decoded) != 32 {
			return errors.New("invalid panel recovery identity")
		}
	}
	needsRollback := j.Status.Status == "prepared" || j.Status.Status == "applying" || j.Status.Status == "awaiting_confirmation" || j.Status.Status == "failed" && !j.Committed
	switch {
	case needsRollback:
		current, e := readPanelConfig(j.ConfigFile, false)
		if e != nil || panelDigest(current) != j.Before && panelDigest(current) != j.After {
			return errors.New("panel recovery refused configuration drift")
		}
		backup, e := readPanelConfig(s.panelPath("backup.yaml"), true)
		if e != nil || panelDigest(backup) != j.Before {
			return errors.New("panel recovery baseline is unavailable")
		}
		baselineConfig, e := config.LoadBytes(backup, j.ConfigFile)
		if e != nil {
			return errors.New("panel recovery baseline is invalid")
		}
		if panelDigest(current) != j.Before {
			if e = writePanelConfig(j.ConfigFile, backup); e != nil {
				return e
			}
		}
		j.Status.Status, j.Status.Error = "rolled_back", "unconfirmed panel address restored after restart"
		baselineStatus := currentPanel(baselineConfig)
		if config.ValidPanelAddressHost(j.PreviousHostname, baselineStatus.ListenIP) && config.ValidPanelPort(j.PreviousPort) {
			baselineStatus.Hostname, baselineStatus.Port, baselineStatus.URL = j.PreviousHostname, j.PreviousPort, config.PanelURL(j.PreviousHostname, j.PreviousPort)
		}
		j.Status.Hostname, j.Status.Port, j.Status.URL = baselineStatus.Hostname, baselineStatus.Port, baselineStatus.URL
		j.Status.CertificateChanged, j.Status.CertificatePEM = false, ""
		j.Status.ConfirmationDeadline = time.Time{}
		if e = s.persistPanel(j); e != nil {
			return e
		}
	case j.Status.Status == "applied" || j.Status.Status == "failed" && j.Committed:
		current, e := readPanelConfig(j.ConfigFile, false)
		if e != nil || panelDigest(current) != j.After {
			return errors.New("committed panel configuration drift requires operator reconciliation")
		}
	case j.Status.Status == "rolled_back":
	default:
		return errors.New("invalid panel recovery phase")
	}
	s.mu.Lock()
	s.panel = &panelOperation{journal: j}
	s.mu.Unlock()
	return nil
}

func currentPanel(c config.Config) config.PanelStatus {
	ip, port, err := config.PanelListen(c)
	if err != nil {
		return config.PanelStatus{Status: "idle", Error: "address editing requires a supervised HTTPS UI on a fixed local IP"}
	}
	host := ip
	for _, name := range c.Web.TLS.Hosts {
		if config.ValidPanelHostname(name) {
			host = name
		}
	}
	if len(c.Web.TLS.AdditionalCertificates) != 0 {
		host = c.Web.TLS.AdditionalCertificates[len(c.Web.TLS.AdditionalCertificates)-1].Hostname
	}
	return config.PanelStatus{Supported: true, Status: "idle", Hostname: host, Port: port, ListenIP: ip, URL: config.PanelURL(host, port)}
}
func (s *supervisor) panelSnapshot() config.PanelStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.panel != nil {
		return s.panel.journal.Status
	}
	if s.rec.UIConfig == "" {
		return config.PanelStatus{Status: "idle", Error: "panel address editing requires a locally supervised UI"}
	}
	c, err := config.Load(s.rec.UIConfig)
	if err != nil {
		return config.PanelStatus{Status: "idle", Error: "panel configuration is unavailable"}
	}
	return currentPanel(c)
}
func decodePanel(w http.ResponseWriter, r *http.Request, input any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(input) != nil || d.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid panel request"})
		return false
	}
	return true
}
func (s *supervisor) panelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/panel/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		reply(w, 200, s.panelSnapshot())
	})
	mux.HandleFunc("/panel/prepare", s.preparePanel)
	mux.HandleFunc("/panel/apply", s.applyPanelRequest)
	mux.HandleFunc("/panel/confirm", s.confirmPanel)
}
func (s *supervisor) preparePanel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input *config.PanelInput
	if !decodePanel(w, r, &input) {
		return
	}
	if input == nil || (!config.ValidPanelHostname(input.Hostname) && net.ParseIP(input.Hostname) == nil) || !config.ValidPanelPort(input.Port) {
		reply(w, 400, map[string]string{"error": "invalid panel hostname or port"})
		return
	}
	if !s.transition.TryLock() {
		reply(w, 409, map[string]string{"error": "another launcher transition is running"})
		return
	}
	defer s.transition.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		reply(w, 503, map[string]string{"error": "launcher is stopping"})
		return
	}
	if s.status.Applying || s.panelNeedsRecovery {
		reply(w, 409, map[string]string{"error": "another launcher transition is running"})
		return
	}
	path := s.rec.UIConfig
	data, err := readPanelConfig(path, false)
	if err != nil {
		reply(w, 409, map[string]string{"error": "panel address editing requires a locally supervised UI"})
		return
	}
	if s.uiConfigHash == "" || panelDigest(data) != s.uiConfigHash {
		reply(w, 409, map[string]string{"error": "panel configuration changed; restart the supervised UI first"})
		return
	}
	c, err := config.LoadBytes(data, path)
	if err != nil {
		reply(w, 409, map[string]string{"error": "panel configuration is unavailable"})
		return
	}
	status := currentPanel(c)
	if !status.Supported {
		reply(w, 409, status)
		return
	}
	if !config.ValidPanelAddressHost(input.Hostname, status.ListenIP) {
		reply(w, 400, map[string]string{"error": "panel IP cannot be changed"})
		return
	}
	previousHostname, previousPort := status.Hostname, status.Port
	if s.panel != nil && (s.panel.journal.Status.Status == "applied" || s.panel.journal.Status.Status == "rolled_back") && s.panel.journal.Status.Port == status.Port {
		previousHostname = s.panel.journal.Status.Hostname
	}
	ready, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	err = webui.Ready(ready, c)
	cancel()
	if err != nil {
		reply(w, 409, map[string]string{"error": "existing UI readiness or certificate trust is unavailable"})
		return
	}
	if input.Port != status.Port {
		ln, e := net.Listen("tcp", net.JoinHostPort(status.ListenIP, strconv.Itoa(input.Port)))
		if e != nil {
			reply(w, 409, map[string]string{"error": "requested panel port is unavailable"})
			return
		}
		_ = ln.Close()
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		reply(w, 500, map[string]string{"error": "cannot prepare panel address"})
		return
	}
	revision := hex.EncodeToString(nonce)
	pemData, extra, changed, err := s.panelIdentity(c, input.Hostname, revision)
	if err != nil {
		reply(w, 409, map[string]string{"error": "cannot prepare panel TLS identity"})
		return
	}
	candidate, _, err := config.PanelConfig(data, path, *input, extra)
	if err != nil {
		reply(w, 400, map[string]string{"error": "invalid panel configuration"})
		return
	}
	status.Hostname, status.Port, status.URL = input.Hostname, input.Port, config.PanelURL(input.Hostname, input.Port)
	status.Revision, status.Status, status.CertificatePEM, status.CertificateChanged = revision, "prepared", string(pemData), changed
	j := panelJournal{Schema: 1, ConfigFile: path, Before: panelDigest(data), After: panelDigest(candidate), Status: status, PreviousHostname: previousHostname, PreviousPort: previousPort}
	if err = writePanelConfig(s.panelPath("backup.yaml"), data); err == nil {
		err = writePanelConfig(s.panelPath("candidate.yaml"), candidate)
	}
	if err == nil {
		err = s.persistPanel(j)
	}
	if err != nil {
		reply(w, 500, map[string]string{"error": "cannot persist prepared panel address"})
		return
	}
	s.panel = &panelOperation{journal: j, confirmed: make(chan struct{})}
	reply(w, 200, status)
}

func (s *supervisor) panelIdentity(c config.Config, host, revision string) ([]byte, []config.TLSCertificate, bool, error) {
	readIdentity := func(pair config.TLSCertificate) ([]byte, bool) {
		data, err := os.ReadFile(pair.CertFile)
		if err != nil {
			return nil, false
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, false
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil || leaf.VerifyHostname(host) != nil || time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
			return nil, false
		}
		if _, err = tls.LoadX509KeyPair(pair.CertFile, pair.KeyFile); err != nil {
			return nil, false
		}
		return data, true
	}
	extra := append([]config.TLSCertificate(nil), c.Web.TLS.AdditionalCertificates...)
	for _, pair := range extra {
		if pair.Hostname == host {
			if data, ok := readIdentity(pair); ok {
				return data, extra, false, nil
			}
			return nil, nil, false, errors.New("existing alias identity unavailable")
		}
	}
	if data, ok := readIdentity(config.TLSCertificate{CertFile: c.Web.TLS.CertFile, KeyFile: c.Web.TLS.KeyFile}); ok {
		return data, extra, false, nil
	}
	if len(extra) >= 16 {
		return nil, nil, false, errors.New("UI alias certificate limit reached")
	}
	dir := filepath.Join(s.c.Update.InstallDir, "panel-identities")
	if err := privateDir(dir); err != nil {
		return nil, nil, false, err
	}
	pair := config.TLSCertificate{Hostname: host, CertFile: filepath.Join(dir, revision+".crt"), KeyFile: filepath.Join(dir, revision+".key")}
	identity := config.TLS{Enabled: true, AutoGenerate: true, Hosts: []string{host}, CertFile: pair.CertFile, KeyFile: pair.KeyFile}
	if err := tlsutil.EnsureTLS(identity, c.Web.Listen); err != nil {
		return nil, nil, false, err
	}
	data, ok := readIdentity(pair)
	if !ok {
		return nil, nil, false, errors.New("generated UI identity unavailable")
	}
	return data, append(extra, pair), true, nil
}

func (s *supervisor) applyPanelRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input *config.PanelRevision
	if !decodePanel(w, r, &input) {
		return
	}
	s.mu.Lock()
	if input == nil || s.panel == nil || s.panel.journal.Status.Status != "prepared" || input.Revision != s.panel.journal.Status.Revision {
		s.mu.Unlock()
		reply(w, 409, map[string]string{"error": "panel preparation changed; prepare again"})
		return
	}
	if s.closing || s.ctx.Err() != nil {
		s.mu.Unlock()
		reply(w, 503, map[string]string{"error": "launcher is stopping"})
		return
	}
	if s.status.Applying {
		s.mu.Unlock()
		reply(w, 409, map[string]string{"error": "another launcher transition is running"})
		return
	}
	op := s.panel
	op.journal.Status.Status = "applying"
	if err := s.persistPanel(op.journal); err != nil {
		op.journal.Status.Status = "prepared"
		s.mu.Unlock()
		reply(w, 500, map[string]string{"error": "cannot persist panel transition"})
		return
	}
	s.status.Applying, s.status.Phase = true, "panel_applying"
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); s.applyPanel(op) }()
	reply(w, 202, map[string]bool{"accepted": true})
}
func (s *supervisor) confirmPanel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input *config.PanelRevision
	if !decodePanel(w, r, &input) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input == nil || s.panel == nil || input.Revision != s.panel.journal.Status.Revision || s.panel.journal.Status.Status != "awaiting_confirmation" {
		reply(w, 409, map[string]string{"error": "panel trial is not awaiting confirmation"})
		return
	}
	if !s.panel.confirmedOnce {
		s.panel.confirmedOnce = true
		close(s.panel.confirmed)
	}
	reply(w, 202, map[string]bool{"accepted": true})
}

func (s *supervisor) applyPanel(op *panelOperation) {
	s.transition.Lock()
	defer s.transition.Unlock()
	defer func() { s.mu.Lock(); s.status.Applying = false; s.status.Phase = "idle"; s.mu.Unlock() }()
	s.mu.Lock()
	j := op.journal
	s.mu.Unlock()
	baseline, err := readPanelConfig(s.panelPath("backup.yaml"), true)
	if err != nil || panelDigest(baseline) != j.Before {
		s.failPanel(op, "panel baseline is unavailable")
		return
	}
	current, err := readPanelConfig(j.ConfigFile, false)
	if err != nil || panelDigest(current) != j.Before {
		s.failPanel(op, "panel configuration drift requires operator reconciliation")
		return
	}
	candidate, err := readPanelConfig(s.panelPath("candidate.yaml"), true)
	if err != nil || panelDigest(candidate) != j.After {
		s.failPanel(op, "prepared panel configuration is unavailable")
		return
	}
	old, err := config.LoadBytes(baseline, j.ConfigFile)
	if err != nil {
		s.failPanel(op, "panel baseline is invalid")
		return
	}
	next, err := config.LoadBytes(candidate, j.ConfigFile)
	if err != nil {
		s.failPanel(op, "prepared panel configuration is invalid")
		return
	}
	rec := s.record()
	rel, err := update.VerifyInstalledRelease(s.c.Update.InstallDir, rec.Active, s.c.Update.PublicKey)
	if err != nil || rel.ManifestSHA256 != rec.ActiveDigest {
		s.failPanel(op, "active UI release identity is unavailable")
		return
	}
	_, oldPort, _ := config.PanelListen(old)
	samePort := oldPort == j.Status.Port
	if err = writePanelConfig(j.ConfigFile, candidate); err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, false)
		return
	}
	if samePort {
		stop, done := context.WithTimeout(context.Background(), 40*time.Second)
		err = s.ui.stop(stop)
		done()
		if err != nil {
			s.rollbackPanel(op, baseline, rel.Directory, true)
			return
		}
	}
	s.panelTrial, err = startChild(filepath.Join(rel.Directory, "kee-route-manager-ui"), j.ConfigFile, "", s.output)
	if err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	if err = s.waitPanel(next, s.panelTrial); err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	if err = panelHostnameReady(s.ctx, next, j.Status.Hostname, j.Status.CertificatePEM); err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	window := s.panelWindow
	if window <= 0 {
		window = 5 * time.Minute
	}
	s.mu.Lock()
	op.journal.Status.Status, op.journal.Status.ConfirmationDeadline = "awaiting_confirmation", time.Now().UTC().Add(window)
	err = s.persistPanel(op.journal)
	s.mu.Unlock()
	if err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	confirmed := false
	select {
	case <-op.confirmed:
		confirmed = true
	case <-timer.C:
	case <-s.ctx.Done():
	case <-s.panelTrial.done:
	}
	if !confirmed || !s.panelTrial.alive() {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	// Recheck readiness and operator drift immediately before the durable decision.
	latest, err := readPanelConfig(j.ConfigFile, false)
	if err != nil || panelDigest(latest) != j.After || s.waitPanel(next, s.panelTrial) != nil || panelHostnameReady(s.ctx, next, j.Status.Hostname, j.Status.CertificatePEM) != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	s.mu.Lock()
	op.journal.Status.Status, op.journal.Status.ConfirmationDeadline = "applied", time.Time{}
	op.journal.Committed = true
	err = s.persistPanel(op.journal)
	s.mu.Unlock()
	if err != nil {
		s.rollbackPanel(op, baseline, rel.Directory, samePort)
		return
	}
	// Commit precedes removing the old origin. Crash recovery thereafter starts
	// the selected UI without any controller or release-slot transition.
	if !samePort {
		stop, done := context.WithTimeout(context.Background(), 40*time.Second)
		err = s.ui.stop(stop)
		done()
	}
	previous := s.ui
	s.ui, s.panelTrial = s.panelTrial, nil
	if err != nil {
		s.panelTrial = previous
		s.mu.Lock()
		s.panelNeedsRecovery = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.uiConfigHash = j.After
	s.mu.Unlock()
	if err != nil {
		s.failPanel(op, "panel committed; old UI did not stop cleanly")
	}
}

func panelHostnameReady(ctx context.Context, c config.Config, host, certificate string) error {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(certificate)) {
		return errors.New("panel hostname trust unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	t := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", c.Web.Listen)
	}}
	defer t.CloseIdleConnections()
	_, port, err := config.PanelListen(c)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", config.PanelURL(host, port)+"/healthz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: t, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("panel hostname readiness unavailable")
	}
	return nil
}
func (s *supervisor) waitPanel(c config.Config, process *child) error {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	for {
		if !process.alive() {
			return errors.New("candidate UI exited")
		}
		if err := webui.Ready(ctx, c); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (s *supervisor) failPanel(op *panelOperation, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op.journal.Status.Status, op.journal.Status.Error, op.journal.Status.ConfirmationDeadline = "failed", message, time.Time{}
	_ = s.persistPanel(op.journal)
}
func (s *supervisor) rollbackPanel(op *panelOperation, baseline []byte, directory string, restartOld bool) {
	stop, done := context.WithTimeout(context.Background(), 40*time.Second)
	err := s.panelTrial.stop(stop)
	done()
	if err != nil {
		s.mu.Lock()
		s.panelNeedsRecovery = true
		s.mu.Unlock()
		s.failPanel(op, "candidate UI did not stop; recovery requires operator reconciliation")
		return
	}
	s.panelTrial = nil
	current, err := readPanelConfig(op.journal.ConfigFile, false)
	if err != nil || panelDigest(current) != op.journal.Before && panelDigest(current) != op.journal.After {
		s.failPanel(op, "panel configuration drift requires operator reconciliation")
		return
	}
	if err = writePanelConfig(op.journal.ConfigFile, baseline); err != nil {
		s.failPanel(op, "panel configuration restore failed")
		return
	}
	if restartOld && s.ctx.Err() == nil {
		s.ui, err = startChild(filepath.Join(directory, "kee-route-manager-ui"), op.journal.ConfigFile, "", s.output)
		if err != nil {
			s.failPanel(op, "previous UI requires restart")
			return
		}
		old, err := config.LoadBytes(baseline, op.journal.ConfigFile)
		if err != nil || s.waitPanel(old, s.ui) != nil {
			s.failPanel(op, "previous UI readiness failed")
			return
		}
	}
	s.mu.Lock()
	op.journal.Committed = false
	op.journal.Status.Status, op.journal.Status.Error, op.journal.Status.ConfirmationDeadline = "rolled_back", "panel address was not confirmed; previous address restored", time.Time{}
	if old, err := config.LoadBytes(baseline, op.journal.ConfigFile); err == nil {
		status := currentPanel(old)
		if config.ValidPanelAddressHost(op.journal.PreviousHostname, status.ListenIP) && config.ValidPanelPort(op.journal.PreviousPort) {
			status.Hostname, status.Port, status.URL = op.journal.PreviousHostname, op.journal.PreviousPort, config.PanelURL(op.journal.PreviousHostname, op.journal.PreviousPort)
		}
		op.journal.Status.Hostname, op.journal.Status.Port, op.journal.Status.URL = status.Hostname, status.Port, status.URL
		op.journal.Status.CertificateChanged, op.journal.Status.CertificatePEM = false, ""
	}
	_ = s.persistPanel(op.journal)
	s.uiConfigHash = op.journal.Before
	s.mu.Unlock()
}

func (s *supervisor) panelRecoveryPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panelNeedsRecovery
}

// Called with transition held. UI failure never restarts a healthy controller.
// An unconfirmed owned change may restore its exact baseline; unrelated edits
// remain untouched and require operator reconciliation.
func (s *supervisor) restartUIOnly() error {
	if s.panelTrial != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		err := s.panelTrial.stop(ctx)
		cancel()
		if err != nil {
			return errors.New("UI candidate still owns its process; recovery is pending")
		}
		s.panelTrial = nil
	}
	s.mu.Lock()
	failedPanel := s.panel != nil && s.panel.journal.Status.Status == "failed"
	s.mu.Unlock()
	if failedPanel {
		if err := s.recoverPanel(); err != nil {
			return err
		}
	}
	rec := s.record()
	if rec.UIConfig == "" {
		return errors.New("managed UI configuration is unavailable")
	}
	data, err := readPanelConfig(rec.UIConfig, false)
	if err != nil {
		return err
	}
	s.mu.Lock()
	expected := s.uiConfigHash
	if s.panel != nil && failedPanel {
		if s.panel.journal.Committed {
			expected = s.panel.journal.After
		} else {
			expected = s.panel.journal.Before
		}
	}
	s.mu.Unlock()
	if expected == "" || panelDigest(data) != expected {
		return errors.New("UI configuration drift requires operator reconciliation")
	}
	c, err := config.LoadBytes(data, rec.UIConfig)
	if err != nil {
		return errors.New("verified UI configuration is invalid")
	}
	rel, err := update.VerifyInstalledRelease(s.c.Update.InstallDir, rec.Active, s.c.Update.PublicKey)
	if err != nil || rel.ManifestSHA256 != rec.ActiveDigest {
		return errors.New("active UI release identity is unavailable")
	}
	if !s.ui.alive() {
		s.ui, err = startChild(filepath.Join(rel.Directory, "kee-route-manager-ui"), rec.UIConfig, "", s.output)
		if err != nil {
			return err
		}
	}
	if err = s.waitPanel(c, s.ui); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		_ = s.ui.stop(ctx)
		cancel()
		return errors.New("verified UI restart readiness failed")
	}
	s.mu.Lock()
	s.uiConfigHash, s.panelNeedsRecovery = expected, false
	s.mu.Unlock()
	return nil
}
