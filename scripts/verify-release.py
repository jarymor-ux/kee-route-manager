#!/usr/bin/env python3
"""Verify channel binding, Ed25519 signatures and every signed release file."""
import base64, hashlib, json, os, pathlib, re, subprocess, sys, tempfile
sys.dont_write_bytecode = True
from release_channel import channel_for_version

p = pathlib.Path(sys.argv[1]).resolve()
manifests = [p / ('manifest-' + channel + '.json') for channel in ('rc', 'stable') if (p / ('manifest-' + channel + '.json')).exists()]
if len(manifests) != 1:
    raise SystemExit('release must contain exactly one rc/stable manifest')
manifest = manifests[0]
name = manifest.name
key_path = pathlib.Path(os.environ.get('KRM_RELEASE_PUBLIC_KEY', 'internal/releasetrust/public.key'))
pub = base64.b64decode(key_path.read_text().strip() + '===')
if len(pub) != 32:
    raise SystemExit('invalid release public key')
with tempfile.TemporaryDirectory() as d:
    d = pathlib.Path(d)
    (d / 'pub.der').write_bytes(bytes.fromhex('302a300506032b6570032100') + pub)
    for signed_name in (name, 'SHA256SUMS'):
        for filename in (signed_name, signed_name + '.sig'):
            if (p / filename).is_symlink() or not (p / filename).is_file():
                raise SystemExit('missing/nonregular signed file ' + filename)
        (d / 'sig').write_bytes(base64.b64decode((p / (signed_name + '.sig')).read_text().strip() + '==='))
        subprocess.run(['openssl', 'pkeyutl', '-verify', '-pubin', '-keyform', 'DER', '-inkey', str(d / 'pub.der'), '-rawin', '-in', str(p / signed_name), '-sigfile', str(d / 'sig')], check=True, stdout=subprocess.DEVNULL)
m = json.loads(manifest.read_text())
try:
    channel = channel_for_version(m.get('version'), m.get('channel'))
except ValueError as exc:
    raise SystemExit(str(exc))
if m.get('schema_version') != 1 or m.get('update_protocol') != 1 or m.get('channel') != channel or name != 'manifest-' + channel + '.json':
    raise SystemExit('unsupported release schema/update protocol/channel')
seen = set()
for asset in m['assets']:
    asset_name = asset['name']
    if pathlib.Path(asset_name).name != asset_name or asset_name in seen:
        raise SystemExit('unsafe/duplicate asset')
    seen.add(asset_name)
    f = p / asset_name
    if f.is_symlink() or not f.is_file():
        raise SystemExit('missing/nonregular asset ' + asset_name)
    data = f.read_bytes()
    if len(data) != asset['size'] or hashlib.sha256(data).hexdigest() != asset['sha256']:
        raise SystemExit('manifest mismatch ' + asset_name)
    if asset['url'] != f'https://github.com/jarymor-ux/kee-route-manager/releases/download/v{m["version"]}/{asset_name}':
        raise SystemExit('not version pinned')
checksummed = set()
for line in (p / 'SHA256SUMS').read_text().splitlines():
    digest, filename = line.split('  ', 1)
    f = p / filename
    if pathlib.Path(filename).name != filename or filename in checksummed or not re.fullmatch('[0-9a-f]{64}', digest):
        raise SystemExit('unsafe/duplicate checksum filename or digest')
    if f.is_symlink() or not f.is_file():
        raise SystemExit('missing/nonregular checksum file ' + filename)
    checksummed.add(filename)
    if hashlib.sha256(f.read_bytes()).hexdigest() != digest:
        raise SystemExit('checksum mismatch ' + filename)
required = {f'{component}-linux-{arch}' for component in ('kee-route-managerd', 'kee-route-manager-ui', 'kee-route-managerctl', 'kee-route-manager-launcher', 'krm-release-tool') for arch in ('amd64', 'arm64', 'armv7', 'mipsle')} | {'bootstrap-keenetic.sh', 'bootstrap-openwrt.sh', 'bootstrap-linux.sh', 'release-files.tar.gz', 'SBOM.spdx.json'}
if not required <= seen:
    raise SystemExit('missing required assets ' + str(required - seen))
expected = seen | {name, name + '.sig'}
if checksummed != expected:
    raise SystemExit('checksum inventory does not match manifest and signature')
actual = {f.name for f in p.iterdir()}
if actual != expected | {'SHA256SUMS', 'SHA256SUMS.sig'}:
    raise SystemExit('unexpected release directory contents')
print(f'Verified {channel} signatures, sizes, digests and required manifest assets ({len(seen)} files).')
