#!/bin/sh
set -eu
umask 077
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
CONFIG_DIR=/opt/etc/kee-route-manager
CONFIG=$CONFIG_DIR/config.yaml
BIN=/opt/bin/kee-route-manager
INIT=/opt/etc/init.d/S99kee-route-manager
ALLOW_OLD=0
[ "${1:-}" = "--allow-existing-blanc-auto" ] && ALLOW_OLD=1
fail(){ echo "ERROR: $*" >&2; exit 1; }
restore_tty(){ stty echo 2>/dev/null || true; }
trap restore_tty EXIT HUP INT TERM
safe_yaml(){
  [ -z "$1" ] && return 0
  case "$1" in *'\'*|*'"'*) fail "input contains an unsupported quote or backslash";; esac
  printf '%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]' && fail "input contains a control character"
}
[ "$(id -u)" = 0 ] || fail "run as root"
[ -d /opt ] || fail "Entware /opt is not mounted"
[ -x /opt/sbin/xray ] || fail "/opt/sbin/xray not found"
[ -x /opt/sbin/xkeen ] || fail "/opt/sbin/xkeen not found"
if [ "$ALLOW_OLD" -ne 1 ] && { [ -x /opt/bin/blanc-auto ] || [ -e /opt/etc/blanc-auto ]; }; then fail "blanc-auto detected. This is a clean install; stop/remove the old service or rerun with --allow-existing-blanc-auto after verifying there is no scheduler conflict"; fi
case "$(uname -m)" in aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; mipsel|mipsle) ARCH=mipsle;; x86_64|amd64) ARCH=amd64;; *) fail "unsupported architecture: $(uname -m)";; esac
SRC=${KRM_BINARY:-$ROOT/dist/kee-route-manager-linux-$ARCH}
[ -x "$SRC" ] || fail "binary not found: $SRC"
ROUTE=${KRM_ROUTE_FILE:-}
if [ -z "$ROUTE" ]; then
  for f in /opt/etc/xray/configs/*.json; do
    [ -f "$f" ] || continue
    if grep -q '"routing"' "$f" 2>/dev/null && grep -q '"vless-reality"' "$f" 2>/dev/null; then ROUTE=$f; break; fi
  done
fi
[ -n "$ROUTE" ] && [ -f "$ROUTE" ] || fail "could not detect the strict-JSON Xray routing file; set KRM_ROUTE_FILE=/path/file.json"
printf 'Subscription URL: '; IFS= read -r SUB_URL
printf 'Score target URL: '; IFS= read -r SCORE_URL
printf 'Health target URL: '; IFS= read -r HEALTH_URL
printf 'Optional speed URL template containing {bytes} (Enter to disable): '; IFS= read -r SPEED_URL
printf 'Web admin username: '; IFS= read -r ADMIN_USER
printf 'Web admin password (minimum 10 characters): '; stty -echo; IFS= read -r ADMIN_PASS; restore_tty; echo
[ -n "$SUB_URL" ] && [ -n "$SCORE_URL" ] && [ -n "$HEALTH_URL" ] && [ -n "$ADMIN_USER" ] || fail "required value is empty"
[ "${#ADMIN_PASS}" -ge 10 ] || fail "password is too short"
for v in "$SUB_URL" "$SCORE_URL" "$HEALTH_URL" "$SPEED_URL" "$ROUTE"; do safe_yaml "$v"; done
mkdir -p "$CONFIG_DIR" /opt/var/lib/kee-route-manager /opt/var/cache/kee-route-manager /opt/var/run/kee-route-manager /opt/var/log /opt/bin /opt/etc/init.d
if [ -f "$CONFIG" ]; then cp "$CONFIG" "$CONFIG.backup.$(date +%Y%m%d%H%M%S)"; fi
cp "$ROOT/configs/keenetic.yaml" "$CONFIG"
esc(){ printf '%s' "$1" | sed 's/[|&\\]/\\&/g'; }
sed -i "s|https://subscription.example.invalid/replace-me|$(esc "$SUB_URL")|" "$CONFIG"
sed -i "s|https://score-target.example.invalid/replace-me|$(esc "$SCORE_URL")|" "$CONFIG"
sed -i "s|https://health-target.example.invalid/replace-me|$(esc "$HEALTH_URL")|" "$CONFIG"
sed -i "s|/opt/etc/xray/configs/05_routing.json|$(esc "$ROUTE")|" "$CONFIG"
if [ -n "$SPEED_URL" ]; then
  sed -i "/^  speed:/,/^update:/{s/^    enabled: false/    enabled: true/;s|https://speed-target.example.invalid/download?bytes={bytes}|$(esc "$SPEED_URL")|;}" "$CONFIG"
fi
cp "$SRC" "$BIN"; chmod 0755 "$BIN"
cp "$SCRIPT_DIR/S99kee-route-manager" "$INIT"; chmod 0755 "$INIT"
printf '%s\n' "$ADMIN_PASS" | "$BIN" passwd --config "$CONFIG" --username "$ADMIN_USER" --password-stdin
unset ADMIN_PASS
"$BIN" validate --config "$CONFIG" >/dev/null
/opt/sbin/xray run -test -confdir /opt/etc/xray/configs >/dev/null 2>&1 || fail "current Xray config is invalid before KRM installation"
"$INIT" restart
echo "Installed. Open https://<router-ip>:9443/"
echo "The first pool build may take several minutes. The browser will show a self-signed certificate warning until you install your own certificate."
