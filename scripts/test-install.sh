#!/usr/bin/env bash
# Real installer filesystem lifecycle in a disposable Linux container; never host.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run only in a disposable Docker container' >&2; exit 1; }
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WORK="$(mktemp -d)"
cleanup(){
 for pidfile in /tmp/krm-test-service-*.pid; do
  [[ ! -s "$pidfile" ]] || kill "$(cat "$pidfile")" 2>/dev/null || true
 done
 rm -rf "$WORK"
}
trap cleanup EXIT
mkdir -p "$WORK/source" "$WORK/fake" "$WORK/private"
tar -C "$ROOT" --exclude=.git --exclude=.omx --exclude=release --exclude=dist -cf - . | tar -xf - -C "$WORK/source"
cd "$WORK/source"
case "$(uname -m)" in aarch64) ARCH=arm64;; x86_64) ARCH=amd64;; *) exit 1;; esac
VERSION=$(tr -d '[:space:]' < VERSION)
mkdir dist
for name in kee-route-managerd kee-route-managerctl kee-route-manager-ui kee-route-manager-launcher; do
 go build -ldflags "-X main.version=$VERSION" -o "dist/$name-linux-$ARCH" "./cmd/$name"
done
go build -o "$WORK/tool" ./cmd/krm-release-tool
"$WORK/tool" keygen --public "$WORK/public" --private "$WORK/private-key"
# The configless bootstrap integration uses a disposable release trust root.
# Rebuild ctl with that key embedded so the real wizard-generated config can
# authenticate the same signed seed when the real launcher imports it.
cp "$WORK/public" internal/releasetrust/public.key
cp "$WORK/public" release-public.key
go build -ldflags "-X main.version=$VERSION" -o "dist/kee-route-managerctl-linux-$ARCH" ./cmd/kee-route-managerctl
tar -czf dist/release-files.tar.gz configs install release-public.key LICENSE NOTICE
"$WORK/tool" manifest --version "$VERSION" --base-url "https://github.com/jarymor-ux/kee-route-manager/releases/download/v$VERSION" --dist dist --out dist/manifest-rc.json --private "$WORK/private-key"
python3 - dist <<'PYSUMS'
import hashlib,pathlib,sys
p=pathlib.Path(sys.argv[1])
entries=[]
for f in sorted(p.iterdir()):
 if f.is_file() and f.name not in {'SHA256SUMS','SHA256SUMS.sig'}:
  entries.append(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n')
(p/'SHA256SUMS').write_text(''.join(entries))
PYSUMS
"$WORK/tool" sign --private "$WORK/private-key" --input dist/SHA256SUMS --out dist/SHA256SUMS.sig
mkdir -p "$WORK/bootstrap-fake"
cat > "$WORK/bootstrap-fake/curl" <<'BOOTSTRAP_CURL'
#!/bin/sh
set -eu
out=
url=
while [ "$#" -gt 0 ]; do
 case "$1" in
  -o) out=$2; shift 2;;
  *) url=$1; shift;;
 esac
done
[ -n "$out" ] && [ -n "$url" ]
cp "$KRM_TEST_ASSETS/$(basename "$url")" "$out"
BOOTSTRAP_CURL
chmod 0755 "$WORK/bootstrap-fake/curl"
python3 - "$WORK" "$VERSION" <<'PYBOOTSTRAP'
import base64,pathlib,sys
work=pathlib.Path(sys.argv[1]);version=sys.argv[2]
raw=base64.b64decode((work/'public').read_text().strip()+'===')
der=bytes.fromhex('302a300506032b6570032100')+raw
pem='-----BEGIN PUBLIC KEY-----\n'+base64.b64encode(der).decode()+'\n-----END PUBLIC KEY-----'
template=pathlib.Path('install/bootstrap.sh').read_text()
for platform in ('linux-systemd','openwrt','keenetic'):
 (work/('bootstrap-'+platform+'.sh')).write_text(template.replace('@VERSION@',version).replace('@PLATFORM@',platform).replace('@PUBLIC_PEM@',pem))
