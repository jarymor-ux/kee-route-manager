package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type platformRegressionRT func(*http.Request) (*http.Response, error)

func (f platformRegressionRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func platformRegressionResponse(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: make(http.Header)}
}

func regressionChecksum(n int) string { return fmt.Sprintf("%032x", n) }
func regressionStartupConfig(checksum string) string {
	return "! $$ Model: Keenetic\n! $$ Md5 checksum: " + checksum + "\n"
}

func TestRegressionWANDisconnected(t *testing.T) {
	for _, connected := range []bool{true, false} {
		t.Run(fmt.Sprint(connected), func(t *testing.T) {
			k := newKeenetic(config.Default(), Runner{}).(*keenetic)
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/rci/show/system":
					return platformRegressionResponse(`{"uptime":1234,"memory":"1024/2048","cpuload":5}`), nil
				case "/rci/show/interface":
					status := "no"
					if connected {
						status = "yes"
					}
					return platformRegressionResponse(`{"wan":{"id":"ISP","connected":"` + status + `","defaultgw":true}}`), nil
				default:
					return platformRegressionResponse(`{}`), nil
				}
			})}
			m, err := k.Metrics(context.Background())
			t.Logf("connected=%v returned_WAN=%v uptime=%v error=%v", connected, m.WANConnected, m.UptimeSeconds, err)
			if connected {
				if err != nil || m.WANConnected == nil || !*m.WANConnected || m.UptimeSeconds != 1234 {
					t.Fatal("negative control failed")
				}
			} else if err != nil || m.WANConnected == nil || *m.WANConnected || m.UptimeSeconds != 1234 {
				t.Fatalf("disconnected WAN discarded fresh metrics: %+v %v", m, err)
			}
		})
	}
}

