# Installation

The complete deployment guide is [AGENT_INSTALL.md](AGENT_INSTALL.md). It is written for an agent starting with only the repository link and covers authenticated SSH, router inventory/backups, private configuration, signed bootstrap and verification.

Modes: `KRM_MODE=core`, `local-ui` (requires two private config files), `ui` (requires upstream public CA and authenticated SSH tunnel for remote controller). Each platform installer refuses to overwrite an existing installation. Installation expects existing valid strict-JSON Xray confdir and matching routing/inbound tags; it does not install Xray/Entware/XKeen itself.

Artifacts pin `v1.1.0-rc.2`; bootstrap authenticates manifests/checksums and complete install payload before executing downloaded programs. Native OpenSSL Ed25519 verification and curl with trusted CAs are mandatory. No downloads from mutable `main`.

Restore occurs through the live daemon before service shutdown. On drift/restore failure keep the installation for diagnosis. Reinstall requires backing up/moving retained private config or intentional purge. A fresh core installation includes the stable launcher. On Keenetic/OpenWrt local-ui installations it supervises both core and UI; systemd keeps the UI in its separate unprivileged service. Checks run automatically, installation is manual through the panel/CLI, and failed read-only trials return to the previous signed release. Existing installations need a private backup and controlled launcher migration; do not run the clean installer over them. See [update format](UPDATE_FORMAT.md).

All platforms are experimental. Keenetic automatic platform bypass is not supported; Linux/OpenWrt bypass requires managed interception. Hardware acceptance remains pending; see [known limitations](KNOWN_LIMITATIONS.md).
