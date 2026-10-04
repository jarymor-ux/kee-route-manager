# Changelog

## Unreleased

## 1.1.0-rc.7 — 2026-10-04

Experimental prerelease. Hardware acceptance remains separate from CI.

- Remove the dead legacy CLI and obsolete UI installer wrappers; keep one embedded UI asset source.
- Remove the legacy in-process updater path and retain the signed stable-launcher staging/trial/rollback flow.
- Enforce core/UI and daemon/launcher/CLI dependency boundaries with regression guards.
- Move shared TLS, HTTP security, build-info and config-flag helpers to neutral packages.
- Clean shipped update templates/documentation and cover service-worker cache migration.
- Split `core.Manager` and `xray.Manager` into focused files without changing runtime algorithms.
- Narrow benchmark runtime configuration to `Benchmark`, `Health` and `Targets`, with an alias-safe AST guard against root `config.Config`.

## 1.1.0-rc.6 — 2026-10-03

- Run download benchmarks with bounded parallel node workers (`benchmark.speed.workers`, default 2, range 1..16); retain sequential warmup/repetitions per node and support isolated sampling with 1 worker. Cancellation stops the speed stage without reporting successful completion.
- Preserve completed restore intent when recovering damaged state; release finished operations after persistence failures and report those failures.
- Reserve the persistent Xray selection prefix and accept equivalent directory aliases for snapshot restore, rollback and journal replay without relaxing file identity checks.
- Prevent unchanged subscriptions with retained pool nodes from repeatedly scheduling benchmarks; enforce cache TTL before normal reuse.
- Show paused and unconfigured routing explicitly in the UI.
- Persist automatic-routing pause after restore; require a successful manual benchmark to resume, preventing scheduler races during uninstall.
- Refresh generated Xray configuration when API/inbound/routing settings change and reject unsupported nested config paths.
- Reset consecutive VPN recovery evidence after Xray outages and retain the failure diagnostic during independent bypass.
- Paginate the UI event history so its latest 300 records include recent errors and route changes.

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
