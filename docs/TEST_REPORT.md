# Test report — 1.0.0-rc.1

Date: 2026-10-02

## Automated verification

| Check | Result |
|---|---:|
| Go unit/integration tests | 27 passed, 0 failed |
| Go packages completed | 14 |
| Statement coverage | 29.1% |
| Race detector (`go test -race ./...`) | passed |
| Static analysis (`go vet ./...`) | passed |
| Go formatting | passed |
| Keenetic shell syntax | passed |
| OpenWrt shell syntax | passed |
| Linux/UI-proxy Bash syntax | passed |
| Web JavaScript syntax | passed |
| Embedded web assets equal source assets | passed |
| Strict validation of all four example YAML files | passed |

The tests cover credentials and sessions, target-majority logic, configuration parsing and rejection, active-node retention during pool refresh, persistent operation recovery, nftables rendering, atomic state/event storage, VLESS parsing and deduplication, subscription cache/backoff/recovery, signed-update verification and rollback primitives, web authentication/proxy behaviour, Xray configuration generation, routing adoption, original-config snapshots and bootstrap selection.

## Cross-compilation

Static `CGO_ENABLED=0` binaries were successfully built for:

- Linux amd64;
- Linux arm64;
- Linux armv7 (`GOARM=7`);
- Linux mipsle (`GOMIPS=softfloat`).

The resulting files were recognized as the expected ELF architecture and were included in the release bundle.

## Security and failure-safety checks

- A tampered update manifest is rejected by Ed25519 verification.
- Update assets are checked against the SHA-256 digest and size from the signed manifest.
- Empty hot-pool slots are `blackhole`, not direct, and the balancer does not select the direct outbound by default.
- Direct routing is entered only by an explicit fail-open override after VPN candidates are exhausted.
- Xray candidate configuration is validated before production files are changed.
- Original Xray fragments are retained for uninstall/restore.
- State, operation records and managed files use atomic temporary-file replacement.
- Managed nftables rules are syntax-checked under a temporary table name before replacing KRM's own table.
- Benchmark response bodies are discarded and never stored as files.

## Not executed in this environment

This report does **not** claim hardware validation on a real Keenetic, OpenWrt router or production Linux gateway. In particular, the following still require RC deployment tests:

- XKeen/Xray restart and dynamic API behaviour on the target Keenetic firmware;
- procd/firewall4 behaviour on a selected OpenWrt release;
- live TProxy/redirect behaviour for the user's LAN layout;
- real multi-provider subscriptions and hour-long provider outages;
- throughput/CPU/RAM measurements on mipsle and armv7 devices;
- signed self-update followed by service-manager restart on each platform.

For that reason the version remains `1.0.0-rc.1`, not stable `1.0.0`.
