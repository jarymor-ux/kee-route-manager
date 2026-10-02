# Signed release format

Manifest schema1 includes version/channel/min_config_schema and all payload files. Assets carry name/component/OS/arch/GOARM/version-pinned HTTPS URL/SHA256/size. Components: daemon, ui, ctl, launcher, release-tool and file. `update_protocol: 1` declares compatibility with the stable launcher. Checks authenticate the signature before version/channel decisions and require one matching daemon, UI and CLI for the native architecture.

`manifest-rc.json.sig` and `SHA256SUMS.sig` are base64 Ed25519 signatures over exact file bytes. Public key is `release-public.key` (raw32-byte base64) and pinned as PEM in generated bootstrap. SHA256SUMS includes payload files and manifest/signature, excluding itself/its signature to avoid circularity.

Bootstrap verifies native OpenSSL Ed25519, pinned version/channel/schema/protocol and SHA256 digests before unpacking the trusted installer or executing a verified binary. Every platform script/config/service and LICENSE/NOTICE are in `release-files.tar.gz`, included in the signed manifest. UI assets are embedded in a separately signed UI binary.

GitHub discovery chooses RC prereleases or stable non-prereleases separately; signed manifest version must match the selected immutable tag. Native executable URLs must belong to that repository and tag. No `/releases/latest` dependency. SemVer compares numeric prerelease components and ignores build metadata in precedence.

The launcher checks automatically and installs only following an explicit panel or CLI request. It downloads all three binaries into a private staging directory, verifies size/hash/signature, fsyncs and publishes an immutable `releases/VERSION` directory. Caller-supplied paths and asset metadata are never installation authority. The launcher verifies the complete slot again before execution.

After the current daemon drains mutations and joins scheduler loops, the launcher stops its daemon/UI children only. Production Xray continues running. The candidate starts in a read-only trial with a fresh nonce, holding both controller locks and validating primary state, journal, files and actual Xray selection. Readiness requires the candidate process identity, version, nonce, Unix API, enabled HTTPS API and co-located UI throughout the grace period. It does not recover old state or run normal managers during this trial.

A failed/interrupted trial restarts the previous signed version. The durable `launcher.json` commit is written before activation grants state-write authority to the same candidate process. A crash after this commit restarts the committed version: restoring a stale controller snapshot is never safe. `current` is a convenience symlink; the journal is authoritative. Configuration, credentials and TLS certificates are preserved.

The stable launcher and service wrappers are bootstrap infrastructure and are not replaced by protocol1 updates. A future incompatible launcher protocol requires a separate verified maintenance upgrade. Legacy v1.0.0-rc.2 installations require the one-time launcher migration; old release tags and keys are unchanged. The public key in new v1.1 releases is also the key trusted by the existing private router installation.
