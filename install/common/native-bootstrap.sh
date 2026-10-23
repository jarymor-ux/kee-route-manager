#!/bin/sh
# Install a clean, disabled native configuration; never activate router policy.
# Must run from an authenticated release, after explicit dependency approval.
set -eu
umask 077
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 2 ] || fail 'usage: native-bootstrap.sh /absolute/staging-dir --approve|--recover|--verify-existing'
stage=$1
action=$2
case "$stage" in /*);; *) fail 'staging directory must be absolute';; esac
case "$action" in --approve|--recover|--verify-existing);; *) fail 'explicit --approve, --recover or --verify-existing required';; esac
prefix=${KRM_DEPENDENCY_PREFIX:-/opt}
case "$prefix" in /*);; *) fail 'prefix must be absolute';; esac
# The prefix override is only for isolated filesystem tests, never a router
# selection mechanism. A normal native install requires mounted Entware.
if [ "$prefix" = /opt ]; then
 [ "$(id -u)" = 0 ] || fail 'run as root on the router'
 [ -x /opt/bin/opkg ] || fail 'mounted Entware with opkg is required'
fi
for dir in "$prefix" "$prefix/etc" "$prefix/etc/init.d" "$prefix/var" "$prefix/var/lib" "$prefix/sbin"; do
 [ -d "$dir" ] && [ ! -L "$dir" ] || fail 'missing or linked native parent directory'
done
command -v flock >/dev/null 2>&1 || fail 'flock is required for serialized native provisioning'
command -v openssl >/dev/null 2>&1 || fail 'openssl is required'
command -v timeout >/dev/null 2>&1 || fail 'timeout is required for bounded native validation'
base="$prefix/var/lib/kee-route-manager-native"
if [ ! -d "$base" ]; then
 [ ! -e "$base" ] && [ ! -L "$base" ] || fail 'native transaction storage is occupied'
 [ "$action" = --approve ] || fail 'no native transaction storage to recover'
 mkdir -m 0700 "$base" || fail 'cannot reserve native transaction storage'
fi
[ ! -L "$base" ] && [ "$(stat -c %a "$base")" = 700 ] || fail 'native transaction storage must be private and unlinked'
[ ! -L "$base/.lock" ] || fail 'native transaction lock is linked'
exec 9> "$base/.lock"
flock -n 9 || fail 'another native provisioning or recovery is running'
journal="$base/.pending"
xray_path="$prefix/etc/xray"
xkeen_path="$prefix/etc/xkeen"
service_path="$prefix/etc/init.d/S05xkeen"
template="$prefix/sbin/.xkeen/02_install/07_install_register/04_register_init.sh"
expected=62509ee96d186fad7bfc75aafeea14171f5fc1ed4a091527b755d5ba57f8c887
absent(){ [ ! -e "$1" ] && [ ! -L "$1" ]; }
owned_link(){ [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ]; }
# A deterministic manifest includes modes, directories and every regular file.
# Reject links/special files before hashing so a changed FIFO cannot hang us.
tree_manifest(){
 tree_root=$1
 [ -d "$tree_root" ] && [ ! -L "$tree_root" ] || return 1
 [ -z "$(find "$tree_root" ! -type d ! -type f -print)" ] || return 1
 (
  cd "$tree_root"
  find . -mindepth 1 ! -path './.krm-native-manifest' -print | LC_ALL=C sort | while IFS= read -r entry; do
   case "$entry" in
    ./xkeen/krm-activation-approved)
     [ "${2:-strict}" = verify ] || exit 1
     continue
     ;;
    ./xray|./xray/configs|./xray/dat|./xkeen|./S05xkeen|./.krm-native-token|./xkeen/xkeen.json|./xkeen/native-service.sh|./xkeen/ip_exclude.lst|./xkeen/port_exclude.lst|./xkeen/port_proxying.lst) ;;
    ./xray/configs/*.json)
     name=${entry#./xray/configs/}
     case "$name" in ''|*[!a-zA-Z0-9_.-]*) exit 1;; esac
     ;;
    *) exit 1;;
   esac
   mode=$(stat -c %a "$entry") || exit 1
   if [ -d "$entry" ]; then
    printf 'D %s %s\n' "$mode" "$entry"
   else
    hash=$(openssl dgst -sha256 "$entry" | sed 's/^.*= //') || exit 1
    printf 'F %s %s %s\n' "$mode" "$hash" "$entry"
   fi
  done
 )
}
# Generate expected bytes from the pinned template and signed downstream
# profile, both for publication and read-only verification of an earlier setup.
write_service(){
 service_output=$1
 service_work=$2
 [ -f "$template" ] && [ ! -L "$template" ] || fail 'verified XKeen native service template is missing'
 cp "$template" "$service_work/template"
 actual=$(openssl dgst -sha256 "$service_work/template" | sed 's/^.*= //')
 [ "$actual" = "$expected" ] || fail 'native service template changed during preparation'
 # Maintained downstream changes to the exact pinned 2.1 service: disable
 # autostart/DSCP/full-policy interception and refuse cached-mark/proxy-all if RCI
 # changes between the signed wrapper's policy check and native startup.
 needle='        policy_mark=$(get_policy_mark "$name_policy")'
 [ "$(grep -Fxc "$needle" "$service_work/template" || true)" = 1 ] || fail 'unexpected upstream policy-mark guard location'
 cat > "$service_work/policy-guard" <<'POLICY_GUARD'
        # KRM: a missing, ambiguous or zero policy mark must never proxy all.
        krm_policy_hex=""
        case "$policy_mark" in 0x*) krm_policy_hex=${policy_mark#0x};; esac
        krm_policy_valid=0
        case "$krm_policy_hex" in
            ''|*[!0-9a-fA-F]*) ;;
            *)
                if [ "${#krm_policy_hex}" -le 8 ]; then
                    while [ "${krm_policy_hex#0}" != "$krm_policy_hex" ]; do krm_policy_hex=${krm_policy_hex#0}; done
                    [ -z "$krm_policy_hex" ] || krm_policy_valid=1
                fi
                ;;
        esac
        if [ "$krm_policy_valid" != 1 ]; then
            echo 'ERROR: native policy mark unavailable or unsafe; activation refused' >&2
            if [ "$_ps_mutex_rc" -eq 0 ]; then _release_proxy_mutex; fi
            trap - INT TERM HUP
            return 1
        fi
POLICY_GUARD
 ipv6_line='    ip6_supported=$(check_ipv6_active && echo true || echo false)'
 [ "$(grep -Fxc "$ipv6_line" "$service_work/template" || true)" = 1 ] || fail 'unexpected upstream IPv6 detection location'
 for setting in 'ipv6_support="on"' 'start_auto="on"' 'dscp_enable="on"' 'name_policy_full="xkeen_full"'; do
 [ "$(grep -Fxc "$setting" "$service_work/template" || true)" = 1 ] || fail 'unexpected upstream native policy scope setting'
 done
 sed -e 's/^    ip6_supported=$(check_ipv6_active \&\& echo true || echo false)$/    ip6_supported=false # KRM keeps IPv6 operator-owned/' -e 's/^start_auto="on"$/start_auto="off"/' -e 's/^dscp_enable="on"$/dscp_enable="off"/' -e 's/^name_policy_full="xkeen_full"$/name_policy_full=""/' "$service_work/template" > "$service_work/autostart-disabled"
 awk -v needle="$needle" -v guard="$service_work/policy-guard" '
 {print}
 $0 == needle {while ((getline line < guard) > 0) print line; close(guard)}
 ' "$service_work/autostart-disabled" > "$service_output"
 grep -Fx 'start_auto="off"' "$service_output" >/dev/null || fail 'unexpected upstream autostart setting'
 grep -Fx 'dscp_enable="off"' "$service_output" >/dev/null || fail 'unexpected native DSCP scope'
 grep -Fx 'name_policy_full=""' "$service_output" >/dev/null || fail 'unexpected native full-policy scope'
 grep -Fx 'proxy_dns="off"' "$service_output" >/dev/null || fail 'unexpected upstream DNS setting'
 grep -Fx 'proxy_router="off"' "$service_output" >/dev/null || fail 'unexpected upstream router proxy setting'
 grep -Fx 'ipv6_support="on"' "$service_output" >/dev/null || fail 'kernel IPv6 must remain operator-owned'
 grep -Fx '    ip6_supported=false # KRM keeps IPv6 operator-owned' "$service_output" >/dev/null || fail 'native IPv6 interception must remain disabled'
 sh -n "$service_output" || fail 'invalid native service shell syntax'
 chmod 0700 "$service_output"
}
read_intent(){
 [ -d "$journal" ] && [ ! -L "$journal" ] || fail 'invalid native transaction journal'
 [ -f "$journal/token" ] && [ ! -L "$journal/token" ] || fail 'native intent has no ownership token; inspect manually'
 token=$(cat "$journal/token")
 case "$token" in ''|*[!0-9a-f]*) fail 'invalid native ownership token';; esac
 [ "${#token}" = 64 ] || fail 'invalid native ownership token length'
 store="$base/install-$token"
}
verify_store(){
 [ -d "$store" ] && [ ! -L "$store" ] || return 1
 [ -f "$store/.krm-native-token" ] && [ ! -L "$store/.krm-native-token" ] || return 1
 cmp -s "$journal/token" "$store/.krm-native-token" || return 1
 [ -f "$journal/manifest" ] && [ ! -L "$journal/manifest" ] || return 1
 [ -f "$store/.krm-native-manifest" ] && [ ! -L "$store/.krm-native-manifest" ] || return 1
 cmp -s "$journal/manifest" "$store/.krm-native-manifest" || return 1
 tree_manifest "$store" > "$journal/observed" || return 1
 cmp -s "$journal/manifest" "$journal/observed"
}
recover(){
 read_intent
 # Refuse all drift before unlinking anything. Do not turn recovery into an
 # uninstall of a later active/edited native setup.
 if ! absent "$store"; then
  verify_store || fail 'native payload changed or incomplete; manual recovery required'
 fi
 for item in xray xkeen service; do
  case "$item" in
   xray) path=$xray_path; target="$store/xray";;
   xkeen) path=$xkeen_path; target="$store/xkeen";;
   service) path=$service_path; target="$store/S05xkeen";;
  esac
  if ! absent "$path"; then
   owned_link "$path" "$target" || fail 'native destination changed; manual recovery required'
   [ -d "$store" ] || fail 'native payload missing; manual recovery required'
  fi
 done
 if owned_link "$xray_path" "$store/xray" && owned_link "$xkeen_path" "$store/xkeen" && owned_link "$service_path" "$store/S05xkeen"; then
  rm -rf -- "$journal"
  sync || fail 'cannot persist native transaction completion'
  printf '%s\n' 'Confirmed completed disabled native setup; no service was started.'
  return
 fi
 # Content identity, exact publication links and the attempt token all match.
 # The entire operation is still inactive, so partial owned files are removable.
 if owned_link "$service_path" "$store/S05xkeen"; then rm -- "$service_path"; fi
 if owned_link "$xkeen_path" "$store/xkeen"; then rm -- "$xkeen_path"; fi
 if owned_link "$xray_path" "$store/xray"; then rm -- "$xray_path"; fi
 if [ -d "$store" ]; then rm -rf -- "$store"; fi
 rm -rf -- "$journal"
 sync || fail 'cannot persist native setup rollback'
 printf '%s\n' 'Rolled back interrupted inactive native setup; safe to retry.'
}
# A completed but not yet KRM-managed native setup may be retried with a new
# private RCI token or the exact activation receipt. Neither exception applies
# to destructive --recover, whose original complete manifest stays immutable.
verify_existing(){
 absent "$journal" || fail 'pending native setup requires explicit --recover before retry'
 [ -L "$xray_path" ] || fail 'native Xray publication is not an owned link'
 target=$(readlink "$xray_path")
 case "$target" in "$base/"install-*/xray) ;; *) fail 'native Xray link is outside owned storage';; esac
 store=${target%/xray}
 token=${store##*/install-}
 case "$token" in ''|*[!0-9a-f]*) fail 'invalid native publication token';; esac
 [ "${#token}" = 64 ] && [ "$store" = "$base/install-$token" ] || fail 'invalid native publication path'
 [ -d "$store" ] && [ ! -L "$store" ] && [ "$(stat -c %a "$store")" = 700 ] || fail 'native version store changed'
 owned_link "$xkeen_path" "$store/xkeen" && owned_link "$service_path" "$store/S05xkeen" || fail 'native sibling publication changed'
 for metadata in .krm-native-token .krm-native-manifest; do
  [ -f "$store/$metadata" ] && [ ! -L "$store/$metadata" ] && [ "$(stat -c %a "$store/$metadata")" = 600 ] || fail 'native ownership receipt unavailable or changed'
 done
 printf '%s\n' "$token" | cmp -s - "$store/.krm-native-token" || fail 'native ownership token changed'
 token_config=$store/xkeen/xkeen.json
 [ -f "$token_config" ] && [ ! -L "$token_config" ] && [ "$(stat -c %a "$token_config")" = 600 ] || fail 'native token configuration must remain private and regular'
 [ "$(wc -c < "$token_config" | tr -d '[:space:]')" -le 65536 ] || fail 'native token configuration exceeds limit'
 command -v jq >/dev/null 2>&1 || fail 'jq is required to verify native token configuration'
 jq -es 'length == 1 and (.[0] | if type == "object" then (keys - ["rci_token"] | length == 0) and ((if has("rci_token") then .rci_token else "" end) | type == "string" and length <= 4096 and test("^[A-Za-z0-9._~+/=-]*$")) else false end)' "$token_config" >/dev/null 2>&1 || fail 'native token configuration is not a strict token-only object'
 approval=$store/xkeen/krm-activation-approved
 if ! absent "$approval"; then
  [ -f "$approval" ] && [ ! -L "$approval" ] && [ "$(stat -c %a "$approval")" = 600 ] || fail 'native activation receipt changed'
  printf 'approved\n' | cmp -s - "$approval" || fail 'native activation receipt changed'
 fi
 verify_work=$(mktemp -d "${TMPDIR:-/tmp}/krm-native-verify.XXXXXX") || fail 'cannot reserve private verification workspace'
 trap 'rm -rf -- "$verify_work"' EXIT
 tree_manifest "$store" verify > "$verify_work/observed" || fail 'native payload has unexpected paths or types'
 # Keep path/type/mode identity for the token config; only its hash is mutable.
 for manifest in observed expected; do
  source=$verify_work/observed
  [ "$manifest" != expected ] || source=$store/.krm-native-manifest
  awk '$1 == "F" && $2 == "600" && $4 == "./xkeen/xkeen.json" {$3 = "TOKEN"} {print}' "$source" > "$verify_work/$manifest.normalized" || fail 'cannot verify native receipt'
 done
 cmp -s "$verify_work/expected.normalized" "$verify_work/observed.normalized" || fail 'native payload differs from original receipt'
 cmp -s "$ROOT/install/keenetic/native-xkeen-service.sh" "$store/S05xkeen" || fail 'native wrapper differs from signed release'
 write_service "$verify_work/native-service.sh" "$verify_work"
 cmp -s "$verify_work/native-service.sh" "$store/xkeen/native-service.sh" || fail 'native service differs from pinned guarded profile'
 printf '%s\n' 'Verified existing native publication; no service or policy was changed.'
}
if [ "$action" = --verify-existing ]; then verify_existing; exit 0; fi
if [ "$action" = --recover ]; then recover; exit 0; fi
absent "$journal" || fail 'pending native setup exists; inspect and run --recover first'
for path in "$xray_path" "$xkeen_path" "$service_path" "$prefix/etc/init.d/S24xray" "$prefix/etc/init.d/S99xkeen" "$prefix/etc/init.d/S99xkeenstart" "$prefix/etc/ndm/netfilter.d/proxy.sh" "$prefix/etc/ndm/schedule.d/00-xkeen-hotspot-sync.sh"; do
 absent "$path" || fail 'existing native configuration, service or hook; refusing overwrite'
