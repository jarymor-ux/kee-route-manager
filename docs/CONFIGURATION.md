# Configuration

Templates in `configs/` use strict YAML subset decoded with unknown-field rejection. Keep real subscriptions and node keys in private files outside the repository.

Controller: `instance.role: controller`, `api.enabled`, `api.listen` loopback-only, `api.unix_socket` within private run_dir; credentials under `web.credentials_file`, session TTL under `web.session_ttl`, API certificate under `api.tls`. `web.enabled` controls UI runtime only; controller never embeds it.

UI: `instance.role: ui`, `ui.enabled: true`, `web.enabled: true`, web listen/TLS, `ui.upstream` as an origin, `upstream_ca_file` and optional `upstream_spki_sha256`. Plaintext web/upstream only permitted on loopback. `insecure_tls` is explicitly unsafe and incompatible with configured CA/pin trust. Default templates never enable it.

Failover: `detection_interval: 5s`, `failure_threshold: 2`, `probe_timeout: 2s`, `overall_deadline: 5s`, `quorum: 2`. Independent health hosts are compared across VPN and WAN. Fewer independent targets yield inconclusive monitoring and preserve routing. `health.hot_pool_freshness` guards fallback freshness. Health recovery threshold, target policy/body limit and benchmark stability/improvement/cooldown remain independent.

`paths.log_file` is bounded to 1MiB plus three backups; private log permissions. Runtime directories/state/cache hold sensitive data. For systemd UI, writable TLS/log files belong in `/var/lib/kee-route-manager-ui`, not root-private core config.

Xray `managed_dir` must resolve to the same directory as absolute `config_dir`; `base_routing_file`, when set, must be a JSON file directly in that directory. Nested paths are rejected because Xray's `-confdir` loading is not recursive. Equivalent directory symlink aliases also work for restore, rollback and pending journal replay; unexpected/duplicate targets and changed file identities remain rejected. API/probe/health/benchmark/web ports cannot overlap. Managed tags must be unique and cannot begin with the reserved `krm-persisted-selection` prefix. Choose `route.inbound_tags`/`replace_outbound_tags` from strict-JSON `route-candidates`, not a hardcoded outbound. Firewall `existing` never claims independent bypass; `managed` requires own table/mark/policy-route ownership and correct redirect/TProxy inbounds.

After changing generated Xray API, probe or health configuration and restarting the daemon, the next benchmark refreshes the full managed configuration. This requires an Xray restart and may interrupt existing connections; unchanged configurations continue to use the configured pool-update mechanism.

`subscriptions.max_nodes_per_source: 0` uses automatic fair share; round-robin merge preserves provider representation before global max_nodes. Provider diversity accounts for all node source memberships.

Normal subscription cache reuse is bounded by both `refresh_interval` and `cache_ttl`. Expiry triggers a fresh fetch even before the refresh interval ends. If fetching fails, expired entries are marked unavailable and used only as emergency cache when no other provider nodes are available.

Update: `enabled: false` recommended in RC2; `auto_apply` must be false. `github_repository: jarymor-ux/kee-route-manager` can discover highest RC/stable matching prerelease channel from up to 100 recent releases. Selected signed manifest version must match tag, URLs HTTPS; `/releases/latest` rejected. Manual versioned URLs remain supported when repository discovery omitted.

Validate offline with `kee-route-managerctl validate --config PATH`. Validation does not prove Xray/network/hardware readiness; follow [installation](AGENT_INSTALL.md).

Local `file://` subscription sources must be regular files. The fetcher opens without blocking on FIFO producers, validates the opened descriptor and checks cancellation during bounded reads. Manual refresh still honors source retry backoff even when no cache exists.

Managed Linux/OpenWrt policy rules use explicit priority `10000`, an exact mark with full mask, and unrestricted source/destination selectors. KRM refuses conflicting priorities, selector drift and duplicate rules before reconciliation or cleanup. Legacy ownership records without a verified priority do not authorize adopting or deleting an existing rule; inspect and reconcile those resources explicitly before migrating.
