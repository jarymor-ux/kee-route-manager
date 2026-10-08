package setup

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func (w *wizard) askBenchmarkInterval() (config.Duration, error) {
	for {
		answer, err := w.askDefault(w.msg.BenchmarkIntervalPrompt, config.Default().Benchmark.FullInterval.String())
		if err != nil {
			return config.Duration{}, err
		}
		duration, err := time.ParseDuration(answer)
		if err == nil && duration >= time.Minute && duration <= 30*24*time.Hour {
			return config.Dur(duration), nil
		}
		fmt.Fprintln(w.out, w.msg.InvalidBenchmarkInterval)
	}
}
func (w *wizard) askLocalUI(platform Platform) (UIOptions, error) {
	fmt.Fprintln(w.out, w.msg.UILocalHelp)
	var host string
	for {
		value, err := w.askDefault(w.msg.UIBindPrompt, "127.0.0.1")
		if err != nil {
			return UIOptions{}, err
		}
		ip := net.ParseIP(value)
		if ip != nil && ip.To4() != nil && (ip.IsLoopback() || ip.IsPrivate()) {
			host = ip.String()
			break
		}
		fmt.Fprintln(w.out, w.msg.InvalidUIBind)
	}
	var port int
	for {
		value, err := w.askDefault(w.msg.UIPortPrompt, "9444")
		if err != nil {
			return UIOptions{}, err
		}
		n, err := strconv.Atoi(value)
		if err == nil && n >= 1 && n <= 65535 && n != 9443 {
			port = n
			break
		}
		fmt.Fprintln(w.out, w.msg.InvalidUIPort)
	}
	fmt.Fprintln(w.out, w.msg.UIHostnameHelp)
	var hostname string
	for {
		value, err := w.askOptional(w.msg.UIHostnamePrompt)
		if err != nil {
			return UIOptions{}, err
		}
		if value == "" || validPanelHostname(value) {
			hostname = strings.ToLower(value)
			break
		}
		fmt.Fprintln(w.out, w.msg.InvalidUIHostname)
	}
	return UIOptions{Platform: platform, Listen: net.JoinHostPort(host, strconv.Itoa(port)), Hostname: hostname, Upstream: "https://127.0.0.1:9443", UpstreamCAFile: managedUIUpstreamCAPath(platform)}, nil
}
func validPanelHostname(name string) bool {
	if len(name) > 253 || strings.HasSuffix(strings.ToLower(name), ".local") || net.ParseIP(name) != nil {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func panelURL(listen, hostname string) string {
	host, port, _ := net.SplitHostPort(listen)
	if hostname != "" {
		host = hostname
	}
	if port == "443" {
		return "https://" + host
	}
	return "https://" + net.JoinHostPort(host, port)
}
