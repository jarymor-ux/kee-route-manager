#!/usr/bin/env bash
# Synthetic Linux/amd64 resource measurements, never router acceptance.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
OUTPUT=${1:-$(mktemp -d /tmp/krm-memory.XXXXXX)}
mkdir -p "$OUTPUT/bin"
OUTPUT=$(cd "$OUTPUT" && pwd)
case "$OUTPUT/" in "$ROOT/"*) echo 'Keep measurement artifacts outside the checkout' >&2; exit 1;; esac
for name in kee-route-managerd kee-route-managerctl kee-route-manager-ui; do
 CGO_ENABLED=0 go build -trimpath -o "$OUTPUT/bin/$name" "./cmd/$name"
done
CGO_ENABLED=0 go test -c -o "$OUTPUT/bin/subscription.test" ./internal/subscription
CGO_ENABLED=0 go test -c -o "$OUTPUT/bin/bench.test" ./internal/bench
CGO_ENABLED=0 go test -c -o "$OUTPUT/bin/xray.test" ./internal/xray
image=${KRM_MEASUREMENT_IMAGE:-krm-test}
docker image inspect "$image" >/dev/null
for memory in 128 256 512 1024; do
 mkdir -p "$OUTPUT/$memory"
 docker run --rm --network none --memory "${memory}m" --memory-swap "${memory}m" --cpus 2 \
  --pids-limit 128 --read-only --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  -v "$OUTPUT/bin:/fixtures:ro" -v "$ROOT/scripts/measure-low-memory.py:/measure.py:ro" \
  -v "$OUTPUT/$memory:/measurements" "$image" python3 /measure.py \
  | tee "$OUTPUT/$memory/result.json"
done
printf 'Synthetic measurements and private local profiles: %s\n' "$OUTPUT"
