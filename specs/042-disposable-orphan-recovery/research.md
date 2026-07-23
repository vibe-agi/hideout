# Research: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

## Decision 1: Durable Authorization Lives In The Lifecycle Journal

**Decision**: Add an optional strict disposal intent to the existing lifecycle
journal. The coordinator may create it only from a locked, validated
`Disposable=true` record, and it binds the environment ID, backend, exact
instance, canonical record digest, lifecycle generation, authority kind, state,
timestamps, and bounded reason code.

**Rationale**: The lifecycle journal is already the daemon-owned single-writer
store for crash recovery and destructive-mutation serialization. A durable
intent is needed before deletion starts so a later daemon can distinguish
authorized disposal from a generic orphan. Binding a canonical record digest
prevents a changed or reused record from inheriting prior authorization.

**Alternatives considered**:

- **Record flag only**: sufficient while the record exists, but cannot classify
  a crash after record loss and cannot show which exact instance was authorized.
- **A new GC ledger**: duplicates lifecycle identity, ordering, validation, and
  recovery state and creates cross-ledger convergence problems.
- **Infer from `rm-*` names or error status**: not authorization and unsafe.

## Decision 2: Use Record-Last Metadata Ordering

**Decision**: Persist intent first, mark the record cleanup-required, prove
backend absence, clear environment-scoped runtime, remove lifecycle
journal/coordinator state, and remove the environment record last.

**Rationale**: Existing ordering can remove the record and crash before journal
removal, producing an unclassifiable journal-only identity. Record-last ordering
ensures every new crash residue has either both record and intent or a retained
disposable record without a journal. Both can be revalidated and retried.

**Alternatives considered**:

- **Record then journal**: recreates the measured defect.
- **Best-effort parallel removal**: has no durable ordering and cannot support
  deterministic recovery.
- **Atomic rename of both stores**: the stores have different directories and
  readers; a multi-file rename does not provide a complete transaction.

## Decision 3: Coordinator Serializes; Manager Executes Authority

**Decision**: Add a narrow disposal coordinator contract. It validates/persists
intent and serializes against attach, stop, clean, idle expiry, and mutation,
then invokes Manager-owned cleanup callbacks. Manager owns environment locks,
owner checks, backend cleanup, stable-absence proof, gateway/runtime cleanup,
record updates, and final record removal.

**Rationale**: This preserves the constitution and current architecture:
lifecycle metadata never receives a backend handle or deletion authority, while
Manager cannot bypass durable coordinator state on the daemon product path.

**Alternatives considered**:

- **Coordinator calls the backend directly**: moves capability authority into
  the journal state machine.
- **Manager deletes the journal directly**: bypasses coordinator admission and
  in-memory status consistency.
- **Reuse only generic `environment clean`**: its explicit operator contract
  does not persist disposable intent before destructive work and cannot resume
  every crash cut.

## Decision 4: Closed Proof Set Before And After Destruction

**Decision**: Before backend cleanup, require current daemon singleton
ownership, exact environment transition serialization, supported record and
intent identity, and no live/unprovable session owner. After cleanup, require
two consecutive exact-instance `Absent` observations before authority metadata
can be removed.

**Rationale**: The daemon lock proves the old daemon is gone, not that guest
processes or the VM are gone. A stale owner is admissible because its lock is
proved released; a live or unreadable owner blocks. Backend return codes can be
ambiguous, so stable observed absence is the destructive completion proof.

**Alternatives considered**:

- **Trust backend cleanup return**: can false-green after partial/ambiguous
  deletion.
- **One absence sample**: transient inventory can race with state changes.
- **Destroy whenever no daemon owns the resource**: turns restart orphan
  reporting into unauthorized generic GC.

## Decision 5: Startup Recovery Is Bounded And Non-Blocking

**Decision**: Start authenticated daemon/status surfaces first, then launch a
bounded worker pool over validated disposable candidates. Each candidate waits
for its lifecycle reconciliation and uses the existing cancellation context.
Shutdown cancels unfinished recovery and leaves durable intent/record state for
the next daemon.

**Rationale**: Recovery may take tens of seconds per Lima delete/observation.
Blocking daemon startup would make status and explicit recovery unavailable
precisely when cleanup is slow or broken. Per-environment serialization allows
unrelated candidates and normal environments to progress.

**Alternatives considered**:

- **Synchronous startup GC**: makes one stuck backend block all operator access.
- **Unbounded goroutine per record**: risks backend/process pressure and weakens
  shutdown bounds.
- **Periodic forever retry**: can create destructive retry storms; v1 performs
  one bounded startup pass plus existing explicit clean recovery.

## Decision 6: Historical Untrusted Journal Residue Remains Blocked

**Decision**: A journal with no record and no valid 042 disposal intent remains
`environment-record-unavailable`. It is not retroactively inferred disposable.
A record-less journal with a valid intent may be removed only after exact
instance stable absence and intent validation; it may not invoke cleanup that
requires unavailable authority facts.

**Rationale**: Existing journal-only residue predates the new authorization
contract. Deleting or using it as authority would turn a migration heuristic
into a destructive capability. New protocol ordering prevents creating more of
this untrusted shape.

**Alternatives considered**:

- **Delete all journal-only rows**: hides potentially live authority.
- **Treat an `rm-*` instance as disposable**: relies on a naming convention
  rather than authorization.

## Decision 7: Evidence Is Strict And Exact-Package Bound

**Decision**: Register 042 Gate 0, real recovery, supporting not-run, and docs
proofs. Real evidence must bind a clean source/package/runtime, macOS arm64
Lima, a closed check inventory, ordinary and crash recovery, negative
destruction counts, 30 residue-free runs, `--rm --ephemeral`, redaction, and
record/journal/backend inventory agreement.

**Rationale**: Local fault injection proves state-machine mechanics but not
actual Lima deletion, process death, inventory convergence, or restart
behavior. A strict Go judge prevents edited or partial JSON from promoting the
claim.

**Alternatives considered**:

- **Use aggregate Gate 2 output alone**: it lacks the complete crash and intent
  artifact contract.
- **Treat `not-run` as success**: cannot establish real destructive behavior.
