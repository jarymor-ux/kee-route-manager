#!/usr/bin/env python3
"""Create channel-pinned bootstraps and a distributable/runtime-module SPDX inventory."""
import base64, datetime, hashlib, json, pathlib, subprocess, sys, os, re
sys.dont_write_bytecode = True
from release_channel import channel_for_version
out=pathlib.Path(sys.argv[1]);version=sys.argv[2]
try: channel=channel_for_version(version,os.environ.get("KRM_RELEASE_CHANNEL"))
except ValueError as exc: raise SystemExit(str(exc))
source=pathlib.Path(__file__).resolve().parent.parent
key_path=pathlib.Path(os.environ.get('KRM_RELEASE_PUBLIC_KEY','internal/releasetrust/public.key'))
pub=base64.b64decode(key_path.read_text().strip()+'===')
if len(pub)!=32: raise SystemExit('invalid release public key')
der=bytes.fromhex('302a300506032b6570032100')+pub
b64=base64.b64encode(der).decode()
pem='-----BEGIN PUBLIC KEY-----\n'+'\n'.join(b64[i:i+64] for i in range(0,len(b64),64))+'\n-----END PUBLIC KEY-----'
template=pathlib.Path('install/bootstrap.sh').read_text()
for platform,name in [('keenetic','keenetic'),('openwrt','openwrt'),('linux-systemd','linux')]:
 s=template.replace('@VERSION@',version).replace('@CHANNEL@',channel).replace('@PLATFORM@',platform).replace('@PUBLIC_PEM@',pem)
 (out/f'bootstrap-{name}.sh').write_text(s)
 (out/f'bootstrap-{name}.sh').chmod(0o755)
files=[]
for f in sorted(out.iterdir()):
 if f.is_file() and f.name!='SBOM.spdx.json' and not f.name.startswith(('manifest-','SHA256SUMS')):
  files.append({'SPDXID':'SPDXRef-File-'+f.name.replace('.','-'),'fileName':'./'+f.name,'checksums':[{'algorithm':'SHA256','checksumValue':hashlib.sha256(f.read_bytes()).hexdigest()}],'licenseConcluded':'NOASSERTION','copyrightText':'NOASSERTION'})
go_version=subprocess.check_output(['go','version'],text=True).strip()
pkgid='SPDXRef-Package-KRM'
commit=(os.environ.get('KRM_SOURCE_COMMIT') or subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip())
sbom={'spdxVersion':'SPDX-2.3','dataLicense':'CC0-1.0','SPDXID':'SPDXRef-DOCUMENT','name':'kee-route-manager-'+version,'documentNamespace':f'https://github.com/jarymor-ux/kee-route-manager/sbom/{version}/{commit}', 'creationInfo':{'creators':['Tool: krm-release-builder','Organization: Kee Route Manager'],'created':datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')},'packages':[{'SPDXID':pkgid,'name':'kee-route-manager','versionInfo':version,'downloadLocation':f'https://github.com/jarymor-ux/kee-route-manager/tree/v{version}','filesAnalyzed':False,'licenseConcluded':'Apache-2.0','licenseDeclared':'Apache-2.0','copyrightText':'2026 Kee Route Manager contributors','comment':'Built with '+go_version+'; runtime modules are inventoried below. This inventory records distributable files, not a transitive stdlib file SBOM.'}], 'files':files,'relationships':[{'spdxElementId':'SPDXRef-DOCUMENT','relationshipType':'DESCRIBES','relatedSpdxElement':pkgid}]+[{'spdxElementId':pkgid,'relationshipType':'CONTAINS','relatedSpdxElement':f['SPDXID']} for f in files]}
# Include only modules reached by production commands, not test-only dependencies.
runtime=set()
for arch in ('amd64','arm64','arm','mipsle'):
 build_env=os.environ.copy();build_env.update(CGO_ENABLED='0',GOOS='linux',GOARCH=arch,GOARM='7' if arch=='arm' else '',GOMIPS='softfloat' if arch=='mipsle' else '')
 runtime.update(subprocess.check_output(['go','list','-deps','-f','{{if .Module}}{{.Module.Path}}{{end}}','./cmd/...'],cwd=source,env=build_env,text=True).split())
module_json=subprocess.check_output(['go','list','-m','-json','all'],cwd=source,text=True)
decoder=json.JSONDecoder()
while module_json.strip():
 module,offset=decoder.raw_decode(module_json.lstrip());module_json=module_json.lstrip()[offset:]
 if module.get('Main') or module['Path'] not in runtime: continue
 if module.get('Replace'): raise SystemExit('release inventory refuses replaced runtime modules')
 path=module['Path'];module_id='SPDXRef-Package-Go-'+re.sub(r'[^A-Za-z0-9.-]','-',path)
 license_id={'go.yaml.in/yaml/v3':'MIT AND Apache-2.0'}.get(path,'NOASSERTION')
 sbom['packages'].append({'SPDXID':module_id,'name':path,'versionInfo':module['Version'],'downloadLocation':'NOASSERTION','filesAnalyzed':False,'licenseConcluded':'NOASSERTION','licenseDeclared':license_id,'copyrightText':'NOASSERTION','externalRefs':[{'referenceCategory':'PACKAGE-MANAGER','referenceType':'purl','referenceLocator':'pkg:golang/'+path+'@'+module['Version']}],'comment':'Resolved runtime Go module; content checksum '+module.get('Sum','unavailable')})
 sbom['relationships'].append({'spdxElementId':pkgid,'relationshipType':'DEPENDS_ON','relatedSpdxElement':module_id})
(out/'SBOM.spdx.json').write_text(json.dumps(sbom,indent=2)+'\n')
