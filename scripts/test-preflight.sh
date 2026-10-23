#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
go test ./internal/preflight ./internal/control/cli
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/install/keenetic" "$WORK/install/common" "$WORK/dist"
cp install/keenetic/{install,preflight}.sh "$WORK/install/keenetic/"
cp install/common/architecture.sh "$WORK/install/common/"
case "$(uname -m)" in x86_64) arch=amd64;; aarch64) arch=arm64;; *) echo 'wrapper fixture requires native amd64/arm64 host' >&2; exit 1;; esac
cat > "$WORK/dist/kee-route-managerctl-linux-$arch" <<'CLI'
#!/bin/sh
printf '%s\n' "$@" > "$PREFLIGHT_TEST_LOG"
exit 42
CLI
cat > "$WORK/install/common/install.sh" <<'INSTALL'
#!/bin/sh
touch "$PREFLIGHT_TEST_WRITTEN"
INSTALL
chmod 0755 "$WORK/dist/kee-route-managerctl-linux-$arch"
export KRM_CONFIG_FILE="$WORK/private.yaml" KRM_MODE=core
export PREFLIGHT_TEST_LOG="$WORK/arguments" PREFLIGHT_TEST_WRITTEN="$WORK/installed"
status=0
sh "$WORK/install/keenetic/install.sh" || status=$?
[[ "$status" = 42 && ! -e "$PREFLIGHT_TEST_WRITTEN" ]]
[[ "$(head -1 "$PREFLIGHT_TEST_LOG")" = preflight ]]
grep -Fxq -- '--release-dir' "$PREFLIGHT_TEST_LOG"
grep -Fxq -- "$WORK" "$PREFLIGHT_TEST_LOG"
printf 'Installer refuses preflight failure before common installer effects.\n'
