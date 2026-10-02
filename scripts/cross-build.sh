#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
OUTPUT="$(mktemp -d)"
trap 'rm -rf "$OUTPUT"' EXIT
for target in amd64 arm64 armv7 mipsle; do
 case "$target" in armv7) arch=arm; arm=7; mips=;; mipsle) arch=mipsle; arm=; mips=softfloat;; *) arch=$target; arm=; mips=;; esac
 for component in kee-route-managerd kee-route-manager-ui kee-route-managerctl krm-release-tool; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" GOMIPS="$mips" go build -trimpath -o "$OUTPUT/$component-$target" "./cmd/$component"
 done
done
printf 'Cross-build passed: 4 components x 4 architectures.\n'
