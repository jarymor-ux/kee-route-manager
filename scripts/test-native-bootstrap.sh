#!/usr/bin/env bash
# Native setup filesystem integration only; never start a router service.
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
helper="$ROOT/install/common/native-bootstrap.sh"
[[ -f "$helper" ]] || { echo 'native bootstrap helper is missing' >&2; exit 1; }
# The real helper pins the upstream template. This private test copy substitutes
# a harmless fixture with the same configuration knobs, never downloaded code.
mkdir -p "$WORK/stage/configs" "$WORK/checkout/install/common" "$WORK/checkout/install/keenetic"
cp "$ROOT/install/keenetic/native-xkeen-service.sh" "$WORK/checkout/install/keenetic/"
printf '%s\n' '{"inbounds":[],"outbounds":[{"tag":"blocked","protocol":"blackhole"}]}' > "$WORK/stage/configs/03_inbounds.json"
printf '%s\n' '{"rci_token":"private-test-token-not-for-logs"}' > "$WORK/stage/xkeen.json"
cat > "$WORK/template" <<'SERVICE'
#!/bin/sh
start_auto="on"
proxy_dns="off"
proxy_router="off"
ipv6_support="on"
check_ipv6_active(){ return 0; }
get_ipver_support(){
    ip6_supported=$(check_ipv6_active && echo true || echo false)
}
name_policy="xkeen"
name_policy_full="xkeen_full"
dscp_enable="on"
dscp_proxy="63"
if [ "$dscp_enable" = "off" ]; then dscp_proxy=""; fi
get_policy_mark(){ printf '%s\n' "${NATIVE_TEST_NATIVE_MARK:-}"; }
_release_proxy_mutex(){ printf 'released\n' >> "$NATIVE_TEST_MUTEX_RELEASES"; }
proxy_start(){
        _ps_mutex_rc=0
        policy_mark=$(get_policy_mark "$name_policy")
        get_ipver_support
        printf '%s|%s\n' "$ipv6_support" "$ip6_supported" > "$NATIVE_TEST_IP6_SCOPE"
        printf '%s|%s\n' "$dscp_proxy" "$name_policy_full" > "$NATIVE_TEST_POLICY_SCOPE"
        printf 'synthetic service started\n' > "$NATIVE_TEST_ACTIVATED"
}
proxy_start
SERVICE
original_pin=62509ee96d186fad7bfc75aafeea14171f5fc1ed4a091527b755d5ba57f8c887
fixture_pin=$(openssl dgst -sha256 "$WORK/template" | sed 's/^.*= //')
sed "s/$original_pin/$fixture_pin/" "$helper" > "$WORK/checkout/install/common/native-bootstrap.sh"
helper="$WORK/checkout/install/common/native-bootstrap.sh"
new_prefix(){
 local prefix=$1
 mkdir -p "$prefix/etc/init.d" "$prefix/sbin/.xkeen/02_install/07_install_register" "$prefix/var/lib"
 cp "$WORK/template" "$prefix/sbin/.xkeen/02_install/07_install_register/04_register_init.sh"
 cat > "$prefix/sbin/xray" <<'XRAY'
#!/bin/sh
[ "$1 $2 $3" = 'run -test -confdir' ] || exit 1
[ -d "$4" ] || exit 1
printf 'native validation must be private: secret-test-subscription\n'
if [ "${NATIVE_TEST_LATE_CONFLICT:-}" = 1 ]; then
 mkdir -p "$KRM_DEPENDENCY_PREFIX/etc/xray"
 printf 'operator-owned\n' > "$KRM_DEPENDENCY_PREFIX/etc/xray/sentinel"
fi
[ "${NATIVE_TEST_INVALID:-}" != 1 ]
XRAY
 chmod 0700 "$prefix/sbin/xray"
}
run_helper(){ KRM_DEPENDENCY_PREFIX="$1" sh "$helper" "$WORK/stage" "${2:---approve}"; }
export NATIVE_TEST_ACTIVATED="$WORK/activated"
prefix="$WORK/entware"
new_prefix "$prefix"
run_helper "$prefix" > "$WORK/output"
[[ -f "$prefix/etc/xray/configs/03_inbounds.json" && -f "$prefix/etc/xkeen/xkeen.json" ]]
[[ -L "$prefix/etc/xray" && -L "$prefix/etc/xkeen" && -L "$prefix/etc/init.d/S05xkeen" ]]
grep -Fx 'start_auto="off"' "$prefix/etc/xkeen/native-service.sh" >/dev/null
cmp -s "$ROOT/install/keenetic/native-xkeen-service.sh" "$prefix/etc/init.d/S05xkeen"
[[ ! -e "$WORK/activated" && ! -e "$prefix/etc/xkeen/krm-activation-approved" ]]
! grep -qE 'private-test-token|secret-test-subscription' "$WORK/output"
[[ "$(stat -c %a "$prefix/etc/xkeen/xkeen.json")" = 600 ]]
# Retry validates exact native ownership without executing a service. Only a
# private token-only JSON update and exact consent receipt may differ.
run_helper "$prefix" --verify-existing >/dev/null
[[ ! -e "$WORK/activated" ]]
for change in foreign-service sibling-link missing-token missing-manifest raw-service forged-service-receipt forged-wrapper-receipt xray-config token-extension; do
 p="$WORK/verify-$change"
 new_prefix "$p"
 run_helper "$p" >/dev/null
 owned_store=$(readlink "$p/etc/xray"); owned_store=${owned_store%/xray}
 case "$change" in
  foreign-service) rm "$p/etc/init.d/S05xkeen"; printf 'operator-service\n' > "$p/etc/init.d/S05xkeen";;
  sibling-link) rm "$p/etc/xkeen"; ln -s "$WORK/stage" "$p/etc/xkeen";;
  missing-token) rm "$owned_store/.krm-native-token";;
  missing-manifest) rm "$owned_store/.krm-native-manifest";;
  raw-service) printf '# operator edit\n' >> "$owned_store/xkeen/native-service.sh";;
  forged-service-receipt|forged-wrapper-receipt)
   changed=./xkeen/native-service.sh
   [[ "$change" != forged-wrapper-receipt ]] || changed=./S05xkeen
   printf '# operator edit\n' >> "$owned_store/$changed"
   digest=$(openssl dgst -sha256 "$owned_store/$changed" | sed 's/^.*= //')
   awk -v changed="$changed" -v digest="$digest" '$4 == changed {$3 = digest} {print}' "$owned_store/.krm-native-manifest" > "$WORK/forged-manifest"
   cp "$WORK/forged-manifest" "$owned_store/.krm-native-manifest"
   ;;
  xray-config) printf '# operator edit\n' >> "$owned_store/xray/configs/03_inbounds.json";;
  token-extension) printf '{"xkeen":{"policy":[]}}\n' > "$owned_store/xkeen/xkeen.json";;
 esac
 if run_helper "$p" --verify-existing >/dev/null 2>&1; then echo "native retry adopted drift: $change" >&2; exit 1; fi
 [[ -L "$p/etc/xray" && -d "$owned_store" ]]
