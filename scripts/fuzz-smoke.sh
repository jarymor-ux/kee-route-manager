#!/usr/bin/env bash
set -euo pipefail
cd "$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
for target in 'config:FuzzYAMLSubset' 'config:FuzzByteSize' 'update:FuzzSemVer' 'subscription:FuzzSubscriptionPayload' 'subscription:FuzzXrayJSONSubscription' 'redact:FuzzRedaction'; do
 package=${target%%:*}; name=${target#*:}
 go test "./internal/$package" -run '^$' -fuzz "^$name$" -fuzztime=2s -parallel=2
done
