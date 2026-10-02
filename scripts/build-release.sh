#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

VERSION="$(tr -d '[:space:]' < VERSION)"
[[ -n "$VERSION" ]] || { echo 'VERSION is empty' >&2; exit 1; }
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT/release}"
PRIVATE_KEY="${KRM_RELEASE_PRIVATE_KEY:-}"
BASE_URL="${KRM_RELEASE_BASE_URL:-https://github.com/jarymor-ux/kee-route-manager/releases/download/v$VERSION}"
CHANNEL="${KRM_RELEASE_CHANNEL:-rc}"

if [[ -z "$PRIVATE_KEY" || ! -f "$PRIVATE_KEY" ]]; then
  echo 'KRM_RELEASE_PRIVATE_KEY must point to the Ed25519 private release key.' >&2
  exit 1
fi

"$ROOT/scripts/check.sh"

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR/dist"
DIST="$OUTPUT_DIR/dist"
COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || printf dev)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildTime=$BUILD_TIME"

build_target() {
  local goarch="$1" suffix="$2" goarm="${3:-}" gomips="${4:-}"
  local output="$DIST/kee-route-manager-linux-$suffix"
  echo "Building $output"
  if [[ -n "$goarm" ]]; then
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/kee-route-manager
  elif [[ -n "$gomips" ]]; then
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOMIPS="$gomips" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/kee-route-manager
  else
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/kee-route-manager
  fi
  chmod 0755 "$output"
}

build_target amd64 amd64
build_target arm64 arm64
build_target arm armv7 7
build_target mipsle mipsle '' softfloat

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/krm-release-tool-linux-amd64" ./cmd/krm-release-tool
chmod 0755 "$DIST/krm-release-tool-linux-amd64"

"$DIST/krm-release-tool-linux-amd64" manifest \
  --version "$VERSION" \
  --channel "$CHANNEL" \
  --base-url "$BASE_URL" \
  --dist "$DIST" \
  --out "$DIST/manifest-$CHANNEL.json" \
  --private "$PRIVATE_KEY" \
  --signature "$DIST/manifest-$CHANNEL.json.sig"

(
  cd "$DIST"
  sha256sum kee-route-manager-linux-* krm-release-tool-linux-amd64 manifest-"$CHANNEL".json manifest-"$CHANNEL".json.sig > SHA256SUMS
)

for target in amd64 arm64 armv7 mipsle; do
  "$DIST/kee-route-manager-linux-$target" version > "$DIST/version-$target.txt" || {
    # Non-native binaries cannot execute on the build host; validate metadata through strings instead.
    strings "$DIST/kee-route-manager-linux-$target" | grep -F "$VERSION" >/dev/null
    rm -f "$DIST/version-$target.txt"
  }
done

printf 'Release output: %s\n' "$OUTPUT_DIR"
