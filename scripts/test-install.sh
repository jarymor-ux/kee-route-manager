#!/usr/bin/env bash
# Real installer filesystem lifecycle in a disposable Linux container; never host.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run only in a disposable Docker container' >&2; exit 1; }
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
cleanup(){
 for pidfile in /tmp/krm-test-service-*.pid; do
  [[ ! -s "$pidfile" ]] || kill "$(cat "$pidfile")" 2>/dev/null || true
 done
 rm -rf "$WORK"
}
trap cleanup EXIT
mkdir -p "$WORK/source" "$WORK/fake" "$WORK/private"
tar -C "$ROOT" --exclude=.git --exclude=.omx --exclude=release --exclude=dist -cf - . | tar -xf - -C "$WORK/source"
cd "$WORK/source"
case "$(uname -m)" in aarch64) ARCH=arm64;; x86_64) ARCH=amd64;; *) exit 1;; esac
mkdir dist
for name in kee-route-managerd kee-route-managerctl kee-route-manager-ui; do go build -o "dist/$name-linux-$ARCH" "./cmd/$name"; done
cat > "$WORK/fake/systemctl" <<'MANAGER'
#!/bin/sh
set -eu
case "$1" in
 daemon-reload) exit 0;;
 enable)
  name=$3
  if [ "$name" = kee-route-manager ]; then binary=/usr/local/bin/kee-route-managerd; config=/etc/kee-route-manager/config.yaml; else binary=/usr/local/bin/kee-route-manager-ui; config=/etc/kee-route-manager-ui/config.yaml; fi
  "$binary" serve --config "$config" > "/tmp/$name.stdout" 2>&1 &
  echo "$!" > "/tmp/krm-test-service-$name.pid";;
 disable)
  name=$3
  file=/tmp/krm-test-service-$name.pid
  if [ -s "$file" ]; then
   pid=$(cat "$file"); kill "$pid"
   for attempt in 1 2 3 4 5 6 7 8 9 10; do
    : "$attempt"
    [ -r "/proc/$pid/stat" ] || break
    [ "$(awk '{print $3}' "/proc/$pid/stat")" != Z ] || break
    sleep 1
   done
   rm -f "$file"
  fi;;
 *) echo 'unexpected service command' >&2; exit 1;;
esac
MANAGER
chmod 0755 "$WORK/fake/systemctl"
export PATH="$WORK/fake:$PATH"
# Core has no sources enabled; safe unconfigured readiness is explicit. No Xray/firewall mutation.
python3 - "$WORK" <<'PY'
import pathlib,sys
w=pathlib.Path(sys.argv[1]);s=pathlib.Path('configs/linux-systemd.yaml').read_text().replace('enabled: true\n      headers:', 'enabled: false\n      headers:')
s=s.replace('base_routing_file: /etc/xray/configs/05_routing.json','base_routing_file: /etc/xray/configs/05_routing.json')
(w/'private/core.yaml').write_text(s)
(w/'private/ui.yaml').write_text(pathlib.Path('configs/ui-proxy.yaml').read_text())
(w/'private/password').write_text('integration-only-long-password\n')
PY
mkdir -p /etc/systemd/system
export KRM_MODE=local-ui KRM_CONFIG_FILE="$WORK/private/core.yaml" KRM_UI_CONFIG_FILE="$WORK/private/ui.yaml" KRM_PASSWORD_FILE="$WORK/private/password"
sh install/linux-systemd/install.sh
/usr/local/bin/kee-route-managerctl ready --config /etc/kee-route-manager/config.yaml
for attempt in 1 2 3 4 5 6 7 8 9 10; do
 : "$attempt"
 curl --cacert /var/lib/kee-route-manager-ui/tls.crt -fsS https://127.0.0.1:9444/assets/app.js >/dev/null 2>&1 && break
 sleep 1
done
curl --cacert /var/lib/kee-route-manager-ui/tls.crt -fsS https://127.0.0.1:9444/assets/app.js >/dev/null
# Reinstall cannot overwrite an installed config/binary.
before=$(sha256sum /etc/kee-route-manager/config.yaml)
if sh install/linux-systemd/install.sh; then echo 'overwrite accepted' >&2; exit 1; fi
[[ "$before" == "$(sha256sum /etc/kee-route-manager/config.yaml)" ]]
sh install/linux-systemd/uninstall.sh --purge
[[ ! -e /usr/local/bin/kee-route-managerd && ! -e /usr/local/bin/kee-route-manager-ui && ! -e /etc/kee-route-manager/config.yaml ]]
sh install/linux-systemd/install.sh
/usr/local/bin/kee-route-managerctl ready --config /etc/kee-route-manager/config.yaml
sh install/linux-systemd/uninstall.sh --purge
printf 'Installer lifecycle passed: local-ui install, trusted HTTPS assets, overwrite refusal, restore/uninstall/purge, reinstall. Service manager is a fake; real systemd/hardware remains pending.\n'
