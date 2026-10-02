# Architecture

## Components

```text
Browser / installed PWA / optional UI proxy
                    │
                    │ HTTPS + JSON API
                    ▼
          kee-route-manager daemon
 ┌─────────────────────────────────────────────┐
 │ authentication and operation coordinator    │
 │ subscription fetch/cache/deduplication      │
 │ health monitor and benchmark engine         │
 │ hot-pool state machine and failover          │
 │ event journal and signed updater             │
 └──────────────┬───────────────────┬──────────┘
                │                   │
                ▼                   ▼
          Xray adapter       platform adapter
          local API          Keenetic / OpenWrt / Linux
```

The controller runs on the device where Xray runs. The PWA may be served directly by that controller or through the optional UI proxy on another Linux host. SSH is not used during normal operation.

## Dependency direction

The frontend calls the versioned HTTP API. It does not contain shell or router commands. The core depends on interfaces implemented by the Xray and platform adapters. Platform-specific commands are isolated in `internal/platform`.

## Persistent state

KRM writes state atomically under the configured `state_dir`:

- `state.json` — active slot, hot pool, source status and measurements;
- `operation.json` — current or last long-running operation;
- `events.jsonl` — append-only bounded event source;
- `xray-original/` — first-install snapshot used by uninstall/restore;
- `update-pending.json` — update health and rollback journal.

Subscription cache and parsed nodes are stored under `cache_dir`. Speed-test bodies are not stored.

## Subscription sources

All enabled sources are fetched independently. Their nodes are normalized and deduplicated by connection identity, not by display name. A node can retain multiple source IDs.

Source states:

- `healthy` — the last network fetch was valid;
- `degraded` — a fresh cache is being used after a fetch failure;
- `unavailable` — no valid fresh source is available; an expired cache is considered only as emergency input when every source is unavailable;
- `recovering` — the source returned after an unavailable period.

Failed sources use the configured exponential-style backoff. Healthy sources obey `refresh_interval`, so the 15-second source scheduler does not repeatedly download valid subscriptions.

## Hot pool and provider diversity

By default all nodes are ranked as one common pool. There is no provider priority.

Optional `provider_diversity` is a soft filling rule. It tries to include multiple source IDs but never leaves a slot empty merely to satisfy diversity. One source is fully supported.

Existing slot assignments are preserved where possible. This reduces dynamic Xray mutations and avoids replacing the active outbound unnecessarily.

## Health and failover state machine

```text
VPN_ACTIVE
  ├─ successful health ───────────────┐
  └─ failure threshold reached        │
            │                         │
            ▼                         │
      PROBE_HOT_POOL                  │
       ├─ fallback works ──> VPN_ACTIVE
       └─ none works
            │
            ▼
        DIRECT_MODE
       ├─ probes continue
       └─ recovery threshold reached ─> VPN_ACTIVE
```

Failover is independent from the full benchmark. The benchmark is a long-running exclusive operation; health failover remains a higher-priority control path and Xray mutations are serialized inside the adapter.

## Xray ownership boundary

KRM does not rewrite arbitrary Xray configuration. It owns four clearly named fragments:

```text
00_90_kee_route_manager_api.json
03_90_kee_route_manager_inbounds.json
04_90_kee_route_manager_outbounds.json
05_90_kee_route_manager_routing.json
```

It also changes only matching rules in the configured base routing fragment, replacing an existing managed outbound tag such as `vless-reality` with the KRM balancer tag.

Before first modification KRM stores a persistent snapshot. Every candidate configuration is checked with `xray run -test` before installation. First bootstrap and restart are transactional; failures restore the previous files and restart Xray. Uninstall uses `restore-xray` to restore the first-install snapshot.

Runtime slot changes use Xray's local HandlerService and RoutingService API. The API listener is bound to loopback.

## Platform adapters

### Keenetic

XKeen retains responsibility for transparent interception. KRM uses local RCI/NDMC for metrics, clients, policy changes, WOL, reboot and logs.

### OpenWrt and Linux

Two firewall modes exist:

- `existing` — KRM does not modify interception rules;
- `managed` — KRM installs an IPv4 nftables prerouting table and policy route for configured LAN interfaces and Xray redirect/TProxy ports.

Managed firewall state is recreated idempotently. The uninstall path removes its table and policy route.

## UI deployment

### Embedded

The controller serves the same PWA and API on one HTTPS endpoint.

### UI proxy

A second KRM binary can run with `instance.role: ui-proxy`. It serves the PWA locally and reverse-proxies `/api/` to the controller. Authentication is still enforced by the controller. This is intended for a PC, Raspberry Pi or kiosk host.

## Update transaction

1. Download manifest and detached signature.
2. Verify the manifest with the embedded/configured Ed25519 public key.
3. Select the exact Linux architecture asset.
4. Stream it to a temporary file and verify size and SHA-256.
5. Save the old executable as `.previous` and write `update-pending.json`.
6. Atomically replace the executable and restart the service.
7. Mark the new version healthy after the configured grace period.
8. If the new process restarts before being marked healthy, restore the previous executable and re-exec it.
