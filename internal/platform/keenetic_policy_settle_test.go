package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

const settlePolicyMAC = "00:11:22:33:44:55"

type policySettleFixture struct {
	mu                                                     sync.Mutex
	k                                                      *keenetic
	policy                                                 string
	conform, foreign, stallMutation, cancelFirstSave       bool
	changeHostBeforeSave, foreignStartupOnSave, injected   bool
	running, saved, savedText, original, pending           string
	revisions                                              map[string]string
	pendingReads, applyReads, rollbackReads, saves, writes int
	settledConfigReads                                     int
	cancel                                                 context.CancelFunc
}

func (f *policySettleFixture) configText() string {
	text := "hostname fixture\nip hotspot\n    host " + settlePolicyMAC + " permit\n"
	if f.policy != "" {
		text += "    host " + settlePolicyMAC + " policy " + f.policy + "\n"
	}
	if f.conform {
		text += "    host " + settlePolicyMAC + " conform\n"
	}
	if f.foreign {
		text += "ip name-server 192.0.2.53\n"
	}
	return text
}

func (f *policySettleFixture) revision() string {
	text := f.configText()
	if checksum, ok := f.revisions[text]; ok {
		return checksum
	}
	checksum := regressionChecksum(len(f.revisions) + 1)
	f.revisions[text] = checksum
	return checksum
}

func newPolicySettleFixture(t *testing.T, timeout time.Duration) *policySettleFixture {
	t.Helper()
	f := &policySettleFixture{policy: "Policy2", conform: true, revisions: make(map[string]string)}
	f.original = f.revision()
	f.running, f.saved, f.savedText = f.original, f.original, f.configText()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/rci/ip/hotspot":
			_ = json.NewEncoder(w).Encode(map[string]any{"host": []any{map[string]any{"mac": settlePolicyMAC, "policy": f.policy, "conform": f.conform, "access": "permit"}}})
		case "/rci/show/ip/policy":
			_, _ = fmt.Fprint(w, `{"Policy0":{"description":"XKeen"}}`)
		case "/rci/show/last-change":
			if f.pending != "" {
				f.pendingReads++
				if f.pending == f.original {
					f.rollbackReads++
				} else {
					f.applyReads++
				}
				if f.pendingReads >= 3 && (!f.stallMutation || f.pending == f.original) {
					f.running, f.pending = f.pending, ""
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"checksum": f.running})
		case "/ci/running-config.txt":
			if f.changeHostBeforeSave && f.pending == "" && f.policy == "Policy0" && !f.conform && !f.injected {
				f.settledConfigReads++
				// The first settled read supplies the trusted mutation snapshot;
				// change only this host before the subsequent final-save reads.
				if f.settledConfigReads == 2 {
					f.injected, f.policy = true, "Policy9"
					f.pending, f.pendingReads = f.revision(), 0
				}
			}
			_, _ = fmt.Fprint(w, regressionStartupConfig(f.running)+f.configText())
		case "/ci/startup-config.txt":
			_, _ = fmt.Fprint(w, regressionStartupConfig(f.saved)+f.savedText)
		case "/rci/ip/hotspot/host":
			f.writes++
			var body map[string]any
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || stringValue(body["mac"]) != settlePolicyMAC {
				http.Error(w, "invalid fixture write", http.StatusBadRequest)
				return
			}
			before := f.configText()
			if value, ok := body["policy"].(string); ok {
				f.policy = value
			}
			if value, ok := body["policy"].(map[string]any); ok && truth(value["no"]) {
				f.policy = ""
			}
			if value, ok := body["conform"].(bool); ok {
				f.conform = value
			}
			if value, ok := body["conform"].(map[string]any); ok && truth(value["no"]) {
				f.conform = false
			}
			if before != f.configText() {
				f.pending, f.pendingReads = f.revision(), 0
			}
			_, _ = fmt.Fprint(w, `{}`)
		case "/rci/system/configuration/save":
			if r.Method != http.MethodPost {
				t.Error("save must use POST")
			}
			f.saves++
			if f.running != f.revision() {
				t.Error("global save ran before the policy configuration checksum settled")
			}
			f.saved, f.savedText = f.running, f.configText()
			if f.foreignStartupOnSave && f.saves == 1 {
				// Saved command publication can race its checksum metadata; keep
				// the owned header while an unrelated saved command is visible.
				f.savedText += "ip name-server 192.0.2.53\n"
			}
			if f.cancelFirstSave && f.saves == 1 {
				f.cancel()
			}
			_, _ = fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected fixture request %s", r.URL.Path)
			http.Error(w, "unexpected fixture request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Paths.StateDir = t.TempDir()
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	c.Platform.Keenetic.RCIBaseURL = server.URL + "/rci"
	f.k = newKeenetic(c, Runner{Timeout: timeout}).(*keenetic)
	return f
}

func TestKeeneticPolicyWaitsForDelayedChecksumBeforeSaving(t *testing.T) {
	f := newPolicySettleFixture(t, 2*time.Second)
	if err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if f.policy != "Policy0" || f.conform || f.applyReads < 3 || f.saves != 1 || f.saved != f.running || f.savedText != f.configText() {
		t.Error("delayed policy revision or full startup configuration was not confirmed")
	}
	f.mu.Unlock()
	// Returning to segment routing is also a delayed configuration change.
	if err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "default"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.policy != "" || !f.conform || f.saves != 2 || f.saved != f.running || f.savedText != f.configText() {
		t.Fatal("default policy was not confirmed in full startup configuration")
	}
}

func TestKeeneticPolicyCancellationWaitsForDelayedRollbackChecksum(t *testing.T) {
	f := newPolicySettleFixture(t, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.cancelFirstSave, f.cancel = true, cancel
	err := f.k.SetClientPolicy(ctx, settlePolicyMAC, "xkeen")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "client policy rollback") {
		t.Fatalf("canceled operation did not complete bounded rollback: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.policy != "Policy2" || !f.conform || f.rollbackReads < 3 || f.saves != 2 || f.running != f.original || f.saved != f.original || f.savedText != f.configText() {
		t.Fatal("cancellation did not restore and persist the original policy after delayed checksum publication")
	}
}

func TestKeeneticPolicyStableUnchangedChecksumTimesOutWithoutSave(t *testing.T) {
	f := newPolicySettleFixture(t, 120*time.Millisecond)
	f.stallMutation = true
	started := time.Now()
	err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unchanged checksum must fail with a bounded deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("checksum wait exceeded its bounded timeout")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saves != 0 || f.saved != f.original || f.policy != "Policy2" || !f.conform {
		t.Fatal("unchanged checksum led to global save or failed runtime restoration")
	}
}

func TestKeeneticPolicySettlingRejectsForeignConfiguration(t *testing.T) {
	f := newPolicySettleFixture(t, time.Second)
	// Change an unowned field when a host mutation has become visible, while
	// show/last-change still publishes the old revision.
	originalTransport := f.k.http.Transport
	if originalTransport == nil {
		originalTransport = http.DefaultTransport
	}
	f.k.http.Transport = platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ci/running-config.txt" {
			f.mu.Lock()
			if f.policy == "Policy0" {
				f.foreign = true
				f.pending, f.pendingReads = f.revision(), 0
			}
			f.mu.Unlock()
		}
		return originalTransport.RoundTrip(r)
	})
	err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "configuration drift") {
		t.Fatalf("foreign configuration was accepted during checksum settlement: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saves != 0 || f.saved != f.original || !f.foreign {
		t.Fatal("checksum waiting saved or removed foreign configuration")
	}
}

