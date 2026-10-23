#!/bin/sh
set -eu
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
export KRM_PLATFORM=keenetic
case "${KRM_INSTALL_ACTION:-install}" in
 install) sh "$HERE/preflight.sh";;
 resume|recover) : ;; # Common installer validates its private durable receipts.
 *) echo 'ERROR: KRM_INSTALL_ACTION must be install, resume or recover' >&2; exit 1;;
esac
exec sh "$HERE/../common/install.sh" "$@"
