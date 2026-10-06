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

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type platformRegressionRT func(*http.Request) (*http.Response, error)

func (f platformRegressionRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func platformRegressionResponse(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: make(http.Header)}
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
		case r.Method == http.MethodPost && r.URL.Path == "/rci/system/configuration/save":
			saves++
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
	if conform, ok := posts[0]["conform"].(map[string]any); !ok || !truth(conform["no"]) {
		t.Fatalf("xkeen must remove conform first: %#v", posts[0])
	}
	if posts[1]["policy"] != "Policy0" {
		t.Fatalf("xkeen must assign Policy0: %#v", posts[1])
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
	if policy, ok := posts[0]["policy"].(map[string]any); !ok || !truth(policy["no"]) {
		t.Fatalf("default must remove explicit policy: %#v", posts[0])
	}
	if posts[1]["conform"] != true {
		t.Fatalf("default must enable conform: %#v", posts[1])
	}
}

func TestRegressionKeeneticPolicyRollbackUsesRCIAfterCancellation(t *testing.T) {
	const mac = "00:11:22:33:44:55"
	c := config.Default()
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	c.Platform.Keenetic.XKeenPolicyName = "XKeen"
	k := newKeenetic(c, Runner{}).(*keenetic)

	state := map[string]any{"mac": mac, "conform": true, "access": "permit"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	policyWrite := 0
	rollbackWrites := 0
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
			if conform, ok := body["conform"].(map[string]any); ok && truth(conform["no"]) {
				state["conform"] = false
				return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
			}
			if _, ok := body["policy"].(string); ok {
				policyWrite++
				cancel()
				return nil, context.Canceled
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
			rollbackWrites++
			return platformRegressionResponse(`{"status":[{"status":"message"}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI request %s %s", r.Method, r.URL.Path)
		}
	})}

	err := k.SetClientPolicy(ctx, mac, "xkeen")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reported: %v", err)
	}
	if policyWrite != 1 || rollbackWrites < 2 {
		t.Fatalf("rollback did not run through RCI: policyWrite=%d rollbackWrites=%d", policyWrite, rollbackWrites)
	}
	if !truth(state["conform"]) || stringValue(state["policy"]) != "" || stringValue(state["access"]) != "permit" {
		t.Fatalf("rollback state=%#v", state)
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
