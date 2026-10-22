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

// lanBindCandidate represents a read-only interface discovery result. Never
// infer a LAN listener from an arbitrary private WAN or VPN interface.
type lanBindCandidate struct {
	name string
	ip   net.IP
	up   bool
}

func selectKeeneticLANBind(candidates []lanBindCandidate) string {
	found := map[string]bool{}
	for _, c := range candidates {
		if !c.up || c.ip == nil || c.ip.To4() == nil || !c.ip.IsPrivate() || c.ip.IsLoopback() {
			continue
		}
		// A named Ethernet/WAN device is not evidence of a trusted LAN.
		// Keenetic normally uses a bridge interface for local segments.
		name := c.name
		if name != "br-lan" && !(len(name) == 3 && strings.HasPrefix(name, "br") && name[2] >= '0' && name[2] <= '9') {
			continue
		}
		found[c.ip.String()] = true
	}
	if len(found) != 1 {
		return "127.0.0.1"
	}
	for address := range found {
		return address
	}
	return "127.0.0.1"
}

func preferredLocalUIBind() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	var candidates []lanBindCandidate
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return "127.0.0.1"
		}
		for _, address := range addresses {
			var ip net.IP
			switch a := address.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr:
				ip = a.IP
			}
			candidates = append(candidates, lanBindCandidate{
				name: iface.Name, ip: ip, up: iface.Flags&net.FlagUp != 0,
			})
		}
	}
	return selectKeeneticLANBind(candidates)
}

func (w *wizard) askLocalUI(platform Platform) (UIOptions, error) {
	fmt.Fprintln(w.out, w.msg.UILocalHelp)
	var host string
	for {
		value, err := w.askDefault(w.msg.UIBindPrompt, preferredLocalUIBind())
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
	// Only the browser listener is plain HTTP. The controller API remains on
	// verified loopback TLS and is not exposed to the LAN.
	plainHTTP := platform == PlatformKeenetic
	if plainHTTP {
		fmt.Fprintln(w.out, w.msg.UIHTTPWarning)
	}
	return UIOptions{Platform: platform, PlainHTTP: plainHTTP, Listen: net.JoinHostPort(host, strconv.Itoa(port)), Hostname: hostname, Upstream: "https://127.0.0.1:9443", UpstreamCAFile: managedUIUpstreamCAPath(platform)}, nil
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
func panelURLScheme(listen, hostname string, plainHTTP bool) string {
	if !plainHTTP {
		return panelURL(listen, hostname)
	}
	host, port, _ := net.SplitHostPort(listen)
	if hostname != "" {
		host = hostname
	}
	if port == "80" {
		return "http://" + host
	}
	return "http://" + net.JoinHostPort(host, port)
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
