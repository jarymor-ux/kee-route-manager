#!/usr/bin/env python3
"""Build deterministic router dependencies from reviewed, SHA-256-pinned sources.

Downloaded programs are never executed. Run this before signing the KRM release
manifest so every resulting archive participates in the same release signature.
Bundled XKeen removes its installer-only runtime migrations; module paths and
the separately distributed upstream BSD license remain unchanged.
"""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import tarfile
import tempfile
import urllib.parse
import urllib.request
import zipfile

MAX_COMPRESSED = 128 * 1024 * 1024
MAX_EXTRACTED = 200 * 1024 * 1024
ARCHES = ('amd64', 'arm64', 'armv7', 'mipsle', 'mips')
REPOS = {'jameszeroX/XKeen', 'XTLS/Xray-core', 'v2fly/domain-list-community'}
INIT_TEMPLATE = '_xkeen/02_install/07_install_register/04_register_init.sh'
LICENSE_DIR = 'share/kee-route-manager/licenses/'
# Exact reviewed XKeen 2.1 block. Fail closed on upstream changes rather than
# letting every status/restart import a retained installer tree or delete files.
XKEEN_INSTALLER_MIGRATION = '''# Скрываем основную директорию xkeen
install_xkeen_rename() {
    local source_dir="_xkeen"
    local target_dir=".xkeen"
    local source_path="$script_dir/$source_dir"
    local target_path="$script_dir/$target_dir"

    if [ -d "$source_path" ]; then
        if [ -d "$target_path" ]; then
            rm -rf "$target_path" 2>/dev/null
        fi
        mv "$source_path" "$target_path"
    fi
    rm "/opt/root/install.sh" 2>/dev/null
}
install_xkeen_rename
'''.encode()


def validate_url(url):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != 'https' or parsed.username or parsed.password or parsed.port or parsed.query or parsed.fragment:
        raise ValueError('source URL must use HTTPS without credentials, query, or fragment')
    parts = parsed.path.strip('/').split('/')
    if '/'.join(parts[:2]) not in REPOS:
        raise ValueError('source URL repository is not trusted')
    if parsed.hostname == 'github.com':
        valid = len(parts) >= 6 and parts[2:4] == ['releases', 'download'] and parts[4] not in ('latest', 'main', 'master')
    elif parsed.hostname == 'raw.githubusercontent.com':
        valid = len(parts) >= 4 and re.fullmatch(r'[0-9a-f]{40}', parts[2]) is not None
    else:
        valid = False
    if not valid:
        raise ValueError('source URL must use a pinned release or commit on a trusted host')


class HTTPSRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        parsed = urllib.parse.urlsplit(newurl)
        if (parsed.scheme != 'https' or parsed.username or parsed.password or parsed.port
                or parsed.hostname not in ('github.com', 'raw.githubusercontent.com',
                                          'release-assets.githubusercontent.com', 'objects.githubusercontent.com')):
            raise ValueError('untrusted or non-HTTPS download redirect')
        return super().redirect_request(request, fp, code, message, headers, newurl)


def limited_read(stream, limit):
    data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError('download or extracted size exceeds limit')
    return data


def fetch(asset, cache):
    validate_url(asset['url'])
    digest = asset['sha256']
    if not isinstance(digest, str) or re.fullmatch(r'[0-9a-f]{64}', digest) is None:
        raise ValueError('invalid SHA-256 pin')
    cached = cache / digest
    if cached.exists():
        if not cached.is_file() or cached.is_symlink():
            raise ValueError('cache entry must be a regular file')
        with cached.open('rb') as source:
            data = limited_read(source, MAX_COMPRESSED)
    else:
        opener = urllib.request.build_opener(HTTPSRedirects())
        request = urllib.request.Request(asset['url'], headers={'User-Agent': 'KRM-release-packer/1'})
        with opener.open(request, timeout=60) as response:
            data = limited_read(response, MAX_COMPRESSED)
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError('SHA-256 mismatch for ' + asset['url'])
    if not cached.exists():
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as target:
            temporary = Path(target.name)
            try:
                target.write(data)
                target.flush()
                os.replace(temporary, cached)
            finally:
                temporary.unlink(missing_ok=True)
    return data


def check_name(name, names):
    if (not name or name.startswith('/') or '\\' in name or '\x00' in name
            or any(part in ('', '.', '..') for part in name.split('/'))):
        raise ValueError('unsafe archive path: ' + repr(name))
    if name in names:
        raise ValueError('duplicate archive path: ' + name)
    names.add(name)


