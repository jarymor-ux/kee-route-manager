#!/usr/bin/env python3
"""Adversarial bootstrap tests: use real Ed25519 verification, fake downloads, harmless installer."""
import base64,hashlib,io,json,os,pathlib,pty,subprocess,sys,tarfile,tempfile,unittest
ROOT=pathlib.Path(__file__).resolve().parent.parent
PLATFORMS=('keenetic','openwrt','linux-systemd')
class BootstrapTests(unittest.TestCase):
 @classmethod
 def setUpClass(cls):
  cls.tmp=tempfile.TemporaryDirectory();cls.root=pathlib.Path(cls.tmp.name)
  cls.tool=cls.root/'tool';subprocess.run(['go','build','-o',str(cls.tool),'./cmd/krm-release-tool'],cwd=ROOT,check=True)
  subprocess.run([str(cls.tool),'keygen','--public',str(cls.root/'public'),'--private',str(cls.root/'private')],check=True)
 @classmethod
 def tearDownClass(cls):cls.tmp.cleanup()
 def test_keenetic_status_helper_ignores_nonproduction_processes(self):
  for kind in ('production','probe','wrong-executable','wrong-config','zombie','missing'):
   with self.subTest(kind=kind):
    proc=pathlib.Path(tempfile.mkdtemp(dir=self.root));entry=proc/'1234';entry.mkdir()
    if kind!='missing':
     (entry/'comm').write_text('xray\n')
     (entry/'exe').symlink_to('/other/xray' if kind=='wrong-executable' else '/opt/sbin/xray')
     (entry/'stat').write_text('1234 (xray) '+('Z' if kind=='zombie' else 'S')+' '+' '.join(['0']*18+['123456'])+'\n')
     (entry/'cmdline').write_bytes(b'xray\x00run\x00'+(b'-confdir\x00/tmp/krm-probe\x00' if kind=='probe' else b''))
     (entry/'environ').write_bytes(b'XRAY_LOCATION_CONFDIR='+ (b'/tmp/krm-probe' if kind=='wrong-config' else b'/opt/etc/xray/configs')+b'\x00')
    result=subprocess.run(['sh',str(ROOT/'install/keenetic/xray-status.sh'),str(proc)],capture_output=True,text=True)
    self.assertEqual(result.returncode,0 if kind=='production' else 1,result.stderr)
 def test_prepare_release_pins_bootstraps_and_inventories_assets(self):
  d=pathlib.Path(tempfile.mkdtemp(dir=self.root));out=d/'dist';out.mkdir();(d/'install').mkdir()
  (d/'install/bootstrap.sh').write_bytes((ROOT/'install/bootstrap.sh').read_bytes())
  (out/'fixture-binary').write_bytes(b'release payload')
  env=os.environ.copy();env['KRM_SOURCE_COMMIT']='fixture-commit';env['KRM_RELEASE_PUBLIC_KEY']=str(self.root/'public')
  subprocess.run([sys.executable,str(ROOT/'scripts/prepare-release.py'),str(out),'1.0.0-rc.2'],cwd=d,env=env,check=True)
  for name,platform in (('keenetic','keenetic'),('openwrt','openwrt'),('linux','linux-systemd')):
   body=(out/f'bootstrap-{name}.sh').read_text()
   self.assertIn('TAG=v1.0.0-rc.2',body);self.assertIn('PLATFORM='+platform,body)
   self.assertIn('-----BEGIN PUBLIC KEY-----',body);self.assertNotIn('@VERSION@',body);self.assertNotIn('@PUBLIC_PEM@',body)
  inventory=json.loads((out/'SBOM.spdx.json').read_text())
  self.assertTrue(inventory['documentNamespace'].endswith('/1.0.0-rc.2/fixture-commit'))
  self.assertEqual(inventory['packages'][0]['licenseDeclared'],'Apache-2.0')
  self.assertEqual({f['fileName'] for f in inventory['files']},{'./fixture-binary','./bootstrap-keenetic.sh','./bootstrap-openwrt.sh','./bootstrap-linux.sh'})
  for entry in inventory['files']:
   self.assertEqual(entry['checksums'][0]['checksumValue'],hashlib.sha256((out/entry['fileName']).read_bytes()).hexdigest())
 def fixture(self,version="1.0.0-rc.2",platform='keenetic',unsafe=None):
  d=pathlib.Path(tempfile.mkdtemp(dir=self.root));assets=d/'assets';assets.mkdir();fake=d/'fake';fake.mkdir()
  marker=d/'executed';order=d/'order.log';config_used=d/'config-used';config_path=d/'config-path';ui_config_used=d/'ui-config-used';ui_config_path=d/'ui-config-path'
  with tarfile.open(assets/'release-files.tar.gz','w:gz') as tf:
   content=b'''#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
for name in manifest-rc.json manifest-rc.json.sig SHA256SUMS SHA256SUMS.sig; do test -s "$root/dist/$name"; done
if [ "$KRM_MODE" != ui ]; then
 for name in kee-route-managerd kee-route-managerctl kee-route-manager-ui kee-route-manager-launcher; do test -x "$root/dist/$name-linux-amd64"; done
else
 test -x "$root/dist/kee-route-manager-ui-linux-amd64"
 test ! -e "$root/dist/kee-route-manager-launcher-linux-amd64"
fi
test -s "$KRM_CONFIG_FILE"
if [ "$KRM_MODE" = local-ui ]; then
 test -s "$KRM_UI_CONFIG_FILE"
 printf '%s\n' "$KRM_UI_CONFIG_FILE" > "$KRM_TEST_UI_CONFIG_PATH"
 cp "$KRM_UI_CONFIG_FILE" "$KRM_TEST_UI_CONFIG_USED"
fi
printf 'installer\n' >> "$KRM_TEST_ORDER"
printf '%s\n' "$KRM_CONFIG_FILE" > "$KRM_TEST_CONFIG_PATH"
cp "$KRM_CONFIG_FILE" "$KRM_TEST_CONFIG_USED"
printf verified > "$KRM_TEST_MARKER"
'''
   info=tarfile.TarInfo('install/'+platform+'/install.sh');info.size=len(content);info.mode=0o755;tf.addfile(info,io.BytesIO(content))
   for path,body in (('configs/ui-keenetic.yaml',b'fixture-ui: keenetic\n'),('configs/ui-linux-openwrt.yaml',b'fixture-ui: linux-openwrt\n')):
    info=tarfile.TarInfo(path);info.size=len(body);info.mode=0o600;tf.addfile(info,io.BytesIO(body))
   if unsafe:
    info=tarfile.TarInfo('../escape' if unsafe=='path' else 'link')
    if unsafe=='link':info.type=tarfile.SYMTYPE;info.linkname='/tmp'
    else:info.size=1
    tf.addfile(info,None if unsafe=='link' else io.BytesIO(b'x'))
  ctl='''#!/bin/sh
set -eu
printf 'ctl:%s\\n' "${1:-}" >> "$KRM_TEST_ORDER"
case "${1:-}" in
 init-config)
  shift
  output=
  platform=
  while [ "$#" -gt 0 ]; do
   case "$1" in
    --output) output=$2; shift 2;;
    --platform) platform=$2; shift 2;;
    *) shift;;
   esac
  done
  printf 'ctl:init-config-platform:%s\\n' "$platform" >> "$KRM_TEST_ORDER"
  [ -n "$output" ] || exit 2
  case "${KRM_TEST_WIZARD_RESULT:-valid}" in
   cancel) exit 130;;
   invalid) printf 'invalid: [\\n' > "$output";;
   *) printf 'fixture: valid\\n' > "$output";;
  esac
  ;;
 validate)
  [ "${KRM_TEST_WIZARD_RESULT:-valid}" != invalid ] || exit 1
  ;;
esac
exit 0
'''
  for component in ['kee-route-managerd','kee-route-managerctl','kee-route-manager-ui','kee-route-manager-launcher']:
   body=ctl if component=='kee-route-managerctl' else '#!/bin/sh\nexit 0\n'
   (assets/(component+'-linux-amd64')).write_text(body)
  subprocess.run([str(self.tool),'manifest','--version',version,'--base-url','https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.2','--dist',str(assets),'--out',str(assets/'manifest-rc.json'),'--private',str(self.root/'private')],check=True)
  manifest=json.loads((assets/'manifest-rc.json').read_text())
  self.assertEqual(manifest['update_protocol'],1)
  self.assertEqual({a['name'] for a in manifest['assets']},{'release-files.tar.gz'}|{c+'-linux-amd64' for c in ['kee-route-managerd','kee-route-managerctl','kee-route-manager-ui','kee-route-manager-launcher']})
  self.sign_checksums(assets)
  pub=base64.b64decode((self.root/'public').read_text().strip()+'===');pem=base64.b64encode(bytes.fromhex('302a300506032b6570032100')+pub).decode()
  bootstrap=ROOT.joinpath('install/bootstrap.sh').read_text().replace('@VERSION@','1.0.0-rc.2').replace('@PLATFORM@',platform).replace('@PUBLIC_PEM@','-----BEGIN PUBLIC KEY-----\n'+pem+'\n-----END PUBLIC KEY-----')
  (d/'bootstrap.sh').write_text(bootstrap);(d/'config.yaml').write_text('private fixture');(d/'ui.yaml').write_text('private ui fixture')
  scripts={'id':'#!/bin/sh\necho 0\n','uname':'#!/bin/sh\necho x86_64\n','curl':'''#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then out=$2; shift 2; else url=$1; shift; fi
done
printf 'fetch:%s\\n' "${url##*/}" >> "$KRM_TEST_ORDER"
cp "$KRM_TEST_ASSETS/${url##*/}" "$out"
'''}
  for name,body in scripts.items():(fake/name).write_text(body);(fake/name).chmod(0o755)
  env=os.environ.copy();env.update(PATH=str(fake)+':'+env['PATH'],KRM_TEST_ASSETS=str(assets),KRM_TEST_MARKER=str(marker),KRM_TEST_ORDER=str(order),KRM_TEST_CONFIG_USED=str(config_used),KRM_TEST_CONFIG_PATH=str(config_path),KRM_TEST_UI_CONFIG_USED=str(ui_config_used),KRM_TEST_UI_CONFIG_PATH=str(ui_config_path),KRM_CONFIG_FILE=str(d/'config.yaml'),KRM_UI_CONFIG_FILE=str(d/'ui.yaml'),KRM_MODE='local-ui')
  return d,assets,marker,env
 def sign_checksums(self,assets):
  sums=''.join(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n' for f in sorted(assets.iterdir()) if f.name not in {'SHA256SUMS','SHA256SUMS.sig'})
  (assets/'SHA256SUMS').write_text(sums)
  subprocess.run([str(self.tool),'sign','--private',str(self.root/'private'),'--input',str(assets/'SHA256SUMS'),'--out',str(assets/'SHA256SUMS.sig')],check=True)
 def run_bootstrap(self,d,env,cwd=None):return subprocess.run(['sh',str(d/'bootstrap.sh')],env=env,cwd=cwd,input='',capture_output=True,text=True)
 def run_bootstrap_tty(self,d,env,cwd=None):
  master,slave=pty.openpty()
  try:return subprocess.run(['sh',str(d/'bootstrap.sh')],env=env,cwd=cwd,stdin=slave,capture_output=True,text=True)
  finally:os.close(slave);os.close(master)
 def test_valid_release_installs(self):
  for platform in PLATFORMS:
   for mode in ('core','local-ui','ui'):
    with self.subTest(platform=platform,mode=mode):
     d,a,m,e=self.fixture(platform=platform);e['KRM_MODE']=mode;r=self.run_bootstrap(d,e);self.assertEqual(r.returncode,0,r.stderr);self.assertEqual(m.read_text(),'verified')
 def test_existing_config_skips_wizard(self):
  d,a,m,e=self.fixture();r=self.run_bootstrap(d,e)
  self.assertEqual(r.returncode,0,r.stderr);self.assertEqual((d/'config-used').read_text(),'private fixture')
  self.assertNotIn('ctl:init-config',(d/'order.log').read_text())
 def test_missing_config_runs_verified_wizard_before_installer(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';r=self.run_bootstrap_tty(d,e)
  self.assertEqual(r.returncode,0,r.stderr);self.assertEqual((d/'config-used').read_text(),'fixture: valid\n')
  order=(d/'order.log').read_text().splitlines();wizard=order.index('ctl:init-config');installer=order.index('installer')
  self.assertGreater(wizard,max(i for i,v in enumerate(order) if v.startswith('fetch:')));self.assertLess(wizard,installer)
  self.assertIn('ctl:validate',order)
 def test_missing_config_supports_relative_tmpdir(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';(d/'relative-tmp').mkdir();e['TMPDIR']='relative-tmp'
  r=self.run_bootstrap_tty(d,e,cwd=d)
  self.assertEqual(r.returncode,0,r.stderr);self.assertEqual((d/'config-used').read_text(),'fixture: valid\n')
  self.assertEqual(list((d/'relative-tmp').iterdir()),[])
 def test_missing_config_pins_bootstrap_platform(self):
  for platform in PLATFORMS:
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture(platform=platform);e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';r=self.run_bootstrap_tty(d,e)
    self.assertEqual(r.returncode,0,r.stderr)
    self.assertIn(f'ctl:init-config-platform:{platform}',(d/'order.log').read_text().splitlines())
 def test_missing_config_local_ui_uses_signed_ui_template(self):
  for platform,expected in (('keenetic','fixture-ui: keenetic\n'),('openwrt','fixture-ui: linux-openwrt\n'),('linux-systemd','fixture-ui: linux-openwrt\n')):
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture(platform=platform);e.pop('KRM_CONFIG_FILE');e.pop('KRM_UI_CONFIG_FILE');e['KRM_MODE']='local-ui';r=self.run_bootstrap_tty(d,e)
    self.assertEqual(r.returncode,0,r.stderr);self.assertEqual((d/'config-used').read_text(),'fixture: valid\n');self.assertEqual((d/'ui-config-used').read_text(),expected)
    order=(d/'order.log').read_text().splitlines();self.assertIn('ctl:init-config',order);self.assertIn('installer',order)
 def test_ui_mode_without_config_requires_prepared_ui_config(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='ui';r=self.run_bootstrap_tty(d,e)
  self.assertNotEqual(r.returncode,0);self.assertIn('KRM_MODE=ui requires KRM_CONFIG_FILE pointing to a prepared UI config',r.stderr);self.assertFalse(m.exists())
  order=(d/'order.log').read_text().splitlines();self.assertNotIn('ctl:init-config',order);self.assertNotIn('installer',order)
 def test_invalid_manifest_signature_never_runs_wizard(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';p=a/'manifest-rc.json.sig';s=p.read_text();p.write_text(('A' if s[:1]!='A' else 'B')+s[1:])
  r=self.run_bootstrap_tty(d,e);self.assertNotEqual(r.returncode,0);self.assertFalse(m.exists())
  self.assertNotIn('ctl:init-config',(d/'order.log').read_text() if (d/'order.log').exists() else '')
 def test_invalid_sha256_never_runs_wizard(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';p=a/'kee-route-managerd-linux-amd64';p.write_bytes(p.read_bytes()+b'BAD')
  r=self.run_bootstrap_tty(d,e);self.assertNotEqual(r.returncode,0);self.assertIn('checksum mismatch',r.stderr);self.assertFalse(m.exists())
  self.assertNotIn('ctl:init-config',(d/'order.log').read_text())
 def test_tampered_ctl_never_runs_wizard(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';p=a/'kee-route-managerctl-linux-amd64';p.write_bytes(p.read_bytes()+b'BAD')
  r=self.run_bootstrap_tty(d,e);self.assertNotEqual(r.returncode,0);self.assertIn('checksum mismatch',r.stderr);self.assertFalse(m.exists())
  self.assertNotIn('ctl:init-config',(d/'order.log').read_text())
 def test_configless_tampering_never_runs_wizard(self):
  for name in ['manifest-rc.json','manifest-rc.json.sig','SHA256SUMS','SHA256SUMS.sig','release-files.tar.gz','kee-route-managerd-linux-amd64','kee-route-managerctl-linux-amd64','kee-route-manager-ui-linux-amd64','kee-route-manager-launcher-linux-amd64']:
   with self.subTest(asset=name):
    d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';p=a/name;p.write_bytes(p.read_bytes()+b'BAD')
    r=self.run_bootstrap_tty(d,e);self.assertNotEqual(r.returncode,0);self.assertFalse(m.exists(),r.stderr)
    order=(d/'order.log').read_text() if (d/'order.log').exists() else ''
    self.assertNotIn('ctl:init-config',order);self.assertNotIn('installer',order)
 def test_missing_config_without_tty_fails_after_verification(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';r=self.run_bootstrap(d,e)
  self.assertNotEqual(r.returncode,0);self.assertIn('No configuration was provided and interactive terminal is unavailable.',r.stderr)
  self.assertIn('Provide KRM_CONFIG_FILE=/absolute/path/config.yaml',r.stderr);self.assertFalse(m.exists())
  order=(d/'order.log').read_text().splitlines();self.assertTrue(any(v=='fetch:kee-route-managerctl-linux-amd64' for v in order));self.assertNotIn('ctl:init-config',order);self.assertNotIn('installer',order)
 def test_cancelled_wizard_does_not_start_installer(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';e['KRM_TEST_WIZARD_RESULT']='cancel';r=self.run_bootstrap_tty(d,e)
  self.assertNotEqual(r.returncode,0);order=(d/'order.log').read_text().splitlines();self.assertIn('ctl:init-config',order);self.assertNotIn('installer',order);self.assertFalse(m.exists())
 def test_invalid_generated_config_does_not_start_installer(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';e['KRM_TEST_WIZARD_RESULT']='invalid';r=self.run_bootstrap_tty(d,e)
  self.assertNotEqual(r.returncode,0);order=(d/'order.log').read_text().splitlines();self.assertIn('ctl:init-config',order);self.assertIn('ctl:validate',order);self.assertNotIn('installer',order);self.assertFalse(m.exists())
 def test_generated_config_is_removed_by_bootstrap_cleanup(self):
  d,a,m,e=self.fixture();e.pop('KRM_CONFIG_FILE');e['KRM_MODE']='core';r=self.run_bootstrap_tty(d,e)
  self.assertEqual(r.returncode,0,r.stderr);generated=pathlib.Path((d/'config-path').read_text().strip());self.assertFalse(generated.exists(),str(generated))
 def test_existing_config_path_remains_noninteractive(self):
  d,a,m,e=self.fixture();e['KRM_MODE']='core';r=self.run_bootstrap(d,e)
  self.assertEqual(r.returncode,0,r.stderr);order=(d/'order.log').read_text().splitlines();self.assertNotIn('ctl:init-config',order);self.assertIn('installer',order)
 def test_valid_signature_old_version_rejected(self):
  for platform in PLATFORMS:
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture("1.0.0-rc.1",platform);r=self.run_bootstrap(d,e);self.assertNotEqual(r.returncode,0);self.assertFalse(m.exists(),r.stderr)
 def test_signed_unsupported_update_protocol_rejected(self):
  for protocol in (None,0,2):
   with self.subTest(protocol=protocol):
    d,a,m,e=self.fixture();p=a/'manifest-rc.json';manifest=json.loads(p.read_text())
    if protocol is None:manifest.pop('update_protocol')
    else:manifest['update_protocol']=protocol
    p.write_text(json.dumps(manifest,indent=2)+'\n')
    subprocess.run([str(self.tool),'sign','--private',str(self.root/'private'),'--input',str(p),'--out',str(p)+'.sig'],check=True)
    self.sign_checksums(a);r=self.run_bootstrap(d,e)
    self.assertNotEqual(r.returncode,0);self.assertIn('update protocol',r.stderr);self.assertFalse(m.exists())
 def test_tampering_never_executes(self):
  for platform in PLATFORMS:
   for name in ['manifest-rc.json','manifest-rc.json.sig','SHA256SUMS','SHA256SUMS.sig','kee-route-managerd-linux-amd64','kee-route-manager-launcher-linux-amd64','release-files.tar.gz']:
    with self.subTest(platform=platform,asset=name):
     d,a,m,e=self.fixture(platform=platform);p=a/name;p.write_bytes(p.read_bytes()+b'BAD');r=self.run_bootstrap(d,e);self.assertNotEqual(r.returncode,0);self.assertFalse(m.exists(),r.stderr)
 def test_signed_wrong_size_rejected(self):
  for platform in PLATFORMS:
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture(platform=platform);p=a/'manifest-rc.json';manifest=json.loads(p.read_text())
    for asset in manifest['assets']:
     if asset['name']=='release-files.tar.gz':asset['size']+=1
    p.write_text(json.dumps(manifest,indent=2)+'\n')
    subprocess.run([str(self.tool),'sign','--private',str(self.root/'private'),'--input',str(p),'--out',str(p)+'.sig'],check=True)
    self.sign_checksums(a);r=self.run_bootstrap(d,e);self.assertNotEqual(r.returncode,0);self.assertIn('size mismatch',r.stderr);self.assertFalse(m.exists())
 def test_signed_unsafe_archive_rejected(self):
  for platform in PLATFORMS:
   for unsafe in ('path','link'):
    with self.subTest(platform=platform,unsafe=unsafe):
     d,a,m,e=self.fixture(platform=platform,unsafe=unsafe);r=self.run_bootstrap(d,e)
     self.assertNotEqual(r.returncode,0);self.assertIn('unsafe archive',r.stderr);self.assertFalse(m.exists())
if __name__=='__main__':unittest.main()
