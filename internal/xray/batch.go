package xray

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

type BatchRunner struct{ cfg config.Config }
type Batch struct {
	Proxies map[string]*url.URL
	cmd     *exec.Cmd
	dir     string
	logs    *commandOutput
	once    sync.Once
}

func NewBatchRunner(c config.Config) *BatchRunner { return &BatchRunner{c} }
func (r *BatchRunner) Start(ctx context.Context, nodes []model.Node) (*Batch, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("empty batch")
	}
	base, e := freeBlock(r.cfg.Benchmark.TemporaryProxyPortStart, len(nodes))
	if e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(r.cfg.Paths.RunDir, "krm-bench-")
	if e != nil {
		return nil, e
	}
	ins, outs, rules := []any{}, []any{}, []any{}
	proxies := map[string]*url.URL{}
	for i, n := range nodes {
		inTag := fmt.Sprintf("bench-in-%d", i)
		outTag := fmt.Sprintf("bench-out-%d", i)
		ins = append(ins, httpInbound(inTag, base+i))
		out, e := Outbound(n, outTag)
		if e != nil {
			os.RemoveAll(dir)
			return nil, e
		}
		outs = append(outs, out)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{inTag}, "outboundTag": outTag})
		proxies[n.ID] = &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(base+i)}
	}
	root := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": ins, "outbounds": outs, "routing": map[string]any{"domainStrategy": "AsIs", "rules": rules}}
	path := filepath.Join(dir, "bench.json")
	if e = os.WriteFile(path, pretty(root), 0600); e != nil {
		os.RemoveAll(dir)
		return nil, e
	}
	cmd := exec.CommandContext(ctx, r.cfg.Xray.Binary, "run", "-c", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logs := &commandOutput{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if r.cfg.Xray.AssetDir != "" {
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+r.cfg.Xray.AssetDir, "xray.location.asset="+r.cfg.Xray.AssetDir)
	}
	if e = cmd.Start(); e != nil {
		os.RemoveAll(dir)
		return nil, e
	}
	b := &Batch{Proxies: proxies, cmd: cmd, dir: dir, logs: logs}
	deadline := time.Now().Add(r.cfg.Benchmark.TemporaryStartupTimeout.Duration)
	for _, p := range proxies {
		for {
			c, e := net.DialTimeout("tcp", p.Host, 200*time.Millisecond)
			if e == nil {
				_ = c.Close()
				break
			}
			if time.Now().After(deadline) {
				b.Stop()
				return nil, fmt.Errorf("temporary xray did not start %s: %s", p.Host, logs.String())
			}
			select {
			case <-ctx.Done():
				b.Stop()
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return b, nil
}
func (b *Batch) Stop() {
	b.once.Do(func() {
		if b.cmd != nil && b.cmd.Process != nil {
			_ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGTERM)
			done := make(chan struct{})
			go func() { _ = b.cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
		_ = os.RemoveAll(b.dir)
	})
}
func (b *Batch) Logs() string {
	if b.logs == nil {
		return ""
	}
	return redact.Text(b.logs.String())
}
func freeBlock(start, count int) (int, error) {
	if start < 1024 {
		start = 20000
	}
	for base := start; base+count < 65535; base += count + 1 {
		ls := []net.Listener{}
		ok := true
		for i := 0; i < count; i++ {
			l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+i))
			if e != nil {
				ok = false
				break
			}
			ls = append(ls, l)
		}
		for _, l := range ls {
			_ = l.Close()
		}
		if ok {
			return base, nil
		}
	}
	return 0, fmt.Errorf("no free port block")
}
