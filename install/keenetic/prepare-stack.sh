#!/bin/sh
# Invoked only from an authenticated release, before controller installation.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
MODE=${KRM_MODE:-local-ui}
[ "$MODE" != ui ] || exit 0
[ "$(id -u)" = 0 ] || fail 'run as root in the Entware shell'
[ -d /opt ] && [ -x /opt/bin/opkg ] || fail 'install and mount Entware first; /opt/bin/opkg is required'
# Never repair a live or retained KRM installation through the fresh installer.
for existing in /opt/etc/kee-route-manager/config.yaml /opt/bin/kee-route-managerd /opt/bin/kee-route-manager-launcher; do
 [ ! -e "$existing" ] && [ ! -L "$existing" ] || fail 'existing controller installation detected; use documented backup/uninstall/rollback procedure'
done
. "$ROOT/install/common/architecture.sh"
ARCH=$(krm_detect_arch) || exit 1
CTL=$ROOT/dist/kee-route-managerctl-linux-$ARCH
INPUT=${KRM_CONFIG_FILE:-}
[ -x "$CTL" ] && [ -f "$INPUT" ] || fail 'signed CLI and private controller config are required'
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/krm-stack.XXXXXX")
new_config_dir=0
new_xkeen_tree=0
cleanup(){
 result=$?
 trap - EXIT
 if [ "$result" != 0 ] && [ -f "$STAGE/publications" ]; then
  while read -r temporary target; do
   # Hard-link identity proves this is our published file, even on failure.
   if [ "$temporary" -ef "$target" ]; then rm -f "$target"; fi
  done < "$STAGE/publications"
 fi
 if [ "$result" != 0 ] && [ "$new_xkeen_tree" = 1 ] && [ /opt/sbin/.xkeen -ef "/proc/$$/fd/4" ]; then
  rm -rf /opt/sbin/.xkeen
 fi
 if [ "$result" != 0 ] && [ "$new_config_dir" = 1 ] && [ "$config_dir" -ef "/proc/$$/fd/3" ]; then
  rmdir "$config_dir" 2>/dev/null || true
 fi
 if [ -f "$STAGE/temporary-files" ]; then
  while IFS= read -r temporary; do rm -f "$temporary"; done < "$STAGE/temporary-files"
 fi
 rm -rf "$STAGE"
 exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
"$CTL" validate --config "$INPUT" > "$STAGE/validated"
sed '1d' "$STAGE/validated" > "$STAGE/config.json"
# Refuse resource exhaustion before package installation, extraction or writes.
available_ram=$(awk '/^MemAvailable:/ {a=$2} /^MemFree:/ {f=$2} END {if (a) print a; else print f}' /proc/meminfo)
case "$available_ram" in ''|*[!0-9]*) fail 'cannot determine available RAM';; esac
[ "$available_ram" -ge 32768 ] || fail 'less than 32 MiB available RAM; stop optional work and retry'
ensure_packages(){
 missing=
 for package in "$@"; do
  if ! /opt/bin/opkg status "$package" 2>/dev/null | grep -q '^Status:.* installed$'; then missing="$missing $package"; fi
 done
 if [ -n "$missing" ]; then
  /opt/bin/opkg update > "$STAGE/opkg.log" 2>&1 || fail 'Entware package index update failed; check trusted package feeds'
  # The list is built exclusively from the literal package names below.
  # shellcheck disable=SC2086
  /opt/bin/opkg install $missing >> "$STAGE/opkg.log" 2>&1 || fail 'required Entware runtime package installation failed'
 fi
}
xkeen_consent=0
require_xkeen_consent(){
 [ "$xkeen_consent" = 0 ] || return 0
 if [ "${KRM_ALLOW_XKEEN_FALLBACK_ALL:-0}" != 1 ]; then
  [ -t 0 ] && [ -r /dev/tty ] && [ -w /dev/tty ] || fail 'new XKeen requires routing-scope consent: set KRM_ALLOW_XKEEN_FALLBACK_ALL=1 to allow its ALL-client fallback, or run interactively'
  printf '%s\n' 'New XKeen uses an existing named XKeen router policy. If that policy is missing or unavailable, XKeen falls back to intercepting ALL clients.' > /dev/tty
  printf 'Allow this routing scope and install XKeen? [y/N] ' > /dev/tty
  answer=
  IFS= read -r answer < /dev/tty || true
  case "$answer" in y|Y|yes|YES) :;; *) fail 'new XKeen installation declined; existing stack preserved';; esac
 fi
 xkeen_consent=1
}
# A fresh default stack must refuse before even installing its JSON prerequisite
# when no terminal or explicit routing-scope consent is available.
if ! command -v jq >/dev/null 2>&1 && [ ! -x /opt/sbin/xkeen ]; then require_xkeen_consent; fi
command -v jq >/dev/null 2>&1 || ensure_packages jq
command -v jq >/dev/null 2>&1 || fail 'Entware jq is required'
binary=$(jq -er '.xray.binary' "$STAGE/config.json")
xkeen=$(jq -er '.platform.keenetic.xkeen_binary' "$STAGE/config.json")
config_dir=$(jq -er '.xray.config_dir' "$STAGE/config.json")
asset_dir=$(jq -er '.xray.asset_dir' "$STAGE/config.json")
new_xray=0; new_xkeen=0; new_config=1
[ -x "$binary" ] || new_xray=1
[ -x "$xkeen" ] || new_xkeen=1
for file in "$config_dir"/*.json; do
 if [ -e "$file" ] || [ -L "$file" ]; then new_config=0; break; fi
done
if [ "$new_xray$new_xkeen$new_config" = 000 ]; then exit 0; fi
# Automatic installation only owns the standard Entware stack. A prepared
# working custom installation is supported without changing any of its paths.
[ "$binary" = /opt/sbin/xray ] && [ "$xkeen" = /opt/sbin/xkeen ] || fail 'missing custom Xray/XKeen executable: install it explicitly or use standard Entware paths'
if [ "$new_xray" = 1 ]; then
 [ ! -L "$binary" ] && [ ! -e "$binary" ] || fail 'refusing an existing missing-Xray path'
fi
if [ "$new_xkeen" = 1 ]; then
 [ ! -L "$xkeen" ] && [ ! -e "$xkeen" ] || fail 'refusing an existing missing-XKeen path'
fi
if [ "$new_config" = 1 ]; then
 [ "$config_dir" = /opt/etc/xray/configs ] && [ "$asset_dir" = /opt/etc/xray/dat ] || fail 'fresh generated Xray requires standard config and asset paths'
 [ ! -L "$config_dir" ] || fail 'refusing a symlinked fresh Xray config directory'
 jq -e '.xray.managed_dir == "/opt/etc/xray/configs" and .xray.base_routing_file == "/opt/etc/xray/configs/05_routing.json" and .xray.route.inbound_tags == ["redirect","tproxy"] and .xray.route.replace_outbound_tags == ["krm-bootstrap"]' "$STAGE/config.json" >/dev/null || fail 'prepared config does not select the generated krm-bootstrap route; use the fresh wizard'
fi
if [ "$new_xkeen" = 1 ]; then
 for path in /opt/sbin/.xkeen /opt/sbin/_xkeen /opt/etc/init.d/S05xkeen /opt/etc/init.d/S24xray /opt/etc/init.d/S99xkeen /opt/etc/init.d/S99xkeenstart /opt/etc/ndm/netfilter.d/proxy.sh /opt/etc/ndm/schedule.d/00-xkeen-hotspot-sync.sh; do
  [ ! -e "$path" ] && [ ! -L "$path" ] || fail 'partial XKeen installation or existing interception detected; preserve it and repair explicitly'
 done
 if pidof xray mihomo >/dev/null 2>&1; then fail 'an independently running proxy exists; refusing fresh XKeen installation'; fi
 require_xkeen_consent
fi
command -v timeout >/dev/null 2>&1 || ensure_packages coreutils-timeout
command -v timeout >/dev/null 2>&1 || fail 'bounded validation requires Entware coreutils-timeout'
DEPS=$ROOT/dist/router-deps-linux-$ARCH.tar.gz
[ -f "$DEPS" ] && [ ! -L "$DEPS" ] || fail 'signed native router dependency archive missing; use the complete new release'
tar -tzf "$DEPS" > "$STAGE/names" || fail 'cannot list signed router dependencies'
if awk 'BEGIN {bad=0} {n=$0; sub(/^\.\//,"",n); if (n == "." || n == "") next; if (n ~ /^\// || n ~ /(^|\/)\.\.(\/|$)/ || n ~ /\\/) bad=1; if (n !~ /^(sbin\/|etc\/|share\/)/ && n !~ /^(sbin|etc|share)\/?$/) bad=1} END {exit bad}' "$STAGE/names"; then :; else fail 'unsafe router dependency paths'; fi
tar -tvzf "$DEPS" > "$STAGE/entries" || fail 'cannot inspect router dependency entries'
if awk 'BEGIN {bad=0} !/^[d-]/ {bad=1} END {exit bad}' "$STAGE/entries"; then :; else fail 'unsafe router dependency entry types'; fi
bytes=$(awk '/^-/ {if ($3 !~ /^[0-9]+$/) exit 1; total+=$3} END {printf "%.0f\n",total}' "$STAGE/entries") || fail 'cannot determine router dependency size'
[ "$bytes" -gt 0 ] && [ "$bytes" -le 209715200 ] || fail 'router dependencies exceed extraction limit'
for filesystem in /opt "$STAGE"; do
 free=$(df -Pk "$filesystem" | awk 'END {print $4}')
 case "$free" in ''|*[!0-9]*) fail 'cannot determine stack staging space';; esac
 [ "$free" -ge "$(((bytes + 1023) / 1024 + 32768))" ] || fail 'insufficient space for router dependencies and reserve; choose TMPDIR on Entware storage'
done
mkdir "$STAGE/payload"
tar -xzf "$DEPS" -C "$STAGE/payload"
if [ "$new_xkeen" = 1 ]; then
 [ -f "$STAGE/payload/sbin/xkeen" ] && [ -f "$STAGE/payload/sbin/.xkeen/import.sh" ] && [ -f "$STAGE/payload/etc/init.d/S05xkeen" ] || fail 'incomplete signed XKeen runtime'
 ensure_packages curl jq ip-full iptables ipset ca-bundle coreutils-uname coreutils-nohup conntrack
fi
candidate_binary=$binary
if [ "$new_xray" = 1 ]; then
 candidate_binary=$STAGE/payload/sbin/xray
 [ -f "$candidate_binary" ] || fail 'incomplete signed Xray runtime'
 chmod 0755 "$candidate_binary"
 timeout -k 2 30 "$candidate_binary" version > "$STAGE/version.log" 2>&1 || fail 'signed Xray cannot execute on this native CPU/ABI within the validation timeout'
fi
candidate_config=$config_dir
if [ "$new_config" = 1 ]; then
 candidate_config=$STAGE/configs
 "$CTL" init-xray --output-dir "$candidate_config" >/dev/null
fi
candidate_assets=$asset_dir
if [ "$new_config" = 1 ]; then
 if [ -e "$asset_dir/geosite_v2fly.dat" ] || [ -L "$asset_dir/geosite_v2fly.dat" ]; then
  [ -f "$asset_dir/geosite_v2fly.dat" ] || fail 'existing GeoSite is not a regular file'
 else
  candidate_assets=$STAGE/payload/etc/xray/dat
 fi
fi
# Bound the same regular strict-JSON input set accepted by the later preflight.
# In particular, never open a pre-existing FIFO while counting or parsing it.
json_count=0; json_bytes=0
for file in "$candidate_config"/*.json; do
 [ -e "$file" ] || [ -L "$file" ] || continue
 [ ! -d "$file" ] || continue
 [ -f "$file" ] || fail 'Xray configuration contains a nonregular JSON file'
 json_count=$((json_count + 1))
 [ "$json_count" -le 128 ] || fail 'Xray configuration has more than 128 JSON files'
 size=$(wc -c < "$file" | tr -d '[:space:]')
 [ "$size" -le 4194304 ] || fail 'Xray JSON file exceeds 4 MiB'
 json_bytes=$((json_bytes + size))
 [ "$json_bytes" -le 16777216 ] || fail 'Xray JSON configuration exceeds 16 MiB'
 timeout -k 2 30 jq -e -s 'length == 1 and (.[0] | type == "object")' "$file" >/dev/null 2>&1 || fail 'Xray configuration must be one strict JSON object per file'
done
[ "$json_count" -gt 0 ] || fail 'Xray configuration has no JSON files'
XRAY_LOCATION_ASSET="$candidate_assets" timeout -k 2 30 "$candidate_binary" run -test -confdir "$candidate_config" > "$STAGE/xray-validation.log" 2>&1 || fail 'staged Xray configuration validation failed or timed out; no stack files installed'
# Stage on the destination filesystem, then publish complete files exclusively.
# Keep temporary hard-link sources until cleanup so any installation failure can
# remove only the exact files this invocation created.
publish_file(){
 publish_source=$1; publish_target=$2; publish_mode=$3
 publish_temporary=$(mktemp "$(dirname -- "$publish_target")/.krm-stack.XXXXXX") || return 1
 printf '%s\n' "$publish_temporary" >> "$STAGE/temporary-files" || return 1
 cp "$publish_source" "$publish_temporary" || return 1
 chmod "$publish_mode" "$publish_temporary" || return 1
 printf '%s %s\n' "$publish_temporary" "$publish_target" >> "$STAGE/publications" || return 1
 ln "$publish_temporary" "$publish_target" || return 1
}
# All executable/configuration checks have succeeded. Install only missing files;
# existing JSON, GeoSite data, XKeen options, firewall hooks and schedulers stay.
mkdir -p /opt/sbin /opt/etc/init.d
if [ "$new_xray" = 1 ]; then
 publish_file "$candidate_binary" "$binary" 0755 || fail 'cannot publish new Xray without replacing an existing path'
fi
if [ "$new_xkeen" = 1 ]; then
 mkdir /opt/sbin/.xkeen
 exec 4< /opt/sbin/.xkeen
 new_xkeen_tree=1
 cp -R "$STAGE/payload/sbin/.xkeen/." /opt/sbin/.xkeen/
 publish_file "$STAGE/payload/sbin/xkeen" "$xkeen" 0755 || fail 'cannot publish new XKeen without replacing an existing path'
 publish_file "$STAGE/payload/etc/init.d/S05xkeen" /opt/etc/init.d/S05xkeen 0755 || fail 'cannot publish new XKeen init script without replacing an existing path'
 mkdir -p /opt/etc/ndm/netfilter.d /opt/var/log/xray
 if [ ! -d /opt/etc/xkeen ]; then mkdir /opt/etc/xkeen; chmod 0700 /opt/etc/xkeen; fi
 for name in xkeen.json port_proxying.lst port_exclude.lst ip_exclude.lst; do
  if [ ! -e "/opt/etc/xkeen/$name" ] && [ ! -L "/opt/etc/xkeen/$name" ]; then
   if [ "$name" = xkeen.json ]; then printf '{}\n'; else printf '# Managed initial empty list; add exclusions explicitly.\n'; fi > "$STAGE/$name"
   publish_file "$STAGE/$name" "/opt/etc/xkeen/$name" 0600 || fail 'cannot publish new XKeen options without replacing an existing path'
  fi
 done
fi
if [ "$new_config" = 1 ]; then
 mkdir -p "$asset_dir" "$(dirname -- "$config_dir")"
 if [ ! -e "$config_dir" ] && [ ! -L "$config_dir" ]; then
  mkdir "$config_dir"
  new_config_dir=1
  exec 3< "$config_dir"
  chmod 0700 "$config_dir"
 fi
 [ -d "$config_dir" ] && [ ! -L "$config_dir" ] || fail 'fresh Xray config directory changed during installation'
 if [ ! -e "$asset_dir/geosite_v2fly.dat" ] && [ ! -L "$asset_dir/geosite_v2fly.dat" ]; then
  publish_file "$STAGE/payload/etc/xray/dat/geosite_v2fly.dat" "$asset_dir/geosite_v2fly.dat" 0600 || fail 'cannot publish GeoSite without replacing an existing path'
 fi
 for file in "$candidate_config"/*.json; do
  name=${file##*/}
  publish_file "$file" "$config_dir/$name" 0600 || fail 'cannot publish Xray configuration without replacing an existing path'
 done
fi
mkdir -p /opt/share/kee-route-manager/licenses
for license in "$STAGE/payload/share/kee-route-manager/licenses/"*.txt; do
 [ -f "$license" ] || continue
 target=/opt/share/kee-route-manager/licenses/${license##*/}
 if [ ! -e "$target" ] && [ ! -L "$target" ]; then
  publish_file "$license" "$target" 0644 || fail 'cannot publish dependency license without replacing an existing path'
 fi
done
printf 'Missing XKeen/Xray components installed; generated=%s. Runtime startup belongs to the KRM launcher.\n' "$new_config"
