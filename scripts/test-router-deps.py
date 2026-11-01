#!/usr/bin/env python3
"""Offline regressions for the router dependency release packer."""
import copy
import hashlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import sys
import unittest
from unittest import mock
import zipfile

PACKER = Path(__file__).with_name('prepare-router-deps.py')
sys.dont_write_bytecode = True
XKEEN_INSTALLER_BLOCK = '''# Скрываем основную директорию xkeen
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
XKEEN_SCRIPT = (b'#!/bin/sh\nscript_dir="$(cd "$(dirname "$0")" && pwd)"\n\n'
                + XKEEN_INSTALLER_BLOCK + b'\n. "$script_dir/.xkeen/import.sh"\n')


def load_packer():
    spec = importlib.util.spec_from_file_location('router_deps', PACKER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def tar_bytes(entries):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w:gz') as archive:
        for name, value in entries:
            member = tarfile.TarInfo(name)
            if isinstance(value, bytes):
                member.size = len(value)
                archive.addfile(member, io.BytesIO(value))
            else:
                member.type = value
                member.linkname = 'xkeen'
                archive.addfile(member)
    return stream.getvalue()


def zip_bytes(entries):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, 'w') as archive:
        for name, data in entries:
            archive.writestr(name, data)
    return stream.getvalue()


class RouterDepsTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(PACKER.is_file(), 'router dependency release packer is missing')
        self.packer = load_packer()
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.cache = self.root / 'cache'
        self.cache.mkdir()
        self.lock = {
            'schema': 1,
            'xkeen': dict(self.asset(tar_bytes([
                ('xkeen', XKEEN_SCRIPT),
                ('_xkeen/import.sh', b'# modules\n'),
                ('_xkeen/02_install/07_install_register/04_register_init.sh',
                 b'#!/bin/sh\nstart_auto="on"\n')]), 'jameszeroX/XKeen'),
                version='2.1', license=self.asset(b'BSD license', 'jameszeroX/XKeen')),
            'xray': {'version': 'v26.10.31', 'archives': {}},
            'geosite': dict(self.asset(b'geosite category-ru', 'v2fly/domain-list-community'),
                            version='20261101000000',
                            license=self.asset(b'MIT license', 'v2fly/domain-list-community')),
        }
        for arch in ('amd64', 'arm64', 'armv7', 'mipsle', 'mips'):
            entries = [('xray', b'ELF-' + arch.encode()), ('LICENSE', b'MPL license'),
                       ('geosite.dat', b'not selected')]
            if arch in ('mipsle', 'mips'):
                entries.append(('xray_softfloat', b'SOFTFLOAT-' + arch.encode()))
            self.lock['xray']['archives'][arch] = self.asset(
                zip_bytes(entries), 'XTLS/Xray-core')

    def asset(self, data, repo):
        digest = hashlib.sha256(data).hexdigest()
        (self.cache / digest).write_bytes(data)
        return {'url': f'https://github.com/{repo}/releases/download/pinned/{digest}',
                'sha256': digest}

    def build(self, lock=None, name='out'):
        with mock.patch('urllib.request.build_opener', side_effect=AssertionError('network in test')):
            self.packer.build(self.root / name, lock or self.lock, self.cache)
        return self.root / name

    def test_package_layout_and_reproducibility(self):
        first = self.build()
        second = self.build(name='second')
        self.assertEqual(len(list(first.iterdir())), 5)
        for path in first.iterdir():
            self.assertEqual(path.read_bytes(), (second / path.name).read_bytes())
            self.assertEqual(path.read_bytes()[4:8], b'\0' * 4)
            with tarfile.open(path) as archive:
                names = archive.getnames()
                self.assertEqual(names, sorted(names))
                self.assertEqual(set(names), {
                    'sbin/xray', 'sbin/xkeen', 'sbin/.xkeen/import.sh',
                    'sbin/.xkeen/02_install/07_install_register/04_register_init.sh',
                    'etc/init.d/S05xkeen', 'etc/xray/dat/geosite_v2fly.dat',
                    'share/kee-route-manager/licenses/XKeen.txt',
                    'share/kee-route-manager/licenses/Xray.txt',
                    'share/kee-route-manager/licenses/domain-list-community.txt'})
                self.assertIn(b'start_auto="off"', archive.extractfile('etc/init.d/S05xkeen').read())
                self.assertEqual(archive.extractfile('etc/xray/dat/geosite_v2fly.dat').read(), b'geosite category-ru')
                for member in archive.getmembers():
                    self.assertTrue(member.isfile())
                    self.assertEqual((member.uid, member.gid, member.mtime), (0, 0, 0))
                    self.assertEqual(member.mode, 0o755 if member.name in ('sbin/xray', 'sbin/xkeen', 'etc/init.d/S05xkeen') else 0o644)

    def test_digest_mismatch_rejected(self):
        (self.cache / self.lock['xkeen']['sha256']).write_bytes(b'tampered')
        with self.assertRaisesRegex(ValueError, 'SHA-256'):
            self.build()

    def test_mips_requires_softfloat_binary(self):
        out = self.build()
        for arch in ('mipsle', 'mips'):
            with tarfile.open(out / f'router-deps-linux-{arch}.tar.gz') as archive:
                self.assertEqual(archive.extractfile('sbin/xray').read(), b'SOFTFLOAT-' + arch.encode())
        lock = copy.deepcopy(self.lock)
        lock['xray']['archives']['mips'] = self.asset(
            zip_bytes([('xray', b'hardfloat'), ('LICENSE', b'license')]), 'XTLS/Xray-core')
        with self.assertRaisesRegex(ValueError, 'xray_softfloat'):
            self.build(lock, name='missing-softfloat')

    def test_unsafe_tar_members_rejected(self):
        for entries in [[('../escape', b'x')], [('/absolute', b'x')],
                        [('xkeen', tarfile.SYMTYPE)], [('xkeen', tarfile.LNKTYPE)],
                        [('xkeen', b'x'), ('xkeen', b'y')]]:
            with self.subTest(entries=entries):
                lock = copy.deepcopy(self.lock)
                lock['xkeen'].update(self.asset(tar_bytes(entries), 'jameszeroX/XKeen'))
                with self.assertRaises(ValueError):
                    self.build(lock)

    def test_unsafe_zip_members_rejected(self):
        bad_archives = [zip_bytes([('../escape', b'x')]), zip_bytes([('/absolute', b'x')])]
        stream = io.BytesIO()
        with zipfile.ZipFile(stream, 'w') as archive:
            member = zipfile.ZipInfo('xray')
            member.create_system = 3
            member.external_attr = 0o120777 << 16
            archive.writestr(member, 'elsewhere')
        bad_archives.append(stream.getvalue())
        with mock.patch('warnings.warn'):
            bad_archives.append(zip_bytes([('xray', b'x'), ('xray', b'y')]))
        for data in bad_archives:
            with self.subTest(digest=hashlib.sha256(data).hexdigest()):
                lock = copy.deepcopy(self.lock)
                lock['xray']['archives']['amd64'] = self.asset(data, 'XTLS/Xray-core')
                with self.assertRaises(ValueError):
                    self.build(lock)

    def test_archive_size_limit_rejected(self):
        with mock.patch.object(self.packer, 'MAX_EXTRACTED', 10):
            with self.assertRaisesRegex(ValueError, 'size|limit'):
                self.build()

    def test_compressed_size_limit_rejected(self):
        with mock.patch.object(self.packer, 'MAX_COMPRESSED', 10):
            with self.assertRaisesRegex(ValueError, 'size|limit'):
                self.build()

    def test_wrong_host_and_mutable_refs_rejected(self):
        for url in ['http://github.com/jameszeroX/XKeen/releases/download/2.1/xkeen.tar.gz',
                    'https://evil.example/xkeen.tar.gz',
                    'https://github.com/jameszeroX/XKeen/releases/latest/download/xkeen.tar.gz',
                    'https://raw.githubusercontent.com/jameszeroX/XKeen/main/LICENSE']:
            lock = copy.deepcopy(self.lock)
            lock['xkeen']['url'] = url
            with self.subTest(url=url), self.assertRaises(ValueError):
                self.build(lock)

    def test_missing_required_member_rejected(self):
        lock = copy.deepcopy(self.lock)
        lock['xkeen'].update(self.asset(tar_bytes([('xkeen', b'x')]), 'jameszeroX/XKeen'))
        with self.assertRaises(ValueError):
            self.build(lock)

    def test_failed_final_architecture_publishes_no_packages(self):
        lock = copy.deepcopy(self.lock)
        lock['xray']['archives']['mips'] = self.asset(
            zip_bytes([('README.md', b'no executable')]), 'XTLS/Xray-core')
        with self.assertRaises(ValueError):
            self.build(lock)
        self.assertEqual(list((self.root / 'out').iterdir()), [])

    def test_changed_init_template_rejected(self):
        lock = copy.deepcopy(self.lock)
        lock['xkeen'].update(self.asset(tar_bytes([
            ('xkeen', b'script'),
            ('_xkeen/02_install/07_install_register/04_register_init.sh',
             b'start_auto="unexpected"\n')]), 'jameszeroX/XKeen'))
        with self.assertRaisesRegex(ValueError, 'autostart template'):
            self.build(lock)

    def test_runtime_preserves_legacy_tree_and_unrelated_installer(self):
        out = self.build()
        with tarfile.open(out / 'router-deps-linux-amd64.tar.gz') as archive:
            script = archive.extractfile('sbin/xkeen').read()
            self.assertEqual(archive.extractfile('share/kee-route-manager/licenses/XKeen.txt').read(), b'BSD license')
        # Redirect the old absolute installer path in this test copy only. Even
        # the vulnerable input can then execute solely within this private tree.
        for command in ('-status', '-restart', '-h'):
            with self.subTest(command=command):
                root = self.root / command.lstrip('-')
                current = root / '.xkeen'
                legacy = root / '_xkeen'
                current.mkdir(parents=True)
                legacy.mkdir()
                (current / 'import.sh').write_text("printf 'current modules\\n'; exit 0\n")
                (legacy / 'import.sh').write_text("printf 'retained legacy modules\\n'; exit 0\n")
                sentinel = root / 'install.sh'
                sentinel.write_text('unrelated installer\n')
                runtime = root / 'xkeen'
                runtime.write_bytes(script.replace(b'/opt/root/install.sh', os.fsencode(sentinel)))
                result = subprocess.run(['sh', str(runtime), command], check=True,
                                        capture_output=True, text=True, timeout=5,
                                        env=dict(os.environ, XKEEN_FOREGROUND='1'))
                self.assertEqual((result.stdout, legacy.is_dir(), sentinel.is_file()),
                                 ('current modules\n', True, True))
                self.assertEqual((legacy / 'import.sh').read_text(), "printf 'retained legacy modules\\n'; exit 0\n")
                self.assertEqual(sentinel.read_text(), 'unrelated installer\n')

    def test_changed_installer_migration_template_rejected(self):
        lock = copy.deepcopy(self.lock)
        lock['xkeen'].update(self.asset(tar_bytes([
            ('xkeen', XKEEN_SCRIPT.replace(b'local source_dir="_xkeen"', b'local source_dir="changed"')),
            ('_xkeen/import.sh', b'exit 0\n'),
            ('_xkeen/02_install/07_install_register/04_register_init.sh',
             b'#!/bin/sh\nstart_auto="on"\n')]), 'jameszeroX/XKeen'))
        with self.assertRaisesRegex(ValueError, 'installer migration template'):
            self.build(lock)


if __name__ == '__main__':
    unittest.main()
