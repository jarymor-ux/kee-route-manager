#!/usr/bin/env python3
"""Regression coverage for Git identity and stable/RC branch authorization."""
import importlib.util
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('build_version', pathlib.Path(__file__).with_name('build-version.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class VersionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)
        self.env = patch.dict(os.environ, {'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1'})
        self.env.start()
        self.addCleanup(self.env.stop)
        for name in list(os.environ):
            if name.startswith('GIT_') and name not in ('GIT_CONFIG_GLOBAL', 'GIT_CONFIG_NOSYSTEM') or name in ('KRM_RELEASE_CHANNEL', 'KRM_RELEASE_TAG'):
                os.environ.pop(name)
        empty = self.root / 'empty-template'
        empty.mkdir()
        self.git('init', '--quiet', '--initial-branch=main', '--template=' + str(empty))
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.git('config', 'tag.gpgSign', 'false')
        (self.root / '.gitignore').write_text('/empty-template/\n/dist/\n')
        self.git('add', '.gitignore')
        self.git('commit', '--quiet', '-m', 'fixture baseline')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], text=True, stderr=subprocess.DEVNULL).strip()

    def test_untagged_build_is_development_and_cannot_publish(self):
        self.assertRegex(module.resolve(self.root), r'^0\.0\.0-dev\.g[0-9a-f]{12}$')
        with self.assertRaises(ValueError):
            module.resolve(self.root, release=True)

    def test_exact_stable_and_rc_tags(self):
        for tag in ('v1.1.0', 'v1.2.0-rc.15'):
            with self.subTest(tag=tag):
                self.git('tag', tag)
                self.assertEqual(module.resolve(self.root, True, tag), tag[1:])
                self.assertEqual(module.resolve(self.root), tag[1:])
                self.git('tag', '-d', tag)

    def test_multiple_release_tags_require_explicit_publication_identity(self):
        self.git('tag', 'v1.1.0-rc.15')
        self.git('tag', 'v1.1.0')
        with self.assertRaises(ValueError):
            module.resolve(self.root, release=True)
        self.assertEqual(module.resolve(self.root, True, 'v1.1.0'), '1.1.0')
        self.assertEqual(module.resolve(self.root, True, 'v1.1.0-rc.15'), '1.1.0-rc.15')
        self.assertIn('-dev.', module.resolve(self.root))

    def test_dirty_build_is_marked_and_release_refused(self):
        self.git('tag', 'v1.1.0')
        (self.root / 'changed').write_text('operator edit')
        self.assertRegex(module.resolve(self.root), r'^1\.1\.0\+dirty\.g[0-9a-f]{12}$')
        with self.assertRaises(ValueError):
            module.resolve(self.root, True, 'v1.1.0')
        (self.root / 'changed').unlink()
        (self.root / 'dist').mkdir()
        (self.root / 'dist' / 'output').write_text('ignored build output')
        self.assertEqual(module.resolve(self.root, True, 'v1.1.0'), '1.1.0')

    def test_tag_on_different_commit_refused(self):
        self.git('tag', 'v1.1.0')
        self.git('commit', '--quiet', '--allow-empty', '-m', 'later source')
        with self.assertRaises(ValueError):
            module.resolve(self.root, True, 'v1.1.0')

    def test_invalid_and_nonrelease_tags_cannot_publish(self):
        for tag in ('1.1.0', 'v01.1.0', 'v1.1.0-beta.1', 'v1.1.0-rc.01', 'v1.1.0+build', '--all', '../v1.1.0'):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                module.resolve(self.root, True, tag)
        self.git('tag', 'test-only')
        with self.assertRaises(ValueError):
            module.resolve(self.root, release=True)

    def test_annotated_tag_is_bound_to_commit(self):
        self.git('tag', '-a', 'v1.1.0', '-m', 'fixture annotation')
        self.assertEqual(module.resolve(self.root, True), '1.1.0')

    def test_channel_override_cannot_relabel_tag(self):
        self.git('tag', 'v1.1.0')
        with patch.dict(os.environ, {'KRM_RELEASE_CHANNEL': 'rc'}), self.assertRaises(ValueError):
            module.resolve(self.root, release=True)

    def test_branches_gate_rc_and_stable_then_allow_promotion(self):
        self.git('update-ref', 'refs/remotes/origin/main', 'HEAD')
        self.git('checkout', '--quiet', '-b', 'release-candidate')
        self.git('commit', '--quiet', '--allow-empty', '-m', 'candidate only')
        self.git('tag', 'v1.1.0-rc.15')
        self.git('tag', 'v1.1.0')
        self.git('update-ref', 'refs/remotes/origin/release-candidate', 'HEAD')
        self.assertEqual(module.resolve(self.root, True, 'v1.1.0-rc.15', True), '1.1.0-rc.15')
        with self.assertRaises(ValueError):
            module.resolve(self.root, True, 'v1.1.0', True)
        self.git('update-ref', 'refs/remotes/origin/main', 'HEAD')
        self.assertEqual(module.resolve(self.root, True, 'v1.1.0', True), '1.1.0')
        self.git('update-ref', '-d', 'refs/remotes/origin/release-candidate')
        with self.assertRaises(ValueError):
            module.resolve(self.root, True, 'v1.1.0-rc.15', True)

    def test_archive_is_development_only_and_not_parent_git_identity(self):
        archive = self.root / 'archive'
        archive.mkdir()
        self.assertEqual(module.resolve(archive), '0.0.0-dev.unknown')
        with self.assertRaises(ValueError):
            module.resolve(archive, True, 'v1.1.0')

    def test_missing_git_is_development_only(self):
        with patch.object(module.subprocess, 'run', side_effect=FileNotFoundError):
            self.assertEqual(module.resolve(self.root), '0.0.0-dev.unknown')
            with self.assertRaises(ValueError):
                module.resolve(self.root, release=True)


if __name__ == '__main__':
    unittest.main()
