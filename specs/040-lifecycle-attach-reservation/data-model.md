# Data Model: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

## Establishment Reservation

Daemon-local coordination scoped to one environment and one allocated session
identity.

| Field | Type | Rules |
| ------- | ------ | ------- |
| `environmentID` | opaque environment ID | Required; existing lifecycle ID validation |
| `sessionID` | opaque session ID | Required; unique within held reservations and active registrations |
| `phase` | enum | `reserved`, `prepared`, `promoted`, or `aborted` |
| `incarnation` | optional `EnvironmentRef` | Set only by successful preparation from a fresh backend observation |
| `createdAt` | daemon-local time | Used only for bounded diagnostics; not persisted or exported verbatim |

The coordinator stores held reservations in
`registryEnvironment.establishing[sessionID]`. Object-local synchronization
makes `Prepare`, `Promote`, and `Abort` idempotent and prevents a caller from
promoting after abort.

### Reservation Invariants

1. A held or prepared reservation has no normal lifecycle handle for the same
   session.
2. A promoted reservation is removed in the same coordinator critical section
   that installs the normal lifecycle handle and planned run-session resource.
3. A reservation never survives coordinator process loss.
4. Reconciliation, explicit/idle stop, forget, and destructive mutation do not
   begin while the environment has any held reservation.
5. Distinct reservations cannot abort, promote, or clean one another.

## Allocated Session Layout

An opaque, side-effect-free set of paths derived from a freshly generated
session ID and the Manager store root.

### Layout Invariants

- Allocation creates no global session directory and no environment runtime
  child.
- Explicit preparation creates the existing global session layout exactly
  once.
- Environment runtime preparation occurs only after reservation and current
  environment/backend validation.
- Cleanup is scoped to the allocated session ID.

## Prepared Establishment

A held reservation plus a validated `AttachRequest` and exact
`EnvironmentRef`.

### Validation

- Environment, instance, and session identities are valid and match the
  reservation.
- Backend observation is schema-valid, belongs to the selected instance, and
  is not unknown.
- Coordinator state is not blocked and has no active stop/mutation/reconcile.
- The observation matches the current active incarnation when sibling handles
  exist; otherwise the existing incarnation-selection rules apply.

Preparation supplies incarnation metadata for shared-workspace planning but
does not add an active handle or a run-session resource.

## Provisional Session Runtime

The existing environment runtime child
`environments/<environment>/runtime/sessions/<session>` plus global session
materialization. It is created only in `prepared` phase while the reservation
excludes reconciliation and destructive mutation.

It is not durable session authority. A crash before owner creation leaves an
ownerless runtime that replacement-daemon reconciliation may prove and scrub.

## Durable Session Owner

The existing `runsession.OwnerRecord` and OS-backed owner lock. No schema field
is added for reservations.

The owner must exist before promotion. A crash after owner creation is handled
by existing owner liveness and recovery rules; replacement reconciliation must
not reconstruct the lost reservation.

## Normal Lifecycle Registration

The existing registration, session resource, and provider resource graph. It
is created by promotion using the prepared incarnation. Provider planning and
commit rules remain unchanged.

## State Transitions

```text
intent
  -> allocated
  -> waiting-for-reconciliation
  -> reserved
  -> prepared
  -> runtime-published
  -> owner-durable
  -> promoted/normal-registration
  -> running

allocated|waiting|reserved|prepared|runtime-published|owner-durable
  -> aborted

reserved|prepared|runtime-published|owner-durable
  -> daemon-crash
  -> reservation-lost
  -> replacement reconciliation from durable facts only
```

### Abort Outcomes

| Boundary | Owned cleanup |
| ---------- | --------------- |
| Before reservation | No filesystem or coordinator state exists |
| Waiting for reconciliation | Context cancellation only; no reservation |
| Reserved before layout preparation | Remove reservation only |
| Prepared before runtime | Remove reservation only |
| Runtime published before owner | Clear that session's runtime/global ephemeral layout, then abort |
| Owner durable before promotion | Close that owner and clear that session's runtime/layout, then abort |
| After promotion | Normal registration/owner/session cleanup path |

## Derived Status

Lifecycle status derives `establishingSessions` from the in-memory map and may
report activity `establishing`. The count is not journaled and returns to zero
after process restart. Event payloads contain only environment identity,
generation, event kind, bounded reason code, time, and the already-redacted
derived status.
