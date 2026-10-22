#!/usr/bin/env bash
# Offline adversarial regression: a successful downloader is not a trusted asset.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/curl" <<'FAKE_CURL'
#!/bin/sh
set -eu
out=
while [ "$#" -gt 0 ]; do
 case "$1" in -o) out=$2; shift 2;; *) shift;; esac
done
[ -n "$out" ] || exit 1
printf 'unverified payload\n' > "$out"
FAKE_CURL
chmod 0755 "$WORK/bin/curl"
if PATH="$WORK/bin:$PATH" sh "$ROOT/install/common/dependency-assets.sh" xkeen "$WORK/unverified" >"$WORK/stdout" 2>"$WORK/stderr"; then
 echo 'tampered XKeen archive accepted' >&2; exit 1
fi
grep -Fq 'dependency asset size mismatch' "$WORK/stderr"
[[ ! -e "$WORK/unverified" ]]
if sh "$ROOT/install/common/dependency-assets.sh" xkeen "$WORK/../relative" >/dev/null 2>&1; then
 echo 'existing parent path was unexpectedly accepted' >&2; exit 1
fi
mkdir "$WORK/existing"
if sh "$ROOT/install/common/dependency-assets.sh" xkeen "$WORK/existing" >/dev/null 2>&1; then
 echo 'existing staging directory was overwritten' >&2; exit 1
fi
[[ -d "$WORK/existing" ]]
if sh "$ROOT/install/common/dependency-assets.sh" unknown "$WORK/unknown" >/dev/null 2>&1; then
 echo 'unsupported dependency was accepted' >&2; exit 1
fi
[[ ! -e "$WORK/unknown" ]]
printf 'Pinned dependency stage refuses corrupted downloads, overwrite and unknown packages.\n'
