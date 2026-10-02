#!/bin/bash
set -euo pipefail
TASK_DIR=/tmp/krm-linux-runtime
[ "$(uname -s)" = Linux ] || { echo 'Linux-only test; use Docker' >&2; exit 1; }
[ ! -e "$TASK_DIR" ] || { echo 'fixture directory already exists' >&2; exit 1; }
mkdir -p "$TASK_DIR"
cd "$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
for component in kee-route-managerd kee-route-managerctl kee-route-manager-ui; do go build -o "$TASK_DIR/$component" ./cmd/$component; done
cat > "$TASK_DIR/status" <<'STATUS'
#!/bin/sh
printf 'called\n' >> /tmp/krm-linux-runtime/status-calls
exit 0
STATUS
chmod 0755 "$TASK_DIR/status"
cat > "$TASK_DIR/core.yaml" <<'CONFIG'
schema_version: 1
instance:
  role: controller
paths:
  state_dir: /tmp/krm-linux-runtime/state
  cache_dir: /tmp/krm-linux-runtime/cache
  run_dir: /tmp/krm-linux-runtime/run
  log_file: /tmp/krm-linux-runtime/core.log
api:
  enabled: true
  listen: "127.0.0.1:19443"
  unix_socket: /tmp/krm-linux-runtime/run/control.sock
  tls:
    enabled: true
    auto_generate: true
    cert_file: /tmp/krm-linux-runtime/api.crt
    key_file: /tmp/krm-linux-runtime/api.key
web:
  enabled: false
  credentials_file: /tmp/krm-linux-runtime/credentials.json
platform:
  kind: linux-systemd
  xray_status_command: [/tmp/krm-linux-runtime/status]
xray:
  config_dir: /tmp/krm-linux-runtime/xray
  managed_dir: /tmp/krm-linux-runtime/xray
  base_routing_file: /tmp/krm-linux-runtime/xray/routing.json
subscriptions:
  sources:
    - id: offline
      enabled: false
      url: "https://unused.example.invalid/subscription"
targets:
  - id: score
    url: "https://score.example.invalid/test"
    role: score
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 4096
  - id: health1
    url: "https://one.example.invalid/test"
    role: health
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 4096
  - id: health2
    url: "https://two.example.invalid/test"
    role: health
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 4096
CONFIG
"$TASK_DIR/kee-route-managerd" validate --config "$TASK_DIR/core.yaml"
printf 'integration-only-password\n' | "$TASK_DIR/kee-route-managerctl" passwd --config "$TASK_DIR/core.yaml" --username admin --password-stdin
CERT=$("$TASK_DIR/kee-route-managerd" tls-init --config "$TASK_DIR/core.yaml")
[ "$CERT" = "$TASK_DIR/api.crt" ] && [ -f "$CERT" ]
"$TASK_DIR/kee-route-managerd" serve --config "$TASK_DIR/core.yaml" > "$TASK_DIR/daemon.stdout" 2>&1 &
OWNER=$!
cleanup(){ kill "$OWNER" "${UI_PID:-$OWNER}" 2>/dev/null || true; wait "$OWNER" 2>/dev/null || true; }
trap 'cleanup; rm -rf "$TASK_DIR"' EXIT
for attempt in $(seq 1 50); do : "$attempt"; "$TASK_DIR/kee-route-managerctl" ready --config "$TASK_DIR/core.yaml" > "$TASK_DIR/ready.json" 2>/dev/null && break; sleep .1; done
"$TASK_DIR/kee-route-managerctl" ready --config "$TASK_DIR/core.yaml"
if "$TASK_DIR/kee-route-managerd" serve --config "$TASK_DIR/core.yaml" > "$TASK_DIR/second.log" 2>&1; then echo 'second daemon started'; exit 1; fi
grep -q 'another controller owns' "$TASK_DIR/second.log"
# A different state directory cannot evade tunnel ownership.
sed 's@state_dir: /tmp/krm-linux-runtime/state@state_dir: /tmp/krm-linux-runtime/alternate-state@' "$TASK_DIR/core.yaml" > "$TASK_DIR/alternate.yaml"
if "$TASK_DIR/kee-route-managerd" serve --config "$TASK_DIR/alternate.yaml" > "$TASK_DIR/alternate.log" 2>&1; then echo 'alternate-state daemon started'; exit 1; fi
grep -q 'tunnel ownership' "$TASK_DIR/alternate.log"
# Let startup benchmark finish, then capture persistent mutation and external commands.
sleep .2
find "$TASK_DIR/state" "$TASK_DIR/cache" "$TASK_DIR/xray" -type f -exec sha256sum {} \; | sort > "$TASK_DIR/before.sha"
COUNT=$(wc -l < "$TASK_DIR/status-calls")
for attempt in $(seq 1 30); do : "$attempt"; "$TASK_DIR/kee-route-managerctl" status --config "$TASK_DIR/core.yaml" >/dev/null; "$TASK_DIR/kee-route-managerctl" ready --config "$TASK_DIR/core.yaml" >/dev/null; done
find "$TASK_DIR/state" "$TASK_DIR/cache" "$TASK_DIR/xray" -type f -exec sha256sum {} \; | sort > "$TASK_DIR/after.sha"
cmp "$TASK_DIR/before.sha" "$TASK_DIR/after.sha"
[ "$COUNT" = "$(wc -l < "$TASK_DIR/status-calls")" ]
"$TASK_DIR/kee-route-managerctl" benchmark --config "$TASK_DIR/core.yaml" | tee "$TASK_DIR/benchmark.json"
grep -q 'operation_id' "$TASK_DIR/benchmark.json"
[ "$(stat -c '%a' "$TASK_DIR/run/control.sock")" = 600 ]
cat > "$TASK_DIR/ui.yaml" <<'CONFIG'
instance:
  role: ui
