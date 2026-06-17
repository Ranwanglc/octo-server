# Operator-Audit Log — admin/management writes (#367 Phase 1)

Forensic "who did what" trail for the admin/management write surface. Every
mutating request to `/v1/manager` emits **one** structured log line. This is
**Phase 1 (logging only)** — Phase 2 (the centralized admin-action audit table,
SecurityEngineer workstream) consumes these lines into durable storage.

- Implementation: `pkg/accesslog/operator_audit.go`
  (`OperatorAuditMiddleware`), wired as a global middleware in `main.go`.
- Scope of writes audited: HTTP `POST` / `PUT` / `PATCH` / `DELETE` whose request
  path is `/v1/manager` or under `/v1/manager/…` (covers `user`, `message`,
  `group`, `workplace`, `robot`, `common`, and any future manager subgroup —
  matching is on the raw request path, so new routes are covered automatically).
- Read traffic (`GET`/`HEAD`) and non-manager routes are **not** audited.

## Line format

The line is emitted via the standard zap logger (`log.NewTLog("ManagerAudit")`),
so it also carries the logger's own `ts` and `level`. Audit-specific fields:

| field         | type   | meaning                                                              |
| ------------- | ------ | -------------------------------------------------------------------- |
| `audit`       | string | constant `manager_admin_action` — stable discriminator for ingest.  |
| `operator_id` | string | acting user/agent id (`c.GetLoginUID()` / `c.Set("uid")`).           |
| `action`      | string | HTTP method: `POST` \| `PUT` \| `PATCH` \| `DELETE`.                 |
| `target`      | string | scrubbed request path identifying the resource acted on.             |
| `status`      | int    | final HTTP response status code.                                     |
| `ts`          | string | RFC3339 UTC timestamp when the line was emitted.                     |

The zap message string is also `manager_admin_action`, identical to the `audit`
field. Phase 2 can key ingest off either.

### Reading `operator_id`

- Non-empty `operator_id` + 2xx `status` ⇒ an authenticated admin successfully
  performed the action.
- **Empty** `operator_id` (paired with a 4xx `status`) ⇒ an unauthenticated or
  auth-rejected attempt. These are intentionally audited too, as attempt
  forensics; Phase 2 may choose to store or filter them.

## Security-by-default (what is deliberately NOT logged)

- **No request bodies.** The middleware never reads the body, so secrets that
  ride in bodies cannot leak — notably `POST /v1/manager/user/resetpassword`
  and `POST /v1/manager/user/updatepassword`.
- **No query string.** `target` is the URL *path* only, run through
  `ScrubPath` (the same scrubber used by the access logger), so even a path that
  embeds a token is masked.
- Consequence for Phase 2: the path names the target *surface* (e.g.
  `/v1/manager/robots/:robot_id`); for actions whose target id lives in the body
  (e.g. password reset), Phase 1 records the operator + action + endpoint but
  not the body-side target. Capturing richer, safe target detail is Phase 2's
  job in the audit table.

GitHub: https://github.com/Mininglamp-OSS/octo-server/issues/367
