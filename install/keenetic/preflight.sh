#!/bin/sh
# Run only from an authenticated extracted release, before installer writes.
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
. "$ROOT/install/common/architecture.sh"
ARCH=$(krm_detect_arch) || exit 1
CTL=$ROOT/dist/kee-route-managerctl-linux-$ARCH
[ -x "$CTL" ] || { echo 'ERROR: signed preflight CLI is missing' >&2; exit 1; }
[ -n "${KRM_CONFIG_FILE:-}" ] || { echo 'ERROR: KRM_CONFIG_FILE must name a prepared private configuration' >&2; exit 1; }
exec "$CTL" preflight --config "$KRM_CONFIG_FILE" --mode "${KRM_MODE:-local-ui}" --release-dir "$ROOT"
