#!/usr/bin/env bash
# Offline wizard integration contracts; no host /opt, router, or downloads.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/source/install/common" "$WORK/prefix/sbin" "$WORK/prefix/bin" "$WORK/prefix/etc" "$WORK/stage" "$WORK/fake"
cp "$ROOT/install/common/installer-dependencies.sh" "$WORK/source/install/common/"
helper="$WORK/source/install/common/installer-dependencies.sh"
export KRM_DEPENDENCY_PREFIX="$WORK/prefix"
printf '#!/bin/sh\nexit 0\n' > "$WORK/prefix/bin/opkg"
chmod 0755 "$WORK/prefix/bin/opkg"
for name in jq timeout flock curl openssl tar unzip ip iptables iptables-save iptables-restore ipset conntrack nohup pidof killall insmod logger sysctl; do
 printf '#!/bin/sh\nexit 0\n' > "$WORK/fake/$name"; chmod 0755 "$WORK/fake/$name"
done
printf '#!/bin/sh\nexit 1\n' > "$WORK/fake/pidof"
export PATH="$WORK/fake:$PATH"
# Declining does not provision or mark a clean native request.
printf 'n\n' | sh "$helper" plan "$WORK/stage" >/dev/null
[[ ! -e "$WORK/stage/native-plan" && ! -e "$WORK/prefix/sbin/xray" ]]
# Missing executable must never resurrect a pre-existing native owner.
mkdir -p "$WORK/prefix/etc/init.d"
printf 'operator-service\n' > "$WORK/prefix/etc/init.d/S24xray"
if printf 'y\n' | sh "$helper" plan "$WORK/stage" >/dev/null 2>&1; then echo 'existing native owner accepted before provisioning' >&2; exit 1; fi
[[ ! -e "$WORK/stage/native-plan" && ! -e "$WORK/prefix/sbin/xray" ]]
rm "$WORK/prefix/etc/init.d/S24xray"
printf 'y\n' | sh "$helper" plan "$WORK/stage" >/dev/null
[[ "$(cat "$WORK/stage/native-plan")" = clean ]]
[[ -f "$WORK/stage/need-xray" && -f "$WORK/stage/need-xkeen" ]]
cat > "$WORK/ctl" <<'CTL'
#!/bin/sh
printf '%s\n' "$*" >> "$PLAN_TEST_CALLS"
if [ "$1" = preflight ] && [ "${PLAN_TEST_RESOURCES_FAIL:-}" = 1 ]; then exit 1; fi
if [ "$1" = native-policy-check ] && [ "${PLAN_TEST_POLICY_FAIL:-}" = 1 ]; then exit 1; fi
CTL
chmod 0755 "$WORK/ctl"
cat > "$WORK/source/install/common/dependency-provision.sh" <<'PROVISION'
#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$PLAN_TEST_PROVISIONS"
PROVISION
cat > "$WORK/source/install/common/native-bootstrap.sh" <<'NATIVE'
#!/bin/sh
set -eu
if [ "$2" = --verify-existing ]; then
 printf 'verify\n' >> "$PLAN_TEST_VERIFICATIONS"
 [ "${PLAN_TEST_VERIFY_DENY:-}" != 1 ] || exit 14
 exit 0