PYBOOTSTRAP
# Disposable service-manager adapters execute the installed service commands.
# Keenetic uses the actual checked-in supervisor; no real host service is used.
cat > "$WORK/fake/krm-test-stop" <<'STOP'
#!/bin/sh
set -eu
file=/tmp/krm-test-service-$1.pid
if [ -s "$file" ]; then
 pid=$(cat "$file"); kill "$pid"
 for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  : "$attempt"
  [ -r "/proc/$pid/stat" ] || break
  [ "$(awk '{print $3}' "/proc/$pid/stat")" != Z ] || break
  sleep 1
 done
 rm -f "$file"
fi
STOP
cat > "$WORK/fake/systemctl" <<'MANAGER'
#!/bin/sh
set -eu
case "$1" in
 daemon-reload) exit 0;;
 enable)
  name=$3
  if [ "$name" = kee-route-manager ]; then binary=/usr/local/bin/kee-route-manager-launcher; config=/etc/kee-route-manager/config.yaml; else binary=/usr/local/bin/kee-route-manager-ui; config=/etc/kee-route-manager-ui/config.yaml; fi
  grep -Fxq "ExecStart=$binary serve --config $config" "/etc/systemd/system/$name.service"
  "$binary" serve --config "$config" </dev/null > "/tmp/$name.stdout" 2>&1 &
  echo "$!" > "/tmp/krm-test-service-$name.pid";;
 disable) krm-test-stop "$3";;
 *) echo 'unexpected service command' >&2; exit 1;;
esac
MANAGER
mkdir -p /etc/init.d /etc/systemd/system /opt/etc/init.d
cat > /etc/rc.common <<'PROCD'
#!/bin/sh
set -eu
init=$1; action=$2; name=${init##*/}
procd_open_instance(){ :; }
procd_close_instance(){ :; }
procd_set_param(){
 if [ "$1" = command ]; then
  shift
  "$@" </dev/null > "/tmp/$name.stdout" 2>&1 &
  echo "$!" > "/tmp/krm-test-service-$name.pid"
 fi
}
. "$init"
case "$action" in
 start) start_service;;
 stop) krm-test-stop "$name";;
 enable|disable) :;;
 *) exit 1;;
esac
PROCD
chmod 0755 "$WORK/fake/"* /etc/rc.common
export PATH="$WORK/fake:$PATH"
# Every test keeps Xray unconfigured, subscriptions disabled and firewall mode
# existing. This exercises real config/auth/TLS/process/file boundaries only.
python3 - "$WORK" <<'PYCONFIG'
import pathlib,re,sys
w=pathlib.Path(sys.argv[1]);key=(w/'public').read_text().strip()
for platform in ('linux-systemd','openwrt','keenetic'):
 s=pathlib.Path('configs/'+platform+'.yaml').read_text().replace('enabled: true\n      headers:', 'enabled: false\n      headers:')
 s=re.sub(r'^  public_key:.*$', '  public_key: "'+key+'"', s, flags=re.M)
 (w/('private/'+platform+'.yaml')).write_text(s)
 prefix='/opt' if platform=='keenetic' else ''
 tls=s.replace('    enabled: false\n    auto_generate: false\n    cert_file: ""\n    key_file: ""\n    hosts: []',
  '    enabled: true\n    auto_generate: true\n    cert_file: '+prefix+'/etc/kee-route-manager/tls.crt\n    key_file: '+prefix+'/etc/kee-route-manager/tls.key\n    hosts: [localhost]',1)
 assert tls != s, 'controller API TLS fixture was not enabled'
 (w/('private/'+platform+'-tls.yaml')).write_text(tls)
 ui=pathlib.Path('configs/ui-linux-openwrt.yaml').read_text()
 if platform=='keenetic':
  ui=ui.replace('/var/', '/opt/var/').replace('/run/', '/opt/var/run/').replace('/etc/kee-route-manager-ui/', '/opt/etc/kee-route-manager-ui/')
 (w/('private/'+platform+'-ui.yaml')).write_text(ui)
 managed_ca=('/opt' if platform=='keenetic' else '')+'/etc/kee-route-manager-ui/controller-ca.crt'
 custom=ui.replace('  upstream: "http://127.0.0.1:9443"\n', '  upstream: "https://127.0.0.1:9443"\n  upstream_ca_file: '+managed_ca+'\n')
 (w/('private/'+platform+'-ui-custom.yaml')).write_text(custom)
 wrong=custom.replace(managed_ca, '/tmp/krm-wrong-controller-ca.crt')
 (w/('private/'+platform+'-ui-wrong-ca.yaml')).write_text(wrong)
