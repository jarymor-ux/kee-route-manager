#!/bin/sh
# Identify the production XKeen Xray, excluding temporary KRM probe processes.
set -eu
proc_root=${1:-/proc}
binary=${2:-/opt/sbin/xray}
for entry in "$proc_root"/[0-9]*/comm; do
 [ -r "$entry" ] || continue
 IFS= read -r name < "$entry" || continue
 [ "$name" = xray ] || continue
 dir=${entry%/comm}
 # Linux resolves executable symlinks in /proc/PID/exe. Compare the actual
 # inode so provisioned native links work without accepting another Xray.
 [ "$dir/exe" -ef "$binary" ] || continue
 identity=$(awk '{sub(/^[^)]*\) /, ""); if ($1 != "Z") print $20}' "$dir/stat" 2>/dev/null) || continue
 [ -n "$identity" ] || continue
 tr '\000' '\n' < "$dir/cmdline" 2>/dev/null | awk 'NR==2 && $0=="run" {run=1} END {exit !(NR==2 && run)}' || continue
 tr '\000' '\n' < "$dir/environ" 2>/dev/null | grep -Fxq 'XRAY_LOCATION_CONFDIR=/opt/etc/xray/configs' || continue
 [ "$(awk '{sub(/^[^)]*\) /, ""); if ($1 != "Z") print $20}' "$dir/stat" 2>/dev/null)" = "$identity" ] || continue
 exit 0
done
exit 1