done
# A live client without its expected configuration is an ambiguous owner.
if command -v pidof >/dev/null 2>&1; then
 if pidof xray >/dev/null 2>&1 || pidof mihomo >/dev/null 2>&1; then
  fail 'existing native proxy process; stop and inspect its ownership first'
 fi
fi
[ -d "$stage/configs" ] && [ ! -L "$stage/configs" ] || fail 'missing clean staged Xray configs'
[ -z "$(find "$stage/configs" -mindepth 1 ! -type f -print)" ] || fail 'staged configs must contain regular JSON files only'
[ -z "$(find "$stage/configs" -mindepth 1 -name '.*' -print)" ] || fail 'hidden staged configs are not supported'
files=0
for file in "$stage/configs/"*; do
 [ -f "$file" ] && [ ! -L "$file" ] || fail 'empty or linked staged configuration'
 name=${file##*/}
 case "$name" in *.json);; *) fail 'only strict JSON config files may be staged';; esac
 case "$name" in ''|*[!a-zA-Z0-9_.-]*) fail 'unsafe staged configuration filename';; esac
 [ "$(wc -c < "$file" | tr -d '[:space:]')" -le 4194304 ] || fail 'staged config exceeds 4 MiB'
 files=$((files + 1))
 [ "$files" -le 128 ] || fail 'too many staged config files'
