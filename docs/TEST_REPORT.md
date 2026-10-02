# Test report — 1.0.0-rc.2

Date: 2026-10-02. Software verification only; real router acceptance is a separate owner-approved stage.

## Verification

| Gate | Result / scope |
|---|---|
| `scripts/check.sh` | Passed: Go tests/vet/formatting, shell and JavaScript syntax, embedded assets, example validation, adversarial bootstrap |
| `go test -race ./...` | Passed locally; Linux CI repeats this gate |
| `staticcheck ./...` | Passed with Go-1.27-compatible pinned development version |
| `govulncheck ./...` | No vulnerabilities found; CI pins v1.8.0 for Go 1.27 SSA support |
| ShellCheck | Passed for installers, init scripts and test/build scripts |
| Fuzz smoke | Five parsers/boundaries, two seconds each; passed |
| Linux runtime subprocess integration | Passed in Docker: real daemon/UI/ctl, duplicate ownership rejection, socket0600, cached reads without file/process mutation, synchronous benchmark ID, trusted TLS, foreign-Origin rejection, static assets HTTP200 |
| Linux install/uninstall/reinstall | Passed in Docker: private prepared config, credentials, local UI, readiness, overwrite rejection, live restore, purge and clean reinstall |
| Real nftables / iproute2 | Passed in Docker with explicit NET_ADMIN: atomic managed table, bypass retained during reconcile, bypass exit, remove/reinstall, foreign table preserved |
| Cross-build | All four components for linux amd64/arm64/armv7/mipsle: 16 static binaries |
| Signed release fixture | Passed: complete build, Ed25519 manifest and checksums, every asset digest/size, deliberate binary tamper rejection |
| Bootstrap adversarial tests | All three platforms: invalid signatures, altered payload/checksums/size/path and signed wrong-version replay rejected before installer execution |
| Routing journal crash tests | Subprocess SIGKILL at six durable stages; recovery checks with fake external resources passed |
| Provider outage | One hour advanced by fake clock: quota fairness, backoff, cached emergency nodes and recovery passed |

Install fixture uses a service-manager shim with actual KRM processes; it does not qualify systemd/procd or Keenetic firmware. Real nftables tests use a Linux container kernel/network namespace, not a router's existing firewall topology. The unconfigured installer fixture correctly reports degraded readiness; configured Xray routing requires hardware acceptance.

The first GitHub race run exposed test teardown that removed a temporary directory before a benchmark's final event write. The test now joins the manager before cleanup, including failure paths; targeted race repetitions cover this fix. Release publication requires green CI on the exact merged commit; evidence is available in [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions) and [PR #3](https://github.com/jarymor-ux/kee-route-manager/pull/3).

## Hardware acceptance — pending

No actual Keenetic/OpenWrt router or production Linux gateway was used. The owner will provide router access later. [HARDWARE_TEST_PLAN.md](HARDWARE_TEST_PLAN.md) tracks clean installation, discovery/removal of the prior deployment, Xray external restart, reboot, failure scenarios, restore/reinstall and throughput/CPU/RAM.

Keenetic independent direct bypass is explicitly unsupported. Existing unmanaged interception cannot be independently bypassed. IPv6 leak protection is not claimed. Automatic update apply is disabled; A/B launcher/rollback tests were not executed. Real subscriptions, power loss, firmware service managers and architecture-specific resource measurements remain pending.

This is an **experimental prerelease**, not a hardware-qualified stable release. See [KNOWN_LIMITATIONS.md](KNOWN_LIMITATIONS.md).
