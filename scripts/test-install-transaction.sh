#!/usr/bin/env bash
# Filesystem-only installer transactions. No router services or host /opt writes.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/entware/var/lib" "$WORK/entware/etc/kee-route-manager" "$WORK/entware/bin"
printf 'synthetic private configuration\n' > "$WORK/config"
printf '#!/bin/sh\nexit 0\n' > "$WORK/binary"
chmod 0755 "$WORK/binary"
helper="$ROOT/install/common/install-transaction.sh"
export KRM_TXN_TEST_PREFIX="$WORK/entware"
run(){ sh -eu -c '. "$1"; shift; txn_lock "$KRM_TXN_TEST_PREFIX" core; "$@"' transaction "$helper" "$@"; }
run txn_begin
run txn_directory "$WORK/entware/etc/kee-route-manager" 0700
[[ "$(stat -c %a "$WORK/entware/etc/kee-route-manager")" = 700 ]]
run txn_file "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml" 0600
[[ "$(cat "$WORK/entware/etc/kee-route-manager/config.yaml")" = 'synthetic private configuration' ]]
[[ "$(stat -c %a "$WORK/entware/etc/kee-route-manager/config.yaml")" = 600 ]]
# A collision never redirects a write into an operator-owned directory.
mkdir "$WORK/entware/bin/operator"
printf 'sentinel\n' > "$WORK/entware/bin/operator/sentinel"
if run txn_file "$WORK/binary" "$WORK/entware/bin/operator" 0755 >/dev/null 2>&1; then
 echo 'directory destination was overwritten' >&2; exit 1
fi
[[ "$(cat "$WORK/entware/bin/operator/sentinel")" = sentinel ]]
[[ ! -e "$WORK/entware/bin/operator/payload" ]]
# Existing matching data was never created by this transaction, either.
printf 'foreign\n' > "$WORK/entware/bin/foreign"
if run txn_file "$WORK/binary" "$WORK/entware/bin/foreign" 0755 >/dev/null 2>&1; then
 echo 'existing path was overwritten' >&2; exit 1
fi
# Detect drift before deleting any recorded managed file.
printf 'edited by operator\n' >> "$WORK/entware/etc/kee-route-manager/config.yaml"
if run txn_recover >/dev/null 2>&1; then echo 'changed config was erased' >&2; exit 1; fi
[[ -f "$WORK/entware/etc/kee-route-manager/config.yaml" ]]
cp "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml"
run txn_recover
[[ ! -e "$WORK/entware/etc/kee-route-manager/config.yaml" ]]
[[ "$(stat -c %a "$WORK/entware/etc/kee-route-manager")" = 755 ]]
[[ "$(cat "$WORK/entware/bin/foreign")" = foreign ]]
[[ -d "$WORK/entware/bin/operator" ]]
# A symlink replacement must survive rollback, even when its content matches.
run txn_begin
run txn_file "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml" 0600
rm "$WORK/entware/etc/kee-route-manager/config.yaml"
ln -s "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml"
if run txn_recover >/dev/null 2>&1; then echo 'foreign symlink was removed' >&2; exit 1; fi
[[ -L "$WORK/entware/etc/kee-route-manager/config.yaml" ]]
rm "$WORK/entware/etc/kee-route-manager/config.yaml"
run txn_recover
# Activation is a durable boundary: never pretend deleting static files also
# restores controller runtime/routing. Resume verifies the retained receipt.
run txn_begin
run txn_file "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml" 0600
run txn_file "$WORK/binary" "$WORK/entware/bin/kee-route-managerctl" 0755
run txn_launcher_binary "$WORK/entware/bin/kee-route-managerctl"
run txn_phase activating
if run txn_recover >/dev/null 2>&1; then echo 'active transaction was rolled back' >&2; exit 1; fi
run txn_verify_resume
printf 'changed\n' >> "$WORK/entware/etc/kee-route-manager/config.yaml"
if run txn_verify_resume >/dev/null 2>&1; then echo 'resume accepted changed config' >&2; exit 1; fi
cp "$WORK/config" "$WORK/entware/etc/kee-route-manager/config.yaml"
# Launcher may atomically replace a seed executable with its identical signed
# slot symlink. Only that explicitly marked transition is accepted on resume.
mkdir "$WORK/slot"
cp "$WORK/binary" "$WORK/slot/kee-route-managerctl"
chmod 0700 "$WORK/slot/kee-route-managerctl"
rm "$WORK/entware/bin/kee-route-managerctl"
ln -s "$WORK/slot/kee-route-managerctl" "$WORK/entware/bin/kee-route-managerctl"
run txn_verify_resume
run txn_capture_links
run txn_phase activating
run txn_verify_resume
run txn_commit
[[ ! -e "$WORK/entware/var/lib/kee-route-manager-install/pending-core" ]]
# A late same-byte foreign file is not owned merely because the persisted
# intent expected those bytes. Hard-link identity must still match.
mkdir "$WORK/race-bin"
cat > "$WORK/race-bin/ln" <<'RACE'
#!/bin/sh
if [ "${TXN_TEST_RACE:-}" = 1 ]; then
 cp "$2" "$3"
fi
exec /bin/ln "$@"
RACE
chmod 0755 "$WORK/race-bin/ln"
run txn_begin
if PATH="$WORK/race-bin:$PATH" TXN_TEST_RACE=1 run txn_file "$WORK/config" "$WORK/entware/bin/raced" 0600 >/dev/null 2>&1; then
 echo 'late foreign replacement was overwritten' >&2; exit 1
fi
if run txn_recover >/dev/null 2>&1; then echo 'late same-byte foreign file was deleted' >&2; exit 1; fi
[[ -f "$WORK/entware/bin/raced" ]]
rm "$WORK/entware/bin/raced"
run txn_recover
# Independent invocations share a kernel lock, including recover/resume.
sh -eu -c '. "$1"; txn_lock "$KRM_TXN_TEST_PREFIX" core; printf ready > "$2"; sleep 3' transaction "$helper" "$WORK/locked" &
locker=$!
for attempt in {1..50}; do : "$attempt"; [[ -f "$WORK/locked" ]] && break; sleep .05; done
[[ -f "$WORK/locked" ]]
if run txn_begin >/dev/null 2>&1; then echo 'parallel installer acquired active lock' >&2; exit 1; fi
wait "$locker"
printf '%s\n' 'Installer transaction rollback, drift protection, activation boundary and locking verified.'
