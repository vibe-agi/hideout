# Contract: Observability And Evidence

<!-- markdownlint-disable MD013 -->

## Lifecycle Status

The existing lifecycle status may add:

```json
{
  "activity": "establishing",
  "establishingSessions": 1,
  "reasonCode": "attach-establishment-in-progress"
}
```

Rules:

- `establishingSessions` is a non-negative derived count and is omitted at zero.
- `activity=establishing` is used only when at least one reservation exists and
  no higher-priority unknown/stop condition must be shown.
- No session ID is exposed by this field.
- After daemon restart the count is zero; any residue is represented through
  existing owner/orphan/reconciliation status.

## Event Kinds

The lifecycle producer may emit the following bounded kinds:

| Kind | Meaning | Public reason |
| ------ | --------- | --------------- |
| `attach-establishment-waiting` | An earlier reconcile owns the fence | `reconciliation-pending` |
| `attach-establishment-waiting` | An invoked automatic stop owns the fence | `automatic-stop-pending` |
| `attach-establishment-reserved` | Reservation inserted | `attach-establishment-in-progress` |
| `attach-establishment-prepared` | Backend incarnation validated | `attach-establishment-in-progress` |
| `attach-establishment-promoted` | Registration replaced reservation | empty or `owner-established` |
| `attach-establishment-aborted` | Reservation released without promotion | one closed abort reason |

Events use the existing environment entity and derived lifecycle status. They
do not add a route or client action.

## Stable Failure Reasons

Public errors/status select from existing lifecycle errors plus bounded
establishment reasons:

- `reconciliation-pending`
- `automatic-stop-pending`
- `destructive-mutation-in-progress`
- `stop-in-progress`
- `backend-observation-unproved`
- `backend-incarnation-changed`
- `environment-record-changed`
- `owner-establishment-failed`
- `attach-establishment-cancelled`
- `attach-establishment-timeout`
- `attach-establishment-aborted`

Raw provider/backend errors may be retained only in existing host-local audit
locations that already permit them. Status, events, doctor, exported evidence,
and operator recovery summaries use bounded classifications.

## Redaction

Tests inject each of the following into cancellation and failure inputs:

- Manager store/control-plane paths and transition-lock filenames;
- broker/UI tokens and backend credentials;
- decimal and string-form process identifiers;
- raw target arguments and callback values; and
- hidden environment/session runtime directories.

None may occur in lifecycle status JSON, daemon event JSON, doctor human/JSON
output, or product evidence artifacts. Opaque environment IDs remain allowed;
reservation session IDs are intentionally omitted from establishment evidence.

## Gate 0 Evidence

Gate 0 must retain registered proof entries for:

- deterministic forced interleavings and cancellation boundaries;
- at least 1,000 seeded randomized schedules under the race detector;
- TLC verification of `AttachReservation.tla` invariants;
- status/event schema parity and redaction negative fixtures; and
- Manager ordering/runtime/owner cleanup integration.

Every new assertion has a recorded mutation proof. Every new lifecycle judge or
gate assertion has a negative fixture that is observed failing.

## Real Lima Evidence

Real macOS arm64 Lima evidence binds:

- full clean source commit and dirty state;
- package/runtime artifact digests and host/guest identity;
- exact environment, instance, boot incarnation, and session evidence;
- reconciliation-first and reservation-first outcomes;
- cancellation during establishment;
- daemon restart before and after durable owner creation;
- clean evidence provenance and artifact digests; and
- at least 30 warm first-output samples with nearest-rank p95 at most 2.0
  seconds and at least 95% of samples at or below 2.0 seconds.

Native/local-fast output, edited summaries, missing artifacts, dirty or partial
identity, fewer samples, and commands without backend observation do not satisfy
the real claim.
