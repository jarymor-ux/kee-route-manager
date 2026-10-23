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
if [ -n "${PROVISION_TEST_PAUSE:-}" ]; then
 : > "$PROVISION_TEST_PAUSE/started"
 while [ ! -f "$PROVISION_TEST_PAUSE/continue" ]; do sleep 0.05; done
fi
# Simulate a concurrent actor changing destination names after preflight.
if [ "${PROVISION_TEST_CONFLICT_XKEEN:-}" = 1 ]; then
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/sbin/xkeen"
fi
if [ "${PROVISION_TEST_CONFLICT_MODULE:-}" = 1 ]; then
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/sbin/.xkeen"
fi
if [ "${PROVISION_TEST_CONFLICT_LEGACY:-}" = 1 ]; then
 mkdir -m 0700 "$KRM_DEPENDENCY_PREFIX/sbin/_xkeen"
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/sbin/_xkeen/import.sh"
fi
if [ -n "${PROVISION_TEST_DIRECTORY:-}" ]; then
 destination="$KRM_DEPENDENCY_PREFIX/sbin/$PROVISION_TEST_DIRECTORY"
 mkdir "$destination"
 printf 'operator-owned\n' > "$destination/sentinel"
fi
if [ -n "${PROVISION_TEST_DIRECTORY_LINK:-}" ]; then
 ln -s "$PROVISION_TEST_DIRECTORY_LINK_TARGET" "$KRM_DEPENDENCY_PREFIX/sbin/$PROVISION_TEST_DIRECTORY_LINK"
fi
if [ "${PROVISION_TEST_CONFLICT_STORE:-}" = 1 ]; then
 mkdir -m 0700 "$KRM_DEPENDENCY_PREFIX/var/lib/kee-route-manager-deps/xray-v26.10.21"
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/var/lib/kee-route-manager-deps/xray-v26.10.21/sentinel"
fi
PREPARE
printf '#!/bin/sh\nexit 0\n' > "$WORK/fixture/xray"
printf '#!/bin/sh\nexit 0\n' > "$WORK/fixture/xkeen"
printf 'module\n' > "$WORK/fixture/_xkeen/readme"
printf '# module loader\n' > "$WORK/fixture/_xkeen/import.sh"
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
[[ -L "$WORK/entware/sbin/xkeen" && -L "$WORK/entware/sbin/.xkeen" ]]
[[ -f "$WORK/entware/sbin/.xkeen/readme" ]]
# The source archive directory is named _xkeen, but XKeen loads .xkeen
# at runtime. Never publish the legacy staging name.
[[ ! -e "$WORK/entware/sbin/_xkeen" && ! -L "$WORK/entware/sbin/_xkeen" ]]
if sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'second provisioning overwrote XKeen' >&2; exit 1
fi
# Do not let upstream's _xkeen -> .xkeen migration erase an operator tree.
mkdir -p "$WORK/existing-dot/sbin/.xkeen" "$WORK/existing-dot/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/existing-dot" sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'operator .xkeen modules were not protected' >&2; exit 1
fi
[[ -d "$WORK/existing-dot/sbin/.xkeen" && ! -L "$WORK/existing-dot/sbin/.xkeen" ]]
mkdir -p "$WORK/existing-legacy/sbin/_xkeen" "$WORK/existing-legacy/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/existing-legacy" sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'operator _xkeen staging directory was not protected' >&2; exit 1
fi
[[ -d "$WORK/existing-legacy/sbin/_xkeen" ]]
rm "$WORK/entware/sbin/xkeen" "$WORK/entware/sbin/.xkeen"
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
[[ ! -e "$WORK/conflict/sbin/.xkeen" && ! -L "$WORK/conflict/sbin/.xkeen" ]]
[[ ! -e "$WORK/conflict/var/lib/kee-route-manager-deps/xkeen-2.1" ]]
# An operator-owned runtime module path arriving after preflight must survive
# the failed no-overwrite symlink publication.
mkdir -p "$WORK/module-conflict/sbin" "$WORK/module-conflict/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/module-conflict" PROVISION_TEST_CONFLICT_MODULE=1 sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'XKeen module-path collision was not detected' >&2; exit 1
fi
[[ "$(cat "$WORK/module-conflict/sbin/.xkeen")" = operator-owned ]]
[[ ! -e "$WORK/module-conflict/sbin/xkeen" ]]
[[ ! -e "$WORK/module-conflict/var/lib/kee-route-manager-deps/xkeen-2.1" ]]
# A competing XKeen installation can populate the legacy module directory
# during archive preparation, after the initial no-overwrite preflight. It must
# not be activated next to our new .xkeen runtime path.
mkdir -p "$WORK/legacy-race/sbin" "$WORK/legacy-race/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/legacy-race" PROVISION_TEST_CONFLICT_LEGACY=1 sh "$helper" xkeen --approve >/dev/null 2>&1; then
 echo 'late XKeen legacy module collision was accepted' >&2; exit 1
