#!/bin/sh
# Explicitly approved, no-overwrite provisioning of pinned native payloads.
# --recover inspects an interrupted transaction and only touches owned paths.
# Neither mode configures or activates XKeen, Xray, or router policies.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 2 ] || fail 'usage: dependency-provision.sh xray|xkeen --approve|--recover'
kind=$1
action=$2
case "$kind" in xray) version=v26.10.21;; xkeen) version=2.1;; *) fail 'unsupported dependency';; esac
case "$action" in --approve|--recover);; *) fail 'explicit --approve or --recover required';; esac
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
# This override is reserved for isolated, unprivileged filesystem tests.
prefix=${KRM_DEPENDENCY_PREFIX:-/opt}
case "$prefix" in /*) ;; *) fail 'prefix must be absolute';; esac
if [ "$prefix" = /opt ]; then
 [ "$(id -u)" = 0 ] || fail 'run as root on the router'
 [ -d /opt ] && [ -x /opt/bin/opkg ] || fail 'mounted Entware with opkg is required'
fi
[ -d "$prefix/sbin" ] && [ ! -L "$prefix/sbin" ] || fail 'missing or linked Entware sbin directory'
[ -d "$prefix/var/lib" ] && [ ! -L "$prefix/var/lib" ] || fail 'missing or linked Entware var/lib directory'
base="$prefix/var/lib/kee-route-manager-deps"
if [ ! -d "$base" ]; then
 [ ! -e "$base" ] && [ ! -L "$base" ] || fail 'dependency storage path is occupied'
 [ "$action" = --approve ] || fail 'no dependency transaction storage to recover'
 mkdir -m 0700 "$base" || fail 'cannot create private dependency storage'
fi
[ ! -L "$base" ] || fail 'dependency storage is a symlink'
[ "$(stat -c %a "$base")" = 700 ] || fail 'dependency storage permissions must be 0700'
store="$base/$kind-$version"
journal="$base/.pending-$kind"
path="$prefix/sbin/$kind"
installed_target="$store/$kind"
module_path="$prefix/sbin/_xkeen"
module_target="$store/_xkeen"

# A journal precedes version-store creation and link publication. The token is
# copied into the version store, allowing recovery to distinguish our own
# directory from an unrelated directory created by another actor.
owned_store(){
 [ -d "$store" ] && [ ! -L "$store" ] &&
 [ -f "$journal/token" ] && [ ! -L "$journal/token" ] &&
 [ -f "$store/.krm-provision-token" ] && [ ! -L "$store/.krm-provision-token" ] &&
 cmp -s "$journal/token" "$store/.krm-provision-token"
}
owned_link(){
 [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ]
}
# Refuse drift before removing anything: never delete a foreign path or
# an occupied version store, even during an explicitly requested recovery.
verify_recovery(){
 [ -d "$journal" ] && [ ! -L "$journal" ] || fail 'invalid pending transaction journal'
 [ -f "$journal/token" ] && [ ! -L "$journal/token" ] || fail 'pending journal has no ownership token; inspect manually'
 if [ -e "$store" ] || [ -L "$store" ]; then
  owned_store || fail 'unowned or ambiguous dependency version store; manual recovery required'
 fi
 if [ -e "$path" ] || [ -L "$path" ]; then
  owned_link "$path" "$installed_target" || fail 'dependency entrypoint changed; manual recovery required'
 fi
 if [ "$kind" = xkeen ] && { [ -e "$module_path" ] || [ -L "$module_path" ]; }; then
  owned_link "$module_path" "$module_target" || fail 'XKeen module link changed; manual recovery required'
 fi
}
recover_pending(){
 [ -e "$journal" ] || [ -L "$journal" ] || return 0
 verify_recovery
 # A power loss after both links were published may precede journal cleanup.
 # Complete the existing transaction rather than uninstalling a working pair.
 if owned_link "$path" "$installed_target" &&
    { [ "$kind" != xkeen ] || owned_link "$module_path" "$module_target"; } &&
    owned_store && [ -f "$store/provenance.sha256" ]; then
  rm -rf -- "$journal"
  printf '%s\n' "Confirmed completed $kind provisioning; removed stale journal."
  return 0
 fi
 # Interrupted/partial provisioning: unlink only exact links recorded here.
 if owned_link "$path" "$installed_target"; then rm -- "$path"; fi
 if [ "$kind" = xkeen ] && owned_link "$module_path" "$module_target"; then rm -- "$module_path"; fi
 if owned_store; then rm -rf -- "$store"; fi
 rm -rf -- "$journal"
 printf '%s\n' "Rolled back interrupted $kind provisioning. Safe to retry with --approve."
}
if [ "$action" = --recover ]; then
 [ -e "$journal" ] || [ -L "$journal" ] || fail 'no interrupted transaction journal found'
 recover_pending
 exit 0
fi
[ ! -e "$journal" ] && [ ! -L "$journal" ] || fail "pending transaction exists: run --recover after checking the router"
[ ! -e "$path" ] && [ ! -L "$path" ] || fail "existing dependency path: $path"
if [ "$kind" = xkeen ]; then
 [ ! -e "$module_path" ] && [ ! -L "$module_path" ] || fail 'existing XKeen modules; refusing overwrite'
fi
[ ! -e "$store" ] && [ ! -L "$store" ] || fail 'dependency version already exists; inspect before retrying'
mkdir -m 0700 "$journal" || fail 'cannot reserve provisioning journal'
# The journal contains no subscription credentials, authentication data, or
# remote URLs. Private permissions and sync keep the intent durable first.
printf '%s\n' "$kind:$version:$journal" > "$journal/token"
sync || fail 'cannot persist dependency transaction intent'
committed=0
cleanup(){
 if [ "$committed" = 0 ]; then
  # An ambiguous interruption is deliberately retained for manual review.
  recover_pending >&2 || true
 else
  rm -rf -- "$journal"
 fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
sh "$ROOT/install/common/dependency-prepare.sh" "$kind" "$journal/prepared"
mkdir -m 0700 "$store" || fail 'cannot reserve immutable dependency directory'
cp "$journal/token" "$store/.krm-provision-token" || fail 'cannot mark owned dependency store'
chmod 0600 "$store/.krm-provision-token"
if [ "$kind" = xray ]; then
 mv "$journal/prepared/extracted/xray" "$store/xray"
 chmod 0700 "$store/xray"
else
 mv "$journal/prepared/extracted/xkeen" "$store/xkeen"
 mv "$journal/prepared/extracted/_xkeen" "$store/_xkeen"
 chmod 0700 "$store/xkeen"
fi
( cd "$store" && if [ "$kind" = xray ]; then openssl dgst -sha256 xray; else openssl dgst -sha256 xkeen; fi ) > "$store/provenance.sha256"
chmod 0600 "$store/provenance.sha256"
# Flush completed native payload before publishing any executable entrypoint.
sync || fail 'cannot persist prepared dependency payload'
if [ "$kind" = xkeen ]; then
 ln -s "$module_target" "$module_path" || fail 'cannot publish XKeen module link'
fi
ln -s "$installed_target" "$path" || fail "cannot publish $kind binary link"
sync || fail 'cannot persist dependency entrypoints'
committed=1
printf '%s\n' "Provisioned $kind $version at $path. No services, router interception, firewall, or configuration were activated."