done
p="$WORK/verify-private-token"
new_prefix "$p"
run_helper "$p" >/dev/null
printf '{"rci_token":"updated-private-token"}\n' > "$p/etc/xkeen/xkeen.json"
printf 'approved\n' > "$p/etc/xkeen/krm-activation-approved"
chmod 0600 "$p/etc/xkeen/xkeen.json" "$p/etc/xkeen/krm-activation-approved"
run_helper "$p" --verify-existing >/dev/null
[[ ! -e "$WORK/activated" ]]
chmod 0644 "$p/etc/xkeen/xkeen.json"
if run_helper "$p" --verify-existing >/dev/null 2>&1; then echo 'public token update accepted' >&2; exit 1; fi
chmod 0600 "$p/etc/xkeen/xkeen.json"
printf 'foreign-consent\n' > "$p/etc/xkeen/krm-activation-approved"
if run_helper "$p" --verify-existing >/dev/null 2>&1; then echo 'changed consent accepted during retry' >&2; exit 1; fi
# Even a valid retry-only token/consent change never grants destructive recovery.
printf 'approved\n' > "$p/etc/xkeen/krm-activation-approved"
owned_store=$(readlink "$p/etc/xray"); owned_store=${owned_store%/xray}
verify_base="$p/var/lib/kee-route-manager-native"
mkdir -m 0700 "$verify_base/.pending"
cp "$owned_store/.krm-native-token" "$verify_base/.pending/token"
cp "$owned_store/.krm-native-manifest" "$verify_base/.pending/manifest"
rm "$p/etc/init.d/S05xkeen"
if run_helper "$p" --recover >/dev/null 2>&1; then echo 'activated retry baseline authorized destructive recovery' >&2; exit 1; fi
[[ -L "$p/etc/xray" && -d "$owned_store" && -d "$verify_base/.pending" ]]

