#!/bin/sh
# Fetch pinned upstream dependency archives into a NEW private staging
# directory only. This helper never unpacks, executes or installs upstream code.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 2 ] || fail 'usage: dependency-assets.sh xray|xkeen /absolute/new/staging-dir'
kind=$1
dest=$2
case "$dest" in
 /*) ;;
 *) fail 'staging directory must be an absolute path';;
esac
[ ! -e "$dest" ] && [ ! -L "$dest" ] || fail 'staging directory already exists; refusing overwrite'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
. "$ROOT/install/common/architecture.sh"
case "$kind" in
 xkeen)
  tag=2.1
  name=xkeen.tar.gz
  bytes=129691
  digest=4b9350b11fab7fd3e4973db609db0fb780994f4b5ab0096f0549bc52acac8c73
  url=https://github.com/jameszeroX/XKeen/releases/download/$tag/$name
  ;;
 xray)
  arch=$(krm_detect_arch) || fail 'unsupported native CPU/ABI'
  tag=v26.10.21
  case "$arch" in
   mips)
    name=Xray-linux-mips32.zip
    bytes=23113049
    digest=3de6ee6a5142fb1372334ad1411f61f1b0a3f9d622a748a6080a29dc007ba207
    ;;
   mipsle)
    name=Xray-linux-mips32le.zip
    bytes=22971552
    digest=1e9953aecee4024d9da6427a42929817b53d1660f5122ec6b22629e11cab4012
    ;;
   arm64)
    name=Xray-linux-arm64-v8a.zip
    bytes=13587069
    digest=d1304946c486d98e6b886e9b931eaaaebb23835b30e217638037b7f2721d4e9c
    ;;
   armv7)
    name=Xray-linux-arm32-v7a.zip
    bytes=14316036
    digest=89978508fa2ebb4a1e661f2c6ed51d241c4091b3cd17f4c65fdf20baae7dedac
    ;;
   *) fail 'no router Xray artifact pin for this architecture';;
  esac
  url=https://github.com/XTLS/Xray-core/releases/download/$tag/$name
  ;;
 *) fail 'dependency must be xray or xkeen';;
esac
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v openssl >/dev/null 2>&1 || fail 'openssl is required'
# mkdir is atomic: do not reuse a possibly attacker-controlled destination.
mkdir -m 0700 "$dest" || fail 'cannot create private staging directory'
complete=0
cleanup(){ [ "$complete" = 1 ] || rm -rf -- "$dest"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
curl --proto '=https' --proto-redir '=https' -fsSL --retry 2 --connect-timeout 15 --max-time 600 --max-filesize 33554432 -o "$dest/$name" "$url" || fail 'pinned dependency download failed'
actual_bytes=$(wc -c < "$dest/$name" | tr -d '[:space:]')
[ "$actual_bytes" = "$bytes" ] || fail 'dependency asset size mismatch'
actual_digest=$(openssl dgst -sha256 "$dest/$name" | sed 's/^.*= //')
[ "$actual_digest" = "$digest" ] || fail 'dependency asset SHA-256 mismatch'
# A verified archive is not an installation. Native ELF, configuration and
# router interception checks are required before any later activation.
printf '%s\n' "Verified $kind $tag: $dest/$name"
complete=1