(w/'private/password').write_text('integration-only-long-password\n')
PYCONFIG
openssl req -x509 -newkey rsa:2048 -nodes -subj /CN=krm-installer-test -keyout "$WORK/private/ca.key" -out "$WORK/private/ca.crt" -days 1 >/dev/null 2>&1
# Assert installed private controller material and both validators/readiness.
check_core(){
 [[ -f "$core_config" && -s "$prefix/etc/kee-route-manager/credentials.json" ]]
 [[ "$(stat -c %a "$core_config")" = 600 ]]
 [[ "$(stat -c %a "$prefix/etc/kee-route-manager/credentials.json")" = 600 ]]
 [[ "$(stat -c %a "$prefix/etc/kee-route-manager")" = 700 ]]
 "$bin/kee-route-managerctl" validate --config "$core_config" >/dev/null
 "$bin/kee-route-managerctl" ready --config "$core_config" >/dev/null
}
check_ui(){
 [[ -f "$ui_config" && "$(stat -c %a "$ui_config")" = 644 ]]
 # UI config is public and readable by systemd DynamicUser; keys remain private.
 "$bin/kee-route-manager-ui" validate --config "$ui_config" >/dev/null
 "$bin/kee-route-manager-ui" ready --config "$ui_config" >/dev/null
 [[ "$(stat -c %a "$prefix/var/lib/kee-route-manager-ui/tls.key")" = 600 ]]
 curl --cacert "$prefix/var/lib/kee-route-manager-ui/tls.crt" -fsS https://127.0.0.1:9444/assets/app.js >/dev/null
}
platform_paths(){
 case "$platform" in
  keenetic) prefix=/opt; bin=/opt/bin;;
  openwrt) prefix=; bin=/usr/bin;;
  linux-systemd) prefix=; bin=/usr/local/bin;;
 esac
 core_config=$prefix/etc/kee-route-manager/config.yaml
 ui_config=$prefix/etc/kee-route-manager-ui/config.yaml
}
# Run the actual signed bootstrap, ctl wizard, installer and launcher for every
# platform/mode combination. Only service managers and downloads are fixtures.
for platform in linux-systemd openwrt keenetic; do
 platform_paths
 for mode in core local-ui; do
  python3 - "$WORK" "$platform" "$mode" <<'PYBOOTRUN'
import os,pty,subprocess,sys
work,platform,mode=sys.argv[1:]
master,slave=pty.openpty()
env=os.environ.copy()
for name in ('KRM_CONFIG_FILE','KRM_UI_CONFIG_FILE','KRM_UPSTREAM_CA_FILE','KRM_GENERATED_TLS_DIR'):
 env.pop(name,None)
env.update(
 KRM_MODE=mode,
 KRM_PASSWORD_FILE=work+'/private/password',
 KRM_TEST_ASSETS=work+'/source/dist',
 PATH=work+'/bootstrap-fake:'+work+'/fake:'+env['PATH'],
)
# Both languages cover the entire installer flow. Disabled subscriptions keep
# this fixture independent of the network; input secrets must never reach logs.
answers='\n'.join([
 '1' if mode=='local-ui' else '2','','','','','proxy-main',
 'https://sub.example.test/main?token=integration-subscription-secret','Main','n',
 'y','X-Integration-Secret','integration-header-secret','n','n',
 'https://score.example.test/ping',
 'https://health.example.test/ping','y','https://independent.example.test/ping','n','',
 'n','n','y',
])+'\n'
proc=subprocess.Popen(
 ['sh',work+'/bootstrap-'+platform+'.sh'],
 stdin=slave,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=env,
)
os.close(slave)
try:
 os.write(master,answers.encode())
 out,err=proc.communicate(timeout=120)
