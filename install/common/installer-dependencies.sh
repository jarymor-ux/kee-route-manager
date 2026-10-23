#!/bin/sh
# Signed interactive bootstrap integration. Planning is read-only; application
# requires prior wizard confirmation and separate native activation consent.
set -eu
umask 077
fail(){ echo "ERROR: $*" >&2; exit 1; }
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
prefix=${KRM_DEPENDENCY_PREFIX:-/opt}
[ "$#" -ge 2 ] || fail 'usage: installer-dependencies.sh plan STAGE | apply STAGE CONFIG CTL MODE'
action=$1; stage=$2
case "$stage" in /*);; *) fail 'stage must be absolute';; esac
[ -d "$stage" ] && [ ! -L "$stage" ] || fail 'private wizard staging directory is missing'
yes_answer(){ case "$1" in y|Y|yes|YES|д|Д|да|Да) return 0;; *) return 1;; esac; }
check_clean_ownership(){
 for path in "$prefix/etc/xray" "$prefix/etc/xkeen" "$prefix/etc/init.d/S05xkeen" "$prefix/etc/init.d/S24xray" "$prefix/etc/init.d/S99xkeen" "$prefix/etc/init.d/S99xkeenstart" "$prefix/etc/ndm/netfilter.d/proxy.sh" "$prefix/etc/ndm/schedule.d/00-xkeen-hotspot-sync.sh"; do
  [ ! -e "$path" ] && [ ! -L "$path" ] || fail 'existing native owner; refusing to publish missing executables or replace its configuration'
 done
 if command -v pidof >/dev/null 2>&1; then
  if pidof xray >/dev/null 2>&1 || pidof mihomo >/dev/null 2>&1; then fail 'live native proxy owner; inspect before provisioning'; fi
 fi
 if command -v crontab >/dev/null 2>&1; then
  crontab -l > "$stage/native-cron" 2>/dev/null || :
  if LC_ALL=C awk '!/^[[:space:]]*(#|$)/ && /[Xx][Kk]een|[Xx]ray|[Mm]ihomo/ {found=1} END {exit !found}' "$stage/native-cron"; then fail 'existing native scheduler; inspect before provisioning'; fi
 fi
}
case "$action" in
 plan)
  [ "$#" = 2 ] || fail 'unexpected planning argument'
  clean=0
  [ -e "$prefix/etc/xray" ] || [ -L "$prefix/etc/xray" ] || clean=1
  if [ -L "$prefix/etc/xray" ]; then
   case "$(readlink "$prefix/etc/xray")" in "$prefix/var/lib/kee-route-manager-native/"install-*/xray) clean=resume;; esac
  fi
  [ "$clean" != resume ] || sh "$ROOT/install/common/native-bootstrap.sh" "$stage/native" --verify-existing
  missing=0
  for kind in xray xkeen; do
   if [ ! -x "$prefix/sbin/$kind" ]; then
    [ ! -e "$prefix/sbin/$kind" ] && [ ! -L "$prefix/sbin/$kind" ] || fail 'occupied native executable path; inspect privately rather than overwrite'
    missing=1
   fi
  done
  [ "$missing" = 1 ] || [ "$clean" != 0 ] || exit 0
  if [ "$clean" = 1 ]; then
   check_clean_ownership
  elif [ "$missing" = 1 ]; then
   fail 'existing native installation has a missing executable; inspect and repair it explicitly rather than resurrect an unknown service'
  fi
  [ -x "$prefix/bin/opkg" ] || fail 'mounted Entware with opkg is required; install Entware manually'
  printf '%s\n' 'XKeen/Xray setup is incomplete. Provision missing pinned, SHA-256/ABI-verified components and prepare clean configuration if absent?' 'Настройка XKeen/Xray не завершена. Установить отсутствующие проверенные компоненты и подготовить чистую конфигурацию? [y/N; д/Н]'
  IFS= read -r answer || fail 'dependency approval input interrupted'
  if ! yes_answer "$answer"; then
   printf '%s\n' 'Provisioning skipped; supply existing working native paths in the wizard.'
   exit 0
  fi
  for tool in curl openssl flock timeout jq tar unzip ip iptables iptables-save iptables-restore ipset conntrack nohup pidof killall insmod logger sysctl; do command -v "$tool" >/dev/null 2>&1 || fail "required native setup tool unavailable: $tool (install from your trusted Entware package manager)"; done
  for kind in xray xkeen; do [ -x "$prefix/sbin/$kind" ] || : > "$stage/need-$kind"; done
  case "$clean" in 1) printf 'clean\n';; resume) printf 'resume\n';; *) printf 'adopt\n';; esac > "$stage/native-plan"
  ;;
 apply)
  [ "$#" = 5 ] || fail 'apply requires CONFIG CTL MODE'
  config=$3; ctl=$4; mode=$5
  [ -f "$stage/native-plan" ] && [ ! -L "$stage/native-plan" ] || exit 0
  plan=$(cat "$stage/native-plan")
  case "$plan" in clean|adopt|resume);; *) fail 'invalid native setup plan';; esac
  [ "$plan" != clean ] || check_clean_ownership
  [ "$plan" != resume ] || sh "$ROOT/install/common/native-bootstrap.sh" "$stage/native" --verify-existing
  # This intentionally cannot replace the full preflight after provisioning.
  "$ctl" preflight --resources-only --config "$config" --mode "$mode" --release-dir "$ROOT" --staging-dir "$stage" || fail 'native provisioning resources check failed'
  for kind in xray xkeen; do
   if [ -f "$stage/need-$kind" ]; then sh "$ROOT/install/common/dependency-provision.sh" "$kind" --approve; fi
  done
  if [ "$plan" = clean ] || [ "$plan" = resume ]; then
   if [ -n "${KRM_XKEEN_CONFIG_FILE:-}" ]; then
    [ -f "$KRM_XKEEN_CONFIG_FILE" ] && [ ! -L "$KRM_XKEEN_CONFIG_FILE" ] || fail 'KRM_XKEEN_CONFIG_FILE must be a private regular token-only JSON file'
    [ "$(wc -c < "$KRM_XKEEN_CONFIG_FILE" | tr -d '[:space:]')" -le 65536 ] || fail 'native token configuration exceeds 64 KiB'
    if [ "$plan" = clean ]; then
     cp "$KRM_XKEEN_CONFIG_FILE" "$stage/native/xkeen.json"
     chmod 0600 "$stage/native/xkeen.json"
    else
     cmp -s "$KRM_XKEEN_CONFIG_FILE" "$prefix/etc/xkeen/xkeen.json" || fail 'prepared native token differs; inspect and update the private native config explicitly before retrying'
    fi
   fi
   [ "$plan" != clean ] || sh "$ROOT/install/common/native-bootstrap.sh" "$stage/native" --approve
   "$ctl" native-policy-check --xkeen-config "$prefix/etc/xkeen/xkeen.json" || fail 'native files remain inactive; configure exactly one xkeen policy and authorized RCI access before installing KRM'
   approval=$prefix/etc/xkeen/krm-activation-approved
   if [ -e "$approval" ] || [ -L "$approval" ]; then
    [ -f "$approval" ] && [ ! -L "$approval" ] && [ "$(stat -c %a "$approval")" = 600 ] || fail 'native activation consent changed; inspect privately'
    printf 'approved\n' | cmp -s - "$approval" || fail 'native activation consent changed; inspect privately'
    exit 0
   fi
   printf '%s\n' 'Activate the prepared native service for the existing xkeen policy? This authorizes its interception/firewall hooks and guarded startup on reboot; existing DNS/WAN policies are not edited.' 'Активировать подготовленный сервис для существующей политики xkeen, включая его правила перехвата/firewall и защищённый запуск после перезагрузки? [y/N; д/Н]'
   IFS= read -r answer || fail 'activation approval input interrupted; native files remain inactive'
   yes_answer "$answer" || fail 'activation declined; native files remain inactive'
   marker=$(mktemp "$prefix/etc/xkeen/.krm-activation.XXXXXX")
   trap 'rm -f -- "$marker"' EXIT
   printf 'approved\n' > "$marker"
   chmod 0600 "$marker"
   sync || fail 'cannot persist activation consent'
   ln -T "$marker" "$approval" || fail 'activation consent path already exists; inspect rather than overwrite'
   sync || fail 'cannot persist native activation consent'
  fi
  ;;
 *) fail 'unsupported native setup action';;
esac
