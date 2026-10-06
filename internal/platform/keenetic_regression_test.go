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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
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
				case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
					return platformRegressionResponse("ip hotspot\n"), nil
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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := k.waitConfigurationSaved(ctx, runningChecksum); err != nil {
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
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}
	start := time.Now()
	err := k.waitConfigurationSaved(context.Background(), runningChecksum)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("save wait deadline not reported: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("save wait exceeded bounded timeout: %v", time.Since(start))
	}
}

func TestKeeneticWaitConfigurationSavedRejectsRunningDrift(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{Timeout: time.Second}).(*keenetic)
	expectedChecksum := regressionChecksum(2)
	runningChecksum := expectedChecksum
	savedChecksum := regressionChecksum(1)
	polls := 0
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			polls++
			if polls >= 2 {
				runningChecksum = regressionChecksum(3)
			}
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.waitConfigurationSaved(context.Background(), expectedChecksum)
	if err == nil || !strings.Contains(err.Error(), "configuration drift") {
		t.Fatalf("running drift accepted while waiting for save: %v", err)
	}
}

func TestRegressionKeeneticPolicyRejectsDriftBeforeSave(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "access": "permit"}
	runningChecksum := regressionChecksum(1)
	savedChecksum := runningChecksum
	checksumReads := 0
	saves := 0
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
			if policy, ok := body["policy"].(string); ok {
				state["policy"] = policy
				runningChecksum = regressionChecksum(2)
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
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			checksumReads++
			if checksumReads >= 4 {
				// Simulate an unrelated writer changing running config after KRM
				// captured the checksum it intends to persist.
				runningChecksum = regressionChecksum(3)
			}
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
			savedChecksum = runningChecksum
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(context.Background(), mac, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "changed before save") {
		t.Fatalf("drift before save was not rejected: %v", err)
	}
	if saves != 0 {
		t.Fatalf("drifted configuration was persisted: saves=%d", saves)
	}
}

func TestRegressionKeeneticRollbackRefusesToSaveExternalDrift(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "policy": "Policy2", "access": "permit"}
	savedChecksum := regressionChecksum(1)
	runningChecksum := savedChecksum
	applyFailed := false
	saves := 0
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
			body, _ := json.Marshal(map[string]any{"host": []any{state}})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
			body, _ := json.Marshal(map[string]any{"checksum": runningChecksum})
			return platformRegressionResponse(string(body)), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
			return platformRegressionResponse("ip hotspot\n"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
			return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			if policy, ok := body["policy"].(string); ok {
				state["policy"] = policy
				if policy == "Policy0" && !applyFailed {
					applyFailed = true
					runningChecksum = regressionChecksum(2)
					return nil, errors.New("apply response lost")
				}
				if policy == "Policy2" {
					// Host state is restored, but an unrelated concurrent change keeps
					// the whole running config different from the original checksum.
					runningChecksum = regressionChecksum(3)
				}
			}
			if conform, ok := body["conform"].(bool); ok {
				state["conform"] = conform
			}
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(context.Background(), mac, "xkeen")
	if err == nil || !strings.Contains(err.Error(), "rollback") || !strings.Contains(err.Error(), "configuration drift") {
		t.Fatalf("external drift during rollback was not reported: %v", err)
	}
	if saves != 0 {
		t.Fatalf("rollback persisted external drift: saves=%d", saves)
	}
}

