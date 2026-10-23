#!/bin/sh
# Clean native installations must not enter XKeen's proxy-all fallback.
set -eu
[ "$#" = 0 ] || { echo 'ERROR: unexpected native restart argument' >&2; exit 1; }
/opt/bin/kee-route-managerctl native-policy-check --xkeen-config /opt/etc/xkeen/xkeen.json
# Direct service invocation is synchronous and never runs xkeen's opkg self-heal.
exec /opt/etc/init.d/S05xkeen restart
