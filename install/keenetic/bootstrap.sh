#!/bin/sh
set -eu
umask 077

REPOSITORY=${KRM_REPOSITORY:-jarymor-ux/kee-route-manager}
VERSION=${KRM_VERSION:-}
if [ -n "$VERSION" ]; then
  case "$VERSION" in
    v*) TAG=$VERSION ;;
    *) TAG=v$VERSION ;;
  esac
  INSTALL_REF=${KRM_INSTALL_REF:-$TAG}
  RELEASE_BASE=${KRM_RELEASE_BASE:-https://github.com/$REPOSITORY/releases/download/$TAG}
else
  INSTALL_REF=${KRM_INSTALL_REF:-main}
  RELEASE_BASE=${KRM_RELEASE_BASE:-https://github.com/$REPOSITORY/releases/latest/download}
fi
RAW_BASE=${KRM_RAW_BASE:-https://raw.githubusercontent.com/$REPOSITORY/$INSTALL_REF}
WORK_DIR=

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

cleanup() {
  [ -z "$WORK_DIR" ] || rm -rf "$WORK_DIR"
}
trap cleanup EXIT HUP INT TERM

download() {
  url=$1
  output=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 20 --max-time 600 -o "$output" "$url"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -O "$output" "$url"
    return
  fi
  fail "curl or wget is required"
}

sha256_file() {
  file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | sed 's/^.*= //'
    return
  fi
  fail "sha256sum or openssl is required"
}

[ "$(id -u)" = 0 ] || fail "run as root"
[ -d /opt ] || fail "Entware /opt is not mounted"
[ -r /dev/tty ] || fail "an interactive terminal is required; run the command from an SSH session"

case "$(uname -m)" in
  aarch64|arm64) ARCH=arm64 ;;
  armv7l|armv7*) ARCH=armv7 ;;
  mipsel|mipsle) ARCH=mipsle ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

TMP_BASE=/tmp
[ -d /opt/tmp ] && [ -w /opt/tmp ] && TMP_BASE=/opt/tmp
if command -v mktemp >/dev/null 2>&1; then
  WORK_DIR=$(mktemp -d "$TMP_BASE/krm-install.XXXXXX")
else
  WORK_DIR=$TMP_BASE/krm-install.$$
  mkdir "$WORK_DIR"
fi
mkdir -p "$WORK_DIR/dist" "$WORK_DIR/configs" "$WORK_DIR/install/keenetic"

BINARY_NAME=kee-route-manager-linux-$ARCH
BINARY=$WORK_DIR/dist/$BINARY_NAME
SUMS=$WORK_DIR/SHA256SUMS
INSTALLER=$WORK_DIR/install/keenetic/install.sh
INIT_SCRIPT=$WORK_DIR/install/keenetic/S99kee-route-manager
CONFIG_TEMPLATE=$WORK_DIR/configs/keenetic.yaml

echo "Downloading Kee Route Manager for $ARCH..."
download "$RELEASE_BASE/$BINARY_NAME" "$BINARY"
download "$RELEASE_BASE/SHA256SUMS" "$SUMS"

EXPECTED=$(awk -v name="$BINARY_NAME" '$2 == name { print $1; exit }' "$SUMS")
[ -n "$EXPECTED" ] || fail "$BINARY_NAME is missing from SHA256SUMS"
ACTUAL=$(sha256_file "$BINARY")
[ "$ACTUAL" = "$EXPECTED" ] || fail "binary SHA-256 mismatch"
chmod 0755 "$BINARY"
echo "Binary SHA-256 verified."

echo "Downloading the Keenetic installer..."
download "$RAW_BASE/install/keenetic/install.sh" "$INSTALLER"
download "$RAW_BASE/install/keenetic/S99kee-route-manager" "$INIT_SCRIPT"
download "$RAW_BASE/configs/keenetic.yaml" "$CONFIG_TEMPLATE"
chmod 0755 "$INSTALLER" "$INIT_SCRIPT"

# The main installer is interactive. Give it the controlling terminal even when
# this bootstrap script itself was started through `curl | sh` or `wget | sh`.
KRM_BINARY=$BINARY sh "$INSTALLER" "$@" </dev/tty
