<!-- markdownlint-disable MD013 -->

# Research: hideoutd Local Control-Plane Daemon

All decisions are grounded in the current code; file:line references are from the
codebase survey performed for this plan.

## Decision: Serve the daemon by mounting `manager.API.Handler()` verbatim — parity by construction

**Rationale**: `manager.API` already encapsulates the entire typed surface: its
`ServeHTTP`/`Handler()` runs `checkHost` → `authorize` → `checkOrigin` → resource
dispatch (`internal/manager/api.go:126-145`), and `authorize` accepts a Bearer or
`X-Hideout-UI-Token` credential with a constant-time compare and TTL expiry
(`api.go:846-870`). The daemon constructs an `API` bound to the same `Core` and
serves `api.Handler()` over its transport, so FR-005 parity is structural — the
daemon cannot drift from embedded behavior because it runs the same handler. The
served route set is fixed and enumerated in
[contracts/api-parity-matrix.md](contracts/api-parity-matrix.md).

**Alternatives considered**: A curated v1 route subset — rejected in clarification
(underbuilds FR-005 and adds allowlist logic). A reimplemented daemon router —
rejected: duplicates validators and invites drift.

## Decision: Primary transport is a store-runtime Unix socket; loopback stays the tokened UI transport

**Rationale**: `StartLocalServer` today binds `net.Listen("tcp", "127.0.0.1:0")`
behind `hostGuard`/`originGuard` (`internal/manager/server.go:64-96`) — that is the
short-lived command-scoped UI server and remains the daemon's loopback UI
transport. The daemon's PRIMARY transport is a Unix socket under a private runtime
subdirectory of the store, whose placement is validated by the same store-reserved
and workspace-safety guards that already protect the store. This placement
structurally excludes real backend guests (Lima): the socket is not under any
workspace, HostFS grant, passthrough mount, or guest-visible path
(`docs/threat-model.md:78-84`). For a weak native target sharing the operator UID,
placement is not an isolation boundary — the operator token (Decision below) is the
sole defense there. `API.checkHost`/`checkOrigin` are HTTP-host concepts; over a
Unix socket the daemon sets the served `API`'s allowed host/origin to the
loopback-equivalent it already accepts, so the reused handler stays satisfied.

**Alternatives considered**: A loopback TCP port as the primary API — rejected: the
threat model forbids an unauthenticated local API and does not treat loopback as a
trust boundary (`threat-model.md:85-87`); the socket placement is the structural
guest exclusion. An abstract-namespace socket — rejected: not filesystem-guarded.

## Decision: One operator token, minted once and persisted 0600 under the daemon runtime dir; reuse `authorize`

**Rationale**: `authorize` already does the credential check; the daemon supplies
the token it mints (reusing the `NewUIToken` generator used by `StartLocalServer`,
`server.go:49-55`) and persists it 0600 in the runtime dir so a client (CLI/TUI/
WebUI) can read it with operator file permissions. v1 ships only the operator token;
the read-only token is deferred (clarification), so no role logic is added. OS peer
identity is deliberately insufficient — the token is required for every request,
which is the threat model's stated reason tokens exist
(`threat-model.md:92-94`).

**Alternatives considered**: OS peer-credential auth (SO_PEERCRED) as the sole
gate — rejected: insufficient for a weak native target sharing the operator UID.
A read-only token in v1 — deferred in clarification.

## Decision: Live event fan-out from operation lifecycle + audit tail; no durable log

**Rationale**: The Manager Core has no event/observer mechanism today — operations
are synchronous request→result. The daemon therefore owns a small in-process
publisher: it wraps each operation it executes to emit start/progress/complete
events, and it tails each session `audit.jsonl`, republishing new records as
events. The audit tail reuses the existing redacted read path — `readAuditEvents`
applies `audit.RedactDetails` at read (`internal/manager/manager.go:488-497`), and
`boundary_summary.go:48` shows the scanner pattern — so streamed audit events are
control-plane-stripped by reuse (FR-008). There is no durable event log: a
(re)connecting client seeds current state with one `overview` read and then
consumes events (zero steady-state polling), and a restart replays nothing. Each
subscriber has a bounded buffer; a slow consumer is dropped with a terminal event
rather than stalling operations (FR-013).

**Alternatives considered**: A durable event journal with replay — rejected in
clarification: it adds persistent state that contradicts the restart-fail-closed
model and inflates the slice. Client polling — rejected: the whole point is live
fan-out (`docs/tui-webui-experience.md:30-31,189`).

## Decision: A minimal nil-default event-observer seam on Core so embedded mode is unchanged

