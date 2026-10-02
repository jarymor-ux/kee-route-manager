# Installation

The complete deployment guide is [AGENT_INSTALL.md](AGENT_INSTALL.md). It is written for an agent starting with only the repository link and covers authenticated SSH, router inventory/backups, private configuration, signed bootstrap and verification.

Modes: `KRM_MODE=core`, `local-ui` (requires two private config files), `ui` (requires upstream public CA and authenticated SSH tunnel for remote controller). Each platform installer refuses to overwrite an existing installation. Installation expects existing valid strict-JSON Xray confdir and matching routing/inbound tags; it does not install Xray/Entware/XKeen itself.

Artifacts pin `v1.0.0-rc.2`; bootstrap authenticates manifests/checksums and complete install payload before executing downloaded programs. Native OpenSSL Ed25519 verification and curl with trusted CAs are mandatory. No downloads from mutable `main`.

Restore occurs through the live daemon before service shutdown. On drift/restore failure keep the installation for diagnosis. Reinstall requires backing up/moving retained private config or intentional purge. Automatic self-update is unavailable; rollback is a controlled clean installation of a verified version.

All platforms are experimental. Keenetic automatic platform bypass is not supported; Linux/OpenWrt bypass requires managed interception. Hardware acceptance remains pending; see [known limitations](KNOWN_LIMITATIONS.md).
