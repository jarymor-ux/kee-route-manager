#!/bin/sh
set -eu
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
export KRM_PLATFORM=keenetic
exec sh "$HERE/../common/uninstall.sh" "$@"
