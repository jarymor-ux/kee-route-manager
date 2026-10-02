# RC2 breaking changes

1. Separate daemon/UI/CLI binaries and processes. Legacy executable is a CLI alias and cannot run the controller.
2. Core HTTPS API defaults to loopback; UI listens on 9444. Remote core access uses a trusted UI proxy or SSH tunnel, not a public root API listener.
3. `api` and `ui` configuration sections replace monolithic controller web runtime; credentials remain core-side. UI upstream TLS requires normal CA verification and optional SPKI pinning.
4. Config validation rejects zero/negative durations, resource/port/path/tag conflicts and insecure listener defaults. Two independent health hosts/quorum are required for conclusive automatic failover.
5. Benchmark preserves a working route; it cannot enable direct when subscriptions/targets return no healthy candidate.
6. Runtime state is validated and previous copy kept. Transactions are durable and replayed against actual runtime/files/firewall before readiness. Recovery may explicitly report degraded rather than silently trusting old JSON.
7. Persistent concrete Xray routing selection replaces random-balancer reliance. Restore reverses only owned changes and refuses irreconcilable drift.
8. Unsafe executable replacement removed; `update.apply` and `auto_apply` unavailable. Update discovery uses channel-aware GitHub release selection or an explicitly pinned HTTPS manifest.
9. RC2 uses a new signing public key; RC1 trust/assets remain unchanged. Production bootstrap verifies every binary and installer from one release.
10. Installer expects a prepared private configuration; no hardcoded legacy outbound name, no silent migration or overwrite. Credentials must be supplied safely through terminal or private file.
11. Keenetic fail-open independent of Xray is unsupported until a verified platform mechanism and real hardware evidence exist.

RC1 state/config compatibility is not promised. Back up, restore original routing, uninstall, then clean-install RC2.
