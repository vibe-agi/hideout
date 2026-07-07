<!-- markdownlint-disable MD013 -->

# Implementation Plan: hideoutd Local Control-Plane Daemon

**Branch**: `006-hideoutd-local-control-plane` | **Date**: 2026-07-07 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-hideoutd-local-control-plane/spec.md`

## Summary

Productize the `hideoutd` per-user local control-plane daemon whose trust shape is
already ratified in `docs/threat-model.md:78-97`. The daemon serves the existing
typed Manager API by mounting `manager.API.Handler()` verbatim over a store-rooted
Unix socket, so behavior parity with embedded mode holds by construction — the
same 32 routes (16 POST at `internal/manager/api.go:186-216`; 16 GET = the
`serveGetResource` switch `:971-997` plus the two special-cased GET resources
`audit/events` `:154` and `run/status` `:163`), the same `authorize` (Bearer /
`X-Hideout-UI-Token`, constant-time, TTL) and `checkHost`/`checkOrigin` guards. The
daemon's own lifecycle/status/event endpoints are a separate surface outside
`/api/v1/…` (not the parity-locked Manager subrouter). The Unix socket lives under a private runtime
subdirectory of the store (reusing the store-reserved and workspace-safety
guards), which structurally excludes real backend guests (Lima); for a weak
native target sharing the operator UID, the operator token is the sole defense.
Loopback stays available only as the short-lived tokened UI transport
(`manager.StartLocalServer`). A daemon-owned live event fan-out publishes
operation lifecycle plus a tail of session `audit.jsonl` (reusing
`readAuditEvents` + `RedactDetails`) with no durable log; clients seed with one
overview read then consume events. Background typed operations run in daemon
goroutines with queryable status; v1 background work is scoped to the existing typed
env stop/clean apply ops (`Core.ApplyEnvironmentStop`/`ApplyEnvironmentClean`,
`internal/manager/environment_lifecycle.go:81,109`) — the genuine long-running typed
operations; `run/status` is a read (served, not background work) and session cleanup
is CLI-direct today (`session.CleanupEphemeral`, `internal/app/app.go:3679`), not a
typed route, so it is out of v1 background scope. After a restart the daemon holds
only in-memory ownership since its current start, so any pre-existing live resource
is unprovable and is reported and audited as an orphan — never silently re-adopted
and never destroyed. Because `authorize` returns 401 without auditing
(`api.go:133,846`), the daemon wraps the mounted handler with an auth-refusal
recorder that logs 401s to a daemon-local, session-unbound audit log without
altering responses or reading token material; lifecycle events land there too. With
no daemon running, every surface behaves exactly as today.

## Technical Context

**Language/Version**: Go 1.25.0 plus the existing CLI and Manager control plane.

**Primary Dependencies**: Existing packages — `internal/manager`
(`API`/`API.Handler()`/`authorize`/`checkHost`/`checkOrigin`, the typed route set;
`Core` and its typed ops; `LocalServer`/`StartLocalServer` for the loopback UI
transport; `AuditEvents`/`readAuditEvents` + `audit.RedactDetails` for the audit
tail), `internal/session` (`New`/`ValidID`/`CleanupEphemeral`, store runtime
layout), `internal/audit` (`Event`/`NewFile`/`RedactDetails` for the daemon-local
audit log), `internal/profile` (`Store` runtime paths and the store-reserved
guard). New: `internal/daemon` (lifecycle, Unix-socket transport, operator-token
mint/persist, event fan-out, background registry, restart fail-closed) and a
`hideout daemon start|status|stop` CLI subcommand dispatched in
`internal/app/app.go` (`cmd/hideout/main.go` remains the thin wrapper). No new
backend capability and no new redaction engine.

**Storage**: No new persistent store and no durable event log. A runtime
subdirectory under the store holds ephemeral control files recreated per start (the
Unix socket, the operator-token file `0600`, the single-instance lock) and one
persistent evidence file: the daemon-local audit log (`daemon-audit.jsonl`), which
is append-only and SURVIVES stop/restart because auth refusals, lifecycle, and
restart-orphan records are evidence, not per-instance diagnostics. Session audit
remains the existing per-session `audit.jsonl`; the event stream stays non-durable.

**Testing**: `go test ./...` (single-instance lifecycle, socket placement/permission
guard, auth refusal + daemon-local audit content, event fan-out ordering +
redaction, embedded-parity, restart fail-closed orphan handling, background
status, bounded per-subscriber buffering, confirmation-required fail-closed) and
`scripts/test-gate0.sh` (daemon status/event schemas, docs). No real-Lima gate —
this is local control-plane machinery, not an isolation claim; native is an
acceptable harness.

**Target Platform**: macOS/Linux host CLI plus the Manager control plane.

**Project Type**: CLI plus Go Manager control plane (single project).

**Performance Goals**: Event fan-out is in-process and bounded per subscriber; no
new steady-state cost when idle and no durable I/O per event.

**Constraints**: Exactly one daemon per store (lock + stale-endpoint detection).
Primary transport is a store-runtime Unix socket with private ancestors, validated
by the existing store-reserved and workspace-safety guards; placement failure means
refuse to start. Loopback is only the short-lived tokened UI transport; no
unauthenticated local API; no non-local bind. Operator token only in v1
(read-only deferred). No durable event log; restart replays nothing and fails
closed (report + audit, not re-adopt, not destroy) for unprovable live resources.
The daemon never prompts — confirmation-required operations fail closed unless the
CLI/WebUI flow supplied confirmation. Streamed events and the daemon-local audit
follow the existing local redaction rules.

**Scale/Scope**: Single-operator prosumer. One daemon lifecycle, one transport +
token, one event fan-out over existing operations + audit tail, background wiring
for existing typed cleanup/status, plus the doc/gate updates.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: PASS. Fails closed on socket placement/permission failure,
  on unauthenticated/invalid-token requests, on confirmation-required ops without
  external confirmation, and after restart for unprovable live resources. No new
  target-facing authority, host execution, raw profile writes, or guest-reachable
  network exposure (FR-002/004/011/015).
- **Typed Authority**: PASS. The daemon adds no operation class: it mounts the
  existing `API.Handler()` and runs existing typed Core ops. It is a transport +
  lifecycle + event wrapper; Go still owns every validator and capability. CLI,
  TUI, WebUI remain intent-describing clients.
- **Workspace And Policy**: PASS. No workspace, HostFS, mount, env-policy, proxy,
  or profile-authority change. The socket placement reuses the existing
  store-reserved and workspace-safety guards rather than a new mechanism.
- **Generality And Provider Scope**: PASS. A generic local control plane over the
  existing Manager model; no backend/agent/transport-vendor specifics become Core
  semantics.
- **Evidence And Redaction**: PASS. Daemon lifecycle and auth refusals are audited
  in a daemon-local, session-unbound log; the event stream is a local authenticated
  evidence surface under the existing deterministic redaction (control-plane
  stripped, operator data verbatim locally). The operator token is a Hideout-minted
  control-plane secret under the redaction contract; no event or log carries token
  material. Anything leaving the machine still goes through the 005 export boundary.
- **Backend And Distribution**: PASS. No backend capability and no real-Lima
  requirement (no isolation claim). Constitution Principle V applies directly: the
  daemon lifecycle, first-run runtime-dir/token setup, single-instance ownership,
  and ordered teardown are Core lifecycle behavior, designed as product behavior
  here. No new helper binary beyond the daemon entrypoint.
- **Gates**: Gate 0 for daemon status/event schemas and docs; `go test ./...` for
  lifecycle, placement, auth, event redaction, parity, restart fail-closed,
  background status, and bounded buffering.
- **Status And Docs**: `docs/STATUS.md` (`hideoutd` design-ready → implemented;
  TUI/WebUI rows gain daemon-backed live refresh), `docs/tui-webui-experience.md`
  (daemon-first steady state becomes current for the existing panels),
  `docs/threat-model.md` (daemon contract wording from "when enabled" to shipped),
  `docs/manager-control-plane.md` (daemon serving + event surface),
  `docs/privacy-run-test-plan.md` (daemon lifecycle/auth/stream gates).

**Pre-design result**: PASS. No constitution violation or complexity exception.

## Project Structure

### Documentation (this feature)

```text
specs/006-hideoutd-local-control-plane/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── daemon-transport-auth.md
│   ├── api-parity-matrix.md
│   └── event-stream.md
└── tasks.md        # generated by /speckit-tasks after this plan
```

### Source Code (repository root)

```text
internal/
├── daemon/             # NEW: daemon lifecycle (single-instance lock, Unix-socket
│                       # listen under store runtime, operator-token mint/persist,
│                       # ordered shutdown, restart fail-closed orphan handling);
│                       # mounts manager.API.Handler() as the parity-locked Manager
│                       # subrouter behind an auth-refusal recorder (logs 401s to the
│                       # daemon audit log without altering responses); serves its own
│                       # status + event-subscribe endpoints (separate surface); the
│                       # live event fan-out (bounded per subscriber); the persistent
│                       # daemon-local audit log; and the background-operation registry
│                       # (v1: env stop/clean apply only).
├── manager/            # add a minimal optional event-observer seam on Core (nil
│                       # by default → embedded behavior unchanged) so daemon-run
│                       # operations emit lifecycle events; reuse API.Handler(),
│                       # authorize, AuditEvents/readAuditEvents+RedactDetails
├── session/            # store runtime layout + ValidID for ownership checks
│                       # (unchanged mechanism)
└── audit/              # Event/NewFile/RedactDetails reused for the daemon audit log

