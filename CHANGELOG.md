# Changelog

## Unreleased

- Added a one-command Keenetic bootstrap installer with architecture detection and release SHA-256 verification.

## 1.0.0-rc.1 — 2026-10-02

- Replaced shell/Python split with one dependency-free Go controller.
- Added adaptive HTTPS Web/PWA interface and optional UI proxy role.
- Added user-defined credentials, PBKDF2 password hashing, sessions, CSRF and security headers.
- Added strict YAML configuration without executable shell sourcing.
- Added 20-source subscription model, cache/backoff and normalized deduplication.
- Added VLESS Reality/TCP and VLESS WS/TLS parsers.
- Added configurable five-slot Xray hot pool and dynamic API switching.
- Added fast fallback before background benchmark.
- Added explicit fail-open direct mode and automatic VPN recovery.
- Added adaptive speed samples streamed to `io.Discard`.
- Added persistent operation, state, event and Xray rollback journals.
- Added Keenetic RCI/NDMC metrics, clients, policy, WOL, reboot and logs.
- Added OpenWrt/procd and Linux/systemd adapters.
- Added optional managed IPv4 nftables interception for OpenWrt/Linux.
- Added signed self-update with atomic replacement and rollback.
- Added clean installers, restore-aware uninstallers, cross-builds and release tooling.
