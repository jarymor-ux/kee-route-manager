#!/bin/sh
set -eu
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
export KRM_PLATFORM=ui-proxy
exec sh "$HERE/../common/install.sh" "$@"
