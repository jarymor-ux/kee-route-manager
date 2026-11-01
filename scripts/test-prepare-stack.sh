#!/usr/bin/env bash
# Fresh dependency installation is tested only in a disposable Linux container.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run only in a disposable Docker container' >&2; exit 1; }
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
case "$(uname -m)" in aarch64) arch=arm64;; x86_64) arch=amd64;; *) exit 1;; esac
mkdir -p "$WORK/source/install/keenetic" "$WORK/source/install/common" "$WORK/source/dist" "$WORK/payload/sbin/.xkeen" "$WORK/payload/etc/init.d" "$WORK/payload/etc/xray/dat" "$WORK/payload/share/kee-route-manager/licenses" /opt/bin /opt/sbin
cp "$ROOT/install/common/architecture.sh" "$WORK/source/install/common/"
cp "$ROOT/install/keenetic/prepare-stack.sh" "$WORK/source/install/keenetic/"
go build -o "$WORK/source/dist/kee-route-managerctl-linux-$arch" ./cmd/kee-route-managerctl
sed -e 's@/opt/share/xray@/opt/etc/xray/dat@' -e 's/REPLACE_WITH_SELECTED_OUTBOUND/krm-bootstrap/' "$ROOT/configs/keenetic.yaml" > "$WORK/config.yaml"
cat > /opt/bin/opkg <<'OPKG'
#!/bin/sh
[ "$1" = status ] && { printf 'Status: install ok installed\n'; exit 0; }
exit 1
OPKG
printf '#!/bin/sh\nexit 0\n' > /opt/bin/ndmc
cat > "$WORK/payload/sbin/xray" <<'XRAY'
#!/bin/sh
case "$1" in
 version) exit 0;;
 run) [ "$2 $3" = '-test -confdir' ] || exit 1
      if [ -f "$XRAY_LOCATION_ASSET/geosite_v2fly.dat" ] && grep -q reject-existing "$XRAY_LOCATION_ASSET/geosite_v2fly.dat"; then exit 1; fi
      for f in "$4"/*.json; do jq -e 'type == "object"' "$f" >/dev/null || exit 1; done;;
 *) exit 1;;
esac
XRAY
printf '#!/bin/sh\necho unexpected-runtime-command >&2\nexit 1\n' > "$WORK/payload/sbin/xkeen"
printf '# fixture module\n' > "$WORK/payload/sbin/.xkeen/import.sh"
printf '#!/bin/sh\nstart_auto="off"\n' > "$WORK/payload/etc/init.d/S05xkeen"
printf 'synthetic geosite\n' > "$WORK/payload/etc/xray/dat/geosite_v2fly.dat"
printf 'synthetic license\n' > "$WORK/payload/share/kee-route-manager/licenses/XKeen.txt"
chmod 0755 /opt/bin/opkg /opt/bin/ndmc "$WORK/payload/sbin/"{xray,xkeen}
tar -czf "$WORK/source/dist/router-deps-linux-$arch.tar.gz" -C "$WORK/payload" .
export KRM_CONFIG_FILE="$WORK/config.yaml" KRM_MODE=core
export PATH="/opt/bin:/opt/sbin:$PATH"
prepare(){ sh "$WORK/source/install/keenetic/prepare-stack.sh"; }
reset_stack(){ rm -rf /opt/sbin /opt/etc /opt/var /opt/share; mkdir -p /opt/sbin; }
# Installing interception needs explicit consent even when no terminal is present.
unset KRM_ALLOW_XKEEN_FALLBACK_ALL
if prepare </dev/null; then echo 'new XKeen installed without fallback-all consent' >&2; exit 1; fi
[[ ! -e /opt/sbin/xray && ! -e /opt/sbin/xkeen && ! -e /opt/sbin/.xkeen ]]
export KRM_ALLOW_XKEEN_FALLBACK_ALL=1
# Predictable old staging names must never be followed or overwritten.
mkdir -p /opt/etc/xray/dat
printf 'operator binary sentinel\n' > "$WORK/binary-sentinel"
printf 'operator asset sentinel\n' > "$WORK/asset-sentinel"
ln -s "$WORK/binary-sentinel" /opt/sbin/.krm-xray-new
printf 'operator XKeen sentinel\n' > /opt/sbin/.krm-xkeen-new
ln -s "$WORK/asset-sentinel" /opt/etc/xray/dat/.krm-geosite-new
prepare
[[ "$(cat "$WORK/binary-sentinel")" = 'operator binary sentinel' && -L /opt/sbin/.krm-xray-new ]]
[[ "$(cat /opt/sbin/.krm-xkeen-new)" = 'operator XKeen sentinel' ]]
[[ "$(cat "$WORK/asset-sentinel")" = 'operator asset sentinel' && -L /opt/etc/xray/dat/.krm-geosite-new ]]
[[ -x /opt/sbin/xray && -x /opt/sbin/xkeen && -s /opt/sbin/.xkeen/import.sh ]]
[[ -d /opt/etc/ndm/netfilter.d ]]
[[ -s /opt/etc/xray/dat/geosite_v2fly.dat && -s /opt/etc/xkeen/xkeen.json ]]
jq -e '.routing.rules[0].outboundTag == "direct" and (.routing.rules[0].domain | index("domain:ru")) != null and .routing.rules[1].outboundTag == "krm-bootstrap"' /opt/etc/xray/configs/05_routing.json >/dev/null
before=$(sha256sum /opt/etc/xray/configs/*.json /opt/sbin/xkeen)
prepare
[[ "$before" = "$(sha256sum /opt/etc/xray/configs/*.json /opt/sbin/xkeen)" ]]
rm /opt/sbin/xray
prepare
[[ -x /opt/sbin/xray && "$before" = "$(sha256sum /opt/etc/xray/configs/*.json /opt/sbin/xkeen)" ]]
rm /opt/sbin/xkeen
if prepare; then echo 'ambiguous partial XKeen install accepted' >&2; exit 1; fi
[[ ! -e /opt/sbin/xkeen && "$before" = "$(sha256sum /opt/etc/xray/configs/*.json "$WORK/payload/sbin/xkeen")" ]] || {
 # Check the routing files independently: the checksum label of the fixture binary differs.
 [[ "${before%$'\n'*}" = "$(sha256sum /opt/etc/xray/configs/*.json)" ]]
}

# Upstream legacy migration paths must never be mistaken for a fresh stack.
for retained in /opt/sbin/_xkeen /opt/etc/init.d/S24xray /opt/etc/init.d/S99xkeen /opt/etc/init.d/S99xkeenstart; do
 for kind in contents dangling-symlink; do
  reset_stack
  mkdir -p "$(dirname "$retained")"
  if [[ "$kind" = dangling-symlink ]]; then
   ln -s "$WORK/missing-legacy-target" "$retained"
  elif [[ "$retained" = /opt/sbin/_xkeen ]]; then
   mkdir "$retained"
   printf 'operator legacy module\n' > "$retained/import.sh"
  else
   printf 'operator legacy service\n' > "$retained"
  fi
  identity=$(stat -c '%d:%i:%a' "$retained")
  if prepare; then echo "retained legacy path accepted: $retained ($kind)" >&2; exit 1; fi
  [[ "$(stat -c '%d:%i:%a' "$retained")" = "$identity" ]]
  if [[ "$kind" = dangling-symlink ]]; then
   [[ "$(readlink "$retained")" = "$WORK/missing-legacy-target" ]]
  elif [[ "$retained" = /opt/sbin/_xkeen ]]; then
   [[ "$(cat "$retained/import.sh")" = 'operator legacy module' ]]
  else
   [[ "$(cat "$retained")" = 'operator legacy service' ]]
  fi
  [[ ! -e /opt/sbin/xray && ! -e /opt/sbin/xkeen && ! -e /opt/sbin/.xkeen && ! -e /opt/etc/xray/configs ]]
 done
done

# A module-copy failure after publishing Xray rolls back the entire owned stack.
reset_stack
mkdir "$WORK/fail-copy-bin"
cat > "$WORK/fail-copy-bin/cp" <<'COPY'
#!/bin/sh
if [ "$1" = -R ] && [ "$3" = /opt/sbin/.xkeen/ ]; then
 printf 'partial module copy\n' > /opt/sbin/.xkeen/import.sh
 printf 'injected\n' > "$KRM_TEST_MODULE_COPY_MARKER"
 exit 1
fi
exec /bin/cp "$@"
COPY
chmod 0755 "$WORK/fail-copy-bin/cp"
if PATH="$WORK/fail-copy-bin:$PATH" KRM_TEST_MODULE_COPY_MARKER="$WORK/copy-failed" prepare; then
 echo 'partial XKeen module copy accepted' >&2; exit 1
fi
[[ -s "$WORK/copy-failed" && ! -e /opt/sbin/xray && ! -e /opt/sbin/xkeen && ! -e /opt/sbin/.xkeen ]]
[[ -z "$(find /opt -name '.krm-stack.*')" ]]

# Test the actual preserved asset, rather than the bundled replacement candidate.
reset_stack
mkdir -p /opt/etc/xray/dat
printf 'reject-existing geosite\n' > /opt/etc/xray/dat/geosite_v2fly.dat
if prepare; then echo 'invalid preserved GeoSite was not validated' >&2; exit 1; fi
[[ ! -e /opt/sbin/xray && ! -e /opt/sbin/xkeen && ! -e /opt/etc/xray/configs ]]
[[ "$(cat /opt/etc/xray/dat/geosite_v2fly.dat)" = 'reject-existing geosite' ]]

# Existing executable symlinks are preserved when only the other binary is new.
reset_stack
ln -s "$WORK/payload/sbin/xray" /opt/sbin/xray
prepare
[[ -L /opt/sbin/xray && "$(readlink /opt/sbin/xray)" = "$WORK/payload/sbin/xray" && -x /opt/sbin/xkeen ]]

# A failure on the third file must roll back the first two complete files,
# preserving both an existing empty directory and unrelated operator content.
mkdir "$WORK/fail-bin"
cat > "$WORK/fail-bin/ln" <<'LINK'
#!/bin/sh
for target do :; done
case "$target" in /opt/etc/xray/configs/03_inbounds.json) exit 1;; esac
exec /bin/ln "$@"
LINK
chmod 0755 "$WORK/fail-bin/ln"
for directory in empty operator-content; do
 reset_stack
 mkdir -p /opt/etc/xray/configs
 chmod 0755 /opt/etc/xray/configs
 identity=$(stat -c '%d:%i:%a' /opt/etc/xray/configs)
 if [[ "$directory" = operator-content ]]; then printf 'operator file\n' > /opt/etc/xray/configs/README; fi
 if PATH="$WORK/fail-bin:$PATH" prepare; then echo 'simulated config publication failure accepted' >&2; exit 1; fi
 [[ "$(stat -c '%d:%i:%a' /opt/etc/xray/configs)" = "$identity" ]]
 [[ -z "$(find /opt/etc/xray/configs -name '*.json' -o -name '.krm-*')" ]]
 if [[ "$directory" = operator-content ]]; then [[ "$(cat /opt/etc/xray/configs/README)" = 'operator file' ]]; fi
done

# A FIFO must be rejected before either the installer or Xray tries to read it.
reset_stack
mkdir -p /opt/etc/xray/configs
mkfifo /opt/etc/xray/configs/01_log.json
set +e
timeout -k 1 5 sh "$WORK/source/install/keenetic/prepare-stack.sh" > "$WORK/fifo.log" 2>&1
result=$?
set -e
[[ "$result" != 0 && "$result" != 124 && "$result" != 137 && -p /opt/etc/xray/configs/01_log.json ]]
[[ ! -e /opt/sbin/xray && ! -e /opt/sbin/xkeen ]]
printf 'Fresh stack consent, legacy refusal, dependency rollback, safe staging, exact assets, transactional configs, symlinks and FIFO refusal passed.\n'
