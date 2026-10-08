# Signed release publication

The release lines are stable `1.2.0` from `main` and candidate `1.2.0-rc.1` from `release-candidate`. The builder and publication workflow support two version shapes: `MAJOR.MINOR.PATCH-rc.N` publishes to the `rc` channel with GitHub prerelease enabled; `MAJOR.MINOR.PATCH` publishes to `stable` with prerelease disabled. Existing `v1.0.0-rc.1` and `v1.0.0-rc.2` tags and assets stay unchanged. Both channels use `latest=false`; discovery selects the channel explicitly. Stable-channel publication does not certify device hardware acceptance; all documented unrun platform checks remain visible. Software gates do not establish router hardware acceptance; maintain the platform limits in [KNOWN_LIMITATIONS.md](KNOWN_LIMITATIONS.md).

The CI workflow checks `main` (stable line), `release-candidate` (candidate line) and pull requests. Only a new `vMAJOR.MINOR.PATCH-rc.N` or `vMAJOR.MINOR.PATCH` tag, or a manual workflow run on that existing tag, can publish. Leading-zero version fields, other prerelease labels and build metadata are refused. The publication job depends on successful checks for the same commit, derives the version from the exact tag on the checked-out commit, and refuses moved tags and existing releases. RC tags must belong to `origin/release-candidate`; stable tags must belong to `origin/main`. A normal branch push does not publish.

## One-time repository setup

Keep the Ed25519 private key outside Git with mode `0600`. Its matching production public key is stored once at `internal/releasetrust/public.key`; Go binaries embed that file and the release builder pins the same key into controller defaults, generated bootstraps and the signed payload as `release-public.key`. Key changes require a deliberate trust migration on installed controllers; downloading a new public key beside an untrusted manifest does not establish trust.

Store the signing key through standard input, without printing it or placing its contents in command arguments:

```sh
gh secret set KRM_RELEASE_SIGNING_KEY --repo jarymor-ux/kee-route-manager < /secure/external/release.private.key
```

Enable [GitHub release immutability](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes) in repository Settings → Releases before publishing. This protects future release assets and tags; it does not alter previous releases. The workflow uses `contents: read` for checks and `contents: write` only in the publication job. It materializes the signing secret in a private temporary file outside the checkout and removes that file after the build. No personal access token is required by the workflow.

## Publish a version

