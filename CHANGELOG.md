# Changelog

## 2.0.3 — 2026-10-29

- Keep node automatic-selection preferences by configured source ID and exact node name while retaining connection fingerprints for routing. IP/port and other connection changes, temporary disappearance and restart no longer reset a named node's exclusion.
- Preserve actual subscription/name aliases through connection deduplication and caching. Isolate different sources, share same-source/name settings and protect the active identity group. Refuse a refreshed excluded alias on the active connection before replacing its cache, including after a concurrent manual switch.
- Migrate recoverable fingerprint bans before probing/cache replacement and after startup reconciliation, preserving unresolved legacy source attribution across refresh/restart. Retain explicit allow records so late legacy reappearance cannot undo re-enabling. Missing historical names remain an explicit migration limitation.
- Preserve metadata during read-only update trials and document older-version downgrade limitations. Add endpoint-rotation, alias, migration, concurrency, cache-isolation and Chromium regressions. No launcher-protocol or configuration-default changes; hardware acceptance remains unchanged.

## 2.0.2 — 2026-10-29

- Add persistent per-node **Автовыбор** controls and an excluded-node filter. Excluded nodes remain visible with historical measurements, but new benchmarks, automatic failover and VPN recovery skip them.
- Reject disabling the active node in both UI and daemon. Serialize preference changes with routing and recheck eligibility before applying in-flight benchmark results; re-enable an excluded node before selecting its pool slot manually.
- Preserve exclusions by connection fingerprint across subscription refresh, provider label/source changes, restart, settings reload and `restore-xray`. Connection-parameter changes create a new identity enabled by default; older versions do not enforce exclusions.
- Expose strict authenticated node preference writes with `vpn.control`, CSRF, same-origin validation and audit events. Add regression and Chromium desktop/mobile light/dark interaction coverage. No launcher-protocol or configuration-default changes; router hardware acceptance remains unchanged.

## 2.0.1 — 2026-10-22

- First 2.0 release target: the public `v2.0.0` tag failed CI and produced no release assets; that tag is retained without moving it. Correct the Linux process-lifecycle test to treat ESRCH (PID already gone) as successful process exit, with a regression. This fixes a false test failure, not application runtime behavior.

- Restrict router deployments to Keenetic/Netcraze on compatible stock KeeneticOS/NDMS and Entware. Remove retired router adapters, installers, service templates and current configuration examples; keep shared Linux/Go tooling and management-only remote UI. Historical releases and changelog entries are unchanged. Older generated Keenetic YAML requires explicit offline removal of retired platform subsections before strict validation.
- Add MIPS Big Endian soft-float builds and signed-release/installer architecture handling, with separate BE/LE detection and immutable legacy inventory verification. These assets are not retroactively added to published v1.3.3.
- Add read-only dependency, routing, resource and ABI preflight, conservative opt-in resource guidance and maintained model/firmware documentation. No model is newly hardware-qualified and no 256 MB capacity or automatic Xray-outage bypass is promised.
- Build with Go 1.27.3 to include the crypto/x509 security fix for GO-2026-6809; published release toolchains remain unchanged.

## 1.3.3 / 1.3.3-rc.1 — 2026-10-16

- Import supported VLESS Reality TCP and WebSocket/TLS nodes from strict Xray JSON configurations and arrays, including base64 responses. Keep provider routing, DNS and inbounds outside the managed configuration; refuse unsupported outbound dependencies and TLS behavior.
- Add an opt-in subscription-editor preset that prepares a persistent random HWID and compatible Happ headers in the existing private source store. Preserve existing identities and custom headers; reject case-insensitive duplicate header names so downloads cannot alternate device IDs.
- Diagnose explicit provider HWID requirements, exhausted device limits and dummy `App not supported` nodes without exposing private subscription content. No automatic device registration before Save, scheduler/cache changes, new dependencies or launcher-protocol changes.

## 1.3.2 / 1.3.2-rc.1 — 2026-10-09

- Wait for coherent Keenetic configuration checksums after client policy and local DNS writes, retaining exact target, whole-file ownership and clean saved-baseline checks. Verify complete saved configuration content instead of treating matching metadata alone as successful persistence.
- Wait for delayed policy rollback revisions, preserve unrelated changes and confirm the owned preceding save before persisting recovery.
- Build release artifacts and isolated Linux checks with security-patched Go 1.27.2.
- Retry unfinished local DNS additions safely without duplicating an alias. Keep the daemon API available if DNS recovery remains pending, retain its private journal/backup, block policy saves and expose specific DNS errors and a recovery event. No interval, subscription-cache or launcher-protocol changes.

## 1.3.1 / 1.3.1-rc.1 — 2026-10-09

- Rework the workspace around a desktop sidebar and mobile drawer with Router/VPN submenus, a visible page title and connection status. Use compact router KPIs, prominent graphs and calmer dark/light surfaces with readable semantic colors and reduced-motion support.
- Split basic and advanced settings, retain every hidden configuration value, and show dependent speed/cache controls only when relevant. Keep revision-checked save/reconnect behavior and an accessible sticky save bar.
- Guide local panel address changes through preparation, verification and explicit confirmation, showing the open and candidate addresses, contextual certificate trust and the server-owned rollback countdown.
- Add node source/status filters, latency/speed sorting, mobile details and graph windows for 15 minutes or one hour, with keyboard/touch/pointer sample inspection. Retain gaps and unavailable samples.
- Apply explicit save/delete/toggle/selection results immediately while restoring keyboard focus. Preserve focused controls during ordinary polling, pause background-page refreshes and recheck access when returning. Add modal keyboard focus containment, Escape dismissal for editors and focus restoration. Keep immediate permission-revocation fences.
- Separate static JavaScript by workspace, charts, dialogs, settings and address lifecycle, extend UI regressions and include all scripts in the offline static cache. No runtime dependencies or configuration-default changes.

