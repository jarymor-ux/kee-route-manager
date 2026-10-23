#!/bin/sh
# Sourced by the signed installer. This journal owns only newly published KRM
# static files. It never restores controller state, Xray or router policy.
txn_fail(){ echo "ERROR: $*" >&2; return 1; }
txn_regular(){ [ -f "$1" ] && [ ! -L "$1" ]; }
txn_digest(){
 digest_output=$(openssl dgst -sha256 "$1") || return 1
 printf '%s\n' "$digest_output" | sed 's/^.*= //'
}
txn_path(){
 case "$1" in "$TXN_PREFIX"/*) ;; *) txn_fail 'transaction path is outside Entware'; return 1;; esac
 case "$1" in *'
'*|*'	'*|*/../*|*/./*|*/..|*/.) txn_fail 'invalid transaction path'; return 1;; esac
 parent=$(dirname -- "$1")
 while [ "$parent" != / ]; do
  [ ! -L "$parent" ] || { txn_fail 'linked transaction parent requires manual inspection'; return 1; }
  parent=$(dirname -- "$parent")
 done
}
txn_lock(){
 TXN_PREFIX=$1
 TXN_MODE=$2
 case "$TXN_PREFIX" in /*) ;; *) txn_fail 'Entware prefix must be absolute'; return 1;; esac
 case "$TXN_MODE" in core|local-ui|ui) ;; *) txn_fail 'invalid installer transaction mode'; return 1;; esac
 command -v flock >/dev/null 2>&1 || { txn_fail 'flock is required for serialized installation and recovery'; return 1; }
 TXN_BASE=$TXN_PREFIX/var/lib/kee-route-manager-install
 TXN_JOURNAL=$TXN_BASE/pending-$TXN_MODE
 txn_path "$TXN_BASE/lock" || return 1
 mkdir -p "$TXN_PREFIX/var/lib" || { txn_fail 'Entware var/lib directory is unavailable'; return 1; }
 if [ ! -e "$TXN_BASE" ]; then
  mkdir -m 0700 "$TXN_BASE" || { txn_fail 'cannot reserve private install journal directory'; return 1; }
 fi
 [ -d "$TXN_BASE" ] && [ ! -L "$TXN_BASE" ] && [ "$(stat -c %a "$TXN_BASE")" = 700 ] || { txn_fail 'install journal storage must be a private regular directory'; return 1; }
 if [ -e "$TXN_BASE/lock" ] || [ -L "$TXN_BASE/lock" ]; then
  txn_regular "$TXN_BASE/lock" || { txn_fail 'install lock is not a regular file'; return 1; }
 fi
 # One lock covers every mode: local-ui and standalone UI share paths.
 exec 9>"$TXN_BASE/lock"
 flock -n 9 || { txn_fail 'another installer or recovery is active'; return 1; }
}
# Preserve an existing directory's permissions in the private intent before
# tightening it for credentials. Shared directory contents are never removed.
txn_directory(){
 txn_read || return 1
 [ "$TXN_PHASE" = installing ] || { txn_fail 'cannot create install directories after activation intent'; return 1; }
 txn_path "$1/managed-entry" || return 1
 if [ -e "$1" ] || [ -L "$1" ]; then
  [ -d "$1" ] && [ ! -L "$1" ] || { txn_fail 'managed directory changed type'; return 1; }
  before=$(stat -c %a "$1")
  after=${2#0}
  if [ "$before" != "$after" ]; then
   changes=$(find "$TXN_JOURNAL/directories" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d '[:space:]')
   change=$TXN_JOURNAL/directories/$(printf '%08d' "$((changes + 1))")
   mkdir -m 0700 "$change" || return 1
   printf '%s\n' "$1" > "$change/path"
   printf '%s\n' "$before" > "$change/before"
   printf '%s\n' "$after" > "$change/after"
   stat -c %d:%i "$1" > "$change/identity" || return 1
   sync || return 1
   chmod "$after" "$1" || return 1
   sync || return 1
  fi
 else
  if [ ! -d "$(dirname -- "$1")" ]; then txn_directory "$(dirname -- "$1")" 0755 || return 1; fi
  # Unlike mkdir -p on the leaf, this refuses a late foreign directory.
  mkdir -m "$2" "$1" || { txn_fail 'managed directory appeared during creation'; return 1; }
 fi
}
txn_directory_verify(){
 for field in path before after identity; do
  txn_regular "$1/$field" || { txn_fail 'incomplete directory intent'; return 1; }
 done
 TXN_DIRECTORY=$(cat "$1/path")
 txn_path "$TXN_DIRECTORY/managed-entry" || return 1
 [ -d "$TXN_DIRECTORY" ] && [ ! -L "$TXN_DIRECTORY" ] && [ "$(stat -c %d:%i "$TXN_DIRECTORY")" = "$(cat "$1/identity")" ] || { txn_fail 'managed directory identity changed'; return 1; }
 TXN_BEFORE=$(cat "$1/before"); TXN_AFTER=$(cat "$1/after")
 case "$TXN_BEFORE:$TXN_AFTER" in *[!0-7:]*) txn_fail 'invalid directory receipt permissions'; return 1;; esac
 [ -n "$TXN_BEFORE" ] && [ -n "$TXN_AFTER" ] || { txn_fail 'missing directory receipt permissions'; return 1; }
 current=$(stat -c %a "$TXN_DIRECTORY")
 [ "$current" = "$TXN_AFTER" ] || { [ "$2" = recover ] && [ "$current" = "$TXN_BEFORE" ]; } || { txn_fail 'managed directory permissions changed'; return 1; }
}
txn_read(){
 [ -d "$TXN_JOURNAL" ] && [ ! -L "$TXN_JOURNAL" ] && [ "$(stat -c %a "$TXN_JOURNAL")" = 700 ] || { txn_fail 'no private pending installation journal'; return 1; }
 txn_regular "$TXN_JOURNAL/mode" && txn_regular "$TXN_JOURNAL/phase" || { txn_fail 'incomplete installation metadata; inspect manually'; return 1; }
 [ "$(cat "$TXN_JOURNAL/mode")" = "$TXN_MODE" ] || { txn_fail 'installation journal mode mismatch'; return 1; }
 for directory in records directories; do
  [ -d "$TXN_JOURNAL/$directory" ] && [ ! -L "$TXN_JOURNAL/$directory" ] || { txn_fail 'invalid installation receipt directory'; return 1; }
 done
 TXN_PHASE=$(cat "$TXN_JOURNAL/phase")
 case "$TXN_PHASE" in installing|launcher|activating) ;; *) txn_fail 'unknown installation phase'; return 1;; esac
}
txn_begin(){
 [ ! -e "$TXN_JOURNAL" ] && [ ! -L "$TXN_JOURNAL" ] || { txn_fail 'pending installation exists; explicitly recover or resume it'; return 1; }
 # Refuse another pending mode, even though the process lock is now free.
 for pending in "$TXN_BASE"/pending-*; do
  [ ! -e "$pending" ] && [ ! -L "$pending" ] || { txn_fail 'another installation mode has a pending journal'; return 1; }
 done
 mkdir -m 0700 "$TXN_JOURNAL" "$TXN_JOURNAL/records" "$TXN_JOURNAL/directories" || return 1
 printf '%s\n' "$TXN_MODE" > "$TXN_JOURNAL/mode"
 printf '%s\n' installing > "$TXN_JOURNAL/phase"
 sync || { txn_fail 'cannot persist installation intent'; return 1; }
}
txn_phase(){
 txn_read || return 1
 case "$TXN_PHASE:$1" in installing:launcher|installing:activating|launcher:activating|launcher:launcher|activating:activating) ;; *) txn_fail 'invalid installation phase transition'; return 1;; esac
 printf '%s\n' "$1" > "$TXN_JOURNAL/phase.next"
 sync || return 1
 mv -f "$TXN_JOURNAL/phase.next" "$TXN_JOURNAL/phase" || return 1
 sync
}
# Snapshot bytes/permissions and persist their identity before atomic creation.
# Hard-link publication refuses a late file, directory or symlink destination.
# Both paths are on Entware storage; no cross-device overwrite fallback exists.
txn_file(){
 txn_read || return 1
 [ "$TXN_PHASE" = installing ] || { txn_fail 'cannot publish files after activation intent'; return 1; }
 txn_regular "$1" || { txn_fail 'staged installer input must be a regular file'; return 1; }
 source=$1; destination=$2; permissions=${3#0}
 case "$permissions" in 600|644|700|755) ;; *) txn_fail 'invalid installed file permissions'; return 1;; esac
 txn_path "$destination" || return 1
 [ ! -e "$destination" ] && [ ! -L "$destination" ] || { txn_fail 'managed destination appeared; refusing overwrite'; return 1; }
 count=$(find "$TXN_JOURNAL/records" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d '[:space:]')
 record=$TXN_JOURNAL/records/$(printf '%08d' "$((count + 1))")
 mkdir -m 0700 "$record" || return 1
 cp "$source" "$record/payload" || return 1
 chmod "$permissions" "$record/payload" || return 1
 printf '%s\n' "$destination" > "$record/path"
 printf '%s\n' "$permissions" > "$record/mode"
 printf '%s\n' file > "$record/kind"
 txn_digest "$record/payload" > "$record/digest" || return 1
 stat -c %d:%i "$record/payload" > "$record/identity" || return 1
 sync || { txn_fail 'cannot persist managed-file intent'; return 1; }
 ln -T "$record/payload" "$destination" || { txn_fail 'cannot publish managed file without overwrite'; return 1; }
 sync
}
txn_record_read(){
 [ -d "$1" ] && [ ! -L "$1" ] || { txn_fail 'invalid installation receipt'; return 1; }
 for field in path mode kind digest identity; do
  txn_regular "$1/$field" || { txn_fail 'incomplete installation receipt; inspect manually'; return 1; }
 done
 TXN_PATH=$(cat "$1/path"); TXN_PERMISSIONS=$(cat "$1/mode")
 TXN_KIND=$(cat "$1/kind"); TXN_DIGEST=$(cat "$1/digest")
 txn_path "$TXN_PATH" || return 1
 case "$TXN_PERMISSIONS" in 600|644|700|755) ;; *) txn_fail 'invalid receipt permissions'; return 1;; esac
 case "$TXN_DIGEST" in ''|*[!0123456789abcdef]*) txn_fail 'invalid receipt digest'; return 1;; esac
 [ "${#TXN_DIGEST}" = 64 ] || { txn_fail 'invalid receipt digest length'; return 1; }
 case "$TXN_KIND" in file|link) ;; *) txn_fail 'unknown installation receipt kind'; return 1;; esac
}
txn_verify_record(){
 record=$1; allow_missing=$2; allow_launcher=$3
 txn_record_read "$record" || return 1
 expected_permissions=$TXN_PERMISSIONS
 if [ ! -e "$TXN_PATH" ] && [ ! -L "$TXN_PATH" ]; then
  [ "$allow_missing" = yes ] && return 0
  txn_fail 'installed managed file is missing; resume refused'; return 1
 fi
 if [ "$TXN_KIND" = link ]; then
  txn_regular "$record/target" && [ -L "$TXN_PATH" ] && [ "$(readlink "$TXN_PATH")" = "$(cat "$record/target")" ] || { txn_fail 'installed executable link changed'; return 1; }
 elif [ -L "$TXN_PATH" ]; then
  [ "$allow_launcher" = yes ] && txn_regular "$record/launcher-binary" || { txn_fail 'managed file was replaced by a symlink'; return 1; }
  # Signed release slots deliberately tighten executable permissions from
  # the bootstrap entrypoint's 0755 to private 0700.
  expected_permissions=700
 else
  [ -f "$TXN_PATH" ] || { txn_fail 'managed file changed type'; return 1; }
  [ "$(stat -c %d:%i "$TXN_PATH")" = "$(cat "$record/identity")" ] || { txn_fail 'managed file identity changed; foreign replacement retained'; return 1; }
 fi
 [ -f "$TXN_PATH" ] && [ "$(txn_digest "$TXN_PATH")" = "$TXN_DIGEST" ] && [ "$(stat -Lc %a "$TXN_PATH")" = "$expected_permissions" ] || { txn_fail 'managed bytes or permissions changed; installation retained'; return 1; }
}
txn_launcher_binary(){
 txn_read || return 1
 case "$TXN_PHASE" in installing|launcher) ;; *) txn_fail 'launcher transition already passed'; return 1;; esac
 case "$1" in "$TXN_PREFIX/bin/kee-route-managerd"|"$TXN_PREFIX/bin/kee-route-managerctl"|"$TXN_PREFIX/bin/kee-route-manager-ui") ;; *) txn_fail 'not a launcher-managed executable'; return 1;; esac
 for receipt in "$TXN_JOURNAL"/records/*; do
  txn_record_read "$receipt" || return 1
  if [ "$TXN_PATH" = "$1" ]; then
   printf '%s\n' yes > "$receipt/launcher-binary"
   sync; return
  fi
 done
 txn_fail 'launcher binary has no installation receipt'
}
txn_verify_resume(){
 txn_read || return 1
 case "$TXN_PHASE" in launcher|activating) ;; *) txn_fail 'partial file installation must be recovered before a fresh retry'; return 1;; esac
 for change in "$TXN_JOURNAL"/directories/*; do
  [ -e "$change" ] || continue
  txn_directory_verify "$change" resume || return 1
 done
 for receipt in "$TXN_JOURNAL"/records/*; do
  [ -e "$receipt" ] || continue
  txn_verify_record "$receipt" no yes || return 1
 done
}
txn_capture_links(){
 txn_verify_resume || return 1
 for receipt in "$TXN_JOURNAL"/records/*; do
  [ -e "$receipt" ] || continue
  txn_record_read "$receipt" || return 1
  if [ -L "$TXN_PATH" ] && [ "$TXN_KIND" = file ]; then
   readlink "$TXN_PATH" > "$receipt/target"
   printf '%s\n' 700 > "$receipt/mode.next"
   mv -f "$receipt/mode.next" "$receipt/mode" || return 1
   sync || return 1
   printf '%s\n' link > "$receipt/kind.next"
   mv -f "$receipt/kind.next" "$receipt/kind" || return 1
  fi
 done
 sync
}
txn_recover(){
 txn_read || return 1
 [ "$TXN_PHASE" = installing ] || { txn_fail 'activation or launcher intent exists; retain state/routing and use KRM_INSTALL_ACTION=resume'; return 1; }
 for change in "$TXN_JOURNAL"/directories/*; do
  [ -e "$change" ] || continue
  txn_directory_verify "$change" recover || return 1
 done
 # Validate every owned path first. A single edited or foreign replacement
 # prevents partial cleanup of otherwise matching application files.
 for receipt in "$TXN_JOURNAL"/records/*; do
  [ -e "$receipt" ] || continue
  txn_verify_record "$receipt" yes no || return 1
 done
 for receipt in "$TXN_JOURNAL"/records/*; do
  [ -e "$receipt" ] || continue
  txn_record_read "$receipt" || return 1
  if [ -e "$TXN_PATH" ] || [ -L "$TXN_PATH" ]; then rm -- "$TXN_PATH" || return 1; fi
 done
 for change in "$TXN_JOURNAL"/directories/*; do
  [ -e "$change" ] || continue
  txn_directory_verify "$change" recover || return 1
  chmod "$TXN_BEFORE" "$TXN_DIRECTORY" || return 1
 done
 # Directories, runtime data and unknown paths are deliberately retained.
 rm -rf -- "$TXN_JOURNAL" || return 1
 sync
 printf '%s\n' 'Rolled back owned static files before activation. Existing directories and unrelated files retained.'
}
txn_commit(){
 txn_read || return 1
 [ "$TXN_PHASE" = activating ] || { txn_fail 'readiness can only commit an activation transaction'; return 1; }
 rm -rf -- "$TXN_JOURNAL" || return 1
 sync
}
