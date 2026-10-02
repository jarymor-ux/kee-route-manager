package control

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

// The trial never invokes the configured service commands: even a vendor's
// status command can repair files or start services. A real read-only API query
// below proves liveness, while ActualState only inspects persisted tunnel files.
type trialPlatform struct{}

func (trialPlatform) XrayRunning(context.Context) bool { return true }
func (trialPlatform) RestartXray(context.Context) error {
	return fmt.Errorf("restart forbidden during trial")
}

func trialReady(ctx context.Context, c config.Config, version string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := store.InspectReadOnly(c.Paths.StateDir, c.Paths.CacheDir, model.NewState(version, c.Xray.SlotTagPrefix, c.Pool.Size))
	if err != nil {
		return err
	}
	// The normal constructor marks unfinished operations unknown. Refuse rather
	// than letting trial startup modify their durable record.
	if b, err := os.ReadFile(filepath.Join(c.Paths.StateDir, "operation.json")); err == nil {
		var op struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(b, &op) != nil || op.Status == "running" {
			return fmt.Errorf("operation is not quiescent")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if c.API.Enabled {
		if _, err := auth.LoadCredentials(c.Web.CredentialsFile); err != nil {
			return fmt.Errorf("controller credentials unavailable")
		}
		if c.API.TLS.Enabled {
			if _, err := tls.LoadX509KeyPair(c.API.TLS.CertFile, c.API.TLS.KeyFile); err != nil {
				return fmt.Errorf("existing controller TLS unavailable")
			}
		}
	}
	runner := platform.Runner{Timeout: 3 * time.Second, MaxOutput: 1 << 20}
	xm := xray.NewManager(c, runner, trialPlatform{})
	actual, err := xm.ActualState(ctx)
	if err != nil {
		return err
	}
	if actual.Drift != "" {
		return fmt.Errorf("tunnel configuration has drifted")
	}
	if !state.XrayConfigured {
		if !state.AutomaticRoutingPaused || actual.Configured {
			return fmt.Errorf("unconfigured controller is not safely paused")
		}
		for _, name := range []string{"00_90_kee_route_manager_api.json", "03_90_kee_route_manager_inbounds.json", "04_90_kee_route_manager_outbounds.json", "05_90_kee_route_manager_routing.json"} {
			if _, err := os.Lstat(filepath.Join(c.Xray.ManagedDir, name)); !os.IsNotExist(err) {
				return fmt.Errorf("unconfigured controller has managed artifacts")
			}
		}
		return noTrialRoutingArtifacts(c)
	}
	if !actual.Configured || state.XrayConfigHash == "" || state.XrayConfigHash != actual.ConfigHash {
		return fmt.Errorf("tunnel files differ from committed state")
	}
	expected := c.Xray.ManagedDirectTag
	if !state.DirectMode {
		if state.ActiveSlot < 0 || state.ActiveSlot >= len(state.Pool) {
			return fmt.Errorf("committed VPN selection is missing")
		}
		expected = state.Pool[state.ActiveSlot].Tag
	}
	if actual.Selection.Tag != expected {
		return fmt.Errorf("persisted tunnel selection differs from state")
	}
	out, err := runner.Run(ctx, []string{c.Xray.Binary, "api", "bi", "--json", "--server=" + c.Xray.APIAddress, c.Xray.BalancerTag})
	if err != nil {
		return fmt.Errorf("read-only Xray API probe failed")
	}
	return validateTrialSelection(out, expected)
}

func validateTrialSelection(out []byte, expected string) error {
	var info struct {
		Balancer *struct {
			Override *struct {
				Target string `json:"target"`
			} `json:"override"`
			Principle *struct {
				Tag []string `json:"tag"`
			} `json:"principleTarget"`
			PrincipleSnake *struct {
				Tag []string `json:"tag"`
			} `json:"principle_target"`
		} `json:"balancer"`
	}
	if json.Unmarshal(out, &info) != nil || info.Balancer == nil {
		return fmt.Errorf("invalid Xray balancer API response")
	}
	b := info.Balancer
	if b.Override != nil && b.Override.Target != "" {
		if b.Override.Target == expected {
			return nil
		}
		return fmt.Errorf("runtime Xray override differs from committed selection")
	}
	// After Xray restarts the durable clone is selected without an override.
	var tags []string
	if b.Principle != nil {
		tags = b.Principle.Tag
	} else if b.PrincipleSnake != nil {
		tags = b.PrincipleSnake.Tag
	}
	if len(tags) == 1 && tags[0] == config.XraySelectionTag {
		return nil
	}
	return fmt.Errorf("runtime Xray selection differs from durable selection")
}

func serveTrial(ctx context.Context, c config.Config, version, nonce string) (bool, error) {
	if b, err := hex.DecodeString(nonce); err != nil || len(b) != 32 || strings.ToLower(nonce) != nonce {
		return false, fmt.Errorf("invalid update trial nonce")
	}
	// Fail before opening sockets or initializing any normal state machinery.
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := trialReady(checkCtx, c, version)
	cancel()
	if err != nil {
		return false, fmt.Errorf("update trial unavailable: %w", err)
	}
	activate := make(chan struct{})
	var once sync.Once
	var admission sync.Mutex
	check := func(ctx context.Context) error {
		admission.Lock()
		defer admission.Unlock()
		return trialReady(ctx, c, version)
	}
	health := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		checkCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := check(checkCtx); err != nil {
			replyUpdate(w, http.StatusServiceUnavailable, map[string]string{"error": "trial reconciliation unavailable"})
			return
		}
		replyUpdate(w, http.StatusOK, map[string]any{"status": "trial_ready", "role": "controller", "version": version, "update_nonce": nonce, "pid": os.Getpid(), "reconciled": true})
	}
	unavailable := func(w http.ResponseWriter, r *http.Request) {
		replyUpdate(w, http.StatusServiceUnavailable, map[string]string{"error": "controller update trial is read-only"})
	}
	public := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			health(w, r)
			return
		}
		unavailable(w, r)
	})
	local := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/update/activate" {
			public.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		admission.Lock()
		defer admission.Unlock()
		var input struct {
			Nonce string `json:"nonce"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		if err := d.Decode(&input); err != nil || d.Decode(new(any)) != io.EOF {
			replyUpdate(w, http.StatusBadRequest, map[string]string{"error": "invalid activation request"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(input.Nonce), []byte(nonce)) != 1 {
			replyUpdate(w, http.StatusForbidden, map[string]string{"error": "invalid activation nonce"})
			return
		}
		checkCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := trialReady(checkCtx, c, version); err != nil {
			replyUpdate(w, http.StatusServiceUnavailable, map[string]string{"error": "trial reconciliation unavailable"})
			return
		}
		replyUpdate(w, http.StatusOK, map[string]any{"activated": true, "version": version, "pid": os.Getpid()})
		once.Do(func() { close(activate) })
	})
	return serveHTTP(ctx, c, local, public, false, activate, nil)
}

// A missing outbounds fragment alone does not prove an unconfigured state: a
// partial manual recovery can leave managed rules in the user's routing file.
func noTrialRoutingArtifacts(c config.Config) error {
	entries, err := os.ReadDir(c.Xray.ConfigDir)
	if err != nil {
		return err
	}
	owned := func(tag string) bool {
		return tag != "" && (tag == c.Xray.APITag || tag == c.Xray.BalancerTag || tag == c.Xray.ManagedDirectTag || tag == "krm-health" || strings.HasPrefix(tag, "krm-probe-") || strings.HasPrefix(tag, c.Xray.SlotTagPrefix) || strings.HasPrefix(tag, config.XraySelectionTag))
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		f, err := os.Open(filepath.Join(c.Xray.ConfigDir, entry.Name()))
		if err != nil {
			return err
		}
		var document struct {
			API struct {
				Tag string `json:"tag"`
			} `json:"api"`
			Inbounds []struct {
				Tag string `json:"tag"`
			} `json:"inbounds"`
			Outbounds []struct {
				Tag string `json:"tag"`
			} `json:"outbounds"`
			Routing struct {
				Balancers []struct {
					Tag string `json:"tag"`
				} `json:"balancers"`
				Rules []struct {
					BalancerTag string   `json:"balancerTag"`
					OutboundTag string   `json:"outboundTag"`
					InboundTag  []string `json:"inboundTag"`
				} `json:"rules"`
			} `json:"routing"`
		}
		d := json.NewDecoder(io.LimitReader(f, (64<<20)+1))
		err = d.Decode(&document)
		if err == nil && d.Decode(new(any)) != io.EOF {
			err = fmt.Errorf("trailing routing JSON")
		}
		f.Close()
		if err != nil {
			return fmt.Errorf("invalid existing Xray JSON")
		}
		if owned(document.API.Tag) {
			return fmt.Errorf("unconfigured controller retains managed API")
		}
		for _, in := range document.Inbounds {
			if owned(in.Tag) {
				return fmt.Errorf("unconfigured controller retains managed inbound")
			}
		}
		for _, out := range document.Outbounds {
			if owned(out.Tag) {
				return fmt.Errorf("unconfigured controller retains managed outbound")
			}
		}
		for _, balancer := range document.Routing.Balancers {
			if owned(balancer.Tag) {
				return fmt.Errorf("unconfigured controller retains managed balancer")
			}
		}
		for _, rule := range document.Routing.Rules {
			if owned(rule.BalancerTag) || owned(rule.OutboundTag) {
				return fmt.Errorf("unconfigured controller retains managed rule")
			}
			for _, tag := range rule.InboundTag {
				if owned(tag) {
					return fmt.Errorf("unconfigured controller retains managed inbound rule")
				}
			}
		}
	}
	return nil
}
