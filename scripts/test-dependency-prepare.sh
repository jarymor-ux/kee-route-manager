#!/usr/bin/env bash
# Synthetic offline archive acceptance. The real pinned downloader is tested
# separately; this replaces only the test checkout's downloader, not trust.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/checkout/install/common" "$WORK/source/_xkeen"
cp "$ROOT/install/common/dependency-prepare.sh" "$WORK/checkout/install/common/"
cat > "$WORK/checkout/install/common/architecture.sh" <<'ARCH'
krm_detect_arch(){ printf 'arm64\n'; }
ARCH
cat > "$WORK/checkout/install/common/dependency-assets.sh" <<'DOWNLOAD'
#!/bin/sh
set -eu
kind=$1
dest=$2
mkdir -m 0700 "$dest"
case "$kind" in
 xkeen) name=xkeen.tar.gz;;
 xray) name=Xray-linux-arm64-v8a.zip;;
 *) exit 1;;
esac
cp "$PREPARE_TEST_ARCHIVE" "$dest/$name"
DOWNLOAD
printf '#!/bin/sh\nexit 0\n' > "$WORK/source/xkeen"
printf 'module\n' > "$WORK/source/_xkeen/readme"
tar -czf "$WORK/good-xkeen.tar.gz" -C "$WORK/source" xkeen _xkeen
helper="$WORK/checkout/install/common/dependency-prepare.sh"
PREPARE_TEST_ARCHIVE="$WORK/good-xkeen.tar.gz" sh "$helper" xkeen "$WORK/ok-xkeen" > /dev/null
cmp "$WORK/ok-xkeen/extracted/xkeen" "$WORK/source/xkeen"
[[ -f "$WORK/ok-xkeen/extracted/_xkeen/readme" ]]
[[ ! -e "$WORK/ok-xkeen/entries" ]]
if PREPARE_TEST_ARCHIVE="$WORK/good-xkeen.tar.gz" sh "$helper" xkeen "$WORK/ok-xkeen" >/dev/null 2>&1; then
 echo 'existing prepared directory was overwritten' >&2; exit 1
fi
ln -s /tmp/outside "$WORK/source/_xkeen/link"
tar -czf "$WORK/link-xkeen.tar.gz" -C "$WORK/source" xkeen _xkeen
if PREPARE_TEST_ARCHIVE="$WORK/link-xkeen.tar.gz" sh "$helper" xkeen "$WORK/bad-link" >/dev/null 2>&1; then
 echo 'XKeen symlink archive accepted' >&2; exit 1
fi
[[ ! -e "$WORK/bad-link" ]]
python3 - "$WORK" <<'PY'
import io, pathlib, sys, tarfile, zipfile
w = pathlib.Path(sys.argv[1])
with tarfile.open(w/'traversal.tar.gz','w:gz') as tf:
 data=b'untrusted'
 info=tarfile.TarInfo('../escape'); info.size=len(data)
 tf.addfile(info,io.BytesIO(data))
header=bytearray(2048)
header[:4]=b'\x7fELF'
header[4]=2
header[5]=1
header[18]=183
with zipfile.ZipFile(w/'valid-xray.zip','w',compression=zipfile.ZIP_DEFLATED) as z:
 z.writestr('xray',header)
with zipfile.ZipFile(w/'bad-elf.zip','w',compression=zipfile.ZIP_DEFLATED) as z:
 z.writestr('xray',b'x'*2048)
with zipfile.ZipFile(w/'duplicate.zip','w',compression=zipfile.ZIP_DEFLATED) as z:
 z.writestr('xray',header)
 z.writestr('xray',header)
PY
if PREPARE_TEST_ARCHIVE="$WORK/traversal.tar.gz" sh "$helper" xkeen "$WORK/bad-traversal" >/dev/null 2>&1; then
 echo 'XKeen path traversal accepted' >&2; exit 1
fi
[[ ! -e "$WORK/bad-traversal" && ! -e "$WORK/escape" ]]
PREPARE_TEST_ARCHIVE="$WORK/valid-xray.zip" sh "$helper" xray "$WORK/ok-xray" >/dev/null
[[ -f "$WORK/ok-xray/extracted/xray" ]]
[[ "$(stat -c %a "$WORK/ok-xray/extracted/xray")" = 700 ]]
for scenario in bad-elf duplicate; do
 if PREPARE_TEST_ARCHIVE="$WORK/$scenario.zip" sh "$helper" xray "$WORK/bad-$scenario" >/dev/null 2>&1; then
  echo "Xray $scenario archive accepted" >&2; exit 1
 fi
 [[ ! -e "$WORK/bad-$scenario" ]]
done
printf 'Pinned dependency preparation: synthetic valid payloads, ELF, links, paths, duplicates and non-overwrite verified.\n'
