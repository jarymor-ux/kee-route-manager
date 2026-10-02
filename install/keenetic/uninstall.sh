#!/bin/sh
set -eu
INIT=/opt/etc/init.d/S99kee-route-manager
BIN=/opt/bin/kee-route-manager
CONFIG=/opt/etc/kee-route-manager/config.yaml
[ "$(id -u)" = 0 ] || { echo "Run as root" >&2; exit 1; }
[ -x "$INIT" ] && "$INIT" stop || true
if [ -x "$BIN" ] && [ -f "$CONFIG" ]; then
  "$BIN" restore-xray --config "$CONFIG" || { echo "Xray restore failed; files were not removed" >&2; exit 1; }
fi
rm -f "$INIT" "$BIN"
if [ "${1:-}" = "--purge" ]; then rm -rf /opt/etc/kee-route-manager /opt/var/lib/kee-route-manager /opt/var/cache/kee-route-manager /opt/var/run/kee-route-manager; else echo "Configuration and state retained. Use --purge to remove them."; fi
echo "Kee Route Manager removed; original Xray routing restored."
