#!/bin/sh
# Release builder substitutes platform, version and pinned Ed25519 public key.
set -eu
umask 077
TAG=v@VERSION@
CHANNEL=@CHANNEL@
MANIFEST=manifest-$CHANNEL.json
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
ACTION=${KRM_INSTALL_ACTION:-install}
case "$ACTION" in install|resume|recover);; *) fail 'KRM_INSTALL_ACTION must be install, resume or recover';; esac
if [ "$ACTION" != install ]; then
 NEED_CONFIG=0
elif [ -n "${KRM_CONFIG_FILE:-}" ]; then
 [ -f "$KRM_CONFIG_FILE" ] || fail 'prepare a private controller/UI config first; set KRM_CONFIG_FILE=/absolute/path/config.yaml; see docs/AGENT_INSTALL.md'
 NEED_CONFIG=0
else
 NEED_CONFIG=1
fi
@ARCH_DETECTION@
ARCH=$(krm_detect_arch) || fail "unsupported native CPU/ABI"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/krm-bootstrap.XXXXXX")
cd "$WORK"
WORK=$(pwd -P)
cat > trusted-public.pem <<'KEY'
@PUBLIC_PEM@
KEY
fetch(){ curl --proto '=https' --proto-redir '=https' -fsSL --retry 3 --connect-timeout 20 --max-time 600 --max-filesize 134217728 -o "$2" "$BASE/$1" || fail "download failed: $1; check trusted HTTPS connectivity and staging free space"; }
verify_signature(){
 openssl base64 -d -A -in "$2" -out signature.bin
 openssl pkeyutl -verify -pubin -inkey trusted-public.pem -rawin -in "$1" -sigfile signature.bin >/dev/null 2>&1 || fail "invalid signature for $1 (or unsupported OpenSSL)"
}
fetch "$MANIFEST" "$MANIFEST"
fetch "$MANIFEST.sig" "$MANIFEST.sig"
verify_signature "$MANIFEST" "$MANIFEST.sig"
fetch SHA256SUMS SHA256SUMS
fetch SHA256SUMS.sig SHA256SUMS.sig
verify_signature SHA256SUMS SHA256SUMS.sig
verify_file(){
 name=$1
 expected=$(awk -v n="$name" '$2 == n {print $1}' SHA256SUMS)
 [ "${#expected}" = 64 ] || fail "missing/duplicate checksum for $name"
 actual=$(openssl dgst -sha256 "$name" | sed 's/^.*= //')
 [ "$actual" = "$expected" ] || fail "checksum mismatch: $name"
 case "$name" in "$MANIFEST"|"$MANIFEST.sig") return;; esac
 expected_size=$(awk -v n="$name" -F '"' '$2 == "name" {selected=($4==n)} $2 == "size" && selected {v=$3;gsub(/[^0-9]/,"",v);print v}' "$MANIFEST")
 case "$expected_size" in ''|*[!0-9]*) fail "invalid/missing manifest size: $name";; esac
 [ "$(wc -c < "$name" | tr -d '[:space:]')" = "$expected_size" ] || fail "size mismatch: $name"
}
verify_file "$MANIFEST"
verify_file "$MANIFEST.sig"
manifest_size(){
 size=$(awk -v n="$1" -F '"' '$2 == "name" {selected=($4==n)} $2 == "size" && selected {v=$3;gsub(/[^0-9]/,"",v);print v}' "$MANIFEST")
 case "$size" in ''|*[!0-9]*) fail "invalid/missing manifest size: $1";; esac
 [ "$size" -gt 0 ] && [ "$size" -le 209715200 ] || fail "unsupported manifest size: $1"
 printf '%s\n' "$size"
}
require_staging_space(){
 available=$(df -Pk "$WORK" | awk 'END {print $4}')
 case "$available" in ''|*[!0-9]*) fail 'cannot determine staging free space';; esac
 [ "$available" -ge "$((($1 + 1023) / 1024))" ] || fail 'not enough staging free space; select TMPDIR on mounted Entware storage and retry'
}
# Parse only the exact top-level fields emitted by the signed release builder.
version=$(awk -F '"' '/^  "version": / {print $4}' "$MANIFEST")
channel=$(awk -F '"' '/^  "channel": / {print $4}' "$MANIFEST")
schema=$(awk '/^  "schema_version": / {gsub(/,/,"",$2);print $2}' "$MANIFEST")
protocol=$(awk '/^  "update_protocol": / {gsub(/,/,"",$2);print $2}' "$MANIFEST")
[ "$version" = "@VERSION@" ] && [ "$channel" = "$CHANNEL" ] && [ "$schema" = 1 ] && [ "$protocol" = 1 ] || fail 'signed manifest version/channel/schema/update protocol does not match pinned bootstrap'
MODE=${KRM_MODE:-local-ui}
case "$MODE" in core|local-ui|ui);; *) fail 'KRM_MODE must be core, local-ui or ui';; esac
if [ "$NEED_CONFIG" = 1 ] && [ "$MODE" = ui ]; then
 fail 'KRM_MODE=ui requires KRM_CONFIG_FILE pointing to a prepared UI config; init-config generates controller configuration only'
