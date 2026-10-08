# API and local control

Controller network API is authenticated and loopback-only. Shipped split-mode configs use plaintext HTTP on `127.0.0.1:9443`; explicit deployments may enable API TLS. Local control uses a private Unix socket (OS owner authorization). Both reach the same daemon manager. UI proxies `/api/` and `/healthz` and owns static routes only.

`GET /healthz` normally returns cached readiness/reconciliation information, including explicit degraded state. During update trial it performs read-only Xray API reconciliation and reports `trial_ready` with process/version/nonce identity. Internal prepare/activate endpoints exist only on the owner socket and are denied through the network API. `GET /api/v1/status` is read-only. Login/session/CSRF authorization remains core-side. Responses carry request IDs; internal errors are not exposed as raw root command output.

`POST /api/v1/actions/benchmark` reserves the operation before responding202 and returns `{ "accepted": true, "operation_id": "op-..." }`. A concurrent request cannot create a second benchmark reservation. `GET /api/v1/update/status` returns launcher availability, current/previous versions, cached signed discovery and progress. `GET /api/v1/update/check` refreshes discovery. `POST /api/v1/update/apply` accepts an optional `{ "version": "1.1.0-rc.7" }` and returns202 once queued; it never waits for the daemon to stop. A specified version must still match fresh signed discovery. The panel exposes installation only for a supported signed release and a running launcher.

Successful `restore-xray` persists `state.automatic_routing_paused: true`. Scheduled, subscription and health work cannot reinstall managed routing while paused, including after a daemon restart. A successful explicit benchmark through CLI or API resumes automatic routing; failed benchmarks and runs without a healthy candidate leave it paused.

If an operation finishes but its completion record cannot be persisted, status exposes `unknown` with a redacted persistence error and subsequent operations can run once storage recovers. The action returns an error, even when its routing effects succeeded; inspect state before retrying. The UI distinguishes paused and unconfigured routing from an active VPN, independently of whether the original Xray process is running.

CLI:

```sh
kee-route-managerctl status --config PATH
kee-route-managerctl ready --config PATH
kee-route-managerctl benchmark --config PATH
kee-route-managerctl switch --slot 1 --config PATH
kee-route-managerctl direct --config PATH
kee-route-managerctl restore-xray --config PATH
kee-route-managerctl update-status --config PATH
kee-route-managerctl update-check --config PATH
kee-route-managerctl update-apply --target-version VERSION --config PATH
```

Offline: `validate`, `passwd --username NAME --password-stdin`, `route-candidates --file STRICT_JSON`. `passwd` must be run while no active sessions/controllers depend on changed credentials; installed controller reload requires explicit restart. No command starts another core manager. Optional `--socket PATH` selects a local control socket.

System journal contract is `{ "output": "..." }` in backend and frontend. Audit diagnostics must be redacted before export. Full endpoint behavior is tested in `internal/web/server_test.go` and documented by routes in `internal/web/server.go`.


## Subscription management

Panel sessions with `subscriptions.manage` can manage subscription sources at runtime:

- `GET /api/v1/subscriptions` requires `subscriptions.view` or `subscriptions.manage`. Managers receive configured sources including URL and headers required by the editor; readers receive only ID, redacted name and enabled state. The response is marked `Cache-Control: no-store`; treat it as secret material.
- `POST /api/v1/subscriptions/save` accepts one source object with `id`, `name`, `url`, `enabled` and `headers`. Existing IDs are replaced; new IDs are appended.
- `POST /api/v1/subscriptions/delete` accepts `{ "id": "source-id" }`.

Mutation endpoints require the normal session, CSRF token and same-origin check. They run the same configuration validation as startup, so invalid IDs/URLs/headers, the source limit and deletion of the last configured source are rejected. Successful changes are persisted before becoming live and schedule a benchmark; errors returned to the browser never include upstream URLs, headers or persistence details.

## Users and permissions

Login and `GET /api/v1/session` return `user` (`id`, `username`, `enabled`, `permissions`, `updated_at`), top-level `username`, `permissions`, `csrf` and `expires_at`. User IDs remain stable across edits. Each network API request checks the current user and permissions; hiding a panel control is not authorization. Permission edits take effect for existing sessions on their next request. Password changes, rename, blocking and deletion revoke existing sessions. Already accepted operations can finish; revocation does not reverse routing effects.