finally:
 os.close(master)
for secret in ('integration-subscription-secret','integration-header-secret','integration-only-long-password'):
 if secret in out+err:
  raise SystemExit('bootstrap exposed a configuration or credential secret')
sys.stdout.write(out)
sys.stderr.write(err)
if proc.returncode:
 raise SystemExit(proc.returncode)
PYBOOTRUN
  check_core
  grep -Fq '"kind": "'"$platform"'"' "$core_config"
  "$bin/kee-route-managerctl" validate --config "$core_config" > "$WORK/generated-validation.json"
  python3 - "$WORK/generated-validation.json" <<'PYDEFAULTS'
import json,sys
with open(sys.argv[1]) as output:
 assert output.readline().strip() == 'Configuration is valid'
 cfg=json.load(output)
assert cfg['update']['auto_apply'] is False
assert cfg['benchmark']['speed']['enabled'] is False
PYDEFAULTS
  [[ -L "$bin/kee-route-managerd" && -f "$bin/kee-route-manager-launcher" ]]
  if [[ "$mode" = local-ui ]]; then
   check_ui
   grep -Fq 'https://127.0.0.1:9443' "$ui_config"
   grep -Fq "$prefix/etc/kee-route-manager-ui/controller-ca.crt" "$ui_config"
   [[ -s "$prefix/etc/kee-route-manager-ui/controller-ca.crt" ]]
   # The UI trusts exactly the local controller certificate, never insecure TLS.
   cmp "$prefix/etc/kee-route-manager/tls.crt" "$prefix/etc/kee-route-manager-ui/controller-ca.crt"
   if grep -Eq 'insecure_tls["[:space:]]*:[[:space:]]*true' "$ui_config"; then
    echo 'generated UI disabled TLS verification' >&2; exit 1
   fi
  else
   [[ ! -e "$ui_config" ]]
  fi
  before=$(sha256sum "$core_config" "$prefix/etc/kee-route-manager/credentials.json")
  KRM_MODE=$mode KRM_CONFIG_FILE=$core_config KRM_UI_CONFIG_FILE=$ui_config KRM_PASSWORD_FILE="$WORK/private/password" \
   KRM_UPSTREAM_CA_FILE="$prefix/etc/kee-route-manager-ui/controller-ca.crt" KRM_TEST_ASSETS="$WORK/source/dist" PATH="$WORK/bootstrap-fake:$PATH" \
   sh "$WORK/bootstrap-$platform.sh" > "$WORK/reinstall.stdout" 2>&1 && { echo 'generated installation overwrite accepted' >&2; exit 1; }
  grep -Fq 'existing controller installation detected' "$WORK/reinstall.stdout"
  [[ "$before" = "$(sha256sum "$core_config" "$prefix/etc/kee-route-manager/credentials.json")" ]]
  KRM_MODE=$mode sh "install/$platform/uninstall.sh" --purge
  [[ ! -e "$bin/kee-route-managerd" && ! -e "$bin/kee-route-manager-launcher" && ! -e "$core_config" && ! -e "$ui_config" ]]
  printf 'Real configless bootstrap -> RU/EN wizard -> validated installer lifecycle passed: %s %s.\n' "$platform" "$mode"
 done
