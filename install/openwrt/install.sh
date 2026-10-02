#!/bin/sh
set -eu
umask 077
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
fail(){ echo "ERROR: $*" >&2; exit 1; }
restore_tty(){ stty echo 2>/dev/null || true; }
trap restore_tty EXIT HUP INT TERM
safe_yaml(){
  [ -z "$1" ] && return 0
  case "$1" in *'\'*|*'"'*) fail "input contains an unsupported quote or backslash";; esac
  printf '%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]' && fail "input contains a control character"
}
[ "$(id -u)" = 0 ] || fail "run as root"
[ -f /etc/openwrt_release ] || fail "OpenWrt not detected"
command -v xray >/dev/null 2>&1 || fail "xray not found"
[ -d /etc/xray/configs ] || fail "supported RC layout requires /etc/xray/configs and Xray -confdir mode"
case "$(uname -m)" in aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; mipsel|mipsle) ARCH=mipsle;; x86_64|amd64) ARCH=amd64;; *) fail "unsupported architecture";; esac
SRC=${KRM_BINARY:-$ROOT/dist/kee-route-manager-linux-$ARCH}; [ -x "$SRC" ] || fail "binary not found: $SRC"
ROUTE=${KRM_ROUTE_FILE:-}
if [ -z "$ROUTE" ]; then for f in /etc/xray/configs/*.json; do [ -f "$f" ] || continue; grep -q '"routing"' "$f" && grep -q '"vless-reality"' "$f" && { ROUTE=$f; break; }; done; fi
[ -n "$ROUTE" ] || fail "set KRM_ROUTE_FILE to the strict-JSON routing fragment"
printf 'Subscription URL: '; IFS= read -r SUB_URL
printf 'Score target URL: '; IFS= read -r SCORE_URL
printf 'Health target URL: '; IFS= read -r HEALTH_URL
printf 'Optional speed URL template containing {bytes} (Enter to disable): '; IFS= read -r SPEED_URL
printf 'Web admin username: '; IFS= read -r ADMIN_USER
printf 'Web admin password (minimum 10 characters): '; stty -echo; IFS= read -r ADMIN_PASS; restore_tty; echo
printf 'Firewall mode [existing/managed] (default existing): '; IFS= read -r FW; FW=${FW:-existing}
[ "$FW" = existing ] || [ "$FW" = managed ] || fail "invalid firewall mode"
[ -n "$SUB_URL" ] && [ -n "$SCORE_URL" ] && [ -n "$HEALTH_URL" ] && [ -n "$ADMIN_USER" ] || fail "required value is empty"
[ "${#ADMIN_PASS}" -ge 10 ] || fail "password is too short"
for v in "$SUB_URL" "$SCORE_URL" "$HEALTH_URL" "$SPEED_URL" "$ROUTE"; do safe_yaml "$v"; done
LAN=br-lan; TCP=12345; UDP=12345
if [ "$FW" = managed ]; then printf 'LAN interface [br-lan]: '; IFS= read -r LAN_IN; LAN=${LAN_IN:-br-lan}; printf 'Xray TCP redirect port [12345]: '; IFS= read -r TCP_IN; TCP=${TCP_IN:-12345}; printf 'Xray UDP TProxy port [12345]: '; IFS= read -r UDP_IN; UDP=${UDP_IN:-12345}; command -v nft >/dev/null 2>&1 || fail "nft required"; fi
safe_yaml "$LAN"
case "$TCP:$UDP" in *[!0-9:]*|:*|*:) fail "ports must be numeric";; esac
mkdir -p /etc/kee-route-manager /var/lib/kee-route-manager /var/cache/kee-route-manager /var/run/kee-route-manager
cp "$ROOT/configs/openwrt.yaml" /etc/kee-route-manager/config.yaml
esc(){ printf '%s' "$1" | sed 's/[|&\\]/\\&/g'; }
C=/etc/kee-route-manager/config.yaml
sed -i "s|https://subscription.example.invalid/replace-me|$(esc "$SUB_URL")|;s|https://score-target.example.invalid/replace-me|$(esc "$SCORE_URL")|;s|https://health-target.example.invalid/replace-me|$(esc "$HEALTH_URL")|;s|/etc/xray/configs/05_routing.json|$(esc "$ROUTE")|;s|firewall_mode: existing|firewall_mode: $FW|;s|lan_interfaces: \[br-lan\]|lan_interfaces: [$LAN]|;s|tcp_redirect_port: 12345|tcp_redirect_port: $TCP|;s|udp_tproxy_port: 12345|udp_tproxy_port: $UDP|" "$C"
if [ -n "$SPEED_URL" ]; then
  sed -i "/^  speed:/,/^update:/{s/^    enabled: false/    enabled: true/;s|https://speed-target.example.invalid/download?bytes={bytes}|$(esc "$SPEED_URL")|;}" "$C"
fi
cp "$SRC" /usr/bin/kee-route-manager; chmod 0755 /usr/bin/kee-route-manager
cp "$SCRIPT_DIR/kee-route-manager.init" /etc/init.d/kee-route-manager; chmod 0755 /etc/init.d/kee-route-manager
printf '%s\n' "$ADMIN_PASS" | /usr/bin/kee-route-manager passwd --config "$C" --username "$ADMIN_USER" --password-stdin
unset ADMIN_PASS
/usr/bin/kee-route-manager validate --config "$C" >/dev/null
xray run -test -confdir /etc/xray/configs >/dev/null 2>&1 || fail "current Xray config invalid"
/etc/init.d/kee-route-manager enable
/etc/init.d/kee-route-manager restart
echo "Installed. Open https://<router-ip>:9443/"
