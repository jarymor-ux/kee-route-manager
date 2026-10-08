package core

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

type panelDNSAdapter struct {
	*fakeAdapter
	calls            int
	started, release chan struct{}
	err              error
}

func (p *panelDNSAdapter) EnsurePanelAlias(ctx context.Context, hostname, ip string) error {
	p.calls++
	if p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.err
}
func (*panelDNSAdapter) ReconcilePanelAlias(context.Context) error { return nil }

func TestPanelDNSRunsDuringBenchmarkWithoutChangingOrCancelingRoute(t *testing.T) {
	m, _, adapter := fixture(t)
	dns := &panelDNSAdapter{fakeAdapter: adapter}
	m.platform = dns
	release := startHeldBenchmark(t, m)
	before := m.State()
	if !m.PanelDNSAutomatic() {
		t.Fatal("automatic DNS capability absent")
	}
	if err := m.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1"); err != nil {
		t.Fatal("benchmark blocked DNS", err)
	}
	after := m.State()
	if dns.calls != 1 || after.DirectMode != before.DirectMode || after.ActiveNodeID != before.ActiveNodeID {
		t.Fatal("DNS operation changed routing state")
	}
	running := false
	for _, op := range m.ops.List() {
		if op.Type == "benchmark" && op.Status == "running" {
			running = true
		}
	}
	if !running {
		t.Fatal("DNS operation canceled benchmark")
	}
	close(release)
	m.wg.Wait()
}
func TestPanelDNSSerializesOtherControlOperationsAndInvalidIdentityHasNoEffects(t *testing.T) {
	m, _, adapter := fixture(t)
	dns := &panelDNSAdapter{fakeAdapter: adapter, started: make(chan struct{}), release: make(chan struct{})}
	m.platform = dns
	for _, input := range []struct{ host, ip string }{{"alice.local", "192.168.1.1"}, {"alice.localhost", "192.168.1.1"}, {"Alice.Jopa", "192.168.1.1"}, {"alice.jopa;reboot", "192.168.1.1"}, {"alice.jopa", "0.0.0.0"}, {"alice.jopa", "8.8.8.8"}, {"alice.jopa", "router.invalid"}} {
		if err := m.EnsurePanelAlias(context.Background(), input.host, input.ip); !errors.Is(err, platform.ErrPanelDNSUnavailable) {
			t.Fatalf("invalid request %q %q: %v", input.host, input.ip, err)
		}
	}
	if dns.calls != 0 {
		t.Fatal("invalid identity reached adapter")
	}
	done := make(chan error, 1)
	go func() { done <- m.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1") }()
	select {
	case <-dns.started:
	case <-time.After(time.Second):
		t.Fatal("DNS operation did not start")
	}
	called := false
	if err := m.RunAction(context.Background(), "client-policy", "web", func(context.Context) error { called = true; return nil }); !errors.Is(err, operation.ErrBusy) || called {
		t.Fatal("control operation overlapped DNS mutation", err)
	}
	close(dns.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A private UDP fixture serves the default resolver's actual A/AAAA requests.
// No real DNS, hosts file, or system resolver settings are touched.
func panelDNSResolverFixture(t *testing.T, answer net.IP) *atomic.Int64 {
	t.Helper()
	socket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var requests atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 1500)
		for {
			n, peer, err := socket.ReadFrom(buf)
			if err != nil {
				return
			}
			requests.Add(1)
			req := append([]byte(nil), buf[:n]...)
			if len(req) < 17 {
				continue
			}
			offset := 12
			for offset < len(req) && req[offset] != 0 {
				offset += int(req[offset]) + 1
			}
			offset++
			if offset+4 > len(req) {
				continue
			}
			typ := binary.BigEndian.Uint16(req[offset:])
			questionEnd := offset + 4
			response := make([]byte, 12)
			copy(response[:2], req[:2])
			response[2], response[3] = 0x81, 0x80
			response[5] = 1
			response = append(response, req[12:questionEnd]...)
			if typ == 1 && answer.To4() != nil {
				response[7] = 1
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4)
				response = append(response, answer.To4()...)
			}
			_, _ = socket.WriteTo(response, peer)
		}
	}()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", socket.LocalAddr().String())
	}}
	t.Cleanup(func() { net.DefaultResolver = previous; _ = socket.Close(); wg.Wait() })
	return &requests
}
func TestPanelDNSGenericAdapterRequiresExistingMatchingLocalDNS(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "wrong-ip"}[matching], func(t *testing.T) {
			m, _, _ := fixture(t)
			answer := net.ParseIP("192.168.1.1")
			if !matching {
				answer = net.ParseIP("192.168.1.9")
			}
			requests := panelDNSResolverFixture(t, answer)
			before := m.State()
			if m.PanelDNSAutomatic() {
				t.Fatal("generic adapter claimed automatic router DNS")
			}
			err := m.EnsurePanelAlias(context.Background(), "alice.jopa", "192.168.1.1")
			if matching && err != nil || !matching && !errors.Is(err, platform.ErrPanelDNSUnavailable) {
				t.Fatal("wrong resolver acceptance", err)
			}
			if requests.Load() == 0 {
				t.Fatal("manual DNS was not actually resolved")
			}
			after := m.State()
			if after.DirectMode != before.DirectMode || after.ActiveNodeID != before.ActiveNodeID {
				t.Fatal("manual DNS check modified route")
			}
		})
	}
}
