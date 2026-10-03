#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
unformatted="$(gofmt -l cmd internal)"
[[ -z "$unformatted" ]] || { printf 'Run gofmt on:\n%s\n' "$unformatted" >&2; exit 1; }
go test ./...
go vet ./...
while IFS= read -r script; do sh -n "$script"; done < <(find install -type f \( -name '*.sh' -o -name '*.init' -o -name 'S9*' \))
for script in scripts/*.sh; do bash -n "$script"; done
node --check internal/web/ui/static/app.js
node --check internal/web/ui/static/sw.js
node scripts/test-ui.js
./scripts/test-architecture.sh
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$TMP/ctl" ./cmd/kee-route-managerctl
for cfg in configs/*.yaml; do "$TMP/ctl" validate --config "$cfg" >/dev/null; done
python3 scripts/test-bootstrap.py
printf 'Source, tests, syntax, embedded assets, templates and adversarial bootstrap checks passed.\n'
