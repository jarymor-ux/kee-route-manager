#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
KRM_VERSION="$(tr -d '[:space:]' < VERSION)"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT/release}"
PRIVATE_KEY="${KRM_RELEASE_PRIVATE_KEY:-}"
PUBLIC_KEY="${KRM_RELEASE_PUBLIC_KEY:-$ROOT/internal/releasetrust/public.key}"
BASE_URL="${KRM_RELEASE_BASE_URL:-https://github.com/jarymor-ux/kee-route-manager/releases/download/v$KRM_VERSION}"
CHANNEL="$(python3 scripts/release_channel.py "$KRM_VERSION" "${KRM_RELEASE_CHANNEL:-}")"
export KRM_RELEASE_CHANNEL="$CHANNEL"
[[ -n "$PRIVATE_KEY" && -f "$PRIVATE_KEY" ]] || { echo 'KRM_RELEASE_PRIVATE_KEY must point to an external Ed25519 private key' >&2; exit 1; }
[[ -f "$PUBLIC_KEY" ]] || { echo 'KRM_RELEASE_PUBLIC_KEY must point to an Ed25519 public key' >&2; exit 1; }
export KRM_RELEASE_PUBLIC_KEY="$PUBLIC_KEY"
# Refuse recursive deletion of arbitrary caller paths.
[[ "$OUTPUT_DIR" == "$ROOT/release" || "$OUTPUT_DIR" == /tmp/krm-release.* || "$OUTPUT_DIR" == /tmp/krm-release.*/output ]] || { echo 'OUTPUT_DIR must be repo/release or /tmp/krm-release.*' >&2; exit 1; }
./scripts/check.sh
mkdir -p "$OUTPUT_DIR"
DIST="$OUTPUT_DIR/dist"
[[ ! -e "$DIST" ]] || { echo 'Release output already exists; move it aside before rebuilding' >&2; exit 1; }
mkdir -p "$DIST"
TOOL_DIR="$(mktemp -d)"
trap 'rm -rf "$TOOL_DIR"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$TOOL_DIR/krm-release-tool" ./cmd/krm-release-tool
COMMIT="${KRM_SOURCE_COMMIT:-$(git rev-parse --short=12 HEAD)}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=$KRM_VERSION -X main.commit=$COMMIT -X main.buildTime=$BUILD_TIME"
for target in amd64 arm64 armv7 mipsle; do
 case "$target" in armv7) arch=arm; arm=7; mips=;; mipsle) arch=mipsle; arm=; mips=softfloat;; *) arch=$target; arm=; mips=;; esac
 for component in kee-route-managerd kee-route-manager-ui kee-route-managerctl kee-route-manager-launcher krm-release-tool; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" GOMIPS="$mips" go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/$component-linux-$target" "./cmd/$component"
 done
done
# Signed install payload contains configuration, all service/uninstall scripts and the selected trust key.
TRUST_DIR="$TOOL_DIR/trust"
mkdir -p "$TRUST_DIR"
cp "$PUBLIC_KEY" "$TRUST_DIR/release-public.key"
tar -czf "$DIST/release-files.tar.gz" configs install LICENSE NOTICE -C "$TRUST_DIR" release-public.key
python3 scripts/prepare-release.py "$DIST" "$KRM_VERSION"
"$TOOL_DIR/krm-release-tool" manifest --version "$KRM_VERSION" --channel "$CHANNEL" --base-url "$BASE_URL" --dist "$DIST" --out "$DIST/manifest-$CHANNEL.json" --private "$PRIVATE_KEY" --signature "$DIST/manifest-$CHANNEL.json.sig"
python3 - "$DIST" <<'PY'
import hashlib,pathlib,sys
p=pathlib.Path(sys.argv[1]);entries=[]
for f in sorted(p.iterdir()):
 if f.is_file() and f.name not in {'SHA256SUMS','SHA256SUMS.sig'}:
  entries.append(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n')
(p/'SHA256SUMS').write_text(''.join(entries))
PY
"$TOOL_DIR/krm-release-tool" sign --private "$PRIVATE_KEY" --input "$DIST/SHA256SUMS" --out "$DIST/SHA256SUMS.sig"
python3 scripts/verify-release.py "$DIST"
printf 'Signed %s artifacts: %s\n' "$CHANNEL" "$DIST"
