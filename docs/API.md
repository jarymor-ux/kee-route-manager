# API and local control

Controller network API is authenticated HTTPS on loopback, local control on private Unix socket (OS owner authorization). Both reach the same daemon manager. UI proxies `/api/` and `/healthz` and owns static routes only.

`GET /healthz` returns cached readiness/reconciliation information, including explicit degraded state; it does not run external commands. `GET /api/v1/status` is read-only. Login/session/CSRF authorization remains core-side. Responses carry request IDs; internal errors are not exposed as raw root command output.

`POST /api/v1/actions/benchmark` reserves the operation before responding202 and returns `{ "accepted": true, "operation_id": "op-..." }`. A concurrent request cannot create a second benchmark reservation. Update apply is disabled and returns an explicit unavailable response.

Successful `restore-xray` persists `state.automatic_routing_paused: true`. Scheduled, subscription and health work cannot reinstall managed routing while paused, including after a daemon restart. A successful explicit benchmark through CLI or API resumes automatic routing; failed benchmarks and runs without a healthy candidate leave it paused.

CLI:

```sh
kee-route-managerctl status --config PATH
kee-route-managerctl ready --config PATH
kee-route-managerctl benchmark --config PATH
kee-route-managerctl switch --slot 1 --config PATH
kee-route-managerctl direct --config PATH
kee-route-managerctl restore-xray --config PATH
kee-route-managerctl update-check --config PATH
```

Offline: `validate`, `passwd --username NAME --password-stdin`, `route-candidates --file STRICT_JSON`. `passwd` must be run while no active sessions/controllers depend on changed credentials; installed controller reload requires explicit restart. No command starts another core manager. Optional `--socket PATH` selects a local control socket.

System journal contract is `{ "output": "..." }` in backend and frontend. Audit diagnostics must be redacted before export. Full endpoint behavior is tested in `internal/web/server_test.go` and documented by routes in `internal/web/server.go`.