func TestRegressionKeeneticPolicyMutationsUseRCI(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "access": "permit"}
	var posts []map[string]any
	saves := 0
	revision := 1
	runningChecksum := regressionChecksum(revision)
	savedChecksum := runningChecksum
	touch := func() {
		revision++
		runningChecksum = regressionChecksum(revision)
	}
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
			body, _ := json.Marshal(map[string]any{"host": []any{state}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			posts = append(posts, body)
			touch()
			if body["mac"] != mac {
				t.Fatalf("unexpected mac: %#v", body)
			}
			if policy, ok := body["policy"].(string); ok {
				state["policy"] = policy
			}
			if policy, ok := body["policy"].(map[string]any); ok && truth(policy["no"]) {
				delete(state, "policy")
			}
			if conform, ok := body["conform"].(bool); ok {
				state["conform"] = conform
			}
			if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
				state["conform"] = false
			}
			return platformRegressionResponse(`{"status":[{"status":"message","message":"ok"}]}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{
				"checksum":  runningChecksum,
				"fail-safe": map[string]any{"unsaved": false},
			})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
			savedChecksum = runningChecksum
			return platformRegressionResponse(`{"status":[{"status":"message","message":"saving"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	if err := k.SetClientPolicy(context.Background(), mac, "xkeen"); err != nil {
		t.Fatal(err)
	}
	if truth(state["conform"]) || stringValue(state["policy"]) != "Policy0" || stringValue(state["access"]) != "permit" {
		t.Fatalf("xkeen state=%#v", state)
	}
	if saves != 1 || len(posts) != 2 {
		t.Fatalf("xkeen writes=%d saves=%d posts=%#v", len(posts), saves, posts)
	}
	if posts[0]["policy"] != "Policy0" {
		t.Fatalf("xkeen must stage Policy0 before changing effective routing: %#v", posts[0])
	}
	if conform, ok := posts[1]["conform"].(map[string]any); !ok || !truth(conform["no"]) {
		t.Fatalf("xkeen must remove conform only after staging Policy0: %#v", posts[1])
	}

	posts = nil
	if err := k.SetClientPolicy(context.Background(), mac, "default"); err != nil {
		t.Fatal(err)
	}
	if !truth(state["conform"]) || stringValue(state["policy"]) != "" || stringValue(state["access"]) != "permit" {
		t.Fatalf("default state=%#v", state)
	}
	if saves != 2 || len(posts) != 2 {
		t.Fatalf("default writes=%d saves=%d posts=%#v", len(posts), saves, posts)
	}
	if posts[0]["conform"] != true {
		t.Fatalf("default must enable conform before removing latent policy: %#v", posts[0])
	}
	if policy, ok := posts[1]["policy"].(map[string]any); !ok || !truth(policy["no"]) {
		t.Fatalf("default must remove explicit policy only after conform is enabled: %#v", posts[1])
	}

	// Repeating an already-satisfied request is a pure no-op. It must never save
	// unrelated pending configuration just because the router happens to be dirty.
	posts = nil
	savedChecksum = regressionChecksum(999)
	if err := k.SetClientPolicy(context.Background(), mac, "default"); err != nil {
		t.Fatal(err)
	}
	if saves != 2 || len(posts) != 0 {
		t.Fatalf("no-op policy request wrote router state: posts=%#v saves=%d", posts, saves)
	}
}

func TestRegressionKeeneticPolicyRefusesPreexistingUnsavedConfiguration(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	writes := 0
	runningChecksum := regressionChecksum(2)
	savedChecksum := regressionChecksum(1)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
			return platformRegressionResponse(`{"host":[{"mac":"00:11:22:33:44:55","conform":true,"access":"permit"}]}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum, "fail-safe": map[string]any{"unsaved": false}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost:
			writes++
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(context.Background(), mac, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "pre-existing unsaved") {
		t.Fatalf("dirty router configuration was not rejected: %v", err)
	}
	if writes != 0 {
		t.Fatalf("dirty router configuration was mutated: writes=%d", writes)
	}
}

func TestRegressionKeeneticPolicyRollbackUsesRCIAfterCancellation(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "policy": "Policy2", "access": "permit"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	policyWrite := 0
	rollbackWrites := 0
	saves := 0
	savedChecksum := regressionChecksum(1)
	runningChecksum := savedChecksum
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
			body, _ := json.Marshal(map[string]any{"host": []any{state}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum, "fail-safe": map[string]any{"unsaved": false}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
				state["conform"] = false
				return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
			}
			if policy, ok := body["policy"].(string); ok {
				state["policy"] = policy
				if policy == "Policy0" && policyWrite == 0 {
					policyWrite++
					runningChecksum = regressionChecksum(2)
					cancel()
					return nil, context.Canceled
				}
				if policy == "Policy2" {
					runningChecksum = savedChecksum
				}
				rollbackWrites++
				return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
			}
			if policy, ok := body["policy"].(map[string]any); ok && truth(policy["no"]) {
				delete(state, "policy")
				rollbackWrites++
			}
			if permit, ok := body["permit"].(bool); ok && permit {
				state["access"] = "permit"
				rollbackWrites++
			}
			if conform, ok := body["conform"].(bool); ok && conform {
				state["conform"] = true
				rollbackWrites++
			}
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
			savedChecksum = runningChecksum
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(ctx, mac, "xkeen")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reported: %v", err)
	}
	if policyWrite != 1 || rollbackWrites != 1 {
		t.Fatalf("rollback did not restore through RCI: policyWrite=%d rollbackWrites=%d", policyWrite, rollbackWrites)
	}
	if saves != 0 {
		t.Fatalf("rollback saved even though startup config was already original: saves=%d", saves)
	}
	if !truth(state["conform"]) || stringValue(state["policy"]) != "Policy2" || stringValue(state["access"]) != "permit" {
		t.Fatalf("rollback state=%#v", state)
	}
}

func TestRegressionKeeneticPolicyRollbackDoesNotSaveAfterLostResponseWhenStartupIsOriginal(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "policy": "Policy2", "access": "permit"}
	applyFailed := false
	rollbackResponseLost := false
	saves := 0
	savedChecksum := regressionChecksum(1)
	runningChecksum := savedChecksum
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
			body, _ := json.Marshal(map[string]any{"host": []any{state}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum, "fail-safe": map[string]any{"unsaved": false}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
				state["conform"] = false
				return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
			}
			if policy, ok := body["policy"].(string); ok {
				state["policy"] = policy
				if policy == "Policy0" && !applyFailed {
					applyFailed = true
					runningChecksum = regressionChecksum(2)
					return nil, errors.New("apply response lost")
				}
				if policy == "Policy2" && !rollbackResponseLost {
					rollbackResponseLost = true
					runningChecksum = savedChecksum
					return nil, errors.New("rollback response lost")
				}
				return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
			}
			if conform, ok := body["conform"].(bool); ok && conform {
				state["conform"] = true
			}
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
			savedChecksum = runningChecksum
			return platformRegressionResponse(`{"status":[{"status":"message","message":"saving"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(context.Background(), mac, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "apply response lost") {
		t.Fatalf("original mutation error not reported: %v", err)
	}
	if strings.Contains(err.Error(), "client policy rollback") {
		t.Fatalf("verified rollback treated as failed: %v", err)
	}
	if !rollbackResponseLost {
		t.Fatal("rollback response-loss path was not exercised")
	}
	if saves != 0 {
		t.Fatalf("verified rollback persisted unrelated config: saves=%d", saves)
	}
	if !truth(state["conform"]) || stringValue(state["policy"]) != "Policy2" || stringValue(state["access"]) != "permit" {
		t.Fatalf("verified rollback state=%#v", state)
	}
}

func TestRegressionKeeneticPolicyRollbackFailuresAreReported(t *testing.T) {
	for _, mode := range []string{"write_failure", "restore_drift", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			const mac = "00:11:22:33:44:55"
			c := config.Default()
			c.Platform.Kind = "keenetic"
			c.Platform.Keenetic.AllowPolicyChange = true
			c.Platform.Keenetic.XKeenPolicyName = "XKeen"
			timeout := time.Second
			if mode == "deadline" {
				timeout = 30 * time.Millisecond
			}
			k := newKeenetic(c, Runner{Timeout: timeout}).(*keenetic)
			state := map[string]any{"mac": mac, "conform": true, "policy": "Policy2", "access": "permit"}
			applyFailed := false
			saves := 0
			savedChecksum := regressionChecksum(1)
			runningChecksum := savedChecksum
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
					body, _ := json.Marshal(map[string]any{"host": []any{state}})
					return platformRegressionResponse(string(body)), nil
				case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
					return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
				case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
					body, _ := json.Marshal(map[string]any{"checksum": runningChecksum, "fail-safe": map[string]any{"unsaved": false}})
					return platformRegressionResponse(string(body)), nil
				case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
					return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
				case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
						state["conform"] = false
						return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
					}
					if policy, ok := body["policy"].(string); ok {
						if policy == "Policy0" && !applyFailed {
							state["policy"] = policy
							runningChecksum = regressionChecksum(2)
							applyFailed = true
							return nil, errors.New("apply response lost")
						}
						if policy == "Policy2" {
							switch mode {
							case "write_failure":
								return nil, errors.New("recovery-write-failed")
							case "restore_drift":
								return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
							case "deadline":
								<-r.Context().Done()
								return nil, r.Context().Err()
							}
						}
						state["policy"] = policy
						if policy == "Policy2" {
							runningChecksum = savedChecksum
						}
						return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
					}
					if conform, ok := body["conform"].(bool); ok && conform {
						state["conform"] = true
					}
					return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
				case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
					saves++
					savedChecksum = runningChecksum
					return platformRegressionResponse(`{"status":[{"status":"message","message":"saving"}]}`), nil
				default:
					return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
				}
			})}

			start := time.Now()
			err := k.SetClientPolicy(context.Background(), mac, "xkeen")
			if err == nil || !strings.Contains(err.Error(), "client policy rollback") {
				t.Fatalf("rollback failure hidden: %v", err)
			}
			if saves != 0 {
				t.Fatalf("unverified rollback must not be persisted, saves=%d", saves)
			}
			switch mode {
			case "write_failure":
				if !strings.Contains(err.Error(), "recovery-write-failed") {
					t.Fatalf("rollback write failure hidden: %v", err)
				}
			case "restore_drift":
				if !strings.Contains(err.Error(), "did not restore") {
					t.Fatalf("rollback verification drift hidden: %v", err)
				}
			case "deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("rollback deadline hidden: %v", err)
				}
				if time.Since(start) > time.Second {
					t.Fatalf("rollback exceeded bounded recovery timeout: %v", time.Since(start))
				}
			}
		})
	}
}