def unpack(data, kind):
    result = {}
    names = set()
    total = 0
    if kind == 'tar':
        archive = tarfile.open(fileobj=io.BytesIO(data), mode='r|gz')
    else:
        archive = zipfile.ZipFile(io.BytesIO(data))
    with archive:
        members = archive if kind == 'tar' else archive.infolist()
        for member in members:
            name = member.name if kind == 'tar' else member.filename
            check_name(name, names)
            if kind == 'tar':
                regular = member.isfile() and member.sparse is None
                size = member.size
            else:
                mode = member.external_attr >> 16
                regular = not member.is_dir() and stat.S_IFMT(mode) in (0, stat.S_IFREG)
                size = member.file_size
            if not regular:
                raise ValueError('archive entry must be a regular file: ' + name)
            total += size
            if size < 0 or total > MAX_EXTRACTED:
                raise ValueError('archive extracted size exceeds limit')
            source = archive.extractfile(member) if kind == 'tar' else archive.open(member)
            with source:
                value = limited_read(source, size)
            if len(value) != size:
                raise ValueError('archive member size mismatch: ' + name)
            result[name] = value
    return result


def required(files, name):
    if name not in files or not files[name]:
        raise ValueError('missing required archive member: ' + name)
    return files[name]


def runtime_xkeen(entrypoint):
    if entrypoint.count(XKEEN_INSTALLER_MIGRATION) != 1:
        raise ValueError('unexpected XKeen installer migration template')
    runtime = entrypoint.replace(
        XKEEN_INSTALLER_MIGRATION,
        b'# KRM: installer-only migrations removed from bundled runtime.\n')
    if b'install_xkeen_rename' in runtime or b'/opt/root/install.sh' in runtime:
        raise ValueError('unexpected XKeen installer migration template remains')
    return runtime


def write_package(path, files):
    if sum(len(data) for data in files.values()) > MAX_EXTRACTED:
        raise ValueError('package extracted size exceeds limit')
    with path.open('wb') as raw:
        with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w', format=tarfile.USTAR_FORMAT) as archive:
                for name, data in sorted(files.items()):
                    member = tarfile.TarInfo(name)
                    member.mode = 0o755 if name in ('sbin/xray', 'sbin/xkeen', 'etc/init.d/S05xkeen') else 0o644
                    member.size = len(data)
                    member.uid = member.gid = member.mtime = 0
                    member.uname = member.gname = ''
                    archive.addfile(member, io.BytesIO(data))
    if path.stat().st_size > MAX_COMPRESSED:
        raise ValueError('package compressed size exceeds limit')


def build(out, lock, cache):
    """Build all architectures; tests supply synthetic pins and a populated cache."""
    out, cache = Path(out), Path(cache)
    if isinstance(lock, (str, Path)):
        lock = json.loads(Path(lock).read_text())
    if lock.get('schema') != 1 or set(lock['xray']['archives']) != set(ARCHES):
        raise ValueError('unsupported dependency lock schema or architecture set')
    cache.mkdir(parents=True, exist_ok=True)
    xkeen = unpack(fetch(lock['xkeen'], cache), 'tar')
    required(xkeen, 'xkeen')
    template = required(xkeen, INIT_TEMPLATE)
    if template.count(b'\nstart_auto="on"\n') != 1:
        raise ValueError('unexpected XKeen autostart template')
    common = {}
    for name, data in xkeen.items():
        if name == 'xkeen':
            common['sbin/xkeen'] = runtime_xkeen(data)
        elif name.startswith('_xkeen/'):
            common['sbin/.xkeen/' + name[len('_xkeen/'):]] = data
        else:
            raise ValueError('unexpected XKeen archive member: ' + name)
    common['etc/init.d/S05xkeen'] = template.replace(b'\nstart_auto="on"\n', b'\nstart_auto="off"\n')
    common['etc/xray/dat/geosite_v2fly.dat'] = fetch(lock['geosite'], cache)
    common[LICENSE_DIR + 'XKeen.txt'] = fetch(lock['xkeen']['license'], cache)
    common[LICENSE_DIR + 'domain-list-community.txt'] = fetch(lock['geosite']['license'], cache)
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.router-deps-', dir=out) as stage:
        for arch in ARCHES:
            xray = unpack(fetch(lock['xray']['archives'][arch], cache), 'zip')
            files = dict(common)
            binary = 'xray_softfloat' if arch in ('mipsle', 'mips') else 'xray'
            files['sbin/xray'] = required(xray, binary)
            files[LICENSE_DIR + 'Xray.txt'] = required(xray, 'LICENSE')
            write_package(Path(stage) / f'router-deps-linux-{arch}.tar.gz', files)
        for arch in ARCHES:
            name = f'router-deps-linux-{arch}.tar.gz'
            os.replace(Path(stage) / name, out / name)
    return [out / f'router-deps-linux-{arch}.tar.gz' for arch in ARCHES]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dist', type=Path)
    parser.add_argument('--cache', type=Path, help='external download cache directory')
    args = parser.parse_args()
    lock = Path(__file__).resolve().parent.parent / 'install/router-dependencies.json'
    if args.cache is None:
        with tempfile.TemporaryDirectory(prefix='krm-router-deps-') as temporary:
            paths = build(args.dist, lock, Path(temporary))
    else:
        paths = build(args.dist, lock, args.cache)
    for path in paths:
        print(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}')


if __name__ == '__main__':
    main()