done
prepared_bootstrap(){
 KRM_TEST_ASSETS="$WORK/source/dist" PATH="$WORK/bootstrap-fake:$PATH" \
  sh "$WORK/bootstrap-$platform.sh" </dev/null
}
for platform in linux-systemd openwrt keenetic; do
 case "$platform" in
  keenetic) prefix=/opt; bin=/opt/bin; core_service=/opt/etc/init.d/S99kee-route-manager; ui_service=/opt/etc/init.d/S98kee-route-manager-ui;;
  openwrt) prefix=; bin=/usr/bin; core_service=/etc/init.d/kee-route-manager; ui_service=/etc/init.d/kee-route-manager-ui;;
  linux-systemd) prefix=; bin=/usr/local/bin; core_service=/etc/systemd/system/kee-route-manager.service; ui_service=/etc/systemd/system/kee-route-manager-ui.service;;
 esac
 core_config=$prefix/etc/kee-route-manager/config.yaml
 ui_config=$prefix/etc/kee-route-manager-ui/config.yaml
 export KRM_CONFIG_FILE="$WORK/private/$platform-tls.yaml" KRM_UI_CONFIG_FILE="$WORK/private/$platform-ui.yaml" KRM_PASSWORD_FILE="$WORK/private/password"
 export KRM_MODE=core
 prepared_bootstrap
 [[ -L "$bin/kee-route-managerd" && -L "$bin/kee-route-managerctl" && -f "$bin/kee-route-manager-launcher" ]]
 [[ ! -e "$ui_service" ]]
 check_core
 before=$(sha256sum "$core_config" "$prefix/etc/kee-route-manager/credentials.json")
 if prepared_bootstrap > "$WORK/reinstall.stdout" 2>&1; then echo 'prepared core overwrite accepted' >&2; exit 1; fi
 grep -Fq 'existing controller installation detected' "$WORK/reinstall.stdout"
 [[ "$before" = "$(sha256sum "$core_config" "$prefix/etc/kee-route-manager/credentials.json")" ]]
 # Custom-CA mistakes must fail during preflight, before UI files or services are installed.
 export KRM_MODE=ui KRM_CONFIG_FILE="$WORK/private/$platform-ui-custom.yaml"
 unset KRM_UPSTREAM_CA_FILE
 if sh "install/$platform/install.sh"; then echo 'custom CA accepted without KRM_UPSTREAM_CA_FILE' >&2; exit 1; fi
 [[ ! -e "$ui_config" && ! -e "$bin/kee-route-manager-ui" && ! -e "$ui_service" ]]
 export KRM_CONFIG_FILE="$WORK/private/$platform-ui-wrong-ca.yaml" KRM_UPSTREAM_CA_FILE="$WORK/private/ca.crt"
 if sh "install/$platform/install.sh"; then echo 'unmanaged custom CA path accepted' >&2; exit 1; fi
 [[ ! -e "$ui_config" && ! -e "$bin/kee-route-manager-ui" && ! -e "$ui_service" ]]
 # Standalone UI must reject plaintext upstreams before installing files.
 export KRM_MODE=ui KRM_CONFIG_FILE="$WORK/private/$platform-ui.yaml"
 unset KRM_UPSTREAM_CA_FILE
 if sh "install/$platform/install.sh"; then echo 'plaintext standalone UI accepted' >&2; exit 1; fi
 [[ ! -e "$ui_config" && ! -e "$bin/kee-route-manager-ui" && ! -e "$ui_service" ]]
 # The trusted HTTPS standalone UI installs beside a controller and removes alone.
 export KRM_CONFIG_FILE="$WORK/private/$platform-ui-custom.yaml" KRM_UPSTREAM_CA_FILE="$prefix/etc/kee-route-manager/tls.crt"
 prepared_bootstrap
 cmp "$KRM_UPSTREAM_CA_FILE" "$prefix/etc/kee-route-manager-ui/controller-ca.crt"
 check_ui
 sh "install/$platform/uninstall.sh" --purge
 "$bin/kee-route-managerctl" ready --config "$core_config"
 export KRM_MODE=core
 sh "install/$platform/uninstall.sh" --purge
 [[ ! -e "$bin/kee-route-managerd" && ! -e "$bin/kee-route-manager-launcher" && ! -e "$core_config" ]]
 # Local UI is supervised only once: routers by launcher, Linux by DynamicUser.
 export KRM_MODE=local-ui KRM_CONFIG_FILE="$WORK/private/$platform.yaml"
 unset KRM_UPSTREAM_CA_FILE
 if [[ "$platform" != linux-systemd ]]; then
  printf '#!/bin/sh\nexit 0\n' > "$ui_service"
  if sh "install/$platform/install.sh"; then echo 'concurrent UI service accepted' >&2; exit 1; fi
  [[ ! -e "$core_config" && ! -e "$bin/kee-route-manager-launcher" ]]
  rm "$ui_service"
 fi
 prepared_bootstrap
 check_core
 check_ui
 if [[ "$platform" == linux-systemd ]]; then
  [[ -f "$ui_service" && ! -L "$bin/kee-route-manager-ui" ]]
  grep -Fxq 'DynamicUser=true' "$ui_service"
 else
  [[ ! -e "$ui_service" && -L "$bin/kee-route-manager-ui" ]]
 fi
 curl --cacert "$prefix/var/lib/kee-route-manager-ui/tls.crt" -fsS https://127.0.0.1:9444/assets/app.js >/dev/null
 before=$(sha256sum "$core_config")
 if sh "install/$platform/install.sh"; then echo 'overwrite accepted' >&2; exit 1; fi
 [[ "$before" == "$(sha256sum "$core_config")" ]]
 # Failed restore must retain the live launcher, both child services and files.
 mv "$bin/kee-route-managerctl" "$WORK/ctl"
 printf '#!/bin/sh\nexit 1\n' > "$bin/kee-route-managerctl"
 chmod 0755 "$bin/kee-route-managerctl"
 if sh "install/$platform/uninstall.sh" --purge; then echo 'uninstall ignored failed restore' >&2; exit 1; fi
 for retained in "$bin/kee-route-managerd" "$bin/kee-route-manager-ui" "$bin/kee-route-manager-launcher" "$core_config" "$ui_config" "$core_service"; do
  [[ -f "$retained" ]] || { echo "failed restore removed $retained" >&2; exit 1; }
 done
 mv "$WORK/ctl" "$bin/kee-route-managerctl"
 "$bin/kee-route-managerctl" ready --config "$core_config"
 curl --cacert "$prefix/var/lib/kee-route-manager-ui/tls.crt" -fsS https://127.0.0.1:9444/assets/app.js >/dev/null
 sh "install/$platform/uninstall.sh" --purge
 [[ ! -e "$bin/kee-route-managerd" && ! -e "$bin/kee-route-manager-ui" && ! -e "$bin/kee-route-manager-launcher" && ! -e "$core_config" ]]
 printf 'Installer lifecycle passed: %s core, standalone UI and local-ui; overwrite refusal; failed-restore retention; restore/uninstall/purge.\n' "$platform"
