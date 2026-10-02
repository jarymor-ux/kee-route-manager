#!/usr/bin/env python3
"""Verify Ed25519 signatures and every manifest asset before release upload."""
import base64,hashlib,json,pathlib,subprocess,sys,tempfile
p=pathlib.Path(sys.argv[1]).resolve()
pub=base64.b64decode(pathlib.Path('release-public.key').read_text().strip()+'===')
with tempfile.TemporaryDirectory() as d:
 d=pathlib.Path(d);(d/'pub.der').write_bytes(bytes.fromhex('302a300506032b6570032100')+pub)
 for name in ['manifest-rc.json','SHA256SUMS']:
  (d/'sig').write_bytes(base64.b64decode((p/(name+'.sig')).read_text().strip()+'==='))
  subprocess.run(['openssl','pkeyutl','-verify','-pubin','-keyform','DER','-inkey',str(d/'pub.der'),'-rawin','-in',str(p/name),'-sigfile',str(d/'sig')],check=True,stdout=subprocess.DEVNULL)
m=json.loads((p/'manifest-rc.json').read_text())
seen=set()
for a in m['assets']:
 name=a['name']
 if pathlib.Path(name).name!=name or name in seen: raise SystemExit('unsafe/duplicate asset')
 seen.add(name);f=p/name
 if f.is_symlink() or not f.is_file(): raise SystemExit('missing/nonregular asset '+name)
 b=f.read_bytes()
 if len(b)!=a['size'] or hashlib.sha256(b).hexdigest()!=a['sha256']: raise SystemExit('manifest mismatch '+name)
 if not a['url'].startswith(f'https://github.com/jarymor-ux/kee-route-manager/releases/download/v{m["version"]}/'): raise SystemExit('not version pinned')
for line in (p/'SHA256SUMS').read_text().splitlines():
 digest,name=line.split('  ',1)
 if pathlib.Path(name).name!=name: raise SystemExit('unsafe checksum filename')
 if hashlib.sha256((p/name).read_bytes()).hexdigest()!=digest: raise SystemExit('checksum mismatch '+name)
required={f'{c}-linux-{a}' for c in ['kee-route-managerd','kee-route-manager-ui','kee-route-managerctl','krm-release-tool'] for a in ['amd64','arm64','armv7','mipsle']}|{'bootstrap-keenetic.sh','bootstrap-openwrt.sh','bootstrap-linux.sh','release-files.tar.gz','SBOM.spdx.json'}
if not required<=seen: raise SystemExit('missing required assets '+str(required-seen))
print(f'Verified signatures, sizes, digests and required manifest assets ({len(seen)} files).')
