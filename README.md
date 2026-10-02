# Kee Route Manager

**English** | [Русский](README.ru.md) | [简体中文](README.zh-CN.md)

Kee Route Manager (KRM) controls a verified Xray hot pool and failover on gateways. **1.1.0-rc.4 is an experimental prerelease.** Hardware acceptance is tracked separately; existing release tags and assets are unchanged.

Runtime components:

- `kee-route-managerd`: sole owner of state, scheduler, Xray/firewall and authenticated HTTPS/Unix APIs. Runs without UI.
- `kee-route-manager-ui`: embedded Web/PWA and TLS-verified API proxy. No router/core/process-execution dependency.
- `kee-route-managerctl`: local daemon client; offline credential/config/strict-JSON candidate tools.
- `kee-route-manager-launcher`: stable parent of the daemon and, on Keenetic/OpenWrt local installations, UI; verifies signed release slots and applies updates on explicit request.

Builds: Linux amd64, arm64, armv7, mipsle for all four runtime components and release tooling. Xray is the first TunnelCore adapter. Subscription formats: plain/base64 VLESS URI lists (Reality TCP and WebSocket TLS). No third-party runtime Go modules.

Failover compares independent health targets through VPN and WAN with quorum; inconclusive/target outages preserve selection. Emergency fallback probes run in parallel; benchmark cannot enable direct. Linux/OpenWrt managed nftables can bypass interception independently of Xray. **Keenetic automatic Xray-outage bypass is unsupported**, as is bypass of pre-existing unmanaged interception. Every platform remains experimental pending hardware checks.

The launcher checks for updates at the configured interval; installation requires an explicit UI/CLI action. A signed protocol-1 bundle updates daemon, CLI and managed local UI together, verifies process/API/reconciliation readiness, and rolls back a failed trial before commit. After commit, recovery restarts the new version without restoring old controller state. The stable launcher is updated manually. Linux UI retains its separate unprivileged service. Bootstrap pins one version and verifies Ed25519 manifest/checksums before executing downloaded code.

For an AI agent given only this repository link, start with [AGENTS.md](AGENTS.md), then follow [the full install runbook](docs/AGENT_INSTALL.md): SSH, backups, route selection, private config, core-only/local UI/remote UI, readiness, uninstall and rollback.

Example after preparing private config as described in the runbook:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.1.0-rc.4/bootstrap-keenetic.sh
KRM_MODE=core KRM_CONFIG_FILE=/root/krm-install/config.yaml sh /tmp/krm-bootstrap.sh
```

OpenWrt/Linux assets: `bootstrap-openwrt.sh` / `bootstrap-linux.sh`. Local UI requires `KRM_UI_CONFIG_FILE`; remote UI uses an authenticated SSH tunnel and trusted controller CA/SPKI. No mutable `main` installation or insecure upstream TLS defaults.

```sh
make build
./scripts/check.sh
go test -race ./...
./scripts/fuzz-smoke.sh
./scripts/cross-build.sh
```

Documentation: [architecture](docs/ARCHITECTURE.md), [configuration](docs/CONFIGURATION.md), [API](docs/API.md), [security](docs/SECURITY.md), [limitations](docs/KNOWN_LIMITATIONS.md), [release](docs/RELEASE.md), [breaking changes](docs/BREAKING_CHANGES.md), [changelog](CHANGELOG.md). Automated verification runs in [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions).

Licensed under [Apache-2.0](LICENSE), selected by the owner.
