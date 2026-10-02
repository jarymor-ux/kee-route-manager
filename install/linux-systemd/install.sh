#!/usr/bin/env bash
set -euo pipefail
umask 077
SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd); ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
fail(){ echo "ERROR: $*" >&2; exit 1; }
restore_tty(){ stty echo 2>/dev/null || true; }
trap restore_tty EXIT HUP INT TERM
safe_yaml(){
  [[ -z $1 ]] && return 0
  [[ $1 != *'\'* && $1 != *'"'* && $1 != *$'\n'* && $1 != *$'\r'* && $1 != *$'\t'* ]] || fail "input contains an unsupported quote, backslash, or control character"
}
[[ $EUID -eq 0 ]] || fail "run as root"
command -v systemctl >/dev/null || fail "systemd required"
command -v xray >/dev/null || fail "xray required"
[[ -d /etc/xray/configs ]] || fail "supported RC layout requires /etc/xray/configs"
case "$(uname -m)" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; mipsel|mipsle) ARCH=mipsle;; *) fail "unsupported architecture";; esac
SRC=${KRM_BINARY:-$ROOT/dist/kee-route-manager-linux-$ARCH}; [[ -x $SRC ]] || fail "binary not found: $SRC"
ROUTE=${KRM_ROUTE_FILE:-}; if [[ -z $ROUTE ]]; then for f in /etc/xray/configs/*.json; do [[ -f $f ]] || continue; if grep -q '"routing"' "$f" && grep -q '"vless-reality"' "$f"; then ROUTE=$f; break; fi; done; fi
[[ -n $ROUTE ]] || fail "set KRM_ROUTE_FILE to strict-JSON routing fragment"
read -r -p 'Subscription URL: ' SUB_URL
read -r -p 'Score target URL: ' SCORE_URL
read -r -p 'Health target URL: ' HEALTH_URL
read -r -p 'Optional speed URL template containing {bytes} (Enter to disable): ' SPEED_URL
read -r -p 'Web admin username: ' ADMIN_USER
read -r -s -p 'Web admin password (minimum 10 characters): ' ADMIN_PASS; restore_tty; echo
read -r -p 'Firewall mode [existing/managed] (default existing): ' FW; FW=${FW:-existing}
[[ $FW == existing || $FW == managed ]] || fail "invalid firewall mode"
LAN=br0; TCP=12345; UDP=12345
if [[ $FW == managed ]]; then
  read -r -p 'LAN interface [br0]: ' LAN_IN; LAN=${LAN_IN:-br0}
  read -r -p 'Xray TCP redirect port [12345]: ' TCP_IN; TCP=${TCP_IN:-12345}
  read -r -p 'Xray UDP TProxy port [12345]: ' UDP_IN; UDP=${UDP_IN:-12345}
  command -v nft >/dev/null || fail "nft required"
fi
[[ -n $SUB_URL && -n $SCORE_URL && -n $HEALTH_URL && -n $ADMIN_USER ]] || fail "required value is empty"
[[ ${#ADMIN_PASS} -ge 10 ]] || fail "password is too short"
for value in "$SUB_URL" "$SCORE_URL" "$HEALTH_URL" "$SPEED_URL" "$ROUTE" "$LAN"; do safe_yaml "$value"; done
[[ $TCP =~ ^[0-9]+$ && $UDP =~ ^[0-9]+$ ]] || fail "ports must be numeric"
install -d -m 0700 /etc/kee-route-manager /var/lib/kee-route-manager /var/cache/kee-route-manager /run/kee-route-manager
install -m 0755 "$SRC" /usr/local/sbin/kee-route-manager
install -m 0600 "$ROOT/configs/linux-systemd.yaml" /etc/kee-route-manager/config.yaml
python3 - "$SUB_URL" "$SCORE_URL" "$HEALTH_URL" "$ROUTE" "$SPEED_URL" "$FW" "$LAN" "$TCP" "$UDP" <<'PY'
from pathlib import Path
import sys
p=Path('/etc/kee-route-manager/config.yaml'); s=p.read_text()
for a,b in zip(['https://subscription.example.invalid/replace-me','https://score-target.example.invalid/replace-me','https://health-target.example.invalid/replace-me','/etc/xray/configs/05_routing.json'],sys.argv[1:5]): s=s.replace(a,b)
speed,fw,lan,tcp,udp=sys.argv[5:]
s=s.replace('firewall_mode: existing', 'firewall_mode: '+fw).replace('lan_interfaces: [br0]', 'lan_interfaces: ['+lan+']').replace('tcp_redirect_port: 12345','tcp_redirect_port: '+tcp).replace('udp_tproxy_port: 12345','udp_tproxy_port: '+udp)
if speed:
    s=s.replace('enabled: false\n    url_template: "https://speed-target.example.invalid/download?bytes={bytes}"','enabled: true\n    url_template: "'+speed+'"')
p.write_text(s)
PY
printf '%s\n' "$ADMIN_PASS" | /usr/local/sbin/kee-route-manager passwd --config /etc/kee-route-manager/config.yaml --username "$ADMIN_USER" --password-stdin
unset ADMIN_PASS
/usr/local/sbin/kee-route-manager validate --config /etc/kee-route-manager/config.yaml >/dev/null
xray run -test -confdir /etc/xray/configs >/dev/null
install -m 0644 "$SCRIPT_DIR/kee-route-manager.service" /etc/systemd/system/kee-route-manager.service
systemctl daemon-reload; systemctl enable --now kee-route-manager
echo 'Installed. Open https://<host-ip>:9443/'
