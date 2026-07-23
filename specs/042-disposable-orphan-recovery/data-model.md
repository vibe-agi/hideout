# Data Model: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

## Disposable Identity

A canonical, immutable projection of an environment record used only to bind a
disposal intent:

- `environmentId`: exact supported record ID
- `recordVersion`: supported environment record version
- `backend`: exact backend name
- `mode`: dedicated disposable mode
- `profile`: profile identity used to derive the machine
- `machineIdentityId`: stable machine identity
- `instanceName`: exact backend instance
- `disposable`: must be true
- `createdAt`: original record creation identity
- `digest`: lowercase SHA-256 over a versioned canonical encoding of the fields

Mutable run status, last-command, last-session, and timestamps other than
creation are excluded so cleanup retries do not invalidate their own intent.
Changing any immutable projection field produces a different digest and blocks
reuse of the old intent.

## Disposal Intent

An optional strict child of one lifecycle journal:

- `schema`: `hideout.disposal-intent/v1`
- `authority`: `run-rm`
- `backend`
- `instanceName`
- `recordDigest`
- `generation`: matches the journal start generation
- `state`
- `requestedAt`
- `updatedAt`
- `reasonCode`: optional bounded stable code

Allowed states:

```text
planned -> backend-absent -> metadata-cleaning -> removed
   |             |                 |
   +----------> blocked <----------+
```

`removed` is emitted/audited but not stored: successful removal deletes the
journal. `blocked` may transition back to `planned` only after a new daemon
revalidates the record/intent/proof set. State never changes the bound identity.

Validation rules:

- journal environment ID is the intent environment identity;
- schema and authority use the closed v1 values;
- backend and instance are non-empty bounded identifiers;
- digest is exactly 64 lowercase hexadecimal characters;
- generation equals the journal start generation and is non-zero;
- timestamps are UTC, non-zero, and ordered;
- reason is present only for `blocked` and uses the existing bounded reason
  grammar;
- no unknown fields are accepted.

## Disposal Request

An in-memory Manager-to-coordinator request:

- validated disposable identity projection and digest;
- current environment ID/backend/instance;
- expected lifecycle generation, when already established;
- source: ordinary finalizer or restart recovery;
- caller context/cancellation.

It carries no backend handle, owner path, token, target arguments, workspace
content, or deletion callback in durable state.

## Recovery Proof Set

An in-memory closed set of authoritative facts:

- current daemon owns the per-store singleton;
- exact environment transition boundary acquired;
- record loaded after the boundary and digest matches intent;
- owner inventory available;
- live owner count is zero;
- unprovable owner count is zero;
- backend observation validates and matches the exact instance;
- typed cleanup invocation completed or instance was already absent;
- consecutive absence observations equal two;
- gateway/runtime cleanup completed;
- journal removal completed;
- record removal completed.

Any missing or contradictory proof produces a bounded blocked reason. Counts
and booleans may enter evidence; raw owner/session identifiers and paths may not.

## Recovery Outcome

- `environmentId`
- `source`: `ordinary-finalizer` or `restart-recovery`
- `status`: `removed`, `cleanup-required`, or `interrupted`
- `lastPhase`: planned, backend-absent, metadata-cleaning, or blocked
- `reasonCode`: optional stable bounded code
- `backendCleanupInvoked`: boolean
- `absenceObservations`: bounded count
- `recordRemoved`: boolean
- `journalRemoved`: boolean
- `runtimeRemoved`: boolean
- `completedAt`

## State And Crash Matrix

| Durable record | Durable intent | Backend | Meaning after restart | Allowed action |
| --- | --- | --- | --- | --- |
| non-disposable | any | any | unauthorized | report/block only |
| disposable | none | known exact | pre-intent or record-last residue | revalidate and begin a new intent |
| disposable | valid | running | authorized interrupted cleanup | owner-check, typed delete, prove absence |
| disposable | valid | absent | authorized metadata completion | prove stable absence, clear metadata |
| disposable | invalid/mismatch | any | unproved | retain and block |
| absent | valid | absent | authorized record-loss residue | prove absence, remove journal only |
| absent | valid | running/unknown | insufficient authority facts | retain and block |
| absent | none/legacy journal | any | historical untrusted residue | retain and explicit recovery |
| absent | absent | absent | complete | no action |

## Concurrency Rules

- One disposal mutation per environment.
- Active/establishing lifecycle handles block disposal.
- Disposal blocks new attach, stop, clean, reconcile mutation, and idle stop for
  the same environment.
- Unrelated environments may recover concurrently within the worker bound.
- Cancellation never clears durable intent or record evidence unless the exact
  cleanup step completed and was persisted.