fi
[[ "$(cat "$WORK/legacy-race/sbin/_xkeen/import.sh")" = operator-owned ]]
[[ ! -e "$WORK/legacy-race/sbin/.xkeen" && ! -L "$WORK/legacy-race/sbin/.xkeen" ]]
[[ ! -e "$WORK/legacy-race/sbin/xkeen" && ! -L "$WORK/legacy-race/sbin/xkeen" ]]
[[ ! -e "$WORK/legacy-race/var/lib/kee-route-manager-deps/xkeen-2.1" ]]
[[ ! -e "$WORK/legacy-race/var/lib/kee-route-manager-deps/.pending-xkeen" ]]
# A concurrent reservation of the private version store is never cleaned up
# as if it were owned by this attempted transaction.
mkdir -p "$WORK/store-conflict/sbin" "$WORK/store-conflict/var/lib"
if KRM_DEPENDENCY_PREFIX="$WORK/store-conflict" PROVISION_TEST_CONFLICT_STORE=1 sh "$helper" xray --approve >/dev/null 2>&1; then
 echo 'version-store collision was not detected' >&2; exit 1
fi
[[ "$(cat "$WORK/store-conflict/var/lib/kee-route-manager-deps/xray-v26.10.21/sentinel")" = operator-owned ]]
[[ ! -e "$WORK/store-conflict/sbin/xray" ]]
# mkdir/symlink-to-directory races must not turn ln's destination into a
# directory operand and write a nested link into an operator-owned directory.
for destination in xray .xkeen xkeen; do
 kind=xkeen
 [[ "$destination" != xray ]] || kind=xray
 conflict="$WORK/directory-$destination"
 mkdir -p "$conflict/sbin" "$conflict/var/lib"
 if KRM_DEPENDENCY_PREFIX="$conflict" PROVISION_TEST_DIRECTORY="$destination" sh "$helper" "$kind" --approve >/dev/null 2>&1; then
  echo "directory collision accepted: $destination" >&2; exit 1
 fi
 [[ "$(cat "$conflict/sbin/$destination/sentinel")" = operator-owned ]]
 [[ "$(find "$conflict/sbin/$destination" -mindepth 1 -maxdepth 1 | wc -l)" -eq 1 ]]
 conflict="$WORK/directory-link-$destination"
 mkdir -p "$conflict/sbin" "$conflict/var/lib" "$WORK/linked-operator-$destination"
 printf 'operator-owned\n' > "$WORK/linked-operator-$destination/sentinel"
 if KRM_DEPENDENCY_PREFIX="$conflict" PROVISION_TEST_DIRECTORY_LINK_TARGET="$WORK/linked-operator-$destination" PROVISION_TEST_DIRECTORY_LINK="$destination" sh "$helper" "$kind" --approve >/dev/null 2>&1; then
  echo "directory symlink collision accepted: $destination" >&2; exit 1
 fi
 [[ "$(readlink "$conflict/sbin/$destination")" = "$WORK/linked-operator-$destination" ]]
 [[ "$(cat "$WORK/linked-operator-$destination/sentinel")" = operator-owned ]]
 [[ "$(find "$WORK/linked-operator-$destination" -mindepth 1 -maxdepth 1 | wc -l)" -eq 1 ]]
