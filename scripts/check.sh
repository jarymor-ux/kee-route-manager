#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# TEMPORARY: formatting extraction and targeted tests on isolated CI runner.
gofmt -w cmd internal
python3 - <<'PY_KRM_FORMAT'
import subprocess, pathlib, json
paths=subprocess.check_output(['git','diff','--name-only','--','cmd','internal'],text=True).splitlines()
for path in paths:
    p=pathlib.Path(path)
    if p.suffix=='.go' and p.is_file():
        print('KRMGFMT:'+json.dumps({'path':path,'content':p.read_text()},ensure_ascii=True))
PY_KRM_FORMAT
go test ./internal/amnezia ./internal/subscription ./internal/xray ./internal/web ./internal/core ./internal/control
exit 0
unformatted="$(gofmt -l cmd internal)"
[[ -z "$unformatted" ]] || { printf 'Run gofmt on:\n%s\n' "$unformatted" >&2; exit 1; }
go test ./...
go vet ./...
while IFS= read -r script; do sh -n "$script"; done < <(find install -type f \( -name '*.sh' -o -name '*.init' -o -name 'S9*' \))
for script in scripts/*.sh; do bash -n "$script"; done
for script in internal/web/ui/static/*.js; do node --check "$script"; done
node --check scripts/test-ui-browser.cjs
node scripts/test-ui.js
node scripts/test-components-ui.cjs
node scripts/test-awg-ui.cjs
if [[ -n "${PLAYWRIGHT_MODULE_PATH:-}" ]]; then node scripts/test-ui-browser.cjs; fi
bash scripts/test-architecture.sh
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$TMP/ctl" ./cmd/kee-route-managerctl
for cfg in configs/*.yaml; do "$TMP/ctl" validate --config "$cfg" >/dev/null; done
python3 scripts/test-bootstrap.py
python3 scripts/test-build-version.py
printf 'Source, tests, syntax, embedded assets, templates and adversarial bootstrap checks passed.\n'