## 1.2.1 / 1.2.1-rc.1 — 2026-10-08

- Support Keenetic firmware that denies loopback `/ci/` configuration export: read running/startup configuration through bounded, validated local `ndmc more` commands after HTTP 403/404. Keep whole-configuration ownership and running/saved checksum checks; reject malformed/truncated output without exposing secrets. This repairs DNS verification for panel address changes and the same guarded configuration reads used by policy operations. Published 1.2.0 assets remain unchanged.

## 1.2.0 / 1.2.0-rc.1 — 2026-10-08

- Add Router → Settings with authenticated, revision-checked forms for benchmark, failover, health, subscription downloading and provider diversity. Validate before applying, preserve unrelated YAML and private sources, drain the old daemon runtime under its existing ownership locks, retain sessions and roll back failed initialization without restoring routing state. Configuration reload does not schedule an extra startup benchmark.
- Add supervised local panel hostname/HTTPS-port changes with explicit confirmation and a five-minute rollback window. Keep the previous listener during port trials, preserve its primary TLS identity through SNI, and supply a public certificate for a newly selected hostname. Only the existing private/loopback bind IP may be used. Update the separately maintained launcher to enable this capability.
- Add guarded Keenetic local DNS alias creation and interrupted-write reconciliation. Preserve existing aliases and refuse conflicting names, pending unrelated configuration or observed drift. Other platforms require an existing matching local DNS record.
- Extend the RU/EN installation wizard with benchmark interval, fresh-download/cache choice and explicit local UI bind address, HTTPS port and optional DNS certificate name. New interactive installations default to uncached downloads and loopback UI; prepared configurations and the global six-hour benchmark default remain unchanged. Clean up only newly created pair files after installation-config errors.

- Add authenticated UI subscription management: list, add, edit, enable/disable and delete sources without restarting the controller. UI-managed sources are validated with the normal config rules, persisted privately in the controller state directory and applied to the live fetcher; secret URL paths/queries and header values are not rendered in the subscriptions table.

- Complete fresh core/local-ui bootstrap on Keenetic, OpenWrt and Linux/systemd: after signed release verification, run the RU/EN wizard pinned to the bootstrap platform and validate private generated configs. Local-ui automatically creates its UI config with verified HTTPS controller upstream and `controller-ca.crt` trust. Preserve prepared-config installation, standalone UI trust requirements and refusal to overwrite existing installations.

## 1.1.1 / 1.1.1-rc.1 — 2026-10-08

- Add optional `subscriptions.cache_enabled: false`: download subscriptions fresh for every benchmark, skip cache reads/writes and emergency cache fallback, and let benchmark cadence own downloads instead of independent source polling. Preserve working routing when fresh downloads fail. Existing installations retain the default cache policy.
- Avoid queuing a redundant healthy hot-pool refresh while a benchmark is already running. In no-cache mode, scheduled testing owns healthy reserve refreshes and provider retries. Preserve explicit source changes and emergency failover/recovery. Configuration defaults and scheduled benchmark intervals remain unchanged.

## 1.1.0 / 1.1.0-rc.15 — 2026-10-08

Release and RC channels share the tested source baseline; platform support remains experimental until documented hardware acceptance is complete.

- Remove the version file; derive release versions and source identity from exact immutable Git tags. Use `main` for stable publication and `release-candidate` for RC publication, mark untagged/dirty builds as development, and exercise signed fixtures in disposable tagged repositories.
- Remove stale embedded fallback version numbers; raw Go builds identify themselves as development builds. Confirm both adjacent ports in the disposable Xray lifecycle fixture before starting its subprocess.

## 1.1.0-rc.14 — 2026-10-08

Experimental prerelease. Stable-channel publication support does not establish hardware acceptance.

- Support Release Candidate (`rc`) and Release (`stable`) publication, signed bootstraps and complete artifact verification. Add persistent channel selection in Router → System with `updates.manage`, preserve installed/rollback slot identity across switches, invalidate stale discovery and keep installation explicit without enabling downgrades. Older launchers require separate manual maintenance before switching is available.
- Include runtime Go modules in the signed SPDX inventory and inject `VERSION` into local `make build` binaries.

## 1.1.0-rc.13 — 2026-10-08

Experimental prerelease. These additions have automated coverage; router hardware and browser visual acceptance remain separate.

- Allow one benchmark to overlap compatible policy, Wake-on-LAN and manual route actions while retaining serialized routing transactions. Preserve manual selections made after benchmark admission; cancel and join tests before restart, restore or reboot.
- Save subscription changes without waiting for network downloads. Discard results from obsolete source revisions, coalesce follow-up testing, and prevent older downloads overwriting newer provider caches.
- Add multiple panel users, role templates and granular server-side permissions, immediate permission revocation, session invalidation, last-administrator protection, optimistic editing and attributed audit events. Preserve original credentials for documented owner recovery; downgrades do not preserve multiuser authorization.
- Group the panel into Router and VPN submenus with permission-aware navigation and requests. Add router/VPN dashboards, explicit telemetry freshness and availability, cancellation and concurrent-operation views.
- Keep up to 720 successful metric samples in daemon memory for last-hour graphs; add Linux/OpenWrt CPU sampling and distinguish unavailable WAN telemetry from idle traffic. WAN figures are aggregate interface traffic; history resets on restart.
- Validate user stores and all operation reservations during read-only update trials. Retain interrupted/unknown companion-operation outcomes and strengthen the SIGKILL recovery regression.

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