done
# Recovery and a second approval cannot enter an active transaction. Preserve
# its journal and ownership token while the first process is still preparing.
concurrent="$WORK/concurrent"
mkdir -p "$concurrent/sbin" "$concurrent/var/lib" "$WORK/pause"
KRM_DEPENDENCY_PREFIX="$concurrent" PROVISION_TEST_PAUSE="$WORK/pause" sh "$helper" xray --approve >"$WORK/active.log" 2>&1 &
active=$!
for ((attempt=0; attempt<100; attempt++)); do
 [[ ! -e "$WORK/pause/started" ]] || break
 sleep 0.05
done
[[ -e "$WORK/pause/started" ]]
token=$(cat "$concurrent/var/lib/kee-route-manager-deps/.pending-xray/token")
if KRM_DEPENDENCY_PREFIX="$concurrent" sh "$helper" xray --recover >/dev/null 2>&1; then
 echo 'recovery raced an active approval' >&2; exit 1
fi
if KRM_DEPENDENCY_PREFIX="$concurrent" sh "$helper" xray --approve >/dev/null 2>&1; then
 echo 'second approval raced an active approval' >&2; exit 1
fi
[[ "$(cat "$concurrent/var/lib/kee-route-manager-deps/.pending-xray/token")" = "$token" ]]
touch "$WORK/pause/continue"
wait "$active"
[[ -L "$concurrent/sbin/xray" ]]
# The ownership token is unique per attempt even with the same kind/path.
rm "$concurrent/sbin/xray"
rm -rf "$concurrent/var/lib/kee-route-manager-deps/xray-v26.10.21"
sh_token_path="$concurrent/var/lib/kee-route-manager-deps/xray-v26.10.21/.krm-provision-token"
KRM_DEPENDENCY_PREFIX="$concurrent" sh "$helper" xray --approve >/dev/null
[[ "$(cat "$sh_token_path")" != "$token" ]]
# Simulate SIGKILL/power loss after a version store and the first XKeen
# symlink were published. Recovery must remove only owned writes.
partial="$WORK/recovery"
mkdir -p "$partial/sbin" "$partial/var/lib"
pbase="$partial/var/lib/kee-route-manager-deps"
mkdir -m 0700 "$pbase" "$pbase/.pending-xkeen" "$pbase/xkeen-2.1"
printf '%s\n' "xkeen:2.1:$pbase/.pending-xkeen" > "$pbase/.pending-xkeen/token"
cp "$pbase/.pending-xkeen/token" "$pbase/xkeen-2.1/.krm-provision-token"
ln -s "$pbase/xkeen-2.1/_xkeen" "$partial/sbin/.xkeen"
KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null
[[ ! -e "$pbase/xkeen-2.1" && ! -L "$partial/sbin/.xkeen" && ! -e "$pbase/.pending-xkeen" ]]
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
LC_ALL=C tar -cf - -C "$pbase/xkeen-2.1" _xkeen | openssl dgst -sha256 >> "$pbase/xkeen-2.1/provenance.sha256"
ln -s "$pbase/xkeen-2.1/xkeen" "$partial/sbin/xkeen"
ln -s "$pbase/xkeen-2.1/_xkeen" "$partial/sbin/.xkeen"
cp "$pbase/xkeen-2.1/provenance.sha256" "$pbase/.pending-xkeen/payload.sha256"
KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null
[[ ! -e "$pbase/.pending-xkeen" && -d "$pbase/xkeen-2.1" ]]
[[ -L "$partial/sbin/xkeen" && -L "$partial/sbin/.xkeen" ]]
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'recovery of non-pending transaction should be refused' >&2; exit 1
fi
# A stale journal must never certify unexpected or modified XKeen module
# contents while the entrypoint itself is unchanged.
mkdir -m 0700 "$pbase/.pending-xkeen"
printf '%s\n' "xkeen:2.1:$pbase/.pending-xkeen" > "$pbase/.pending-xkeen/token"
cp "$pbase/xkeen-2.1/provenance.sha256" "$pbase/.pending-xkeen/payload.sha256"
printf 'unexpected module\n' > "$pbase/xkeen-2.1/_xkeen/foreign"
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'modified XKeen modules were accepted during recovery' >&2; exit 1
fi
[[ -f "$pbase/xkeen-2.1/_xkeen/foreign" && -d "$pbase/.pending-xkeen" ]]
rm "$pbase/xkeen-2.1/_xkeen/foreign"
# A previously valid payload later changed by an operator must not be
# silently accepted or removed simply because the old links still match.
printf '# modified\n' >> "$pbase/xkeen-2.1/xkeen"
if KRM_DEPENDENCY_PREFIX="$partial" sh "$helper" xkeen --recover >/dev/null 2>&1; then
 echo 'tampered dependency was finalized or removed' >&2; exit 1
