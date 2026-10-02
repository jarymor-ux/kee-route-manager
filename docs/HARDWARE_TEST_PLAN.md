# Hardware acceptance — separate post-implementation stage

Status: **not executed**. Owner will provide Keenetic access after implementation. All RC2 platform builds remain experimental until this report is completed; cross-build, Docker and fakes do not substitute for router tests.

## Prerequisites

Authorized SSH address/port/user/key and independently verified fingerprint; independent recovery session; private backup of current Xray configs/routing/firewall/init/cron; current WAN and Xray passing baseline. Inventory old selector installation on device, stop its scheduler/watchdog, restore routing, then remove only verified owned files. No old product identifiers are required in KRM runtime.

## Device evidence

Model/firmware/Entware/XKeen/Xray versions, CPU/RAM/free disk, release binary digest, installation mode, timestamps, baseline routes/inbounds. Redact topology, node secrets, subscription data and credentials.

## Matrix

| Scenario | Acceptance | Outcome |
|---|---|---|
| core-only install | one owner, private socket, cached readiness | pending |
| local UI / login / PWA | trusted TLS, all assets 200, sessions+CSRF work | pending |
| initial benchmark | reserved operation ID, pool committed, verified active | pending |
| manual switch | selected populated slot, persistent restart selection | pending |
| one/all health targets fail | target/inconclusive classification, route preserved | pending |
| active VPN fails | first verified fallback within configured deadline | pending |
| all VPN fail | direct only with independently confirmed WAN | pending |
| Xray killed | truthful platform bypass capability; verify actual WAN | pending; Keenetic automatic bypass unsupported |
| external Xray restart | persisted selected outbound; no empty random slot | pending |
| router reboot | supervisor/lock/journal reconcile, correct selection | pending |
| provider outage/recovery >=1h | private cache, backoff, fair provider continuity | pending |
| user edits routing after install | reverse restore preserves unrelated changes | pending |
| restore / uninstall / reinstall | owned artifacts removed, WAN retained, clean reinstall | pending |
| update apply / broken future slot | apply explicitly disabled in RC2 | A/B launcher not implemented; separate future stage |

## Stop conditions

Loss of SSH/recovery path, unmanaged firewall effects, secret disclosure, unknown routing drift, unexpected change to another scheduler. Restore using the owner API while daemon is alive; preserve files if restoration fails. Do not invent unsafe Keenetic bypass commands.

## Completion

Attach a redacted result for each row, logs summarized without live secrets, and exact release/version/checksums. Remaining failures keep capabilities experimental. Stable release requires separate acceptance decision.
