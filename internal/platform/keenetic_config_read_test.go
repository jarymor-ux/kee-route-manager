package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func nativeConfigReadFixture(t *testing.T, status int, output []byte, scriptSuffix string) (*keenetic, string, *atomic.Int64) {
	t.Helper()
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "private-config")
	logPath := filepath.Join(dir, "command-log")
	if err := os.WriteFile(bodyPath, output, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KRM_NATIVE_CONFIG_FIXTURE", bodyPath)
	t.Setenv("KRM_NATIVE_COMMAND_LOG", logPath)
	binary := filepath.Join(dir, "ndmc")
	script := "#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = -c ] || exit 99\nprintf '%s\\n' \"$2\" >> \"$KRM_NATIVE_COMMAND_LOG\"\ncase \"$2\" in 'more running-config'|'more startup-config') ;; *) exit 98;; esac\ncat \"$KRM_NATIVE_CONFIG_FIXTURE\"\n" + scriptSuffix + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	requests := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write(output)
	}))
	t.Cleanup(server.Close)
	cfg := config.Default()
	cfg.Platform.Keenetic.RCIBaseURL = server.URL + "/rci"
	cfg.Platform.Keenetic.NDMCBinary = binary
	return newKeenetic(cfg, Runner{Timeout: 2 * time.Second}).(*keenetic), logPath, requests
}
func validNativeConfigRead() []byte {
	return []byte(regressionStartupConfig(regressionChecksum(1)) + "hostname router\nusername admin password PRIVATE-NATIVE-CONFIG-SECRET\nip host alice.jopa 192.168.1.1\n!\n")
}

func TestKeeneticConfigReadNativeFallbackUsesFixedReadOnlyCommands(t *testing.T) {
	for _, status := range []int{403, 404} {
		for _, file := range []struct{ name, command string }{{"running-config.txt", "more running-config"}, {"startup-config.txt", "more startup-config"}} {
			t.Run(file.name+http.StatusText(status), func(t *testing.T) {
				expected := validNativeConfigRead()
				k, logPath, requests := nativeConfigReadFixture(t, status, expected, "")
				actual, err := k.configFile(context.Background(), file.name)
				if err != nil {
					t.Fatal(err)
				}
				if string(actual) != string(expected) {
					t.Fatal("native fallback did not preserve whole private configuration")
				}
				log, err := os.ReadFile(logPath)
				if err != nil || strings.TrimSpace(string(log)) != file.command || requests.Load() != 1 {
					t.Fatal("fallback executed wrong command")
				}
				checksum, err := k.startupConfigChecksum(context.Background())
				if err != nil || checksum != regressionChecksum(1) {
					t.Fatal("native saved MD5 no longer authoritative", err)
				}
			})
		}
	}
}
func TestKeeneticConfigReadFallbackDoesNotMaskOtherHTTPFailures(t *testing.T) {
	for _, status := range []int{200, 401, 405, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			expected := validNativeConfigRead()
			k, logPath, _ := nativeConfigReadFixture(t, status, expected, "")
			actual, err := k.configFile(context.Background(), "running-config.txt")
			if status == 200 {
				if err != nil || string(actual) != string(expected) {
					t.Fatal("HTTP success changed", err)
				}
			} else if err == nil {
				t.Fatal("unexpected HTTP failure was masked")
			}
			if _, err = os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatal("unapproved fallback executed")
			}
		})
	}
	k, logPath, requests := nativeConfigReadFixture(t, 403, validNativeConfigRead(), "")
	if _, err := k.configFile(context.Background(), "../private-file"); err == nil {
		t.Fatal("unknown filename accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("unknown filename reached HTTP")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("unknown filename reached NDMC")
	}
}
func TestKeeneticConfigReadFallbackRejectsInvalidLimitedAndFailedOutputPrivately(t *testing.T) {
	header := regressionStartupConfig(regressionChecksum(1))
	for _, tc := range []struct {
		name   string
		output []byte
		suffix string
		limit  int64
	}{
		{"empty", nil, "", 0},
		{"JSON status", []byte(`[{"status":"error","message":"PRIVATE-NATIVE-CONFIG-SECRET"}]`), "", 0},
		{"plain error", []byte("PRIVATE-NATIVE-CONFIG-SECRET: rejected"), "", 0},
		{"missing MD5", []byte("! Model: Keenetic\nhostname router\n"), "", 0},
		{"duplicate MD5", append(validNativeConfigRead(), []byte(header)...), "", 0},
		{"header alone", []byte(header), "", 0},
		{"missing terminal marker", []byte(header + "hostname router\n"), "", 0},
		{"missing leading framing", append(validNativeConfigRead(), []byte("\x1b[K")...), "", 0},
		{"missing trailing framing", append([]byte("\x1b[K"), validNativeConfigRead()...), "", 0},
		{"unknown internal ANSI", append(validNativeConfigRead(), []byte("\x1b[0K")...), "", 0},
		{"duplicated ANSI framing", append([]byte("\x1b[K\x1b[K"), append(validNativeConfigRead(), []byte("\x1b[K")...)...), "", 0},
		{"binary NUL", append(validNativeConfigRead(), 0), "", 0},
		{"invalid UTF8", append(validNativeConfigRead(), 255), "", 0},
		{"successful truncated header", validNativeConfigRead(), "", int64(len(header) + 8)},
		{"saturated exact limit", validNativeConfigRead(), "", int64(len(validNativeConfigRead()))},
		{"command nonzero", validNativeConfigRead(), "printf 'PRIVATE-NATIVE-CONFIG-SECRET' >&2; exit 3", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _, _ := nativeConfigReadFixture(t, 403, tc.output, tc.suffix)
			k.r.MaxOutput = tc.limit
			data, err := k.configFile(context.Background(), "running-config.txt")
			if err == nil || data != nil {
				t.Fatal("invalid private configuration accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "username") {
				t.Fatal("command error exposed private output")
			}
		})
	}
}
func TestKeeneticConfigReadFallbackRetainsContextCancellation(t *testing.T) {
	for _, mode := range []string{"canceled", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			k, _, _ := nativeConfigReadFixture(t, 403, validNativeConfigRead(), "sleep 2")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := context.Canceled
			if mode == "canceled" {
				timer := time.AfterFunc(50*time.Millisecond, cancel)
				defer timer.Stop()
			} else {
				k.r.Timeout = 50 * time.Millisecond
				expected = context.DeadlineExceeded
			}
			start := time.Now()
			data, err := k.configFile(ctx, "startup-config.txt")
			if data != nil || !errors.Is(err, expected) {
				t.Fatalf("lost cancellation cause: %v", err)
			}
			if time.Since(start) > time.Second || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("canceled native command escaped lifetime/privacy limits")
			}
		})
	}
}

func TestKeeneticConfigReadNativeFallbackUnwrapsOnlyConfirmedNDMCTransport(t *testing.T) {
	expected := validNativeConfigRead()
	wrapped := append([]byte("\x1b[K"), append(expected, []byte("\x1b[K")...)...)
	k, _, _ := nativeConfigReadFixture(t, 403, wrapped, "")
	data, err := k.configFile(context.Background(), "running-config.txt")
	if err != nil || string(data) != string(expected) {
		t.Fatal("confirmed NDMC framing was not removed precisely", err)
	}
	if _, count, err := panelDNSNormalized(data, "alice.jopa", "192.168.1.1"); err != nil || count != 1 {
		t.Fatal("transport framing polluted alias ownership", err)
	}
}