1. Develop and test candidates on `release-candidate`. Push that branch before creating a new `vX.Y.Z-rc.N` tag for its tested commit. After software acceptance and review of the declared platform limits, merge the accepted candidate into `main` and create `vX.Y.Z` there. Hardware qualification is a separate explicit gate before claiming verified device support. Release numbers live in immutable tags; there is no version file to edit. The initial Release and RC artifacts use the same tested source baseline with separate immutable tags; platform support stays experimental until its documented hardware acceptance is complete.
2. Run the software gates: source/tests/vet, race, Staticcheck, Govulncheck, ShellCheck, JS, fuzz smoke, Linux runtime integration, container installer/firewall tests, all component cross-builds and signed release fixture verification. CI repeats these gates for the tag.
3. Create and push a new tag for the tested commit in the appropriate branch. Never move an existing release tag. The workflow validates the tag, then builds and verifies signed artifacts.
4. The workflow creates a draft with the version-derived prerelease flag, uploads the complete artifact set, downloads it again and verifies signatures, channel binding, sizes and digests before publishing with that same flag and `latest=false`, using the [GitHub CLI release options](https://cli.github.com/manual/gh_release_create). With immutability enabled, publication locks the assets and tag. A failed upload or verification leaves an unpublished draft for inspection; the workflow never replaces assets on retry.

A manual run uses the same gates and requires an existing tag:

```sh
gh workflow run ci.yml --ref v1.1.0-rc.7 --repo jarymor-ux/kee-route-manager
```

Artifact signing and immutable tags are separate controls. The workflow signs release content with Ed25519; it does not generate or validate an additional GPG/SSH Git-tag signature.

## Artifact contract

For a local signed build from the intended source commit:

```sh
KRM_RELEASE_TAG=v1.2.0-rc.1 KRM_RELEASE_PRIVATE_KEY=/secure/external/release.private.key ./scripts/build-release.sh
python3 scripts/verify-release.py release/dist

# KRM_RELEASE_PUBLIC_KEY is reserved for isolated build/test fixtures.
# Production builds use internal/releasetrust/public.key by default.
```

The builder resolves the exact Git release tag at `HEAD` and derives its version/channel. A clean original Git checkout is required; source archives and untagged commits cannot produce a production release. `KRM_RELEASE_TAG` selects the exact tag when multiple release tags identify one commit; the tag must resolve to `HEAD`. An optional `KRM_RELEASE_CHANNEL` must agree with it. `KRM_SOURCE_COMMIT`, when supplied, must equal the actual tagged commit; binaries and SPDX use that Git identity. Existing output is refused to prevent accidental replacement.

`make build` uses an exact tag when unambiguous; dirty builds include a visible `+dirty` suffix, untagged commits report `0.0.0-dev.g<commit>`, and an exported source archive reports `0.0.0-dev.unknown`. Raw `go build` without injected metadata reports `dev`. These are development builds. Signed release publication rejects dirty/unidentified sources. Branches and GitHub `latest` are never download or execution authority; installed updates still use immutable signed release assets.

Five components are built for Linux amd64, arm64, armv7 and mipsle: daemon, UI, CLI, stable launcher and release tool. The native build-host tool signs the manifest and `SHA256SUMS`; cross-compiled binaries are not executed on the build host. The distribution also contains three version-pinned bootstraps, the signed install payload and SPDX inventory. `manifest-rc.json` or `manifest-stable.json` includes the matching channel, `schema_version: 1` and `update_protocol: 1`. Exactly one channel manifest belongs in a release directory. The SPDX inventory lists distributable checksums and production Go modules (currently `go.yaml.in/yaml/v3`, with its MIT and Apache-2.0 portions); it does not claim a complete standard-library source SBOM.

Bootstrap verifies native Ed25519 signatures and SHA-256 before executing any downloaded program. Controller installs receive the signed manifest and a complete daemon/UI/CLI binary set for their architecture, even when the UI is not enabled. The separately verified launcher authenticates the initial seed using its signed channel and seeds a private release slot. Seed import preserves the configured future update channel; installing a stable seed with an RC discovery preference, or the reverse, is allowed. Configless wizard defaults remain RC: select `stable` explicitly when enabling updates if that is the desired discovery preference. Prepared configuration is preserved. Installed component links select the active slot; the stable launcher executable is outside those slots and is not automatically replaced by an application update.

## Installation and update scope

Fresh core installs use the launcher as their service process. On Keenetic and OpenWrt, local-ui installs let that launcher supervise both controller and UI; no second UI supervisor is installed. Standalone UI installs keep their existing service. Linux local-ui preserves the separate `DynamicUser` UI service: the controller launcher does not elevate that UI to root, and the standalone UI remains manually maintained.

Launcher installation accepts `install --config CORE_PATH [--ui-config UI_PATH] --release-dir VERIFIED_RELEASE_DIRECTORY`. The directory contains exactly one `manifest-rc.json` or `manifest-stable.json`, its signature and the architecture-specific raw daemon/UI/CLI files. Both services must be stopped before an existing installation is migrated. Migration requires private backups and an independent recovery shell; running an ordinary fresh installer over an existing installation remains refused. The `serve --config CORE_PATH` service uses the UI path recorded during installation. An interrupted initial import retains its files for inspection. A retry must use the same verified seed; foreign or leftover temporary entrypoints are refused. Inspect any reported `.launcher-new` path before removing it and retrying, rather than deleting the launcher record or switching `current` manually.

The panel's RC/stable discovery preference is persisted privately at `<update.install_dir>/update-channel.json`; it survives daemon/launcher restarts without rewriting the YAML configuration. This changes future discovery, not the currently installed signed slot. Upgrading to this capability on an installation with an older launcher requires a separate maintenance upgrade of the stable launcher from a signature-verified, immutable release, with services stopped, private backups and an independent recovery shell. Normal application updates do not replace the launcher. Fresh installer execution over an existing installation remains refused; follow the migration/maintenance procedure rather than replacing slots or launcher records manually.

Channel selection never authorizes a downgrade. For example, an installed `1.1.0-rc.N` may advance to stable `1.1.0`; stable `1.0.x` is older and cannot be installed merely by selecting stable. If there is no newer signed release in the selected channel, keep the current slot. Rollback remains the launcher's failure-recovery path to its authenticated previous slot, independent of the discovery preference.

Automatic checks and explicit application are separate operations. Updates require a protocol-compatible installed launcher, valid trusted signatures and launcher readiness/rollback checks. `auto_apply` remains disabled. Standalone UI and unsupported launcher/platform combinations must report their limitations; publishing a release does not by itself make an old v1.0.0-rc.2 installation update-capable.

Uninstall restores Xray through the live controller before stopping services. A failed restore retains the installation. Configuration, state and release slots are kept unless `--purge` is requested. Purge covers only standard directories, including `/var/lib/kee-route-manager-updates` (under `/opt` on Keenetic); nonstandard `update.install_dir` paths remain for explicit operator handling.

`./scripts/test-release.sh` generates a disposable external signing key and builds/verifies both RC and stable artifact sets plus tamper rejection, using isolated Git repositories, commits and tags. It checks embedded binary version/commit, bootstraps and SPDX against those disposable identities without a version file. It does not publish tags or releases. `./scripts/test-install.sh` runs only inside a disposable Docker container and exercises core, local-ui and standalone UI installations across the platform wrappers. Its service-manager adapters do not establish systemd/procd or device hardware acceptance. Follow [installation acceptance checks](AGENT_INSTALL.md#8-проверить-и-принять) on authorized hardware separately.

## v1.2.0 launcher and configuration maintenance

The runtime editor can be delivered in an ordinary signed application update. Local panel address control additionally requires the v1.2.0+ separately installed launcher; it is never replaced by application download. Download the full immutable release, verify pinned Ed25519 signatures and every digest, preserve private controller/UI configs, certificates, credential/source files and launcher record/channel, and retain independent SSH. Stop the installed launcher service before atomically replacing only its verified architecture binary, then restart and prove current application/route readiness. Do not reinstall over its existing record or manually change slots.

Upgrade the launcher before writing additional SNI identities: earlier binaries reject the new optional UI TLS field. Keep the original UI YAML/certificates for a coordinated maintenance rollback; the older UI cannot parse additional certificates. Restoring an older binary does not restore newer permission vocabulary, users or controller state. Never overwrite post-commit state from a backup.

Fresh wizard settings: benchmark interval defaults to6h, new interactive subscription caching defaults off, UI binds loopback unless a private IPv4 is explicitly chosen; HTTPS9444 and optional local DNS certificate name are prompted. A hostname SAN does not register DNS. Prepared configs bypass these prompts and preserve their own cadence/cache/listen values.
