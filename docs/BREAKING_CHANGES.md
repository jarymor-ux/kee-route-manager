# Breaking changes

## Current: v1.3.2 / v1.3.2-rc.1

1. A separately installed stable launcher supervises the daemon and, on Keenetic/OpenWrt local-ui installations, the UI. It imports a signed protocol-1 daemon/UI/CLI bundle into private release slots. The launcher, service scripts and configuration schema upgrades require separate maintenance.
2. The shipped templates use `update.enabled: true` and `check_interval: 30m` to check for new versions automatically every 30 minutes. Applying one requires an explicit UI action or `kee-route-managerctl update-apply`; `auto_apply` must remain false. Discovery and downloads verify the pinned signing key, channel, platform, version and asset hashes.
3. A candidate runs a read-only trial with process/API/reconciliation readiness before commit. Failure before commit restores the previous executable version. After commit, recovery restarts the new version without restoring old controller state; manually changing `current` is unsupported.
4. Linux/OpenWrt with `firewall_mode: managed` supports fresh launcher installation and ordinary routing. Its updater permits signed discovery only: staging, application and candidate trial are refused until read-only reconciliation of owned nftables and policy routes is implemented. Keenetic and Linux/OpenWrt `existing` mode remain eligible; see [platform limits](KNOWN_LIMITATIONS.md).
5. Router local-ui uses one launcher service; a second standalone UI supervisor must be removed during migration. Linux local-ui retains its separate unprivileged DynamicUser UI service, and standalone UI is updated separately.
6. Legacy installations need a verified one-time migration or backup → restore routing → uninstall → clean signed install. Existing configurations are never silently overwritten. Pin the new release public key through a trusted channel; published legacy tags, keys and assets are unchanged. Follow the [installation runbook](AGENT_INSTALL.md) and [release procedure](RELEASE.md).

7. The original panel account becomes the administrator in a multiuser authorization view. Explicit user edits create a private sidecar next to the original credential file. Back up both files: downgrading uses the original single account/password and does not preserve new users or revoked rights. See [credential migration and recovery](API.md#credential-migration-and-recovery).
8. The panel now uses Router/VPN submenus and current server-side permissions. Compatible actions can run during a benchmark; lifecycle actions cancel and join it. Metric history is bounded and resets when the daemon restarts.

9. Panel RC/Release channel switching requires this separately maintained launcher capability. The discovery preference is private launcher metadata; old installed/rollback slots remain signature- and digest-verified independently. Application updates alone do not update an older launcher. Switching does not enable downgrades.

10. Release versions now come from immutable Git tags instead of a version file. Stable tags must belong to `main`, RC tags to `release-candidate`; source archives and untagged/dirty builds cannot publish. Raw development builds report `dev`.

These channels share the tested baseline; platform support remains experimental. Prior builds passed signed launcher migration and a manual GitHub update through the panel API on one Keenetic while preserving the Xray process and device policy. The concurrent-control, multiuser and dashboard additions do not yet have hardware acceptance. Router reboot, power-loss, deliberately broken candidate rollback and independent-bypass acceptance remain unverified.

## Historical: v1.0.0-rc.2

The following describes the old release, not current installation guidance. Its published assets are immutable; its Xray routing-section overwrite defect makes it unsuitable for a new installation.

1. Separate daemon/UI/CLI binaries and processes. Legacy executable is a CLI alias and cannot run the controller.
2. Core HTTPS API defaults to loopback; UI listens on 9444. Remote core access uses a trusted UI proxy or SSH tunnel, not a public root API listener.
3. `api` and `ui` configuration sections replace monolithic controller web runtime; credentials remain core-side. UI upstream TLS requires normal CA verification and optional SPKI pinning.
4. Config validation rejects zero/negative durations, resource/port/path/tag conflicts and insecure listener defaults. Two independent health hosts/quorum are required for conclusive automatic failover.
5. Benchmark preserves a working route; it cannot enable direct when subscriptions/targets return no healthy candidate.
6. Runtime state is validated and previous copy kept. Transactions are durable and replayed against actual runtime/files/firewall before readiness. Recovery may explicitly report degraded rather than silently trusting old JSON.
7. Persistent concrete Xray routing selection replaces random-balancer reliance. Restore reverses only owned changes and refuses irreconcilable drift.
8. Unsafe executable replacement was removed; `update.apply` and `auto_apply` were unavailable in that release. Update discovery uses channel-aware GitHub release selection or an explicitly pinned HTTPS manifest.
9. RC2 uses a new signing public key; RC1 trust/assets remain unchanged. Production bootstrap verifies every binary and installer from one release.
10. Installer expects a prepared private configuration; no hardcoded legacy outbound name, no silent migration or overwrite. Credentials must be supplied safely through terminal or private file.
11. Keenetic fail-open independent of Xray is unsupported until a verified platform mechanism and real hardware evidence exist.

That release did not promise compatibility with v1.0.0-rc.1 state/config. Its migration procedure required backup, routing restoration, uninstall and a clean installation. Use the current runbook for new installations.

## Settings editor and panel addressing (v1.2.0)

Runtime configuration editing is an allowlist rather than unrestricted YAML. It retains credentials, private subscriptions and infrastructure; pool size remains read-only. `config.manage` grants editor/address access; existing `users.manage` administrators retain access. Older versions may reject a user sidecar containing the newly granted permission, so retain private authorization backups for deliberate downgrade/recovery.

Panel hostname/port trials need the separately maintained new launcher. Additional UI SNI certificates are an optional new schema field and cannot be loaded by old UI/launcher binaries; upgrade that parent first and retain a compatible UI config for maintenance rollback. New interactive installs default to fresh subscription downloads and loopback UI, and ask for an explicit LAN bind when remote access is desired. Prepared config and global defaults remain unchanged.

## Workspace UI (v1.3.1)

Navigation moves to a desktop sidebar and mobile drawer, retaining the Router/VPN groups and permission-based pages. Settings are organized into basic controls and collapsible advanced groups; hiding dependent fields does not remove their values from revision-checked saves. Panel address changes retain explicit confirmation and the server-owned rollback deadline. This is a static UI update, with no controller configuration/schema or launcher protocol migration. Existing benchmark cadence, subscription cache policy and panel addressing are retained.
