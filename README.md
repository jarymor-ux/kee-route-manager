# Kee Route Manager

**English** | [Русский](README.ru.md) | [简体中文](README.zh-CN.md)

**Kee Route Manager (KRM)** is a local Xray route controller for routers and Linux gateways. It combines multiple subscription sources into one deduplicated node pool, keeps a configurable hot pool loaded in Xray, switches new connections to a verified fallback without waiting for a full benchmark, and exposes an adaptive Web/PWA interface.

Current release: **1.0.0-rc.1**.

## Scope of RC1

| Area | Supported |
|---|---|
| Platforms | Keenetic + Entware + XKeen; OpenWrt + procd; Linux + systemd |
| CPU builds | amd64, arm64, armv7, mipsle |
| VPN core | Xray |
| Nodes | VLESS Reality/TCP; VLESS WebSocket/TLS |
| Subscriptions | plain URI list; base64-encoded URI list; up to 20 sources |
| Pool | unified deduplicated pool; 5 hot nodes by default; configurable |
| Failover | verified fallback first; direct route only when every VPN fallback fails |
| UI | embedded responsive Web/PWA; optional Linux UI proxy for PC/Raspberry Pi |
| Authentication | user-defined login/password; PBKDF2-SHA256; session + CSRF protection |
| Updates | Ed25519-signed manifests; SHA-256 assets; atomic replacement and rollback |

## How failover works

```text
subscription sources
        │
        ▼
deduplicated node pool
        │
        ▼
benchmark + health history
        │
        ▼
hot pool (default: 5 Xray outbounds)
        │
        ├─ active VPN node
        ├─ verified fallback
        ├─ verified fallback
        ├─ verified fallback
        └─ verified fallback
```

1. The active path is checked through Xray every 15 seconds by default.
2. After two failed health cycles, KRM probes the already-loaded fallback slots.
3. New connections are moved to the first working fallback through Xray's local API.
4. A full benchmark runs after connectivity is restored, not before failover.
5. If no VPN slot works, KRM deliberately switches the managed traffic to `direct`.
6. While direct mode is active, fallback slots continue to be probed. VPN is restored after two successful checks.
7. Existing TCP/UDP sessions on a dead remote server cannot be migrated; applications reconnect through the new path.

Benchmark payloads are streamed directly to `io.Discard`. Downloaded speed-test data is never persisted to disk.

## Repository layout

```text
cmd/kee-route-manager/       daemon and CLI
cmd/krm-release-tool/        Ed25519 release tooling
internal/auth/               credentials, sessions, CSRF support
internal/bench/              latency, health and adaptive speed tests
internal/config/             strict YAML subset and validation
internal/core/               scheduler, pool, failover and operations
internal/platform/           Keenetic, OpenWrt and Linux adapters
internal/subscription/       fetch, cache, parser and deduplication
internal/update/             signed self-update and rollback
internal/web/                HTTPS API and embedded PWA
internal/xray/               managed fragments, API switching, rollback
configs/                     platform templates
install/                     platform installers and uninstallers
web/                         frontend source
```

## Installation

For an interactive one-command installation on Keenetic, run as `root` over SSH:

```sh
curl -fsSL https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | sh
```

The bootstrap detects the CPU architecture, downloads the latest release binary and `SHA256SUMS`, verifies the binary, fetches the Keenetic installer files, and starts the same interactive setup. No repository clone or release archive is required.

If only `wget` is available:

```sh
wget -qO- https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | sh
```

To install a specific release instead of `latest`:

```sh
curl -fsSL https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | KRM_VERSION=v1.0.0-rc.1 sh
```

Installation from an unpacked release bundle remains supported:

```sh
sh install/keenetic/install.sh
```

Read [docs/INSTALL.md](docs/INSTALL.md) for requirements and recovery guidance. The installer refuses to silently migrate `blanc-auto`; RC1 is clean-install only.

## Local development

KRM has no third-party Go dependencies.

```sh
go test ./...
go vet ./...
go build ./cmd/kee-route-manager
go build ./cmd/krm-release-tool
```

Validate a configuration:

```sh
./kee-route-manager validate --config configs/linux-systemd.yaml
```

Create credentials without putting the password in process arguments:

```sh
printf '%s\n' 'a-long-password' |
  ./kee-route-manager passwd \
    --config /etc/kee-route-manager/config.yaml \
    --username admin \
    --password-stdin
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Configuration](docs/CONFIGURATION.md)
- [Installation](docs/INSTALL.md)
- [Security](docs/SECURITY.md)
- [Breaking changes](docs/BREAKING_CHANGES.md)
- [Update and release format](docs/UPDATE_FORMAT.md)
- [Known limitations](docs/KNOWN_LIMITATIONS.md)
- [API overview](docs/API.md)
- [Test report](docs/TEST_REPORT.md)
- [Release procedure](docs/RELEASE.md)

## Release status

This is a release candidate. Unit tests, static checks, configuration validation, installer syntax checks and cross-compilation are part of the release script. Installation on the target router must still be treated as a controlled rollout with access to the router's recovery path.
