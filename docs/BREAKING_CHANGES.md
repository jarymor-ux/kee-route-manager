# Breaking changes from blanc-auto / blanc-display

Kee Route Manager 1.0.0-rc.1 is a clean replacement, not an in-place upgrade.

1. Product and commands are renamed to `kee-route-manager`.
2. The shell runtime and Python/Qt panel are replaced by one Go service and a Web/PWA frontend.
3. Normal control no longer uses SSH. The UI calls a local HTTPS API.
4. Old `.conf` files, sourced shell configuration, `targets.tsv`, state files and command names are unsupported.
5. Configuration is strict YAML under `/opt/etc/kee-route-manager` or `/etc/kee-route-manager`.
6. Automatic migration is intentionally absent.
7. The Xray layout changes from one fixed `vless-reality` outbound to managed hot-pool slots plus a balancer.
8. The service takes ownership only of its four named fragments and the selected legacy routing rule.
9. Web login/password are mandatory and created during installation.
10. The native 480×320 Qt UI is removed. The responsive PWA supports phones, PCs and kiosks.
11. Fail-open direct routing is an explicit system state when all VPN fallbacks fail.
12. Subscription sources are merged and deduplicated. Provider priority does not exist in RC1.
13. Only the existing VLESS Reality/TCP and VLESS WS/TLS formats are retained.
14. Updates require a signed manifest; unsigned binaries cannot be applied by the updater.
15. `blanc-auto` and KRM schedulers must not run concurrently against the same Xray configuration.
