# Signed prerelease publication

The current release line is `1.1.0-rc.N`. Existing `v1.0.0-rc.1` and `v1.0.0-rc.2` tags and assets stay unchanged. Releases remain experimental prereleases and are never marked latest stable. Software gates do not establish router hardware acceptance; maintain the platform limits in [KNOWN_LIMITATIONS.md](KNOWN_LIMITATIONS.md).

The CI workflow checks branch pushes and pull requests. Only a new `vMAJOR.MINOR.PATCH-rc.N` tag, or a manual workflow run on that existing tag, can publish. The publication job depends on successful checks for the same commit, requires the tag to equal `v` plus `VERSION`, and refuses commits outside `main`, moved tags and existing releases. A normal push to `main` does not publish.

## One-time repository setup

Keep the Ed25519 private key outside Git with mode `0600`. Its matching public key must be present in `release-public.key`, the controller configuration and generated bootstraps. Key changes require a deliberate trust migration on installed controllers; downloading a new public key beside an untrusted manifest does not establish trust.

Store the signing key through standard input, without printing it or placing its contents in command arguments:

```sh
gh secret set KRM_RELEASE_SIGNING_KEY --repo jarymor-ux/kee-route-manager < /secure/external/release.private.key
```

Enable [GitHub release immutability](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes) in repository Settings → Releases before publishing. This protects future release assets and tags; it does not alter previous releases. The workflow uses `contents: read` for checks and `contents: write` only in the publication job. It materializes the signing secret in a private temporary file outside the checkout and removes that file after the build. No personal access token is required by the workflow.

## Publish a version

1. Update `VERSION` and maintained configuration/docs in a reviewed commit. Merge the workflow and source into `main` before tagging.
2. Run the software gates: source/tests/vet, race, Staticcheck, Govulncheck, ShellCheck, JS, fuzz smoke, Linux runtime integration, container installer/firewall tests, all component cross-builds and signed release fixture verification. CI repeats these gates for the tag.
3. Create and push a new tag for the merged commit. Never move an existing release tag. The workflow validates the tag, then builds and verifies signed artifacts.
4. The workflow creates a draft prerelease, uploads the complete artifact set, downloads it again and verifies signatures, sizes and digests before publishing with `latest=false`, using the [GitHub CLI release options](https://cli.github.com/manual/gh_release_create). With immutability enabled, publication locks the assets and tag. A failed upload or verification leaves an unpublished draft for inspection; the workflow never replaces assets on retry.

A manual run uses the same gates and requires an existing tag:

```sh
gh workflow run ci.yml --ref v1.1.0-rc.3 --repo jarymor-ux/kee-route-manager
```

Artifact signing and immutable tags are separate controls. The workflow signs release content with Ed25519; it does not generate or validate an additional GPG/SSH Git-tag signature.

## Artifact contract

For a local signed build from the intended source commit:

```sh
KRM_RELEASE_PRIVATE_KEY=/secure/external/release.private.key ./scripts/build-release.sh
python3 scripts/verify-release.py release/dist
```

The builder reads `VERSION` directly. `KRM_SOURCE_COMMIT` can identify an isolated source copy; official publication uses the exact tested commit SHA. Existing output is refused to prevent accidental replacement.

Five components are built for Linux amd64, arm64, armv7 and mipsle: daemon, UI, CLI, stable launcher and release tool. The native build-host tool signs the manifest and `SHA256SUMS`; cross-compiled binaries are not executed on the build host. The distribution also contains three version-pinned bootstraps, the signed install payload and SPDX inventory. `manifest-rc.json` includes `schema_version: 1` and `update_protocol: 1`.

Bootstrap verifies native Ed25519 signatures and SHA-256 before executing any downloaded program. Controller installs receive the signed manifest and a complete daemon/UI/CLI binary set for their architecture, even when the UI is not enabled. The separately verified launcher seeds a private initial release slot. Installed component links select the active slot; the stable launcher executable is outside those slots and is not automatically replaced by an application update.

## Installation and update scope

Fresh core installs use the launcher as their service process. On Keenetic and OpenWrt, local-ui installs let that launcher supervise both controller and UI; no second UI supervisor is installed. Standalone UI installs keep their existing service. Linux local-ui preserves the separate `DynamicUser` UI service: the controller launcher does not elevate that UI to root, and the standalone UI remains manually maintained.

Launcher installation accepts `install --config CORE_PATH [--ui-config UI_PATH] --release-dir VERIFIED_RELEASE_DIRECTORY`. The directory contains `manifest-rc.json`, its signature and the architecture-specific raw daemon/UI/CLI files. Both services must be stopped before an existing installation is migrated. Migration requires private backups and an independent recovery shell; running an ordinary fresh installer over an existing installation remains refused. The `serve --config CORE_PATH` service uses the UI path recorded during installation. An interrupted initial import retains its files for inspection. A retry must use the same verified seed; foreign or leftover temporary entrypoints are refused. Inspect any reported `.launcher-new` path before removing it and retrying, rather than deleting the launcher record or switching `current` manually.

Automatic checks and explicit application are separate operations. Updates require a protocol-compatible installed launcher, valid trusted signatures and launcher readiness/rollback checks. `auto_apply` remains disabled. Standalone UI and unsupported launcher/platform combinations must report their limitations; publishing a release does not by itself make an old v1.0.0-rc.2 installation update-capable.

Uninstall restores Xray through the live controller before stopping services. A failed restore retains the installation. Configuration, state and release slots are kept unless `--purge` is requested. Purge covers only standard directories, including `/var/lib/kee-route-manager-updates` (under `/opt` on Keenetic); nonstandard `update.install_dir` paths remain for explicit operator handling.

`./scripts/test-release.sh` generates a disposable external signing key and verifies the complete artifact set plus tamper rejection. `./scripts/test-install.sh` runs only inside a disposable Docker container and exercises core, local-ui and standalone UI installations across the platform wrappers. Its service-manager adapters do not establish systemd/procd or device hardware acceptance. Follow [installation acceptance checks](AGENT_INSTALL.md#8-проверить-и-принять) on authorized hardware separately.
