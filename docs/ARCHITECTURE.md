# Architecture

One Go module, four runtime boundaries:

```text
browser → UI (embedded PWA + TLS upstream proxy) → authenticated core API
CLI → owner-only Unix socket → same core handlers
core → TunnelCore (Xray adapter) + platform adapter → owned routing/firewall
launcher → daemon + optional local UI; signed release slots and update lifecycle
```

UI and CLI dependency graphs exclude core, Xray, platform, store and benchmark managers. Daemon acquires flock on both state directory and Xray configuration directory before constructing managers/schedulers; owner metadata records PID/start-time/random instance. Local socket authorization is OS directory 0700/socket0600 ownership; network API uses core credentials/sessions/CSRF.

Core reserves an operation synchronously before accepting benchmark requests. State/nodes/cache and operation logs persist privately. A durable journal records intended transition before Xray/files/firewall/selection/state effects, and startup observes/reconciles runtime before cached readiness. Explicit safe-degraded state is distinct from working VPN.

Benchmark updates pool/measurements while retaining selection and honoring improvement/stability/cooldown. Emergency probes use independent target quorum and WAN comparison, with parallel fallback verification and bounded deadline. Unknown WAN/target/DNS observations preserve selection; dead Xray may use independent managed-platform bypass if supported. Keenetic adapter has no verified automatic bypass capability.

Benchmark latency probes run with bounded node concurrency in one temporary Xray batch at a time. After latency completes, healthy finalists run download sampling through a bounded worker pool (`benchmark.speed.workers`). Warmup and adaptive repetitions remain sequential per node; results and progress are merged by the caller. Cancellation joins download workers before stopping the temporary Xray batch and returns an error instead of a completed benchmark. Parallel downloads measure shared-link throughput under load; `workers: 1` retains isolated sampling.

TunnelCore exposes lifecycle/capabilities/pool/selection/probe/readiness/actual-state/restore, enabling a future non-Xray adapter. Xray writes persistent concrete routing selection so restart does not select empty blackhole slots randomly. Restoration reverses owned patches while retaining unrelated routing edits; drift rejects unsafe overwrite.

Restore also durably pauses automatic routing until a successful explicit benchmark. Startup, source refresh and scheduled benchmark loops honor that pause so uninstall cannot race with routing reinstallation. Xray readiness failures reset consecutive recovery evidence.

When the primary state cannot be loaded, a validated completed restore journal preserves its desired paused state instead of reverting to a pre-restore snapshot. Pending manual-resume journals remain subject to normal runtime/file reconciliation. A failed operation-completion write releases the execution reservation, reports the error and exposes an unknown completion status; it does not authorize overlapping running operations.

Subscription comparisons track processed provider membership separately from deliberately retained pool nodes so unchanged providers do not trigger repeated benchmarks. Normal cache reuse stops at its TTL; expired entries remain emergency candidates only when no non-emergency provider nodes are available. Xray's persistent-selection tag prefix is reserved, and snapshot/replay paths may use equivalent directory aliases only when the entire owned file set and recorded content identities still match.

Managed nft replacement is one validated transaction in the owned table; route ownership metadata protects foreign tables/rules. Existing interception remains operator-managed. [nftables atomic replacement](https://wiki.nftables.org/wiki-nftables/index.php/Atomic_rule_replacement) documents the transaction contract.

The stable launcher owns only the executable release slots and its process tree. The daemon remains the exclusive owner of mutable controller state, Xray and firewall. Initial installation imports a signed protocol-1 bundle into a private immutable version directory and points standard daemon/CLI entrypoints through an atomic `current` symlink. Keenetic/OpenWrt local UI shares that slot and parent; standalone UI and Linux DynamicUser UI retain their independent service. The stable launcher itself is never replaced by an automatic download.

Update discovery periodically checks the configured RC/stable channel; application always requires an explicit authenticated action. The launcher stages and verifies the native daemon/UI/CLI bundle, then asks the current daemon to freeze mutations and drain work. The candidate starts in a read-only trial with a random nonce. Readiness requires the expected process PID, version, nonce and completed reconciliation through the local API, the configured loopback network API using HTTP or HTTPS according to `api.tls.enabled`, and the managed UI proxy. A failed trial restarts the previous verified slot. No controller-state snapshot is restored.

The launcher durably commits the new release identity before activating its mutable runtime. A crash before commit recovers the old slot; a crash after commit restarts the new slot, because its activation may already have changed controller state. The owner-only launcher socket exposes update status/check/apply behind the daemon's authenticated API and CLI. Production installers, bootstraps and component binaries belong to the same signed immutable release. See [release procedure](RELEASE.md) for publication and recovery limits.