func TestKeeneticWaitConfigurationSavedUsesRunningAndStartupChecksums(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{}).(*keenetic)
	polls := 0
	runningChecksum := regressionChecksum(2)
	savedChecksum := regressionChecksum(1)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			polls++
			if polls == 3 {
				savedChecksum = runningChecksum
			}
			body, _ := json.Marshal(map[string]any{
				"checksum":  runningChecksum,
				"fail-safe": map[string]any{"unsaved": false},
			})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := k.waitConfigurationSaved(ctx); err != nil {
		t.Fatal(err)
	}
	if polls != 3 {
		t.Fatalf("save confirmation did not wait for startup checksum: polls=%d", polls)
	}
}

func TestKeeneticWaitConfigurationSavedDeadline(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{Timeout: 20 * time.Millisecond}).(*keenetic)
	runningChecksum := regressionChecksum(2)
	savedChecksum := regressionChecksum(1)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{
				"checksum":  runningChecksum,
				"fail-safe": map[string]any{"unsaved": false},
			})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}
	start := time.Now()
	err := k.waitConfigurationSaved(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("save wait deadline not reported: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("save wait exceeded bounded timeout: %v", time.Since(start))
	}
}

func TestRCIPostRejectsNestedStatusError(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{}).(*keenetic)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		return platformRegressionResponse(`{"policy":{"status":[{"status":"error","code":"6553609","message":"rejected"}]}}`), nil
	})}
	if _, err := k.rciPost(context.Background(), "ip/hotspot/host", map[string]any{"mac": "00:11:22:33:44:55"}); err == nil || !strings.Contains(err.Error(), "RCI status error") {
		t.Fatalf("nested RCI error accepted: %v", err)
	}
}

func TestWANMetricsRejectUnsafeConnectedInterfaceControl(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{}).(*keenetic)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rci/show/interface" {
			return platformRegressionResponse(`{"wan":{"id":"bad?name","connected":"yes","defaultgw":true}}`), nil
		}
		return platformRegressionResponse(`{}`), nil
	})}
	if _, err := k.Metrics(context.Background()); err == nil {
		t.Fatal("unsafe interface id accepted")
	}
}
