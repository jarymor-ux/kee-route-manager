#!/bin/sh
set -eu
HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
export KRM_PLATFORM=keenetic
unset KRM_ENABLE_NEW_XKEEN_AUTOSTART
new_xkeen=0
[ -x /opt/sbin/xkeen ] || new_xkeen=1
sh "$HERE/prepare-stack.sh"
if [ "$new_xkeen" = 1 ] && [ -x /opt/sbin/xkeen ]; then
 KRM_ENABLE_NEW_XKEEN_AUTOSTART=1
 export KRM_ENABLE_NEW_XKEEN_AUTOSTART
fi
sh "$HERE/preflight.sh"
exec sh "$HERE/../common/install.sh" "$@"
