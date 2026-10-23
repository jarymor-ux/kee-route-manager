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
command -v flock >/dev/null 2>&1 || fail 'flock is required for serialized provisioning and recovery'
base="$prefix/var/lib/kee-route-manager-deps"
if [ ! -d "$base" ]; then
 [ ! -e "$base" ] && [ ! -L "$base" ] || fail 'dependency storage path is occupied'
 [ "$action" = --approve ] || fail 'no dependency transaction storage to recover'
 mkdir -m 0700 "$base" || fail 'cannot create private dependency storage'
fi
[ ! -L "$base" ] || fail 'dependency storage is a symlink'
[ "$(stat -c %a "$base")" = 700 ] || fail 'dependency storage permissions must be 0700'
# Keep the lock inode after exit: unlinking it would allow another process
# to lock a different inode while a current transaction still holds this one.
lock="$base/.lock-$kind"
[ ! -L "$lock" ] && { [ ! -e "$lock" ] || [ -f "$lock" ]; } || fail 'invalid dependency lock path'
exec 9>"$lock"
flock -n 9 || fail 'dependency provisioning or recovery is already running'
store="$base/$kind-$version"
journal="$base/.pending-$kind"
path="$prefix/sbin/$kind"
installed_target="$store/$kind"
# XKeen imports /opt/sbin/.xkeen/import.sh at runtime. The verified
# release archive calls the source directory _xkeen, but publishing that
# source name would trigger upstream's on-start rename and can collide with
# an operator's existing .xkeen tree.
module_path="$prefix/sbin/.xkeen"
legacy_module_path="$prefix/sbin/_xkeen"
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
# Record payload identities in the journal BEFORE moving verified files into
# the version store. Recovery cannot trust provenance stored beside a payload
# that may have been edited after interruption.
identity_digest(){
 recorded=$(sed -n "$1"'s/^.*= //p' "$journal/payload.sha256")
 case "$recorded" in ''|*[!0123456789abcdef]*) return 1;; esac
 [ "${#recorded}" = 64 ]
}
tree_digest(){
 # Do not hide tar errors behind a successful openssl pipeline.
 LC_ALL=C tar -cf "$journal/.checking-tree.tar" -C "$1" _xkeen 2>/dev/null || return 1
 computed=$(openssl dgst -sha256 "$journal/.checking-tree.tar" | sed 's/^.*= //') || return 1
 rm -f "$journal/.checking-tree.tar"
}
valid_partial_store(){
 owned_store || return 1
 # A token authorizes only known files, never recursive deletion of later
 # operator additions. Inspect dotfiles as well as ordinary root entries.
 for entry in "$store"/* "$store"/.[!.]* "$store"/..?*; do
  [ -e "$entry" ] || [ -L "$entry" ] || continue
  case "${entry##*/}" in
   .krm-provision-token|provenance.sha256) ;;
   "$kind") ;;
   _xkeen) [ "$kind" = xkeen ] || return 1;;
   *) return 1;;
  esac
 done
 # A crash just after the token was copied leaves an empty owned store. It
 # is safe to remove that store even for older token-only journals.
 if [ ! -e "$store/$kind" ] && [ ! -L "$store/$kind" ] &&
    [ ! -e "$store/_xkeen" ] && [ ! -L "$store/_xkeen" ] &&
    [ ! -e "$store/provenance.sha256" ] && [ ! -L "$store/provenance.sha256" ]; then
  return 0
 fi
 [ -f "$journal/payload.sha256" ] && [ ! -L "$journal/payload.sha256" ] || return 1
 expected_lines=1
 [ "$kind" != xkeen ] || expected_lines=2
 [ "$(wc -l < "$journal/payload.sha256" | tr -d '[:space:]')" = "$expected_lines" ] || return 1
 identity_digest 1 || return 1
 if [ -e "$store/$kind" ] || [ -L "$store/$kind" ]; then
  [ -f "$store/$kind" ] && [ -x "$store/$kind" ] && [ ! -L "$store/$kind" ] || return 1
  computed=$(openssl dgst -sha256 "$store/$kind" | sed 's/^.*= //') || return 1
  [ "$recorded" = "$computed" ] || return 1
 fi
 if [ "$kind" = xkeen ]; then
  identity_digest 2 || return 1
  if [ -e "$store/_xkeen" ] || [ -L "$store/_xkeen" ]; then
   [ -d "$store/_xkeen" ] && [ ! -L "$store/_xkeen" ] || return 1
   tree_digest "$store" || return 1
   [ "$recorded" = "$computed" ] || return 1
  fi
 fi
 if [ -e "$store/provenance.sha256" ] || [ -L "$store/provenance.sha256" ]; then
  [ -f "$store/provenance.sha256" ] && [ ! -L "$store/provenance.sha256" ] || return 1
  cmp -s "$journal/payload.sha256" "$store/provenance.sha256" || return 1
 fi
}
valid_payload(){
 [ -f "$store/$kind" ] && [ -x "$store/$kind" ] && [ ! -L "$store/$kind" ] || return 1
 [ -f "$store/provenance.sha256" ] && [ ! -L "$store/provenance.sha256" ] || return 1
 if [ "$kind" = xkeen ]; then
  [ -d "$store/_xkeen" ] && [ ! -L "$store/_xkeen" ] || return 1
 fi
 valid_partial_store
}
# Refuse drift before removing anything: never delete a foreign path or
# an occupied version store, even during an explicitly requested recovery.
verify_recovery(){
 [ -d "$journal" ] && [ ! -L "$journal" ] || fail 'invalid pending transaction journal'
 [ -f "$journal/token" ] && [ ! -L "$journal/token" ] || fail 'pending journal has no ownership token; inspect manually'
 if [ -e "$store" ] || [ -L "$store" ]; then
  valid_partial_store || fail 'unowned, changed or ambiguous dependency version store; manual recovery required'
 fi
 if [ -e "$path" ] || [ -L "$path" ]; then
  owned_link "$path" "$installed_target" || fail 'dependency entrypoint changed; manual recovery required'
 fi
 if [ "$kind" = xkeen ]; then
  [ ! -e "$legacy_module_path" ] && [ ! -L "$legacy_module_path" ] || fail 'unexpected XKeen staging modules; manual recovery required'
  if [ -e "$module_path" ] || [ -L "$module_path" ]; then
   owned_link "$module_path" "$module_target" || fail 'XKeen module link changed; manual recovery required'
  fi
 fi
}
recover_pending(){
 [ -e "$journal" ] || [ -L "$journal" ] || return 0
 verify_recovery
 # A power loss after both links were published may precede journal cleanup.
 # Complete the existing transaction rather than uninstalling a working pair.
 if owned_link "$path" "$installed_target" &&
    { [ "$kind" != xkeen ] || owned_link "$module_path" "$module_target"; } &&
    owned_store && valid_payload; then
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
 [ ! -e "$legacy_module_path" ] && [ ! -L "$legacy_module_path" ] || fail 'existing XKeen staging modules; refusing overwrite'
fi
[ ! -e "$store" ] && [ ! -L "$store" ] || fail 'dependency version already exists; inspect before retrying'
mkdir -m 0700 "$journal" || fail 'cannot reserve provisioning journal'
# The journal contains no subscription credentials, authentication data, or
# remote URLs. Private permissions and sync keep the intent durable first.
token=$(openssl rand -hex 32) || fail 'cannot create dependency ownership token'
[ "${#token}" = 64 ] || fail 'invalid dependency ownership token'
printf '%s\n' "$token" > "$journal/token"
sync || fail 'cannot persist dependency transaction intent'
committed=0
cleanup(){
 if [ "$committed" = 0 ]; then
  # Preserve foreign replacements. Only clean up known writes after the
  # complete partial store still matches the independent journal identity.
  safe_cleanup=0
  if [ ! -e "$store" ] && [ ! -L "$store" ]; then
   safe_cleanup=1
  elif valid_partial_store; then
   safe_cleanup=1
  fi
  if [ "$safe_cleanup" = 1 ]; then
   if owned_link "$path" "$installed_target"; then rm -- "$path" || true; fi
   if [ "$kind" = xkeen ] && owned_link "$module_path" "$module_target"; then
    rm -- "$module_path" || true
   fi
  fi
  if owned_store && valid_partial_store; then
   rm -rf -- "$store" || true
  fi
  if [ ! -e "$store" ] && [ ! -L "$store" ]; then
   rm -rf -- "$journal"
  else
   echo 'ERROR: unfinished dependency transaction requires manual recovery' >&2
  fi
 else
  rm -rf -- "$journal"
 fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
sh "$ROOT/install/common/dependency-prepare.sh" "$kind" "$journal/prepared"
chmod 0700 "$journal/prepared/extracted/$kind"
( cd "$journal/prepared/extracted" && openssl dgst -sha256 "$kind" ) > "$journal/payload.sha256"
if [ "$kind" = xkeen ]; then
 tree_digest "$journal/prepared/extracted" || fail 'cannot record verified XKeen module identity'
 printf '%s\n' "SHA256(_xkeen)= $computed" >> "$journal/payload.sha256"
fi
chmod 0600 "$journal/payload.sha256"
sync || fail 'cannot persist verified dependency identity'
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
cp "$journal/payload.sha256" "$store/provenance.sha256"
chmod 0600 "$store/provenance.sha256"
valid_payload || fail 'installed dependency differs from verified prepared payload'
# Flush completed native payload before publishing any executable entrypoint.
sync || fail 'cannot persist prepared dependency payload'
if [ "$kind" = xkeen ]; then
 # Another XKeen setup may create the legacy source module directory while
 # we download and verify the archive. Refuse to publish a competing runtime
 # after that change, even though the original preflight was clear.
 [ ! -e "$legacy_module_path" ] && [ ! -L "$legacy_module_path" ] || fail 'legacy XKeen modules appeared during provisioning'
 ln -sT "$module_target" "$module_path" || fail 'cannot publish XKeen module link'
fi
ln -sT "$installed_target" "$path" || fail "cannot publish $kind binary link"
sync || fail 'cannot persist dependency entrypoints'
committed=1
printf '%s\n' "Provisioned $kind $version at $path. No services, router interception, firewall, or configuration were activated."
