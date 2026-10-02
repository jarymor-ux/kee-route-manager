# RC2 implementation and release report

Prepared 2026-10-02 against the owner's RC2 task and subsequent clarifications. License: Apache-2.0. Hardware acceptance was explicitly deferred; the release is experimental.

## Fixed findings

Priorities below describe release-review severity, not externally assigned CVE ratings.

| Priority | Finding | Result |
|---|---|---|
| P0 | UI/CLI could create competing controller ownership | Separate daemon/UI/ctl, ownership locks before manager creation, protected Unix socket; UI has no controller/process dependencies |
| P0 | Unsafe binary replacement without an A/B launcher | Apply disabled; signed metadata/discovery retained; no automatic replacement |
| P0 | Unauthenticated bootstrap/payload execution | Version-pinned Ed25519 verification before execution, signed size/SHA256 checks, immutable release URLs, archive traversal and replay rejection |
| P0 | Route/firewall mutations and restart lost consistent intent | Atomic nft transaction, validated write-ahead journal, actual-resource startup reconciliation, deterministic persistent Xray selection |
| P1 | Monitoring target failure could trigger direct or unnecessary switching | Independent host quorum and WAN comparison; ambiguity holds route; benchmark never enables direct |
| P1 | Dead Xray made nominal direct routing ineffective | Independent bypass implemented for owned Linux/OpenWrt interception; unsupported Keenetic/existing modes explicitly exposed |
| P1 | Corrupt state, restore drift or destructive uninstall | Validated state plus previous copy, reverse owned patches, drift checks, live restore before removal, refusal to overwrite installs |
| P1 | Authentication/commands could exhaust CPU, memory or processes | Bounded PBKDF2 concurrency/rate limits, input/config bounds, timeout/process-group cancellation, bounded private rotating/redacted logs |
| P1 | Untrusted proxy TLS, Origin and credential exposure | CA/SPKI trust, pre-rewrite Origin validation, loopback-only core API, safe public errors and centralized secret redaction |
| P1 | Provider monopoly/outage and early operation response | Fair source quotas, backoff/cache recovery, real reserved operation ID before acceptance |
| P2 | SemVer/channel errors, static/PWA paths, installer lifecycle and naming | Full SemVer; channel-aware discovery; fixed assets; separate service lifecycles and readiness; prior product names removed from current source |
| P2 | Missing release/agent/security governance | Apache-2.0/NOTICE, AGENTS.md, complete agent deployment guide, pinned CI, security policy/private reporting, signed asset inventory |

## Evidence and outstanding work

[TEST_REPORT.md](TEST_REPORT.md) records software checks and their scope. CI, container tests, unit/fuzz/race coverage and signed fixture verification are release gates. [HARDWARE_TEST_PLAN.md](HARDWARE_TEST_PLAN.md) is **pending**, not an acceptance report. Remaining capabilities and operational boundaries are listed in [KNOWN_LIMITATIONS.md](KNOWN_LIMITATIONS.md).

In particular: no A/B launcher, no Keenetic platform bypass, no unmanaged interception bypass or IPv6 protection claim; direct probing depends on the actual WAN path. Xray's local gRPC API trusts local processes. Existing Entware/XKeen/Xray prerequisites and an optional remote-UI SSH tunnel remain operator-managed.

## Delivery and integrity

- [PR #3](https://github.com/jarymor-ux/kee-route-manager/pull/3)
- [RC2 release](https://github.com/jarymor-ux/kee-route-manager/releases/tag/v1.0.0-rc.2)
- [All asset SHA-256 hashes](https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.2/SHA256SUMS), authenticated by the separately attached `SHA256SUMS.sig`
- `manifest-rc.json` / `manifest-rc.json.sig`: signed version, sizes, digests and exact URLs
- `release-public.key`: RC2 trust key; private signer remains outside Git. RC1 tag, trust key embedded in its assets and release assets are unchanged.

The release includes 16 component binaries for amd64/arm64/armv7/mipsle, three platform bootstraps, the installer payload and SPDX inventory. Source `AGENTS.md` and [AGENT_INSTALL.md](AGENT_INSTALL.md) let an agent start from the repository URL. Release notes record the final source commit, CI evidence and actual binary sizes; publication is conditional on those gates.

## Installation and breaking changes

[AGENT_INSTALL.md](AGENT_INSTALL.md) covers SSH identity/fingerprint, router inventory, prerequisite checks, private configuration, signed bootstrap, **core-only**, **core with local UI**, **remote UI**, readiness/TLS/functional verification, **uninstall** and **manual rollback**. [INSTALL.md](INSTALL.md) is the shorter operator guide. [BREAKING_CHANGES.md](BREAKING_CHANGES.md) describes RC1 migration: separate executables/services, new ownership/state contracts, disabled update apply and clean installation rather than in-place compatibility.

Do not erase a prior router deployment by guessed names or paths. The owner's Desktop task now includes the separate inventory/backup/restore/removal and clean Keenetic installation stage. All hardware DoD rows remain open until that stage is completed.

## Commit record

The complete thematic history is retained in PR #3 and Git history (`git log --reverse --oneline 34979ea..v1.0.0-rc.2`). Release source and merge commit are identified by the signed tag and release notes. This preserves implementation, independent review fixes, test gates, documentation and final verification changes rather than collapsing them into a single opaque commit.
