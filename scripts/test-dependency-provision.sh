#!/usr/bin/env bash
# No router, network, root privileges, or service activation required.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/checkout/install/common" "$WORK/entware/sbin" "$WORK/entware/var/lib" "$WORK/fixture/_xkeen"
cp "$ROOT/install/common/dependency-provision.sh" "$WORK/checkout/install/common/"
cat > "$WORK/checkout/install/common/dependency-prepare.sh" <<'PREPARE'
#!/bin/sh
set -eu
[ "$#" = 2 ] && [ ! -e "$2" ]
[ "${PROVISION_TEST_FAIL:-}" != 1 ] || exit 12
mkdir -m 0700 "$2" "$2/extracted"
case "$1" in
 xray) cp "$PROVISION_TEST_FIXTURE/xray" "$2/extracted/xray";;
 xkeen) cp "$PROVISION_TEST_FIXTURE/xkeen" "$2/extracted/xkeen"; cp -R "$PROVISION_TEST_FIXTURE/_xkeen" "$2/extracted/_xkeen";;
 *) exit 1;;
esac
# Simulate a concurrent actor changing destination names after preflight.
if [ "${PROVISION_TEST_CONFLICT_XKEEN:-}" = 1 ]; then
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/sbin/xkeen"
fi
if [ "${PROVISION_TEST_CONFLICT_STORE:-}" = 1 ]; then
 mkdir -m 0700 "$KRM_DEPENDENCY_PREFIX/var/lib/kee-route-manager-deps/xray-v26.10.21"
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/var/lib/kee-route-manager-deps/xray-v26.10.21/sentinel"
fi
PREPARE
printf '#!/bin/sh\nexit 0\n' > "$WORK/fixture/xray"
printf '#!/bin/sh\nexit 0\n' > "$WORK/fixture/xkeen"
printf 'module\n' > "$WORK/fixture/_xkeen/readme"
chmod 0755 "$WORK/fixture/xray" "$WORK/fixture/xkeen"
helper="$WORK/checkout/install/common/dependency-provision.sh"
export KRM_DEPENDENCY_PREFIX="$WORK/entware" PROVISION_TEST_FIXTURE="$WORK/fixture"
if PROVISION_TEST_FAIL=1 sh "$helper" xray --approve >/dev/null 2>&1; then
 echo 'failed preparation was installed' >&2; exit 1
fi
[[ ! -e "$WORK/entware/sbin/xray" ]]
sh "$helper" xray --approve >/dev/null
[[ -L "$WORK/entware/sbin/xray" && -x "$WORK/entware/sbin/xray" ]]
[[ "$(readlink "$WORK/entware/sbin/xray")" = "$WORK/entware/var/lib/kee-route-manager-deps/xray-v26.10.21/xray" ]]
[[ -f "$WORK/entware/var/lib/kee-route-manager-deps/xray-v26.10.21/provenance.sha256" ]]
if sh "$helper" xray --approve >/dev/null 2>&1; then
 echo 'second provisioning overwrote Xray' >&2; exit 1
fi
sh "$helper" xkeen --approve >/dev/null
[[ -L "$WORK/entware/sbin/xkeen" && -L "$WORK/entware/sbin/_xkeen" ]]
[[ -f "$WORK/entware/sbin/_xkeen/readme" ]]
if sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'second provisioning overwrote XKeen' >&2; exit 1
fi
rm "$WORK/entware/sbin/xkeen" "$WORK/entware/sbin/_xkeen"
ln -s /nonexistent "$WORK/entware/sbin/xkeen"
if sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'dangling XKeen symlink was overwritten' >&2; exit 1
fi
[[ -L "$WORK/entware/sbin/xkeen" ]]
if sh "$helper" unknown --approve >/dev/null 2>&1; then
 echo 'unsupported dependency was accepted' >&2; exit 1
fi
if sh "$helper" xray >/dev/null 2>&1; then
 echo 'unapproved dependency provisioning was accepted' >&2; exit 1
fi
# A collision during publishing XKeen must undo only this transaction's link.
mkdir -p "$WORK/conflict/sbin" "$WORK/conflict/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/conflict" PROVISION_TEST_CONFLICT_XKEEN=1 sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'XKeen collision was not detected' >&2; exit 1
fi
[[ "$(cat "$WORK/conflict/sbin/xkeen")" = operator-owned ]]
[[ ! -e "$WORK/conflict/sbin/_xkeen" && ! -L "$WORK/conflict/sbin/_xkeen" ]]
[[ ! -e "$WORK/conflict/var/lib/kee-route-manager-deps/xkeen-2.1" ]]
# A concurrent reservation of the private version store is never cleaned up
# as if it were owned by this attempted transaction.
mkdir -p "$WORK/store-conflict/sbin" "$WORK/store-conflict/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/store-conflict" PROVISION_TEST_CONFLICT_STORE=1 sh "$helper" xray --approve >/dev/null 2>&1; then
 echo 'version-store collision was not detected' >&2; exit 1
fi
[[ "$(cat "$WORK/store-conflict/var/lib/kee-route-manager-deps/xray-v26.10.21/sentinel")" = operator-owned ]]
[[ ! -e "$WORK/store-conflict/sbin/xray" ]]
printf '%s\n' 'Isolated approved provisioning, pinned-store linking, refusal to overwrite, failed prepare, and no implicit service activation verified.'