func TestKeeneticPolicyPendingDNSIntentRefusesBeforeRouterAccess(t *testing.T) {
	f := newPolicySettleFixture(t, time.Second)
	journal := f.k.panelDNSJournal()
	data := []byte(`{"schema":1,"phase":"prepared"}`)
	if err := os.WriteFile(journal, data, 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	f.k.http.Transport = platformRegressionRT(func(_ *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("policy guard unexpectedly accessed router")
	})
	if err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen"); !errors.Is(err, ErrPanelDNSPending) {
		t.Fatalf("pending DNS transaction did not block client policy: %v", err)
	}
	if requests != 0 {
		t.Fatal("pending DNS policy guard performed router requests")
	}
	after, err := os.ReadFile(journal)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("client policy changed the unresolved DNS journal")
	}
	info, err := os.Stat(journal)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unresolved DNS journal lost private permissions")
	}
}

func TestKeeneticPolicyRejectsUnsavedContentBeforeChecksumPublication(t *testing.T) {
	f := newPolicySettleFixture(t, time.Second)
	// The foreign command is already visible in running-config, but both
	// last-change and startup still publish the original saved checksum.
	f.foreign = true
	err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen")
	if err == nil {
		t.Fatal("pre-existing unsaved content was silently adopted as the policy baseline")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 || f.saves != 0 || f.policy != "Policy2" || !f.conform || !f.foreign || f.saved != f.original {
		t.Fatal("unsaved content with stale checksum caused host writes or a global save")
	}
}

func TestKeeneticPolicyFinalSaveRejectsSameHostEditWithLaggingChecksum(t *testing.T) {
	f := newPolicySettleFixture(t, time.Second)
	f.changeHostBeforeSave = true
	err := f.k.SetClientPolicy(context.Background(), settlePolicyMAC, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "configuration drift") {
		t.Fatalf("final save adopted a changed host despite its old checksum: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.injected || f.saves != 0 || f.saved != f.original {
		t.Fatal("changed host was persisted by the final policy save")
	}
}

func TestKeeneticPolicyCanceledSaveRejectsForeignStartupWithOwnedHeader(t *testing.T) {
	f := newPolicySettleFixture(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.cancelFirstSave, f.foreignStartupOnSave, f.cancel = true, true, cancel
	err := f.k.SetClientPolicy(ctx, settlePolicyMAC, "xkeen")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "startup configuration drift") {
		t.Fatalf("rollback accepted a foreign saved body with an owned checksum header: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saves != 1 || !strings.Contains(f.savedText, "ip name-server 192.0.2.53") || f.policy != "Policy2" || !f.conform {
		t.Fatal("rollback globally saved over foreign startup content or failed to restore host runtime")
	}
}
