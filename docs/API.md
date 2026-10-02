# HTTP API overview

Base path: `/api/v1`.

The API is same-origin and JSON-only. Login creates a secure session cookie. Every mutation after login requires `X-KRM-CSRF` with the token returned by `/api/v1/session`.

| Method | Path | Purpose |
|---|---|---|
| POST | `/auth/login` | Create session |
| POST | `/auth/logout` | Destroy session |
| GET | `/session` | Current user and CSRF token |
| GET | `/status` | Runtime, operation, pool and capability summary |
| GET | `/nodes` | Sanitized nodes and measurements |
| GET | `/events?after=&limit=` | Incremental event journal |
| GET | `/router/metrics` | Router/system metrics with freshness |
| GET | `/router/clients` | Client list with freshness |
| GET | `/router/logs?lines=` | Bounded platform logs |
| GET | `/router/diagnostics` | Read-only connectivity diagnostics |
| POST | `/actions/benchmark` | Start full benchmark |
| POST | `/actions/switch` | Select hot-pool slot |
| POST | `/actions/direct` | Enter direct mode |
| POST | `/actions/xray-restart` | Restart Xray |
| POST | `/actions/reboot` | Reboot gateway when enabled |
| POST | `/actions/wake` | Keenetic WOL |
| POST | `/actions/policy` | Keenetic client policy |
| GET | `/update/check` | Verify signed manifest and report availability |
| POST | `/update/apply` | Download, verify and atomically install update |

Arbitrary shell execution is not exposed.
