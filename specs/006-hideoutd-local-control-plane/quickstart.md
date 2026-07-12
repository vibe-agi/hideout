<!-- markdownlint-disable MD013 -->

# Quickstart: hideoutd Local Control-Plane Daemon

Validation guide. Each scenario maps a requirement to unit or Gate 0 evidence.
This is local control-plane machinery, not an isolation claim, so no real-Lima
gate is used.

## 1. Build And Static Gates

```bash
go test ./...
scripts/test-gate0.sh
```

Expected: green. New coverage: `internal/daemon` lifecycle/auth/event/background
tests, the `schemas/daemon-status.schema.json` and `schemas/daemon-event.schema.json`
validation, and the daemon smoke.

## 2. Single-Instance Lifecycle (unit) — FR-001, FR-012

Start a daemon on a temp store; assert exactly one instance, that a second start
reports the existing one (no race), that `status` is inspectable, and that `stop`
performs ordered shutdown leaving no socket/lock. Then simulate a stale
socket/lock and assert the next start reclaims safely or fails closed with a
diagnostic — never two live daemons.

## 3. Transport Placement Fails Closed (unit) — FR-002, FR-003, SC-007

Assert the primary socket is placed under the private store runtime dir and that a
guest-visible/non-private placement (workspace path, world-readable ancestor)
prevents start. Assert no unauthenticated API is served and no non-local address is
bound. Assert the native-vs-real-backend split: placement excludes real backends
structurally; token auth is the defense for a UID-sharing native target.

## 4. Authentication And Unauth Audit (unit) — FR-004, SC-001

Drive requests with no token, a wrong token, an expired token, and the valid
operator token; assert the invalid ones (including the expired token) are refused
with no state change and the valid one succeeds. Assert each refusal is recorded in
the daemon-local, session-unbound audit log with channel and reason, and that no
client-supplied token material appears anywhere in the log.

## 5. Embedded Parity (unit) — FR-005, SC-002

Assert the daemon-served Manager route set equals the embedded handler's complete
production `manager.ManagerRoutes()` registry as a drift guard. Run at least one plan/apply
through the daemon and assert plan, apply, and result are identical to the
embedded-mode equivalent. Assert the daemon's own status/event endpoints live
outside `/api/v1/…`.

## 6. Daemon-less Zero Regression (unit) — FR-006, SC-006

Assert the existing CLI/TUI/WebUI flows pass unchanged with no daemon running — the
Core event-observer seam is nil in embedded construction and emits nothing.

## 7. Live Events, No Polling, Redacted (unit) — FR-007, FR-008, SC-003, SC-004, SC-010

Subscribe a client, drive an operation and an audit write, and assert corresponding
ordered events arrive without any polling read after a single `overview` seed. Seed
control-plane material into an audited operation and assert it never appears in any
event while local user data remains verbatim. Assert a restart replays no history.

## 7b. Mid-Stream Credential Lifetime (unit) — FR-004

With an active subscription, expire/revoke/rotate the operator credential and assert
the stream is terminated with a terminal event. Assert a resubscribe with the stale
credential is refused and recorded in the daemon-local audit log, and that a
resubscribe with a fresh valid credential succeeds.

## 8. Backpressure (unit) — FR-013

Attach a slow subscriber and assert it is disconnected with a terminal event, and
that operations and other subscribers are unaffected and daemon memory stays bounded.

## 8b. Daemon-Specific Endpoints Are A Separate Surface (unit) — FR-016

Assert the daemon's own endpoints — loopback-only `GET /`, `GET /daemon/status`,
`POST /daemon/stop`, `GET /daemon/events`, and `POST /daemon/background` — live
outside `/api/v1/…`, add no Manager operation class, and are subject to the same
authentication and redaction as Manager routes.

## 9. Surfaces Consume Events (unit) — FR-009

Scope: 006 delivers event-triggered refresh, verified at the plumbing level. Assert
the WebUI is served over the daemon loopback UI transport and its HTML opens an
`EventSource` on `/daemon/events` (browser query-param token accepted; wrong token
refused), so the panels re-fetch on events rather than on a polling timer. Assert
the TUI consumes the same stream via `daemon.SubscribeEvents` (a refresh signal per
event; the channel closes when the stream ends and the TUI falls back to interval
polling). No console redesign; TUI stays lightweight parity. Payload-driven panel
state (zero further overview reads) and end-to-end user-visible live-refresh
verification are deferred (FR-009 scope note).

## 10. Background Ownership And Status (unit) — FR-010, SC-008

Submit an existing typed environment stop/clean apply as background work through the
`POST /daemon/background` product endpoint; assert status transitions through
completion under the same plan/apply semantics as foreground, and that daemon stop
leaves zero headless work and zero live transport endpoints. Assert no new typed
operation class was added (session cleanup and
run/status are out of v1 background scope).

## 11. Restart Fails Closed For Orphans (unit) — FR-011, SC-005

With a fabricated pre-restart live-resource record and no current-instance ownership,
restart the daemon and assert the resource is reported and audited as an orphan —
failed closed, not re-adopted, not destroyed.

## 12. Confirmation-Required Fails Closed (unit) — FR-014, FR-015, SC-009

Submit a confirmation-required operation to the daemon without CLI/WebUI-supplied
confirmation and assert it fails closed — the daemon neither prompts nor treats the
missing prompt channel as approval.

## 13. Schema And Docs (Gate 0)

`scripts/test-gate0.sh` validates `schemas/daemon-status.schema.json` and
`schemas/daemon-event.schema.json` and the doc updates (`docs/STATUS.md`,
`docs/tui-webui-experience.md`, `docs/threat-model.md`,
`docs/manager-control-plane.md`, `docs/privacy-run-test-plan.md`), and runs the
daemon smoke.