done
[ "$(du -sk "$stage/configs" | awk '{print $1}')" -le 16384 ] || fail 'staged configuration exceeds 16 MiB'
if [ -e "$stage/xkeen.json" ] || [ -L "$stage/xkeen.json" ]; then
 [ -f "$stage/xkeen.json" ] && [ ! -L "$stage/xkeen.json" ] || fail 'staged XKeen config must be a regular file'
 [ "$(wc -c < "$stage/xkeen.json" | tr -d '[:space:]')" -le 65536 ] || fail 'staged XKeen config exceeds 64 KiB'
 command -v jq >/dev/null 2>&1 || fail 'jq is required to validate staged XKeen JSON'
fi
[ -f "$template" ] && [ ! -L "$template" ] || fail 'verified XKeen native service template is missing'
actual=$(openssl dgst -sha256 "$template" | sed 's/^.*= //')
[ "$actual" = "$expected" ] || fail 'XKeen 2.1 native service digest mismatch'
[ -x "$prefix/sbin/xray" ] || fail 'native Xray executable is missing'
# Reserve conservative headroom before invoking a native validator. The
# required filesystem reserve is additional to this small configuration tree.
available=$(awk '/^MemAvailable:/ {print $2; found=1} END {if (!found) exit 1}' /proc/meminfo) || fail 'cannot determine available RAM'
case "$available" in ''|*[!0-9]*) fail 'invalid available memory';; esac
[ "$available" -ge 32768 ] || fail 'less than 32 MiB RAM available for native validation'
space=$(df -Pk "$base" | awk 'END {print $4}')
case "$space" in ''|*[!0-9]*) fail 'cannot determine Entware free space';; esac
[ "$space" -ge 49152 ] || fail 'less than 48 MiB Entware storage available'
mkdir -m 0700 "$journal" || fail 'cannot reserve native intent journal'
openssl rand -hex 32 > "$journal/token" || fail 'cannot create native ownership token'
read_intent
mkdir -m 0700 "$journal/payload" "$journal/payload/xray" "$journal/payload/xray/configs" "$journal/payload/xray/dat" "$journal/payload/xkeen"
cp "$stage/configs/"*.json "$journal/payload/xray/configs/"
chmod 0600 "$journal/payload/xray/configs/"*.json
if [ -f "$stage/xkeen.json" ]; then
 cp "$stage/xkeen.json" "$journal/payload/xkeen/xkeen.json"
