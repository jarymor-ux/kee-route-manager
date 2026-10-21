#!/bin/sh
set -eu
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
export KRM_PLATFORM=keenetic
sh "$HERE/preflight.sh"
exec sh "$HERE/../common/install.sh" "$@"