done
# A slow but bounded launcher drain must finish before its init wrapper returns.
# This fixture deliberately exceeds the former 30-second wrapper budget.
mkdir -p /opt/bin /opt/etc/kee-route-manager /opt/etc/init.d /opt/var/run/kee-route-manager
cp install/keenetic/S99kee-route-manager /opt/etc/init.d/S99kee-route-manager
chmod 0755 /opt/etc/init.d/S99kee-route-manager
printf 'supervisor-only fixture\n' > /opt/etc/kee-route-manager/config.yaml
cat > /opt/bin/kee-route-manager-launcher <<'SLOW_LAUNCHER'
#!/bin/sh
set -eu
finish(){ sleep 32; touch /tmp/krm-test-drained; exit 0; }
trap finish TERM INT
touch /tmp/krm-test-drain-ready
while :; do sleep 1 & wait "$!" || true; done
SLOW_LAUNCHER
chmod 0755 /opt/bin/kee-route-manager-launcher
/opt/etc/init.d/S99kee-route-manager start
for attempt in {1..10}; do
 : "$attempt"
 [[ ! -e /tmp/krm-test-drain-ready ]] || break
 sleep 1
done
[[ -e /tmp/krm-test-drain-ready ]]
/opt/etc/init.d/S99kee-route-manager stop
[[ -e /tmp/krm-test-drained ]]
[[ ! -e /opt/var/run/kee-route-manager/supervisor.pid && ! -e /opt/var/run/kee-route-manager/service-child.pid ]]
rm -f /opt/bin/kee-route-manager-launcher /opt/etc/kee-route-manager/config.yaml /opt/etc/init.d/S99kee-route-manager /tmp/krm-test-drain-ready /tmp/krm-test-drained
printf 'Keenetic init waited for complete delayed launcher drain.\n'
printf 'Service managers are container fixtures; systemd/procd and router hardware acceptance remain separate.\n'
