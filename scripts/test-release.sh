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
export KRM_RELEASE_PUBLIC_KEY="$WORK/public"
# Build both channel shapes with real cross-compiled components. Isolated fixture
# versions never create tags, upload releases, or alter repository VERSION.
source_version=$(tr -d '[:space:]' < VERSION)
source_channel=$(python3 scripts/release_channel.py "$source_version")
if [[ "$source_channel" == rc ]]; then opposite_version=9.9.9; else opposite_version=9.9.9-rc.1; fi
for fixture_version in "$source_version" "$opposite_version"; do
 printf '%s\n' "$fixture_version" > VERSION
 channel=$(python3 scripts/release_channel.py "$fixture_version")
 output="$WORK/output-$channel"
 KRM_RELEASE_CHANNEL="$channel" KRM_RELEASE_PRIVATE_KEY="$WORK/private" OUTPUT_DIR="$output" ./scripts/build-release.sh
 python3 scripts/verify-release.py "$output/dist"
 # Change a file: verifier must reject it even though signatures remain valid.
 printf tampered >> "$output/dist/kee-route-manager-ui-linux-amd64"
 if python3 scripts/verify-release.py "$output/dist"; then echo 'tampering was accepted' >&2; exit 1; fi
done
printf 'Both signed release channels and tamper rejection passed.\n'