else
 printf '{}\n' > "$journal/payload/xkeen/xkeen.json"
fi
for name in ip_exclude.lst port_exclude.lst port_proxying.lst; do : > "$journal/payload/xkeen/$name"; done
chmod 0600 "$journal/payload/xkeen/"*
# DNS/router interception stays off. The downstream profile limits native
# interception to the one explicitly approved xkeen policy.
write_service "$journal/payload/xkeen/native-service.sh" "$journal"
wrapper="$ROOT/install/keenetic/native-xkeen-service.sh"
[ -f "$wrapper" ] && [ ! -L "$wrapper" ] || fail 'signed native service wrapper is missing'
cp "$wrapper" "$journal/payload/S05xkeen"
sh -n "$journal/payload/S05xkeen" || fail 'invalid signed native service wrapper'
chmod 0700 "$journal/payload/S05xkeen"
cp "$journal/token" "$journal/payload/.krm-native-token"
chmod 0600 "$journal/payload/.krm-native-token"
# Validate the private copied snapshot, not a mutable source pathname. Errors
# from both parsers stay private because they can contain credentials.
command -v jq >/dev/null 2>&1 || fail 'jq is required to validate staged XKeen JSON'
jq -es 'length == 1 and (.[0] | if type == "object" then (keys - ["rci_token"] | length == 0) and ((if has("rci_token") then .rci_token else "" end) | type == "string" and length <= 4096 and test("^[A-Za-z0-9._~+/=-]*$")) else false end)' "$journal/payload/xkeen/xkeen.json" >/dev/null 2>&1 || fail 'staged XKeen config must be one strict token-only JSON object'
timeout -k 2 30 "$prefix/sbin/xray" run -test -confdir "$journal/payload/xray/configs" >/dev/null 2>&1 || fail 'native Xray offline validation failed; inspect privately'
for path in "$xray_path" "$xkeen_path" "$service_path" "$prefix/etc/init.d/S24xray" "$prefix/etc/init.d/S99xkeen" "$prefix/etc/init.d/S99xkeenstart" "$prefix/etc/ndm/netfilter.d/proxy.sh" "$prefix/etc/ndm/schedule.d/00-xkeen-hotspot-sync.sh"; do
 absent "$path" || fail 'native ownership changed during preparation; inspect pending journal'
