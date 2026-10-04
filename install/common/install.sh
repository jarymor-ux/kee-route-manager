#!/bin/sh
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'run as root'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
PLATFORM=${KRM_PLATFORM:?platform wrapper is required}
MODE=${KRM_MODE:-local-ui}
case "$MODE" in core|local-ui|ui);; *) fail 'KRM_MODE must be core, local-ui or ui';; esac
case "$(uname -m)" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; mipsel|mipsle) ARCH=mipsle;; mips)
  endian=$(od -An -t u1 -j5 -N1 /bin/sh | tr -d '[:space:]')
  [ "$endian" = 1 ] || fail 'only little-endian MIPS is supported'
  ARCH=mipsle;; *) fail 'unsupported architecture';; esac
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/krm-install.XXXXXX")
TTY_HIDDEN=0
cleanup(){ [ "$TTY_HIDDEN" = 0 ] || stty echo < /dev/tty 2>/dev/null || true; rm -rf "$STAGE"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
case "$PLATFORM" in
 keenetic) PREFIX=/opt; BIN=/opt/bin; RUN=/opt/var/run/kee-route-manager; [ -d /opt ] || fail 'Entware /opt is not mounted';;
 openwrt) PREFIX=; BIN=/usr/bin; RUN=/var/run/kee-route-manager;;
 linux-systemd) PREFIX=; BIN=/usr/local/bin; RUN=/run/kee-route-manager;;
 *) fail 'unsupported platform';;
esac
case "$PLATFORM" in
 linux-systemd) command -v systemctl >/dev/null 2>&1 || fail 'systemd is required';;
 openwrt) [ -f /etc/rc.common ] || fail 'OpenWrt rc.common/procd is required';;
esac
CONFIG_DIR=$PREFIX/etc/kee-route-manager
UI_DIR=$PREFIX/etc/kee-route-manager-ui
CONFIG=$CONFIG_DIR/config.yaml
UI_CONFIG=$UI_DIR/config.yaml
# Linux UI keeps its DynamicUser service; only router platforms already running
# the UI as root combine it with the launcher-owned controller process tree.
MANAGED_UI=0
if [ "$MODE" = local-ui ]; then
 case "$PLATFORM" in keenetic|openwrt) MANAGED_UI=1;; esac
