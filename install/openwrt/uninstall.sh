#!/bin/sh
set -eu
/etc/init.d/kee-route-manager stop 2>/dev/null || true
if [ -x /usr/bin/kee-route-manager ] && [ -f /etc/kee-route-manager/config.yaml ]; then /usr/bin/kee-route-manager restore-xray --config /etc/kee-route-manager/config.yaml; fi
/etc/init.d/kee-route-manager disable 2>/dev/null || true
rm -f /etc/init.d/kee-route-manager /usr/bin/kee-route-manager
if [ "${1:-}" = "--purge" ]; then rm -rf /etc/kee-route-manager /var/lib/kee-route-manager /var/cache/kee-route-manager /var/run/kee-route-manager; fi
echo "Kee Route Manager removed."