**Rationale**: To emit operation lifecycle events the daemon needs a hook where
operations run. Adding an optional observer field on `Core` (or the operation
options) that is nil in embedded construction means embedded CLI/WebUI paths emit
nothing and behave byte-for-byte as today (FR-006/SC-006). The daemon sets the
observer to its publisher. Go owns the seam; scripts and clients never see it.

**Alternatives considered**: Deriving all events from the audit tail only —
rejected: operation progress (start/in-flight) is not always an audit event, and
the UI contract expects operation lifecycle. Always-on global event bus — rejected:
perturbs embedded behavior and complicates the zero-regression proof.

## Decision: Restart fails closed by holding only in-memory ownership since the current start

**Rationale**: Because there is no durable event/ownership log (Decision above), a
restarted daemon has no record of what a prior instance started. A running resource
that survives the restart (a live Lima environment, a port bridge) therefore cannot
be proven to belong to a session the current daemon owns, so it is failed closed:
reported and written to the daemon-local audit log as an orphan, and NOT re-adopted
as daemon-managed background work. Fail-closed here means refuse silent
re-adoption — it does NOT destroy the resource (destruction would be its own
authority); the operator handles orphans through the normal typed env/session
commands. `session.ValidID` and the session layout (`internal/session/session.go`)
identify sessions; ownership is the daemon's in-memory set since its current start.

**Alternatives considered**: Persisting an ownership journal to re-adopt across
restart — rejected: durable state contradicting the no-log decision, and re-adoption
is exactly the silent behavior the threat model forbids (`threat-model.md:96-97`).
Destroying orphaned resources on restart — rejected: destructive, and not this
feature's authority.

## Decision: Single instance per store via a runtime lock plus stale-endpoint detection

**Rationale**: FR-001 requires exactly one daemon per store. A lock file in the
runtime dir plus a liveness probe of the existing socket (attempt a connect) lets a
second `start` report the existing instance instead of racing it. A stale
socket/lock left by a crash is detected (connect refused / lock not held) and either
safely reclaimed or the start fails closed with a diagnostic — never two live
daemons on one store (Edge Cases, FR-012).

**Alternatives considered**: PID-file only — rejected: PID reuse races; a socket
connect probe is the authoritative liveness signal. No lock (rely on socket bind
error) — rejected: does not distinguish stale from live cleanly.

## Decision: Daemon-local, session-unbound audit log for lifecycle and auth refusals

**Rationale**: Unauthenticated/invalid-token refusals happen before any session or
profile context exists, so they cannot use session audit. The daemon writes an
`audit.Event` stream via `audit.NewFile` (`internal/audit/audit.go:62`) to a log in
its runtime dir, using the same deterministic `RedactDetails` at emit. It records
the channel and refusal reason and NEVER any client-supplied token material (the
handler compares tokens but the log stores only pass/fail + reason). Lifecycle
events (start/stop/restart-orphan) go to the same log.

**Alternatives considered**: Reuse a default-profile session audit — rejected in
clarification: forces a session/profile context that does not exist at refusal time
and muddies user-session semantics. Plain text log — rejected: must share the audit
format and deterministic redaction.

## Decision: Confirmation-required operations fail closed at the daemon; confirmation stays in CLI/WebUI

**Rationale**: Prompt channels are out of scope (FR-014). The existing approval
model is interactive operator confirmation recorded against the Manager-computed
canonical request (`docs/threat-model.md:94-96`); the CLI already confirms
interactively for sensitive actions (the `confirmExport`/TTY pattern used by 005).
The daemon does not prompt: an operation that requires confirmation is refused
unless the client (CLI/WebUI) supplies the confirmation out-of-band, and the daemon
never treats a missing prompt channel as approval (FR-015).

**Alternatives considered**: A daemon prompt/approval channel — rejected: explicitly
out of scope and a larger interaction surface. Auto-approving in daemon mode —
rejected: violates fail-closed.

## Decision: Gate 0 plus unit/integration tests; no real-Lima gate

**Rationale**: This is local control-plane machinery, not an isolation claim, so no
backend isolation gate applies (native is a weak but acceptable harness here). Gate
0 validates the daemon status/event schemas and the doc updates; `go test ./...`
covers lifecycle, socket placement/permission refusal, auth refusal + daemon-local
audit content, event ordering + redaction, embedded parity, restart fail-closed,
background status, and bounded buffering.

**Alternatives considered**: A real-Lima daemon gate — rejected: nothing
isolation-related is exercised; a native/loopback+socket harness proves the
control-plane behavior.
