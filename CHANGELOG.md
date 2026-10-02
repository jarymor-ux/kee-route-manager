# Changelog

## 1.0.0-rc.2 — 2026-10-02

Experimental prerelease; real Keenetic acceptance deferred to separate owner-authorized hardware stage.

- Split daemon, UI and socket CLI; flock on state and tunnel confdir with owner metadata.
- Fix embedded assets/PWA routing; keep auth in core and enforce CA/SPKI upstream trust.
- Reserve asynchronous benchmark operations before202; remove benchmark direct transitions.
- Compare independent VPN/WAN targets; parallel emergency fallback with quorum, stability and cooldown.
- Add TunnelCore boundary, persistent Xray selection, durable transition journal/reconciliation and validated previous state fallback.
- Atomically replace owned nft table with metadata/conflict checks; independent managed bypass. Keenetic capability explicitly unsupported.
- Reverse restore owned routing patches and retain unrelated user edits.
- Remove unsafe updater executable replacement; apply disabled, correct SemVer/channel discovery/signature verification.
- Bound auth/config/resource limits; fair provider merge and central redaction; bounded private runtime logging and supervision.
- Authenticate version-pinned install payload and all assets with Ed25519/SHA256; native verifier before downloaded executable.
- Add adversarial tests, race/static/vulnerability/shell/JS/fuzz/cross-build/packaging gates, repository AI contract and end-to-end agent installation runbook.
- Apache-2.0 license selected by owner; security policy, contribution guide and CODEOWNERS.

## 1.0.0-rc.1

Initial release candidate. Published tag/assets preserved unchanged. See immutable RC1 tag for original code and documentation.
