#!/bin/sh
# Release builder substitutes platform, version and pinned Ed25519 public key.
set -eu
umask 077
TAG=v@VERSION@
PLATFORM=@PLATFORM@
BASE=https://github.com/jarymor-ux/kee-route-manager/releases/download/$TAG
WORK=
fail(){ echo "ERROR: $*" >&2; exit 1; }
cleanup(){ [ -z "$WORK" ] || rm -rf "$WORK"; }
trap cleanup EXIT HUP INT TERM
[ "$(id -u)" = 0 ] || fail 'run as root from an SSH shell'
command -v openssl >/dev/null 2>&1 || fail 'OpenSSL with Ed25519 pkeyutl support is required; install it from your trusted package manager'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
command -v curl >/dev/null 2>&1 || fail 'curl with trusted CA certificates is required'
[ -n "${KRM_CONFIG_FILE:-}" ] && [ -f "$KRM_CONFIG_FILE" ] || fail 'prepare a private controller/UI config first; set KRM_CONFIG_FILE=/absolute/path/config.yaml; see docs/AGENT_INSTALL.md'
case "$(uname -m)" in
 x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; armv7l|armv7*) ARCH=armv7;; mipsel|mipsle) ARCH=mipsle;; mips)
  endian=$(od -An -t u1 -j5 -N1 /bin/sh | tr -d '[:space:]')
  [ "$endian" = 1 ] || fail 'only little-endian MIPS is supported'
  ARCH=mipsle;; *) fail 'unsupported CPU architecture';;
esac
WORK=$(mktemp -d "${TMPDIR:-/tmp}/krm-bootstrap.XXXXXX")
cd "$WORK"
cat > trusted-public.pem <<'KEY'
@PUBLIC_PEM@
KEY
fetch(){ curl --proto '=https' --proto-redir '=https' -fsSL --retry 3 --connect-timeout 20 --max-time 600 --max-filesize 134217728 -o "$2" "$BASE/$1"; }
verify_signature(){
 openssl base64 -d -A -in "$2" -out signature.bin
 openssl pkeyutl -verify -pubin -inkey trusted-public.pem -rawin -in "$1" -sigfile signature.bin >/dev/null 2>&1 || fail "invalid signature for $1 (or unsupported OpenSSL)"
}
fetch manifest-rc.json manifest-rc.json
fetch manifest-rc.json.sig manifest-rc.json.sig
verify_signature manifest-rc.json manifest-rc.json.sig
fetch SHA256SUMS SHA256SUMS
fetch SHA256SUMS.sig SHA256SUMS.sig
verify_signature SHA256SUMS SHA256SUMS.sig
verify_file(){
 name=$1
 expected=$(awk -v n="$name" '$2 == n {print $1}' SHA256SUMS)
 [ "${#expected}" = 64 ] || fail "missing/duplicate checksum for $name"
 actual=$(openssl dgst -sha256 "$name" | sed 's/^.*= //')
 [ "$actual" = "$expected" ] || fail "checksum mismatch: $name"
 case "$name" in manifest-rc.json|manifest-rc.json.sig) return;; esac
 expected_size=$(awk -v n="$name" -F '"' '$2 == "name" {selected=($4==n)} $2 == "size" && selected {v=$3;gsub(/[^0-9]/,"",v);print v}' manifest-rc.json)
 case "$expected_size" in ''|*[!0-9]*) fail "invalid/missing manifest size: $name";; esac
 [ "$(wc -c < "$name" | tr -d '[:space:]')" = "$expected_size" ] || fail "size mismatch: $name"
}
verify_file manifest-rc.json
verify_file manifest-rc.json.sig
# Parse only the exact top-level fields emitted by the signed release builder.
version=$(awk -F '"' '/^  "version": / {print $4}' manifest-rc.json)
channel=$(awk -F '"' '/^  "channel": / {print $4}' manifest-rc.json)
schema=$(awk '/^  "schema_version": / {gsub(/,/,"",$2);print $2}' manifest-rc.json)
[ "$version" = "@VERSION@" ] && [ "$channel" = rc ] && [ "$schema" = 1 ] || fail 'signed manifest version/channel/schema does not match pinned bootstrap'
fetch release-files.tar.gz release-files.tar.gz
verify_file release-files.tar.gz
mkdir payload
# Even trusted publisher mistakes must not write outside the temporary payload directory.
if tar -tzf release-files.tar.gz | awk 'BEGIN {bad=0} /^\// || /(^|\/)\.\.(\/|$)/ {bad=1} END {exit bad}'; then :; else fail 'unsafe archive paths'; fi
tar -xzf release-files.tar.gz -C payload
mkdir -p payload/dist
MODE=${KRM_MODE:-local-ui}
for component in kee-route-managerd kee-route-managerctl kee-route-manager-ui; do
 case "$MODE:$component" in core:kee-route-manager-ui|ui:kee-route-managerd|ui:kee-route-managerctl) continue;; esac
 name=$component-linux-$ARCH
 fetch "$name" "$name"
 verify_file "$name"
 cp "$name" "payload/dist/$name"
 chmod 0755 "payload/dist/$name"
done
# No downloaded program has been executed before signature and digest checks.
KRM_MODE=$MODE sh "payload/install/$PLATFORM/install.sh"
