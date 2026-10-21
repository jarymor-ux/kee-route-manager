#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
OUTPUT="$(mktemp -d)"
trap 'rm -rf "$OUTPUT"' EXIT
for target in amd64 arm64 armv7 mipsle mips; do
 case "$target" in armv7) arch=arm; arm=7; mips=;; mipsle|mips) arch=$target; arm=; mips=softfloat;; *) arch=$target; arm=; mips=;; esac
 for component in kee-route-managerd kee-route-manager-ui kee-route-managerctl kee-route-manager-launcher krm-release-tool; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" GOMIPS="$mips" go build -trimpath -o "$OUTPUT/$component-$target" "./cmd/$component"
 done
 if [[ "$target" = mips || "$target" = mipsle ]]; then
  python3 - "$OUTPUT" "$target" <<'PYELF'
import pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);arch=sys.argv[2]
for binary in root.glob('*-'+arch):
 header=binary.read_bytes()[:40]
 assert header[:5]==b'\x7fELF\x01', binary
 assert header[5]==(2 if arch=='mips' else 1), binary
 order='big' if arch=='mips' else 'little'
 assert int.from_bytes(header[18:20],order)==8, binary
 assert int.from_bytes(header[36:40],order)&0xf020==0x1000, binary
 settings=subprocess.check_output(['go','version','-m',str(binary)],text=True)
 assert 'GOMIPS=softfloat' in settings and 'CGO_ENABLED=0' in settings, binary
PYELF
  emulator=qemu-mips
  [[ "$target" = mips ]] || emulator=qemu-mipsel
  if command -v "$emulator" >/dev/null 2>&1; then
   for component in kee-route-managerd kee-route-manager-ui kee-route-managerctl kee-route-manager-launcher; do
    timeout 60 "$emulator" "$OUTPUT/$component-$target" version
   done
   # The release tool has no version command: parse help without touching keys.
   set +e
   timeout 60 "$emulator" "$OUTPUT/krm-release-tool-$target" sign --help > "$OUTPUT/help-$target" 2>&1
   status=$?
   set -e
   if [[ "$status" != 1 ]] || ! grep -Fq 'Usage of sign:' "$OUTPUT/help-$target"; then
    cat "$OUTPUT/help-$target" >&2
    echo "MIPS release-tool smoke failed (exit $status)" >&2
    exit 1
   fi
   printf 'QEMU userspace smoke passed: %s (not router hardware acceptance).\n' "$target"
  else
   printf 'QEMU userspace smoke pending: %s is not installed.\n' "$emulator"
  fi
 fi
done
printf 'Cross-build passed: 5 components x 5 architectures.\n'
