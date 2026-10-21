#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
PLATFORM=${KRM_PLATFORM:?platform required}
MODE=${KRM_MODE:-local-ui}
case "$MODE" in core|local-ui|ui);; *) echo 'invalid mode' >&2; exit 1;; esac
[ "$PLATFORM" = keenetic ] || { echo 'only Keenetic/Netcraze installations are supported' >&2; exit 1; }
PREFIX=/opt; BIN=/opt/bin
CONFIG=$PREFIX/etc/kee-route-manager/config.yaml
if [ "$MODE" != ui ]; then
 # Restore through the live single owner BEFORE stopping it. Failure preserves installation.
 "$BIN/kee-route-managerctl" restore-xray --config "$CONFIG" || { echo 'Restore failed; installation retained. Start/recover daemon and retry.' >&2; exit 1; }
fi
stop_service(){
 name=$1
 if [ "$name" = kee-route-manager ]; then init=/opt/etc/init.d/S99kee-route-manager; else init=/opt/etc/init.d/S98kee-route-manager-ui; fi
 [ -x "$init" ] || return 0
 "$init" stop; rm -f "$init"
}
if [ "$MODE" != core ] && [ -x "$BIN/kee-route-manager-ui" ]; then stop_service kee-route-manager-ui; fi
if [ "$MODE" != ui ]; then stop_service kee-route-manager; rm -f "$BIN/kee-route-managerd" "$BIN/kee-route-managerctl" "$BIN/kee-route-manager-launcher"; fi
if [ "$MODE" != core ]; then rm -f "$BIN/kee-route-manager-ui"; fi
if [ "${1:-}" = --purge ]; then
 [ "$MODE" = ui ] || rm -rf "$PREFIX/etc/kee-route-manager" "$PREFIX/var/lib/kee-route-manager" "$PREFIX/var/lib/kee-route-manager-updates" "$PREFIX/var/cache/kee-route-manager"
 [ "$MODE" = core ] || rm -rf "$PREFIX/etc/kee-route-manager-ui" "$PREFIX/var/lib/kee-route-manager-ui" "$PREFIX/var/cache/kee-route-manager-ui"
fi
echo 'Services removed. Private config/state retained unless --purge was used. Before reinstall, back up then move retained config directory aside. See docs/AGENT_INSTALL.md.'
