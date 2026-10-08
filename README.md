# Kee Route Manager

**English** | [Русский](README.ru.md) | [简体中文](README.zh-CN.md)

Kee Route Manager (KRM) controls a verified Xray hot pool and failover on gateways. **Release `v1.1.0` and Release Candidate `v1.1.0-rc.15` use separate publication channels.** Platform support remains experimental pending the documented hardware acceptance; existing release tags and assets are unchanged.

The panel has Router and VPN navigation groups, live dashboards with bounded history, and multiple users with server-enforced permissions. Compatible router actions and manual route selection remain available during benchmarks; lifecycle actions cancel and join testing before execution. See [credential migration and rollback limits](docs/API.md#credential-migration-and-recovery).

Runtime components:

- `kee-route-managerd`: sole owner of state, scheduler, Xray/firewall and authenticated HTTPS/Unix APIs. Runs without UI.
- `kee-route-manager-ui`: embedded Web/PWA and TLS-verified API proxy. No router/core/process-execution dependency.
- `kee-route-managerctl`: local daemon client; offline credential/config/strict-JSON candidate tools.
- `kee-route-manager-launcher`: stable parent of the daemon and, on Keenetic/OpenWrt local installations, UI; verifies signed release slots and applies updates on explicit request.

Builds: Linux amd64, arm64, armv7, mipsle for all four runtime components and release tooling. Xray is the first TunnelCore adapter. Subscription formats: plain/base64 VLESS URI lists (Reality TCP and WebSocket TLS). Configuration parsing uses the YAML module listed in `go.mod`; runtime modules are included in the release inventory.

Failover compares independent health targets through VPN and WAN with quorum; inconclusive/target outages preserve selection. Emergency fallback probes run in parallel; benchmark cannot enable direct. Linux/OpenWrt managed nftables can bypass interception independently of Xray. **Keenetic automatic Xray-outage bypass is unsupported**, as is bypass of pre-existing unmanaged interception. Every platform remains experimental pending hardware checks.

The launcher checks for updates at the configured interval; installation requires an explicit UI/CLI action. A signed protocol-1 bundle updates daemon, CLI and managed local UI together, verifies process/API/reconciliation readiness, and rolls back a failed trial before commit. After commit, recovery restarts the new version without restoring old controller state. The stable launcher is updated manually. Linux UI retains its separate unprivileged service. Bootstrap pins one version and verifies Ed25519 manifest/checksums before executing downloaded code.

Router → System offers Release Candidate (`rc`) and Release (`stable`) update channels with a compatible separately maintained launcher. Switching persists the discovery preference without installing a version or enabling downgrades. See [release channels and migration](docs/RELEASE.md).

For an AI agent given only this repository link, start with [AGENTS.md](AGENTS.md), then follow [the full install runbook](docs/AGENT_INSTALL.md): SSH, backups, route selection, prepared or interactive configuration, core-only/local UI/remote UI, readiness, uninstall and rollback.

For a fresh installation, download the bootstrap for your platform from **one immutable signed release containing the interactive installer**, then run it without a config:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
sh /tmp/krm-bootstrap.sh
```

Replace `RELEASE_URL` with that release's `bootstrap-keenetic.sh`, `bootstrap-openwrt.sh` or `bootstrap-linux.sh` asset URL. Use a signed release that includes the interactive installer; previously published assets remain unchanged.

After verification of all release assets, the RU/EN wizard configures Xray paths, explicit routing tags, subscriptions and headers, score/health targets, pool size, optional speed testing and update discovery. With no `KRM_MODE`, installation defaults to `local-ui`; use `KRM_MODE=core sh /tmp/krm-bootstrap.sh` for controller only. The wizard generates a validated private controller config; `local-ui` also creates the UI config automatically, using `https://127.0.0.1:9443` and the controller's public certificate installed as `controller-ca.crt`. TLS verification remains enabled. An interactive terminal is required.

Routing tags must come from the actual Xray rules: inspect `kee-route-managerctl route-candidates --file PATH`; never guess a tag. Use health targets on independent hosts, preferably different operators, so one target outage does not look like VPN failure. Safe defaults retain existing firewall management, keep API access on loopback, disable speed tests and keep `auto_apply: false`; update discovery does not apply updates. Subscription URLs, headers and credentials belong in private files, never logs.

The advanced/noninteractive path remains available and skips the wizard:

```sh
KRM_MODE=core \
KRM_CONFIG_FILE=/root/krm-install/config.yaml \
sh /tmp/krm-bootstrap.sh
```

Prepared `local-ui` installations also require `KRM_UI_CONFIG_FILE`; prepare both files and their TLS settings explicitly. Standalone `KRM_MODE=ui` requires a prepared UI config, a trusted upstream CA supplied via `KRM_UPSTREAM_CA_FILE`, and an authenticated SSH tunnel. The wizard does not bypass remote trust requirements. Existing installations are refused rather than overwritten; follow the runbook for restore, uninstall and reinstall. No mutable `main` installation or insecure upstream TLS defaults.

```sh
make build
./scripts/check.sh
go test -race ./...
./scripts/fuzz-smoke.sh
./scripts/cross-build.sh
```

Documentation: [architecture](docs/ARCHITECTURE.md), [configuration](docs/CONFIGURATION.md), [API](docs/API.md), [security](docs/SECURITY.md), [limitations](docs/KNOWN_LIMITATIONS.md), [release](docs/RELEASE.md), [breaking changes](docs/BREAKING_CHANGES.md), [changelog](CHANGELOG.md). Automated verification runs in [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions).

Licensed under [Apache-2.0](LICENSE), selected by the owner.

Release development uses two branches: `main` for the stable line and `release-candidate` for candidates. Versions come from immutable Git tags (`vX.Y.Z` / `vX.Y.Z-rc.N`), not a version file. Branch pushes run checks; tags publish signed releases after channel/branch validation. Untagged local builds are marked `dev`. Publishing the stable channel does not certify device models or the unrun hardware scenarios. See [release publication](docs/RELEASE.md).
