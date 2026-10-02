#!/usr/bin/env python3
"""Adversarial bootstrap tests: use real Ed25519 verification, fake downloads, harmless installer."""
import base64,hashlib,io,json,os,pathlib,subprocess,tarfile,tempfile,unittest
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
 def fixture(self,version="1.0.0-rc.2",platform='keenetic',unsafe=None):
  d=pathlib.Path(tempfile.mkdtemp(dir=self.root));assets=d/'assets';assets.mkdir();fake=d/'fake';fake.mkdir()
  marker=d/'executed'
  with tarfile.open(assets/'release-files.tar.gz','w:gz') as tf:
   content=b'#!/bin/sh\nprintf verified > "$KRM_TEST_MARKER"\n'
   info=tarfile.TarInfo('install/'+platform+'/install.sh');info.size=len(content);info.mode=0o755;tf.addfile(info,io.BytesIO(content))
   if unsafe:
    info=tarfile.TarInfo('../escape' if unsafe=='path' else 'link')
    if unsafe=='link':info.type=tarfile.SYMTYPE;info.linkname='/tmp'
    else:info.size=1
    tf.addfile(info,None if unsafe=='link' else io.BytesIO(b'x'))
  for component in ['kee-route-managerd','kee-route-managerctl','kee-route-manager-ui']:
   (assets/(component+'-linux-amd64')).write_text('#!/bin/sh\nexit 0\n')
  subprocess.run([str(self.tool),'manifest','--version',version,'--base-url','https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.2','--dist',str(assets),'--out',str(assets/'manifest-rc.json'),'--private',str(self.root/'private')],check=True)
  manifest=json.loads((assets/'manifest-rc.json').read_text())
  self.assertEqual({a['name'] for a in manifest['assets']},{'release-files.tar.gz'}|{c+'-linux-amd64' for c in ['kee-route-managerd','kee-route-managerctl','kee-route-manager-ui']})
  self.sign_checksums(assets)
  pub=base64.b64decode((self.root/'public').read_text().strip()+'===');pem=base64.b64encode(bytes.fromhex('302a300506032b6570032100')+pub).decode()
  bootstrap=ROOT.joinpath('install/bootstrap.sh').read_text().replace('@VERSION@','1.0.0-rc.2').replace('@PLATFORM@',platform).replace('@PUBLIC_PEM@','-----BEGIN PUBLIC KEY-----\n'+pem+'\n-----END PUBLIC KEY-----')
  (d/'bootstrap.sh').write_text(bootstrap);(d/'config.yaml').write_text('private fixture')
  scripts={'id':'#!/bin/sh\necho 0\n','uname':'#!/bin/sh\necho x86_64\n','curl':'''#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then out=$2; shift 2; else url=$1; shift; fi
done
cp "$KRM_TEST_ASSETS/${url##*/}" "$out"
'''}
  for name,body in scripts.items():(fake/name).write_text(body);(fake/name).chmod(0o755)
  env=os.environ.copy();env.update(PATH=str(fake)+':'+env['PATH'],KRM_TEST_ASSETS=str(assets),KRM_TEST_MARKER=str(marker),KRM_CONFIG_FILE=str(d/'config.yaml'),KRM_MODE='local-ui')
  return d,assets,marker,env
 def sign_checksums(self,assets):
  sums=''.join(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n' for f in sorted(assets.iterdir()) if f.name not in {'SHA256SUMS','SHA256SUMS.sig'})
  (assets/'SHA256SUMS').write_text(sums)
  subprocess.run([str(self.tool),'sign','--private',str(self.root/'private'),'--input',str(assets/'SHA256SUMS'),'--out',str(assets/'SHA256SUMS.sig')],check=True)
 def run_bootstrap(self,d,env):return subprocess.run(['sh',str(d/'bootstrap.sh')],env=env,capture_output=True,text=True)
 def test_valid_release_installs(self):
  for platform in PLATFORMS:
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture(platform=platform);r=self.run_bootstrap(d,e);self.assertEqual(r.returncode,0,r.stderr);self.assertEqual(m.read_text(),'verified')
 def test_valid_signature_old_version_rejected(self):
  for platform in PLATFORMS:
   with self.subTest(platform=platform):
    d,a,m,e=self.fixture("1.0.0-rc.1",platform);r=self.run_bootstrap(d,e);self.assertNotEqual(r.returncode,0);self.assertFalse(m.exists(),r.stderr)
 def test_tampering_never_executes(self):
  for platform in PLATFORMS:
   for name in ['manifest-rc.json','manifest-rc.json.sig','SHA256SUMS','SHA256SUMS.sig','kee-route-managerd-linux-amd64','release-files.tar.gz']:
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
