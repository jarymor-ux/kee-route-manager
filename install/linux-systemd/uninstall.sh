#!/usr/bin/env bash
set -euo pipefail
systemctl stop kee-route-manager 2>/dev/null || true
if [[ -x /usr/local/sbin/kee-route-manager && -f /etc/kee-route-manager/config.yaml ]]; then /usr/local/sbin/kee-route-manager restore-xray --config /etc/kee-route-manager/config.yaml; fi
systemctl disable kee-route-manager 2>/dev/null || true
rm -f /etc/systemd/system/kee-route-manager.service /usr/local/sbin/kee-route-manager
systemctl daemon-reload
if [[ ${1:-} == --purge ]]; then rm -rf /etc/kee-route-manager /var/lib/kee-route-manager /var/cache/kee-route-manager /run/kee-route-manager; fi
echo "Kee Route Manager removed."
