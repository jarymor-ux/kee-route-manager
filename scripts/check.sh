#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

unformatted="$(gofmt -l cmd internal)"
if [[ -n "$unformatted" ]]; then
  printf 'The following Go files are not gofmt-formatted:\n%s\n' "$unformatted" >&2
  exit 1
fi

go test ./...
go vet ./...

sh -n install/keenetic/install.sh
sh -n install/keenetic/S99kee-route-manager
sh -n install/keenetic/uninstall.sh
sh -n install/openwrt/install.sh
sh -n install/openwrt/kee-route-manager.init
sh -n install/openwrt/uninstall.sh
bash -n install/linux-systemd/install.sh
bash -n install/linux-systemd/uninstall.sh
bash -n install/ui-proxy/install.sh

if command -v node >/dev/null 2>&1; then
  node --check web/app.js
fi

for name in index.html app.css app.js manifest.webmanifest sw.js; do
  cmp -s "web/$name" "internal/web/static/$name" || {
    echo "embedded web asset differs from web/$name" >&2
    exit 1
  }
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$TMP/kee-route-manager" ./cmd/kee-route-manager
for cfg in configs/keenetic.yaml configs/openwrt.yaml configs/linux-systemd.yaml configs/ui-proxy.yaml; do
  "$TMP/kee-route-manager" validate --config "$cfg" >/dev/null
done

printf 'All source, test, syntax, embedded-asset and configuration checks passed.\n'