func TestRegressionKeeneticPolicyRejectsExternalDriftDuringMutation(t *testing.T) {
	for _, timing := range []string{"mutation", "baseline"} {
		t.Run(timing, func(t *testing.T) {
			const mac = "00:11:22:33:44:55"
			c := config.Default()
			c.Platform.Kind = "keenetic"
			c.Platform.Keenetic.AllowPolicyChange = true
			c.Platform.Keenetic.XKeenPolicyName = "XKeen"
			k := newKeenetic(c, Runner{}).(*keenetic)

			state := map[string]any{"mac": mac, "conform": true, "access": "permit"}
			savedChecksum := regressionChecksum(1)
			runningChecksum := savedChecksum
			externalDrift := false
			saves := 0
			writes := 0
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/rci/ip/hotspot":
					body, _ := json.Marshal(map[string]any{"host": []any{state}})
					return platformRegressionResponse(string(body)), nil
				case r.Method == http.MethodGet && r.URL.Path == "/rci/show/ip/policy":
					return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
				case r.Method == http.MethodGet && r.URL.Path == "/rci/show/last-change":
					body, _ := json.Marshal(map[string]any{"checksum": runningChecksum})
					return platformRegressionResponse(string(body)), nil
				case r.Method == http.MethodGet && r.URL.Path == "/ci/startup-config.txt":
					return platformRegressionResponse(regressionStartupConfig(savedChecksum)), nil
				case r.Method == http.MethodGet && r.URL.Path == "/ci/running-config.txt":
					if timing == "baseline" && !externalDrift {
						externalDrift = true
						runningChecksum = regressionChecksum(3)
					}
					config := "ip hotspot\n    host " + mac + " permit\n"
					if policy := stringValue(state["policy"]); policy != "" {
						config += "    host " + mac + " policy " + policy + "\n"
					}
					if truth(state["conform"]) {
						config += "    host " + mac + " conform\n"
					}
					if externalDrift {
						config += "ip name-server 1.1.1.1\n"
					}
					return platformRegressionResponse(config), nil
				case r.Method == http.MethodPost && r.URL.Path == "/rci/ip/hotspot/host":
					writes++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					if policy, ok := body["policy"].(string); ok {
						state["policy"] = policy
						if policy == "Policy0" {
							externalDrift = true
							runningChecksum = regressionChecksum(2)
						} else {
							runningChecksum = regressionChecksum(3)
						}
					}
					if policy, ok := body["policy"].(map[string]any); ok && truth(policy["no"]) {
						delete(state, "policy")
						runningChecksum = regressionChecksum(3)
					}
					if conform, ok := body["conform"].(bool); ok {
						state["conform"] = conform
					}
					if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
						state["conform"] = false
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

			err := k.SetClientPolicy(context.Background(), mac, "xkeen")
			if err == nil || !strings.Contains(err.Error(), "external configuration drift") {
				t.Fatalf("external drift during mutation was not rejected: %v", err)
			}
			if timing == "baseline" && writes != 0 {
				t.Fatalf("baseline drift must be rejected before host writes: writes=%d", writes)
			}
			if saves != 0 {
				t.Fatalf("external drift was persisted: saves=%d", saves)
			}
		})
	}
}
func TestRegressionKeeneticPolicyRollbackWaitsForInflightSave(t *testing.T) {
	for _, mode := range []string{"delayed", "wait_canceled", "never_confirmed", "not_accepted", "before_send", "startup_drift", "running_drift"} {
		t.Run(mode, func(t *testing.T) {
			const mac = "00:11:22:33:44:55"
			c := config.Default()
			c.Platform.Keenetic.AllowPolicyChange = true
			timeout := 2 * time.Second
			if mode == "never_confirmed" || mode == "not_accepted" {
				timeout = 50 * time.Millisecond
			}
			k := newKeenetic(c, Runner{Timeout: timeout}).(*keenetic)
			state := map[string]any{"mac": mac, "conform": true, "policy": "Policy2", "access": "permit"}
			original := regressionChecksum(1)
			mutation := regressionChecksum(2)
			saved, pending := original, ""
			saves, recoveryReads := 0, 0
			mutationReads, recoverySaveReads := 0, 0
			checksum := func() string {
				if mode == "running_drift" && recoveryReads >= 2 {
					return regressionChecksum(3)
				}
				if truth(state["conform"]) && stringValue(state["policy"]) == "Policy2" {
					return original
				}
				return mutation
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				switch r.URL.Path {
				case "/rci/ip/hotspot":
					body, _ := json.Marshal(map[string]any{"host": []any{state}})
					return platformRegressionResponse(string(body)), nil
				case "/rci/show/ip/policy":
					return platformRegressionResponse(`{"Policy0":{"description":"XKeen"}}`), nil
				case "/rci/show/last-change":
					if mode == "wait_canceled" && saves == 1 && checksum() == mutation {
						cancel()
					}
					if checksum() == mutation && saves == 0 {
						mutationReads++
						if mode == "before_send" && mutationReads == 2 {
							cancel()
						}
					}
					body, _ := json.Marshal(map[string]any{"checksum": checksum()})
					return platformRegressionResponse(string(body)), nil
				case "/ci/running-config.txt":
					return platformRegressionResponse("ip hotspot\n"), nil
				case "/ci/startup-config.txt":
					if saves == 1 {
						recoveryReads++
						// The first rollback read still sees the old startup config.
						if recoveryReads == 2 {
							switch mode {
							case "delayed", "wait_canceled", "running_drift":
								saved, pending = pending, ""
							case "startup_drift":
								saved = regressionChecksum(3)
							}
						}
					}
					if saves == 2 {
						recoverySaveReads++
						if recoverySaveReads == 2 {
							saved, pending = pending, ""
						}
					}
					return platformRegressionResponse(regressionStartupConfig(saved)), nil
				case "/rci/ip/hotspot/host":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					if policy, ok := body["policy"].(string); ok {
						state["policy"] = policy
					}
					if conform, ok := body["conform"].(bool); ok {
						state["conform"] = conform
					}
					if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
						state["conform"] = false
					}
					return platformRegressionResponse(`{}`), nil
				case "/rci/system/configuration/save":
					saves++
					if saves == 1 {
						if mode != "not_accepted" {
							pending = checksum()
						}
						if mode == "wait_canceled" {
							return platformRegressionResponse(`{}`), nil
						}
						cancel()
						return nil, context.Canceled
					}
					if pending != "" {
						t.Fatal("recovery save raced with the preceding save")
					}
					// Confirmation must wait for this recovery save as well.
					pending = checksum()
					return platformRegressionResponse(`{}`), nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
				}
			})}
			start := time.Now()
			err := k.SetClientPolicy(ctx, mac, "xkeen")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation not reported: %v", err)
			}
			if !truth(state["conform"]) || stringValue(state["policy"]) != "Policy2" {
				t.Fatalf("runtime not restored: %#v", state)
			}
			if mode == "before_send" {
				if saves != 0 || saved != original || strings.Contains(err.Error(), "rollback") {
					t.Fatalf("cancellation before send started a save: saves=%d err=%v", saves, err)
				}
			} else if mode == "delayed" || mode == "wait_canceled" {
				if saves != 2 || saved != original || pending != "" || strings.Contains(err.Error(), "rollback") {
					t.Fatalf("inflight save not reconciled: saves=%d saved=%s pending=%s err=%v", saves, saved, pending, err)
				}
			} else {
				if saves != 1 || !strings.Contains(err.Error(), "client policy rollback") {
					t.Fatalf("unconfirmed or foreign revision was saved: saves=%d err=%v", saves, err)
				}
				if (mode == "never_confirmed" || mode == "not_accepted") && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unconfirmed save deadline not reported: %v", err)
				}
			}
			if time.Since(start) > timeout+time.Second {
				t.Fatal("rollback exceeded recovery deadline")
			}
		})
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