fi
# The signed manifest supplies sizes before any large download. Keep room for
# both downloaded binaries and their verified payload copies; never count RAM
# as if it were all available to a tmpfs staging directory.
components='kee-route-managerd kee-route-managerctl kee-route-manager-ui kee-route-manager-launcher'
[ "$MODE" != ui ] || components="kee-route-manager-ui kee-route-managerctl"
binary_bytes=0
for component in $components; do
 bytes=$(manifest_size "$component-linux-$ARCH") || exit 1
 binary_bytes=$((binary_bytes + bytes))
done
archive_bytes=$(manifest_size release-files.tar.gz) || exit 1
require_staging_space "$((archive_bytes + 2 * binary_bytes + 4194304))"
fetch release-files.tar.gz release-files.tar.gz
verify_file release-files.tar.gz
mkdir payload
# Even trusted publisher mistakes must not write outside the temporary payload directory.
tar -tzf release-files.tar.gz > archive.names || fail 'cannot list verified archive'
if awk 'BEGIN {bad=0} /^\// || /(^|\/)\.\.(\/|$)/ {bad=1} END {exit bad}' archive.names; then :; else fail 'unsafe archive paths'; fi
# Symlinks/hardlinks and special files can escape extraction despite safe names.
tar -tvzf release-files.tar.gz > archive.entries || fail 'cannot list verified archive entries'
if awk 'BEGIN {bad=0} !/^[d-]/ {bad=1} END {exit bad}' archive.entries; then :; else fail 'unsafe archive entry types'; fi
extracted_bytes=$(awk '/^-/ {if ($3 !~ /^[0-9]+$/) exit 1; total+=$3} END {printf "%.0f\n",total}' archive.entries) || fail 'cannot determine payload extraction size'
[ "$extracted_bytes" -le 209715200 ] || fail 'verified install payload expands beyond the supported limit'
require_staging_space "$((extracted_bytes + 2 * binary_bytes + 4194304))"
tar -xzf release-files.tar.gz -C payload
mkdir -p payload/dist
cp "$MANIFEST" "$MANIFEST.sig" SHA256SUMS SHA256SUMS.sig payload/dist/
# The launcher verifies and installs a complete daemon/UI/ctl slot, including
# the dormant UI binary in core-only mode. The launcher itself remains stable.
for component in $components; do
 name=$component-linux-$ARCH
 fetch "$name" "$name"
 verify_file "$name"
 cp "$name" "payload/dist/$name"
 chmod 0755 "payload/dist/$name"
done
# No downloaded program has been executed before signature and digest checks.
# Ignore inherited internal staging state; only this verified wizard creates it.
unset KRM_GENERATED_TLS_DIR KRM_DEPENDENCY_PREFIX
if [ "$NEED_CONFIG" = 1 ]; then
 if [ ! -t 0 ]; then
  cat >&2 <<'EOF'
ERROR: No configuration was provided and interactive terminal is unavailable.

Provide KRM_CONFIG_FILE=/absolute/path/config.yaml
or run the installer from an interactive SSH shell.
EOF
  exit 1
 fi
 CTL="payload/dist/kee-route-managerctl-linux-$ARCH"
 GENERATED_CONFIG="$WORK/generated-config.yaml"
 sh payload/install/common/installer-dependencies.sh plan "$WORK"
 set -- --platform "$PLATFORM" --output "$GENERATED_CONFIG"
 if [ -f "$WORK/native-plan" ]; then
  case "$(cat "$WORK/native-plan")" in clean|resume) set -- "$@" --native-output "$WORK/native";; esac
 fi
 if [ "$MODE" = local-ui ]; then
  [ -z "${KRM_UI_CONFIG_FILE:-}" ] && [ -z "${KRM_UPSTREAM_CA_FILE:-}" ] || fail 'configless local-ui generates its own UI config and local trust; use prepared controller/UI configs for custom settings'
  KRM_UI_CONFIG_FILE="$WORK/generated-ui.yaml"
  KRM_GENERATED_TLS_DIR="$WORK/controller-tls"
  "$CTL" init-config "$@" --ui-output "$KRM_UI_CONFIG_FILE" --tls-dir "$KRM_GENERATED_TLS_DIR" || fail 'configuration wizard was cancelled or failed'
  KRM_UPSTREAM_CA_FILE="$KRM_GENERATED_TLS_DIR/tls.crt"
  export KRM_UI_CONFIG_FILE KRM_GENERATED_TLS_DIR KRM_UPSTREAM_CA_FILE
 else
  "$CTL" init-config "$@" || fail 'configuration wizard was cancelled or failed'
 fi
 [ -f "$GENERATED_CONFIG" ] || fail 'configuration wizard did not create a configuration'
 "$CTL" validate --config "$GENERATED_CONFIG" >/dev/null || fail 'configuration wizard produced an invalid configuration'
 sh payload/install/common/installer-dependencies.sh apply "$WORK" "$GENERATED_CONFIG" "$CTL" "$MODE"
 KRM_CONFIG_FILE=$GENERATED_CONFIG
 export KRM_CONFIG_FILE
fi
KRM_MODE=$MODE KRM_CONFIG_FILE="${KRM_CONFIG_FILE:-}" sh "payload/install/$PLATFORM/install.sh"