# Execute only the synthetic raw fixture (it contains no firewall or RCI
# implementation) to prove the downstream guard blocks a late policy loss.
export NATIVE_TEST_MUTEX_RELEASES="$WORK/mutex-releases"
export NATIVE_TEST_POLICY_SCOPE="$WORK/policy-scope"
export NATIVE_TEST_IP6_SCOPE="$WORK/ipv6-scope"
for mark in '' 0x0 0x00000000 invalid 0xzz 0x123456789 $'0x1\n0x2'; do
 if NATIVE_TEST_NATIVE_MARK="$mark" sh "$prefix/etc/xkeen/native-service.sh" > "$WORK/raw-output" 2>&1; then
  echo 'raw service policy-loss guard allowed activation' >&2; exit 1
 fi
 [[ ! -e "$WORK/activated" ]]
done
[[ "$(wc -l < "$NATIVE_TEST_MUTEX_RELEASES" | tr -d '[:space:]')" = 7 ]]
NATIVE_TEST_NATIVE_MARK=0x1234 sh "$prefix/etc/xkeen/native-service.sh"
[[ -e "$WORK/activated" ]]
[[ "$(cat "$NATIVE_TEST_IP6_SCOPE")" = 'on|false' ]] || { echo 'IPv6 interception was enabled or kernel IPv6 was disabled' >&2; exit 1; }
[[ "$(cat "$NATIVE_TEST_POLICY_SCOPE")" = '|' ]] || { echo 'native service intercepted DSCP or another policy' >&2; exit 1; }
rm "$WORK/activated"
if run_helper "$prefix" > /dev/null 2>&1; then echo 'existing native setup was overwritten' >&2; exit 1; fi
for conflict in etc/xray etc/xkeen etc/init.d/S05xkeen etc/init.d/S24xray etc/init.d/S99xkeen etc/init.d/S99xkeenstart etc/ndm/netfilter.d/proxy.sh etc/ndm/schedule.d/00-xkeen-hotspot-sync.sh; do
 p="$WORK/conflict-${conflict//\//_}"
 new_prefix "$p"
 mkdir -p "$(dirname "$p/$conflict")"
 ln -s /missing-operator-path "$p/$conflict"
 if run_helper "$p" >/dev/null 2>&1; then echo "collision accepted: $conflict" >&2; exit 1; fi
 [[ "$(readlink "$p/$conflict")" = /missing-operator-path ]]
 [[ ! -e "$p/etc/xray/configs" ]]
