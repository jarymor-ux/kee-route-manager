#!/bin/sh
# Prepare pinned Xray/XKeen archives for a later, separately approved
# installation transaction. Never activate a component or router policy here.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 2 ] || fail 'usage: dependency-prepare.sh xray|xkeen /absolute/new/staging-dir'
kind=$1
dest=$2
case "$kind" in xray|xkeen);; *) fail 'unsupported dependency';; esac
case "$dest" in /*);; *) fail 'staging directory must be absolute';; esac
[ ! -e "$dest" ] && [ ! -L "$dest" ] || fail 'staging directory exists (refusing overwrite)'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
. "$ROOT/install/common/architecture.sh"
if [ "$kind" = xray ]; then
 arch=$(krm_detect_arch) || fail 'unsupported native CPU/ABI'
 case "$arch" in
  mips) name=Xray-linux-mips32.zip; expected='1:2:0:8';;
  mipsle) name=Xray-linux-mips32le.zip; expected='1:1:8:0';;
  armv7) name=Xray-linux-arm32-v7a.zip; expected='1:1:40:0';;
  arm64) name=Xray-linux-arm64-v8a.zip; expected='2:1:183:0';;
  *) fail 'no pinned Xray artifact for this CPU/ABI';;
 esac
 command -v unzip >/dev/null 2>&1 || fail 'unzip is required for offline preparation'
else
 name=xkeen.tar.gz
 command -v tar >/dev/null 2>&1 || fail 'tar is required for offline preparation'
fi
mkdir -m 0700 "$dest" || fail 'cannot create private staging directory'
complete=0
cleanup(){ [ "$complete" = 1 ] || rm -rf -- "$dest"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
# The downloader checks an immutable upstream release asset's exact digest and
# size before any data is unpacked. Never use mutable upstream install scripts.
sh "$ROOT/install/common/dependency-assets.sh" "$kind" "$dest/verified"
archive=$dest/verified/$name
[ -f "$archive" ] && [ ! -L "$archive" ] || fail 'verified archive missing'
mkdir -m 0700 "$dest/extracted"
if [ "$kind" = xray ]; then
 unzip -tqq "$archive" >/dev/null || fail 'invalid Xray ZIP archive'
 unzip -Z -1 "$archive" > "$dest/entries" || fail 'cannot list Xray archive'
 [ "$(grep -Fxc xray "$dest/entries" || true)" = 1 ] || fail 'Xray ZIP must contain one root xray executable'
 # Extract only the named executable: no archive-supplied paths, symlinks,
 # geodata, service scripts or policies are ever materialized.
 unzip -p "$archive" xray | head -c 67108865 > "$dest/extracted/xray"
 bytes=$(wc -c < "$dest/extracted/xray" | tr -d '[:space:]')
 [ "$bytes" -ge 1024 ] && [ "$bytes" -le 67108864 ] || fail 'Xray payload size out of bounds'
 header=$(od -An -v -t u1 -N 20 "$dest/extracted/xray") || fail 'cannot inspect Xray ELF'
 set -- $header
 [ "$#" = 20 ] && [ "$1 $2 $3 $4" = '127 69 76 70' ] || fail 'Xray payload is not ELF'
 elf_class=$5; elf_endian=$6
 shift 18
 [ "$elf_class:$elf_endian:$1:$2" = "$expected" ] || fail 'Xray ELF does not match native CPU/ABI'
 chmod 0700 "$dest/extracted/xray"
else
 tar -tzf "$archive" > "$dest/entries" || fail 'invalid XKeen archive'
 [ "$(wc -l < "$dest/entries" | tr -d '[:space:]')" -le 1024 ] || fail 'too many XKeen archive entries'
 while IFS= read -r entry; do
  case "$entry" in
   ./|.|xkeen|./xkeen|_xkeen|_xkeen/|./_xkeen|./_xkeen/|_xkeen/*|./_xkeen/*) ;;
   *) fail 'unrecognized or unsafe XKeen archive entry';;
  esac
  case "$entry" in *'/../'*|../*|*'/..'|*'/./'*|*'//'*) fail 'unsafe XKeen archive path';; esac
 done < "$dest/entries"
 tar -tvzf "$archive" > "$dest/entry-types" || fail 'cannot inspect XKeen archive types'
 LC_ALL=C awk 'substr($0,1,1)!="-" && substr($0,1,1)!="d" {bad=1} END {exit bad}' "$dest/entry-types" || fail 'XKeen archive contains links or special files'
 tar -xzf "$archive" -C "$dest/extracted" || fail 'cannot extract XKeen'
 [ -f "$dest/extracted/xkeen" ] && [ ! -L "$dest/extracted/xkeen" ] || fail 'XKeen entrypoint missing'
 [ -d "$dest/extracted/_xkeen" ] && [ ! -L "$dest/extracted/_xkeen" ] || fail 'XKeen modules missing'
 [ "$(du -sk "$dest/extracted" | awk '{print $1}')" -le 16384 ] || fail 'XKeen extraction exceeds 16 MiB'
fi
rm -f "$dest/entries" "$dest/entry-types"
complete=1
printf '%s\n' "Verified $kind prepared at $dest/extracted (NOT installed or activated)."
