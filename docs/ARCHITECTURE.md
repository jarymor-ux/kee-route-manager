# Architecture

One Go module, three runtime boundaries:

```text
browser → UI (embedded PWA + TLS upstream proxy) → authenticated core API
CLI → owner-only Unix socket → same core handlers
core → TunnelCore (Xray adapter) + platform adapter → owned routing/firewall
```

UI and CLI dependency graphs exclude core, Xray, platform, store and benchmark managers. Daemon acquires flock on both state directory and Xray configuration directory before constructing managers/schedulers; owner metadata records PID/start-time/random instance. Local socket authorization is OS directory 0700/socket0600 ownership; network API uses core credentials/sessions/CSRF.

Core reserves an operation synchronously before accepting benchmark requests. State/nodes/cache and operation logs persist privately. A durable journal records intended transition before Xray/files/firewall/selection/state effects, and startup observes/reconciles runtime before cached readiness. Explicit safe-degraded state is distinct from working VPN.

Benchmark updates pool/measurements while retaining selection and honoring improvement/stability/cooldown. Emergency probes use independent target quorum and WAN comparison, with parallel fallback verification and bounded deadline. Unknown WAN/target/DNS observations preserve selection; dead Xray may use independent managed-platform bypass if supported. Keenetic adapter has no verified automatic bypass capability.

TunnelCore exposes lifecycle/capabilities/pool/selection/probe/readiness/actual-state/restore, enabling a future non-Xray adapter. Xray writes persistent concrete routing selection so restart does not select empty blackhole slots randomly. Restoration reverses owned patches while retaining unrelated routing edits; drift rejects unsafe overwrite.

Managed nft replacement is one validated transaction in the owned table; route ownership metadata protects foreign tables/rules. Existing interception remains operator-managed. [nftables atomic replacement](https://wiki.nftables.org/wiki-nftables/index.php/Atomic_rule_replacement) documents the transaction contract.

Updates authenticate channel manifests but never replace running executables in RC2. Production installers are part of the same signed release payload; readiness and manual rollback remain operator/agent visible.
