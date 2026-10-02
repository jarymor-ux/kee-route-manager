# Contributing

Read [AGENTS.md](AGENTS.md). Use small topic branches and coherent commits. Add a regression test for behavior or security fixes before changing code. No new runtime dependencies without maintainer agreement. Keep `web/` and embedded `internal/web/ui/static/` synchronized.

Run `./scripts/check.sh`, `go test -race ./...`, `staticcheck ./...`, `govulncheck ./...`, ShellCheck, `./scripts/fuzz-smoke.sh` and `./scripts/cross-build.sh`. Signed fixture packaging is checked by `./scripts/test-release.sh`.

Contributions are licensed under Apache-2.0. Never commit private release keys, subscription URLs, passwords, UUIDs, private router addresses or raw diagnostic output. Keep review/test reports, evidence logs and agent setup summaries outside the checkout. Share verification results in CI or discussion; hardware claims require reproducible device evidence kept privately, with maintained capability limits in [KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md).