fi
[[ -d "$pbase/.pending-xkeen" && -f "$pbase/xkeen-2.1/xkeen" ]]
[[ -L "$partial/sbin/xkeen" && -L "$partial/sbin/.xkeen" ]]

# A crash before provenance creation must not authorize deleting files
# changed after the crash. Payload identity lives in the journal first.
pre="$WORK/pre-provenance"
mkdir -p "$pre/sbin" "$pre/var/lib"
base="$pre/var/lib/kee-route-manager-deps"
mkdir -m 0700 "$base" "$base/.pending-xray" "$base/xray-v26.10.21"
printf 'attempt-token\n' > "$base/.pending-xray/token"
cp "$base/.pending-xray/token" "$base/xray-v26.10.21/.krm-provision-token"
cp "$WORK/fixture/xray" "$base/xray-v26.10.21/xray"
( cd "$base/xray-v26.10.21" && openssl dgst -sha256 xray ) > "$base/.pending-xray/payload.sha256"
printf '# operator modification\n' >> "$base/xray-v26.10.21/xray"
if KRM_DEPENDENCY_PREFIX="$pre" sh "$helper" xray --recover >/dev/null 2>&1; then
 echo 'pre-provenance operator edits were deleted' >&2; exit 1
fi
[[ -f "$base/xray-v26.10.21/xray" && -f "$base/.pending-xray/token" ]]
# Rewriting the colocated provenance to match edits cannot supersede the
# independently recorded pre-effect identity in the journal.
( cd "$base/xray-v26.10.21" && openssl dgst -sha256 xray ) > "$base/xray-v26.10.21/provenance.sha256"
if KRM_DEPENDENCY_PREFIX="$pre" sh "$helper" xray --recover >/dev/null 2>&1; then
 echo 'edited payload and rewritten provenance were accepted' >&2; exit 1
fi
rm "$base/xray-v26.10.21/provenance.sha256"
# Unknown root entries are preserved even when the recognized payload hashes
# still match (including a token-only partial store).
rm "$base/xray-v26.10.21/xray"
printf 'operator-owned\n' > "$base/xray-v26.10.21/operator-note"
if KRM_DEPENDENCY_PREFIX="$pre" sh "$helper" xray --recover >/dev/null 2>&1; then
 echo 'unknown root file was deleted during recovery' >&2; exit 1
fi
[[ "$(cat "$base/xray-v26.10.21/operator-note")" = operator-owned ]]
# The same interrupted state with only the verified original payload can be
# rolled back without requiring the post-copy provenance write to have run.
rm "$base/xray-v26.10.21/operator-note"
cp "$WORK/fixture/xray" "$base/xray-v26.10.21/xray"
KRM_DEPENDENCY_PREFIX="$pre" sh "$helper" xray --recover >/dev/null
[[ ! -e "$base/xray-v26.10.21" && ! -e "$base/.pending-xray" ]]
printf '%s\n' 'Isolated provisioning and interruption recovery verified.'
