# Signed release format

Manifest schema1 includes version/channel/min_config_schema and **all payload files**, not just daemon binaries. Assets carry name/component/OS/arch/GOARM/version-pinned HTTPS URL/SHA256/size. Components: daemon, ui, ctl, release-tool and file. Update checks select daemon only and authenticate signature before version/channel decisions.

`manifest-rc.json.sig` and `SHA256SUMS.sig` are base64 Ed25519 signatures over exact file bytes. Public key is `release-public.key` (raw32-byte base64) and pinned as PEM in generated bootstrap. SHA256SUMS includes payload files and manifest/signature, excluding itself/its signature to avoid circularity.

Bootstrap verifies native OpenSSL Ed25519, pinned version/channel/schema and SHA256 digests before unpacking trusted installer or executing verified binary. Every platform script/config/service and LICENSE/NOTICE are in `release-files.tar.gz` included in signed manifest. UI assets are embedded in a separately signed UI binary.

GitHub discovery chooses RC prereleases or stable non-prereleases separately; signed manifest version must match selected immutable tag. No `/releases/latest` dependency. SemVer compares numeric prerelease components and ignores build metadata in precedence.

`update.apply` explicitly unavailable until a real stable A/B launcher with signed slot validation, real readiness and crash rollback is implemented. No executable self-replacement or arbitrary pending-path recovery remains.