fi
[ "$2" = --approve ]
mkdir -p "$KRM_DEPENDENCY_PREFIX/etc/xkeen"
printf native >> "$PLAN_TEST_PROVISIONS"
NATIVE
export PLAN_TEST_CALLS="$WORK/calls" PLAN_TEST_PROVISIONS="$WORK/provisions" PLAN_TEST_VERIFICATIONS="$WORK/verifications"
mkdir -p "$WORK/stage/native"
printf '{}\n' > "$WORK/stage/native/xkeen.json"
if PLAN_TEST_RESOURCES_FAIL=1 sh "$helper" apply "$WORK/stage" "$WORK/core.yaml" "$WORK/ctl" core >/dev/null 2>&1; then echo 'resource failure accepted' >&2; exit 1; fi
[[ ! -e "$WORK/provisions" ]]
if printf 'y\n' | PLAN_TEST_POLICY_FAIL=1 sh "$helper" apply "$WORK/stage" "$WORK/core.yaml" "$WORK/ctl" core >/dev/null 2>&1; then echo 'missing policy activated' >&2; exit 1; fi
[[ "$(cat "$WORK/provisions")" = $'xray\nxkeen\nnative' ]]
rm "$WORK/provisions"
rm -rf "$WORK/prefix/etc/xkeen"
if printf 'n\n' | sh "$helper" apply "$WORK/stage" "$WORK/core.yaml" "$WORK/ctl" core >/dev/null 2>&1; then echo 'unapproved activation accepted' >&2; exit 1; fi
rm -rf "$WORK/prefix/etc/xkeen"
printf 'y\n' | sh "$helper" apply "$WORK/stage" "$WORK/core.yaml" "$WORK/ctl" core >/dev/null
[[ "$(grep -c 'preflight --resources-only' "$WORK/calls")" = 4 ]]
# Retrying after consent but before KRM starts must preserve native presets
# and reuse only the exact private consent receipt, without an overwrite.
mkdir -p "$WORK/prefix/var/lib/kee-route-manager-native/install-fixture/xray" "$WORK/retry"
ln -s "$WORK/prefix/var/lib/kee-route-manager-native/install-fixture/xray" "$WORK/prefix/etc/xray"
printf '#!/bin/sh\nexit 0\n' > "$WORK/prefix/sbin/xray"
printf '#!/bin/sh\nexit 0\n' > "$WORK/prefix/sbin/xkeen"
chmod 0755 "$WORK/prefix/sbin/"*
printf 'y\n' | sh "$helper" plan "$WORK/retry" >/dev/null
[[ "$(cat "$WORK/retry/native-plan")" = resume ]]
sh "$helper" apply "$WORK/retry" "$WORK/core.yaml" "$WORK/ctl" core </dev/null >/dev/null
# Resume ownership refusal precedes questions, resource probes, provisioning
# and reuse of the earlier activation approval.
mkdir "$WORK/denied-retry"
cp "$WORK/calls" "$WORK/calls-before-denial"
cp "$WORK/provisions" "$WORK/provisions-before-denial"
if PLAN_TEST_VERIFY_DENY=1 sh "$helper" plan "$WORK/denied-retry" </dev/null > "$WORK/denied-output" 2>&1; then echo 'native retry ownership failure ignored in planning' >&2; exit 1; fi
[[ ! -e "$WORK/denied-retry/native-plan" ]]
! grep -q 'Provision missing' "$WORK/denied-output"
if PLAN_TEST_VERIFY_DENY=1 sh "$helper" apply "$WORK/retry" "$WORK/core.yaml" "$WORK/ctl" core </dev/null >/dev/null 2>&1; then echo 'native retry ownership failure ignored during application' >&2; exit 1; fi
cmp -s "$WORK/calls" "$WORK/calls-before-denial"
cmp -s "$WORK/provisions" "$WORK/provisions-before-denial"
[[ "$(cat "$WORK/prefix/etc/xkeen/krm-activation-approved")" = approved ]]
printf 'foreign\n' > "$WORK/prefix/etc/xkeen/krm-activation-approved"
if sh "$helper" apply "$WORK/retry" "$WORK/core.yaml" "$WORK/ctl" core </dev/null >/dev/null 2>&1; then echo 'changed native consent accepted' >&2; exit 1; fi
# An occupied non-executable dependency never becomes an overwrite request.
mkdir -p "$WORK/other-stage"
rm "$WORK/prefix/sbin/xray"
printf operator > "$WORK/prefix/sbin/xray"
if printf 'y\n' | sh "$helper" plan "$WORK/other-stage" >/dev/null 2>&1; then echo 'occupied native path accepted' >&2; exit 1; fi
[[ "$(cat "$WORK/prefix/sbin/xray")" = operator ]]
printf '%s\n' 'Wizard dependency consent, resource gate, policy guard and activation consent passed.'
