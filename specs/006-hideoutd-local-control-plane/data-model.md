<!-- markdownlint-disable MD013 -->

# Data Model: hideoutd Local Control-Plane Daemon

## Daemon Instance (runtime state)

The single per-store control-plane process; not a persisted record beyond its
runtime footprint, which is recreated per start.

- `storeRoot`: the effective Hideout store this daemon serves (one daemon per store).
- `runtimeDir`: a private subdirectory of the store holding the ephemeral control
  files (socket, token file, lock) plus the persistent daemon-local audit log;
  ancestors must be operator-private.
- `startedAt`: the current instance's start time; the boundary for provable ownership.
- `state`: `starting` | `serving` | `stopping` (no persisted history).

Rules:

- Exactly one instance per `storeRoot`; a second start reports the existing
  instance rather than racing (single-instance lock + socket liveness probe).
- The ephemeral control files (socket, token, lock) are recreated per start; the
  daemon-local audit log is append-only and survives stop/restart as evidence.
  There is no durable event log and no persisted operation/ownership state.

## Local Transport

The endpoints the daemon serves on.

- `primarySocket`: a Unix socket at `runtimeDir/hideoutd.sock`, placement validated
  by the existing store-reserved and workspace-safety guards; structurally
  guest-unreachable for real backends, token-protected for a weak native target.
- `loopbackUI`: the optional short-lived tokened loopback transport reused from
  `manager.StartLocalServer` for browser UI only.

Rules:

- If placement or permission preconditions fail (non-private ancestor, guest-visible
  path), the daemon refuses to start.
- No unauthenticated API on any transport; no non-local bind.

## Operator Credential

- `operatorToken`: minted once per start, persisted `0600` at `runtimeDir/token`;
  required on every request via the reused `authorize` (Bearer / `X-Hideout-UI-Token`).
- A Hideout-minted control-plane secret under the deterministic redaction contract;
  never appears in events, audit, or logs.
- `readOnlyToken`: designed but out of v1.

Rules:

- OS peer identity alone is never sufficient; the token gates every request.

## Served API Surface (parity, by construction)

The daemon mounts `manager.API.Handler()`; the served route set is the current
typed surface, enumerated in [contracts/api-parity-matrix.md](contracts/api-parity-matrix.md):
16 POST plan/apply routes and 16 GET read routes (14 via the `overviewResource`
switch plus the two special-cased GET resources `audit/events` and `run/status`) —
32 routes total. The daemon's own status and event-subscription endpoints are a
separate surface outside `/api/v1/…`, inventoried in the parity matrix.

Rules:

- No new Manager operation class, raw profile write, or host execution is added; the
  daemon-specific status/event endpoints live outside the parity-locked subrouter.
- Confirmation-required operations fail closed unless CLI/WebUI supplied confirmation
  (the daemon never prompts).

## Event (live, non-durable)

An item in the per-subscriber live stream. Not persisted.

- `kind`: `operation` | `environment` | `audit` | `export` | `cleanup`.
- `phase` (operation/background): `start` | `progress` | `complete` | `failed`.
- `payload`: the redacted event body — for `audit`, an `audit.Event` already passed
  through `RedactDetails`; for others, a summary of the operation/resource.
- `seq`: per-subscriber ordering position.

Rules:

- Control-plane material never appears; operator user/application data is verbatim on
  this local authenticated surface (existing local redaction rules).
- Derived from operation lifecycle hooks plus the audit tail; no durable event log.
- A (re)connecting client seeds via one `overview` read, then consumes events;
  restart replays nothing.
- Per-subscriber buffering is bounded; a slow consumer is disconnected with a
  terminal event.

## Background Operation

An existing typed Manager operation submitted to the daemon for background
execution.

- `id`: daemon-assigned handle for status queries.
- `op`: the underlying existing typed operation — v1 is environment stop/clean apply
  (`Core.ApplyEnvironmentStop`/`ApplyEnvironmentClean`) with the same plan/apply
  semantics as foreground. Session cleanup (CLI-direct `session.CleanupEphemeral`,
  not a Manager route) and `run/status` (a read) are out of v1 background scope.
- `status`: `queued` | `running` | `completed` | `failed`, queryable until terminal.
- `ownedSince`: bound to the current daemon instance; not persisted across restart.

Rules:

- Runs under the same validators/results as foreground; adds no new authority.
- On daemon stop, finishes or fails closed with a recorded terminal status; nothing
  continues headless.

## Live Resource Record (ownership; in-memory only)

The daemon's knowledge of resources it started under an active session during the
current instance.

- `resource`: the live resource (running environment, background op, bridge).
- `sessionId`: the owning session (`session.ValidID`), tracked in-memory since
  `startedAt`.

Rules:

- After restart, any live resource without an in-memory ownership record from the
  current instance is unprovable → reported and audited as an orphan, never silently
  re-adopted and never destroyed.

## Daemon Audit Log (session-unbound)

- `path`: `runtimeDir/daemon-audit.jsonl`, written via `audit.NewFile` with
  `RedactDetails` at emit; bound to no profile or session; append-only and persistent
  across stop/restart (evidence, not per-instance diagnostics).
- `records`: lifecycle (start/stop/restart-orphan) and authentication events
  (unauthenticated/invalid-token refusals with channel and reason).
- `mechanism`: the reused Manager `authorize` refuses with a 401 and does not audit,
  so auth refusals are captured by a daemon wrapper around the served handler that
  observes the refusal outcome and writes the record — without altering the response
  or reading token material.

Rules:

- Never records client-supplied token material (only pass/fail + reason).
- Same deterministic redaction as session audit; control-plane material absent.
