# Kee Route Manager — instructions for AI agents

This contract applies to the entire repository. Product name: **Kee Route Manager (KRM)**. Read [README.md](README.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md) and the relevant source/tests before editing. For deployment, follow [docs/AGENT_INSTALL.md](docs/AGENT_INSTALL.md) from beginning to end.

## Architecture invariants

- `kee-route-managerd` is the only state/Xray/firewall owner. Acquire its state-directory process lock before constructing managers or starting scheduler loops.
- CLI is a client of its owner-only Unix socket. Offline commands only validate config, manage credentials, or inspect strict-JSON route candidates; never instantiate a controller.
- UI serves static assets and proxies authentication/API. Its dependency graph must not include core, Xray, platform or process execution.
- Benchmark cannot enable direct routing. Only explicit control operations and the failover state machine may change route; inconclusive target/WAN monitoring preserves the route.
- Platform bypass requires an explicit adapter capability. Do not claim Keenetic fail-open without a real device test.
- Persist journal intent before effects; startup reconciles unfinished work against runtime/files/firewall. Preserve user routing changes on restore; refuse drift that cannot be safely reversed.
- `update.apply` stays disabled until a separate A/B launcher with process/API/reconciliation readiness and rollback has been implemented and tested.

## Execution and verification

Execute clear, reversible repository work through completion. Delegate bounded independent work when it improves correctness; use separate file ownership and integrate before verification. Before cleanup/refactor, write a short plan and lock missing behavior with regressions. Prefer deletion and existing patterns, no unsolicited new dependencies.

Run targeted tests, then `./scripts/check.sh`, race, vet, Staticcheck, Govulncheck, ShellCheck, fuzz smoke, cross-build and signed fixture verification. Use Docker for isolated Linux integration; never test firewall/uninstall against the developer host. Read failures and fix root causes. Do not equate cross-build or fake tests with hardware acceptance.

## Secrets, releases and deployment

- Credentials, subscription headers/URLs, node UUIDs/Reality data and router backups stay outside Git, PRs and logs. Redact diagnostics centrally.
- Never read or print private signing-key contents. Keep key files outside this checkout with mode 0600. Production bootstrap verifies native Ed25519 signatures and SHA-256 before executing any downloaded program; all files come from one immutable release tag.
- Do not rewrite published tags/assets. RC1 is immutable. RC2 is a prerelease, never latest stable. Keep hardware limitations and failed/unrun checks visible.
- Do not automatically change a router address, SSH keys, existing firewall, WAN policy, credentials or another scheduler without task authorization. Maintain a working independent SSH/recovery path and private backups before routing changes.
- Never recommend disabling SSH host verification or upstream certificate verification. For remote UI use an authenticated SSH tunnel with trusted CA/SPKI.

## Completion report

Report changed behavior, exact test evidence, unresolved limitations, commit/PR/release links and install/restore/rollback instructions. Keep reports honest about hardware coverage. Project license is Apache-2.0, selected by the owner.
