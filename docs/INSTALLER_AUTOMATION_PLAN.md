# Keenetic / Netcraze installer automation (draft plan)

Status: **partially implemented in Draft PR #31**; do not treat this plan as a working zero-touch installer or hardware acceptance.

Implemented: conservative read-only routing ownership discovery and prepared-configuration refusal of conditional, competing balancer/DIRECT and preceding global routes, guarded LAN/loopback HTTP browser UI with verified loopback HTTPS upstream, stable signed update discovery without automatic application, early overwrite/symlink rejection for installer-owned paths, and mandatory native Xray/XKeen/routing/resource preflight before persistent installer writes.

Staged dependency groundwork: `install/common/dependency-assets.sh` downloads **immutable, digest-and-size-pinned** XKeen 2.1 or Xray v26.10.21 archives into a new private directory. The official GitHub release-asset SHA-256 digests are embedded for MIPS BE/LE, ARMv7 and ARM64. A separate `install/common/dependency-prepare.sh` now safely extracts the pinned payload to another private staging directory, rejects archive path/link tricks and checks Xray's native ELF class/CPU. It does **not** install or execute either component; native Xray validation, activation and router policy changes still require separate implementation and acceptance.\n\nStill required: safe provisioning and clean Xray/XKeen bootstrap, durable installation intent/rollback/resume, native service validation and exact-model router acceptance. The installed KRM workflow still needs working XKeen/Xray prerequisites. XKeen on newer KeeneticOS may require operator-supplied device configuration; do not run upstream mutable install scripts or automatically change DNS, WAN or interception policy.

## Agreed user experience

- Operator installs and mounts **Entware** before running KRM. KRM does not format disks, modify KeeneticOS, or install Entware.
- KRM detects a compatible installed **XKeen** and **Xray**; when missing, offers to provision them with pinned, verified sources and known compatible versions. Preserve existing services/configuration.
- Health and score endpoint URLs remain **user supplied**. VPN subscription URL(s) and admin password also remain user supplied.
- Prefer an **HTTP management UI restricted to a specific trusted LAN IP** (not wildcard/WAN), with HTTPS optional for users who want it. Warn clearly that HTTP transmits credentials and sessions without transport encryption. Keep controller API loopback-only.
- Check for signed application updates automatically on the **stable** channel, but **never auto-apply**. Explicit authenticated UI/CLI approval remains required to install an update.
- Discover Xray binary, config directory, routing file and inbound/outbound tags from the actual router. Automatically adopt only a unique unambiguous VPN routing rule. If several valid candidates exist, require an explicit choice.
- Support a clean router by generating minimal valid Xray/XKeen configuration with validated routes. Preserve DNS rules, DIRECT exceptions, firewall and existing traffic policies on configured routers.
- Handle lower-memory MIPS routers carefully and report hardware/ABI/resource incompatibilities rather than attempting unsafe changes.

## Phases

1. **Preflight and discovery**: read-only platform/ABI, Entware, Xray/XKeen versions/paths, configuration and routing candidate discovery; explain deterministic choices and ambiguity.
2. **Provision dependencies**: explicit user approval; pinned/download-verified XKeen/Xray artifacts per ABI; reject incompatible or mutable unverified install scripts. Validate with native Xray and checked services.
3. **Safe configuration**: generate or adopt Xray routing under snapshot/ownership tracking; never silently seize unrelated routing rules. Keep health/score endpoints editable by the operator.
4. **Local HTTP panel**: require concrete RFC1918/LAN bind (or loopback), no automatic WAN exposure, no browser certificate import; provide an opt-in TLS mode and warnings for HTTP.
5. **Installation transaction**: durable journal/backup, resumable retries and rollback of KRM-owned changes only; never overwrite installed service/config without explicit migration.
6. **Update discovery**: stable/default checks enabled, auto_apply always false. No unattended launcher or Xray/XKeen update.
7. **Acceptance/tests/docs**: tests for clean/already-configured/broken/missing resources, architecture, interruptions and ambiguous routes; hardware testing on exact Keenetic/Netcraze models.

## Constraints

- No changes to `main`, no release/tag creation or merge in this PR.
- No automatic Entware installation, disk formatting or router WAN policy changes.
- Never execute unverified downloaded code; pin repository/tag/digests and respect release trust model.
- Never expose API or passwords on WAN; do not assume HTTP is encrypted.
- Real-router compatibility is experimental until separate hardware acceptance.

### Offline dependency preparation (no activation)

After Entware is available, `dependency-prepare.sh xray /absolute/new/staging-dir` or `dependency-prepare.sh xkeen /absolute/new/staging-dir` downloads the pinned release asset using `dependency-assets.sh`, verifies exact bytes and SHA-256, then unpacks approved files into a new private directory. Xray requires `unzip`; XKeen requires `tar`. The source directory must not exist. Neither helper writes to `/opt/sbin`, initializes XKeen, starts Xray, configures routing, or modifies router policy. A successful offline preparation is **not** evidence that the native services work on a device.

### Approved native payload provisioning (not an active XKeen setup)

With mounted Entware and no existing dependency installed, `sh install/common/dependency-provision.sh xray --approve` or `sh install/common/dependency-provision.sh xkeen --approve` explicitly installs the respective pinned/verified native files. It invokes the existing size/SHA-256/ABI-checked archive preparation and publishes new, private versioned files via no-overwrite symlinks in `/opt/sbin`. It does not replace an existing binary, configure Xray, call `xkeen -i`/`-io`, start services, install Entware packages or alter router interception, DNS, firewall, WAN or crontab. A terminated transaction may leave a partial private version directory or new link requiring operator inspection; automatic resume/journal and rollback are still pending. On KeeneticOS 5.2+ XKeen also requires device-specific RCI authorization; do not invent a token. This helper is a standalone development-phase entry point, **not** a zero-touch wizard workflow. KRM preflight still refuses missing/invalid native configs and services.
