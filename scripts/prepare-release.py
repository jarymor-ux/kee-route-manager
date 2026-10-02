#!/usr/bin/env python3
"""Create pinned bootstraps and a source/package SPDX inventory; no third party runtime Go modules."""
import base64, datetime, hashlib, json, pathlib, subprocess, sys, os
out=pathlib.Path(sys.argv[1]);version=sys.argv[2]
pub=base64.b64decode(pathlib.Path('release-public.key').read_text().strip()+'===')
if len(pub)!=32: raise SystemExit('invalid release public key')
der=bytes.fromhex('302a300506032b6570032100')+pub
b64=base64.b64encode(der).decode()
pem='-----BEGIN PUBLIC KEY-----\n'+'\n'.join(b64[i:i+64] for i in range(0,len(b64),64))+'\n-----END PUBLIC KEY-----'
template=pathlib.Path('install/bootstrap.sh').read_text()
for platform,name in [('keenetic','keenetic'),('openwrt','openwrt'),('linux-systemd','linux')]:
 s=template.replace('@VERSION@',version).replace('@PLATFORM@',platform).replace('@PUBLIC_PEM@',pem)
 (out/f'bootstrap-{name}.sh').write_text(s)
 (out/f'bootstrap-{name}.sh').chmod(0o755)
files=[]
for f in sorted(out.iterdir()):
 if f.is_file():
  files.append({'SPDXID':'SPDXRef-File-'+f.name.replace('.','-'),'fileName':'./'+f.name,'checksums':[{'algorithm':'SHA256','checksumValue':hashlib.sha256(f.read_bytes()).hexdigest()}],'licenseConcluded':'NOASSERTION','copyrightText':'NOASSERTION'})
go_version=subprocess.check_output(['go','version'],text=True).strip()
pkgid='SPDXRef-Package-KRM'
sbom={'spdxVersion':'SPDX-2.3','dataLicense':'CC0-1.0','SPDXID':'SPDXRef-DOCUMENT','name':'kee-route-manager-'+version,'documentNamespace':f'https://github.com/jarymor-ux/kee-route-manager/sbom/{version}/{(os.environ.get('KRM_SOURCE_COMMIT') or subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip())}', 'creationInfo':{'creators':['Tool: krm-release-builder','Organization: Kee Route Manager'],'created':datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')},'packages':[{'SPDXID':pkgid,'name':'kee-route-manager','versionInfo':version,'downloadLocation':f'https://github.com/jarymor-ux/kee-route-manager/tree/v{version}','filesAnalyzed':False,'licenseConcluded':'Apache-2.0','licenseDeclared':'Apache-2.0','copyrightText':'2026 Kee Route Manager contributors','comment':'Built with '+go_version+'; see go.mod (no external runtime dependencies). This inventory records distributable binaries, not a transitive stdlib file SBOM.'}], 'files':files,'relationships':[{'spdxElementId':'SPDXRef-DOCUMENT','relationshipType':'DESCRIBES','relatedSpdxElement':pkgid}]+[{'spdxElementId':pkgid,'relationshipType':'CONTAINS','relatedSpdxElement':f['SPDXID']} for f in files]}
(out/'SBOM.spdx.json').write_text(json.dumps(sbom,indent=2)+'\n')
