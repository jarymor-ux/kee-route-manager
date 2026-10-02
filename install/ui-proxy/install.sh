#!/usr/bin/env bash
set -euo pipefail
umask 077
SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd); ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
fail(){ echo "ERROR: $*" >&2; exit 1; }
safe_yaml(){ [[ -n $1 && $1 != *'\'* && $1 != *'"'* && $1 != *$'\n'* && $1 != *$'\r'* && $1 != *$'\t'* ]] || fail "invalid value"; }
[[ $EUID -eq 0 ]] || fail "run as root"
case "$(uname -m)" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; *) echo "Unsupported architecture" >&2; exit 1;; esac
SRC=${KRM_BINARY:-$ROOT/dist/kee-route-manager-linux-$ARCH}; [[ -x $SRC ]] || { echo "Binary missing: $SRC" >&2; exit 1; }
read -r -p 'Controller URL, e.g. https://192.168.1.1:9443: ' UPSTREAM
read -r -p 'Local listen address [0.0.0.0:9444]: ' LISTEN; LISTEN=${LISTEN:-0.0.0.0:9444}
read -r -p 'Trust controller self-signed TLS certificate? [y/N]: ' INSECURE
safe_yaml "$UPSTREAM"; safe_yaml "$LISTEN"
install -d -m 0700 /etc/kee-route-manager-ui /var/lib/kee-route-manager-ui /var/cache/kee-route-manager-ui /run/kee-route-manager-ui
install -m 0755 "$SRC" /usr/local/bin/kee-route-manager-ui
install -m 0600 "$ROOT/configs/ui-proxy.yaml" /etc/kee-route-manager-ui/config.yaml
python3 - "$UPSTREAM" "$LISTEN" "$INSECURE" <<'PY'
from pathlib import Path
import sys
p=Path('/etc/kee-route-manager-ui/config.yaml'); s=p.read_text(); s=s.replace('https://192.168.1.1:9443',sys.argv[1]).replace('0.0.0.0:9444',sys.argv[2]); s=s.replace('insecure_tls: true', 'insecure_tls: '+('true' if sys.argv[3].lower() in {'y','yes'} else 'false')); p.write_text(s)
PY
install -m 0644 "$SCRIPT_DIR/kee-route-manager-ui.service" /etc/systemd/system/
systemctl daemon-reload; systemctl enable --now kee-route-manager-ui
echo "UI proxy installed at https://<this-device>:${LISTEN##*:}/"
