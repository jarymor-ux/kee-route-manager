#!/bin/sh
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'run as root'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
PLATFORM=${KRM_PLATFORM:?platform wrapper is required}
MODE=${KRM_MODE:-local-ui}
case "$MODE" in core|local-ui|ui);; *) fail 'KRM_MODE must be core, local-ui or ui';; esac
. "$ROOT/install/common/architecture.sh"
ARCH=$(krm_detect_arch) || fail "unsupported native CPU/ABI"
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/krm-install.XXXXXX")
TTY_HIDDEN=0
cleanup(){ [ "$TTY_HIDDEN" = 0 ] || stty echo < /dev/tty 2>/dev/null || true; rm -rf "$STAGE"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
[ "$PLATFORM" = keenetic ] || fail 'only Keenetic/Netcraze installations are supported'
PREFIX=/opt; BIN=/opt/bin; RUN=/opt/var/run/kee-route-manager
[ -d /opt ] || fail 'Entware /opt is not mounted'
CONFIG_DIR=$PREFIX/etc/kee-route-manager
UI_DIR=$PREFIX/etc/kee-route-manager-ui
CONFIG=$CONFIG_DIR/config.yaml
UI_CONFIG=$UI_DIR/config.yaml
# Local UI shares the controller launcher; standalone UI remains separate.
MANAGED_UI=0
if [ "$MODE" = local-ui ]; then
 MANAGED_UI=1
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
 # The signed seed may differ from the configured future update channel.
 # Refuse ambiguity; the launcher authenticates the selected seed independently.
 manifest=
 for channel in rc stable; do
  if [ -f "$ROOT/dist/manifest-$channel.json" ]; then
   [ -z "$manifest" ] || fail 'ambiguous signed release manifests'
   manifest=manifest-$channel.json
  fi
 done
 [ -n "$manifest" ] || fail 'missing signed release manifest'
 for name in "$manifest" "$manifest.sig" kee-route-manager-ui-linux-$ARCH; do
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
UI_UPSTREAM_CA_PATH=
if [ "$MODE" != core ]; then
 src=$ROOT/dist/kee-route-manager-ui-linux-$ARCH
 [ -x "$src" ] || fail 'missing UI executable'
 "$src" version >/dev/null
 if [ "$MODE" = ui ]; then UI_INPUT=$INPUT; else UI_INPUT=${KRM_UI_CONFIG_FILE:-}; fi
 [ -n "$UI_INPUT" ] && [ -f "$UI_INPUT" ] || fail 'local-ui requires KRM_UI_CONFIG_FILE pointing to a prepared UI config'
 "$src" validate --config "$UI_INPUT"
 UI_UPSTREAM_CA_PATH=$("$src" upstream-ca-path --config "$UI_INPUT")
 if [ "$MODE" = ui ]; then
  [ -n "$UI_UPSTREAM_CA_PATH" ] && [ -n "${KRM_UPSTREAM_CA_FILE:-}" ] || fail 'standalone UI requires HTTPS with trusted upstream CA and KRM_UPSTREAM_CA_FILE; use an authenticated SSH tunnel'
 fi
 if [ -n "$UI_UPSTREAM_CA_PATH" ]; then
  [ "$UI_UPSTREAM_CA_PATH" = "$UI_DIR/controller-ca.crt" ] || fail "ui.upstream_ca_file must be $UI_DIR/controller-ca.crt for installer-managed custom CA"
  [ -n "${KRM_UPSTREAM_CA_FILE:-}" ] || fail 'UI config requires a custom upstream CA; set KRM_UPSTREAM_CA_FILE before installation'
  [ -f "$KRM_UPSTREAM_CA_FILE" ] || fail 'KRM_UPSTREAM_CA_FILE does not exist'
  openssl x509 -in "$KRM_UPSTREAM_CA_FILE" -noout >/dev/null || fail 'invalid public upstream CA'
 elif [ -n "${KRM_UPSTREAM_CA_FILE:-}" ]; then
  fail 'KRM_UPSTREAM_CA_FILE was provided but ui.upstream_ca_file is empty'
 fi
 cp "$UI_INPUT" "$STAGE/ui.yaml"
fi
# Only configless local-ui supplies a private staged identity, before any service
# starts. The controller alone receives the private key; UI gets the public CA.
if [ -n "${KRM_GENERATED_TLS_DIR:-}" ]; then
 [ "$MODE" = local-ui ] || fail 'generated TLS identity requires local-ui'
 [ -f "$KRM_GENERATED_TLS_DIR/tls.crt" ] && [ -f "$KRM_GENERATED_TLS_DIR/tls.key" ] || fail 'generated controller TLS identity is missing'
 [ ! -e "$CONFIG_DIR/tls.crt" ] && [ ! -L "$CONFIG_DIR/tls.crt" ] && [ ! -e "$CONFIG_DIR/tls.key" ] && [ ! -L "$CONFIG_DIR/tls.key" ] || fail 'existing controller TLS identity detected'
 openssl x509 -in "$KRM_GENERATED_TLS_DIR/tls.crt" -noout >/dev/null || fail 'invalid generated controller certificate'
fi
# Refuse stale partial installs, pre-existing service scripts, symlinks and
# private credentials BEFORE writing anything. Even a dangling symlink can
# redirect cp/passwd into files not owned by KRM.
ensure_absent(){
 [ ! -e "$1" ] && [ ! -L "$1" ] || fail "existing managed path detected (do not overwrite): $1"
}
if [ "$MODE" != ui ]; then
 [ ! -e "$CONFIG" ] && [ ! -L "$CONFIG" ] && [ ! -e "$BIN/kee-route-managerd" ] && [ ! -L "$BIN/kee-route-managerd" ] && [ ! -e "$BIN/kee-route-manager-launcher" ] && [ ! -L "$BIN/kee-route-manager-launcher" ] || fail 'existing controller installation detected; use documented backup/uninstall/rollback procedure'
 [ ! -L "$CONFIG_DIR" ] || fail 'controller configuration directory must not be a symlink'
 for path in "$BIN/kee-route-managerctl" "$CONFIG_DIR/credentials.json" "$CONFIG_DIR/xray-status.sh" /opt/etc/init.d/S99kee-route-manager; do
  ensure_absent "$path"
 done
 if [ -n "${KRM_GENERATED_TLS_DIR:-}" ]; then
  ensure_absent "$CONFIG_DIR/tls.crt"
  ensure_absent "$CONFIG_DIR/tls.key"
 fi
fi
if [ "$MODE" != core ]; then
 [ ! -e "$UI_CONFIG" ] && [ ! -L "$UI_CONFIG" ] && [ ! -e "$BIN/kee-route-manager-ui" ] && [ ! -L "$BIN/kee-route-manager-ui" ] || fail 'existing UI installation detected'
 [ ! -L "$UI_DIR" ] || fail 'UI configuration directory must not be a symlink'
 ensure_absent "$UI_DIR/controller-ca.crt"
fi
if [ "$MANAGED_UI" = 1 ]; then
 UI_SERVICE=/opt/etc/init.d/S98kee-route-manager-ui
 [ ! -e "$UI_SERVICE" ] && [ ! -L "$UI_SERVICE" ] || fail 'existing standalone UI service detected; stop and uninstall it before a launcher-managed local-ui installation'
elif [ "$MODE" = ui ]; then
 ensure_absent /opt/etc/init.d/S98kee-route-manager-ui
fi
# The signed, read-only native preflight must succeed before any persistent
# KRM path, credential, service or launcher slot is created. In particular,
# missing/broken XKeen or Xray and ambiguous routing cannot become partial
# installations. The report is kept only in the private installer staging dir.
PREFLIGHT_CONFIG=$INPUT
[ "$MODE" != ui ] || PREFLIGHT_CONFIG=$UI_INPUT
if ! "$ROOT/dist/kee-route-managerctl-linux-$ARCH" preflight \
 --config "$PREFLIGHT_CONFIG" --mode "$MODE" \
 --staging-dir "$STAGE" --release-dir "$ROOT" > "$STAGE/preflight.json"; then
 cat "$STAGE/preflight.json" >&2
 fail 'router preflight failed before installation; resolve prerequisites and retry'
fi
mkdir -p "$BIN"
if [ "$MODE" != ui ]; then
 mkdir -p "$CONFIG_DIR" "$RUN"
 chmod 0700 "$CONFIG_DIR" "$RUN"
 cp "$STAGE/core.yaml" "$CONFIG"; chmod 0600 "$CONFIG"
 if [ -n "${KRM_GENERATED_TLS_DIR:-}" ]; then
  cp "$KRM_GENERATED_TLS_DIR/tls.crt" "$CONFIG_DIR/tls.crt"
  cp "$KRM_GENERATED_TLS_DIR/tls.key" "$CONFIG_DIR/tls.key"
  chmod 0644 "$CONFIG_DIR/tls.crt"; chmod 0600 "$CONFIG_DIR/tls.key"
 fi
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
if [ "$MODE" != core ] && [ -n "$UI_UPSTREAM_CA_PATH" ]; then
 cp "$KRM_UPSTREAM_CA_FILE" "$UI_UPSTREAM_CA_PATH"; chmod 0644 "$UI_UPSTREAM_CA_PATH"
fi
if [ "$MODE" != ui ]; then
 if [ "$MANAGED_UI" = 1 ]; then
  "$BIN/kee-route-manager-launcher" install --config "$CONFIG" --ui-config "$UI_CONFIG" --release-dir "$ROOT/dist"
 else
  "$BIN/kee-route-manager-launcher" install --config "$CONFIG" --release-dir "$ROOT/dist"
 fi
fi
mkdir -p /opt/etc/init.d
[ "$MODE" = ui ] || { cp "$ROOT/install/keenetic/S99kee-route-manager" /opt/etc/init.d/; chmod 0755 /opt/etc/init.d/S99kee-route-manager; /opt/etc/init.d/S99kee-route-manager start; }
[ "$MODE" != ui ] || { cp "$ROOT/install/keenetic/S98kee-route-manager-ui" /opt/etc/init.d/; chmod 0755 /opt/etc/init.d/S98kee-route-manager-ui; /opt/etc/init.d/S98kee-route-manager-ui start; }
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
printf 'Installed mode=%s. Check readiness, initial benchmark, HTTP(S) login according to UI config and router recovery using docs/AGENT_INSTALL.md.\n' "$MODE"
