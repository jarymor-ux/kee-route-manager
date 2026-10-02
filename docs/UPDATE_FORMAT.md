# Signed update format

## Manifest

```json
{
  "schema_version": 1,
  "version": "1.0.0-rc.1",
  "channel": "rc",
  "published_at": "2026-10-02T00:00:00Z",
  "min_config_schema": 1,
  "assets": [
    {
      "os": "linux",
      "arch": "arm64",
      "url": "https://.../kee-route-manager-linux-arm64",
      "sha256": "...",
      "size": 12345678
    }
  ]
}
```

The detached signature file contains base64-encoded Ed25519 signature bytes over the exact manifest bytes, including whitespace and final newline.

## Release tooling

Generate a key pair once:

```sh
go run ./cmd/krm-release-tool keygen \
  --private /secure/location/release-private.key \
  --public release-public.key
```

Generate and sign a manifest:

```sh
go run ./cmd/krm-release-tool manifest \
  --version 1.0.0-rc.1 \
  --channel rc \
  --dist dist \
  --base-url https://github.com/OWNER/REPO/releases/download/v1.0.0-rc.1 \
  --out dist/manifest-rc.json \
  --private /secure/location/release-private.key \
  --signature dist/manifest-rc.json.sig
```

Never commit or upload `release-private.key` as a repository file or ordinary release asset.
