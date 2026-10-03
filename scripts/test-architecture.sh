#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

MODULE=github.com/jarymor-ux/kee-route-manager

deps() {
  go list -deps "$1"
}

assert_excludes() {
  target=$1
  shift
  graph="$(deps "$target")"
  for forbidden in "$@"; do
    forbidden_path="$MODULE/$forbidden"
    while IFS= read -r dependency; do
      case "$dependency" in
        "$forbidden_path"|"$forbidden_path"/*)
          printf '%s must not depend on %s or its subpackages\n' "$target" "$forbidden" >&2
          exit 1
          ;;
      esac
    done <<< "$graph"
  done
}

assert_excludes ./cmd/kee-route-managerd internal/web/ui internal/control/cli
assert_excludes ./cmd/kee-route-manager-launcher internal/control/cli
assert_excludes ./cmd/kee-route-manager-ui internal/core internal/xray internal/platform internal/store internal/bench internal/control
assert_excludes ./cmd/kee-route-managerctl internal/core internal/xray internal/platform internal/store internal/bench

if grep -R --include='*.go' -n 'config\.Config' internal/bench | grep -v '_test.go:'; then
  echo 'internal/bench must not depend on the root config.Config type' >&2
  exit 1
fi

printf 'Architecture dependency boundaries passed.\n'