paths:
  state_dir: /must-not-create-controller-state
  cache_dir: /must-not-create-controller-cache
  run_dir: /must-not-create-controller-run
  log_file: /tmp/krm-linux-runtime/ui.log
api:
  enabled: false
web:
  listen: "127.0.0.1:19444"
  tls:
    enabled: false
ui:
  upstream: "https://127.0.0.1:19443"
  upstream_ca_file: /tmp/krm-linux-runtime/api.crt
xray:
  config_dir: /must-not-create-xray
CONFIG
"$TASK_DIR/kee-route-manager-ui" validate --config "$TASK_DIR/ui.yaml"
if "$TASK_DIR/kee-route-managerd" validate --config "$TASK_DIR/ui.yaml" > "$TASK_DIR/role.log" 2>&1; then echo 'daemon validates UI role'; exit 1; fi
"$TASK_DIR/kee-route-manager-ui" serve --config "$TASK_DIR/ui.yaml" > "$TASK_DIR/ui.stdout" 2>&1 &
UI_PID=$!
for attempt in $(seq 1 50); do : "$attempt"; curl -fsS http://127.0.0.1:19444/assets/app.css >/dev/null 2>&1 && break; sleep .1; done
for path in / /assets/app.css /assets/app.js /sw.js /manifest.webmanifest /healthz; do curl -fsS "http://127.0.0.1:19444$path" >/dev/null; done
for path in /must-not-create-controller-state /must-not-create-controller-cache /must-not-create-controller-run /must-not-create-xray; do [ ! -e "$path" ]; done
# Reject foreign origin before forwarding it into the trusted core.
CODE=$(curl -sS -o "$TASK_DIR/origin.json" -w '%{http_code}' -H 'Origin: http://evil.example' -X POST http://127.0.0.1:19444/api/v1/actions/direct)
[ "$CODE" = 403 ]
printf 'Linux subprocess smoke passed: owner lock, alternate state lock, read-only status, cached readiness, CLI benchmark, TLS-init, UI isolation, assets, CA proxy, CSRF origin.\n'
