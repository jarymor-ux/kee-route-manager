#!/bin/sh
# Explicitly approved, no-overwrite installation of pinned native payloads.
# This does not initialize XKeen, change firewall/routing, or start services.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 2 ] && [ "$2" = --approve ] || fail 'usage: dependency-provision.sh xray|xkeen --approve'
kind=$1
case "$kind" in xray) version=v26.10.21;; xkeen) version=2.1;; *) fail 'unsupported dependency';; esac
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
# The override is only used by isolated, unprivileged filesystem tests.
prefix=${KRM_DEPENDENCY_PREFIX:-/opt}
case "$prefix" in /*) ;; *) fail 'prefix must be absolute';; esac
if [ "$prefix" = /opt ]; then
 [ "$(id -u)" = 0 ] || fail 'run as root on the router'
 [ -d /opt ] && [ -x /opt/bin/opkg ] || fail 'mounted Entware with opkg is required'
fi
[ -d "$prefix/sbin" ] && [ ! -L "$prefix/sbin" ] || fail 'missing or linked Entware sbin directory'
[ -d "$prefix/var/lib" ] && [ ! -L "$prefix/var/lib" ] || fail 'missing or linked Entware var/lib directory'
path="$prefix/sbin/$kind"
[ ! -e "$path" ] && [ ! -L "$path" ] || fail "existing dependency path: $path"
if [ "$kind" = xkeen ]; then
 [ ! -e "$prefix/sbin/_xkeen" ] && [ ! -L "$prefix/sbin/_xkeen" ] || fail 'existing XKeen modules; refusing overwrite'
fi
base="$prefix/var/lib/kee-route-manager-deps"
if [ ! -d "$base" ]; then
 [ ! -e "$base" ] && [ ! -L "$base" ] || fail 'dependency storage path is occupied'
 mkdir -m 0700 "$base" || fail 'cannot create private dependency storage'
fi
[ ! -L "$base" ] || fail 'dependency storage is a symlink'
[ "$(stat -c %a "$base")" = 700 ] || fail 'dependency storage permissions must be 0700'
store="$base/$kind-$version"
[ ! -e "$store" ] && [ ! -L "$store" ] || fail 'dependency version already exists; inspect before retrying'
stage=$(mktemp -d "$base/.prepare.XXXXXX") || fail 'cannot prepare dependencies'
committed=0
owned_store=0
cleanup(){
 if [ "$committed" != 1 ]; then
  if [ -n "${installed_entry:-}" ] && [ -L "$installed_entry" ] && [ "$(readlink "$installed_entry")" = "$installed_target" ]; then rm -f "$installed_entry"; fi
  if [ -n "${installed_module:-}" ] && [ -L "$installed_module" ] && [ "$(readlink "$installed_module")" = "$module_target" ]; then rm -f "$installed_module"; fi
  if [ "$owned_store" = 1 ]; then rm -rf -- "$store"; fi
 fi
 rm -rf -- "$stage"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
sh "$ROOT/install/common/dependency-prepare.sh" "$kind" "$stage/prepared"
mkdir -m 0700 "$store" || fail 'cannot reserve immutable dependency directory'
owned_store=1
if [ "$kind" = xray ]; then
 mv "$stage/prepared/extracted/xray" "$store/xray"
 chmod 0700 "$store/xray"
else
 mv "$stage/prepared/extracted/xkeen" "$store/xkeen"
 mv "$stage/prepared/extracted/_xkeen" "$store/_xkeen"
 chmod 0700 "$store/xkeen"
fi
# Record a self-contained digest for operator verification; the source archive
# was verified against the upstream immutable release pin before extraction.
( cd "$store" && if [ "$kind" = xray ]; then openssl dgst -sha256 xray; else openssl dgst -sha256 xkeen; fi ) > "$store/provenance.sha256"
chmod 0600 "$store/provenance.sha256"
# A symlink is published atomically via ln -s. Neither a regular file nor an
# existing symlink (including dangling ones) is ever replaced. Failure rolls
# back only links that still point at our own freshly created version store.
if [ "$kind" = xkeen ]; then
 module_target="$store/_xkeen"
 ln -s "$module_target" "$prefix/sbin/_xkeen" || fail 'cannot install XKeen module link'
 installed_module="$prefix/sbin/_xkeen"
fi
installed_target="$store/$kind"
ln -s "$installed_target" "$prefix/sbin/$kind" || fail "cannot install $kind binary link"
installed_entry="$prefix/sbin/$kind"
committed=1
printf '%s\n' "Prepared $kind $version at $prefix/sbin/$kind. No services, router interception, firewall, or configuration were activated."