`GET /api/v1/users` requires `users.manage` and returns `{ "users": [...], "permissions": [...], "roles": { "admin": [...], "viewer": [...], "vpn-operator": [...], "router-operator": [...] } }`. Roles are editable permission templates, not implicit runtime privileges. `POST /api/v1/users/save` accepts `{ "id": "optional-existing-id", "username": "name", "password": "optional-new-password", "enabled": true, "permissions": [...] }`. New users require a password; omission or an empty password keeps the existing password on edits. `POST /api/v1/users/delete` accepts `{ "id": "user-id" }`. Existing-user save/delete requests may include `expected_updated_at` with the exact timestamp returned by the user list. A stale precondition returns409 with `code: "user_changed"` and leaves the current user intact. The panel sends this token to protect simultaneous editors. User mutations require CSRF and same-origin checks; the initiating administrator is checked again under the user-store lock before applying the edit. The last enabled account with `users.manage` cannot be deleted, blocked or stripped of that permission. Successful create/edit/delete actions emit `user.created`, `user.updated`, `user.deleted` journal events with actor ID, target ID and generated request ID. Other accepted panel control mutations emit `api.action` events linking actor/request IDs, endpoint and operation ID when available; failed actions remain visible in access logs. Usernames are unique case-insensitively; login uses the saved spelling. Limits: 128 accounts including the reserved recovery account, username 3–64 bytes without controls, password 10–1024 bytes. Credential hashes never appear in API responses.

| Permission | Authorized endpoints |
| --- | --- |
| `vpn.view` | Status, nodes |
| `vpn.control` | Benchmark, cancellation, Xray restart, slot/direct switch |
| `subscriptions.view` | Subscription list without URL or headers |
| `subscriptions.manage` | Subscription editor including URL/headers, save and delete |
| `router.view` | Current metrics and history; status platform/capabilities without VPN state |
| `router.clients` | Devices and policies snapshot |
| `router.policy` | Change device policy |
| `router.wake` | Wake-on-LAN |
| `router.system` | System logs and diagnostics |
| `router.reboot` | Reboot |
| `updates.manage` | Update discovery/status/application and channel selection |
| `users.manage` | List, create, edit, block and delete users |
| `events.view` | Event journal |

Session, logout and the minimal status response require authentication but no additional permission. Full VPN status requires `vpn.view`. Unknown protected routes fail closed. The owner-only local Unix socket retains OS owner authorization, independent of panel users.

`POST /api/v1/actions/benchmark/cancel` requires `vpn.control`, joins canceled benchmark workers and returns202. `GET /api/v1/router/metrics/history` requires `router.view` and returns `{ "samples": [...] }`; history is bounded in memory and resets on daemon restart. Unsupported adapters/controllers return501 for these optional capabilities. Operation rejection responses contain a stable `code`: `busy`, `canceled`, `settings_changed`, `conflict` or `unavailable`, plus a public `error` without raw backend output.

### Credential migration and recovery

At first startup, the schema-1 `web.credentials_file` becomes the full-permission account `legacy-admin` in memory. An explicit user change persists the private sidecar `<credentials_file>.users.json`; loading and update-trial validation never write it. Panel changes affect only the sidecar: the original credential file remains usable by older versions. Consequently, rolling back restores the original single account/password and ignores new users and revoked rights. Preserve both private files in backups; do not treat a downgrade as preserving panel authorization policy.

For owner recovery, stop the controller, run the existing offline `passwd --username UNIQUE_NAME --password-stdin` command, and restart. A changed legacy credential fingerprint restores the stable `legacy-admin` account enabled with all permissions, preserving other accounts. Choose a name not held by another account. Merely restarting does not undo panel edits. The controller does not reload offline credentials while running. Invalid/corrupted user sidecars fail startup rather than silently restoring old credentials; restore a private backup, or move the corrupted sidecar aside while stopped to explicitly recreate the original administrator. Keep the moved file private.

## Update channels

`GET /api/v1/update/status` from a compatible launcher includes `channel` (`rc` or `stable`) and `channel_switch_supported`. `GET /api/v1/update/check` includes the selected `channel`; the signed manifest must match it. `POST /api/v1/update/channel` accepts `{ "channel": "stable" }` or `{ "channel": "rc" }`, requires `updates.manage`, CSRF and same origin, and returns the updated launcher status after durable persistence. It clears cached discovery without stopping processes or installing a version. Switching during application returns409. Invalid channels return400; disabled updates or manual-URL discovery return409. An unavailable/incompatible launcher does not authorize a setting change.

`POST /api/v1/update/apply` may also supply `channel` with `version`. A changed channel rejects the confirmation before queuing or downloading. The queued transaction captures its channel; old clients may continue omitting that field. Channel generations prevent stale in-flight checks from republishing availability after a switch, including switching away and back. Older launchers omit the capability fields, so their UI keeps switching disabled and sends the original apply request. Channel selection never enables downgrades: `1.0.0` is older than `1.1.0-rc.13`; `1.1.0` is newer.
