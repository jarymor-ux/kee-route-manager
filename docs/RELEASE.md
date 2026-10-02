# Release procedure

## Build

```bash
export KRM_RELEASE_PRIVATE_KEY=/secure/path/kee-route-manager-release-private.key
export OUTPUT_DIR="$PWD/release"
./scripts/build-release.sh
```

The script runs source checks, builds four static Linux binaries, creates an Ed25519-signed update manifest and writes `SHA256SUMS`.

The private key is never copied into the repository or generated release directory. `release-public.key` is safe to publish and is also embedded in the example configurations.

## GitHub release layout

Upload these files from `release/dist/` to a tag named `v1.0.0-rc.1`:

```text
kee-route-manager-linux-amd64
kee-route-manager-linux-arm64
kee-route-manager-linux-armv7
kee-route-manager-linux-mipsle
manifest-rc.json
manifest-rc.json.sig
SHA256SUMS
```

The default configuration expects:

```text
https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.1/manifest-rc.json
https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.1/manifest-rc.json.sig
```

## Stable channel

A stable release must use channel `stable`, a stable semantic version and a separate `manifest-stable.json`/signature pair. Do not retag or replace published assets: signed manifests bind each architecture to an exact URL, size and SHA-256 digest.

## RC acceptance criteria

Before promoting to `1.0.0`:

1. Run the RC on the target Keenetic for at least 72 hours.
2. Test loss and recovery of every subscription source.
3. Test exhaustion of the full hot pool and confirm explicit direct mode.
4. Confirm that existing connections behave as expected during balancer changes; dead remote TCP/UDP sessions cannot be migrated.
5. Run installation/uninstallation on OpenWrt and Linux test gateways.
6. Perform one signed update and one deliberately broken update to verify rollback.
7. Review diagnostic output for accidental credential disclosure.
