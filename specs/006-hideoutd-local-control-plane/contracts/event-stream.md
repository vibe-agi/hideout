<!-- markdownlint-disable MD013 -->

# Contract: Live Event Stream

## Source Of Truth

- Events are a live fan-out from two sources: operation lifecycle (the daemon wraps
  each operation it executes and emits start/progress/complete/failed) and the audit
  tail (new records appended to each session `audit.jsonl`). There is NO durable
  daemon event log.
- Audit-derived events reuse the existing redacted read path (`readAuditEvents` applies
  `audit.RedactDetails`, `internal/manager/manager.go:488-497`), so they are
  control-plane-stripped by construction.

## Subscribe / Seed / Consume

- A subscriber authenticates like any request (operator token).
- On (re)connect, the client seeds current state with a single `overview` read and then
  consumes events; steady-state operation performs zero polling reads.
- A restart replays no historical events; the client re-seeds via `overview`.

## Event Shape

- Envelope validates against `schemas/daemon-event.schema.json`: `kind`
  (`operation` | `environment` | `audit` | `export` | `cleanup`), optional `phase`
  (`start` | `progress` | `complete` | `failed`), `seq` (per-subscriber order), and a
  redacted `payload`.
- `audit` payloads are `audit.Event` values already passed through `RedactDetails`.

## Redaction

- No event MAY carry Hideout-minted control-plane material (`HIDEOUT_SECRET_*` backing,
  `cap_`/`ui_` token values, control-plane detail field names, generated machine-id,
  or the operator token). Operator user/application data is verbatim on this local
  authenticated surface. Content leaving the machine still goes through the 005 export
  boundary.

## Credential Lifetime (mid-stream)

- A subscription is bound to the operator credential presented at subscribe time.
- When that credential expires, is revoked, or is rotated, the daemon MUST terminate
  the active subscription with a terminal event; the client MUST re-subscribe with a
  valid credential.
- A resubscribe attempt with the stale/expired credential MUST be refused and recorded
  in the daemon-local audit log (like any auth refusal), with no client-supplied token
  material.

## Backpressure

- Each subscriber has a bounded buffer. A slow or stuck consumer MUST be disconnected
  with an explicit terminal event rather than stalling operations or growing daemon
  memory without bound (FR-013).

## Shutdown

- On daemon stop, every subscriber receives a terminal event and clients fall back to
  their daemon-less behavior rather than hanging.
