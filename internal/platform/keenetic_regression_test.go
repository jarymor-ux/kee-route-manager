package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
func TestRegressionPolicyCancelledRollback(t *testing.T) {
	for _, mode := range []string{"success", "verification_failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			d := t.TempDir()
			logPath := filepath.Join(d, "commands")
			script := filepath.Join(d, "fake-ndmc")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$2\" >> \"$REVIEW_NDMC_LOG\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("REVIEW_NDMC_LOG", logPath)
			c := config.Default()
			c.Platform.Keenetic.NDMCBinary = script
			k := newKeenetic(c, Runner{}).(*keenetic)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/rci/show/ip/policy" {
					return platformRegressionResponse(`{"Policy1":{"description":"XKeen"}}`), nil
				}
				calls++
				if calls == 1 {
					return platformRegressionResponse(`{"host":[{"mac":"00:11:22:33:44:55","conform":true,"policy":"Policy2","access":"permit"}]}`), nil
				}
				if mode == "cancel" && calls == 2 {
					cancel()
					return nil, context.Canceled
				}
				if calls >= 3 {
					return platformRegressionResponse(`{"host":[{"mac":"00:11:22:33:44:55","conform":true,"policy":"Policy2","access":"permit"}]}`), nil
				}
				access := "permit"
				if mode == "verification_failure" {
					access = "deny"
				}
				return platformRegressionResponse(`{"host":[{"mac":"00:11:22:33:44:55","conform":false,"policy":"Policy1","access":"` + access + `"}]}`), nil
			})}
			err := k.SetClientPolicy(ctx, "00:11:22:33:44:55", "xkeen")
			logs, _ := os.ReadFile(logPath)
			t.Logf("mode=%s error=%v command_log=\n%s", mode, err, logs)
			rollbackPresent := strings.Contains("\n"+string(logs), "\nip hotspot host 00:11:22:33:44:55 conform\n")
			switch mode {
			case "success":
				if err != nil || rollbackPresent || calls != 2 || !strings.Contains(string(logs), "system configuration save") {
					t.Fatal("success negative control failed")
				}
			case "verification_failure":
				if err == nil || !rollbackPresent {
					t.Fatal("normal rollback negative control failed")
				}
			case "cancel":
				if err == nil || !rollbackPresent || calls != 3 || !strings.Contains(string(logs), "policy Policy2") || !strings.Contains(string(logs), "system configuration save") {
					t.Fatal("canceled policy mutation was not restored")
				}
			}
		})
	}
}

func TestPolicyRollbackFailureAndVerificationAreReported(t *testing.T) {
	for _, mode := range []string{"command_failure", "restore_drift", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			d := t.TempDir()
			script := filepath.Join(d, "fake-ndmc")
			body := `#!/bin/sh
case "$2" in
 *'policy Policy2')
  case "$KRM_RECOVERY_MODE" in
   command_failure) echo recovery-command-failed >&2; exit 1 ;;
   deadline) exec sleep 5 ;;
  esac
 ;;
esac
`
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KRM_RECOVERY_MODE", mode)
			c := config.Default()
			c.Platform.Keenetic.NDMCBinary = script
			timeout := 3 * time.Second
			if mode == "deadline" {
				timeout = time.Second
			}
			k := newKeenetic(c, Runner{Timeout: timeout}).(*keenetic)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/rci/show/ip/policy" {
					return platformRegressionResponse(`{"Policy1":{"description":"XKeen"}}`), nil
				}
				calls++
				if calls == 2 {
					cancel()
					return nil, context.Canceled
				}
				policy := "Policy2"
				if calls >= 3 && mode == "restore_drift" {
					policy = "Policy1"
				}
				return platformRegressionResponse(`{"host":[{"mac":"00:11:22:33:44:55","conform":true,"policy":"` + policy + `","access":"permit"}]}`), nil
			})}
			start := time.Now()
			err := k.SetClientPolicy(ctx, "00:11:22:33:44:55", "xkeen")
			if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "client policy rollback") {
				t.Fatalf("recovery failure hidden: %v", err)
			}
			if mode == "command_failure" && !strings.Contains(err.Error(), "recovery-command-failed") {
				t.Fatalf("command failure hidden: %v", err)
			}
			if mode == "restore_drift" && !strings.Contains(err.Error(), "did not restore") {
				t.Fatalf("verification drift hidden: %v", err)
			}
			if mode == "deadline" && time.Since(start) > 4*time.Second {
				t.Fatal("recovery exceeded its total deadline")
			}
		})
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
