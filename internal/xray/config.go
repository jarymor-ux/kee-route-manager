package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

type Managed struct{ API, Inbounds, Outbounds, Routing []byte }

func BuildManaged(cfg config.Config, slots []model.Slot, nodes map[string]model.Node) (Managed, error) {
	host, portText, e := net.SplitHostPort(cfg.Xray.APIAddress)
	if e != nil {
		return Managed{}, e
	}
	port, e := strconv.Atoi(portText)
	if e != nil {
		return Managed{}, e
	}
	api := map[string]any{"api": map[string]any{"tag": cfg.Xray.APITag, "services": []string{"HandlerService", "RoutingService", "StatsService"}}, "inbounds": []any{map[string]any{"tag": cfg.Xray.APITag, "listen": host, "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": host}}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{cfg.Xray.APITag}, "outboundTag": cfg.Xray.APITag}}}}
	ins := []any{httpInbound("krm-health", cfg.Xray.HealthProxyPort)}
	outs := []any{map[string]any{"tag": cfg.Xray.ManagedDirectTag, "protocol": "freedom", "settings": map[string]any{}}}
	rules := []any{map[string]any{"type": "field", "inboundTag": []string{"krm-health"}, "balancerTag": cfg.Xray.BalancerTag}}
	byIndex := map[int]model.Slot{}
	selectors := []string{}
	for _, s := range slots {
		byIndex[s.Index] = s
		if s.NodeID != "" {
			selectors = append(selectors, s.Tag)
		}
	}
	for i := 0; i < cfg.Pool.Size; i++ {
		ins = append(ins, httpInbound(fmt.Sprintf("krm-probe-%d", i), cfg.Xray.ProbePortStart+i))
		tag := fmt.Sprintf("%s%d", cfg.Xray.SlotTagPrefix, i)
		slot := byIndex[i]
		if n, ok := nodes[slot.NodeID]; ok {
			v, e := Outbound(n, tag)
			if e != nil {
				return Managed{}, e
			}
			outs = append(outs, v)
		} else {
			outs = append(outs, blackholeOutbound(tag))
		}
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{fmt.Sprintf("krm-probe-%d", i)}, "outboundTag": tag})
	}
	if len(selectors) == 0 {
		return Managed{}, fmt.Errorf("hot pool has no selectable VPN outbounds")
	}
	routing := map[string]any{"routing": map[string]any{"balancers": []any{map[string]any{"tag": cfg.Xray.BalancerTag, "selector": selectors, "strategy": map[string]any{"type": "random"}}}, "rules": rules}}
	return Managed{pretty(api), pretty(map[string]any{"inbounds": ins}), pretty(map[string]any{"outbounds": outs}), pretty(routing)}, nil
}
func Outbound(n model.Node, tag string) (map[string]any, error) {
	if n.Protocol != "vless" {
		return nil, fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
	user := map[string]any{"id": n.UUID, "encryption": defaultString(n.Encryption, "none"), "level": 0}
	if n.Flow != "" {
		user["flow"] = n.Flow
	}
	stream := map[string]any{"network": n.Network, "security": n.Security}
	switch {
	case n.Network == "tcp" && n.Security == "reality":
		r := map[string]any{"serverName": n.ServerName, "publicKey": n.PublicKey}
		if n.Fingerprint != "" {
			r["fingerprint"] = n.Fingerprint
		}
		if n.ShortID != "" {
			r["shortId"] = n.ShortID
		}
		if n.SpiderX != "" {
			r["spiderX"] = n.SpiderX
		}
		stream["realitySettings"] = r
	case n.Network == "ws" && n.Security == "tls":
		t := map[string]any{"serverName": n.ServerName, "allowInsecure": false}
		if n.Fingerprint != "" {
			t["fingerprint"] = n.Fingerprint
		}
		if len(n.ALPN) > 0 {
			t["alpn"] = n.ALPN
		}
		stream["tlsSettings"] = t
		w := map[string]any{"path": defaultString(n.WSPath, "/")}
		if n.WSHost != "" {
			w["headers"] = map[string]any{"Host": n.WSHost}
		}
		stream["wsSettings"] = w
	default:
		return nil, fmt.Errorf("unsupported transport %s/%s", n.Network, n.Security)
	}
	return map[string]any{"tag": tag, "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": n.Address, "port": n.Port, "users": []any{user}}}}, "streamSettings": stream}, nil
}
func blackholeOutbound(tag string) map[string]any {
	return map[string]any{"tag": tag, "protocol": "blackhole", "settings": map[string]any{}}
}

func httpInbound(tag string, port int) map[string]any {
	return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "http", "settings": map[string]any{}}
}
func pretty(v any) []byte {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		panic(e)
	}
	return append(b, '\n')
}
func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
