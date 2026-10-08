#!/usr/bin/env python3
"""Bind an official release version to exactly one publication channel."""
import re
import sys

_NUMBER = r"(?:0|[1-9][0-9]*)"
_VERSION = re.compile(rf"{_NUMBER}\.{_NUMBER}\.{_NUMBER}(?:-rc\.{_NUMBER})?\Z")


def channel_for_version(version, requested=None):
    if not isinstance(version, str) or not _VERSION.fullmatch(version):
        raise ValueError("release version must be MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-rc.N")
    channel = "rc" if "-rc." in version else "stable"
    if requested and requested != channel:
        raise ValueError("release channel does not match version")
    return channel


if __name__ == "__main__":
    try:
        if len(sys.argv) not in (2, 3):
            raise ValueError("usage: release_channel.py VERSION [CHANNEL]")
        print(channel_for_version(sys.argv[1], sys.argv[2] if len(sys.argv) == 3 else None))
    except ValueError as exc:
        raise SystemExit(str(exc))