done
p="$WORK/invalid"
new_prefix "$p"
if NATIVE_TEST_INVALID=1 run_helper "$p" > "$WORK/invalid-output" 2>&1; then echo 'invalid Xray config installed' >&2; exit 1; fi
[[ ! -e "$p/etc/xray" && ! -L "$p/etc/xray" && ! -e "$p/etc/init.d/S05xkeen" ]]
! grep -q 'secret-test-subscription' "$WORK/invalid-output"
run_helper "$p" --recover >/dev/null
[[ ! -e "$p/var/lib/kee-route-manager-native/.pending" ]]
p="$WORK/hidden-config"
new_prefix "$p"
printf '{}\n' > "$WORK/stage/configs/.hidden.json"
if run_helper "$p" >/dev/null 2>&1; then echo 'hidden config silently omitted' >&2; exit 1; fi
[[ ! -e "$p/etc/xray" ]]
rm "$WORK/stage/configs/.hidden.json"
p="$WORK/tampered-template"
new_prefix "$p"
printf '# tampered\n' >> "$p/sbin/.xkeen/02_install/07_install_register/04_register_init.sh"
if run_helper "$p" >/dev/null 2>&1; then echo 'unverified service installed' >&2; exit 1; fi
[[ ! -e "$p/etc/xray" ]]
p="$WORK/late-directory"
new_prefix "$p"
if NATIVE_TEST_LATE_CONFLICT=1 run_helper "$p" >/dev/null 2>&1; then echo 'late directory collision accepted' >&2; exit 1; fi
[[ "$(cat "$p/etc/xray/sentinel")" = operator-owned ]]
[[ ! -L "$p/etc/xray" && ! -e "$p/etc/xkeen" && ! -e "$p/etc/init.d/S05xkeen" ]]
# A syntactically valid JSON extension must not enable arbitrary upstream
# configuration knobs; native setup accepts only the explicit RCI token.
cp "$WORK/stage/xkeen.json" "$WORK/token-backup"
for invalid in '{"xkeen":{"killswitch":"off"}}' '{"rci_token":false}' '{} {}' '{"rci_token":"bad\\token"}'; do
 printf '%s\n' "$invalid" > "$WORK/stage/xkeen.json"
 p="$WORK/invalid-token-${RANDOM}"
 new_prefix "$p"
 if run_helper "$p" >/dev/null 2>&1; then echo 'unapproved XKeen JSON accepted' >&2; exit 1; fi
 [[ ! -e "$p/etc/xray" ]]
done
mv "$WORK/token-backup" "$WORK/stage/xkeen.json"
# Rebuild a pending journal from the immutable initial receipt, simulating a
# crash after all links were published but before commit cleanup.
base="$prefix/var/lib/kee-route-manager-native"
store=$(readlink "$prefix/etc/xray")
store=${store%/xray}
mkdir -m 0700 "$base/.pending"
cp "$store/.krm-native-token" "$base/.pending/token"
cp "$store/.krm-native-manifest" "$base/.pending/manifest"
run_helper "$prefix" --recover >/dev/null
[[ ! -e "$base/.pending" && -f "$prefix/etc/xray/configs/03_inbounds.json" ]]
# Partial publication rolls back only owned content.
mkdir -m 0700 "$base/.pending"
cp "$store/.krm-native-token" "$base/.pending/token"
cp "$store/.krm-native-manifest" "$base/.pending/manifest"
rm "$prefix/etc/init.d/S05xkeen"
run_helper "$prefix" --recover >/dev/null
[[ ! -e "$base/.pending" && ! -e "$store" && ! -L "$prefix/etc/xray" && ! -L "$prefix/etc/xkeen" ]]
# Foreign replacements make recovery refuse before touching any owned link.
p="$WORK/foreign-recovery"
new_prefix "$p"
run_helper "$p" >/dev/null
foreign_base="$p/var/lib/kee-route-manager-native"
foreign_store=$(readlink "$p/etc/xray"); foreign_store=${foreign_store%/xray}
mkdir -m 0700 "$foreign_base/.pending"
cp "$foreign_store/.krm-native-token" "$foreign_base/.pending/token"
cp "$foreign_store/.krm-native-manifest" "$foreign_base/.pending/manifest"
rm "$p/etc/init.d/S05xkeen"
ln -s /operator-native-service "$p/etc/init.d/S05xkeen"
if run_helper "$p" --recover >/dev/null 2>&1; then echo 'foreign recovery entrypoint accepted' >&2; exit 1; fi
[[ "$(readlink "$p/etc/init.d/S05xkeen")" = /operator-native-service ]]
[[ -L "$p/etc/xray" && -L "$p/etc/xkeen" && -e "$foreign_base/.pending" ]]
# Modified native content and foreign replacements are preserved together
# with the journal, including on partial publication recovery.
run_helper "$prefix" >/dev/null
store=$(readlink "$prefix/etc/xray"); store=${store%/xray}
mkdir -m 0700 "$base/.pending"
cp "$store/.krm-native-token" "$base/.pending/token"
cp "$store/.krm-native-manifest" "$base/.pending/manifest"
printf '# operator edit\n' >> "$store/xray/configs/03_inbounds.json"
rm "$prefix/etc/init.d/S05xkeen"
if run_helper "$prefix" --recover >/dev/null 2>&1; then echo 'modified native tree deleted' >&2; exit 1; fi
[[ -e "$base/.pending" && -f "$store/xray/configs/03_inbounds.json" && -L "$prefix/etc/xray" ]]
# Exercise the wrapper itself using private substituted paths. The production
# entrypoint keeps literal /opt paths; no developer-host paths are touched.
fixture="$WORK/wrapper-runtime"
mkdir -p "$fixture/opt/bin" "$fixture/opt/etc/xkeen"
sed "s|/opt/|$fixture/opt/|g" "$ROOT/install/keenetic/native-xkeen-service.sh" > "$fixture/wrapper"
cat > "$fixture/opt/etc/xkeen/native-service.sh" <<'RAW'
#!/bin/sh
printf '%s\n' "$*" >> "$NATIVE_TEST_CALLS"
RAW
cat > "$fixture/opt/bin/kee-route-managerctl" <<'GUARD'
#!/bin/sh
[ "$*" = native-policy-check ] || exit 2
printf '%s\n' checked >> "$NATIVE_TEST_GUARD_CALLS"
[ "${NATIVE_TEST_POLICY_DENIED:-}" != 1 ]
GUARD
chmod 0700 "$fixture/opt/etc/xkeen/native-service.sh" "$fixture/opt/bin/kee-route-managerctl"
export NATIVE_TEST_CALLS="$WORK/native-calls" NATIVE_TEST_GUARD_CALLS="$WORK/guard-calls"
if sh "$fixture/wrapper" start >/dev/null 2>&1; then echo 'unapproved native activation accepted' >&2; exit 1; fi
[[ ! -e "$NATIVE_TEST_CALLS" && ! -e "$NATIVE_TEST_GUARD_CALLS" ]]
printf 'approved\n' > "$fixture/opt/etc/xkeen/krm-activation-approved"
chmod 0600 "$fixture/opt/etc/xkeen/krm-activation-approved"
if NATIVE_TEST_POLICY_DENIED=1 sh "$fixture/wrapper" start >/dev/null 2>&1; then echo 'native policy failure ignored' >&2; exit 1; fi
[[ ! -e "$NATIVE_TEST_CALLS" ]]
sh "$fixture/wrapper" start
sh "$fixture/wrapper" restart
sh "$fixture/wrapper" stop
sh "$fixture/wrapper" status
printf 'start on\nrestart on\nstop\nstatus\n' > "$WORK/expected-calls"
cmp -s "$WORK/expected-calls" "$NATIVE_TEST_CALLS"
for command in 'start on' cold_start '-install'; do
 # The space case deliberately passes multiple arguments.
 read -r -a arguments <<< "$command"
 if sh "$fixture/wrapper" "${arguments[@]}" >/dev/null 2>&1; then echo 'unexpected native wrapper command accepted' >&2; exit 1; fi
