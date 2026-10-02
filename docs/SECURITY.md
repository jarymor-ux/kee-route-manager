# Security

## Trust boundary

KRM is a privileged local network service. It controls routing, Xray configuration and, optionally, firewall rules. Do not expose its web port directly to the public Internet.

## Web authentication

- Login and password are selected by the operator during installation.
- The password is stored as PBKDF2-HMAC-SHA256 with a random salt and 600,000 iterations.
- Password input is accepted through stdin, not a command-line argument.
- Session identifiers and CSRF tokens are generated with `crypto/rand`.
- Cookies are `HttpOnly`, `Secure` when TLS is enabled, and `SameSite=Strict`.
- Mutations require both a valid session and the matching `X-KRM-CSRF` header.
- Browser mutation requests are restricted to the same origin.
- Login attempts are rate-limited per remote address.
- CSP, frame denial, MIME sniffing protection and restrictive browser permissions are enabled.

## TLS

The built-in server uses HTTPS by default. Automatic TLS creates a local self-signed ECDSA certificate. This encrypts traffic but does not establish public trust; install a trusted local certificate where appropriate.

## Command execution

The HTTP API never accepts an arbitrary command. Platform adapters construct fixed command arrays and validate MAC addresses, interfaces, policies, line limits and action types.

## Configuration and subscriptions

- YAML is parsed by a strict non-executable parser.
- Unknown and duplicate fields are rejected.
- Subscription and target responses have size limits.
- VLESS URI length and supported transport are validated.
- Subscription headers containing CR/LF are rejected.
- Node identity uses a truncated SHA-256 fingerprint over normalized connection parameters.

Subscription URLs, UUIDs and Reality keys are secrets. Do not commit populated configuration files or diagnostic bundles.

## Xray transaction safety

- KRM writes only named managed fragments and one configured base route.
- Candidate confdirs are validated before installation.
- Writes use temporary files and atomic rename.
- First bootstrap keeps both a transient rollback snapshot and a persistent uninstall snapshot.
- Runtime pool replacement changes the active slot last and attempts reverse-order rollback on failure.

## Release signing

Release manifests are signed with Ed25519. Asset size and SHA-256 are verified after download. Only the public key belongs in the repository and configuration.

The private key generated alongside this source archive is a separate sensitive artifact. Store it offline and rotate the configured public key if it is exposed.

## Reporting

Before sharing logs or diagnostics, remove subscription URLs, UUIDs, hostnames, IP addresses, public keys and short IDs. The previous diagnostic collector was not included as a release utility because its redaction behavior was not strong enough on older `jq` versions.
