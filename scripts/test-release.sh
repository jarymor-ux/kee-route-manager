#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
KRM_SOURCE_COMMIT="$(git rev-parse HEAD)"
export KRM_SOURCE_COMMIT
WORK="$(mktemp -d /tmp/krm-release.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
# Use an isolated source copy so the fixture never touches the production public key.
mkdir -p "$WORK/source"
tar --exclude=.git --exclude=.omx --exclude=dist --exclude=release -cf - . | tar -xf - -C "$WORK/source"
cd "$WORK/source"
go build -o "$WORK/tool" ./cmd/krm-release-tool
"$WORK/tool" keygen --public "$WORK/public" --private "$WORK/private"
cp "$WORK/public" release-public.key
KRM_RELEASE_PRIVATE_KEY="$WORK/private" OUTPUT_DIR="$WORK/output" ./scripts/build-release.sh
python3 scripts/verify-release.py "$WORK/output/dist"
# Change a published file: verifier must reject it even though signatures remain valid.
printf tampered >> "$WORK/output/dist/kee-route-manager-ui-linux-amd64"
if python3 scripts/verify-release.py "$WORK/output/dist"; then echo 'tampering was accepted' >&2; exit 1; fi
printf 'Signed release fixture and tamper rejection passed.\n'