done
# Broad permissions, symlink approval or extra receipt lines cannot authorize
# activation. Stop/status remain available without the ctl or receipt.
chmod 0644 "$fixture/opt/etc/xkeen/krm-activation-approved"
if sh "$fixture/wrapper" start >/dev/null 2>&1; then echo 'public activation receipt accepted' >&2; exit 1; fi
rm "$fixture/opt/etc/xkeen/krm-activation-approved"
printf 'approved\napproved\n' > "$fixture/approval"
chmod 0600 "$fixture/approval"
ln -s "$fixture/approval" "$fixture/opt/etc/xkeen/krm-activation-approved"
if sh "$fixture/wrapper" start >/dev/null 2>&1; then echo 'linked activation receipt accepted' >&2; exit 1; fi
rm "$fixture/opt/etc/xkeen/krm-activation-approved"
cp "$fixture/approval" "$fixture/opt/etc/xkeen/krm-activation-approved"
if sh "$fixture/wrapper" start >/dev/null 2>&1; then echo 'ambiguous activation receipt accepted' >&2; exit 1; fi
rm "$fixture/opt/etc/xkeen/krm-activation-approved" "$fixture/opt/bin/kee-route-managerctl"
sh "$fixture/wrapper" stop
sh "$fixture/wrapper" status
printf '%s\n' 'Isolated disabled native bootstrap, guarded service and recovery verified.'