done
tree_manifest "$journal/payload" > "$journal/manifest" || fail 'cannot record native payload identity'
cp "$journal/manifest" "$journal/payload/.krm-native-manifest"
chmod 0600 "$journal/payload/.krm-native-manifest"
sync || fail 'cannot persist native intent before publication'
absent "$store" || fail 'native payload destination appeared; inspect pending intent'
# The random store stays private; -T prevents nesting into a late directory.
# Verify that the source disappeared, because mv -n may report skipped success.
mv -nT "$journal/payload" "$store" || fail 'cannot publish native version store'
[ ! -e "$journal/payload" ] && verify_store || fail 'native payload publication collision'
sync || fail 'cannot persist native payload before entrypoints'
ln -sT "$store/xray" "$xray_path" || fail 'cannot publish native Xray configuration'
ln -sT "$store/xkeen" "$xkeen_path" || fail 'cannot publish native XKeen configuration'
ln -sT "$store/S05xkeen" "$service_path" || fail 'cannot publish disabled native service'
sync || fail 'cannot persist native entrypoints'
rm -rf -- "$journal"
sync || fail 'cannot persist native setup completion'
printf '%s\n' 'Installed disabled native XKeen/Xray configuration. No service, DNS, firewall, WAN policy or scheduler was activated.'
