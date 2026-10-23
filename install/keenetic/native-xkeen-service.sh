#!/bin/sh
# Signed entrypoint for clean native installs. Upstream remains synchronous and
# its missing-policy proxy-all fallback is guarded on every start and restart.
set -eu
fail(){ echo "ERROR: $*" >&2; exit 1; }
[ "$#" = 1 ] || fail 'usage: S05xkeen start|restart|stop|status'
service=/opt/etc/xkeen/native-service.sh
[ -f "$service" ] && [ ! -L "$service" ] && [ -x "$service" ] || fail 'native service payload is unavailable'
case "$1" in
 start|restart)
  approval=/opt/etc/xkeen/krm-activation-approved
  [ -f "$approval" ] && [ ! -L "$approval" ] && [ "$(stat -c %a "$approval")" = 600 ] || fail 'explicit native activation approval is missing'
  printf 'approved\n' | cmp -s - "$approval" || fail 'invalid native activation approval'
  [ -x /opt/bin/kee-route-managerctl ] || fail 'KRM native policy guard is unavailable; activation refused'
  /opt/bin/kee-route-managerctl native-policy-check >/dev/null 2>&1 || fail 'native RCI policy or authorization is unavailable; activation refused'
  exec "$service" "$1" on
  ;;
 stop|status) exec "$service" "$1";;
 *) fail 'usage: S05xkeen start|restart|stop|status';;
esac