fi
INPUT=${KRM_CONFIG_FILE:-}
[ -n "$INPUT" ] && [ -f "$INPUT" ] || fail 'KRM_CONFIG_FILE must point to a prepared, private YAML configuration'
# Resolve before changing working directories in callers.
case "$INPUT" in /*);; *) fail 'KRM_CONFIG_FILE must be an absolute path';; esac
if [ "$MODE" != ui ]; then
 admin_user=$(printf '%s' "${KRM_ADMIN_USER:-admin}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
 admin_bytes=$(printf '%s' "$admin_user" | wc -c | tr -d '[:space:]')
 [ "$admin_bytes" -ge 3 ] && [ "$admin_bytes" -le 64 ] || fail 'admin username must be 3..64 bytes'
 if printf '%s' "$admin_user" | LC_ALL=C grep -q '[[:cntrl:]]'; then fail 'admin username contains controls'; fi
 for name in kee-route-managerd kee-route-managerctl kee-route-manager-launcher; do
  src=$ROOT/dist/$name-linux-$ARCH
  [ -x "$src" ] || fail "missing signed release executable: $src"
  "$src" version >/dev/null
 done
 for name in manifest-rc.json manifest-rc.json.sig kee-route-manager-ui-linux-$ARCH; do
  [ -f "$ROOT/dist/$name" ] || fail "missing signed release input: $name"
 done
 "$ROOT/dist/kee-route-managerd-linux-$ARCH" validate --config "$INPUT"
 cp "$INPUT" "$STAGE/core.yaml"
 if [ -n "${KRM_PASSWORD_FILE:-}" ]; then
  [ -f "$KRM_PASSWORD_FILE" ] || fail 'password file missing'
  IFS= read -r password < "$KRM_PASSWORD_FILE" || [ -n "$password" ]
 else
  [ -r /dev/tty ] || fail 'set KRM_PASSWORD_FILE for noninteractive installation'
  printf 'Admin password (10..1024 characters): ' > /dev/tty
  stty -echo < /dev/tty; TTY_HIDDEN=1
  IFS= read -r password < /dev/tty || { stty echo < /dev/tty; fail 'password input interrupted'; }
  stty echo < /dev/tty; TTY_HIDDEN=0; printf '\n' > /dev/tty
 fi
 password_bytes=$(printf '%s' "$password" | wc -c | tr -d '[:space:]')
 [ "$password_bytes" -ge 10 ] && [ "$password_bytes" -le 1024 ] || fail 'password must be 10..1024 characters'
 printf '%s\n' "$password" > "$STAGE/password"; unset password

fi
if [ "$MODE" != core ]; then
 src=$ROOT/dist/kee-route-manager-ui-linux-$ARCH
 [ -x "$src" ] || fail 'missing UI executable'
 "$src" version >/dev/null
 if [ "$MODE" = ui ]; then UI_INPUT=$INPUT; else UI_INPUT=${KRM_UI_CONFIG_FILE:-}; fi
 [ -n "$UI_INPUT" ] && [ -f "$UI_INPUT" ] || fail 'local-ui requires KRM_UI_CONFIG_FILE pointing to a prepared UI config'
 "$src" validate --config "$UI_INPUT"
 cp "$UI_INPUT" "$STAGE/ui.yaml"
fi
if [ "$MODE" = ui ]; then
 [ -n "${KRM_UPSTREAM_CA_FILE:-}" ] && [ -f "$KRM_UPSTREAM_CA_FILE" ] || fail 'UI requires a public upstream CA obtained through authenticated SSH'
 openssl x509 -in "$KRM_UPSTREAM_CA_FILE" -noout >/dev/null || fail 'invalid public upstream CA'
fi
# Preserve existing installations; reinstall requires explicit uninstall first.
if [ "$MODE" != ui ]; then [ ! -e "$CONFIG" ] && [ ! -e "$BIN/kee-route-managerd" ] && [ ! -e "$BIN/kee-route-manager-launcher" ] || fail 'existing controller installation detected; use documented backup/uninstall/rollback procedure'; fi
if [ "$MODE" != core ]; then [ ! -e "$UI_CONFIG" ] && [ ! -e "$BIN/kee-route-manager-ui" ] || fail 'existing UI installation detected'; fi
if [ "$MANAGED_UI" = 1 ]; then
 case "$PLATFORM" in keenetic) UI_SERVICE=/opt/etc/init.d/S98kee-route-manager-ui;; openwrt) UI_SERVICE=/etc/init.d/kee-route-manager-ui;; esac
 [ ! -e "$UI_SERVICE" ] || fail 'existing standalone UI service detected; stop and uninstall it before a launcher-managed local-ui installation'
fi
mkdir -p "$BIN"
if [ "$MODE" != ui ]; then
 mkdir -p "$CONFIG_DIR" "$RUN"
 chmod 0700 "$CONFIG_DIR" "$RUN"
 cp "$STAGE/core.yaml" "$CONFIG"; chmod 0600 "$CONFIG"
 if [ "$PLATFORM" = keenetic ]; then
  cp "$ROOT/install/keenetic/xray-status.sh" "$CONFIG_DIR/xray-status.sh"
  chmod 0755 "$CONFIG_DIR/xray-status.sh"
 fi
 for name in kee-route-managerd kee-route-managerctl kee-route-manager-launcher; do cp "$ROOT/dist/$name-linux-$ARCH" "$BIN/$name"; chmod 0755 "$BIN/$name"; done
 "$BIN/kee-route-managerctl" passwd --config "$CONFIG" --username "$admin_user" --password-stdin < "$STAGE/password"
fi
if [ "$MODE" != core ]; then
 mkdir -p "$UI_DIR"; chmod 0755 "$UI_DIR"
 cp "$STAGE/ui.yaml" "$UI_CONFIG"; chmod 0644 "$UI_CONFIG"
 cp "$ROOT/dist/kee-route-manager-ui-linux-$ARCH" "$BIN/kee-route-manager-ui"; chmod 0755 "$BIN/kee-route-manager-ui"
fi
if [ "$MODE" != ui ]; then
 mkdir -p "$PREFIX/var/lib/kee-route-manager" "$PREFIX/var/cache/kee-route-manager"
 chmod 0700 "$PREFIX/var/lib/kee-route-manager" "$PREFIX/var/cache/kee-route-manager"
fi
if [ "$MODE" = ui ] && [ -n "${KRM_UPSTREAM_CA_FILE:-}" ]; then
 [ -f "$KRM_UPSTREAM_CA_FILE" ] || fail 'KRM_UPSTREAM_CA_FILE does not exist'
 cp "$KRM_UPSTREAM_CA_FILE" "$UI_DIR/controller-ca.crt"; chmod 0644 "$UI_DIR/controller-ca.crt"
fi
if [ "$MODE" != ui ]; then
 if [ "$MANAGED_UI" = 1 ]; then
  "$BIN/kee-route-manager-launcher" install --config "$CONFIG" --ui-config "$UI_CONFIG" --release-dir "$ROOT/dist"
 else
  "$BIN/kee-route-manager-launcher" install --config "$CONFIG" --release-dir "$ROOT/dist"
 fi
fi
case "$PLATFORM" in
 linux-systemd)
  if [ "$MODE" != ui ]; then cp "$ROOT/install/linux-systemd/kee-route-manager.service" /etc/systemd/system/; fi
  if [ "$MODE" != core ]; then cp "$ROOT/install/linux-systemd/kee-route-manager-ui.service" /etc/systemd/system/; fi
  systemctl daemon-reload
  [ "$MODE" = ui ] || systemctl enable --now kee-route-manager
  [ "$MODE" = core ] || systemctl enable --now kee-route-manager-ui;;
 openwrt)
  [ "$MODE" = ui ] || { cp "$ROOT/install/openwrt/kee-route-manager.init" /etc/init.d/kee-route-manager; chmod 0755 /etc/init.d/kee-route-manager; /etc/init.d/kee-route-manager enable; /etc/init.d/kee-route-manager start; }
  [ "$MODE" != ui ] || { cp "$ROOT/install/openwrt/kee-route-manager-ui.init" /etc/init.d/kee-route-manager-ui; chmod 0755 /etc/init.d/kee-route-manager-ui; /etc/init.d/kee-route-manager-ui enable; /etc/init.d/kee-route-manager-ui start; };;
 keenetic)
  mkdir -p /opt/etc/init.d
  [ "$MODE" = ui ] || { cp "$ROOT/install/keenetic/S99kee-route-manager" /opt/etc/init.d/; chmod 0755 /opt/etc/init.d/S99kee-route-manager; /opt/etc/init.d/S99kee-route-manager start; }
  [ "$MODE" != ui ] || { cp "$ROOT/install/keenetic/S98kee-route-manager-ui" /opt/etc/init.d/; chmod 0755 /opt/etc/init.d/S98kee-route-manager-ui; /opt/etc/init.d/S98kee-route-manager-ui start; };;
esac
if [ "$MODE" != ui ]; then
 ready=0
 for attempt in 1 2 3 4 5 6 7 8 9 10; do
  : "$attempt"
  if "$BIN/kee-route-managerctl" ready --config "$CONFIG" > "$STAGE/status.json"; then ready=1; break; fi
  sleep 2
 done
 [ "$ready" = 1 ] || fail 'daemon did not become reachable; installation retained for diagnosis; do not assume routing is ready'
 cat "$STAGE/status.json"
fi
if [ "$MODE" != core ]; then
 ui_ready=0
 for attempt in 1 2 3 4 5 6 7 8 9 10; do
  : "$attempt"
  if "$BIN/kee-route-manager-ui" ready --config "$UI_CONFIG"; then ui_ready=1; break; fi
  sleep 2
 done
 [ "$ui_ready" = 1 ] || fail 'UI/upstream not ready; installation retained for diagnosis'
fi
printf 'Installed mode=%s. Check readiness, initial benchmark, HTTPS login and router recovery using docs/AGENT_INSTALL.md.\n' "$MODE"
