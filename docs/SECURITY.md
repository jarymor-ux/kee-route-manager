# Security boundaries

KRM controller runs with privilege to manage owned Xray routing/interception. Anyone able to alter its private config, credentials, state, socket or signing key is trusted at that boundary. UI has no controller/process execution dependency; systemd UI runs DynamicUser. Only expose UI to intended trusted LAN clients; do not expose root API/socket to WAN.

Authentication: bounded username/password input, bounded PBKDF2 work/cost, global verification semaphore and rate limit with stale-entry collection, expiring sessions and CSRF/Origin checks. Unix control access uses owner-only filesystem permissions. HTTP errors return stable public messages/request IDs while audit logs retain redacted access outcomes. Health/readiness uses cached runtime state rather than executing external programs per request.

UI validates Origin before rewriting proxy headers. Upstream validates CA/hostname and optional SPKI SHA256; `insecure_tls` emits a warning and is not used by installers/templates. HSTS is only sent on actual TLS connections. Service worker caches static assets only, never API/health.

Config bounds duration/resources/ports/tags, rejects non-loopback API, unsafe path containment and HTTPS update downgrade. Runtime state validation/fallback and write-ahead reconciliation make errors visible. No claim of protection from root or physical compromise; state holds node credentials privately.

Supply chain: one version-pinned GitHub release, pinned bootstrap Ed25519 key, signed manifest plus signed checksum list, verification BEFORE downloaded code executes. Installer/config/services are in signed payload. Bootstrap itself is initially trusted via HTTPS/authenticated repository; a compromised source/signing account remains in the trust boundary. RC2 signing key rotation is separate from immutable RC1.

Update application is disabled because RC2 has no verified A/B launcher. Channel discovery authenticates selected tag/version; it does not execute downloaded updates. Open-source dependency checks use [Govulncheck](https://go.dev/doc/security/vuln/); no scan guarantees absence of unknown vulnerabilities.

Redaction masks subscription URL query/userinfo, Authorization/Cookie, UUID/Reality fields and diagnostics host/IP where supported. Do not export raw config/state/router backups, URLs or raw upstream errors. Private files mode0600, logs bounded1MiB+3 backups. Review diagnostic content before sharing.

Remaining risks: actual platform kernel/service/XKeen behavior awaits hardware tests; Keenetic independent bypass unsupported; manual restore can require operator handling on drift; IPv4-only managed interception does not claim an IPv6 leak-prevention policy. See [known limitations](KNOWN_LIMITATIONS.md) and [private reporting](../.github/SECURITY.md).

Xray loopback TCP API is unauthenticated by Xray design. Process locks serialize KRM owners, but do not prevent another local process calling Xray directly. On multiuser Linux, local users must be trusted or the administrator must restrict that API port by OS/firewall policy; do not expose it off-host. KRM socket permissions do not authenticate Xray gRPC.