internal/app/            # NEW: `hideout daemon start|status|stop` dispatch (app.go)
cmd/
└── hideout/            # main.go remains the thin wrapper (no daemon logic here)

schemas/
├── daemon-status.schema.json   # NEW: status/inventory shape
└── daemon-event.schema.json    # NEW: event envelope shape

scripts/
├── test-gate0.sh               # add daemon schema validation
└── test-daemon-smoke.sh        # NEW (optional): start/auth-refusal/event/stop smoke

docs/
├── STATUS.md
├── tui-webui-experience.md
├── threat-model.md
├── manager-control-plane.md
└── privacy-run-test-plan.md
```

**Structure Decision**: A new `internal/daemon` package owns lifecycle, transport,
event fan-out, and background execution, and reuses `manager.API.Handler()`
verbatim so FR-005 parity is structural rather than reimplemented. Manager Core
gains only a minimal optional event-observer seam that is nil in embedded mode, so
FR-006 (zero daemon-less regression) holds by construction. The audit tail and the
daemon-local audit log reuse `internal/audit` + the existing redacted read path;
no new redaction logic is introduced.

## Phase 0: Research

See [research.md](research.md).

## Phase 1: Design

See [data-model.md](data-model.md),
[contracts/daemon-transport-auth.md](contracts/daemon-transport-auth.md),
[contracts/api-parity-matrix.md](contracts/api-parity-matrix.md),
[contracts/event-stream.md](contracts/event-stream.md), and
[quickstart.md](quickstart.md).

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. Every failure mode fails closed; no new target
  authority.
- **Typed Authority**: PASS. Reused handler + existing typed ops; no new op class.
- **Workspace And Policy**: PASS. Existing guards reused; nothing broadened.
- **Generality And Provider Scope**: PASS. Generic control plane.
- **Evidence And Redaction**: PASS. Daemon-local audit + redacted event stream;
  token under the redaction contract.
- **Backend And Distribution**: PASS. Principle V lifecycle; native ok; no
  real-Lima; no new helper beyond the daemon entrypoint.
- **Gates**: PASS. Quickstart maps each requirement to unit or Gate 0 evidence.
- **Status And Docs**: PASS. Doc updates enumerated and carried to tasks.

## Complexity Tracking

No constitution violations. The one new package (`internal/daemon`) is justified:
the daemon is a genuinely new runtime lifecycle (persistent process, Unix-socket
ownership, event fan-out, background execution, restart fail-closed) that must not
be entangled with the request-scoped `manager` server. It deliberately reuses the
existing API handler and audit read path rather than reimplementing them, and the
only change inside `manager` is an optional nil-default observer seam so embedded
mode is provably unchanged.
