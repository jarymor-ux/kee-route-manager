#!/usr/bin/env python3
"""Resolve build identity from Git, keeping development builds out of publication."""
import argparse
import os
import pathlib
import subprocess
import sys

sys.dont_write_bytecode = True
from release_channel import channel_for_version


def git(root, *args, required=True):
    try:
        result = subprocess.run(['git', '--no-replace-objects', '-C', str(root), *args],
                                capture_output=True, text=True)
    except FileNotFoundError:
        if required:
            raise ValueError('Git is required for release identity') from None
        return ''
    if result.returncode and required:
        raise ValueError('Git identity unavailable: ' + ' '.join(args))
    return result.stdout.strip() if result.returncode == 0 else ''


def tag_version(tag):
    if not tag.startswith('v') or len(tag) > 128:
        raise ValueError('release tag must be vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N')
    version = tag[1:]
    channel_for_version(version)
    return version


def resolve(root, release=False, requested='', check_branch=False):
    root = pathlib.Path(root).resolve()
    top = git(root, 'rev-parse', '--show-toplevel', required=False)
    if not top or pathlib.Path(top).resolve() != root:
        if release or requested or check_branch:
            raise ValueError('a release requires its original Git checkout and exact tag')
        return '0.0.0-dev.unknown'
    head = git(root, 'rev-parse', '--verify', 'HEAD')
    dirty = bool(git(root, 'status', '--porcelain', '--untracked-files=normal'))
    exact = []
    for tag in git(root, 'tag', '--points-at', 'HEAD').splitlines():
        try:
            tag_version(tag)
            exact.append(tag)
        except ValueError:
            continue
    if requested:
        version = tag_version(requested)
        if git(root, 'rev-parse', '--verify', 'refs/tags/' + requested + '^{commit}', required=False) != head:
            raise ValueError('requested release tag does not identify HEAD')
    elif len(exact) == 1:
        requested = exact[0]
        version = tag_version(requested)
    elif release:
        raise ValueError('release needs exactly one official HEAD tag or KRM_RELEASE_TAG')
    else:
        version = ''
    if release:
        if dirty:
            raise ValueError('release requires a clean Git checkout')
        channel = channel_for_version(version, os.environ.get('KRM_RELEASE_CHANNEL'))
        if check_branch:
            branch = 'release-candidate' if channel == 'rc' else 'main'
            result = subprocess.run(['git', '--no-replace-objects', '-C', str(root),
                                     'merge-base', '--is-ancestor', head, 'refs/remotes/origin/' + branch],
                                    capture_output=True, text=True)
            if result.returncode:
                raise ValueError('release tag must belong to origin/' + branch)
        return version
    if version:
        return version + ('+dirty.g' + head[:12] if dirty else '')
    # Untagged commits have their own visible identity, without predicting the
    # next release number or treating a moving branch as an installation source.
    return '0.0.0-dev.g' + head[:12] + ('.dirty' if dirty else '')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', action='store_true')
    parser.add_argument('--check-branch', action='store_true')
    args = parser.parse_args()
    try:
        if args.check_branch and not args.release:
            raise ValueError('--check-branch requires --release')
        print(resolve(pathlib.Path(__file__).resolve().parent.parent, args.release,
                      os.environ.get('KRM_RELEASE_TAG', ''), args.check_branch))
    except ValueError as exc:
        raise SystemExit(str(exc))
