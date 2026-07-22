# Contract: Establishment Protocol

<!-- markdownlint-disable MD013 -->

## Go Boundary

The daemon-owned lifecycle registrar used by Manager exposes an establishment
reservation in addition to the existing direct registration mechanics.

```go
type EstablishmentRequest struct {
    EnvironmentID string
    SessionID     string
}

type EstablishmentReservation interface {
    Prepare(context.Context, AttachRequest) (EnvironmentRef, error)
    Promote(context.Context) (Registration, error)
    Abort(context.Context, error) error
}

type Registrar interface {
    ReserveAttach(context.Context, EstablishmentRequest) (EstablishmentReservation, error)
    BeginAttach(context.Context, AttachRequest) (Registration, error)
}
```

Exact names may follow repository conventions, but the three-stage semantics
and authority boundary are normative.

## Admission

`ReserveAttach` MUST:

1. validate opaque environment and session identities;
2. observe caller cancellation before and throughout any wait;
3. wait for an already-running reconciliation without holding the coordinator
   mutex or the environment transition lock;
4. retry admission under the coordinator mutex so reconciliation cannot restart
   between the wait and reservation insertion;
5. refuse blocked, mutation, stop, duplicate-session, closing, or closed state;
6. cancel an idle deadline through the existing durable checkpoint path; and
7. insert only an in-memory reservation and emit redacted derived evidence.

Compatible reservations with different session IDs may coexist.

## Preparation

`Prepare` MUST:

1. accept only a request matching the reservation environment/session;
2. validate the backend observation and exact instance identity;
3. fail on unknown observation, blocked state, active stop/mutation/reconcile,
   or sibling-incarnation conflict;
4. select or join the existing `EnvironmentRef` under the coordinator mutex;
5. remain a reservation without installing an active handle or session
   resource; and
6. return only the typed incarnation, never a backend handle or credential.

Manager invokes preparation only while holding the existing environment
transition lock after reloading and validating the selected record.

## Promotion

Manager MUST create the existing durable owner record before calling
`Promote`. `Promote` MUST, within one coordinator critical section:

1. prove the reservation is still prepared and coordinator state remains
   admissible;
2. create the existing planned lifecycle run-session resource and normal
   registration from the prepared incarnation;
3. remove the reservation only after registration creation succeeds; and
4. emit a redacted promotion event whose derived status never observes neither
   a reservation nor a registration.

Promotion is single-use. Duplicate promotion is idempotent only when it can
return the same live registration safely; otherwise it fails closed.

## Abort

`Abort` MUST be idempotent and scoped to the caller's session. It removes only
that reservation and schedules ordinary idle handling only when no reservation
or active handle remains. It never removes runtime, owner, or provider state;
Manager owns those resources and performs their existing session-scoped
cleanup before or alongside abort.

Abort receives an error only for internal classification. Public evidence uses
a closed bounded reason and never serializes the raw error.

## Conflicting Operations

While any reservation is held for an environment:

- `BeginReconciliation` and direct `Reconcile` refuse before observation-driven
  cleanup;
- idle-stop expiry performs no stop transition;
- explicit stop and destructive mutation return their existing bounded
  activity error;
- lifecycle metadata cannot be forgotten; and
- another compatible session may reserve and establish independently.

If mutation or stop already owns the environment, a new reservation fails
before runtime publication.

## Manager Ordering

```text
select environment
allocate session identity (no filesystem publication)
reserve attach (may wait for older reconciliation)
acquire environment transition lock
reload and validate environment record/workspace/runtime identity
observe backend and prepare reservation/incarnation
materialize session layout and environment runtime
capture/apply shared workspace plan from prepared incarnation
write durable run-session owner
install owner/session cleanup
promote reservation to normal lifecycle registration
plan and commit provider resources
continue existing target startup
```

No code may wait for reconciliation while it holds the transition lock.

## Compatibility

- `BeginAttach` may remain as a coordinator-internal/test convenience using the
  same validation and promotion helpers, but executable reusable Lima runs use
  the reservation protocol.
- Explain-only and inactive/native mechanics retain their non-executable or
  weak-harness semantics and do not silently emulate the reservation claim.
- No CLI, profile, manifest, journal, owner, or backend schema is added.
