# Contract: Disposable Disposal Protocol

<!-- markdownlint-disable MD013 -->

## Authority

The sole automatic authorization is a validated supported environment record
with `Disposable=true`. A lifecycle disposal intent is evidence that this
authorization was validated earlier; it cannot be created from a name, status,
backend inventory row, or generic orphan report.

Manager Core owns all destructive callbacks. The lifecycle coordinator owns
admission, durable intent, ordering, and metadata status only.

## Admission

Before intent creation:

1. current daemon singleton ownership is established;
2. lifecycle reconciliation for the exact environment is terminal;
3. no attach establishment, session handle, stop attempt, mutation, or disposal
   is active;
4. the environment transition boundary is acquired;
5. the record is reloaded, supported, disposable, and exact-instance-bound;
6. owner inventory contains no live or unprovable owner;
7. an existing intent is absent or matches the same canonical record digest,
   backend, instance, and generation.

Failure before all admission checks performs no backend cleanup.

## Execution

1. Persist `planned` intent and a cleanup-required record status.
2. Observe the exact instance.
3. If running or stopped, invoke existing typed backend cleanup. If absent,
   do not start or recreate it. If unknown/mismatched, block.
4. Observe the exact instance `Absent` twice consecutively within a bound.
5. Persist `backend-absent`.
6. Close the exact environment gateway and clear exact environment runtime,
   owner, activation, and service state.
7. Persist `metadata-cleaning`.
8. Remove lifecycle journal and in-memory coordinator identity.
9. Remove the environment record/directory last.
10. Emit removed outcome and retain audit.

## Failure And Resume

- Before stable absence: retain record and intent as `blocked`; no metadata is
  represented as removed.
- After stable absence but before journal removal: retain intent at
  `backend-absent` or `blocked`; retry metadata cleanup without backend start.
- After journal removal but before record removal: retain the disposable record
  in cleanup-required state; next recovery creates a new matching intent and
  re-proves absence.
- After record loss with a valid intent: only already-absent exact-instance
  metadata completion is allowed. Running/unknown state blocks.
- Context cancellation returns interrupted and leaves the last durable state.

## Idempotence

Repeating any admitted step with the same bound identity produces the same or a
later state. A retry cannot change backend, instance, digest, generation, or
authority. Absence and missing owned runtime are success inputs; foreign or
unsafe filesystem entries are errors.

## Non-Claims

- no automatic cleanup of reusable environments;
- no bulk cleanup of unmatched Lima instances;
- no adoption of a prior daemon's live session;
- no inference from `rm-*` naming;
- no generic repair of historical journal-only residue;
- no new target authority or host capability.
