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
# Simulate SIGKILL/power loss after a version store and the first XKeen
# symlink were published. Recovery must remove only owned writes.
partial="$WORK/recovery"
mkdir -p "$partial/sbin" "$partial/var/lib"
pbase="$partial/var/lib/kee-route-manager-deps"
mkdir -m 0700 "$pbase" "$pbase/.pending-xkeen" "$pbase/xkeen-2.1"
printf '%s\n' "xkeen:2.1:$pbase/.pending-xkeen" > "$pbase/.pending-xkeen/token"
cp "$pbase/.pending-xkeen/token" "$pbase/xkeen-2.1/.krm-provision-token"
ln -s "$pbase/xkeen-2.1/_xkeen" "$partial/sbin/_xkeen"
KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null
[[ ! -e "$pbase/xkeen-2.1" && ! -L "$partial/sbin/_xkeen" && ! -e "$pbase/.pending-xkeen" ]]
# Foreign replacement after interruption must be refused and preserved,
# including a dangling/foreign link at the published binary path.
mkdir -m 0700 "$pbase/.pending-xkeen" "$pbase/xkeen-2.1"
printf '%s\n' "xkeen:2.1:$pbase/.pending-xkeen" > "$pbase/.pending-xkeen/token"
cp "$pbase/.pending-xkeen/token" "$pbase/xkeen-2.1/.krm-provision-token"
ln -s /unrelated-operator-target "$partial/sbin/xkeen"
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'foreign link was modified during recovery' >&2; exit 1
fi
[[ "$(readlink "$partial/sbin/xkeen")" = /unrelated-operator-target ]]
[[ -d "$pbase/.pending-xkeen" && -d "$pbase/xkeen-2.1" ]]
rm "$partial/sbin/xkeen"
# Loss of the ownership token also requires manual inspection, never rm -rf.
rm "$pbase/xkeen-2.1/.krm-provision-token"
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'unmarked version directory was deleted' >&2; exit 1
fi
[[ -d "$pbase/xkeen-2.1" ]]
# A crash after both links are published is a completed transaction. Merely
# remove the stale journal, do not uninstall the functional payload.
cp "$pbase/.pending-xkeen/token" "$pbase/xkeen-2.1/.krm-provision-token"
printf '#!/bin/sh\nexit 0\n' > "$pbase/xkeen-2.1/xkeen"
chmod 0700 "$pbase/xkeen-2.1/xkeen"
mkdir -m 0700 "$pbase/xkeen-2.1/_xkeen"
( cd "$pbase/xkeen-2.1" && openssl dgst -sha256 xkeen ) > "$pbase/xkeen-2.1/provenance.sha256"
ln -s "$pbase/xkeen-2.1/xkeen" "$partial/sbin/xkeen"
ln -s "$pbase/xkeen-2.1/_xkeen" "$partial/sbin/_xkeen"
KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null
[[ ! -e "$pbase/.pending-xkeen" && -d "$pbase/xkeen-2.1" ]]
[[ -L "$partial/sbin/xkeen" && -L "$partial/sbin/_xkeen" ]]
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'recovery of non-pending transaction should be refused' >&2; exit 1
fi
# A previously valid payload later changed by an operator must not be
# silently accepted or removed simply because the old links still match.
mkdir -m 0700 "$pbase/.pending-xkeen"
printf '%s\n' "xkeen:2.1:$pbase/.pending-xkeen" > "$pbase/.pending-xkeen/token"
printf '# modified\n' >> "$pbase/xkeen-2.1/xkeen"
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'tampered dependency was finalized or removed' >&2; exit 1
fi
[[ -d "$pbase/.pending-xkeen" && -f "$pbase/xkeen-2.1/xkeen" ]]
[[ -L "$partial/sbin/xkeen" && -L "$partial/sbin/_xkeen" ]]

printf '%s\n' 'Isolated provisioning and interruption recovery verified.'
